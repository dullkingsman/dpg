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
	"github.com/dullkingsman/dpg/internal/introspect"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripRecursiveViewApplies is the regression guard for RFC audit
// item #27: buildView's caller in ir.Build hardcoded recursive=false
// unconditionally, and pgparser.Parse never populated PGParseResult.Kind at
// all (a second, deeper bug found while fixing #27 — Kind was always its
// zero value, KindUnknown, on every non-passthrough path), so
// ir.View.Recursive could never be true via real compilation regardless of
// what the scanner detected. This proves a RECURSIVE VIEW declaration
// actually applies against a real database and round-trips through a fresh
// introspect pass with zero drift.
func TestRoundtripRecursiveViewApplies(t *testing.T) {
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

	v1 := `RECURSIVE VIEW countdown(n) AS (
    VALUES (10)
    UNION ALL
    SELECT n - 1 FROM countdown WHERE n > 1
);`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}

	// Directly discriminates bug #27's exact defect: buildView's caller
	// hardcoded recursive=false, so the *compiled IR itself* (real scanner
	// -> parser -> builder pipeline, not hand-built) never carried
	// Recursive = true regardless of what got applied — the live apply
	// below can succeed either way (a plain "CREATE VIEW ... AS WITH
	// RECURSIVE ..." is valid SQL too), so this check has to happen before
	// that, on the IR itself.
	compiledV1, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile v1: %v", err)
	}
	var compiledView *ir.View
	for _, obj := range compiledV1 {
		if v, ok := obj.(*ir.View); ok && v.Name == "countdown" {
			compiledView = v
		}
	}
	if compiledView == nil {
		t.Fatal("compiled IR has no countdown view")
	}
	if !compiledView.Recursive {
		t.Fatal("compiled countdown view has Recursive = false — bug #27 regressed (buildView never wired the RECURSIVE flag through)")
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM countdown`)
	if err != nil {
		t.Fatalf("query countdown: %v", err)
	}
	if !rows.Next() {
		t.Fatal("countdown view returned no row")
	}
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows.Close()
	if n != 10 {
		t.Fatalf("countdown: got %d rows, want 10 — RECURSIVE VIEW did not apply correctly", n)
	}

	// A follow-up diff against the freshly-applied snapshot must be a
	// genuine no-op — proves Recursive round-trips through the snapshot,
	// not just that CREATE succeeded once.
	newSnap, _ := store.Load("test", "dpgtest")
	desired, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	noDriftOps, err := differ.Diff(desired, newSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(noDriftOps) != 0 {
		t.Errorf("expected zero drift for an unchanged RECURSIVE VIEW, got %d ops:", len(noDriftOps))
		for _, op := range noDriftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}

	// Independent live-catalog corroboration via a fresh introspect pass:
	// confirms the live view is genuinely detected as recursive (not just
	// that the offline snapshot round-trip happens to agree with itself).
	// This deliberately does NOT assert zero drift against the full
	// introspected object: PostgreSQL's own pg_get_viewdef rewrites a
	// recursive CTE's self-reference with a disambiguating alias (confirmed
	// live: "FROM countdown" becomes "FROM countdown countdown_1") for
	// every recursive view, self-referencing or not — a pre-existing,
	// general view-query-text-normalization gap unrelated to bug #27 (the
	// Recursive flag itself, which this test does assert on).
	ci := introspect.New()
	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var liveView *ir.View
	for _, obj := range liveObjects {
		if v, ok := obj.(*ir.View); ok && v.Name == "countdown" {
			liveView = v
		}
	}
	if liveView == nil {
		t.Fatal("introspect did not return countdown view")
	}
	if !liveView.Recursive {
		t.Fatal("introspected countdown view has Recursive = false — bug #27 regressed on the introspection side")
	}
}
