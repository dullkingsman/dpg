package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/format"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func TestObjectSchema(t *testing.T) {
	cases := []struct {
		obj  pipeline.IRObject
		want string
	}{
		{&ir.Table{Schema: "public"}, "public"},
		{&ir.View{Schema: "reports"}, "reports"},
		{&ir.Function{Schema: "util"}, "util"},
		{&ir.Procedure{Schema: "util"}, "util"},
		{&ir.Aggregate{Schema: "stats"}, "stats"},
		{&ir.Type{Schema: "domain"}, "domain"},
		{&ir.Sequence{Schema: "public"}, "public"},
		{&ir.Role{}, ""},
		{&ir.Schema{}, ""},
	}
	for _, tc := range cases {
		got := objectSchema(tc.obj)
		if got != tc.want {
			t.Errorf("objectSchema(%T) = %q, want %q", tc.obj, got, tc.want)
		}
	}
}

// TestRenderOpaqueObjectsCompile guards the no-verb mandate: dump renders
// introspected opaque objects (whose Body is a canonical "CREATE …" statement)
// as DPG declarations, which MUST NOT begin with CREATE and MUST re-compile.
func TestRenderOpaqueObjectsCompile(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}

	objs := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "t", Columns: []*ir.Column{
			{Name: "a", Type: ir.TypeRef{Name: "int4"}},
			{Name: "b", Type: ir.TypeRef{Name: "int4"}},
		}},
		&ir.ForeignDataWrapper{Name: "dummy_fdw", Body: "CREATE FOREIGN DATA WRAPPER dummy_fdw"},
		&ir.ForeignServer{Name: "dummy_srv", Body: "CREATE SERVER dummy_srv FOREIGN DATA WRAPPER dummy_fdw"},
		&ir.UserMapping{Server: "dummy_srv", Body: "CREATE USER MAPPING FOR PUBLIC SERVER dummy_srv"},
		&ir.Publication{Name: "my_pub", Body: "CREATE PUBLICATION my_pub FOR ALL TABLES"},
		&ir.EventTrigger{Name: "et", Body: "CREATE EVENT TRIGGER et ON ddl_command_start EXECUTE FUNCTION f()"},
		&ir.Collation{Schema: "public", Name: "my_coll", Body: "CREATE COLLATION public.my_coll (locale = 'C')"},
		&ir.Cast{SourceType: ir.TypeRef{Name: "int4"}, TargetType: ir.TypeRef{Name: "bool"},
			Body: "CREATE CAST (integer AS boolean) WITHOUT FUNCTION"},
		&ir.StatisticsObject{Schema: "public", Name: "st",
			Body: "CREATE STATISTICS public.st (dependencies) ON a, b FROM public.t"},
		&ir.Tablespace{Name: "ts", Body: "CREATE TABLESPACE ts LOCATION '/tmp/x'"},
		&ir.Operator{Schema: "public", Name: "===",
			Body: "CREATE OPERATOR public.=== (FUNCTION = int4eq, LEFTARG = integer, RIGHTARG = integer)"},
		&ir.OperatorClass{Schema: "public", Name: "my_opc", AccessMethod: "btree",
			Body: "CREATE OPERATOR CLASS public.my_opc FOR TYPE integer USING btree AS OPERATOR 3 =, FUNCTION 1 btint4cmp(integer, integer)"},
		&ir.OperatorFamily{Schema: "public", Name: "my_fam", AccessMethod: "btree",
			Body: "CREATE OPERATOR FAMILY public.my_fam USING btree"},
		&ir.TSConfig{Schema: "public", Name: "my_cfg",
			Body: `CREATE TEXT SEARCH CONFIGURATION public.my_cfg (PARSER = pg_catalog."default")`},
		&ir.TSDict{Schema: "public", Name: "my_dict",
			Body: "CREATE TEXT SEARCH DICTIONARY public.my_dict (TEMPLATE = pg_catalog.simple)"},
		&ir.TSParser{Schema: "public", Name: "my_prs",
			Body: "CREATE TEXT SEARCH PARSER public.my_prs (START = prsd_start, GETTOKEN = prsd_nexttoken, END = prsd_end, LEXTYPES = prsd_lextype)"},
		&ir.TSTemplate{Schema: "public", Name: "my_tmpl",
			Body: "CREATE TEXT SEARCH TEMPLATE public.my_tmpl (LEXIZE = dsimple_lexize)"},
	}

	var b strings.Builder
	for _, o := range objs {
		renderObjectDPG(&b, o, fmtOpts)
	}
	rendered := b.String()

	// The no-verb mandate: no declaration may begin with CREATE.
	for line := range strings.SplitSeq(rendered, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "CREATE ") {
			t.Errorf("rendered declaration begins with CREATE (violates no-verb mandate): %q", line)
		}
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped .dpg failed to recompile: %v\n---\n%s", err, rendered)
	}

	// Every opaque object must survive the round-trip.
	wantKinds := map[string]bool{
		"*ir.ForeignDataWrapper": false, "*ir.ForeignServer": false, "*ir.UserMapping": false,
		"*ir.Publication": false, "*ir.EventTrigger": false, "*ir.Collation": false,
		"*ir.Cast": false, "*ir.StatisticsObject": false, "*ir.Tablespace": false,
		"*ir.Operator": false, "*ir.OperatorClass": false, "*ir.OperatorFamily": false,
		"*ir.TSConfig": false, "*ir.TSDict": false, "*ir.TSParser": false, "*ir.TSTemplate": false,
	}
	for _, o := range compiled {
		k := reflect.TypeOf(o).String()
		if _, ok := wantKinds[k]; ok {
			wantKinds[k] = true
		}
	}
	for k, seen := range wantKinds {
		if !seen {
			t.Errorf("kind %s missing after recompile", k)
		}
	}

	// Every opaque object must carry a Body after recompile — an empty one aborts
	// createOpaque ("body not captured") and would break plan/apply for the whole
	// database. The differ's create path is the direct guard: running it against
	// an empty snapshot exercises createOpaque for each object.
	if _, err := diff.New().Diff(compiled, &pipeline.Snapshot{}); err != nil {
		t.Fatalf("diffing dumped opaque objects failed (empty body?): %v", err)
	}
}

