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

// TestRoundtripFunctionDependsOnExtension is the live regression guard for
// RFC audit item #71: Section 9.1's [NO] DEPENDS ON EXTENSION func-block
// directive, previously zero code anywhere. Proves live that a declared
// dependency actually takes effect (via pg_depend deptype='x') and that
// dropping the extension it depends on auto-drops the function too — the
// entire point of the mechanism.
func TestRoundtripFunctionDependsOnExtension(t *testing.T) {
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

	v1 := `FUNCTION uses_crypto() RETURNS text LANGUAGE sql AS $$ SELECT encode(digest('x', 'sha256'), 'hex') $$ {
    DEPENDS ON EXTENSION pgcrypto;
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	hasDep := func() bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_depend d JOIN pg_proc p ON p.oid = d.objid JOIN pg_extension e ON e.oid = d.refobjid WHERE p.proname = 'uses_crypto' AND d.deptype = 'x' AND e.extname = 'pgcrypto'`)
		if err != nil {
			t.Fatalf("query pg_depend: %v", err)
		}
		defer rows.Close()
		var n int
		rows.Next()
		_ = rows.Scan(&n)
		return n == 1
	}
	if !hasDep() {
		t.Fatalf("expected a deptype='x' dependency on pgcrypto after applying DEPENDS ON EXTENSION")
	}

	// The entire point of the mechanism: dropping the extension should
	// auto-drop the function too.
	if _, err := conn.Exec(ctx, `DROP EXTENSION pgcrypto CASCADE;`); err != nil {
		t.Fatalf("drop extension cascade: %v", err)
	}
	rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_proc WHERE proname = 'uses_crypto'`)
	if err != nil {
		t.Fatalf("query pg_proc: %v", err)
	}
	defer rows.Close()
	var n int
	rows.Next()
	_ = rows.Scan(&n)
	if n != 0 {
		t.Fatalf("expected uses_crypto to be auto-dropped when pgcrypto was dropped, but it still exists")
	}
}

// TestRoundtripTriggerDependsOnExtension is the live regression guard for
// RFC audit item #75: the same directive reused verbatim for triggers.
func TestRoundtripTriggerDependsOnExtension(t *testing.T) {
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

	v1 := `FUNCTION trg_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END; $$ {}

TABLE t (id INTEGER) {
    TRIGGER trg_a AFTER INSERT
        FOR EACH ROW
        EXECUTE FUNCTION trg_fn()
        DEPENDS ON EXTENSION pgcrypto;
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_depend d JOIN pg_trigger tg ON tg.oid = d.objid JOIN pg_extension e ON e.oid = d.refobjid WHERE tg.tgname = 'trg_a' AND d.deptype = 'x' AND e.extname = 'pgcrypto'`)
	if err != nil {
		t.Fatalf("query pg_depend: %v", err)
	}
	defer rows.Close()
	var n int
	rows.Next()
	_ = rows.Scan(&n)
	if n != 1 {
		t.Fatalf("expected a deptype='x' dependency on pgcrypto for trg_a after applying DEPENDS ON EXTENSION, got count %d", n)
	}
}
