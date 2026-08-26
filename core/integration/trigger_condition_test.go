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
	"github.com/thec1oud/dpg/internal/introspect"
	"github.com/thec1oud/dpg/internal/pipeline"
	"github.com/thec1oud/dpg/internal/snapshot"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestRoundtripTriggerCondition proves a conditional trigger's WHEN clause is
// (a) correctly reconstructed from the live catalog with no false-positive
// drift caused by pg_get_expr's mandatory outer-paren wrapping, and (b) an
// edited WHEN condition is genuinely detected and reapplied — not silently
// discarded. Before this fix, introspectTriggers never selected tgqual at
// all, so Condition was always nil from a live-sourced Trigger regardless of
// what the database actually had, and diffTriggers never compared it either.
func TestRoundtripTriggerCondition(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()
	ci := introspect.New()
	store := newMemStore()

	writeAndCompile := func(dir, schema string) []pipeline.IRObject {
		schemaFile := filepath.Join(dir, "schema.dpg")
		if err := os.WriteFile(schemaFile, []byte(schema), 0o644); err != nil {
			t.Fatalf("write schema: %v", err)
		}
		desired, _, err := compiler.Compile([]string{schemaFile}, dir, pipeline.Default)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		return desired
	}

	dir := t.TempDir()
	schemaV1 := `FUNCTION trg_touch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.touched := true;
  RETURN NEW;
END;
$$ {}

TABLE widgets (
    id     bigint PRIMARY KEY,
    status text NOT NULL,
    touched boolean NOT NULL DEFAULT false
) {
    TRIGGER trg_a AFTER UPDATE FOR EACH ROW WHEN (NEW.status = 'active') EXECUTE FUNCTION trg_touch();
}
`
	desired := writeAndCompile(dir, schemaV1)

	emptySnap, _ := store.Load("test", "dpgtest")
	ops, err := differ.Diff(desired, emptySnap)
	if err != nil {
		t.Fatalf("diff (initial): %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("expected ops against empty snapshot, got none")
	}

	migration, err := emitter.Emit(ops, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if err := applyExec.Apply(ctx, migration, conn); err != nil {
		t.Fatalf("apply: %v", err)
	}

	appliedSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(appliedSnap, desired); err != nil {
		t.Fatalf("populate snapshot: %v", err)
	}
	if err := store.Save("test", "dpgtest", appliedSnap); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

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

	// (a) No false-positive drift: pg_get_expr wraps the live condition in
	// its own outer parens ("(status = 'active'::text)"), which must
	// normalize equal to the declared, unwrapped, uncast source text.
	noDriftOps, err := differ.Diff(desired, liveSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(noDriftOps) != 0 {
		t.Errorf("expected zero drift after introspecting an unchanged WHEN condition, got %d ops:", len(noDriftOps))
		for _, op := range noDriftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}

	// Explicit live catalog assertion: tgqual must actually be set, proving
	// this isn't accidentally passing because both sides are nil.
	rows, err := conn.QueryRows(ctx, "SELECT tgqual IS NOT NULL FROM pg_trigger WHERE tgname = 'trg_a' AND NOT tgisinternal")
	if err != nil {
		t.Fatalf("query pg_trigger: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("pg_trigger has no row for trg_a")
	}
	var hasQual bool
	if err := rows.Scan(&hasQual); err != nil {
		rows.Close()
		t.Fatalf("scan tgqual presence: %v", err)
	}
	rows.Close()
	if !hasQual {
		t.Fatal("trg_a: tgqual is NULL live — WHEN condition was not actually applied, test setup is broken")
	}

	// (b) A genuinely edited WHEN condition must be detected and reapplied,
	// not silently discarded.
	dir2 := t.TempDir()
	schemaV2 := `FUNCTION trg_touch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.touched := true;
  RETURN NEW;
END;
$$ {}

TABLE widgets (
    id     bigint PRIMARY KEY,
    status text NOT NULL,
    touched boolean NOT NULL DEFAULT false
) {
    TRIGGER trg_a AFTER UPDATE FOR EACH ROW WHEN (NEW.status = 'inactive') EXECUTE FUNCTION trg_touch();
}
`
	editedDesired := writeAndCompile(dir2, schemaV2)

	editOps, err := differ.Diff(editedDesired, liveSnap)
	if err != nil {
		t.Fatalf("edit diff: %v", err)
	}
	var sawDrop, sawCreateWithNewCond bool
	for _, op := range editOps {
		sql := op.SQL()
		if strings.Contains(sql, `DROP TRIGGER IF EXISTS "trg_a"`) {
			sawDrop = true
		}
		if strings.Contains(sql, "WHEN (NEW.status = 'inactive')") {
			sawCreateWithNewCond = true
		}
	}
	if !sawDrop || !sawCreateWithNewCond {
		t.Errorf("expected DROP+CREATE reflecting the edited WHEN condition, got %d ops:", len(editOps))
		for _, op := range editOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}

	// Apply the edit and confirm it actually landed live.
	editMigration, err := emitter.Emit(editOps, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit edit: %v", err)
	}
	if err := applyExec.Apply(ctx, editMigration, conn); err != nil {
		t.Fatalf("apply edit: %v", err)
	}
	rows2, err := conn.QueryRows(ctx, "SELECT pg_get_triggerdef(oid, true) FROM pg_trigger WHERE tgname = 'trg_a' AND NOT tgisinternal")
	if err != nil {
		t.Fatalf("query pg_trigger after edit: %v", err)
	}
	if !rows2.Next() {
		rows2.Close()
		t.Fatal("pg_trigger has no row for trg_a after edit")
	}
	var liveCond string
	if err := rows2.Scan(&liveCond); err != nil {
		rows2.Close()
		t.Fatalf("scan tgqual after edit: %v", err)
	}
	rows2.Close()
	if !strings.Contains(liveCond, "inactive") {
		t.Errorf("live WHEN condition after edit = %q, want it to reference 'inactive'", liveCond)
	}
}
