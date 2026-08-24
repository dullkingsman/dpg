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

// TestRoundtripAggregateFinalfuncExtra is the live regression guard for RFC
// audit item #29: FINALFUNC_EXTRA is a bare presence flag (no "= value"
// part, confirmed via pg_query.Parse — its DefElem.Arg is nil), which
// buildAggregateOptions previously dropped unconditionally for every option
// requiring a non-nil Arg. This silently discarded the flag both from
// diffAggregate's structured comparison (an add/remove was completely
// undetected drift, not just an unoptimized DROP+CREATE) and from dpg
// dump's rendering. Proves: (a) declaring FINALFUNC_EXTRA genuinely lands
// in pg_aggregate.aggfinalextra, and (b) adding it to an existing aggregate
// is detected and reapplied via DROP+CREATE rather than silently ignored.
func TestRoundtripAggregateFinalfuncExtra(t *testing.T) {
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

	v1 := `FUNCTION agg_accum(state INTEGER, val INTEGER) RETURNS INTEGER LANGUAGE sql AS $$ SELECT state + val $$ {}

FUNCTION agg_final_v1(state INTEGER) RETURNS INTEGER LANGUAGE sql AS $$ SELECT state $$ {}

AGGREGATE myagg (INTEGER) (SFUNC = agg_accum, STYPE = INTEGER, FINALFUNC = agg_final_v1) {}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	finalExtra := func() bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT a.aggfinalextra FROM pg_proc p JOIN pg_aggregate a ON a.aggfnoid = p.oid WHERE p.proname = 'myagg'`)
		if err != nil {
			t.Fatalf("query pg_aggregate: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("pg_aggregate has no row for myagg")
		}
		var extra bool
		if err := rows.Scan(&extra); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return extra
	}

	if finalExtra() {
		t.Fatal("myagg: aggfinalextra already true before FINALFUNC_EXTRA was ever declared — test setup is broken")
	}

	v2 := `FUNCTION agg_accum(state INTEGER, val INTEGER) RETURNS INTEGER LANGUAGE sql AS $$ SELECT state + val $$ {}

FUNCTION agg_final_v1(state INTEGER) RETURNS INTEGER LANGUAGE sql AS $$ SELECT state $$ {}

FUNCTION agg_final_v2(state INTEGER, extra INTEGER) RETURNS INTEGER LANGUAGE sql AS $$ SELECT state $$ {}

AGGREGATE myagg (INTEGER) (SFUNC = agg_accum, STYPE = INTEGER, FINALFUNC = agg_final_v2, FINALFUNC_EXTRA) {}`
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
		t.Fatalf("diff (finalfunc_extra added): %v", err)
	}
	var sawDrop bool
	for _, op := range ops {
		if op.SQL() == `DROP AGGREGATE IF EXISTS "public"."myagg"(integer);` {
			sawDrop = true
		}
	}
	if !sawDrop {
		t.Fatalf("expected adding FINALFUNC_EXTRA to be detected as a DROP+CREATE (bug #29 regressed: undetected drift), got: %v", opsSQL(ops))
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if !finalExtra() {
		t.Fatal("myagg: aggfinalextra still false after FINALFUNC_EXTRA was declared — bug #29 regressed")
	}
}
