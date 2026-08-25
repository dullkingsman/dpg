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

// TestRoundtripSequenceUnlogged is the live regression guard for RFC
// Section 10's UNLOGGED prefix: ir.Sequence had no Unlogged field at all
// before this, so a declared UNLOGGED SEQUENCE either failed to parse (the
// scanner rejected any keyword after UNLOGGED other than TABLE) or, had it
// parsed, would have silently built as a logged sequence.
//
// Confirms live: a declared UNLOGGED SEQUENCE lands with
// pg_class.relpersistence = 'u', a second plan against freshly introspected
// live state is a genuine no-op, toggling it runs a real ALTER SEQUENCE
// ... SET LOGGED/UNLOGGED (not a drop+recreate) in both directions, and the
// sequence's current value survives the toggle (proving it's a metadata-
// only ALTER, not a hidden recreate).
func TestRoundtripSequenceUnlogged(t *testing.T) {
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
		rows, err := conn.QueryRows(ctx, `SELECT relpersistence::text FROM pg_class WHERE relname = 'order_seq'`)
		if err != nil {
			t.Fatalf("query relpersistence: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("order_seq not found")
		}
		var p string
		_ = rows.Scan(&p)
		return p
	}

	write(`UNLOGGED SEQUENCE order_seq;`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := relpersistence(); got != "u" {
		t.Fatalf("relpersistence after create: got %q, want %q", got, "u")
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	if _, err := conn.Exec(ctx, `SELECT nextval('order_seq');`); err != nil {
		t.Fatalf("nextval: %v", err)
	}

	write(`SEQUENCE order_seq;`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := relpersistence(); got != "p" {
		t.Fatalf("relpersistence after SET LOGGED: got %q, want %q", got, "p")
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	rows, err := conn.QueryRows(ctx, `SELECT last_value FROM order_seq;`)
	if err != nil {
		t.Fatalf("query last_value: %v", err)
	}
	var lastValue int64
	rows.Next()
	_ = rows.Scan(&lastValue)
	rows.Close()
	if lastValue != 1 {
		t.Fatalf("last_value after toggle: got %d, want 1 (toggle must not reset the sequence)", lastValue)
	}

	write(`UNLOGGED SEQUENCE order_seq;`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := relpersistence(); got != "u" {
		t.Fatalf("relpersistence after SET UNLOGGED: got %q, want %q", got, "u")
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
