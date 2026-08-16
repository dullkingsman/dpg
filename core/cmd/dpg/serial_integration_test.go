//go:build integration

package main

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
	"github.com/dullkingsman/dpg/internal/format"
	"github.com/dullkingsman/dpg/internal/introspect"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestDumpSerialColumnReapplies proves dpg dump's rendering path
// (renderObjectDPG / renderColText) reproduces a SERIAL column as the
// literal SERIAL keyword against a REAL introspected live table — not just
// the synthetic IR object TestRenderColumnSerialCompiles builds by hand —
// and that the dumped source recompiles and shows zero drift against the
// same live catalog. This is the test that closes the "genuinely broken,
// non-reapplicable output" bug: a naive dump of a SERIAL column rendered
// the normalized underlying integer type plus a hand-reconstructed
// DEFAULT nextval(...) referencing a sequence the dump never declares.
func TestDumpSerialColumnReapplies(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()
	ci := introspect.New()

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

	desired, _, err := compiler.Compile([]string{schemaFile}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	ops, err := differ.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatalf("diff (initial): %v", err)
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

	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	var tbl *ir.Table
	for _, obj := range liveObjects {
		if tt, ok := obj.(*ir.Table); ok && tt.Name == "widgets" {
			tbl = tt
		}
	}
	if tbl == nil {
		t.Fatal("introspection did not return the widgets table")
	}

	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	var b strings.Builder
	renderObjectDPG(&b, tbl, fmtOpts)
	rendered := b.String()

	if !strings.Contains(rendered, "SERIAL") {
		t.Fatalf("dumped source does not use the literal SERIAL keyword: %s", rendered)
	}
	if strings.Contains(rendered, "nextval") {
		t.Fatalf("dumped source must not reference nextval(...) directly: %s", rendered)
	}

	dumpDir := t.TempDir()
	dumpFile := filepath.Join(dumpDir, "widgets.dpg")
	if err := os.WriteFile(dumpFile, []byte(rendered), 0o644); err != nil {
		t.Fatalf("write dumped source: %v", err)
	}

	redesired, _, err := compiler.Compile([]string{dumpFile}, dumpDir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped source failed to recompile: %v\n---\n%s", err, rendered)
	}

	var managedLive []pipeline.IRObject
	for _, obj := range liveObjects {
		if obj.QualifiedName() == tbl.QualifiedName() {
			managedLive = append(managedLive, obj)
		}
	}
	liveSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(liveSnap, managedLive); err != nil {
		t.Fatalf("populate live snapshot: %v", err)
	}

	driftOps, err := differ.Diff(redesired, liveSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(driftOps) != 0 {
		t.Errorf("drift between dumped-and-recompiled source and live catalog (%d ops):", len(driftOps))
		for _, op := range driftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}
}
