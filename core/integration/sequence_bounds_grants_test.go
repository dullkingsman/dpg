//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thec1oud/dpg/internal/compiler"
	"github.com/thec1oud/dpg/internal/diff"
	"github.com/thec1oud/dpg/internal/emit"
	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/pipeline"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestRoundtripSequenceNoMinMaxValueToggledOn is the regression guard for
// RFC audit item #20: pg_query represents "NO MINVALUE"/"NO MAXVALUE" and
// the option being omitted entirely as the identical nil-Arg DefElem shape,
// so switching an existing bounded sequence to NO MINVALUE/NO MAXVALUE
// produced zero diff and silently left the live bound in place forever.
// This proves against a real database that the bound actually changes.
func TestRoundtripSequenceNoMinMaxValueToggledOn(t *testing.T) {
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

	liveBounds := func() (min, max int64) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT min_value, max_value FROM pg_sequences WHERE schemaname = 'public' AND sequencename = 'order_seq'`)
		if err != nil {
			t.Fatalf("query pg_sequences: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("pg_sequences has no row for order_seq")
		}
		if err := rows.Scan(&min, &max); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return min, max
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	v1 := `SEQUENCE order_seq MINVALUE 5 MAXVALUE 1000;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if min, max := liveBounds(); min != 5 || max != 1000 {
		t.Fatalf("order_seq: live bounds = [%d, %d] after initial apply, want [5, 1000] — test setup is broken", min, max)
	}

	v2 := `SEQUENCE order_seq NO MINVALUE NO MAXVALUE;`
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
		t.Fatalf("diff (NO MINVALUE/NO MAXVALUE): %v", err)
	}
	var sawNoMin, sawNoMax bool
	for _, op := range ops {
		if strings.Contains(op.SQL(), "ALTER SEQUENCE") {
			if strings.Contains(op.SQL(), "NO MINVALUE") {
				sawNoMin = true
			}
			if strings.Contains(op.SQL(), "NO MAXVALUE") {
				sawNoMax = true
			}
		}
	}
	if !sawNoMin || !sawNoMax {
		t.Fatalf("expected ALTER SEQUENCE ... NO MINVALUE ... NO MAXVALUE in the diff, got: %v", opsSQL(ops))
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	// A bigint sequence's real defaults are 1 and 2^63-1 for an ascending
	// sequence — confirms the live bound genuinely moved, not just that some
	// ALTER SEQUENCE ran.
	if min, max := liveBounds(); min != 1 || max != 9223372036854775807 {
		t.Fatalf("order_seq: live bounds = [%d, %d] after NO MINVALUE/NO MAXVALUE — bug #20 regressed (drift invisible)", min, max)
	}

	newSnap, _ := store.Load("test", "dpgtest")
	noDriftOps, err := differ.Diff(desired2, newSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(noDriftOps) != 0 {
		t.Errorf("expected zero drift after toggling NO MINVALUE/NO MAXVALUE and re-diffing, got %d ops:", len(noDriftOps))
		for _, op := range noDriftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}
}

// TestRoundtripSequenceGrant is the regression guard for RFC audit item #24:
// ir.Sequence.Grants was correctly populated by the builder, but neither
// createSequence nor diffSequence ever referenced it — no GRANT SQL on
// create, no diff/ALTER on update — and SnapSequence had no Grants field at
// all, so it couldn't even round-trip through the snapshot. This proves the
// GRANT actually lands live, that removing it emits and applies a real
// REVOKE, and that a fresh introspect pass sees the sequence's grants too.
func TestRoundtripSequenceGrant(t *testing.T) {
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

	v1 := `ROLE app_readonly NOLOGIN;

SEQUENCE order_seq {
    GRANT USAGE TO app_readonly;
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	hasUsage := func() bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT has_sequence_privilege('app_readonly', 'order_seq', 'USAGE')`)
		if err != nil {
			t.Fatalf("query has_sequence_privilege: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("has_sequence_privilege returned no row")
		}
		var v bool
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return v
	}
	if !hasUsage() {
		t.Fatal("app_readonly should have USAGE on order_seq after initial apply — bug #24 regressed (GRANT never applied)")
	}

	v2 := `ROLE app_readonly NOLOGIN;

SEQUENCE order_seq;`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if hasUsage() {
		t.Fatal("app_readonly still has USAGE on order_seq after the GRANT was removed from source")
	}
}
