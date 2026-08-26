//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dpg/internal/diff"
	"github.com/thec1oud/dpg/internal/emit"
	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestRoundtripOperatorClassImplicitAutoFamilySetSchema is the live
// regression guard for the "narrower gap" flagged in
// TestRoundtripOperatorClassRenamedFromCrossSchema's own doc comment: a
// class relying on PostgreSQL's implicit same-schema/same-name auto-created
// family (no FAMILY clause declared at all) previously misdiffed a
// class-only SET SCHEMA move as a DESTRUCTIVE drop+recreate, because
// diffOperatorClass's desired-side family-schema fallback assumed the
// family followed the class into its new schema. Confirmed live that real
// PostgreSQL never moves the auto-created family on a class-only SET
// SCHEMA — it stays exactly where the class was originally created.
//
// Confirms live: moving a class with an implicit auto-family runs a real
// ALTER OPERATOR CLASS ... SET SCHEMA (stable OID, not dropped and
// recreated), the family itself genuinely stays behind in its original
// schema, and a second plan against freshly introspected live state is a
// genuine no-op.
func TestRoundtripOperatorClassImplicitAutoFamilySetSchema(t *testing.T) {
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
	classAndFamilySchema := func() (class, family string) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
SELECT cn.nspname, fn.nspname
FROM pg_opclass oc
JOIN pg_namespace cn ON cn.oid = oc.opcnamespace
JOIN pg_opfamily f ON f.oid = oc.opcfamily
JOIN pg_namespace fn ON fn.oid = f.opfnamespace
WHERE oc.opcname = 'my_int_ops'`)
		if err != nil {
			t.Fatalf("query opclass/opfamily schema: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("my_int_ops does not exist")
		}
		if err := rows.Scan(&class, &family); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return class, family
	}

	v1 := `SCHEMA old_schema {}

OPERATOR CLASS old_schema.my_int_ops FOR TYPE integer USING btree AS
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

	oldOID := classOID()
	if oldOID == nil {
		t.Fatalf("old_schema.my_int_ops does not exist after initial apply")
	}
	classSchema, familySchema := classAndFamilySchema()
	if classSchema != "old_schema" || familySchema != "old_schema" {
		t.Fatalf("expected class and implicit family both in old_schema initially, got class=%q family=%q", classSchema, familySchema)
	}

	v2 := `SCHEMA old_schema {}

SCHEMA new_schema {}

OPERATOR CLASS new_schema.my_int_ops FOR TYPE integer USING btree AS
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

	classSchema, familySchema = classAndFamilySchema()
	if classSchema != "new_schema" {
		t.Fatalf("expected my_int_ops to live in new_schema after the move, got %q", classSchema)
	}
	if familySchema != "old_schema" {
		t.Fatalf("expected the implicit auto-family to stay behind in old_schema (real PostgreSQL never moves it), got %q", familySchema)
	}
	newOID := classOID()
	if newOID == nil || *newOID != *oldOID {
		t.Fatalf("my_int_ops OID changed (old=%v new=%v) — dropped and recreated instead of a targeted SET SCHEMA", oldOID, newOID)
	}

	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
