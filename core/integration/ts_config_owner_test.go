//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripTSConfigOwner is the live regression guard for RFC audit
// item #33 (Section 12.1's OWNER TO capability): previously ir.TSConfig had
// no Owner field at all, so a declared owner had zero effect on the live
// catalog.
func TestRoundtripTSConfigOwner(t *testing.T) {
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

	if _, err := conn.Exec(ctx, `CREATE ROLE search_admin NOLOGIN;`); err != nil {
		t.Fatalf("create role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, `DROP ROLE IF EXISTS search_admin;`)
	})

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	v1 := `TEXT SEARCH CONFIGURATION my_cfg (PARSER = pg_catalog."default");`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	ownerOf := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT r.rolname FROM pg_ts_config c JOIN pg_roles r ON r.oid = c.cfgowner WHERE c.cfgname = 'my_cfg'`)
		if err != nil {
			t.Fatalf("query owner: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("my_cfg does not exist")
		}
		var owner string
		_ = rows.Scan(&owner)
		return owner
	}

	if got := ownerOf(); got == "search_admin" {
		t.Fatalf("search_admin should not already own my_cfg before OWNER TO is applied")
	}

	v2 := `TEXT SEARCH CONFIGURATION my_cfg (PARSER = pg_catalog."default") {
    OWNER "search_admin";
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := ownerOf(); got != "search_admin" {
		t.Fatalf("expected search_admin to own my_cfg after OWNER TO, got %q", got)
	}

	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
