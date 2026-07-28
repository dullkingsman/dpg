package graph_test

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/graph"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

var pos = pipeline.SourcePos{File: "test.dpg", Line: 1, Col: 1}

func schema(name string) *ir.Schema {
	return &ir.Schema{Name: name, SrcPos: pos}
}

func table(schema, name string, constraints ...*ir.Constraint) *ir.Table {
	return &ir.Table{Schema: schema, Name: name, SrcPos: pos, Constraints: constraints}
}

func fk(expr string, deferrable bool) *ir.Constraint {
	return &ir.Constraint{Type: "FOREIGN KEY", Expr: expr, Deferrable: deferrable, Pos: pos}
}

func enumType(schema, name string) *ir.Type {
	return &ir.Type{Schema: schema, Name: name, Variant: "ENUM", SrcPos: pos}
}

func columnWithType(colName, schema, typeName string) *ir.Column {
	return &ir.Column{
		Name:   colName,
		Type:   ir.TypeRef{Schema: schema, Name: typeName},
		SrcPos: pos,
	}
}

func sortObjects(t *testing.T, objects []pipeline.IRObject) []pipeline.IRObject {
	t.Helper()
	r := graph.New()
	sorted, err := r.Sort(objects)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	return sorted
}

func indexOf(sorted []pipeline.IRObject, qualName string) int {
	for i, o := range sorted {
		if o.QualifiedName() == qualName {
			return i
		}
	}
	return -1
}

func assertBefore(t *testing.T, sorted []pipeline.IRObject, before, after string) {
	t.Helper()
	bi := indexOf(sorted, before)
	ai := indexOf(sorted, after)
	if bi < 0 {
		t.Errorf("%q not found in sorted output", before)
		return
	}
	if ai < 0 {
		t.Errorf("%q not found in sorted output", after)
		return
	}
	if bi >= ai {
		t.Errorf("expected %q (pos %d) before %q (pos %d)", before, bi, after, ai)
	}
}

// ── Basic ordering ────────────────────────────────────────────────────────────

func TestSort_EmptyInput(t *testing.T) {
	r := graph.New()
	out, err := r.Sort(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty output, got %d", len(out))
	}
}

func TestSort_SchemaBeforeTable(t *testing.T) {
	objects := []pipeline.IRObject{
		table("iam", "users"),
		schema("iam"),
	}
	sorted := sortObjects(t, objects)
	assertBefore(t, sorted, "iam", "iam.users")
}

func TestSort_TypeBeforeTable(t *testing.T) {
	tbl := table("app", "orders")
	tbl.Columns = []*ir.Column{columnWithType("status", "app", "order_status")}
	objects := []pipeline.IRObject{
		tbl,
		schema("app"),
		enumType("app", "order_status"),
	}
	sorted := sortObjects(t, objects)
	assertBefore(t, sorted, "app.order_status", "app.orders")
}

func TestSort_FKDependency(t *testing.T) {
	orders := table("app", "orders",
		fk(`FOREIGN KEY (user_id) REFERENCES app.users (id)`, false),
	)
	users := table("app", "users")
	objects := []pipeline.IRObject{
		schema("app"),
		orders,
		users,
	}
	sorted := sortObjects(t, objects)
	assertBefore(t, sorted, "app.users", "app.orders")
}

func TestSort_UnqualifiedFKSameSchema(t *testing.T) {
	orders := table("app", "orders",
		fk(`FOREIGN KEY (user_id) REFERENCES users (id)`, false),
	)
	users := table("app", "users")
	objects := []pipeline.IRObject{schema("app"), orders, users}
	sorted := sortObjects(t, objects)
	assertBefore(t, sorted, "app.users", "app.orders")
}

// ── Circular FK via DEFERRABLE ─────────────────────────────────────────────────

func TestSort_CircularDeferrableFKResolved(t *testing.T) {
	a := table("pub", "a", fk(`FOREIGN KEY (b_id) REFERENCES pub.b (id)`, true))
	b := table("pub", "b", fk(`FOREIGN KEY (a_id) REFERENCES pub.a (id)`, true))
	objects := []pipeline.IRObject{schema("pub"), a, b}

	r := graph.New()
	sorted, err := r.Sort(objects)
	if err != nil {
		t.Fatalf("Sort returned error for resolvable circular FKs: %v", err)
	}
	if len(sorted) != 3 {
		t.Errorf("expected 3 objects, got %d", len(sorted))
	}
}

