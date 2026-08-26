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

// TestRoundtripForeignServerOwnerAndRenamedFrom is the live regression guard
// for RFC audit item #79: ir.ForeignServer had neither an Owner nor a
// RenamedFrom field at all — real PostgreSQL supports both
// ALTER SERVER ... OWNER TO and ALTER SERVER ... RENAME TO, but neither
// mechanism reached the differ. Foreign servers are cluster-level, not
// schema-scoped, so this reuses the same bare rename mechanism as
// Role/Publication. Proves: (a) the declared owner change genuinely applies
// live, and (b) the rename is a targeted, metadata-only ALTER (stable
// server OID across the rename, not dropped and recreated).
func TestRoundtripForeignServerOwnerAndRenamedFrom(t *testing.T) {
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

	v1 := `ROLE srv_admin NOLOGIN;

EXTENSION postgres_fdw;

SERVER srv_old FOREIGN DATA WRAPPER postgres_fdw;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	srvState := func(name string) (bool, string, int64) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT r.rolname, s.oid::bigint FROM pg_foreign_server s JOIN pg_roles r ON r.oid = s.srvowner WHERE s.srvname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_foreign_server for %s: %v", name, err)
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

	exists, owner, oldOID := srvState("srv_old")
	if !exists {
		t.Fatal("srv_old does not exist after initial apply")
	}
	if owner == "srv_admin" {
		t.Fatal("srv_old already owned by srv_admin before OWNER TO was ever declared — test setup is broken")
	}

	v2 := `ROLE srv_admin NOLOGIN;

EXTENSION postgres_fdw;

SERVER srv_new FOREIGN DATA WRAPPER postgres_fdw {
    OWNER srv_admin;
    RENAMED FROM srv_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if exists, _, _ := srvState("srv_old"); exists {
		t.Fatal("srv_old still exists after rename")
	}
	exists, owner, newOID := srvState("srv_new")
	if !exists {
		t.Fatal("srv_new does not exist after rename")
	}
	if owner != "srv_admin" {
		t.Fatalf("srv_new: owner = %q, want srv_admin — bug #79 regressed (OWNER TO not applied)", owner)
	}
	if newOID != oldOID {
		t.Fatalf("srv_new has a different OID (%d) than srv_old had (%d) — dropped and recreated instead of a targeted ALTER", newOID, oldOID)
	}
}
