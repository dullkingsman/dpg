package ir_test

import (
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/blockparser"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pgparser"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

var zeroPos = pipeline.SourcePos{File: "test.dpg", Line: 1, Col: 1}

func buildObject(t *testing.T, kind pipeline.ObjectKind, part1, part2 string) pipeline.IRObject {
	t.Helper()
	p := pgparser.New()
	pgResult, err := p.Parse(kind, part1, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	bp := blockparser.New()
	blockAST, err := bp.Parse(kind, part2, zeroPos)
	if err != nil {
		t.Fatalf("block parse error: %v", err)
	}
	builder := ir.NewBuilder()
	obj, err := builder.Build(pgResult, blockAST)
	if err != nil {
		t.Fatalf("build error: %v", err)
	}
	return obj
}

// ── Table ─────────────────────────────────────────────────────────────────────

func TestBuildSimpleTable(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`users (
			id    BIGINT GENERATED ALWAYS AS IDENTITY,
			email TEXT NOT NULL,
			CONSTRAINT pk_users PRIMARY KEY (id)
		)`,
		``,
	)
	tbl, ok := obj.(*ir.Table)
	if !ok {
		t.Fatalf("expected *ir.Table, got %T", obj)
	}
	if tbl.Name != "users" {
		t.Errorf("Name: got %q", tbl.Name)
	}
	if len(tbl.Columns) != 2 {
		t.Errorf("Columns: expected 2, got %d", len(tbl.Columns))
	}
	if tbl.Columns[0].Name != "id" {
		t.Errorf("col[0].Name: got %q", tbl.Columns[0].Name)
	}
	if tbl.Columns[1].Name != "email" {
		t.Errorf("col[1].Name: got %q", tbl.Columns[1].Name)
	}
	if !tbl.Columns[1].NotNull {
		t.Error("email.NotNull: expected true")
	}
	if len(tbl.Constraints) != 1 {
		t.Errorf("Constraints: expected 1, got %d", len(tbl.Constraints))
	}
	if tbl.Constraints[0].Type != "PRIMARY KEY" {
		t.Errorf("constraint type: got %q", tbl.Constraints[0].Type)
	}
	if tbl.QualifiedName() != "public.users" {
		t.Errorf("QualifiedName: got %q", tbl.QualifiedName())
	}
}

func TestBuildTableWithBlock(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`users (
			id    BIGINT GENERATED ALWAYS AS IDENTITY,
			email TEXT NOT NULL
		)`,
		`
			COMMENT 'Primary user store';
			OWNER   "app_role";
			COLUMN email { COMMENT 'Email address'; STATISTICS 300; }
			INDICES { idx_email (email); }
			ENABLE ROW LEVEL SECURITY;
			GRANTS { SELECT TO app_readonly; }
		`,
	)
	tbl, ok := obj.(*ir.Table)
	if !ok {
		t.Fatalf("expected *ir.Table, got %T", obj)
	}
	if tbl.Comment == nil || *tbl.Comment != "Primary user store" {
		t.Errorf("Comment: got %v", tbl.Comment)
	}
	if tbl.Owner == nil || *tbl.Owner != "app_role" {
		t.Errorf("Owner: got %v", tbl.Owner)
	}
	if !tbl.RLSEnabled {
		t.Error("expected RLSEnabled")
	}
	if len(tbl.Indexes) != 1 || tbl.Indexes[0].Name != "idx_email" {
		t.Errorf("Indexes: got %v", tbl.Indexes)
	}
	if len(tbl.Grants) != 1 {
		t.Errorf("Grants: got %d", len(tbl.Grants))
	}
	// Column block merged in.
	emailCol := findCol(tbl.Columns, "email")
	if emailCol == nil {
		t.Fatal("email column not found")
	}
	if emailCol.Comment == nil || *emailCol.Comment != "Email address" {
		t.Errorf("email.Comment: got %v", emailCol.Comment)
	}
	if emailCol.Statistics == nil || *emailCol.Statistics != 300 {
		t.Errorf("email.Statistics: got %v", emailCol.Statistics)
	}
}

func TestBuildSchemaQualifiedTable(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`billing.invoices (id BIGINT)`,
		``,
	)
	tbl := obj.(*ir.Table)
	if tbl.Schema != "billing" {
		t.Errorf("Schema: got %q", tbl.Schema)
	}
	if tbl.Name != "invoices" {
		t.Errorf("Name: got %q", tbl.Name)
	}
	if tbl.QualifiedName() != "billing.invoices" {
		t.Errorf("QualifiedName: got %q", tbl.QualifiedName())
	}
}

func TestBuildPrimaryKeyImpliesNotNull(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`facilities (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY)`,
		``,
	)
	tbl := obj.(*ir.Table)
	col := findCol(tbl.Columns, "id")
	if col == nil {
		t.Fatal("id column not found")
	}
	if !col.NotNull {
		t.Error("expected NotNull=true for inline PRIMARY KEY column")
	}
}

func TestBuildIdentityColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`orders (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY)`,
		``,
	)
	tbl := obj.(*ir.Table)
	col := findCol(tbl.Columns, "id")
	if col == nil {
		t.Fatal("id column not found")
	}
	if col.Identity == nil {
		t.Fatal("expected Identity spec")
	}
	if !col.Identity.Always {
		t.Error("expected Always = true")
	}
}

// ── View ──────────────────────────────────────────────────────────────────────

func TestBuildView(t *testing.T) {
	obj := buildObject(t, pipeline.KindView,
		`users_summary AS SELECT id, email FROM users`,
		`COMMENT 'Summary view'; GRANTS { SELECT TO app_readonly; }`,
	)
	v, ok := obj.(*ir.View)
	if !ok {
		t.Fatalf("expected *ir.View, got %T", obj)
	}
	if v.Name != "users_summary" {
		t.Errorf("Name: got %q", v.Name)
	}
	if v.Comment == nil || *v.Comment != "Summary view" {
		t.Errorf("Comment: got %v", v.Comment)
	}
	if len(v.Grants) != 1 {
		t.Errorf("Grants: got %d", len(v.Grants))
	}
}

// TestBuildMaterializedView guards a real bug found live-testing a demo
// project: pg_query parses CREATE MATERIALIZED VIEW as CreateTableAsStmt
// (Objtype == OBJECT_MATVIEW), not ViewStmt like a plain CREATE VIEW —
// PostgreSQL's own grammar implements it as a CREATE TABLE AS variant.
// Build's switch had no case for CreateTableAsStmt at all, so every
// MATERIALIZED VIEW silently fell through to the generic default case (an
// empty, nameless OpaqueObject) — confirmed live: `dpg plan` reported
// "-- (no changes)" for a newly-declared MATERIALIZED VIEW instead of a
// CREATE, because the malformed object's QualifiedName() was "" and never
// matched anything, in either direction of the diff.
func TestBuildMaterializedView(t *testing.T) {
	obj := buildObject(t, pipeline.KindMaterializedView,
		`order_status_summary AS SELECT status, count(*) AS order_count FROM orders GROUP BY status`,
		`COMMENT 'Order counts by status';`,
	)
	v, ok := obj.(*ir.View)
	if !ok {
		t.Fatalf("expected *ir.View, got %T", obj)
	}
	if v.Name != "order_status_summary" {
		t.Errorf("Name: got %q, want %q", v.Name, "order_status_summary")
	}
	if !v.Materialized {
		t.Error("Materialized: got false, want true")
	}
	if v.Comment == nil || *v.Comment != "Order counts by status" {
		t.Errorf("Comment: got %v", v.Comment)
	}
	if v.Query == "" {
		t.Error("Query: got empty, want the deparsed SELECT")
	}
}

// TestBuildMaterializedViewWithNoData guards the WITH NO DATA clause, which
// has no ViewStmt counterpart at all (it's CreateTableAsStmt.Into.SkipData) —
// a distinct field from anything buildView already handled.
func TestBuildMaterializedViewWithNoData(t *testing.T) {
	obj := buildObject(t, pipeline.KindMaterializedView,
		`order_status_summary AS SELECT status, count(*) AS order_count FROM orders GROUP BY status WITH NO DATA`,
		``,
	)
	v := obj.(*ir.View)
	if !v.WithNoData {
		t.Error("WithNoData: got false, want true")
	}
}

// ── Enum ──────────────────────────────────────────────────────────────────────

func TestBuildEnum(t *testing.T) {
	obj := buildObject(t, pipeline.KindEnum,
		`status AS ENUM ('active', 'pending', 'inactive')`,
		`COMMENT 'User lifecycle states';`,
	)
	tp, ok := obj.(*ir.Type)
	if !ok {
		t.Fatalf("expected *ir.Type, got %T", obj)
	}
	if tp.Variant != "ENUM" {
		t.Errorf("Variant: got %q", tp.Variant)
	}
	if tp.Name != "status" {
		t.Errorf("Name: got %q", tp.Name)
	}
	if len(tp.EnumValues) != 3 {
		t.Errorf("EnumValues: got %d", len(tp.EnumValues))
	}
	if tp.Comment == nil || *tp.Comment != "User lifecycle states" {
		t.Errorf("Comment: got %v", tp.Comment)
	}
}

