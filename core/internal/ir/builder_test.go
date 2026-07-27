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
