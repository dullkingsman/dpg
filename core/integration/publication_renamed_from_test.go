//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dpg/internal/diff"
	"github.com/thec1oud/dpg/internal/emit"
	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestRoundtripPublicationRenamedFrom is the regression guard for RFC audit
// item #78: ir.Publication had no RenamedFrom field at all, so a renamed
// publication was indistinguishable from "old publication dropped, new one
// added" — a real DROP+CREATE PUBLICATION, losing and rebuilding replication
// state unnecessarily. This proves the rename is a targeted, metadata-only
// ALTER PUBLICATION ... RENAME TO (stable publication OID across the
// rename, not dropped and recreated).
func TestRoundtripPublicationRenamedFrom(t *testing.T) {
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

	v1 := `PUBLICATION pub_old FOR ALL TABLES;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	pubState := func(name string) (bool, int64) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT oid::bigint FROM pg_publication WHERE pubname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_publication for %s: %v", name, err)
		}
		defer rows.Close()
		if !rows.Next() {
			return false, 0
		}
		var oid int64
		if err := rows.Scan(&oid); err != nil {
			t.Fatalf("scan oid: %v", err)
		}
		return true, oid
	}

	exists, oldOID := pubState("pub_old")
	if !exists {
		t.Fatal("pub_old does not exist after initial apply")
	}

	v2 := `PUBLICATION pub_new FOR ALL TABLES {
    RENAMED FROM pub_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if exists, _ := pubState("pub_old"); exists {
		t.Fatal("pub_old still exists after rename")
	}
	exists, newOID := pubState("pub_new")
	if !exists {
		t.Fatal("pub_new does not exist after rename")
	}
	if newOID != oldOID {
		t.Fatalf("pub_new has a different OID (%d) than pub_old had (%d) — dropped and recreated instead of a targeted ALTER", newOID, oldOID)
	}
}