func TestSort_CircularNonDeferrableFKErrors(t *testing.T) {
	a := table("pub", "a", fk(`FOREIGN KEY (b_id) REFERENCES pub.b (id)`, false))
	b := table("pub", "b", fk(`FOREIGN KEY (a_id) REFERENCES pub.a (id)`, false))
	objects := []pipeline.IRObject{schema("pub"), a, b}

	r := graph.New()
	_, err := r.Sort(objects)
	if err == nil {
		t.Fatal("expected error for non-deferrable circular FK")
	}
}

// ── Unknown FK / type targets in managed schemas ───────────────────────────────

func TestSort_UnresolvedFKInManagedSchemaErrors(t *testing.T) {
	// "app" schema is in source, but "app.nonexistent" is not.
	orders := table("app", "orders",
		fk(`FOREIGN KEY (x_id) REFERENCES app.nonexistent (id)`, false),
	)
	objects := []pipeline.IRObject{schema("app"), orders}

	r := graph.New()
	_, err := r.Sort(objects)
	if err == nil {
		t.Fatal("expected error for unresolved FK target in managed schema")
	}
}

func TestSort_UnresolvedTypeInManagedSchemaErrors(t *testing.T) {
	tbl := table("app", "things")
	tbl.Columns = []*ir.Column{columnWithType("kind", "app", "ghost_type")}
	objects := []pipeline.IRObject{schema("app"), tbl}

	r := graph.New()
	_, err := r.Sort(objects)
	if err == nil {
		t.Fatal("expected error for unresolved type in managed schema")
	}
}

func TestSort_UnresolvedFKInExternalSchemaAllowed(t *testing.T) {
	// "extensions" schema is NOT in source, so FK to it should be silently allowed.
	orders := table("app", "orders",
		fk(`FOREIGN KEY (geom) REFERENCES extensions.geometry (id)`, false),
	)
	objects := []pipeline.IRObject{schema("app"), orders}

	sorted := sortObjects(t, objects)
	if len(sorted) != 2 {
		t.Errorf("expected 2 objects, got %d", len(sorted))
	}
}

// ── View heuristic ────────────────────────────────────────────────────────────

func TestSort_ViewAfterAllTables(t *testing.T) {
	v := &ir.View{Schema: "app", Name: "user_view", SrcPos: pos}
	u := table("app", "users")
	o := table("app", "orders")
	s := schema("app")
	objects := []pipeline.IRObject{v, u, o, s}
	sorted := sortObjects(t, objects)
	assertBefore(t, sorted, "app.users", "app.user_view")
	assertBefore(t, sorted, "app.orders", "app.user_view")
}

// A column whose custom type is written UNQUALIFIED (as introspected columns are
// — e.g. "mood" rather than "public.mood") must still order the type before the
// table, resolved against the table's own schema. Regression: dump emitted the
// table before its enum, so apply-to-fresh-DB failed with "type does not exist".
func TestSort_UnqualifiedTypeSameSchema(t *testing.T) {
	tbl := table("app", "orders")
	tbl.Columns = []*ir.Column{columnWithType("status", "", "order_status")}
	objects := []pipeline.IRObject{
		tbl,
		schema("app"),
		enumType("app", "order_status"),
	}
	sorted := sortObjects(t, objects)
	assertBefore(t, sorted, "app.order_status", "app.orders")
}

// An unqualified type name with no matching custom type is a built-in and must
// not error or create a dependency.
func TestSort_UnqualifiedBuiltinTypeNoError(t *testing.T) {
	tbl := table("app", "orders")
	tbl.Columns = []*ir.Column{columnWithType("n", "", "integer")}
	objects := []pipeline.IRObject{tbl, schema("app")}
	if _, err := graph.New().Sort(objects); err != nil {
		t.Fatalf("unqualified built-in type must not error: %v", err)
	}
}

// ── Operator class → family, TS config → parser, TS dict → template ────────────
//
// These three mirror the same shape: an opaque object that names another
// opaque object by qualified reference, needing a real dependency edge so
// CREATE order is correct regardless of declaration order — unlike a table's
// column type or FK reference, PostgreSQL's CREATE OPERATOR FAMILY/TEXT
// SEARCH PARSER/TEXT SEARCH TEMPLATE have no IF NOT EXISTS to self-heal a
// wrong order, and CREATE OPERATOR CLASS ... FAMILY x / CREATE TEXT SEARCH
// CONFIGURATION ... (PARSER = x) / CREATE TEXT SEARCH DICTIONARY ... (TEMPLATE
// = x) all hard-error if x doesn't exist yet. Each test deliberately declares
// the referencing object BEFORE its reference in the input slice, so a
// passing assertBefore proves the edge actually reorders things rather than
// coincidentally matching input order.

