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

// TestRoundtripSubscriptionOwner is the live regression guard for the
// lower-priority fresh-audit finding that ir.Subscription had no Owner
// support at all. Real PostgreSQL supports ALTER SUBSCRIPTION ... OWNER TO.
// connect=false/create_slot=false avoids needing a real replicating
// connection — same reasoning as TestRoundtripSubscriptionRenamedFrom,
// proving the diff/apply contract for Owner, not replication itself.
// Mirrors TestRoundtripForeignServerOwnerAndRenamedFrom's structure: proves
// the post-creation OWNER TO ALTER path (a targeted, metadata-only change —
// stable subscription OID across the change, not dropped and recreated).
func TestRoundtripSubscriptionOwner(t *testing.T) {
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

	if _, err := conn.Exec(ctx, `CREATE PUBLICATION gl_sub_owner_pub FOR ALL TABLES;`); err != nil {
		t.Fatalf("create publication: %v", err)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	const connInfo = "host=127.0.0.1 port=5432 dbname=dpgtest user=dpg password=dpg"

	v1 := "ROLE sub_admin SUPERUSER NOLOGIN;\n\n" +
		"SUBSCRIPTION gl_sub_owner\n" +
		"    CONNECTION '" + connInfo + "'\n" +
		"    PUBLICATION gl_sub_owner_pub\n" +
		"    WITH (connect = false, create_slot = false);\n"
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	subState := func() (bool, string, int64) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT r.rolname, s.oid::bigint FROM pg_subscription s JOIN pg_roles r ON r.oid = s.subowner WHERE s.subname = 'gl_sub_owner'`)
		if err != nil {
			t.Fatalf("query pg_subscription: %v", err)
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

	exists, owner, oldOID := subState()
	if !exists {
		t.Fatal("gl_sub_owner does not exist after initial apply")
	}
	if owner == "sub_admin" {
		t.Fatal("gl_sub_owner already owned by sub_admin before OWNER TO was ever declared — test setup is broken")
	}

	v2 := "ROLE sub_admin SUPERUSER NOLOGIN;\n\n" +
		"SUBSCRIPTION gl_sub_owner\n" +
		"    CONNECTION '" + connInfo + "'\n" +
		"    PUBLICATION gl_sub_owner_pub\n" +
		"    WITH (connect = false, create_slot = false) {\n" +
		"    OWNER sub_admin;\n" +
		"}\n"
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	exists, owner, newOID := subState()
	if !exists {
		t.Fatal("gl_sub_owner does not exist after OWNER TO apply")
	}
	if owner != "sub_admin" {
		t.Fatalf("gl_sub_owner: owner = %q, want sub_admin — OWNER TO not applied", owner)
	}
	if newOID != oldOID {
		t.Fatalf("gl_sub_owner has a different OID (%d) than it had before (%d) — dropped and recreated instead of a targeted ALTER", newOID, oldOID)
	}

	// A second plan against the same declaration must be a no-op — proves
	// introspectSubscriptions now populates Owner and the differ compares
	// it correctly against the declared value.
	assertNoLiveDrift(t, ctx, conn, []string{f}, dir, differ, store)
}
