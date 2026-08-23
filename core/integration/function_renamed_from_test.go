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

// TestRoundtripFunctionRenamedFrom is the regression guard for two bugs found
// together while auditing renamedFromKey's Function case: (1) it never
// included the function's arg-types signature, unlike its Procedure/
// Aggregate siblings — ir.Function.QualifiedName() (the actual snapshot key)
// always includes "(args)", so RENAMED FROM could never match ANY existing
// snapshot entry for a function with at least one argument, and (2)
// diffFunction had no rename-emission branch at all, so even a matched
// rename (a zero-arg function, where the missing "(args)" suffix happens to
// be "()" either way and so still needs to match) was a silent no-op live —
// unlike diffProcedure/diffAggregate (RFC audit items #10/#11), which
// genuinely act on the match. Proves both are fixed: the function is
// recognized as the same object (not DROP+CREATE'd) and is actually renamed
// live via ALTER FUNCTION ... RENAME TO.
func TestRoundtripFunctionRenamedFrom(t *testing.T) {
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

	v1 := `FUNCTION calc_total(n INTEGER) RETURNS INTEGER LANGUAGE plpgsql AS $$
BEGIN
    RETURN n * 2;
END;
$$ {}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	countFunc := func(name string) int {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_proc WHERE proname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_proc for %s: %v", name, err)
		}
		defer rows.Close()
		var n int
		rows.Next()
		_ = rows.Scan(&n)
		return n
	}
	if countFunc("calc_total") != 1 {
		t.Fatalf("calc_total does not exist after initial apply")
	}

	v2 := `FUNCTION calc_total_v2(n INTEGER) RETURNS INTEGER LANGUAGE plpgsql AS $$
BEGIN
    RETURN n * 2;
END;
$$ {
    RENAMED FROM calc_total;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if countFunc("calc_total") != 0 {
		t.Fatalf("calc_total still exists live after rename — the rename didn't land (identity match or emission regressed)")
	}
	if countFunc("calc_total_v2") != 1 {
		t.Fatalf("calc_total_v2 doesn't exist live after rename — expected exactly one match")
	}
}