func TestIsClusterScoped(t *testing.T) {
	cluster := []pipeline.IRObject{&ir.Role{Name: "r"}, &ir.Tablespace{Name: "ts"}}
	database := []pipeline.IRObject{
		&ir.ForeignDataWrapper{Name: "f"}, &ir.ForeignServer{Name: "s"},
		&ir.UserMapping{Server: "s"}, &ir.Publication{Name: "p"},
		&ir.EventTrigger{Name: "e"}, &ir.Cast{}, &ir.Collation{Name: "c"},
		&ir.Table{Schema: "public", Name: "t"},
	}
	for _, o := range cluster {
		if !isClusterScoped(o) {
			t.Errorf("%T should be cluster-scoped", o)
		}
	}
	for _, o := range database {
		if isClusterScoped(o) {
			t.Errorf("%T should NOT be cluster-scoped", o)
		}
	}
	// excludeClusterScoped keeps exactly the database objects.
	kept := excludeClusterScoped(append(append([]pipeline.IRObject{}, cluster...), database...))
	if len(kept) != len(database) {
		t.Errorf("excludeClusterScoped kept %d, want %d", len(kept), len(database))
	}
}

func TestQuoteIdentIfNeeded(t *testing.T) {
	cases := map[string]string{
		"items":     "items",       // safe lowercase
		"a1_$":      "a1_$",        // safe with digit/underscore/dollar
		"name":      "name",        // unreserved/col-name keyword -> bare
		"order":     `"order"`,     // reserved
		"select":    `"select"`,    // reserved
		"user":      `"user"`,      // reserved
		"left":      `"left"`,      // type_func_name
		"MyTable":   `"MyTable"`,   // uppercase folds -> must quote
		"has space": `"has space"`, // special char
		"1st":       `"1st"`,       // leading digit
		"":          `""`,          // empty
	}
	for in, want := range cases {
		if got := quoteIdentIfNeeded(in); got != want {
			t.Errorf("quoteIdentIfNeeded(%q) = %q, want %q", in, got, want)
		}
	}
}

