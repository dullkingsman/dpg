package ir_test

import (
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"

	"github.com/dullkingsman/dpg/internal/ir"
)

func TestRenderCreateFunctionSQLDollarQuoteCollision(t *testing.T) {
	fn := &ir.Function{
		Schema:     "public",
		Name:       "f",
		ReturnType: ir.TypeRef{Name: "text"},
		Args:       []ir.FuncArg{{Name: "n", Mode: "IN", Type: ir.TypeRef{Name: "integer"}}},
		Attrs: ir.FuncAttrs{
			Language: "sql",
			Body:     "SELECT $$literal$$",
		},
	}
	sql := ir.RenderCreateFunctionSQL(fn)
	if _, err := pg_query.Parse(sql); err != nil {
		t.Fatalf("rendered SQL with a body containing a literal $$ failed to parse: %v\nSQL: %s", err, sql)
	}
}

func TestRenderCreateFunctionSQLArgModes(t *testing.T) {
	fn := &ir.Function{
		Schema:     "public",
		Name:       "f",
		ReturnType: ir.TypeRef{Name: "record"},
		Args: []ir.FuncArg{
			{Name: "a", Mode: "IN", Type: ir.TypeRef{Name: "integer"}},
			{Name: "b", Mode: "OUT", Type: ir.TypeRef{Name: "integer"}},
			{Name: "rest", Mode: "VARIADIC", Type: ir.TypeRef{Name: "text"}},
		},
		Attrs: ir.FuncAttrs{
			Language: "sql",
			Body:     "SELECT a, rest",
		},
	}
	sql := ir.RenderCreateFunctionSQL(fn)
	if _, err := pg_query.Parse(sql); err != nil {
		t.Fatalf("rendered SQL with IN/OUT/VARIADIC args failed to parse: %v\nSQL: %s", err, sql)
	}
}

func TestRenderCreateFunctionSQLReturnsTable(t *testing.T) {
	fn := &ir.Function{
		Schema: "public",
		Name:   "f",
		Args: []ir.FuncArg{
			{Name: "n", Mode: "IN", Type: ir.TypeRef{Name: "integer"}},
			{Name: "col1", Mode: "TABLE", Type: ir.TypeRef{Name: "integer"}},
		},
		Attrs: ir.FuncAttrs{
			Language: "sql",
			Body:     "SELECT n",
		},
	}
	sql := ir.RenderCreateFunctionSQL(fn)
	if _, err := pg_query.Parse(sql); err != nil {
		t.Fatalf("rendered SQL with RETURNS TABLE failed to parse: %v\nSQL: %s", err, sql)
	}
}

func TestRenderCreateProcedureSQLArgNames(t *testing.T) {
	proc := &ir.Procedure{
		Schema: "public",
		Name:   "p",
		Args: []ir.FuncArg{
			{Name: "a", Mode: "IN", Type: ir.TypeRef{Name: "integer"}},
			{Name: "b", Mode: "INOUT", Type: ir.TypeRef{Name: "integer"}},
		},
		Attrs: ir.FuncAttrs{
			Language: "plpgsql",
			Body:     "BEGIN b := a + 1; END;",
		},
	}
	sql := ir.RenderCreateProcedureSQL(proc)
	if _, err := pg_query.Parse(sql); err != nil {
		t.Fatalf("rendered PROCEDURE SQL failed to parse: %v\nSQL: %s", err, sql)
	}
}