// TestBuildCompositeType guards a real bug found live-testing a demo
// project, the same shape as TestBuildMaterializedView above: pg_query
// parses CREATE TYPE name AS (attr type, ...) as its own distinct node,
// CompositeTypeStmt — unlike CREATE TYPE name AS ENUM (...), which parses as
// CreateEnumStmt. Build's switch had no case for CompositeTypeStmt at all,
// so every composite type declaration silently fell through to the generic
// default case (an empty, nameless OpaqueObject), even though ir.Type
// already had a "COMPOSITE" Variant and CompositeAttrs field fully wired
// through differ.go and snapshot/convert.go — just never reachable from
// source. Confirmed live: `dpg plan` reported "-- (no changes)" for a
// newly-declared composite TYPE instead of a CREATE.
func TestBuildCompositeType(t *testing.T) {
	obj := buildObject(t, pipeline.KindCompositeType,
		`address AS (street text, city text, zip text)`,
		`COMMENT 'A postal address';`,
	)
	tp, ok := obj.(*ir.Type)
	if !ok {
		t.Fatalf("expected *ir.Type, got %T", obj)
	}
	if tp.Variant != "COMPOSITE" {
		t.Errorf("Variant: got %q, want %q", tp.Variant, "COMPOSITE")
	}
	if tp.Name != "address" {
		t.Errorf("Name: got %q, want %q", tp.Name, "address")
	}
	if len(tp.CompositeAttrs) != 3 {
		t.Fatalf("CompositeAttrs: got %d, want 3", len(tp.CompositeAttrs))
	}
	wantNames := []string{"street", "city", "zip"}
	for i, want := range wantNames {
		if tp.CompositeAttrs[i].Name != want {
			t.Errorf("CompositeAttrs[%d].Name: got %q, want %q", i, tp.CompositeAttrs[i].Name, want)
		}
		if tp.CompositeAttrs[i].Type.Name != "text" {
			t.Errorf("CompositeAttrs[%d].Type: got %q, want %q", i, tp.CompositeAttrs[i].Type.Name, "text")
		}
	}
	if tp.Comment == nil || *tp.Comment != "A postal address" {
		t.Errorf("Comment: got %v", tp.Comment)
	}
}

// TestBuildRangeType guards a real bug found live-testing a demo project,
// the same shape as TestBuildMaterializedView/TestBuildCompositeType above:
// CREATE TYPE name AS RANGE (options) parses as its own distinct node,
// CreateRangeStmt — the third of three CREATE TYPE variants pg_query splits
// into dedicated node types (ENUM -> CreateEnumStmt, composite ->
// CompositeTypeStmt, range -> CreateRangeStmt), none of which is DefineStmt.
// Build's switch had no case for CreateRangeStmt at all, so every range type
// declaration silently fell through to the generic default case (an empty,
// nameless OpaqueObject) — confirmed live: `dpg plan` reported
// "-- (no changes)" for a newly-declared range TYPE instead of a CREATE.
// Notably, buildDefineStmt already has dead "isRange" detection logic for a
// DefineStmt shape pg_query v6 simply never produces for this syntax.
func TestBuildRangeType(t *testing.T) {
	obj := buildObject(t, pipeline.KindRangeType,
		`price_range AS RANGE (SUBTYPE = numeric)`,
		`COMMENT 'A price range';`,
	)
	tp, ok := obj.(*ir.Type)
	if !ok {
		t.Fatalf("expected *ir.Type, got %T", obj)
	}
	if tp.Variant != "RANGE" {
		t.Errorf("Variant: got %q, want %q", tp.Variant, "RANGE")
	}
	if tp.Name != "price_range" {
		t.Errorf("Name: got %q, want %q", tp.Name, "price_range")
	}
	if tp.Body == "" {
		t.Error("Body: got empty, want the deparsed CREATE TYPE statement")
	}
	if tp.Comment == nil || *tp.Comment != "A price range" {
		t.Errorf("Comment: got %v", tp.Comment)
	}
}

// ── Schema ────────────────────────────────────────────────────────────────────

func TestBuildSchema(t *testing.T) {
	obj := buildObject(t, pipeline.KindSchema,
		`billing`,
		`OWNER "finance_role"; COMMENT 'Billing schema';`,
	)
	s, ok := obj.(*ir.Schema)
	if !ok {
		t.Fatalf("expected *ir.Schema, got %T", obj)
	}
	if s.Name != "billing" {
		t.Errorf("Name: got %q", s.Name)
	}
	if s.Owner == nil || *s.Owner != "finance_role" {
		t.Errorf("Owner: got %v", s.Owner)
	}
}

// ── Function ─────────────────────────────────────────────────────────────────

func TestBuildFunction(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`add(a INT, b INT) RETURNS INT LANGUAGE sql AS $$ SELECT a + b $$;`,
		`COMMENT 'Adds two integers';`,
	)
	fn, ok := obj.(*ir.Function)
	if !ok {
		t.Fatalf("expected *ir.Function, got %T", obj)
	}
	if fn.Name != "add" {
		t.Errorf("Name: got %q", fn.Name)
	}
	if len(fn.Args) != 2 {
		t.Errorf("Args: got %d", len(fn.Args))
	}
	if fn.Attrs.Language != "sql" {
		t.Errorf("Language: got %q", fn.Attrs.Language)
	}
	if fn.BodyHash == "" {
		t.Error("expected non-empty BodyHash")
	}
	if fn.Comment == nil || *fn.Comment != "Adds two integers" {
		t.Errorf("Comment: got %v", fn.Comment)
	}
}

// TestBuildFunctionParallelCostRows proves PARALLEL/COST/ROWS all parse
// correctly. COST/ROWS use pg_query's NumericOnly grammar production
// (Integer or Float node, never String — confirmed via a live probe before
// writing this test), unlike LANGUAGE/VOLATILITY/PARALLEL's plain
// identifier arguments.
func TestBuildFunctionParallelCostRows(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f(x INT) RETURNS INT LANGUAGE sql STABLE PARALLEL SAFE COST 500 ROWS 50 AS $$ SELECT x $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.Attrs.Parallel != "SAFE" {
		t.Errorf("Parallel: got %q, want SAFE", fn.Attrs.Parallel)
	}
	if fn.Attrs.Cost == nil || *fn.Attrs.Cost != 500 {
		t.Errorf("Cost: got %v, want 500", fn.Attrs.Cost)
	}
	if fn.Attrs.Rows == nil || *fn.Attrs.Rows != 50 {
		t.Errorf("Rows: got %v, want 50", fn.Attrs.Rows)
	}
}

// TestBuildFunctionFractionalCost proves a fractional COST value (a real,
// documented PostgreSQL grammar form — NumericOnly reduces to a Float node
// for FCONST, confirmed via a live probe) parses correctly, not just the
// integer-looking common case.
func TestBuildFunctionFractionalCost(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f() RETURNS INT LANGUAGE sql COST 500.5 AS $$ SELECT 1 $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.Attrs.Cost == nil || *fn.Attrs.Cost != 500.5 {
		t.Errorf("Cost: got %v, want 500.5", fn.Attrs.Cost)
	}
}

// TestBuildFunctionParallelCostRowsUnspecified proves Parallel defaults to
// PostgreSQL's own real default ("UNSAFE", matching what a live-introspected
// function with no explicit PARALLEL clause also reports), while Cost/Rows
// stay nil rather than defaulting to a guessed number — nil is what lets
// diffFunction distinguish "source doesn't mention it" from "source
// explicitly set it to a value that happens to match the default."
func TestBuildFunctionParallelCostRowsUnspecified(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f() RETURNS INT LANGUAGE sql AS $$ SELECT 1 $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.Attrs.Parallel != "UNSAFE" {
		t.Errorf("Parallel: got %q, want UNSAFE (PostgreSQL's own default)", fn.Attrs.Parallel)
	}
	if fn.Attrs.Cost != nil {
		t.Errorf("Cost: got %v, want nil (unspecified)", fn.Attrs.Cost)
	}
	if fn.Attrs.Rows != nil {
		t.Errorf("Rows: got %v, want nil (unspecified)", fn.Attrs.Rows)
	}
}

