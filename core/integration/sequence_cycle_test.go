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

// TestRoundtripSequenceCycleToggledOff is the regression guard for RFC audit
// item #21: writeSeqParams only ever emitted "CYCLE", never "NO CYCLE",
// because the toggle was gated on `*o.Cycle` being true. Toggling an
// existing CYCLE sequence off was correctly *detected* as a diff, but the
// emitted ALTER SEQUENCE carried no CYCLE clause at all — and unlike CREATE
// SEQUENCE, PostgreSQL's ALTER SEQUENCE never resets an omitted option to
// its default, so the live sequence kept cycling forever while the snapshot
// silently recorded the change as applied. This proves the live catalog
// flag actually flips and that a follow-up diff against the freshly
// introspected state is a true no-op (the drift is no longer invisible).
func TestRoundtripSequenceCycleToggledOff(t *testing.T) {
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

	seqCycleLive := func() bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, "SELECT cycle FROM pg_sequences WHERE schemaname = 'public' AND sequencename = 'order_seq'")
		if err != nil {
			t.Fatalf("query pg_sequences: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("pg_sequences has no row for order_seq")
		}
		var cycle bool
		if err := rows.Scan(&cycle); err != nil {
			t.Fatalf("scan cycle: %v", err)
		}
		return cycle
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	// Create the sequence with CYCLE.
	v1 := `SEQUENCE order_seq MAXVALUE 5 CYCLE;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if !seqCycleLive() {
		t.Fatal("order_seq: live cycle flag is false after CREATE SEQUENCE ... CYCLE — test setup is broken")
	}

	// Toggle to NO CYCLE.
	v2 := `SEQUENCE order_seq MAXVALUE 5 NO CYCLE;`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	desired2, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile v2: %v", err)
	}
	prevSnap, _ := store.Load("test", "dpgtest")
	ops2, err := differ.Diff(desired2, prevSnap)
	if err != nil {
		t.Fatalf("diff (toggle off): %v", err)
	}
	var sawNoCycle bool
	for _, op := range ops2 {
		if strings.Contains(op.SQL(), "ALTER SEQUENCE") && strings.Contains(op.SQL(), "NO CYCLE") {
			sawNoCycle = true
		}
	}
	if !sawNoCycle {
		t.Fatalf("expected ALTER SEQUENCE ... NO CYCLE in the diff, got: %v", opsSQL(ops2))
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if seqCycleLive() {
		t.Fatal("order_seq: live cycle flag is still true after ALTER SEQUENCE ... NO CYCLE — the drift is invisible, bug #21 regressed")
	}

	// The drift must not stay silently invisible: re-diffing against the
	// freshly-applied snapshot must be a genuine no-op.
	newSnap, _ := store.Load("test", "dpgtest")
	noDriftOps, err := differ.Diff(desired2, newSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(noDriftOps) != 0 {
		t.Errorf("expected zero drift after toggling CYCLE off and re-diffing, got %d ops:", len(noDriftOps))
		for _, op := range noDriftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}

	// Independent live-catalog corroboration via a fresh introspect pass,
	// not just the snapshot the applier itself wrote.
	ci := introspect.New()
	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var managedLive []pipeline.IRObject
	for _, obj := range liveObjects {
		if _, ok := newSnap.Objects[obj.QualifiedName()]; ok {
			managedLive = append(managedLive, obj)
		}
	}
	liveSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(liveSnap, managedLive); err != nil {
		t.Fatalf("populate live snapshot: %v", err)
	}
	liveDriftOps, err := differ.Diff(desired2, liveSnap)
	if err != nil {
		t.Fatalf("live drift diff: %v", err)
	}
	if len(liveDriftOps) != 0 {
		t.Errorf("expected zero drift against freshly introspected live state, got %d ops:", len(liveDriftOps))
		for _, op := range liveDriftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}
}

func opsSQL(ops []pipeline.DiffOp) []string {
	out := make([]string, len(ops))
	for i, op := range ops {
		out[i] = op.SQL()
	}
	return out
}
