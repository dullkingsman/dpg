package merger_test

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/merger"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

var pos = pipeline.SourcePos{File: "a.dpg", Line: 1, Col: 1}
var pos2 = pipeline.SourcePos{File: "b.dpg", Line: 1, Col: 1}

func ptr[T any](v T) *T { return &v }

func merge(t *testing.T, objects ...pipeline.IRObject) []pipeline.IRObject {
	t.Helper()
	m := merger.New()
	out, err := m.Merge(objects)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	return out
}

// ── No-op: single objects pass through ────────────────────────────────────────

func TestMerge_SingleObjects(t *testing.T) {
	objects := []pipeline.IRObject{
		&ir.Schema{Name: "app", SrcPos: pos},
		&ir.Table{Schema: "app", Name: "users", SrcPos: pos},
	}
	out := merge(t, objects...)
	if len(out) != 2 {
		t.Errorf("expected 2, got %d", len(out))
	}
}

// ── Table merge ───────────────────────────────────────────────────────────────

func TestMerge_Table_ColumnsUnioned(t *testing.T) {
	a := &ir.Table{
		Schema: "app", Name: "users", SrcPos: pos,
		Columns: []*ir.Column{{Name: "id", SrcPos: pos}},
	}
	b := &ir.Table{
		Schema: "app", Name: "users", SrcPos: pos2,
		Columns: []*ir.Column{{Name: "email", SrcPos: pos2}},
	}
	out := merge(t, a, b)
	if len(out) != 1 {
		t.Fatalf("expected 1 merged table, got %d", len(out))
	}
	tbl := out[0].(*ir.Table)
	if len(tbl.Columns) != 2 {
		t.Errorf("Columns: expected 2, got %d", len(tbl.Columns))
	}
	if tbl.Columns[0].Name != "id" || tbl.Columns[1].Name != "email" {
		t.Errorf("column names: got %q, %q", tbl.Columns[0].Name, tbl.Columns[1].Name)
	}
}

func TestMerge_Table_ScalarLastWins(t *testing.T) {
	owner1 := ptr("alice")
	owner2 := ptr("bob")
	a := &ir.Table{Schema: "app", Name: "t", SrcPos: pos, Owner: owner1}
	b := &ir.Table{Schema: "app", Name: "t", SrcPos: pos2, Owner: owner2}
	out := merge(t, a, b)
	tbl := out[0].(*ir.Table)
	if *tbl.Owner != "bob" {
		t.Errorf("Owner (last-wins): got %q", *tbl.Owner)
	}
}

func TestMerge_Table_IndexesUnioned(t *testing.T) {
	a := &ir.Table{
		Schema: "app", Name: "t", SrcPos: pos,
		Indexes: []*ir.Index{{Name: "idx_a", Columns: []pipeline.IndexColumn{{Name: "a"}}}},
	}
	b := &ir.Table{
		Schema: "app", Name: "t", SrcPos: pos2,
		Indexes: []*ir.Index{{Name: "idx_b", Columns: []pipeline.IndexColumn{{Name: "b"}}}},
	}
	out := merge(t, a, b)
	tbl := out[0].(*ir.Table)
	if len(tbl.Indexes) != 2 {
		t.Errorf("Indexes: expected 2, got %d", len(tbl.Indexes))
	}
}

func TestMerge_Table_DuplicateIndexDeduped(t *testing.T) {
	idx := &ir.Index{Name: "idx_same", Columns: []pipeline.IndexColumn{{Name: "x"}}}
	a := &ir.Table{Schema: "app", Name: "t", SrcPos: pos, Indexes: []*ir.Index{idx}}
	b := &ir.Table{Schema: "app", Name: "t", SrcPos: pos2, Indexes: []*ir.Index{idx}}
	out := merge(t, a, b)
	tbl := out[0].(*ir.Table)
	if len(tbl.Indexes) != 1 {
		t.Errorf("Indexes: duplicate should be deduped; got %d", len(tbl.Indexes))
	}
}

func TestMerge_Table_ConstraintsUnioned(t *testing.T) {
	a := &ir.Table{
		Schema: "app", Name: "t", SrcPos: pos,
		Constraints: []*ir.Constraint{{Name: "pk_t", Type: "PRIMARY KEY", Expr: "PRIMARY KEY (id)"}},
	}
	b := &ir.Table{
		Schema: "app", Name: "t", SrcPos: pos2,
		Constraints: []*ir.Constraint{{Name: "uq_t_email", Type: "UNIQUE", Expr: "UNIQUE (email)"}},
	}
	out := merge(t, a, b)
	tbl := out[0].(*ir.Table)
	if len(tbl.Constraints) != 2 {
		t.Errorf("Constraints: expected 2, got %d", len(tbl.Constraints))
	}
}