// TestBuildFunctionReturnsSetOf proves RETURNS SETOF <type> parses with
// ReturnType.SetOf true — pg_query's TypeName.Setof field was previously
// never read anywhere in the codebase (confirmed via a repo-wide grep), so
// DPG silently dropped SETOF from every function it compiled.
func TestBuildFunctionReturnsSetOf(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f(n INT) RETURNS SETOF INT LANGUAGE sql AS $$ SELECT n $$;`, ``)
	fn := obj.(*ir.Function)
	if !fn.ReturnType.SetOf {
		t.Error("ReturnType.SetOf: got false, want true")
	}
	if fn.ReturnType.Name != "integer" {
		t.Errorf("ReturnType.Name: got %q, want integer", fn.ReturnType.Name)
	}
}

// TestBuildFunctionReturnsPlainNotSetOf is the negative control: an ordinary
// scalar RETURNS clause must leave SetOf false, not true by some fluke of
// the shared typeNameToRef conversion.
func TestBuildFunctionReturnsPlainNotSetOf(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f(n INT) RETURNS INT LANGUAGE sql AS $$ SELECT n $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.ReturnType.SetOf {
		t.Error("ReturnType.SetOf: got true, want false for a plain scalar RETURNS")
	}
}

// TestBuildFunctionArgTypeNeverSetOf guards typeNameToRef's shared-conversion
// design: SetOf is a field on every parsed TypeName regardless of syntactic
// context, but PostgreSQL's grammar only ever sets it true when parsing a
// function's RETURNS clause — an argument's type must never pick it up.
func TestBuildFunctionArgTypeNeverSetOf(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f(n INT) RETURNS INT LANGUAGE sql AS $$ SELECT n $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.Args[0].Type.SetOf {
		t.Error("Args[0].Type.SetOf: got true, want false (SETOF is only valid on RETURNS)")
	}
}

// TestBuildColumnTypeModifiers guards three stacked bugs found live-testing
// a throwaway demo project (numeric(10,2)/varchar(50)/timestamptz(3) columns
// all silently lost their modifier in generated DDL):
//  1. typmodString read a typmod literal via Node.GetInteger(), but pg_query
//     wraps it in an A_Const node instead (confirmed via a direct pg_query.Parse
//     probe) — GetInteger() always returned nil, so the whole function always
//     returned "" regardless of type.
//  2. Once (1) was fixed, character/varchar's case incorrectly subtracted 4
//     from the value (a live-catalog atttypmod encoding quirk that does NOT
//     apply to the parse tree's plain literal typmod — this function is only
//     ever fed from source-parsed TypeName nodes, never a live atttypmod).
//  3. Once (1) and (2) were fixed, timestamptz/timetz's switch case only
//     matched their short internal name, but typeNameToRef always runs the
//     name through pgCatalogName first, which renames them to the long form
//     ("timestamp with time zone") before typmodString ever sees it — so the
//     case never matched and the modifier was dropped a third way.
//
// Also guards TypeRef.String() splicing the modifier in the right position
// for the "with time zone" family specifically: PostgreSQL requires
// "timestamp(3) with time zone", and errors on "timestamp with time
// zone(3)" — confirmed live via format_type() against a real column.
func TestBuildColumnTypeModifiers(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (
			a NUMERIC(10,2),
			b VARCHAR(50),
			c TIMESTAMPTZ(3),
			d TIME(2) WITH TIME ZONE
		)`,
		``,
	)
	tbl := obj.(*ir.Table)
	want := map[string]string{
		"a": "numeric(10,2)",
		"b": "character varying(50)",
		"c": "timestamp(3) with time zone",
		"d": "time(2) with time zone",
	}
	for _, col := range tbl.Columns {
		if w, ok := want[col.Name]; ok && col.Type.String() != w {
			t.Errorf("column %s: got %q, want %q", col.Name, col.Type.String(), w)
		}
	}
}

// TestBuildFunctionImplicitReturnsSingleOut proves that omitting RETURNS
// entirely (valid PostgreSQL when at least one OUT/INOUT parameter is
// present) infers the correct return type from that single OUT parameter —
// confirmed live against postgres:17 (pg_get_functiondef always reconstructs
// this as an explicit "RETURNS integer"), matching what a live-introspected
// version of the same function would report. Before this fix, cfs.ReturnType
// was nil (pg_query performs no semantic analysis) and fn.ReturnType stayed
// the zero TypeRef, producing a broken "RETURNS  LANGUAGE" (empty type) on
// buildFunctionSQL/dump.
func TestBuildFunctionImplicitReturnsSingleOut(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f_single_out(IN n INT, OUT a INT) LANGUAGE sql AS $$ SELECT n + 1 $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.ReturnType.Name != "integer" {
		t.Errorf("ReturnType.Name: got %q, want integer", fn.ReturnType.Name)
	}
	if fn.ReturnType.SetOf {
		t.Error("ReturnType.SetOf: got true, want false")
	}
}

// TestBuildFunctionImplicitReturnsMultiOut proves more than one OUT/INOUT
// parameter with no RETURNS clause infers "record" — confirmed live against
// postgres:17 (a 2-OUT-param function with no RETURNS reports
// pg_get_function_result = "record", proretset = false).
func TestBuildFunctionImplicitReturnsMultiOut(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f_multi_out(IN n INT, OUT a INT, OUT b TEXT) LANGUAGE sql AS $$ SELECT n, 'x' $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.ReturnType.Name != "record" {
		t.Errorf("ReturnType.Name: got %q, want record", fn.ReturnType.Name)
	}
	if fn.ReturnType.SetOf {
		t.Error("ReturnType.SetOf: got true, want false")
	}
}

// TestBuildFunctionImplicitReturnsInout proves a single INOUT parameter (not
// just OUT) also infers its own type as the return type — confirmed live
// against postgres:17.
func TestBuildFunctionImplicitReturnsInout(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f_inout(INOUT n INT) LANGUAGE sql AS $$ SELECT n + 1 $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.ReturnType.Name != "integer" {
		t.Errorf("ReturnType.Name: got %q, want integer", fn.ReturnType.Name)
	}
}

// ── Extension ─────────────────────────────────────────────────────────────────

func TestBuildExtension(t *testing.T) {
	obj := buildObject(t, pipeline.KindExtension, `pgcrypto`, ``)
	e, ok := obj.(*ir.Extension)
	if !ok {
		t.Fatalf("expected *ir.Extension, got %T", obj)
	}
	if e.Name != "pgcrypto" {
		t.Errorf("Name: got %q", e.Name)
	}
}

// ── Sequence ──────────────────────────────────────────────────────────────────

// TestBuildSequenceCycle is the regression guard for a bug found during a
// diff-package coverage push: pg_query represents CYCLE/NO CYCLE as a
// DefElem whose Arg is a Boolean node, not an Integer/A_Const like every
// other sequence option — buildSequence routed all options through
// seqOptionInt (which only handles Integer/A_Const), so CYCLE always
// silently evaluated to nil/unset regardless of what was written, no matter
// how the differ compared it.
func TestBuildSequenceCycle(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id CYCLE`, ``)
	s, ok := obj.(*ir.Sequence)
	if !ok {
		t.Fatalf("expected *ir.Sequence, got %T", obj)
	}
	if s.Cycle == nil {
		t.Fatal("Cycle: got nil, want non-nil (true)")
	}
	if !*s.Cycle {
		t.Error("Cycle: got false, want true")
	}
}

func TestBuildSequenceNoCycle(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id NO CYCLE`, ``)
	s := obj.(*ir.Sequence)
	if s.Cycle == nil {
		t.Fatal("Cycle: got nil, want non-nil (false)")
	}
	if *s.Cycle {
		t.Error("Cycle: got true, want false")
	}
}

// TestBuildSequenceCycleUnspecified proves Cycle stays nil (not false) when
// the source doesn't mention CYCLE/NO CYCLE at all — the nil/false
// distinction is what lets the differ tell "don't touch cycling" apart from
// "explicitly set to no cycle".
func TestBuildSequenceCycleUnspecified(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id INCREMENT BY 1`, ``)
	s := obj.(*ir.Sequence)
	if s.Cycle != nil {
		t.Errorf("Cycle: got %v, want nil", *s.Cycle)
	}
}

func TestBuildSequenceAllOptions(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence,
		`seq_id INCREMENT BY 2 MINVALUE 1 MAXVALUE 100 START WITH 5 CACHE 10 CYCLE`, ``)
	s := obj.(*ir.Sequence)
	if s.IncrementBy == nil || *s.IncrementBy != 2 {
		t.Errorf("IncrementBy: got %v, want 2", s.IncrementBy)
	}
	if s.MinValue == nil || *s.MinValue != 1 {
		t.Errorf("MinValue: got %v, want 1", s.MinValue)
	}
	if s.MaxValue == nil || *s.MaxValue != 100 {
		t.Errorf("MaxValue: got %v, want 100", s.MaxValue)
	}
	if s.StartValue == nil || *s.StartValue != 5 {
		t.Errorf("StartValue: got %v, want 5", s.StartValue)
	}
	if s.Cache == nil || *s.Cache != 10 {
		t.Errorf("Cache: got %v, want 10", s.Cache)
	}
	if s.Cycle == nil || !*s.Cycle {
		t.Errorf("Cycle: got %v, want true", s.Cycle)
	}
}

// ── CHECK constraint column extraction ──────────────────────────────────────────

func findCheck(cs []*ir.Constraint, expr string) *ir.Constraint {
	for _, c := range cs {
		if c.Type == "CHECK" && strings.Contains(c.Expr, expr) {
			return c
		}
	}
	return nil
}

// TestBuildCheckConstraintSingleColumn mirrors PostgreSQL's own default-name
// selection for CHECK constraints (heap.c's AddRelationNewConstraints):
// when the expression references exactly one distinct column, CheckColumn
// must carry it (used only for reconstructing PG's auto-generated name).
func TestBuildCheckConstraintSingleColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a INTEGER, b INTEGER, CONSTRAINT chk CHECK (a > 0))`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "a > 0")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if c.CheckColumn == nil || *c.CheckColumn != "a" {
		t.Errorf("CheckColumn: got %v, want \"a\"", c.CheckColumn)
	}
}

func TestBuildCheckConstraintMultiColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a INTEGER, b INTEGER, CONSTRAINT chk CHECK (a > 0 AND b > 0))`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "a > 0")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if c.CheckColumn != nil {
		t.Errorf("CheckColumn: got %q, want nil (2 distinct columns referenced)", *c.CheckColumn)
	}
}

// TestBuildCheckConstraintDedupSameColumn proves a repeated reference to the
// SAME column still counts as exactly one distinct column, matching PG's
// list_union dedup in heap.c.
func TestBuildCheckConstraintDedupSameColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a INTEGER, CONSTRAINT chk CHECK (a > 0 AND a < 100))`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "a > 0")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if c.CheckColumn == nil || *c.CheckColumn != "a" {
		t.Errorf("CheckColumn: got %v, want \"a\" (deduped)", c.CheckColumn)
	}
}

func TestBuildCheckConstraintNoColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a INTEGER, CONSTRAINT chk CHECK (1 = 1))`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "1 = 1")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if c.CheckColumn != nil {
		t.Errorf("CheckColumn: got %q, want nil (no column referenced)", *c.CheckColumn)
	}
}

// TestBuildCheckConstraintNestedExpression proves the extraction walks
// arbitrarily nested expression nodes (CASE, function calls), not just a
// flat A_Expr — needed since a generic protoreflect walk, not a hand-picked
// set of node types, is what makes this robust.
func TestBuildCheckConstraintNestedExpression(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a INTEGER, CONSTRAINT chk CHECK (CASE WHEN a > 0 THEN true ELSE false END))`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "CASE")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if c.CheckColumn == nil || *c.CheckColumn != "a" {
		t.Errorf("CheckColumn: got %v, want \"a\"", c.CheckColumn)
	}
}

// TestBuildCheckConstraintPromotedColumnLevelSingleColumn proves an inline
// column-level CHECK gets a CheckColumn consistent with its (only) column,
// same as the existing syntactic-position-based Columns marker.
func TestBuildCheckConstraintPromotedColumnLevelSingleColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable, `t (a INTEGER CHECK (a > 0))`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "a > 0")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if len(c.Columns) != 1 || c.Columns[0] != "a" {
		t.Errorf("Columns: got %v, want [a]", c.Columns)
	}
	if c.CheckColumn == nil || *c.CheckColumn != "a" {
		t.Errorf("CheckColumn: got %v, want \"a\"", c.CheckColumn)
	}
}

// TestBuildCheckConstraintPromotedColumnLevelReferencesOtherColumn is the
// key divergence case: a column-level CHECK is free to reference OTHER
// columns too (valid SQL), and PG's real naming rule is expression-based,
// not position-based. Columns must stay the syntactic-position marker
// ([a], used only by createTable's inline-rendering decision) while
// CheckColumn must correctly reflect that 2 distinct columns are referenced
// (nil), not silently inherit the wrong single-column assumption.
func TestBuildCheckConstraintPromotedColumnLevelReferencesOtherColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a INTEGER CHECK (a > b), b INTEGER)`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "a > b")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if len(c.Columns) != 1 || c.Columns[0] != "a" {
		t.Errorf("Columns: got %v, want [a] (syntactic-position marker, unaffected)", c.Columns)
	}
	if c.CheckColumn != nil {
		t.Errorf("CheckColumn: got %q, want nil (references 2 distinct columns)", *c.CheckColumn)
	}
}

// ── EXCLUDE constraint round-tripping ────────────────────────────────────────────

func findExclude(cs []*ir.Constraint) *ir.Constraint {
	for _, c := range cs {
		if c.Type == "EXCLUDE" {
			return c
		}
	}
	return nil
}

// TestBuildExcludeBinaryGistWithWhere is the core regression guard: a
// realistic multi-element EXCLUDE (the canonical "no overlapping bookings"
// shape) must capture its access method, both elements' operators, and its
// WHERE predicate — not the old "EXCLUDE" placeholder, which would already
// fail to apply as invalid SQL.
func TestBuildExcludeBinaryGistWithWhere(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (room integer, during tsrange, CONSTRAINT no_overlap EXCLUDE USING gist (room WITH =, during WITH &&) WHERE (room > 0))`,
		``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude == nil {
		t.Fatal("Exclude spec not populated")
	}
	if c.Exclude.AccessMethod != "gist" {
		t.Errorf("AccessMethod: got %q, want gist", c.Exclude.AccessMethod)
	}
	if c.Exclude.Where != "room > 0" {
		t.Errorf("Where: got %q, want %q", c.Exclude.Where, "room > 0")
	}
	if len(c.Exclude.Elements) != 2 {
		t.Fatalf("Elements: got %d, want 2", len(c.Exclude.Elements))
	}
	if c.Exclude.Elements[0].Column != "room" || c.Exclude.Elements[0].Operator != "=" {
		t.Errorf("Elements[0]: got %+v, want Column=room Operator= =", c.Exclude.Elements[0])
	}
	if c.Exclude.Elements[1].Column != "during" || c.Exclude.Elements[1].Operator != "&&" {
		t.Errorf("Elements[1]: got %+v, want Column=during Operator=&&", c.Exclude.Elements[1])
	}
	if len(c.Columns) != 2 || c.Columns[0] != "room" || c.Columns[1] != "during" {
		t.Errorf("Columns: got %v, want [room during]", c.Columns)
	}
	wantExpr := `EXCLUDE USING gist ("room" WITH =, "during" WITH &&) WHERE (room > 0)`
	if c.Expr != wantExpr {
		t.Errorf("Expr: got %q, want %q", c.Expr, wantExpr)
	}
}

