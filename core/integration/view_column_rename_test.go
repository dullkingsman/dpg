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

// TestRoundtripViewSurvivesUnaliasedColumnRename is the regression guard for
// RFC audit item #29: DPG emitted CREATE OR REPLACE VIEW unconditionally on
// any query-text change, but real PostgreSQL rejects a replacement that
// would change an existing output column's implicit name
// ("ERROR: cannot change name of view column ... SQLSTATE 42P16"). The most
// common real-world trigger is exactly what this test does: an unaliased
// "SELECT col FROM t" view whose source column gets renamed in the same
// migration as the table's own RENAMED FROM directive. Before the fix this
// aborted the whole apply; after, the differ detects the output-column
// change and falls back to DROP VIEW CASCADE; CREATE VIEW.
func TestRoundtripViewSurvivesUnaliasedColumnRename(t *testing.T) {
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

	v1 := `TABLE widgets (
    id       BIGINT PRIMARY KEY,
    old_name TEXT NOT NULL
);

VIEW widget_names AS
    SELECT old_name FROM widgets;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, err := conn.Exec(ctx, `INSERT INTO widgets (id, old_name) VALUES (1, 'gadget')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	v2 := `TABLE widgets (
    id       BIGINT PRIMARY KEY,
    new_name TEXT NOT NULL
) {
    COLUMN new_name { RENAMED FROM old_name; }
}

VIEW widget_names AS
    SELECT new_name FROM widgets;`
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
	var sawViewReplace, sawViewDropCreate bool
	for _, op := range ops {
		sql := op.SQL()
		if strings.Contains(sql, "CREATE OR REPLACE VIEW") && strings.Contains(sql, "widget_names") {
			sawViewReplace = true
		}
		if strings.Contains(sql, "DROP VIEW") && strings.Contains(sql, "widget_names") {
			sawViewDropCreate = true
		}
	}
	if sawViewReplace {
		t.Fatalf("expected no CREATE OR REPLACE VIEW for widget_names, got: %v", opsSQL(ops))
	}
	if !sawViewDropCreate {
		t.Fatalf("expected DROP VIEW for widget_names, got: %v", opsSQL(ops))
	}

	migration2, err := emitter.Emit(ops, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit v2: %v", err)
	}
	// The actual bug: this apply used to fail with SQLSTATE 42P16 because
	// the emitted SQL was CREATE OR REPLACE VIEW against a renamed column.
	if err := applyExec.Apply(ctx, migration2, conn); err != nil {
		t.Fatalf("apply v2 failed (this is exactly the SQLSTATE 42P16 migration-abort bug #29 fixes): %v", err)
	}
	appliedSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(appliedSnap, desired2); err != nil {
		t.Fatalf("populate snapshot: %v", err)
	}
	if err := store.Save("test", "dpgtest", appliedSnap); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	rows, err := conn.QueryRows(ctx, "SELECT new_name FROM widget_names WHERE new_name = 'gadget'")
	if err != nil {
		t.Fatalf("query widget_names: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("widget_names has no row for the seeded widget after the rename")
	}
	rows.Close()

	// Live-verify blindness check: a fresh introspect pass must agree too.
	ci := introspect.New()
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
