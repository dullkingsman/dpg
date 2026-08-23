//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripFunctionRenamedFromCrossSchema is diffTable's
// TestRoundtripTableRenamedFromCrossSchema counterpart for Function: before
// this fix, a cross-schema RENAMED FROM matched identity and renamed the
// function in place but never actually moved it to the new schema (the
// same latent gap Table/View/Type/Collation had, just never checked for
// Function/Procedure/Aggregate/the opaque-body kinds since they picked up
// RenamedFromSchema generically during an earlier, unrelated pass).
func TestRoundtripFunctionRenamedFromCrossSchema(t *testing.T) {
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

	v1 := `SCHEMA old_schema {}

FUNCTION old_schema.calc_total(n INTEGER) RETURNS INTEGER LANGUAGE plpgsql AS $$ BEGIN RETURN n; END; $$ {}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	funcOID := func(qualified string) *int64 {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT (to_regprocedure($1))::oid::bigint`, qualified)
		if err != nil {
			t.Fatalf("query oid for %s: %v", qualified, err)
		}
		defer rows.Close()
		if !rows.Next() {
			return nil
		}
		var oid *int64
		if err := rows.Scan(&oid); err != nil {
			t.Fatalf("scan oid for %s: %v", qualified, err)
		}
		return oid
	}

	oldOID := funcOID("old_schema.calc_total(integer)")
	if oldOID == nil {
		t.Fatalf("old_schema.calc_total does not exist after initial apply")
	}

	v2 := `SCHEMA old_schema {}

SCHEMA new_schema {}

FUNCTION new_schema.calc_total(n INTEGER) RETURNS INTEGER LANGUAGE plpgsql AS $$ BEGIN RETURN n; END; $$ {
    RENAMED FROM old_schema.calc_total;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if funcOID("old_schema.calc_total(integer)") != nil {
		t.Fatalf("old_schema.calc_total still exists — expected it to be moved away")
	}
	movedOID := funcOID("new_schema.calc_total(integer)")
	if movedOID == nil {
		t.Fatalf("new_schema.calc_total does not exist — the cross-schema RENAMED FROM SET SCHEMA move failed")
	}
	if *movedOID != *oldOID {
		t.Fatalf("new_schema.calc_total has a different OID (%d) than old_schema.calc_total had (%d) — dropped and recreated instead of moved", *movedOID, *oldOID)
	}
}

// TestRoundtripTSDictRenamedFromCrossSchema also proves the
// opaqueBodyForHash generalization: Body embeds the schema-qualified name,
// so a cross-schema move changes that text even when nothing else changed
// — previously this would have misdetected as a body change and forced
// DROP+CREATE instead of matching by RENAMED FROM at all (same class of
// bug as the body-embeds-its-own-name fix from earlier this session, just
// for the schema half of the qualified name).
func TestRoundtripTSDictRenamedFromCrossSchema(t *testing.T) {
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

	v1 := `SCHEMA old_schema {}

TEXT SEARCH DICTIONARY old_schema.simple_dict (TEMPLATE = pg_catalog.simple);`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oldOID := catalogOID(t, ctx, conn, "pg_ts_dict", "dictname", "simple_dict")
	if oldOID == nil {
		t.Fatalf("old_schema.simple_dict does not exist after initial apply")
	}

	v2 := `SCHEMA old_schema {}

SCHEMA new_schema {}

TEXT SEARCH DICTIONARY new_schema.simple_dict (TEMPLATE = pg_catalog.simple) {
    RENAMED FROM old_schema.simple_dict;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	rows, err := conn.QueryRows(ctx, `SELECT n.nspname FROM pg_ts_dict d JOIN pg_namespace n ON n.oid = d.dictnamespace WHERE d.dictname = 'simple_dict'`)
	if err != nil {
		t.Fatalf("query dict namespace: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("simple_dict does not exist after rename")
	}
	var schema string
	_ = rows.Scan(&schema)
	rows.Close()
	if schema != "new_schema" {
		t.Fatalf("expected simple_dict to live in new_schema, got %q — SET SCHEMA move failed", schema)
	}
	newOID := catalogOID(t, ctx, conn, "pg_ts_dict", "dictname", "simple_dict")
	if newOID == nil || *newOID != *oldOID {
		t.Fatalf("simple_dict OID changed (old=%v new=%v) — dropped and recreated instead of moved", oldOID, newOID)
	}
}

// TestRoundtripOperatorClassRenamedFromCrossSchema proves the
// renameOperatorIfUnchanged generalization emits a real
// ALTER OPERATOR CLASS ... USING method SET SCHEMA, not just a rename.
func TestRoundtripOperatorClassRenamedFromCrossSchema(t *testing.T) {
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

	// FAMILY deliberately names a family that never moves and is declared
	// explicitly (not PostgreSQL's own implicit same-schema/same-name
	// auto-family) — isolates this test from a separate, narrower gap
	// found while writing it: ALTER OPERATOR CLASS ... SET SCHEMA does not
	// (and per real PostgreSQL, cannot on its own) move an implicitly
	// auto-created family along with the class, which DPG's FamilySchema
	// fallback doesn't currently account for. That's a pre-existing
	// family-resolution modeling gap, orthogonal to the SET SCHEMA
	// mechanism this test verifies — see the memory note for this session.
	v1 := `SCHEMA old_schema {}

OPERATOR FAMILY old_schema.my_int_ops_fam USING btree;

OPERATOR CLASS old_schema.my_int_ops FOR TYPE integer USING btree FAMILY old_schema.my_int_ops_fam AS
    OPERATOR 1 <,
    OPERATOR 2 <=,
    OPERATOR 3 =,
    OPERATOR 4 >=,
    OPERATOR 5 >,
    FUNCTION 1 btint4cmp(integer, integer);`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	classOID := func() *int64 {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT oc.oid::bigint FROM pg_opclass oc WHERE oc.opcname = 'my_int_ops'`)
		if err != nil {
			t.Fatalf("query opclass oid: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			return nil
		}
		var oid *int64
		_ = rows.Scan(&oid)
		return oid
	}
	classSchema := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT n.nspname FROM pg_opclass oc JOIN pg_namespace n ON n.oid = oc.opcnamespace WHERE oc.opcname = 'my_int_ops'`)
		if err != nil {
			t.Fatalf("query opclass schema: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("my_int_ops does not exist")
		}
		var s string
		_ = rows.Scan(&s)
		return s
	}

	oldOID := classOID()
	if oldOID == nil {
		t.Fatalf("old_schema.my_int_ops does not exist after initial apply")
	}
	if got := classSchema(); got != "old_schema" {
		t.Fatalf("expected initial schema old_schema, got %q", got)
	}

	v2 := `SCHEMA old_schema {}

SCHEMA new_schema {}

OPERATOR FAMILY old_schema.my_int_ops_fam USING btree;

OPERATOR CLASS new_schema.my_int_ops FOR TYPE integer USING btree FAMILY old_schema.my_int_ops_fam AS
    OPERATOR 1 <,
    OPERATOR 2 <=,
    OPERATOR 3 =,
    OPERATOR 4 >=,
    OPERATOR 5 >,
    FUNCTION 1 btint4cmp(integer, integer) {
    RENAMED FROM old_schema.my_int_ops;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := classSchema(); got != "new_schema" {
		t.Fatalf("expected my_int_ops to live in new_schema after the move, got %q", got)
	}
	newOID := classOID()
	if newOID == nil || *newOID != *oldOID {
		t.Fatalf("my_int_ops OID changed (old=%v new=%v) — dropped and recreated instead of moved", oldOID, newOID)
	}
}
