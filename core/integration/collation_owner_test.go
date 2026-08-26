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

// TestRoundtripCollationOwner is the live regression guard for RFC audit
// item #81: ir.Collation had no Owner field at all — real PostgreSQL
// supports ALTER COLLATION ... OWNER TO, but neither the builder nor the
// differ ever reached it. The collation is first created without OWNER (as
// the connecting superuser) so this only exercises the ALTER OWNER TO path,
// not wrapCreateWithOwner's SET ROLE-then-CREATE path — creating a schema
// object as a plain NOLOGIN role would additionally need CREATE on the
// target schema (revoked from PUBLIC by default since PG15), an unrelated
// prerequisite this test isn't about. Proves the owner change genuinely
// applies live via a metadata-only ALTER (stable collation OID across the
// change, not a drop+recreate).
func TestRoundtripCollationOwner(t *testing.T) {
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

	v1 := `ROLE coll_admin NOLOGIN;

COLLATION c (LOCALE = 'C');`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	collState := func() (string, int64) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT r.rolname, c.oid::bigint FROM pg_collation c JOIN pg_roles r ON r.oid = c.collowner WHERE c.collname = 'c'`)
		if err != nil {
			t.Fatalf("query pg_collation: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("pg_collation has no row for c")
		}
		var owner string
		var oid int64
		if err := rows.Scan(&owner, &oid); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return owner, oid
	}

	owner, oldOID := collState()
	if owner == "coll_admin" {
		t.Fatal("c already owned by coll_admin before OWNER was ever declared — test setup is broken")
	}

	v2 := `ROLE coll_admin NOLOGIN;

COLLATION c (LOCALE = 'C') {
    OWNER coll_admin;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	owner, newOID := collState()
	if owner != "coll_admin" {
		t.Fatalf("c: owner = %q after OWNER change, want coll_admin — bug #81 regressed", owner)
	}
	if newOID != oldOID {
		t.Fatalf("c has a different OID (%d) than before (%d) — dropped and recreated instead of a targeted ALTER", newOID, oldOID)
	}
}
