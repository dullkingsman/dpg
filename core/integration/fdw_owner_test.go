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

// TestRoundtripForeignDataWrapperOwner is the live regression guard for the
// lower-priority fresh-audit finding that ir.ForeignDataWrapper had no
// Owner support at all, unlike ForeignServer (its closest sibling opaque
// kind, which already has Owner — RFC audit item #79). Real PostgreSQL
// supports ALTER FOREIGN DATA WRAPPER ... OWNER TO (superuser-only, same
// restriction as CREATE FOREIGN DATA WRAPPER itself). Mirrors
// TestRoundtripForeignServerOwnerAndRenamedFrom's structure: proves the
// post-creation OWNER TO ALTER path (a targeted, metadata-only change —
// stable FDW OID across the change, not dropped and recreated), the same
// scope that test exercises for ForeignServer.
func TestRoundtripForeignDataWrapperOwner(t *testing.T) {
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

	v1 := `ROLE fdw_admin SUPERUSER NOLOGIN;

FOREIGN DATA WRAPPER gl_fdw_owner;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	fdwState := func() (bool, string, int64) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT r.rolname, f.oid::bigint FROM pg_foreign_data_wrapper f JOIN pg_roles r ON r.oid = f.fdwowner WHERE f.fdwname = 'gl_fdw_owner'`)
		if err != nil {
			t.Fatalf("query pg_foreign_data_wrapper: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			return false, "", 0
		}
		var owner string
		var oid int64
		if err := rows.Scan(&owner, &oid); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return true, owner, oid
	}

	exists, owner, oldOID := fdwState()
	if !exists {
		t.Fatal("gl_fdw_owner does not exist after initial apply")
	}
	if owner == "fdw_admin" {
		t.Fatal("gl_fdw_owner already owned by fdw_admin before OWNER TO was ever declared — test setup is broken")
	}

	v2 := `ROLE fdw_admin SUPERUSER NOLOGIN;

FOREIGN DATA WRAPPER gl_fdw_owner {
    OWNER fdw_admin;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	exists, owner, newOID := fdwState()
	if !exists {
		t.Fatal("gl_fdw_owner does not exist after OWNER TO apply")
	}
	if owner != "fdw_admin" {
		t.Fatalf("gl_fdw_owner: owner = %q, want fdw_admin — OWNER TO not applied", owner)
	}
	if newOID != oldOID {
		t.Fatalf("gl_fdw_owner has a different OID (%d) than it had before (%d) — dropped and recreated instead of a targeted ALTER", newOID, oldOID)
	}

	// A second plan against the same declaration must be a no-op — proves
	// introspectForeignDataWrappers now populates Owner and the differ
	// compares it correctly against the declared value.
	assertNoLiveDrift(t, ctx, conn, []string{f}, dir, differ, store)
}
