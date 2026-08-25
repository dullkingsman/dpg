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

// noDriftAgainstLive re-diffs files' compiled desired state against both the
// in-memory store snapshot and a freshly introspected live snapshot,
// failing if either produces ops — the same two-level no-op check used by
// integration/temporal_keys_test.go.
func noDriftAgainstLive(
	t *testing.T, ctx context.Context, conn *executor.PgxConn, files []string, dir string,
	differ pipeline.Differ, store *memStore,
) {
	t.Helper()
	desired, _, err := compiler.Compile(files, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	base, _ := store.Load("test", "dpgtest")
	ops, err := differ.Diff(desired, base)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no-op re-diff against stored snapshot, got %d ops:", len(ops))
		for _, op := range ops {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}

	ci := introspect.New()
	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var managedLive []pipeline.IRObject
	for _, obj := range liveObjects {
		if _, ok := base.Objects[obj.QualifiedName()]; ok {
			managedLive = append(managedLive, obj)
		}
	}
	liveSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(liveSnap, managedLive); err != nil {
		t.Fatalf("populate live snapshot: %v", err)
	}
	liveDriftOps, err := differ.Diff(desired, liveSnap)
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

// TestRoundtripTypedTable is the live regression guard for RFC Section
// 7.1's "Form 2" typed table (CREATE TABLE ... OF type_name) and Section
// 7.11's OF type_name/NOT OF ALTER: previously ir.Table had no OfType field
// at all, a Tenet-1 gap with no DPG equivalent.
//
// Confirms live: a typed table's OF association lands in pg_class.reloftype
// (with buildTable's introspectColumns-vs-desired asymmetry — a typed
// table's inherited attributes are real pg_attribute columns live, but
// never modeled as ir.Column on the desired side — correctly producing zero
// spurious column ops), a table-level constraint declared in the OF-type
// parenthesized list applies, switching to a different (structurally
// identical) type runs a real ALTER TABLE ... OF, and a second plan against
// freshly introspected live state is a genuine no-op at every stage.
// Detaching (NOT OF) is verified directly against the server, not through
// the full DPG plan/apply lifecycle — switching a typed table back to a
// plain declared column list is a separate, unscoped feature (see
// ir.Table.OfType's doc comment on the narrower WITH OPTIONS gap).
func TestRoundtripTypedTable(t *testing.T) {
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

	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
			t.Fatalf("write schema: %v", err)
		}
	}

	ofTypeName := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
			SELECT ot.typname
			FROM pg_class c
			JOIN pg_type ot ON ot.oid = c.reloftype
			WHERE c.relname = 'shipping_address'`)
		if err != nil {
			t.Fatalf("query reloftype: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			return ""
		}
		var name string
		_ = rows.Scan(&name)
		return name
	}

	// Stage 1: bare typed table. address2 is declared here already (unused
	// until stage 3) so its CREATE TYPE lands in an earlier migration,
	// sidestepping any question of whether the dependency graph orders a
	// same-migration CREATE TYPE before an ALTER TABLE ... OF referencing
	// it — a separate, orthogonal concern from this feature's own grammar.
	write(`
TYPE address AS (street TEXT, city TEXT, zip TEXT) {}
TYPE address2 AS (street TEXT, city TEXT, zip TEXT) {}
TABLE shipping_address OF address;`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := ofTypeName(); got != "address" {
		t.Fatalf("reloftype after create: got %q, want %q", got, "address")
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	// Stage 2: add a table-level constraint via the OF-type parenthesized
	// list.
	write(`
TYPE address AS (street TEXT, city TEXT, zip TEXT) {}
TYPE address2 AS (street TEXT, city TEXT, zip TEXT) {}
TABLE shipping_address OF address (
    CONSTRAINT zip_format CHECK (zip ~ '^[0-9]{5}$')
);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	rows, err := conn.QueryRows(ctx, `SELECT 1 FROM pg_constraint WHERE conname = 'zip_format'`)
	if err != nil {
		t.Fatalf("query constraint: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("zip_format constraint not found after apply")
	}
	rows.Close()
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	// Stage 3: switch OF association to the second, structurally identical
	// type (already created in stage 1) — real ALTER TABLE ... OF,
	// CAUTION, not a recreate.
	write(`
TYPE address AS (street TEXT, city TEXT, zip TEXT) {}
TYPE address2 AS (street TEXT, city TEXT, zip TEXT) {}
TABLE shipping_address OF address2 (
    CONSTRAINT zip_format CHECK (zip ~ '^[0-9]{5}$')
);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := ofTypeName(); got != "address2" {
		t.Fatalf("reloftype after OF-type switch: got %q, want %q", got, "address2")
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	// Detaching (NOT OF) verified directly, not through the DPG lifecycle
	// (see doc comment above).
	if _, err := conn.Exec(ctx, `ALTER TABLE shipping_address NOT OF;`); err != nil {
		t.Fatalf("ALTER TABLE ... NOT OF: %v", err)
	}
	if got := ofTypeName(); got != "" {
		t.Fatalf("reloftype after NOT OF: got %q, want empty (detached)", got)
	}
}

// TestRoundtripTableAccessMethod is the live regression guard for RFC
// Section 7.1's USING method (table access method) clause and Section
// 7.11's SET ACCESS METHOD ALTER: previously ir.Table had no AccessMethod
// field at all.
//
// Registers a second table access method reusing PostgreSQL's own built-in
// heap handler (no extension needed) so the test can prove a genuinely
// non-default access method round-trips, rather than only exercising the
// "heap" default every fresh table already has. Confirms live: USING lands
// in pg_class.relam at CREATE time, a second plan against freshly
// introspected live state is a genuine no-op, changing it runs a real
// ALTER TABLE ... SET ACCESS METHOD, and clearing a previously-declared
// value runs SET ACCESS METHOD DEFAULT (reverting relam to the cluster's
// actual default, "heap" — there is no bare "unset" form in real
// PostgreSQL's grammar).
func TestRoundtripTableAccessMethod(t *testing.T) {
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

	if _, err := conn.Exec(ctx, `CREATE ACCESS METHOD heap2 TYPE TABLE HANDLER heap_tableam_handler;`); err != nil {
		t.Fatalf("create access method: %v", err)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
			t.Fatalf("write schema: %v", err)
		}
	}

	accessMethod := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
			SELECT am.amname
			FROM pg_class c
			JOIN pg_am am ON am.oid = c.relam
			WHERE c.relname = 'events'`)
		if err != nil {
			t.Fatalf("query relam: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("events table not found")
		}
		var name string
		_ = rows.Scan(&name)
		return name
	}

	write(`TABLE events (id BIGINT NOT NULL) USING heap2;`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := accessMethod(); got != "heap2" {
		t.Fatalf("relam after create: got %q, want %q", got, "heap2")
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	write(`TABLE events (id BIGINT NOT NULL);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := accessMethod(); got != "heap" {
		t.Fatalf("relam after clearing USING: got %q, want %q (cluster default)", got, "heap")
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