// TestBuildExcludeDefaultsToBtreeAccessMethod proves an EXCLUDE with no
// USING clause still gets a real access method ("btree", PostgreSQL's
// grammar-level default — confirmed live: PG itself both accepts
// "EXCLUDE (a WITH =)" with no USING and reports
// "EXCLUDE USING btree (a WITH =)" back via pg_get_constraintdef), not an
// empty one that would render invalid/ambiguous SQL.
func TestBuildExcludeDefaultsToBtreeAccessMethod(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE (a WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.AccessMethod != "btree" {
		t.Errorf("AccessMethod: got %q, want btree", c.Exclude.AccessMethod)
	}
	wantExpr := `EXCLUDE USING btree ("a" WITH =)`
	if c.Expr != wantExpr {
		t.Errorf("Expr: got %q, want %q", c.Expr, wantExpr)
	}
}

// TestBuildExcludeExpressionElement proves an element that's an expression
// (parenthesized, not a bare column) rather than a plain column reference
// round-trips too — EXCLUDE elements follow the same IndexElem grammar as
// CREATE INDEX, which allows either.
func TestBuildExcludeExpressionElement(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a text, CONSTRAINT c EXCLUDE ((lower(a)) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if len(c.Exclude.Elements) != 1 {
		t.Fatalf("Elements: got %d, want 1", len(c.Exclude.Elements))
	}
	el := c.Exclude.Elements[0]
	if el.Column != "" {
		t.Errorf("Column: got %q, want empty (expression element)", el.Column)
	}
	if el.Expr != "lower(a)" {
		t.Errorf("Expr: got %q, want %q", el.Expr, "lower(a)")
	}
	if len(c.Columns) != 0 {
		t.Errorf("Columns: got %v, want empty (no plain-column elements)", c.Columns)
	}
	wantExpr := `EXCLUDE USING btree ((lower(a)) WITH =)`
	if c.Expr != wantExpr {
		t.Errorf("Expr: got %q, want %q", c.Expr, wantExpr)
	}
}

// TestBuildExcludeCollationOpclassSortNulls proves the full IndexElem
// attribute set — COLLATE, opclass, sort direction, NULLS placement — is
// captured, in PostgreSQL's own rendering order.
func TestBuildExcludeCollationOpclassSortNulls(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a text, CONSTRAINT c EXCLUDE (a COLLATE "C" text_ops DESC NULLS LAST WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	el := c.Exclude.Elements[0]
	if el.Collation != "C" {
		t.Errorf("Collation: got %q, want C", el.Collation)
	}
	if el.OpClass != "text_ops" {
		t.Errorf("OpClass: got %q, want text_ops", el.OpClass)
	}
	if el.SortOrder != "DESC" {
		t.Errorf("SortOrder: got %q, want DESC", el.SortOrder)
	}
	if el.Nulls != "LAST" {
		t.Errorf("Nulls: got %q, want LAST", el.Nulls)
	}
	wantExpr := `EXCLUDE USING btree ("a" COLLATE C text_ops DESC NULLS LAST WITH =)`
	if c.Expr != wantExpr {
		t.Errorf("Expr: got %q, want %q", c.Expr, wantExpr)
	}
}

// TestBuildExcludeOperatorSchemaQualificationStripped proves an explicitly
// schema-qualified built-in operator (OPERATOR(pg_catalog.=)) renders
// without the redundant "pg_catalog." prefix, mirroring pgCatalogName's
// identical treatment of built-in type names.
func TestBuildExcludeOperatorSchemaQualificationStripped(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE (a WITH OPERATOR(pg_catalog.=)))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].Operator != "=" {
		t.Errorf("Operator: got %q, want = (pg_catalog. prefix stripped)", c.Exclude.Elements[0].Operator)
	}
}

// TestBuildExcludeFuncCallElementPredictedName proves a bare, top-level
// function-call element captures its function name — needed to predict
// PostgreSQL's real auto-generated constraint name for such an element
// (verified live: PG's own algorithm uses only the bare function name for
// this shape, never descending into the call's arguments).
func TestBuildExcludeFuncCallElementPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a text, CONSTRAINT c EXCLUDE ((lower(a)) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "lower" {
		t.Errorf("PredictedName: got %q, want lower", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeFuncCallElementMultiArgPredictedName proves the function
// name is captured regardless of how many arguments the call has (verified
// live: PostgreSQL's algorithm never appends argument-derived text for a
// function-call element).
func TestBuildExcludeFuncCallElementMultiArgPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (ts timestamp, CONSTRAINT c EXCLUDE ((date_trunc('day', ts)) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "date_trunc" {
		t.Errorf("PredictedName: got %q, want date_trunc", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeFuncCallElementSchemaQualifiedPredictedName proves a
// schema-qualified function call's PredictedName is the bare function name
// only — matching PostgreSQL's get_func_name, which never includes the
// schema (verified live for both pg_catalog and a user-defined schema).
func TestBuildExcludeFuncCallElementSchemaQualifiedPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((myschema.myfunc(a)) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "myfunc" {
		t.Errorf("PredictedName: got %q, want myfunc (schema stripped)", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeBareOperatorElementPredictedNameEmpty proves PredictedName
// stays empty for a bare, uncast operator expression — PostgreSQL's real
// generated name for this shape is the literal "expr" (verified live), a
// constant this tool does not attempt to reproduce (it carries no
// information about the actual expression, so hardcoding it would be pure
// guesswork dressed up as a prediction, not meaningfully better than not
// predicting at all).
func TestBuildExcludeBareOperatorElementPredictedNameEmpty(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, b integer, CONSTRAINT c EXCLUDE ((a + b) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "" {
		t.Errorf("PredictedName: got %q, want empty (bare operator, no cast)", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeParenthesizedColumnPredictedName proves a bare column
// wrapped in redundant parens — "(a)" rather than "a" — still predicts
// correctly, matching PostgreSQL's own ColumnRef handling (verified live:
// EXCLUDE ((a) WITH =) generates the same name as EXCLUDE (a WITH =) would).
func TestBuildExcludeParenthesizedColumnPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((a) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "a" {
		t.Errorf("PredictedName: got %q, want a", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeNullifElementPredictedName proves NULLIF(a, b) predicts
// the literal "nullif" — PostgreSQL's own FigureColnameInternal special-cases
// NULLIF to act like a regular function call for naming purposes (verified
// live: EXCLUDE ((nullif(a,b)) WITH =) generates "..._nullif_excl").
func TestBuildExcludeNullifElementPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, b integer, CONSTRAINT c EXCLUDE ((nullif(a, b)) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "nullif" {
		t.Errorf("PredictedName: got %q, want nullif", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCastOverColumnPredictedName proves a cast wrapping a
// plain column predicts the COLUMN's name, not the cast's target type —
// PostgreSQL's real algorithm prefers a "strong" name (a column or function
// call) from the cast's argument over its own type name (verified live:
// EXCLUDE ((a::text) WITH =) generates "..._a_excl", not "..._text_excl").
func TestBuildExcludeCastOverColumnPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((a::text) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "a" {
		t.Errorf("PredictedName: got %q, want a (the column, not the cast type)", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCastOverFuncCallPredictedName proves a cast wrapping a
// function call ALSO prefers the function's name over the cast's target
// type (verified live: EXCLUDE ((lower(a)::text) WITH =) generates
// "..._lower_excl", not "..._text_excl").
func TestBuildExcludeCastOverFuncCallPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a text, CONSTRAINT c EXCLUDE ((lower(a)::text) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "lower" {
		t.Errorf("PredictedName: got %q, want lower (the function, not the cast type)", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCastOverOperatorPredictedNameFallsBackToType is the key
// divergence case: a cast wrapping a BARE OPERATOR expression (which alone
// predicts nothing at all — see
// TestBuildExcludeBareOperatorElementPredictedNameEmpty) falls back to the
// cast's OWN target type name, since the operator gave nothing "strong" to
// prefer instead (verified live: EXCLUDE (((a + b)::text) WITH =) generates
// "..._text_excl").
func TestBuildExcludeCastOverOperatorPredictedNameFallsBackToType(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, b integer, CONSTRAINT c EXCLUDE (((a + b)::text) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "text" {
		t.Errorf("PredictedName: got %q, want text (cast's own target type, operator gave nothing strong)", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeNestedCastPredictedName proves a cast-of-a-cast still
// resolves through to the innermost strong name (verified live:
// EXCLUDE (((a::text)::varchar) WITH =) generates "..._a_excl").
func TestBuildExcludeNestedCastPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE (((a::text)::varchar) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "a" {
		t.Errorf("PredictedName: got %q, want a", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCoalesceElementPredictedName proves COALESCE(...) predicts
// the literal "coalesce" — PostgreSQL's own FigureColnameInternal treats it
// like a regular function call for naming, but unlike a real FuncCall it
// never even inspects which function-like node it is (verified live:
// EXCLUDE ((coalesce(a,0)) WITH =) generates "..._coalesce_excl", the same
// literal regardless of COALESCE's arguments).
func TestBuildExcludeCoalesceElementPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((coalesce(a, 0)) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "coalesce" {
		t.Errorf("PredictedName: got %q, want coalesce", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCaseElementWithElsePredictedName proves a CASE expression
// falls back to the literal "case" when its ELSE clause (Defresult) isn't
// itself a strong name — PostgreSQL's real algorithm deliberately only ever
// consults the ELSE clause, never the WHEN branches, for naming purposes
// (verified live: EXCLUDE ((case when a>0 then 1 else 2 end) WITH =)
// generates "..._case_excl", regardless of what the WHEN branch contains).
func TestBuildExcludeCaseElementWithElsePredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((case when a > 0 then 1 else 2 end) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "case" {
		t.Errorf("PredictedName: got %q, want case", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCaseElementNoElsePredictedName proves a CASE expression
// with NO ELSE clause at all (Defresult absent) also falls back to "case",
// not empty/unpredictable (verified live: EXCLUDE
// ((case when a>0 then 1 end) WITH =) generates "..._case_excl" too).
func TestBuildExcludeCaseElementNoElsePredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((case when a > 0 then 1 end) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "case" {
		t.Errorf("PredictedName: got %q, want case", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeArraySubscriptElementPredictedName proves an array
// subscript expression (with no field-access component) recurses through
// to the underlying column — PostgreSQL's A_Indirection handling only
// takes a name directly from a genuine field-access suffix, never from a
// subscript, falling back to the base expression otherwise (verified live:
// EXCLUDE (((array[a,b])[1]) WITH =) generates "..._a_excl", the base
// column's name, not something subscript-derived).
func TestBuildExcludeArraySubscriptElementPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((a[1]) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "a" {
		t.Errorf("PredictedName: got %q, want a", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCollateElementPredictedName proves a COLLATE clause is
// transparent for naming purposes — PostgreSQL's CollateClause handling is
// a pure pass-through to its argument, no naming contribution of its own
// (verified live: EXCLUDE ((a COLLATE "C") WITH =) generates "..._a_excl").
func TestBuildExcludeCollateElementPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a text, CONSTRAINT c EXCLUDE ((a COLLATE "C") WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "a" {
		t.Errorf("PredictedName: got %q, want a", c.Exclude.Elements[0].PredictedName)
	}
}

// ── TypeRef ───────────────────────────────────────────────────────────────────

func TestTypeRefBuiltIn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable, `t (n BIGINT, s TEXT)`, ``)
	tbl := obj.(*ir.Table)
	n := findCol(tbl.Columns, "n")
	if n == nil {
		t.Fatal("column n not found")
	}
	if n.Type.Name != "bigint" {
		t.Errorf("type name: got %q", n.Type.Name)
	}
}

// TestBuildTableRejectsUnknownColumnBlock guards the RFC §7.2 contract: a
// COLUMN block must reference a column that exists in the DDL. Silently
// inventing one (the prior behaviour) leads to malformed migrations like an
// `ALTER COLUMN ... TYPE ` with an empty type when the phantom flows into diff.
func TestBuildTableRejectsUnknownColumnBlock(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindTable,
		`groups (
			id          BIGINT,
			locality_id BIGINT
		)`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse: %v", err)
	}
	bp := blockparser.New()
	// "locality_ids" — note the trailing s — does not match any DDL column.
	blockAST, err := bp.Parse(pipeline.KindTable,
		`COLUMN locality_ids { RENAMED FROM locale_id; }`, zeroPos)
	if err != nil {
		t.Fatalf("block parse: %v", err)
	}
	_, err = ir.NewBuilder().Build(pgResult, blockAST)
	if err == nil {
		t.Fatal("expected build error for unknown COLUMN block target, got nil")
	}
	msg := err.Error()
	for _, want := range []string{`"locality_ids"`, "locality_id"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error to mention %s, got: %s", want, msg)
		}
	}
}

// TestBuildTableAcceptsKnownColumnBlock is the positive case: when the COLUMN
// block names a real DDL column, the build succeeds and merges the attributes.
func TestBuildTableAcceptsKnownColumnBlock(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`groups (
			id          BIGINT,
			locality_id BIGINT
		)`,
		`COLUMN locality_id { COMMENT 'geo locality'; }`,
	)
	tbl := obj.(*ir.Table)
	col := findCol(tbl.Columns, "locality_id")
	if col == nil || col.Comment == nil || *col.Comment != "geo locality" {
		t.Fatalf("expected locality_id with comment, got %+v", col)
	}
}

// ── Registry ──────────────────────────────────────────────────────────────────

func TestRegistration(t *testing.T) {
	impl, ok := pipeline.Resolve[pipeline.IRBuilder](pipeline.Default, pipeline.KeyIRBuilder)
	if !ok {
		t.Fatal("IRBuilder not registered")
	}
	if impl == nil {
		t.Fatal("registered IRBuilder is nil")
	}
}

// ── ArgsKey ───────────────────────────────────────────────────────────────────

func TestArgsKey(t *testing.T) {
	cases := []struct {
		args []ir.FuncArg
		want string
	}{
		{nil, ""},
		{[]ir.FuncArg{{Mode: "IN", Type: ir.TypeRef{Name: "integer"}}}, "integer"},
		{[]ir.FuncArg{
			{Mode: "IN", Type: ir.TypeRef{Name: "integer"}},
			{Mode: "IN", Type: ir.TypeRef{Name: "text"}},
		}, "integer, text"},
		// OUT params are excluded from the identity key.
		{[]ir.FuncArg{
			{Mode: "IN", Type: ir.TypeRef{Name: "integer"}},
			{Mode: "OUT", Type: ir.TypeRef{Name: "text"}},
		}, "integer"},
		// TABLE params are also excluded.
		{[]ir.FuncArg{
			{Mode: "TABLE", Type: ir.TypeRef{Name: "bigint"}},
		}, ""},
		// INOUT params are included.
		{[]ir.FuncArg{
			{Mode: "INOUT", Type: ir.TypeRef{Name: "integer"}},
		}, "integer"},
		// Default mode (empty string treated as IN) is included.
		{[]ir.FuncArg{
			{Type: ir.TypeRef{Name: "boolean"}},
		}, "boolean"},
	}
	for _, tc := range cases {
		got := ir.ArgsKey(tc.args)
		if got != tc.want {
			t.Errorf("ArgsKey(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// ── VirtualType ───────────────────────────────────────────────────────────────

func TestBuildVirtualTypeTypeRef(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType, `label AS text`, ``)
	vt, ok := obj.(*ir.VirtualType)
	if !ok {
		t.Fatalf("expected *ir.VirtualType, got %T", obj)
	}
	if vt.Name != "label" {
		t.Errorf("Name: got %q, want %q", vt.Name, "label")
	}
	if vt.QualifiedName() != "public.label" {
		t.Errorf("QualifiedName: got %q", vt.QualifiedName())
	}
	ref, ok := vt.Body.(ir.VtypeTypeRef)
	if !ok {
		t.Fatalf("Body: expected VtypeTypeRef, got %T", vt.Body)
	}
	if ref.Name != "text" {
		t.Errorf("Body.Name: got %q, want %q", ref.Name, "text")
	}
	if ref.IsArray {
		t.Errorf("Body.IsArray: want false")
	}
}

func TestBuildVirtualTypeTypeRefArray(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType, `tags AS text[]`, ``)
	vt := obj.(*ir.VirtualType)
	ref, ok := vt.Body.(ir.VtypeTypeRef)
	if !ok {
		t.Fatalf("Body: expected VtypeTypeRef, got %T", vt.Body)
	}
	if ref.Name != "text" || !ref.IsArray {
		t.Errorf("Body: got name=%q array=%v, want name=text array=true", ref.Name, ref.IsArray)
	}
}

func TestBuildVirtualTypeSchemaQualifiedRef(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType, `status AS billing.payment_method`, ``)
	vt := obj.(*ir.VirtualType)
	ref, ok := vt.Body.(ir.VtypeTypeRef)
	if !ok {
		t.Fatalf("Body: expected VtypeTypeRef, got %T", vt.Body)
	}
	if ref.Schema != "billing" || ref.Name != "payment_method" {
		t.Errorf("Body: got schema=%q name=%q, want billing/payment_method", ref.Schema, ref.Name)
	}
}

func TestBuildVirtualTypeComposite(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType, `point AS (x float8, y float8)`, ``)
	vt := obj.(*ir.VirtualType)
	comp, ok := vt.Body.(ir.VtypeComposite)
	if !ok {
		t.Fatalf("Body: expected VtypeComposite, got %T", vt.Body)
	}
	if len(comp.Fields) != 2 {
		t.Fatalf("Fields: got %d, want 2", len(comp.Fields))
	}
	if comp.Fields[0].Name != "x" || comp.Fields[0].Type.Name != "float8" {
		t.Errorf("Fields[0]: got %+v", comp.Fields[0])
	}
	if comp.Fields[1].Name != "y" || comp.Fields[1].Type.Name != "float8" {
		t.Errorf("Fields[1]: got %+v", comp.Fields[1])
	}
}

func TestBuildVirtualTypeCompositeWithArrayField(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType, `order_summary AS (id bigint, items line_item[])`, ``)
	vt := obj.(*ir.VirtualType)
	comp, ok := vt.Body.(ir.VtypeComposite)
	if !ok {
		t.Fatalf("Body: expected VtypeComposite, got %T", vt.Body)
	}
	if len(comp.Fields) != 2 {
		t.Fatalf("Fields: got %d, want 2", len(comp.Fields))
	}
	itemsField := comp.Fields[1]
	if itemsField.Name != "items" || itemsField.Type.Name != "line_item" || !itemsField.Type.IsArray {
		t.Errorf("Fields[1]: got name=%q type=%q array=%v", itemsField.Name, itemsField.Type.Name, itemsField.Type.IsArray)
	}
}

func TestBuildVirtualTypeUnion(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType,
		`shape AS (x float8, y float8) | (width float8, height float8) | text`, ``)
	vt := obj.(*ir.VirtualType)
	union, ok := vt.Body.(ir.VtypeUnion)
	if !ok {
		t.Fatalf("Body: expected VtypeUnion, got %T", vt.Body)
	}
	if len(union.Members) != 3 {
		t.Fatalf("Members: got %d, want 3", len(union.Members))
	}
	// First two should be composites, last a type ref.
	if _, ok := union.Members[0].(ir.VtypeComposite); !ok {
		t.Errorf("Members[0]: expected VtypeComposite, got %T", union.Members[0])
	}
	if _, ok := union.Members[1].(ir.VtypeComposite); !ok {
		t.Errorf("Members[1]: expected VtypeComposite, got %T", union.Members[1])
	}
	ref, ok := union.Members[2].(ir.VtypeTypeRef)
	if !ok {
		t.Errorf("Members[2]: expected VtypeTypeRef, got %T", union.Members[2])
	}
	if ref.Name != "text" {
		t.Errorf("Members[2].Name: got %q, want %q", ref.Name, "text")
	}
}

func TestBuildVirtualTypeUnionTypeRefs(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType, `metric AS integer | numeric | text`, ``)
	vt := obj.(*ir.VirtualType)
	union, ok := vt.Body.(ir.VtypeUnion)
	if !ok {
		t.Fatalf("Body: expected VtypeUnion, got %T", vt.Body)
	}
	if len(union.Members) != 3 {
		t.Fatalf("Members: got %d, want 3", len(union.Members))
	}
	names := []string{"integer", "numeric", "text"}
	for i, m := range union.Members {
		ref, ok := m.(ir.VtypeTypeRef)
		if !ok {
			t.Errorf("Members[%d]: expected VtypeTypeRef, got %T", i, m)
			continue
		}
		if ref.Name != names[i] {
			t.Errorf("Members[%d].Name: got %q, want %q", i, ref.Name, names[i])
		}
	}
}

func TestBuildVirtualTypeWithComment(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType,
		`user_state AS text`,
		`COMMENT 'User lifecycle state';`)
	vt := obj.(*ir.VirtualType)
	if vt.Comment == nil || *vt.Comment != "User lifecycle state" {
		t.Errorf("Comment: got %v", vt.Comment)
	}
}

func TestBuildVirtualTypePreferredJsonFormatJsonb(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType,
		`payload AS (kind text, data text)`,
		`PREFERRED JSON FORMAT jsonb;`)
	vt := obj.(*ir.VirtualType)
	if vt.JsonFormat != "jsonb" {
		t.Errorf("JsonFormat: got %q, want %q", vt.JsonFormat, "jsonb")
	}
}

func TestBuildVirtualTypePreferredJsonFormatJson(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType,
		`payload AS (kind text, data text)`,
		`PREFERRED JSON FORMAT json;`)
	vt := obj.(*ir.VirtualType)
	if vt.JsonFormat != "json" {
		t.Errorf("JsonFormat: got %q, want %q", vt.JsonFormat, "json")
	}
}

func TestBuildVirtualTypeDefaultJsonFormat(t *testing.T) {
	// No PREFERRED JSON FORMAT → JsonFormat is empty (caller defaults to jsonb).
	obj := buildObject(t, pipeline.KindVirtualType, `tag AS text`, ``)
	vt := obj.(*ir.VirtualType)
	if vt.JsonFormat != "" {
		t.Errorf("JsonFormat: got %q, want empty (default)", vt.JsonFormat)
	}
}

func TestBuildVirtualTypeCommentAndFormat(t *testing.T) {
	// Both COMMENT and PREFERRED JSON FORMAT can coexist in the {} block.
	obj := buildObject(t, pipeline.KindVirtualType,
		`event AS (type text, ts bigint)`,
		`COMMENT 'App event'; PREFERRED JSON FORMAT json;`)
	vt := obj.(*ir.VirtualType)
	if vt.Comment == nil || *vt.Comment != "App event" {
		t.Errorf("Comment: got %v", vt.Comment)
	}
	if vt.JsonFormat != "json" {
		t.Errorf("JsonFormat: got %q, want json", vt.JsonFormat)
	}
}

func TestBuildVirtualTypeSchemaQualifiedName(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindVirtualType, `billing.status AS text`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	// Explicit schema context must NOT override the qualified name.
	pgResult.SchemaContext = "public"
	bp := blockparser.New()
	blockAST, _ := bp.Parse(pipeline.KindVirtualType, ``, zeroPos)
	obj, buildErr := ir.NewBuilder().Build(pgResult, blockAST)
	if buildErr != nil {
		t.Fatalf("build error: %v", buildErr)
	}
	vt := obj.(*ir.VirtualType)
	if vt.Schema != "billing" {
		t.Errorf("Schema: got %q, want %q", vt.Schema, "billing")
	}
	if vt.Name != "status" {
		t.Errorf("Name: got %q, want %q", vt.Name, "status")
	}
}

func TestBuildVirtualTypeSchemaContext(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindVirtualType, `status AS text`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	pgResult.SchemaContext = "myschema"
	bp := blockparser.New()
	blockAST, _ := bp.Parse(pipeline.KindVirtualType, ``, zeroPos)
	obj, buildErr := ir.NewBuilder().Build(pgResult, blockAST)
	if buildErr != nil {
		t.Fatalf("build error: %v", buildErr)
	}
	vt := obj.(*ir.VirtualType)
	if vt.Schema != "myschema" {
		t.Errorf("Schema: got %q, want %q", vt.Schema, "myschema")
	}
}

func TestBuildVirtualTypeEmptyBodyError(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindVirtualType, `bad AS`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	bp := blockparser.New()
	blockAST, _ := bp.Parse(pipeline.KindVirtualType, ``, zeroPos)
	_, buildErr := ir.NewBuilder().Build(pgResult, blockAST)
	if buildErr == nil {
		t.Error("expected error for empty body, got nil")
	}
}

func TestBuildVirtualTypeMissingASError(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindVirtualType, `noashere`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	bp := blockparser.New()
	blockAST, _ := bp.Parse(pipeline.KindVirtualType, ``, zeroPos)
	_, buildErr := ir.NewBuilder().Build(pgResult, blockAST)
	if buildErr == nil {
		t.Error("expected error for missing AS keyword, got nil")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func findCol(cols []*ir.Column, name string) *ir.Column {
	for _, c := range cols {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ── Operator class / family access method ─────────────────────────────────────

func TestBuildOperatorClassAccessMethod(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorClass,
		`my_ops FOR TYPE int4 USING gin AS STORAGE int4`, ``)
	oc, ok := obj.(*ir.OperatorClass)
	if !ok {
		t.Fatalf("expected *ir.OperatorClass, got %T", obj)
	}
	if oc.AccessMethod != "gin" {
		t.Errorf("AccessMethod: got %q, want %q", oc.AccessMethod, "gin")
	}
}

func TestBuildOperatorFamilyAccessMethod(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorFamily, `my_family USING gist`, ``)
	of, ok := obj.(*ir.OperatorFamily)
	if !ok {
		t.Fatalf("expected *ir.OperatorFamily, got %T", obj)
	}
	if of.AccessMethod != "gist" {
		t.Errorf("AccessMethod: got %q, want %q", of.AccessMethod, "gist")
	}
}

// ── Operator class FAMILY / TS config PARSER / TS dict TEMPLATE extraction ─────
//
// These three back the class→family/config→parser/dict→template dependency
// edges added to graph.go (the operator-family general fix): pg_query already
// parses each of these structurally (CreateOpClassStmt.Opfamilyname, and a
// "parser"/"template" DefElem whose Arg is a TypeName, confirmed live via a
// throwaway probe before writing this code), the builder just previously
// discarded them.

func TestBuildOperatorClassFamilyExplicitUnqualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorClass,
		`my_ops FOR TYPE int4 USING gin FAMILY my_family AS STORAGE int4`, ``)
	oc := obj.(*ir.OperatorClass)
	if oc.FamilySchema != "" || oc.FamilyName != "my_family" {
		t.Errorf("Family: got schema=%q name=%q, want schema=\"\" name=\"my_family\"", oc.FamilySchema, oc.FamilyName)
	}
}

func TestBuildOperatorClassFamilyExplicitQualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorClass,
		`my_ops FOR TYPE int4 USING gin FAMILY myschema.my_family AS STORAGE int4`, ``)
	oc := obj.(*ir.OperatorClass)
	if oc.FamilySchema != "myschema" || oc.FamilyName != "my_family" {
		t.Errorf("Family: got schema=%q name=%q, want schema=\"myschema\" name=\"my_family\"", oc.FamilySchema, oc.FamilyName)
	}
}

// TestBuildOperatorClassFamilyOmitted is the negative control: hand-written
// source that omits FAMILY (relying on PostgreSQL's own same-name
// auto-creation) must leave FamilyName empty, not default it to the class's
// own name — that defaulting is a graph.go dependency-edge-lookup concern
// (see defaultSchema there), not something the builder should fabricate,
// since introspection is what always supplies an explicit FAMILY going
// forward (see introspectOperatorClasses); hand-written source keeps
// whatever the user actually wrote.
func TestBuildOperatorClassFamilyOmitted(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorClass,
		`my_ops FOR TYPE int4 USING gin AS STORAGE int4`, ``)
	oc := obj.(*ir.OperatorClass)
	if oc.FamilyName != "" {
		t.Errorf("FamilyName: got %q, want empty (FAMILY omitted from source)", oc.FamilyName)
	}
}

func TestBuildTSConfigParserUnqualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindTSConfig, `my_cfg (PARSER = my_parser)`, ``)
	tc := obj.(*ir.TSConfig)
	if tc.ParserSchema != "" || tc.ParserName != "my_parser" {
		t.Errorf("Parser: got schema=%q name=%q, want schema=\"\" name=\"my_parser\"", tc.ParserSchema, tc.ParserName)
	}
}

func TestBuildTSConfigParserQualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindTSConfig, `my_cfg (PARSER = pg_catalog."default")`, ``)
	tc := obj.(*ir.TSConfig)
	if tc.ParserSchema != "pg_catalog" || tc.ParserName != "default" {
		t.Errorf("Parser: got schema=%q name=%q, want schema=\"pg_catalog\" name=\"default\"", tc.ParserSchema, tc.ParserName)
	}
}

func TestBuildTSDictTemplateUnqualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindTSDict, `my_dict (TEMPLATE = simple)`, ``)
	td := obj.(*ir.TSDict)
	if td.TemplateSchema != "" || td.TemplateName != "simple" {
		t.Errorf("Template: got schema=%q name=%q, want schema=\"\" name=\"simple\"", td.TemplateSchema, td.TemplateName)
	}
}

func TestBuildTSDictTemplateQualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindTSDict, `my_dict (TEMPLATE = pg_catalog.simple)`, ``)
	td := obj.(*ir.TSDict)
	if td.TemplateSchema != "pg_catalog" || td.TemplateName != "simple" {
		t.Errorf("Template: got schema=%q name=%q, want schema=\"pg_catalog\" name=\"simple\"", td.TemplateSchema, td.TemplateName)
	}
}

// ── Operator LEFTARG/RIGHTARG extraction ────────────────────────────────────────

// TestBuildOperatorBinaryOperandTypes proves a binary operator's LEFTARG/
// RIGHTARG are captured, needed so QualifiedName can disambiguate overloaded
// operator symbols (same name, different operand types) instead of colliding
// in the flat, name-keyed snapshot/diff maps.
func TestBuildOperatorBinaryOperandTypes(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperator,
		`public.## (LEFTARG = integer, RIGHTARG = integer, FUNCTION = int4eq)`, ``)
	op, ok := obj.(*ir.Operator)
	if !ok {
		t.Fatalf("expected *ir.Operator, got %T", obj)
	}
	if op.LeftType == nil || op.LeftType.String() != "integer" {
		t.Errorf("LeftType: got %v, want integer", op.LeftType)
	}
	if op.RightType == nil || op.RightType.String() != "integer" {
		t.Errorf("RightType: got %v, want integer", op.RightType)
	}
	if want := `public.##(integer, integer)`; op.QualifiedName() != want {
		t.Errorf("QualifiedName: got %q, want %q", op.QualifiedName(), want)
	}
}

// TestBuildOperatorPrefixOperandTypes proves a unary (prefix) operator, which
// omits LEFTARG entirely, leaves LeftType nil rather than defaulting to a
// zero value that could be mistaken for a real type.
func TestBuildOperatorPrefixOperandTypes(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperator,
		`public.!! (RIGHTARG = integer, FUNCTION = fact)`, ``)
	op, ok := obj.(*ir.Operator)
	if !ok {
		t.Fatalf("expected *ir.Operator, got %T", obj)
	}
	if op.LeftType != nil {
		t.Errorf("LeftType: got %v, want nil (prefix operator has no left operand)", op.LeftType)
	}
	if op.RightType == nil || op.RightType.String() != "integer" {
		t.Errorf("RightType: got %v, want integer", op.RightType)
	}
	if want := `public.!!(NONE, integer)`; op.QualifiedName() != want {
		t.Errorf("QualifiedName: got %q, want %q", op.QualifiedName(), want)
	}
}

// TestBuildOperatorOverloadDistinctQualifiedNames is the core regression
// guard: two operators sharing the same symbol but different operand types
// (the common overload shape, e.g. + for integer vs numeric) must produce
// different QualifiedName values, or one would silently overwrite the other
// in the flat snapshot/diff maps — the same collision class already fixed
// for OperatorClass/OperatorFamily.
func TestBuildOperatorOverloadDistinctQualifiedNames(t *testing.T) {
	intOp := buildObject(t, pipeline.KindOperator,
		`public.+ (LEFTARG = integer, RIGHTARG = integer, FUNCTION = int4pl)`, ``).(*ir.Operator)
	numOp := buildObject(t, pipeline.KindOperator,
		`public.+ (LEFTARG = numeric, RIGHTARG = numeric, FUNCTION = numeric_add)`, ``).(*ir.Operator)
	if intOp.QualifiedName() == numOp.QualifiedName() {
		t.Errorf("expected distinct QualifiedName for overloaded operators, both got %q", intOp.QualifiedName())
	}
}

// ── Subscription CONNECTION (RFC §13.2) ─────────────────────────────────────────

func TestBuildSubscriptionPlainLiteral(t *testing.T) {
	obj := buildObject(t, pipeline.KindSubscription,
		`sub CONNECTION 'host=primary.internal dbname=myapp user=replicator' PUBLICATION pub`, ``)
	sub, ok := obj.(*ir.Subscription)
	if !ok {
		t.Fatalf("expected *ir.Subscription, got %T", obj)
	}
	if sub.ConnInfo != "host=primary.internal dbname=myapp user=replicator" {
		t.Errorf("ConnInfo: got %q", sub.ConnInfo)
	}
}

func TestBuildSubscriptionTemplatedLiteral(t *testing.T) {
	obj := buildObject(t, pipeline.KindSubscription,
		`sub CONNECTION 'host=x user=y password={{vault:secret/db#pw}}' PUBLICATION pub`, ``)
	sub := obj.(*ir.Subscription)
	if sub.ConnInfo != "host=x user=y password={{vault:secret/db#pw}}" {
		t.Errorf("ConnInfo: got %q, want the raw literal with {{...}} left unresolved at build time", sub.ConnInfo)
	}
}

func TestBuildSubscriptionWholeValueTemplatedLiteral(t *testing.T) {
	obj := buildObject(t, pipeline.KindSubscription,
		`sub CONNECTION '{{vault:secret/repl/db#conninfo}}' PUBLICATION pub`, ``)
	sub := obj.(*ir.Subscription)
	if sub.ConnInfo != "{{vault:secret/repl/db#conninfo}}" {
		t.Errorf("ConnInfo: got %q", sub.ConnInfo)
	}
}

func TestBuildSubscriptionComment(t *testing.T) {
	obj := buildObject(t, pipeline.KindSubscription,
		`sub CONNECTION 'host=x user=y' PUBLICATION pub`,
		`COMMENT 'replication for orders';`,
	)
	sub, ok := obj.(*ir.Subscription)
	if !ok {
		t.Fatalf("expected *ir.Subscription, got %T", obj)
	}
	if sub.Comment == nil || *sub.Comment != "replication for orders" {
		t.Errorf("Comment: got %v", sub.Comment)
	}
}

// ── Role attributes (RFC §11.1) ──────────────────────────────────────────────

func TestBuildRoleAllAttributes(t *testing.T) {
	obj := buildObject(t, pipeline.KindRole,
		`app_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 20 PASSWORD '{{vault:secret/roles/app_service#pw}}' VALID UNTIL '2030-01-01' IN ROLE role_a, role_b ROLE role_c ADMIN role_d`,
		``,
	)
	r, ok := obj.(*ir.Role)
	if !ok {
		t.Fatalf("expected *ir.Role, got %T", obj)
	}
	if r.CanLogin == nil || !*r.CanLogin {
		t.Errorf("CanLogin: got %v, want true", r.CanLogin)
	}
	if r.Superuser == nil || *r.Superuser {
		t.Errorf("Superuser: got %v, want false", r.Superuser)
	}
	if r.CreateDB == nil || *r.CreateDB {
		t.Errorf("CreateDB: got %v, want false", r.CreateDB)
	}
	if r.CreateRole == nil || *r.CreateRole {
		t.Errorf("CreateRole: got %v, want false", r.CreateRole)
	}
	if r.Inherit == nil || !*r.Inherit {
		t.Errorf("Inherit: got %v, want true", r.Inherit)
	}
	if r.IsReplication == nil || *r.IsReplication {
		t.Errorf("IsReplication: got %v, want false", r.IsReplication)
	}
	if r.BypassRLS == nil || *r.BypassRLS {
		t.Errorf("BypassRLS: got %v, want false", r.BypassRLS)
	}
	if r.ConnectionLimit == nil || *r.ConnectionLimit != 20 {
		t.Errorf("ConnectionLimit: got %v, want 20", r.ConnectionLimit)
	}
	if r.Password == nil || *r.Password != "{{vault:secret/roles/app_service#pw}}" {
		t.Errorf("Password: got %v", r.Password)
	}
	if r.ValidUntil == nil || *r.ValidUntil != "2030-01-01" {
		t.Errorf("ValidUntil: got %v", r.ValidUntil)
	}
	if len(r.InRole) != 2 || r.InRole[0] != "role_a" || r.InRole[1] != "role_b" {
		t.Errorf("InRole: got %v", r.InRole)
	}
	if len(r.RoleMembers) != 1 || r.RoleMembers[0] != "role_c" {
		t.Errorf("RoleMembers: got %v", r.RoleMembers)
	}
	if len(r.AdminRoles) != 1 || r.AdminRoles[0] != "role_d" {
		t.Errorf("AdminRoles: got %v", r.AdminRoles)
	}
}

func TestBuildRoleUnsetAttributesAreNil(t *testing.T) {
	obj := buildObject(t, pipeline.KindRole, `plain_role`, ``)
	r := obj.(*ir.Role)
	if r.CanLogin != nil || r.Superuser != nil || r.CreateDB != nil || r.CreateRole != nil ||
		r.Inherit != nil || r.IsReplication != nil || r.BypassRLS != nil ||
		r.ConnectionLimit != nil || r.Password != nil || r.ValidUntil != nil {
		t.Errorf("expected all optional attributes nil for a bare ROLE decl, got: %+v", r)
	}
	if r.InRole != nil || r.RoleMembers != nil || r.AdminRoles != nil {
		t.Errorf("expected nil membership lists, got InRole=%v RoleMembers=%v AdminRoles=%v", r.InRole, r.RoleMembers, r.AdminRoles)
	}
}

func TestBuildRolePlainLiteralPassword(t *testing.T) {
	obj := buildObject(t, pipeline.KindRole, `svc PASSWORD 'hunter2'`, ``)
	r := obj.(*ir.Role)
	if r.Password == nil || *r.Password != "hunter2" {
		t.Errorf("Password: got %v", r.Password)
	}
}

func TestBuildRoleComment(t *testing.T) {
	obj := buildObject(t, pipeline.KindRole, `app_readonly NOLOGIN`, `COMMENT 'Read-only access';`)
	r := obj.(*ir.Role)
	if r.Comment == nil || *r.Comment != "Read-only access" {
		t.Errorf("Comment: got %v", r.Comment)
	}
	if r.CanLogin == nil || *r.CanLogin {
		t.Errorf("CanLogin: got %v, want false (NOLOGIN)", r.CanLogin)
	}
}
