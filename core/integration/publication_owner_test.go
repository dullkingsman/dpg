//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thec1oud/dpg/internal/compiler"
	"github.com/thec1oud/dpg/internal/diff"
	"github.com/thec1oud/dpg/internal/emit"
	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/pipeline"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestRoundtripPublicationOwner is the regression guard for RFC audit item
// #7: Publication had no Owner field at all, even though real PostgreSQL
// supports ALTER PUBLICATION ... OWNER TO. This proves against a real
// database that a declared OWNER actually applies, that changing it emits
// and applies a real ALTER PUBLICATION ... OWNER TO, and that the resulting
// state round-trips through the snapshot with zero drift.
func TestRoundtripPublicationOwner(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()
	store := newMemStore()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	liveOwner := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT pg_get_userbyid(pubowner) FROM pg_publication WHERE pubname = 'pub_all'`)
		if err != nil {
			t.Fatalf("query pg_publication: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("pg_publication has no row for pub_all")
		}
		var owner string
		if err := rows.Scan(&owner); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return owner
	}

	v1 := `ROLE app_admin SUPERUSER LOGIN;

PUBLICATION pub_all FOR ALL TABLES {
    OWNER app_admin;
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := liveOwner(); got != "app_admin" {
		t.Fatalf("pub_all: live owner = %q after initial apply, want app_admin — bug #7 regressed (OWNER never applied)", got)
	}

	v2 := `ROLE app_admin SUPERUSER LOGIN;
ROLE app_admin2 SUPERUSER LOGIN;

PUBLICATION pub_all FOR ALL TABLES {
    OWNER app_admin2;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	desired2, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile v2: %v", err)
	}
	prevSnap, _ := store.Load("test", "dpgtest")
	ops, err := differ.Diff(desired2, prevSnap)
	if err != nil {
		t.Fatalf("diff (owner change): %v", err)
	}
	var sawOwnerChange bool
	for _, op := range ops {
		if strings.Contains(op.SQL(), `ALTER PUBLICATION "pub_all" OWNER TO "app_admin2";`) {
			sawOwnerChange = true
		}
	}
	if !sawOwnerChange {
		t.Fatalf("expected ALTER PUBLICATION ... OWNER TO, got: %v", opsSQL(ops))
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := liveOwner(); got != "app_admin2" {
		t.Fatalf("pub_all: live owner = %q after OWNER change — bug #7 regressed (drift invisible)", got)
	}

	newSnap, _ := store.Load("test", "dpgtest")
	noDriftOps, err := differ.Diff(desired2, newSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(noDriftOps) != 0 {
		t.Errorf("expected zero drift after the owner change, got %d ops:", len(noDriftOps))
		for _, op := range noDriftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}
}