// Dumped source for reserved-word / schema / extension objects must quote where
// needed and recompile cleanly (regression: renderObjectDPG emitted identifiers
// raw, so a reserved-word table/column produced non-compiling source).
func TestRenderReservedWordAndStructuralCompile(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	comment := "reporting schema"
	tblComment := "a table's comment"
	where := `"select" > 0`
	objs := []pipeline.IRObject{
		&ir.Schema{Name: "reporting", Comment: &comment},
		&ir.Schema{Name: "select"},
		&ir.Extension{Name: "hstore"},
		// ENUM must render "AS ENUM (...)" with single-quoted labels.
		&ir.Type{Schema: "public", Name: "mood", Variant: "ENUM", EnumValues: []string{"happy", "it's ok"}},
		// Reserved-word names + a COMMENT + an INDEX (INDICES block) must all recompile.
		&ir.Table{Schema: "public", Name: "user", Comment: &tblComment,
			Columns: []*ir.Column{
				{Name: "select", Type: ir.TypeRef{Name: "integer"}},
				{Name: "id", Type: ir.TypeRef{Name: "integer"}},
			},
			Indexes: []*ir.Index{
				{Name: "idx_user_sel", Columns: []pipeline.IndexColumn{{Name: "select"}}, Where: &where},
			}},
	}
	var b strings.Builder
	for _, o := range objs {
		renderObjectDPG(&b, o, fmtOpts)
	}
	rendered := b.String()
	// Reserved-word table/column/schema names must all be quoted.
	for _, q := range []string{`"user"`, `"select"`} {
		if !strings.Contains(rendered, q) {
			t.Errorf("expected %s quoted in output:\n%s", q, rendered)
		}
	}
	if !strings.Contains(rendered, `SCHEMA "select" {`) {
		t.Errorf("expected reserved-word schema name quoted, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "EXTENSION hstore;") {
		t.Errorf("expected bare EXTENSION hstore, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "SCHEMA reporting {") {
		t.Errorf("expected SCHEMA reporting block, got:\n%s", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("reserved-word/structural dump did not recompile: %v\n---\n%s", err, rendered)
	}
	var sawSchema, sawReservedSchema, sawExt, sawTable bool
	for _, o := range compiled {
		switch v := o.(type) {
		case *ir.Schema:
			if v.Name == "reporting" {
				sawSchema = true
			}
			if v.Name == "select" {
				sawReservedSchema = true
			}
		case *ir.Extension:
			if v.Name == "hstore" {
				sawExt = true
			}
		case *ir.Table:
			if v.Name == "user" {
				sawTable = true
			}
		}
	}
	if !sawSchema || !sawReservedSchema || !sawExt || !sawTable {
		t.Errorf("recompile missing objects: schema=%v reservedSchema=%v ext=%v table=%v", sawSchema, sawReservedSchema, sawExt, sawTable)
	}

	// Index columns must render bare in source (the differ's createIndex quotes
	// them when generating SQL). Emitting """select""" would create an index on a
	// literal 8-char column name. The dumped INDICES entry must contain (select),
	// not ("select").
	if strings.Contains(rendered, `("select")`) {
		t.Errorf("index column must be bare in dumped source, got quoted:\n%s", rendered)
	}

	// End-to-end: diffing the recompiled objects against an empty snapshot must
	// produce a CREATE INDEX with the column quoted exactly once, and with the
	// partial-index WHERE preserved.
	ops, err := diff.New().Diff(compiled, &pipeline.Snapshot{})
	if err != nil {
		t.Fatalf("diff of recompiled objects failed: %v", err)
	}
	var idxSQL string
	for _, o := range ops {
		if strings.Contains(o.SQL(), "CREATE") && strings.Contains(o.SQL(), "INDEX") {
			idxSQL = o.SQL()
			break
		}
	}
	if idxSQL == "" {
		t.Fatal("no CREATE INDEX op from recompiled table")
	}
	if strings.Contains(idxSQL, `"""select"""`) || !strings.Contains(idxSQL, `("select")`) {
		t.Errorf("index column not single-quoted correctly: %s", idxSQL)
	}
	if !strings.Contains(idxSQL, `WHERE "select" > 0`) {
		t.Errorf("partial-index WHERE lost through round-trip: %s", idxSQL)
	}
}

// TestRenderOwnerAndColumnStorage guards a dump false-negative found during
// the diff-coverage push: Owner and column Comment/Storage/Compression/
// Statistics are all genuinely diffed (diffTable/diffSchema/diffSequence/
// diffColumns) but were never rendered by dump, so a dumped project could
// never detect drift on any of them, forever. Also proves the Storage-vs-
// type-default suppression: only a genuine STORAGE override renders, not
// every column's ordinary type-driven default.
func TestRenderOwnerAndColumnStorage(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	owner := "app_owner"
	colComment := "internal id"
	compression := "lz4"
	stats := 500
	overrideStorage := "EXTERNAL"
	defaultStorage := "EXTENDED"
	objs := []pipeline.IRObject{
		&ir.Schema{Name: "app", Owner: &owner},
		&ir.Sequence{Schema: "public", Name: "seq_id", Owner: &owner},
		&ir.Table{Schema: "public", Name: "t", Owner: &owner,
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "integer"}, Comment: &colComment,
					Compression: &compression, Statistics: &stats,
					Storage: &overrideStorage, StorageIsTypeDefault: false},
				{Name: "body", Type: ir.TypeRef{Name: "text"},
					Storage: &defaultStorage, StorageIsTypeDefault: true},
			},
		},
	}
	var b strings.Builder
	for _, o := range objs {
		renderObjectDPG(&b, o, fmtOpts)
	}
	rendered := b.String()

	if !strings.Contains(rendered, "SCHEMA app {") || !strings.Contains(rendered, "OWNER app_owner;") {
		t.Errorf("expected schema OWNER, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "SEQUENCE") || strings.Count(rendered, "OWNER app_owner;") != 3 {
		t.Errorf("expected 3 OWNER declarations (schema, sequence, table), got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `COMMENT 'internal id';`) {
		t.Errorf("expected column COMMENT, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `COMPRESSION lz4;`) {
		t.Errorf("expected column COMPRESSION, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "STATISTICS 500;") {
		t.Errorf("expected column STATISTICS, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, `STORAGE "EXTERNAL";`) {
		t.Errorf("expected the overridden column's STORAGE to render, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "EXTENDED") {
		t.Errorf("type-default STORAGE must be suppressed (noise), got:\n%s", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("owner/storage dump did not recompile: %v\n---\n%s", err, rendered)
	}
	var sawSchemaOwner, sawSeqOwner, sawTableOwner bool
	var sawColAttrs bool
	for _, o := range compiled {
		switch v := o.(type) {
		case *ir.Schema:
			sawSchemaOwner = v.Owner != nil && *v.Owner == owner
		case *ir.Sequence:
			sawSeqOwner = v.Owner != nil && *v.Owner == owner
		case *ir.Table:
			sawTableOwner = v.Owner != nil && *v.Owner == owner
			for _, col := range v.Columns {
				if col.Name == "id" && col.Comment != nil && *col.Comment == colComment &&
					col.Compression != nil && *col.Compression == compression &&
					col.Statistics != nil && *col.Statistics == stats &&
					col.Storage != nil && *col.Storage == overrideStorage {
					sawColAttrs = true
				}
			}
		}
	}
	if !sawSchemaOwner || !sawSeqOwner || !sawTableOwner {
		t.Errorf("recompile missing owner: schema=%v sequence=%v table=%v", sawSchemaOwner, sawSeqOwner, sawTableOwner)
	}
	if !sawColAttrs {
		t.Error("recompile missing column comment/compression/statistics/storage")
	}
}

// TestRenderFunctionAndProcedureBody guards the fix for a much deeper dump
// gap than a rendering tweak: FUNCTION previously rendered only a
// placeholder comment ("body omitted"), and PROCEDURE had no case in
// renderObjectDPG's switch at all — dump silently dropped every procedure
// from generated source. dump can now actually reconstruct both, using
// Attrs.Body (already populated by the IR builder from source, and now also
// by introspection from pg_proc.prosrc). Also guards the "no CREATE verb"
// rule (RFC §3.4-adjacent convention already enforced elsewhere in this
// file, e.g. renderOpaqueBody) — a naive port of differ.go's
// buildFunctionSQL/createProcedure (which DO emit CREATE, since that's real
// SQL, not DPG source) would have produced unparseable DPG source.
func TestRenderFunctionAndProcedureBody(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	fnComment := "adds two integers"
	fnBody := "BEGIN\n    RETURN a + b;\nEND;"
	procBody := "BEGIN\n    NULL;\nEND;"
	objs := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "add_ints",
			Args: []ir.FuncArg{
				{Name: "a", Type: ir.TypeRef{Name: "integer"}},
				{Name: "b", Type: ir.TypeRef{Name: "integer"}},
			},
			ReturnType: ir.TypeRef{Name: "integer"},
			Attrs:      ir.FuncAttrs{Language: "plpgsql", Volatility: "IMMUTABLE", Strict: true, Body: fnBody},
			Comment:    &fnComment,
			Grants:     []ir.Grant{{Privileges: []string{"EXECUTE"}, Roles: []string{"app_user"}}},
		},
		&ir.Procedure{
			Schema: "public", Name: "recalc",
			Attrs: ir.FuncAttrs{Language: "plpgsql", Body: procBody},
		},
	}
	var b strings.Builder
	for _, o := range objs {
		renderObjectDPG(&b, o, fmtOpts)
	}
	rendered := b.String()

	if strings.Contains(rendered, "CREATE FUNCTION") || strings.Contains(rendered, "CREATE PROCEDURE") {
		t.Errorf("DPG source must not begin a declaration with CREATE, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "FUNCTION add_ints(") {
		t.Errorf("expected bare FUNCTION declaration, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "RETURN a + b;") {
		t.Errorf("expected the real function body rendered, not a placeholder, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "IMMUTABLE") || !strings.Contains(rendered, "STRICT") {
		t.Errorf("expected IMMUTABLE/STRICT rendered, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "COMMENT 'adds two integers';") {
		t.Errorf("expected function COMMENT, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "GRANT EXECUTE TO app_user;") {
		t.Errorf("expected function GRANT, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "PROCEDURE recalc(") {
		t.Errorf("expected a PROCEDURE declaration (previously dropped entirely), got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "NULL;") {
		t.Errorf("expected the real procedure body rendered, got:\n%s", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("function/procedure dump did not recompile: %v\n---\n%s", err, rendered)
	}
	var sawFunc, sawProc bool
	for _, o := range compiled {
		switch v := o.(type) {
		case *ir.Function:
			if v.Name == "add_ints" {
				sawFunc = v.BodyHash == ir.HashBody(fnBody) && v.Comment != nil && *v.Comment == fnComment &&
					len(v.Grants) == 1 && v.Attrs.Volatility == "IMMUTABLE" && v.Attrs.Strict
			}
		case *ir.Procedure:
			if v.Name == "recalc" {
				sawProc = v.BodyHash == ir.HashBody(procBody)
			}
		}
	}
	if !sawFunc {
		t.Error("recompile missing function body/comment/grant/attrs fidelity")
	}
	if !sawProc {
		t.Error("recompile missing procedure body fidelity")
	}
}

// TestRenderFunctionParallelCostRowsRoundtrip guards the full render →
// recompile chain for PARALLEL/COST/ROWS specifically — previously not
// rendered by dump at all despite ir.FuncAttrs already having fields for
// them. Covers both the explicit-value case and the default-suppression
// case (PARALLEL UNSAFE and unset Cost/Rows must render nothing, matching
// the same "don't render PostgreSQL's own default" precedent already
// established for column STORAGE).
func TestRenderFunctionParallelCostRowsRoundtrip(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	cost := 500.0
	rows := 50.0
	objs := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f_explicit",
			ReturnType: ir.TypeRef{Name: "integer"},
			Attrs: ir.FuncAttrs{
				Language: "sql", Volatility: "STABLE", Parallel: "SAFE",
				Cost: &cost, Rows: &rows, Body: "SELECT 1",
			},
		},
		&ir.Function{
			Schema: "public", Name: "f_default",
			ReturnType: ir.TypeRef{Name: "integer"},
			Attrs:      ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT 1"},
		},
	}
	var b strings.Builder
	for _, o := range objs {
		renderObjectDPG(&b, o, fmtOpts)
	}
	rendered := b.String()

	if !strings.Contains(rendered, "PARALLEL SAFE") {
		t.Errorf("expected PARALLEL SAFE rendered for f_explicit, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "COST 500") {
		t.Errorf("expected COST 500 rendered for f_explicit, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "ROWS 50") {
		t.Errorf("expected ROWS 50 rendered for f_explicit, got:\n%s", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("did not recompile: %v\n---\n%s", err, rendered)
	}
	for _, o := range compiled {
		fn, ok := o.(*ir.Function)
		if !ok {
			continue
		}
		switch fn.Name {
		case "f_explicit":
			if fn.Attrs.Parallel != "SAFE" {
				t.Errorf("f_explicit Parallel round-trip: got %q", fn.Attrs.Parallel)
			}
			if fn.Attrs.Cost == nil || *fn.Attrs.Cost != 500 {
				t.Errorf("f_explicit Cost round-trip: got %v", fn.Attrs.Cost)
			}
			if fn.Attrs.Rows == nil || *fn.Attrs.Rows != 50 {
				t.Errorf("f_explicit Rows round-trip: got %v", fn.Attrs.Rows)
			}
		case "f_default":
			if strings.Contains(rendered[strings.Index(rendered, "f_default"):], "PARALLEL") {
				t.Errorf("f_default must not render PARALLEL (matches PostgreSQL's own UNSAFE default)")
			}
			if fn.Attrs.Cost != nil {
				t.Errorf("f_default Cost: got %v, want nil (never rendered, so never re-parsed)", fn.Attrs.Cost)
			}
			if fn.Attrs.Rows != nil {
				t.Errorf("f_default Rows: got %v, want nil", fn.Attrs.Rows)
			}
		}
	}
}

// TestRenderFunctionSetOfRoundtrip guards the render -> recompile chain for
// RETURNS SETOF specifically: ReturnType.SetOf (pg_query's TypeName.Setof)
// was previously never read anywhere in the codebase, so dump never rendered
// SETOF and the compiler silently dropped it from hand-written source too.
// Also covers the plain (non-SETOF) case as a negative control.
func TestRenderFunctionSetOfRoundtrip(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	objs := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f_setof",
			ReturnType: ir.TypeRef{Name: "integer", SetOf: true},
			Attrs:      ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT n"},
		},
		&ir.Function{
			Schema: "public", Name: "f_plain",
			ReturnType: ir.TypeRef{Name: "integer", SetOf: false},
			Attrs:      ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT n"},
		},
	}
	var b strings.Builder
	for _, o := range objs {
		renderObjectDPG(&b, o, fmtOpts)
	}
	rendered := b.String()

	if !strings.Contains(rendered, "RETURNS SETOF integer") {
		t.Errorf("expected RETURNS SETOF integer rendered for f_setof, got:\n%s", rendered)
	}
	if strings.Contains(rendered[strings.Index(rendered, "f_plain"):], "SETOF") {
		t.Errorf("f_plain must not render SETOF, got:\n%s", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("did not recompile: %v\n---\n%s", err, rendered)
	}
	for _, o := range compiled {
		fn, ok := o.(*ir.Function)
		if !ok {
			continue
		}
		switch fn.Name {
		case "f_setof":
			if !fn.ReturnType.SetOf {
				t.Error("f_setof ReturnType.SetOf round-trip: got false, want true")
			}
		case "f_plain":
			if fn.ReturnType.SetOf {
				t.Error("f_plain ReturnType.SetOf round-trip: got true, want false")
			}
		}
	}
}

// TestRenderIndexVariantsRoundtrip guards the full render → recompile → createIndex
// chain for every index variant. Apply runs createIndex's SQL, so asserting it
// here (fast) is equivalent to dump → apply for the index class. Regressions in
// this chain (sort-order lost on parse, INCLUDE dropped, columns double-quoted)
// are invisible to plan and only surface on apply.
func TestRenderIndexVariantsRoundtrip(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	where := "b > 0"
	tbl := &ir.Table{
		Schema: "public", Name: "t",
		Columns: []*ir.Column{
			{Name: "a", Type: ir.TypeRef{Name: "int4"}},
			{Name: "b", Type: ir.TypeRef{Name: "int4"}},
			{Name: "MixedCol", Type: ir.TypeRef{Name: "text"}},
		},
		Indexes: []*ir.Index{
			{Name: "i_uniq", Unique: true, Columns: []pipeline.IndexColumn{{Name: "a"}}},
			{Name: "i_sort", Columns: []pipeline.IndexColumn{{Name: "MixedCol", SortOrder: "DESC", Nulls: "LAST"}, {Name: "b", SortOrder: "ASC"}}},
			{Name: "i_partial", Columns: []pipeline.IndexColumn{{Name: "b"}}, Where: &where},
			{Name: "i_cover", Columns: []pipeline.IndexColumn{{Name: "a"}}, Include: []string{"MixedCol", "b"}},
			{Name: "i_expr", Columns: []pipeline.IndexColumn{{Expr: &pipeline.RawExpr{Text: "lower(\"MixedCol\")"}}}},
			{Name: "i_gin", Method: "gin", Columns: []pipeline.IndexColumn{{Expr: &pipeline.RawExpr{Text: "to_tsvector('english', \"MixedCol\")"}}}},
			{Name: "i_nulls_nd", Unique: true, Columns: []pipeline.IndexColumn{{Name: "a"}}, NullsNotDistinct: true},
			{Name: "i_with", Columns: []pipeline.IndexColumn{{Name: "a"}}, With: []pipeline.StorageParam{{Key: "fillfactor", Value: "70"}}},
		},
	}
	var b strings.Builder
	renderObjectDPG(&b, tbl, fmtOpts)
	rendered := b.String()

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("index variants did not recompile: %v\n---\n%s", err, rendered)
	}
	ops, err := diff.New().Diff(compiled, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, o := range ops {
		sql := o.SQL()
		for _, n := range []string{"i_uniq", "i_sort", "i_partial", "i_cover", "i_expr", "i_gin", "i_nulls_nd", "i_with"} {
			if strings.Contains(sql, `"`+n+`"`) {
				byName[n] = sql
			}
		}
	}
	// Each variant's generated SQL must be correct — no double-quoting, sort-order
	// preserved, INCLUDE present, WHERE present, method carried.
	checks := map[string][]string{
		"i_uniq":     {`CREATE UNIQUE INDEX "i_uniq"`, `("a")`},
		"i_sort":     {`"MixedCol" DESC NULLS LAST`, `"b" ASC`},
		"i_partial":  {`("b")`, `WHERE b > 0`},
		"i_cover":    {`("a")`, `INCLUDE ("MixedCol", "b")`},
		"i_expr":     {`(lower("MixedCol"))`},
		"i_gin":      {`USING gin`, `to_tsvector('english', "MixedCol")`},
		"i_nulls_nd": {`CREATE UNIQUE INDEX "i_nulls_nd"`, `NULLS NOT DISTINCT`},
		"i_with":     {`("a")`, `WITH (fillfactor=70)`},
	}
	for name, wants := range checks {
		sql, ok := byName[name]
		if !ok {
			t.Errorf("%s: no CREATE INDEX op emitted", name)
			continue
		}
		if strings.Contains(sql, `"""`) {
			t.Errorf("%s: double-quoted identifier: %s", name, sql)
		}
		for _, w := range wants {
			if !strings.Contains(sql, w) {
				t.Errorf("%s: missing %q in: %s", name, w, sql)
			}
		}
	}
}

// TestRenderExcludeConstraintRoundtrip proves dump can reconstruct a real
// EXCLUDE constraint into valid, recompilable DPG source. EXCLUDE isn't in
// isInlineable, so it must render via the generic table-level constraint
// path (CONSTRAINT name <Expr>) — this exercises that path with an Expr
// that's genuinely populated (access method, two elements + operators, a
// WHERE clause) rather than the old bare "EXCLUDE" placeholder, which would
// have failed to even parse back.
func TestRenderExcludeConstraintRoundtrip(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	tbl := &ir.Table{
		Schema: "public", Name: "bookings",
		Columns: []*ir.Column{
			{Name: "room", Type: ir.TypeRef{Name: "int4"}},
			{Name: "during", Type: ir.TypeRef{Name: "tsrange"}},
		},
		Constraints: []*ir.Constraint{
			{Name: "no_overlap", Type: "EXCLUDE", Columns: []string{"room", "during"},
				Exclude: &ir.ExcludeSpec{
					AccessMethod: "gist",
					Elements: []ir.ExcludeElement{
						{Column: "room", Operator: "="},
						{Column: "during", Operator: "&&"},
					},
					Where: "room > 0",
				},
				Expr: `EXCLUDE USING gist ("room" WITH =, "during" WITH &&) WHERE (room > 0)`,
			},
		},
	}
	var b strings.Builder
	renderObjectDPG(&b, tbl, fmtOpts)
	rendered := b.String()

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("EXCLUDE constraint did not recompile: %v\n---\n%s", err, rendered)
	}
	ops, err := diff.New().Diff(compiled, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected a CREATE TABLE op")
	}
	sql := ops[0].SQL()
	if !strings.Contains(sql, `CONSTRAINT "no_overlap" EXCLUDE USING gist ("room" WITH =, "during" WITH &&) WHERE (room > 0)`) {
		t.Errorf("expected the real EXCLUDE body to survive render+recompile, got: %s", sql)
	}
}
