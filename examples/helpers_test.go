// Package examples_test contains runnable examples that exercise the full DPG
// pipeline: tokenising .dpg sources, parsing to IR, diffing against a snapshot,
// emitting SQL, running the linter, and running portability analysis.
//
// Run all examples:
//
//	go test ./examples/... -v
//
// Run a specific example:
//
//	go test ./examples/... -v -run TestCompileInitialDDL
package examples_test

import (
	"strings"
	"testing"
	"time"

	// Side-effect imports: each package registers its implementation in
	// pipeline.Default via init(), making it available to compiler.Compile
	// and pipeline.MustResolve calls below.
	_ "github.com/dullkingsman/dpg/internal/blockparser"
	_ "github.com/dullkingsman/dpg/internal/diff"
	_ "github.com/dullkingsman/dpg/internal/graph"
	_ "github.com/dullkingsman/dpg/internal/ir"
	_ "github.com/dullkingsman/dpg/internal/linter"
	_ "github.com/dullkingsman/dpg/internal/merger"
	_ "github.com/dullkingsman/dpg/internal/pgparser"
	_ "github.com/dullkingsman/dpg/internal/portability"
	_ "github.com/dullkingsman/dpg/internal/scanner"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/pipeline"
	// Named import gives us snapshot.Populate; init() also registers the FileStore.
	"github.com/dullkingsman/dpg/internal/snapshot"
)

// compileDPG compiles one or more .dpg fixture files into a sorted IR slice.
// File paths are relative to the examples/ package directory.
func compileDPG(t *testing.T, files ...string) []pipeline.IRObject {
	t.Helper()
	objects, err := compiler.Compile(files, pipeline.Default)
	if err != nil {
		t.Fatalf("compile %v: %v", files, err)
	}
	return objects
}

// buildSnapshot converts a slice of compiled IR objects into an in-memory
// pipeline.Snapshot, as if a previous `dpg apply` had succeeded.
func buildSnapshot(t *testing.T, objects []pipeline.IRObject) *pipeline.Snapshot {
	t.Helper()
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, objects); err != nil {
		t.Fatalf("populate snapshot: %v", err)
	}
	return snap
}

// planSQL diffs desired IR against snap and returns the rendered SQL migration.
// The migration header uses a fixed timestamp so output is deterministic.
func planSQL(t *testing.T, desired []pipeline.IRObject, snap *pipeline.Snapshot) string {
	t.Helper()

	differ, err := pipeline.MustResolve[pipeline.Differ](pipeline.Default, pipeline.KeyDiffer)
	if err != nil {
		t.Fatalf("resolve differ: %v", err)
	}
	ops, err := differ.Diff(desired, snap)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}

	emitter, err := pipeline.MustResolve[pipeline.Emitter](pipeline.Default, pipeline.KeyEmitter)
	if err != nil {
		t.Fatalf("resolve emitter: %v", err)
	}
	migration, err := emitter.Emit(ops, pipeline.MigrationMeta{
		GeneratedAt: time.Date(2026, 4, 27, 0, 0, 0, 0, time.UTC),
		Cluster:     "prod",
		Database:    "myapp",
	})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	var sb strings.Builder
	if err := emit.Render(&sb, migration, emit.DefaultRenderOptions()); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

// assertContains fails the test if none of the expected strings appear in sql.
func assertContains(t *testing.T, sql string, expected ...string) {
	t.Helper()
	for _, want := range expected {
		if !strings.Contains(sql, want) {
			t.Errorf("expected %q in output\ngot:\n%s", want, sql)
		}
	}
}

// assertNotContains fails the test if any of the forbidden strings appear in sql.
func assertNotContains(t *testing.T, sql string, forbidden ...string) {
	t.Helper()
	for _, bad := range forbidden {
		if strings.Contains(sql, bad) {
			t.Errorf("unexpected %q in output\ngot:\n%s", bad, sql)
		}
	}
}
