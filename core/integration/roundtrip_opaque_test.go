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
	"github.com/dullkingsman/dpg/internal/ir"
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

// TestRoundtripOpaqueBodyEditAppliesLive is the live-catalog guard for #3
// (real update path for opaque objects, internal/diff/differ.go diffOpaqueIR):
// previously an offline body edit to an opaque object (here, a COLLATION's
// LOCALE) only ever produced a "-- WARNING: ... manual DROP + recreate
// required" SQL comment — applying it was a silent no-op against a real
// database. This proves the structured DROP + CREATE it emits now is valid
// SQL that a real PostgreSQL instance accepts, and that after applying it the
// live catalog actually reflects the new body (zero drift against the edited
// desired state, not the original one).
func TestRoundtripOpaqueBodyEditAppliesLive(t *testing.T) {
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
	v1 := `COLLATION my_coll (LOCALE = 'C');`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	// Edit the body (same identity, different LOCALE) and diff against the
	// snapshot from the v1 apply — this must route through diffOpaqueIR's
	// offline body-hash-changed branch.
	v2 := `COLLATION my_coll (LOCALE = 'POSIX');`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	desired2, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile v2: %v", err)
	}
	snap, _ := store.Load("test", "dpgtest")
	ops, err := differ.Diff(desired2, snap)
	if err != nil {
		t.Fatalf("diff (body edit): %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("expected structured DROP+CREATE ops for the body edit, got none")
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), "manual DROP + recreate required") {
			t.Fatalf("must not fall back to the manual warning for collation: %s", op.SQL())
		}
	}

	migration, err := emitter.Emit(ops, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := applyExec.Apply(ctx, migration, conn); err != nil {
		t.Fatalf("apply (body edit) against live PostgreSQL: %v", err)
	}

	appliedSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(appliedSnap, desired2); err != nil {
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
	driftOps, err := differ.Diff(desired2, liveSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(driftOps) != 0 {
		t.Errorf("drift after body-edit apply (%d ops) — live catalog doesn't reflect the new body:", len(driftOps))
		for _, op := range driftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}

	rs, err := conn.QueryRows(ctx, `SELECT collcollate FROM pg_collation WHERE collname = 'my_coll'`)
	if err != nil {
		t.Fatalf("query pg_collation: %v", err)
	}
	defer rs.Close()
	if !rs.Next() {
		t.Fatal("my_coll not found in pg_collation after body-edit apply")
	}
	var collate string
	if err := rs.Scan(&collate); err != nil {
		t.Fatalf("scan collcollate: %v", err)
	}
	if !strings.Contains(strings.ToUpper(collate), "POSIX") {
		t.Errorf("live collcollate = %q, want it to reflect the POSIX edit (was 'C')", collate)
	}
}

// TestRoundtripIndexVariants applies a table carrying every index variant —
// unique, multi-column sort order (DESC/ASC + NULLS), partial (WHERE), covering
// (INCLUDE), expression, and a non-btree method — and asserts zero drift. This
// exercises the parse → createIndex → introspect round-trip for the index class
// that repeatedly hid apply-only defects (sort-order corrupting the column,
// INCLUDE silently dropped).
// TestRoundtripDeferredTierObjects applies the "deferred tier" opaque objects
// whose catalog reconstruction was added alongside the reliable tier — an
// operator, an operator family, a text-search dictionary and configuration, and
// an operator class with a full member list — and asserts zero drift. Before
// these introspectors existed, plan --live emitted a spurious CREATE for each
// (introspection returned nothing), so zero drift here proves they are now
// discovered and their reconstructed DDL round-trips.
//
// All five reference only built-in functions/templates so the fixture needs no C
// extension. Text-search parsers and templates are intentionally excluded: their
// START/LEXIZE functions must be C-language internal functions, which cannot be
// created from pure SQL; their introspection queries are still exercised (for
// column-name validity) by every assertOpaqueRoundtrip call, which runs a full
// Introspect over the live catalog.
func TestRoundtripDeferredTierObjects(t *testing.T) {
	assertOpaqueRoundtrip(t, `OPERATOR public.=== (FUNCTION = int4eq, LEFTARG = integer, RIGHTARG = integer);
OPERATOR FAMILY public.rt_fam USING btree;
TEXT SEARCH DICTIONARY public.rt_dict (TEMPLATE = pg_catalog.simple);
TEXT SEARCH CONFIGURATION public.rt_cfg (PARSER = pg_catalog."default");
OPERATOR CLASS public.rt_opc FOR TYPE integer USING btree AS
    OPERATOR 1 <, OPERATOR 2 <=, OPERATOR 3 =, OPERATOR 4 >=, OPERATOR 5 >,
    FUNCTION 1 btint4cmp(integer, integer);`)
}

// TestIntrospectOperatorClassNoSpuriousFamily guards the auto-created-family
// handling. CREATE OPERATOR CLASS without a FAMILY clause makes PostgreSQL
// auto-create a same-named operator family. That family must NOT be introspected
// as a standalone object (it would emit a redundant CREATE OPERATOR FAMILY that
// conflicts with the auto-creation on re-apply), and the class's reconstructed
// body must NOT carry a FAMILY clause naming it. The deptype-based discriminator
// this originally used was dead (auto and explicit families share deptype 'a'),
// so this asserts the name-based signal that replaced it, against a live catalog.
func TestIntrospectOperatorClassNoSpuriousFamily(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `CREATE OPERATOR CLASS rt_auto_opc FOR TYPE integer USING btree AS
        OPERATOR 3 =, FUNCTION 1 btint4cmp(integer, integer)`); err != nil {
		t.Fatalf("create opclass: %v", err)
	}

	objs, err := introspect.New().Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	var classBody string
	for _, o := range objs {
		if fam, ok := o.(*ir.OperatorFamily); ok && fam.Name == "rt_auto_opc" {
			t.Errorf("auto-created family rt_auto_opc was introspected as a standalone object: %q", fam.Body)
		}
		if cls, ok := o.(*ir.OperatorClass); ok && cls.Name == "rt_auto_opc" {
			classBody = cls.Body
		}
	}
	if classBody == "" {
		t.Fatal("operator class rt_auto_opc was not introspected")
	}
	if strings.Contains(strings.ToUpper(classBody), "FAMILY") {
		t.Errorf("class body names its implicit auto-created family (should be omitted): %q", classBody)
	}
}

func TestRoundtripIndexVariants(t *testing.T) {
	assertOpaqueRoundtrip(t, `TABLE t (a INTEGER, b INTEGER, c TEXT, e TEXT) {
    INDICES {
        i_uniq UNIQUE (a);
        i_sort (c DESC NULLS LAST, b);
        i_partial (b) WHERE (b > 0);
        i_cover (a) INCLUDE (c, b);
        i_expr (lower(e));
        i_gin (to_tsvector('english', e)) USING gin;
        i_nulls_nd UNIQUE (b) NULLS NOT DISTINCT;
        i_with (a) WITH (fillfactor = 70);
    }
}`)
}

// TestRoundtripIndexModeB is the live-catalog guard for the Mode-B (RFC §4.8
// singular keyword) fix: previously "INDEX name (...)" outside an INDICES{}
// block was a hard parse error, so it could never reach a live database at
// all. This proves the whole pipeline — parse Mode-B, build IR, emit CREATE
// INDEX, introspect back — round-trips against real PostgreSQL with zero
// drift, and that it can be freely mixed with a Mode-A INDICES{} block on the
// same table.
func TestRoundtripIndexModeB(t *testing.T) {
	assertOpaqueRoundtrip(t, `TABLE t (a INTEGER, b INTEGER) {
    INDEX i_modeb_a (a);
    INDEX i_modeb_uniq UNIQUE (b) NULLS NOT DISTINCT;
    INDICES { i_modea_ab (a, b); }
}`)
}
