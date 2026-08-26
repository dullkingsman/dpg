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

// TestRoundtripTableUnloggedToggle is the live regression guard for RFC
// Section 7.12's LOGGED/UNLOGGED toggle: diffTable had zero handling for
// Unlogged at all before this — createTable read it at CREATE time, but
// changing it on an already-applied table was a silent no-op, despite the
// RFC's own E.13 changelog entry claiming this was already "folded in" as
// a DESTRUCTIVE-to-safe-ALTER swap.
//
// Confirms live: a declared UNLOGGED TABLE lands with pg_class.
// relpersistence = 'u', a second plan against freshly introspected live
// state is a genuine no-op, toggling it runs a real ALTER TABLE ... SET
// LOGGED/UNLOGGED (not a drop+recreate) in both directions, and the
// table's rows survive the toggle (proving it's a rewrite in place, not a
// hidden recreate that would lose data).
func TestRoundtripTableUnloggedToggle(t *testing.T) {
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

	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
			t.Fatalf("write schema: %v", err)
		}
	}

	relpersistence := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT relpersistence::text FROM pg_class WHERE relname = 't'`)
		if err != nil {
			t.Fatalf("query relpersistence: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("t not found")
		}
		var p string
		_ = rows.Scan(&p)
		return p
	}

	write(`UNLOGGED TABLE t (id INTEGER NOT NULL);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := relpersistence(); got != "u" {
		t.Fatalf("relpersistence after create: got %q, want %q", got, "u")
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	if _, err := conn.Exec(ctx, `INSERT INTO t (id) VALUES (1), (2);`); err != nil {
		t.Fatalf("insert rows: %v", err)
	}

	write(`TABLE t (id INTEGER NOT NULL);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := relpersistence(); got != "p" {
		t.Fatalf("relpersistence after SET LOGGED: got %q, want %q", got, "p")
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	rowCount := func() int {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM t`)
		if err != nil {
			t.Fatalf("query row count: %v", err)
		}
		defer rows.Close()
		rows.Next()
		var n int
		_ = rows.Scan(&n)
		return n
	}
	if n := rowCount(); n != 2 {
		t.Fatalf("row count after SET LOGGED: got %d, want 2 (toggle must not lose data)", n)
	}

	write(`UNLOGGED TABLE t (id INTEGER NOT NULL);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := relpersistence(); got != "u" {
		t.Fatalf("relpersistence after SET UNLOGGED: got %q, want %q", got, "u")
	}
	if n := rowCount(); n != 2 {
		t.Fatalf("row count after SET UNLOGGED: got %d, want 2 (toggle must not lose data)", n)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
