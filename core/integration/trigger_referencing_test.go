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
	"github.com/thec1oud/dpg/internal/ir"
	"github.com/thec1oud/dpg/internal/pipeline"
	"github.com/thec1oud/dpg/internal/snapshot"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestRoundtripTriggerReferencing is the regression guard for RFC Section 7.9
// (audit item #2): REFERENCING OLD TABLE AS ... NEW TABLE AS ... was
// already specified in the RFC's own ABNF grammar and worked example, but
// was a hard parse error in the reference implementation — blockparser had
// no handling for it at all. This proves the fix real against a real
// database, not just that the grammar is now accepted:
//
//  1. A statement-level AFTER UPDATE trigger with REFERENCING OLD TABLE AS
//     ... NEW TABLE AS ... applies live, and the transition-table pseudo-
//     relations are genuinely queryable inside the trigger function (not
//     just syntactically present) — the function inserts a row counting
//     new_rows, proving PostgreSQL actually populated it.
//  2. A fresh introspect pass round-trips the transition names with zero
//     drift (pg_trigger.tgoldtable/tgnewtable read back correctly).
//  3. A changed transition name is detected and reapplied (DROP+CREATE),
//     not silently ignored.
func TestRoundtripTriggerReferencing(t *testing.T) {
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

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	v1 := `TABLE change_log (
    id             bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    change_count   integer NOT NULL
) {}

FUNCTION log_bulk_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO change_log (change_count) SELECT count(*) FROM new_rows;
    RETURN NULL;
END;
$$ {}

TABLE widgets (
    id     bigint PRIMARY KEY,
    status text NOT NULL
) {
    TRIGGER trg_audit AFTER UPDATE
        REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
        FOR EACH STATEMENT
        EXECUTE FUNCTION log_bulk_update();
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, err := conn.Exec(ctx, `INSERT INTO widgets (id, status) VALUES (1, 'a'), (2, 'a'), (3, 'a')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := conn.Exec(ctx, `UPDATE widgets SET status = 'b'`); err != nil {
		t.Fatalf("update: %v", err)
	}

	rows, err := conn.QueryRows(ctx, `SELECT change_count FROM change_log`)
	if err != nil {
		t.Fatalf("query change_log: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("change_log has no row — trigger did not fire, or NEW TABLE was not populated")
	}
	var changeCount int
	if err := rows.Scan(&changeCount); err != nil {
		rows.Close()
		t.Fatalf("scan change_count: %v", err)
	}
	rows.Close()
	if changeCount != 3 {
		t.Fatalf("change_count = %d, want 3 — REFERENCING NEW TABLE AS new_rows did not correctly capture all updated rows", changeCount)
	}

	// (2) Fresh introspect pass must round-trip with zero drift.
	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var liveTrigger *ir.Trigger
	for _, obj := range liveObjects {
		tbl, ok := obj.(*ir.Table)
		if !ok || tbl.Name != "widgets" {
			continue
		}
		for _, trg := range tbl.Triggers {
			if trg.Name == "trg_audit" {
				liveTrigger = trg
			}
		}
	}
	if liveTrigger == nil {
		t.Fatal("introspect did not return trg_audit")
	}
	if liveTrigger.OldTransitionName == nil || *liveTrigger.OldTransitionName != "old_rows" {
		t.Fatalf("introspected OldTransitionName = %v, want old_rows", liveTrigger.OldTransitionName)
	}
	if liveTrigger.NewTransitionName == nil || *liveTrigger.NewTransitionName != "new_rows" {
		t.Fatalf("introspected NewTransitionName = %v, want new_rows", liveTrigger.NewTransitionName)
	}

	prevSnap, _ := store.Load("test", "dpgtest")
	var managedLive []pipeline.IRObject
	for _, obj := range liveObjects {
		if _, ok := prevSnap.Objects[obj.QualifiedName()]; ok {
			managedLive = append(managedLive, obj)
		}
	}
	liveSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(liveSnap, managedLive); err != nil {
		t.Fatalf("populate live snapshot: %v", err)
	}
	desired, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("recompile: %v", err)
	}
	noDriftOps, err := differ.Diff(desired, liveSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(noDriftOps) != 0 {
		t.Errorf("expected zero drift after introspecting an unchanged REFERENCING clause, got %d ops:", len(noDriftOps))
		for _, op := range noDriftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}

	// (3) A changed transition name must be detected and reapplied.
	v2 := `TABLE change_log (
    id             bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    change_count   integer NOT NULL
) {}

FUNCTION log_bulk_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO change_log (change_count) SELECT count(*) FROM changed_rows;
    RETURN NULL;
END;
$$ {}

TABLE widgets (
    id     bigint PRIMARY KEY,
    status text NOT NULL
) {
    TRIGGER trg_audit AFTER UPDATE
        REFERENCING OLD TABLE AS old_rows NEW TABLE AS changed_rows
        FOR EACH STATEMENT
        EXECUTE FUNCTION log_bulk_update();
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	desired2, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile v2: %v", err)
	}
	ops, err := differ.Diff(desired2, prevSnap)
	if err != nil {
		t.Fatalf("diff (transition name changed): %v", err)
	}
	var sawDrop, sawRecreate bool
	for _, op := range ops {
		if strings.Contains(op.SQL(), `DROP TRIGGER IF EXISTS "trg_audit"`) {
			sawDrop = true
		}
		if strings.Contains(op.SQL(), "CREATE TRIGGER") && strings.Contains(op.SQL(), `NEW TABLE AS "changed_rows"`) {
			sawRecreate = true
		}
	}
	if !sawDrop || !sawRecreate {
		t.Fatalf("expected DROP+CREATE reflecting the new transition name, got: %v", opsSQL(ops))
	}
}
