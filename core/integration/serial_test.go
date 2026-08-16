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
	"github.com/dullkingsman/dpg/internal/introspect"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripSerialColumn proves a table with a SERIAL PRIMARY KEY column
// plus a bare SMALLSERIAL/BIGSERIAL column (no PK) applies live and shows
// zero drift when the live catalog is re-introspected and diffed against the
// desired IR. It also directly asserts, via pg_attribute, that the bare
// SERIAL-family columns are NOT NULL live — SERIAL implies NOT NULL in real
// PG independent of PRIMARY KEY, and a wrong value on both sides of the diff
// could coincidentally agree and mask a regression in that behavior.
func TestRoundtripSerialColumn(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()
	ci := introspect.New()
	store := newMemStore()

	schema := `TABLE widgets (
    id    SERIAL PRIMARY KEY,
    name  text NOT NULL,
    qty   SMALLSERIAL,
    total BIGSERIAL
) {}
`
	dir := t.TempDir()
	schemaFile := filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(schemaFile, []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	desired, err := compiler.Compile([]string{schemaFile}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	emptySnap, _ := store.Load("test", "dpgtest")
	ops, err := differ.Diff(desired, emptySnap)
	if err != nil {
		t.Fatalf("diff (initial): %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("expected ops against empty snapshot, got none")
	}
	for _, op := range ops {
		if op.Safety() == pipeline.Destructive {
			t.Errorf("initial plan contains unexpected DESTRUCTIVE op: %s", op.SQL())
		}
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

	// Explicit live catalog assertion: the bare SMALLSERIAL/BIGSERIAL
	// columns (no PK) must actually be NOT NULL in pg_attribute.
	for _, col := range []string{"qty", "total"} {
		rows, err := conn.QueryRows(ctx,
			"SELECT attnotnull FROM pg_attribute WHERE attrelid = 'public.widgets'::regclass AND attname = $1",
			col,
		)
		if err != nil {
			t.Fatalf("query pg_attribute for %s: %v", col, err)
		}
		if !rows.Next() {
			rows.Close()
			t.Fatalf("pg_attribute has no row for widgets.%s", col)
		}
		var notNull bool
		if err := rows.Scan(&notNull); err != nil {
			rows.Close()
			t.Fatalf("scan attnotnull for %s: %v", col, err)
		}
		rows.Close()
		if !notNull {
			t.Errorf("widgets.%s: attnotnull got false, want true (SERIAL implies NOT NULL)", col)
		}
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

	driftOps, err := differ.Diff(desired, liveSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(driftOps) != 0 {
		t.Errorf("drift detected after apply (%d ops):", len(driftOps))
		for _, op := range driftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}
}
