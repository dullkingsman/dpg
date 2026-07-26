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
