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

// assertOpaqueRoundtrip compiles an inline .dpg fixture, applies it, introspects
// the live catalog, and asserts that re-diffing the desired IR against the
// introspected state yields zero drift. It targets the reliable-tier opaque
// objects, whose reconstructed bodies must deparse identically to the compiler's.
func assertOpaqueRoundtrip(t *testing.T, schema string) {
	t.Helper()
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
	if err := os.WriteFile(f, []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	snap, _ := store.Load("test", "dpgtest")
	var managedLive []pipeline.IRObject
	for _, obj := range liveObjects {
		if _, ok := snap.Objects[obj.QualifiedName()]; ok {
			managedLive = append(managedLive, obj)
		}
	}
	liveSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(liveSnap, managedLive); err != nil {
		t.Fatalf("populate live snapshot: %v", err)
	}

	desired, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	driftOps, err := differ.Diff(desired, liveSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(driftOps) != 0 {
		t.Errorf("drift after apply (%d ops):", len(driftOps))
		for _, op := range driftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}
}

func TestRoundtripPublication(t *testing.T) {
	assertOpaqueRoundtrip(t, `PUBLICATION my_pub FOR ALL TABLES;`)
}

func TestRoundtripForeignInfra(t *testing.T) {
	assertOpaqueRoundtrip(t, `FOREIGN DATA WRAPPER dummy_fdw;
SERVER dummy_srv FOREIGN DATA WRAPPER dummy_fdw;
USER MAPPING FOR PUBLIC SERVER dummy_srv;`)
}

// TestRoundtripIndexVariants applies a table carrying every index variant —
// unique, multi-column sort order (DESC/ASC + NULLS), partial (WHERE), covering
// (INCLUDE), expression, and a non-btree method — and asserts zero drift. This
// exercises the parse → createIndex → introspect round-trip for the index class
// that repeatedly hid apply-only defects (sort-order corrupting the column,
// INCLUDE silently dropped).
func TestRoundtripIndexVariants(t *testing.T) {
	assertOpaqueRoundtrip(t, `TABLE t (a INTEGER, b INTEGER, c TEXT, e TEXT) {
    INDICES {
        i_uniq UNIQUE (a);
        i_sort (c DESC NULLS LAST, b);
        i_partial (b) WHERE (b > 0);
        i_cover (a) INCLUDE (c, b);
        i_expr (lower(e));
        i_gin (to_tsvector('english', e)) USING gin;
    }
}`)
}