func TestMerge_Table_ProtectedAndDropCascadeOrred(t *testing.T) {
	a := &ir.Table{Schema: "app", Name: "t", SrcPos: pos, Protected: true}
	b := &ir.Table{Schema: "app", Name: "t", SrcPos: pos2, DropCascade: true}
	out := merge(t, a, b)
	tbl := out[0].(*ir.Table)
	if !tbl.Protected {
		t.Error("Protected should be true")
	}
	if !tbl.DropCascade {
		t.Error("DropCascade should be true")
	}
}

// ── Schema merge ──────────────────────────────────────────────────────────────

func TestMerge_Schema_OwnerLastWins(t *testing.T) {
	a := &ir.Schema{Name: "app", SrcPos: pos, Owner: ptr("alice")}
	b := &ir.Schema{Name: "app", SrcPos: pos2, Owner: ptr("bob")}
	out := merge(t, a, b)
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
	s := out[0].(*ir.Schema)
	if *s.Owner != "bob" {
		t.Errorf("Owner: got %q", *s.Owner)
	}
}

// ── View merge ────────────────────────────────────────────────────────────────

func TestMerge_View_GrantsAppended(t *testing.T) {
	a := &ir.View{
		Schema: "app", Name: "v", SrcPos: pos,
		Grants: []ir.Grant{{Roles: []string{"r1"}, Privileges: []string{"SELECT"}}},
	}
	b := &ir.View{
		Schema: "app", Name: "v", SrcPos: pos2,
		Grants: []ir.Grant{{Roles: []string{"r2"}, Privileges: []string{"SELECT"}}},
	}
	out := merge(t, a, b)
	v := out[0].(*ir.View)
	if len(v.Grants) != 2 {
		t.Errorf("Grants: expected 2, got %d", len(v.Grants))
	}
}

// ── Type (ENUM) merge ─────────────────────────────────────────────────────────

func TestMerge_EnumValuesUnioned(t *testing.T) {
	a := &ir.Type{Schema: "app", Name: "status", Variant: "ENUM", SrcPos: pos, EnumValues: []string{"active", "inactive"}}
	b := &ir.Type{Schema: "app", Name: "status", Variant: "ENUM", SrcPos: pos2, EnumValues: []string{"inactive", "pending"}}
	out := merge(t, a, b)
	tp := out[0].(*ir.Type)
	if len(tp.EnumValues) != 3 {
		t.Errorf("EnumValues: expected 3, got %d: %v", len(tp.EnumValues), tp.EnumValues)
	}
}

// ── Function merge ────────────────────────────────────────────────────────────

func TestMerge_Function_GrantsAppended(t *testing.T) {
	a := &ir.Function{Schema: "app", Name: "f", SrcPos: pos, Grants: []ir.Grant{{Roles: []string{"r1"}, Privileges: []string{"EXECUTE"}}}}
	b := &ir.Function{Schema: "app", Name: "f", SrcPos: pos2, Grants: []ir.Grant{{Roles: []string{"r2"}, Privileges: []string{"EXECUTE"}}}}
	out := merge(t, a, b)
	f := out[0].(*ir.Function)
	if len(f.Grants) != 2 {
		t.Errorf("Grants: expected 2, got %d", len(f.Grants))
	}
}

// ── Different types with same name are separate objects ────────────────────────

func TestMerge_SameNameDifferentType(t *testing.T) {
	tbl := &ir.Table{Schema: "app", Name: "status", SrcPos: pos}
	tp := &ir.Type{Schema: "app", Name: "status", Variant: "ENUM", SrcPos: pos2}
	out := merge(t, tbl, tp)
	if len(out) != 2 {
		t.Errorf("expected 2 objects (different types, same name), got %d", len(out))
	}
}

// ── Column-level merge ────────────────────────────────────────────────────────

func TestMerge_ColumnScalarLastWins(t *testing.T) {
	col := func(comment string, pos pipeline.SourcePos) *ir.Column {
		c := &ir.Column{Name: "email", SrcPos: pos}
		c.Comment = ptr(comment)
		return c
	}
	a := &ir.Table{Schema: "app", Name: "users", SrcPos: pos, Columns: []*ir.Column{col("old", pos)}}
	b := &ir.Table{Schema: "app", Name: "users", SrcPos: pos2, Columns: []*ir.Column{col("new", pos2)}}
	out := merge(t, a, b)
	tbl := out[0].(*ir.Table)
	if *tbl.Columns[0].Comment != "new" {
		t.Errorf("Column.Comment (last-wins): got %q", *tbl.Columns[0].Comment)
	}
}
