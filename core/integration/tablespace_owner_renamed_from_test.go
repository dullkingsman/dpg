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

// TestRoundtripTablespaceOwnerAndRenamedFrom is the live regression guard
// for RFC audit item #80: ir.Tablespace's inline OWNER clause (valid real
// PostgreSQL CREATE TABLESPACE grammar) was discarded entirely at build
// time — never even reaching the IR, let alone being diffed — and
// RenamedFrom didn't exist at all. Tablespaces are cluster-level, not
// schema-scoped, so this reuses the same bare rename mechanism as
// Role/Publication/ForeignServer. Proves: (a) a declared owner genuinely
// applies at CREATE time (not just diffed later), (b) changing it emits a
// real ALTER TABLESPACE ... OWNER TO, and (c) the rename is a targeted,
// metadata-only ALTER (stable tablespace OID across the rename).
func TestRoundtripTablespaceOwnerAndRenamedFrom(t *testing.T) {
	connStr, container := testpg.StartWithContainer(t)
	ctx := context.Background()

	if code, _, err := container.Exec(ctx, []string{"mkdir", "-p", "/var/lib/postgresql/ts_owner"}); err != nil || code != 0 {
		t.Fatalf("mkdir in container: code=%d err=%v", code, err)
	}
	if code, _, err := container.Exec(ctx, []string{"chown", "postgres:postgres", "/var/lib/postgresql/ts_owner"}); err != nil || code != 0 {
		t.Fatalf("chown in container: code=%d err=%v", code, err)
	}

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

	v1 := `ROLE ts_admin NOLOGIN;
ROLE ts_admin2 NOLOGIN;

TABLESPACE ts_old OWNER ts_admin LOCATION '/var/lib/postgresql/ts_owner';`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	tsState := func(name string) (bool, string, int64) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT r.rolname, t.oid::bigint FROM pg_tablespace t JOIN pg_roles r ON r.oid = t.spcowner WHERE t.spcname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_tablespace for %s: %v", name, err)
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

	exists, owner, oldOID := tsState("ts_old")
	if !exists {
		t.Fatal("ts_old does not exist after initial apply")
	}
	if owner != "ts_admin" {
		t.Fatalf("ts_old: owner = %q after CREATE TABLESPACE ... OWNER ts_admin, want ts_admin — bug #80 regressed (OWNER discarded at CREATE time)", owner)
	}

	v2 := `ROLE ts_admin NOLOGIN;
ROLE ts_admin2 NOLOGIN;

TABLESPACE ts_new OWNER ts_admin2 LOCATION '/var/lib/postgresql/ts_owner' {
    RENAMED FROM ts_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if exists, _, _ := tsState("ts_old"); exists {
		t.Fatal("ts_old still exists after rename")
	}
	exists, owner, newOID := tsState("ts_new")
	if !exists {
		t.Fatal("ts_new does not exist after rename")
	}
	if owner != "ts_admin2" {
		t.Fatalf("ts_new: owner = %q, want ts_admin2 — bug #80 regressed (OWNER TO not applied)", owner)
	}
	if newOID != oldOID {
		t.Fatalf("ts_new has a different OID (%d) than ts_old had (%d) — dropped and recreated instead of a targeted ALTER", newOID, oldOID)
	}
}
