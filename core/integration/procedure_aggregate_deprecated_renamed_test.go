//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripProcedureDeprecatedAndRenamed is the regression guard for RFC
// audit items #8/#10: PROCEDURE { DEPRECATED '...'; } and
// { RENAMED FROM ...; } were silently discarded — no field existed on
// ir.Procedure at all, so a declared DEPRECATED comment never reached the
// database and a procedure rename was treated as an unrelated DROP+CREATE
// instead of ALTER PROCEDURE ... RENAME TO. This proves both against a real
// database.
func TestRoundtripProcedureDeprecatedAndRenamed(t *testing.T) {
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

	body := `PROCEDURE recalc_totals() LANGUAGE plpgsql AS $$
BEGIN
    NULL;
END;
$$ {
    DEPRECATED 'use recalc_totals_v2 instead';
}`
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	liveComment := func(procName string) string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT obj_description(p.oid, 'pg_proc') FROM pg_proc p WHERE p.proname = $1`, procName)
		if err != nil {
			t.Fatalf("query pg_proc comment: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("pg_proc has no row for %s", procName)
		}
		var c *string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if c == nil {
			return ""
		}
		return *c
	}
	if got := liveComment("recalc_totals"); !strings.Contains(got, "[DEPRECATED] use recalc_totals_v2 instead") {
		t.Fatalf("recalc_totals: live comment = %q, want [DEPRECATED] marker — bug #8 regressed", got)
	}

	// Now rename it.
	v2 := `PROCEDURE recalc_totals_v2() LANGUAGE plpgsql AS $$
BEGIN
    NULL;
END;
$$ {
    RENAMED FROM recalc_totals;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_proc WHERE proname = 'recalc_totals'`)
	if err != nil {
		t.Fatalf("query pg_proc: %v", err)
	}
	var oldCount int
	rows.Next()
	_ = rows.Scan(&oldCount)
	rows.Close()
	if oldCount != 0 {
		t.Fatalf("recalc_totals still exists live after rename — bug #10 regressed")
	}

	rows, err = conn.QueryRows(ctx, `SELECT count(*) FROM pg_proc WHERE proname = 'recalc_totals_v2'`)
	if err != nil {
		t.Fatalf("query pg_proc: %v", err)
	}
	var newCount int
	rows.Next()
	_ = rows.Scan(&newCount)
	rows.Close()
	if newCount != 1 {
		t.Fatalf("recalc_totals_v2 doesn't exist live after rename — bug #10 regressed (rename didn't land, likely a DROP+CREATE producing wrong final state)")
	}
}

// TestRoundtripAggregateDeprecatedAndRenamed is
// TestRoundtripProcedureDeprecatedAndRenamed's AGGREGATE counterpart (RFC
// audit items #9/#11).
func TestRoundtripAggregateDeprecatedAndRenamed(t *testing.T) {
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

	v1 := `AGGREGATE amount_product (DOUBLE PRECISION) (
    SFUNC = float8mul,
    STYPE = DOUBLE PRECISION,
    INITCOND = '1'
) {
    DEPRECATED 'use amount_product_v2 instead';
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	liveComment := func(name string) string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT obj_description(a.aggfnoid, 'pg_proc') FROM pg_aggregate a JOIN pg_proc p ON p.oid = a.aggfnoid WHERE p.proname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_aggregate comment: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("pg_aggregate has no row for %s", name)
		}
		var c *string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if c == nil {
			return ""
		}
		return *c
	}
	if got := liveComment("amount_product"); !strings.Contains(got, "[DEPRECATED] use amount_product_v2 instead") {
		t.Fatalf("amount_product: live comment = %q, want [DEPRECATED] marker — bug #9 regressed", got)
	}

	v2 := `AGGREGATE amount_product_v2 (DOUBLE PRECISION) (
    SFUNC = float8mul,
    STYPE = DOUBLE PRECISION,
    INITCOND = '1'
) {
    RENAMED FROM amount_product;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	desired2, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile v2: %v", err)
	}
	prevSnap, _ := store.Load("test", "dpgtest")
	ops, err := differ.Diff(desired2, prevSnap)
	if err != nil {
		t.Fatalf("diff (rename): %v", err)
	}
	var sawRename, sawDropCreate bool
	for _, op := range ops {
		if strings.Contains(op.SQL(), "ALTER AGGREGATE") && strings.Contains(op.SQL(), "RENAME TO") {
			sawRename = true
		}
		if strings.Contains(op.SQL(), "DROP AGGREGATE") {
			sawDropCreate = true
		}
	}
	if !sawRename {
		t.Fatalf("expected ALTER AGGREGATE ... RENAME TO, got: %v", opsSQL(ops))
	}
	if sawDropCreate {
		t.Fatalf("rename should not DROP+CREATE, got: %v", opsSQL(ops))
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_proc WHERE proname = 'amount_product'`)
	if err != nil {
		t.Fatalf("query pg_proc: %v", err)
	}
	var oldCount int
	rows.Next()
	_ = rows.Scan(&oldCount)
	rows.Close()
	if oldCount != 0 {
		t.Fatalf("amount_product still exists live after rename — bug #11 regressed")
	}

	rows, err = conn.QueryRows(ctx, `SELECT count(*) FROM pg_proc WHERE proname = 'amount_product_v2'`)
	if err != nil {
		t.Fatalf("query pg_proc: %v", err)
	}
	var newCount int
	rows.Next()
	_ = rows.Scan(&newCount)
	rows.Close()
	if newCount != 1 {
		t.Fatalf("amount_product_v2 doesn't exist live after rename — bug #11 regressed")
	}
}