func opClass(schema, name, am, famSchema, famName string) *ir.OperatorClass {
	return &ir.OperatorClass{
		Schema: schema, Name: name, AccessMethod: am,
		FamilySchema: famSchema, FamilyName: famName, SrcPos: pos,
	}
}

func opFamily(schema, name, am string) *ir.OperatorFamily {
	return &ir.OperatorFamily{Schema: schema, Name: name, AccessMethod: am, SrcPos: pos}
}

func TestSort_OperatorClassBeforeFamily_QualifiedRef(t *testing.T) {
	objects := []pipeline.IRObject{
		opClass("app", "my_ops", "btree", "app", "my_fam"),
		opFamily("app", "my_fam", "btree"),
	}
	sorted := sortObjects(t, objects)
	assertBefore(t, sorted, "app.my_fam USING btree FAMILY", "app.my_ops USING btree")
}

// Unqualified FAMILY name (FamilySchema empty) resolves against the class's
// own schema — same convention as an unqualified column type reference.
func TestSort_OperatorClassBeforeFamily_UnqualifiedRef(t *testing.T) {
	objects := []pipeline.IRObject{
		opClass("app", "my_ops", "btree", "", "my_fam"),
		opFamily("app", "my_fam", "btree"),
	}
	sorted := sortObjects(t, objects)
	assertBefore(t, sorted, "app.my_fam USING btree FAMILY", "app.my_ops USING btree")
}

// A class whose FAMILY isn't part of the managed object set (e.g. a built-in
// pg_catalog family, or simply omitted in hand-written source relying on
// PostgreSQL's own auto-creation) must not error — mirrors the unqualified
// built-in type case above.
func TestSort_OperatorClassNoFamilyReferenceNoError(t *testing.T) {
	objects := []pipeline.IRObject{opClass("app", "my_ops", "btree", "", "")}
	if _, err := graph.New().Sort(objects); err != nil {
		t.Fatalf("operator class with no FAMILY reference must not error: %v", err)
	}
}

func tsConfig(schema, name, parserSchema, parserName string) *ir.TSConfig {
	return &ir.TSConfig{Schema: schema, Name: name, ParserSchema: parserSchema, ParserName: parserName, SrcPos: pos}
}

func tsParser(schema, name string) *ir.TSParser {
	return &ir.TSParser{Schema: schema, Name: name, SrcPos: pos}
}

func TestSort_TSConfigBeforeParser(t *testing.T) {
	objects := []pipeline.IRObject{
		tsConfig("app", "my_cfg", "app", "my_parser"),
		tsParser("app", "my_parser"),
	}
	sorted := sortObjects(t, objects)
	assertBefore(t, sorted, "app.my_parser", "app.my_cfg")
}

// The overwhelmingly common case: PARSER references a pg_catalog built-in
// (e.g. "default"), which is never part of the managed object set — must not
// error.
func TestSort_TSConfigBuiltinParserNoError(t *testing.T) {
	objects := []pipeline.IRObject{tsConfig("app", "my_cfg", "pg_catalog", "default")}
	if _, err := graph.New().Sort(objects); err != nil {
		t.Fatalf("TS config referencing a built-in parser must not error: %v", err)
	}
}

func tsDict(schema, name, tmplSchema, tmplName string) *ir.TSDict {
	return &ir.TSDict{Schema: schema, Name: name, TemplateSchema: tmplSchema, TemplateName: tmplName, SrcPos: pos}
}

func tsTemplate(schema, name string) *ir.TSTemplate {
	return &ir.TSTemplate{Schema: schema, Name: name, SrcPos: pos}
}

func TestSort_TSDictBeforeTemplate(t *testing.T) {
	objects := []pipeline.IRObject{
		tsDict("app", "my_dict", "app", "my_tmpl"),
		tsTemplate("app", "my_tmpl"),
	}
	sorted := sortObjects(t, objects)
	assertBefore(t, sorted, "app.my_tmpl", "app.my_dict")
}

func TestSort_TSDictBuiltinTemplateNoError(t *testing.T) {
	objects := []pipeline.IRObject{tsDict("app", "my_dict", "pg_catalog", "simple")}
	if _, err := graph.New().Sort(objects); err != nil {
		t.Fatalf("TS dict referencing a built-in template must not error: %v", err)
	}
}
