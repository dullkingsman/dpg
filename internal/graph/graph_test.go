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
