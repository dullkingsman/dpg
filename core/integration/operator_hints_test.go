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
	"github.com/dullkingsman/dpg/internal/introspect"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripOperatorOptimizerHintsAndDiffing is the live regression guard
// for RFC Section 14.3: Operator had no structured fields for its
// optimizer-hint properties (RESTRICT/JOIN/COMMUTATOR/NEGATOR/HASHES/
// MERGES), so any change to them — even hint-only, function/operand types
// unchanged — always routed through the generic whole-body-hash compare and
// forced a DESTRUCTIVE DROP+CREATE, contradicting Section 14.3's own
// diffing table (ALTER OPERATOR ... SET (...) -> SAFE).
//
// Stage 1 proves the fix: editing RESTRICT/JOIN is a genuine in-place
// ALTER (operator OID stable, no DROP+CREATE). Stage 2 proves the
// documented exception still works: real PostgreSQL rejects changing an
// already-set COMMUTATOR via ALTER, confirmed live, so that transition must
// still fall back to DROP+CREATE (new OID) and actually apply successfully.
func TestRoundtripOperatorOptimizerHintsAndDiffing(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()
	ci := introspect.New()
	store := newMemStore()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `
		CREATE FUNCTION my_lt(int, int) RETURNS bool AS 'SELECT $1 < $2' LANGUAGE sql IMMUTABLE;
		CREATE FUNCTION my_lt_rev(int, int) RETURNS bool AS 'SELECT $2 < $1' LANGUAGE sql IMMUTABLE;
		CREATE OPERATOR public.==!= (LEFTARG = int, RIGHTARG = int, PROCEDURE = my_lt_rev);
	`); err != nil {
		t.Fatalf("setup: %v", err)
	}

	oprOID := func() uint32 {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT oid FROM pg_operator WHERE oprname = '===' AND oprnamespace = 'public'::regnamespace`)
		if err != nil {
			t.Fatalf("query oid: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("operator === not found")
		}
		var oid uint32
		if err := rows.Scan(&oid); err != nil {
			t.Fatalf("scan oid: %v", err)
		}
		return oid
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	// Stage 1: create with RESTRICT/JOIN declared, then edit them only.
	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
			t.Fatalf("write schema: %v", err)
		}
	}
	write(`OPERATOR public.=== (
    LEFTARG = int, RIGHTARG = int, PROCEDURE = my_lt,
    RESTRICT = scalarltsel, JOIN = scalarltjoinsel
);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oidBefore := oprOID()

	write(`OPERATOR public.=== (
    LEFTARG = int, RIGHTARG = int, PROCEDURE = my_lt,
    RESTRICT = scalargtsel, JOIN = scalargtjoinsel
);`)
	desired2, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile v2: %v", err)
	}
	base, _ := store.Load("test", "dpgtest")
	ops, err := differ.Diff(desired2, base)
	if err != nil {
		t.Fatalf("diff (hint edit): %v", err)
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), "DROP OPERATOR") || strings.Contains(op.SQL(), "CREATE OPERATOR") {
			t.Fatalf("hint-only RESTRICT/JOIN edit must not drop/recreate, got: %s", op.SQL())
		}
	}
	if len(ops) != 1 || !strings.Contains(ops[0].SQL(), "ALTER OPERATOR") {
		t.Fatalf("expected exactly one ALTER OPERATOR SET op, got: %v", opsSQL(ops))
	}
	if ops[0].Safety() != pipeline.Safe {
		t.Fatalf("expected Safe, got %s: %s", ops[0].Safety(), ops[0].SQL())
	}

	migration, err := emitter.Emit(ops, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := applyExec.Apply(ctx, migration, conn); err != nil {
		t.Fatalf("apply (hint edit) against live PostgreSQL: %v", err)
	}

	if oprOID() != oidBefore {
		t.Fatalf("expected operator OID to stay stable across the hint-only ALTER, got %d before and %d after", oidBefore, oprOID())
	}
	rows, err := conn.QueryRows(ctx, `SELECT oprrest::regproc::text, oprjoin::regproc::text FROM pg_operator WHERE oprname = '===' AND oprnamespace = 'public'::regnamespace`)
	if err != nil {
		t.Fatalf("query hints: %v", err)
	}
	if !rows.Next() {
		t.Fatal("operator not found after apply")
	}
	var restrict, join string
	if err := rows.Scan(&restrict, &join); err != nil {
		t.Fatalf("scan hints: %v", err)
	}
	rows.Close()
	if restrict != "scalargtsel" || join != "scalargtjoinsel" {
		t.Fatalf("expected updated hints scalargtsel/scalargtjoinsel, got %s/%s", restrict, join)
	}

	appliedSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(appliedSnap, desired2); err != nil {
		t.Fatalf("populate snapshot: %v", err)
	}
	if err := store.Save("test", "dpgtest", appliedSnap); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	// No-drift check against a fresh live introspection.
	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var managedLive []pipeline.IRObject
	for _, obj := range liveObjects {
		if _, ok := appliedSnap.Objects[obj.QualifiedName()]; ok {
			managedLive = append(managedLive, obj)
		}
	}
	liveSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(liveSnap, managedLive); err != nil {
		t.Fatalf("populate live snapshot: %v", err)
	}
	driftOps, err := differ.Diff(desired2, liveSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(driftOps) != 0 {
		t.Errorf("expected zero drift after hint-only apply, got %d ops:", len(driftOps))
		for _, op := range driftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}

	// Stage 2: an already-set COMMUTATOR changing to a different operator —
	// real PostgreSQL rejects this via ALTER, confirmed live, so it must
	// still fall back to DROP+CREATE (new OID) and actually apply.
	write(`OPERATOR public.=== (
    LEFTARG = int, RIGHTARG = int, PROCEDURE = my_lt,
    RESTRICT = scalargtsel, JOIN = scalargtjoinsel,
    COMMUTATOR = OPERATOR(public.==!=)
);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	oidAfterCommutatorSet := oprOID()

	write(`OPERATOR public.=== (
    LEFTARG = int, RIGHTARG = int, PROCEDURE = my_lt,
    RESTRICT = scalargtsel, JOIN = scalargtjoinsel,
    COMMUTATOR = OPERATOR(public.===)
);`)
	desired3, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile v3: %v", err)
	}
	base3, _ := store.Load("test", "dpgtest")
	ops3, err := differ.Diff(desired3, base3)
	if err != nil {
		t.Fatalf("diff (commutator change): %v", err)
	}
	foundDrop := false
	for _, op := range ops3 {
		if strings.Contains(op.SQL(), `DROP OPERATOR IF EXISTS "public".===(integer, integer);`) {
			foundDrop = true
		}
	}
	if !foundDrop {
		t.Fatalf("expected DROP OPERATOR for a COMMUTATOR change, got: %v", opsSQL(ops3))
	}
	migration3, err := emitter.Emit(ops3, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit v3: %v", err)
	}
	if err := applyExec.Apply(ctx, migration3, conn); err != nil {
		t.Fatalf("apply (commutator change) against live PostgreSQL: %v", err)
	}
	if oprOID() == oidAfterCommutatorSet {
		t.Fatalf("expected a new OID after DROP+CREATE for the COMMUTATOR change, got the same OID %d", oidAfterCommutatorSet)
	}
}
