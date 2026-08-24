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

// TestRoundtripCollationRules is the live regression guard for RFC audit
// item #111: PG16+ ICU RULES was entirely absent from Collation's
// significant-property comparison — buildCollation's DefElem switch had no
// "rules" case, and since Collation decides DROP+CREATE by comparing that
// property set (not a raw body hash), a RULES-only change was completely
// undetected drift, not just an unoptimized DROP+CREATE. This proves:
// (a) a declared RULES value genuinely lands in the live catalog
// (pg_collation.collicurules) at CREATE time, and (b) changing RULES is
// actually detected and reapplied (DROP+CREATE, real PostgreSQL has no
// in-place ALTER for it), not silently ignored.
func TestRoundtripCollationRules(t *testing.T) {
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

	v1 := `COLLATION c (PROVIDER = icu, LOCALE = 'und', RULES = '&a < b');`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	liveRules := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT collicurules FROM pg_collation WHERE collname = 'c'`)
		if err != nil {
			t.Fatalf("query pg_collation: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("pg_collation has no row for c")
		}
		var rules *string
		if err := rows.Scan(&rules); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if rules == nil {
			return ""
		}
		return *rules
	}

	if got := liveRules(); got != "&a < b" {
		t.Fatalf("c: collicurules = %q after initial apply, want \"&a < b\" — bug #111 regressed (RULES discarded at CREATE time)", got)
	}

	v2 := `COLLATION c (PROVIDER = icu, LOCALE = 'und', RULES = '&b < a');`
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
		t.Fatalf("diff (rules change): %v", err)
	}
	var sawDrop bool
	for _, op := range ops {
		if op.SQL() == `DROP COLLATION IF EXISTS "public"."c";` {
			sawDrop = true
		}
	}
	if !sawDrop {
		t.Fatalf("expected a RULES change to be detected as a DROP+CREATE (bug #111 regressed: undetected drift), got: %v", opsSQL(ops))
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := liveRules(); got != "&b < a" {
		t.Fatalf("c: collicurules = %q after RULES change, want \"&b < a\" — bug #111 regressed", got)
	}
}
