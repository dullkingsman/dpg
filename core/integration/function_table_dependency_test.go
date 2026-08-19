//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestApplyFunctionBodyReferencesTableOrdering is the live-database
// regression guard for RFC §22.1 item 9 (audit item #30): a function body's
// static table references now create real dependency-graph edges, so a
// function that references a table it needs must be ordered after that
// table exists — function/procedure bodies were previously completely
// opaque to the dependency graph.
//
// The function is declared BEFORE the table in source text on purpose:
// with no dependency edge at all, the resolver's tie-break (original source
// order, per graph.go's Kahn's-algorithm queue) would create the function
// first, exactly the wrong order. A LANGUAGE sql body is used deliberately
// (not plpgsql) because real PostgreSQL validates a LANGUAGE sql body's
// referenced relations immediately at CREATE FUNCTION time — so applying in
// the wrong order fails loudly and immediately with a live "relation ...
// does not exist" error, rather than only failing later when the function
// is actually called (plpgsql's lazy compilation would hide a wrong order
// here).
func TestApplyFunctionBodyReferencesTableOrdering(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.Start(t)

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()
	store := newMemStore()

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	src := `FUNCTION log_event() RETURNS void LANGUAGE sql AS $$
    INSERT INTO events (msg) VALUES ('created')
$$ {}

TABLE events (
    id bigint PRIMARY KEY,
    msg text
) {}`
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	rows, err := conn.QueryRows(ctx, `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'log_event')`)
	if err != nil {
		t.Fatalf("query pg_proc: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("existence check returned no row")
	}
	var exists bool
	if err := rows.Scan(&exists); err != nil {
		t.Fatalf("scan exists: %v", err)
	}
	if !exists {
		t.Fatal("log_event was not created")
	}
}

// TestApplyTriggerFunctionSelfReferenceNoSpuriousCycle is the live-database
// proof of the specific regression the RFC §22.1 item 9 design conversation
// identified when weighing a cheaper "function depends on all tables"
// heuristic against the real AST walk actually implemented: combined with
// the pre-existing table→trigger-function edge (edge source 6 — a trigger's
// EXECUTE FUNCTION target must exist first), a blunt heuristic would
// manufacture a 2-node cycle for every trigger-bearing table (the heuristic
// always includes the trigger's own table), and §22.2's cycle-breaker has no
// mechanism for a function↔table cycle — only FK cycles. A real AST walk
// doesn't have this problem for the overwhelmingly common case of a trigger
// function that only touches NEW/OLD and never references its own table in
// SQL. Applies (and re-applies, forcing a real plan/diff/apply cycle, not
// just a one-shot create) a table with exactly this shape, live.
func TestApplyTriggerFunctionSelfReferenceNoSpuriousCycle(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.Start(t)

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()
	store := newMemStore()

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	src := `FUNCTION touch_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ {}

TABLE widgets (
    id bigint PRIMARY KEY,
    updated_at timestamptz NOT NULL DEFAULT now()
) {
    TRIGGER trg_touch BEFORE UPDATE FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
}`
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, err := conn.Exec(ctx, `INSERT INTO widgets (id) VALUES (1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := conn.Exec(ctx, `UPDATE widgets SET id = id WHERE id = 1`); err != nil {
		t.Fatalf("update: %v", err)
	}

	// A no-op second plan/diff pass (re-compile, re-diff against the saved
	// snapshot) must also stay cycle-free — the real regression risk is at
	// graph-build time on every plan/apply, not just the first one.
	desired, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("recompile: %v", err)
	}
	prevSnap, _ := store.Load("test", "dpgtest")
	if _, err := differ.Diff(desired, prevSnap); err != nil {
		t.Fatalf("re-diff must not error (spurious function<->table cycle): %v", err)
	}
}
