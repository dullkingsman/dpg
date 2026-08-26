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

// TestRoundtripTriggerRenamedFrom is the live regression guard for RFC
// Section 7.9's trigger RENAMED FROM clause: ir.Trigger had no such field
// at all, the blockparser never recognized it inside a TRIGGERS block, and
// diffTriggers had no rename-detection logic — despite the RFC documenting
// it as fully implemented (grammar, a diffing-table row, Section 25's
// coverage matrix, and Appendix E's E.19 entry explicitly claiming it was
// "verified against PostgreSQL's own official documentation before
// drafting, not assumed"). A renamed trigger previously misdiffed as a
// drop of the old name plus a create of the new one instead of a real
// ALTER TRIGGER ... RENAME TO.
//
// Confirms live: renaming a trigger runs a real ALTER TRIGGER ... RENAME TO
// (stable trigger OID across the rename, not dropped and recreated), and a
// second plan against freshly introspected live state is a genuine no-op.
func TestRoundtripTriggerRenamedFrom(t *testing.T) {
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

	triggerOID := func(name string) (bool, int64) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
SELECT tg.oid::bigint FROM pg_trigger tg
JOIN pg_class c ON c.oid = tg.tgrelid
WHERE c.relname = 't' AND tg.tgname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_trigger for %s: %v", name, err)
		}
		defer rows.Close()
		if !rows.Next() {
			return false, 0
		}
		var oid int64
		if err := rows.Scan(&oid); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return true, oid
	}

	v1 := `FUNCTION trg_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.n := NEW.n + 1; RETURN NEW; END; $$ {}

TABLE t (id INTEGER, n INTEGER DEFAULT 0) {
    TRIGGER trg_a BEFORE INSERT
        FOR EACH ROW
        EXECUTE FUNCTION trg_fn();
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	exists, oldOID := triggerOID("trg_a")
	if !exists {
		t.Fatal("trg_a does not exist after initial apply")
	}

	v2 := `FUNCTION trg_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.n := NEW.n + 1; RETURN NEW; END; $$ {}

TABLE t (id INTEGER, n INTEGER DEFAULT 0) {
    TRIGGER trg_b BEFORE INSERT
        FOR EACH ROW
        EXECUTE FUNCTION trg_fn()
        RENAMED FROM trg_a;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if exists, _ := triggerOID("trg_a"); exists {
		t.Fatal("trg_a still exists after rename")
	}
	exists, newOID := triggerOID("trg_b")
	if !exists {
		t.Fatal("trg_b does not exist after rename")
	}
	if newOID != oldOID {
		t.Fatalf("trg_b has a different OID (%d) than trg_a had (%d) — dropped and recreated instead of a targeted ALTER TRIGGER RENAME TO", newOID, oldOID)
	}

	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}

// TestTriggerRenamedFromWithDependsOnExtension confirms the RFC's ordering
// rule (Section 7.9): real PostgreSQL's ALTER TRIGGER cannot combine
// RENAME TO with DEPENDS ON EXTENSION in one statement, so when both change
// at once the compiler must emit RENAME TO first (referencing the old
// name), then the DEPENDS ON EXTENSION change as a second statement
// referencing the new name.
func TestTriggerRenamedFromWithDependsOnExtension(t *testing.T) {
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

	if _, err := conn.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto;`); err != nil {
		t.Fatalf("create extension: %v", err)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	v1 := `FUNCTION trg_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.n := NEW.n + 1; RETURN NEW; END; $$ {}

TABLE t (id INTEGER, n INTEGER DEFAULT 0) {
    TRIGGER trg_a BEFORE INSERT
        FOR EACH ROW
        EXECUTE FUNCTION trg_fn();
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	v2 := `FUNCTION trg_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.n := NEW.n + 1; RETURN NEW; END; $$ {}

TABLE t (id INTEGER, n INTEGER DEFAULT 0) {
    TRIGGER trg_b BEFORE INSERT
        FOR EACH ROW
        EXECUTE FUNCTION trg_fn()
        DEPENDS ON EXTENSION pgcrypto
        RENAMED FROM trg_a;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	rows, err := conn.QueryRows(ctx, `
SELECT count(*) FROM pg_depend d
JOIN pg_trigger tg ON tg.oid = d.objid
JOIN pg_class c ON c.oid = tg.tgrelid
JOIN pg_extension e ON e.oid = d.refobjid
WHERE c.relname = 't' AND tg.tgname = 'trg_b' AND e.extname = 'pgcrypto' AND d.deptype = 'x'`)
	if err != nil {
		t.Fatalf("query pg_depend: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("no rows from pg_depend query")
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		rows.Close()
		t.Fatalf("scan: %v", err)
	}
	rows.Close()
	if count != 1 {
		t.Fatalf("expected trg_b to depend on extension pgcrypto after combined rename+depends-on-extension change, got count=%d", count)
	}

	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
