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
			COMMENT "Primary user store";
			OWNER   "app_role";
			COLUMN email { COMMENT "Email address"; STATISTICS 300; }
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
		`COMMENT "Summary view"; GRANTS { SELECT TO app_readonly; }`,
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
		`COMMENT "User lifecycle states";`,
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
		`OWNER "finance_role"; COMMENT "Billing schema";`,
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
		`COMMENT "Adds two integers";`,
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
		`COLUMN locality_id { COMMENT "geo locality"; }`,
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
		`COMMENT "User lifecycle state";`)
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
		`COMMENT "App event"; PREFERRED JSON FORMAT json;`)
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
