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

// TestRenderIndexUsingMethodCompiles guards a real bug found live-testing a
// demo project: the RFC's ABNF and every one of its worked examples
// (rfc/dpg-1.md §7.7, e.g. "idx_location USING gist (location);") place
// USING before the column list, matching real PostgreSQL's own
// CREATE INDEX ... USING method (columns) order — but the blockparser used
// to accept USING only AFTER the columns, silently rejecting the RFC's own
// syntax. This guards both halves of the round-trip: renderIndex must emit
// USING before the columns (matching the parser's corrected grammar), and
// the result must actually recompile.
func TestRenderIndexUsingMethodCompiles(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	tbl := &ir.Table{
		Schema: "public", Name: "t",
		Columns: []*ir.Column{{Name: "tags", Type: ir.TypeRef{Name: "jsonb"}}},
		Indexes: []*ir.Index{
			{Name: "t_tags_gin", Method: "gin", Columns: []pipeline.IndexColumn{{Name: "tags"}}},
		},
	}

	var b strings.Builder
	renderObjectDPG(&b, tbl, fmtOpts)
	rendered := b.String()

	if !strings.Contains(rendered, "USING gin (") {
		t.Errorf("rendered index does not place USING before the column list: %q", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.Compile([]string{f}, dir, pipeline.Default); err != nil {
		t.Fatalf("dumped table with a USING-method index failed to recompile: %v\n---\n%s", err, rendered)
	}
}

// TestRenderIndexUniqueConcurrentlyPrefixCompiles guards the CONCURRENTLY
// grammar fix: real PostgreSQL's CONCURRENTLY is a bare presence keyword
// (CREATE [UNIQUE] INDEX [CONCURRENTLY] name), never a boolean toggle — DPG
// used to accept a trailing "CONCURRENTLY <bool>;" clause with no PG
// equivalent. renderIndex must emit both UNIQUE and CONCURRENTLY as prefix
// keywords before the name, in that order, matching real PG exactly, and
// the result must recompile.
// TestRenderForeignTableCompiles guards a real bug found live-testing a
// demo project: renderObjectDPG's *ir.Table case hardcoded the "TABLE"
// keyword regardless of Foreign, and never emitted SERVER/OPTIONS at all —
// dump would silently turn a foreign table into a (broken, unrecompilable)
// plain table declaration. Also covers Unlogged's keyword, found broken the
// same way while fixing this.
func TestRenderForeignTableCompiles(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	server := "loopback_server"
	tbl := &ir.Table{
		Schema: "public", Name: "remote_users",
		Foreign: true, ForeignServer: &server,
		ForeignOptions: []pipeline.StorageParam{
			{Key: "table_name", Value: "users"},
			{Key: "schema_name", Value: "public"},
		},
		Columns: []*ir.Column{
			{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
			{Name: "email", Type: ir.TypeRef{Name: "text"}},
		},
	}

	var b strings.Builder
	renderObjectDPG(&b, tbl, fmtOpts)
	rendered := b.String()

	if !strings.Contains(rendered, "FOREIGN TABLE remote_users") {
		t.Errorf("rendered table does not use the FOREIGN TABLE keyword: %q", rendered)
	}
	if !strings.Contains(rendered, `SERVER loopback_server`) {
		t.Errorf("rendered table missing SERVER clause: %q", rendered)
	}
	if !strings.Contains(rendered, "OPTIONS (table_name 'users', schema_name 'public')") {
		t.Errorf("rendered table missing OPTIONS clause: %q", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped foreign table failed to recompile: %v\n---\n%s", err, rendered)
	}
	var found *ir.Table
	for _, o := range compiled {
		if tb, ok := o.(*ir.Table); ok && tb.Name == "remote_users" {
			found = tb
		}
	}
	if found == nil {
		t.Fatal("remote_users missing after recompile")
	}
	if !found.Foreign || found.ForeignServer == nil || *found.ForeignServer != "loopback_server" {
		t.Errorf("Foreign/ForeignServer did not round-trip: Foreign=%v ForeignServer=%v", found.Foreign, found.ForeignServer)
	}
	if len(found.ForeignOptions) != 2 {
		t.Errorf("ForeignOptions did not round-trip: got %v", found.ForeignOptions)
	}
}

// TestRenderUnloggedTableCompiles guards the UNLOGGED keyword gap found
// alongside the FOREIGN TABLE one: dump's table renderer never checked
// o.Unlogged either.
func TestRenderUnloggedTableCompiles(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	tbl := &ir.Table{
		Schema: "public", Name: "sessions",
		Unlogged: true,
		Columns:  []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
	}

	var b strings.Builder
	renderObjectDPG(&b, tbl, fmtOpts)
	rendered := b.String()

	if !strings.Contains(rendered, "UNLOGGED TABLE sessions") {
		t.Errorf("rendered table does not use the UNLOGGED TABLE keyword: %q", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped unlogged table failed to recompile: %v\n---\n%s", err, rendered)
	}
	var found *ir.Table
	for _, o := range compiled {
		if tb, ok := o.(*ir.Table); ok && tb.Name == "sessions" {
			found = tb
		}
	}
	if found == nil || !found.Unlogged {
		t.Errorf("Unlogged did not round-trip: %+v", found)
	}
}

// TestRenderTableGrantsAndRevocationsCompiles guards a real bug found live-
// testing REVOCATIONS against a demo project: case *ir.Table in
// renderObjectDPG had NO Grants/Revocations rendering at all — neither
// table-level nor per-column — even though both are fully diffed and applied
// correctly. A dumped table silently lost every GRANT/REVOCATION declaration,
// so a dumped project could never detect drift on them. Uses the flat Mode B
// GRANT/REVOCATION style (matching writeViewBlock's existing convention),
// both at the table level and nested inside a per-column COLUMNS entry —
// mirroring the RFC §7.5 worked example's "COLUMN ssn { GRANTS {...}
// REVOCATIONS {...} }" shape.
func TestRenderTableGrantsAndRevocationsCompiles(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	tbl := &ir.Table{
		Schema: "public", Name: "users",
		Columns: []*ir.Column{
			{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
			{
				Name: "email", Type: ir.TypeRef{Name: "text"},
				Grants:      []ir.Grant{{Privileges: []string{"SELECT"}, Roles: []string{"app_service"}}},
				Revocations: []ir.Revocation{{Privileges: nil, Roles: []string{"PUBLIC"}}},
			},
		},
		Grants:      []ir.Grant{{Privileges: []string{"SELECT"}, Roles: []string{"app_service"}}},
		Revocations: []ir.Revocation{{Privileges: nil, Roles: []string{"PUBLIC"}}},
	}

	var b strings.Builder
	renderObjectDPG(&b, tbl, fmtOpts)
	rendered := b.String()

	if !strings.Contains(rendered, "GRANT SELECT TO app_service;") {
		t.Errorf("expected table-level GRANT, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "REVOCATION ALL FROM PUBLIC;") {
		t.Errorf("expected table-level REVOCATION, got:\n%s", rendered)
	}
	if strings.Count(rendered, "GRANT SELECT TO app_service;") != 2 {
		t.Errorf("expected both table-level and column-level GRANT, got:\n%s", rendered)
	}
	if strings.Count(rendered, "REVOCATION ALL FROM PUBLIC;") != 2 {
		t.Errorf("expected both table-level and column-level REVOCATION, got:\n%s", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped table grants/revocations failed to recompile: %v\n---\n%s", err, rendered)
	}
	var found *ir.Table
	for _, o := range compiled {
		if tb, ok := o.(*ir.Table); ok && tb.Name == "users" {
			found = tb
		}
	}
	if found == nil {
		t.Fatalf("table users missing after recompile\n---\n%s", rendered)
	}
	if len(found.Grants) != 1 || found.Grants[0].Roles[0] != "app_service" {
		t.Errorf("table Grants did not round-trip: %+v", found.Grants)
	}
	if len(found.Revocations) != 1 || found.Revocations[0].Roles[0] != "PUBLIC" {
		t.Errorf("table Revocations did not round-trip: %+v", found.Revocations)
	}
	var emailCol *ir.Column
	for _, c := range found.Columns {
		if c.Name == "email" {
			emailCol = c
		}
	}
	if emailCol == nil {
		t.Fatal("email column missing after recompile")
	}
	if len(emailCol.Grants) != 1 || emailCol.Grants[0].Roles[0] != "app_service" {
		t.Errorf("column Grants did not round-trip: %+v", emailCol.Grants)
	}
	if len(emailCol.Revocations) != 1 || emailCol.Revocations[0].Roles[0] != "PUBLIC" {
		t.Errorf("column Revocations did not round-trip: %+v", emailCol.Revocations)
	}
}

// TestRenderSubPartitionedTableCompiles guards a real, pre-existing gap
// found while implementing RFC §7.13 sub-partitioning: dump had NO support
// for rendering PARTITION BY or PARTITIONS { } at all (not even the flat,
// non-nested case) — a dumped partitioned table silently lost its entire
// partition structure. This exercises both the top-level PARTITION BY clause
// and a nested sub-partition (a partition with its own PARTITION BY and
// PARTITIONS block), round-tripped through the real compiler.
func TestRenderSubPartitionedTableCompiles(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	tbl := &ir.Table{
		Schema: "public", Name: "metrics",
		Columns: []*ir.Column{
			{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
			{Name: "created_at", Type: ir.TypeRef{Name: "timestamptz"}, NotNull: true},
			{Name: "channel", Type: ir.TypeRef{Name: "text"}, NotNull: true},
		},
		PartitionBy: &ir.PartitionSpec{Strategy: "RANGE", Columns: []string{"created_at"}},
		Partitions: []*ir.Partition{
			{Name: "metrics_2025", Bounds: "FOR VALUES FROM ('2025-01-01') TO ('2026-01-01')"},
			{
				Name:        "metrics_2026",
				Bounds:      "FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')",
				PartitionBy: &ir.PartitionSpec{Strategy: "LIST", Columns: []string{"channel"}},
				Partitions: []*ir.Partition{
					{Name: "metrics_2026_web", Bounds: "FOR VALUES IN ('web')"},
					{Name: "metrics_2026_other", Bounds: "FOR VALUES IN ('mobile', 'api')"},
				},
			},
		},
	}

	var b strings.Builder
	renderObjectDPG(&b, tbl, fmtOpts)
	rendered := b.String()

	if !strings.Contains(rendered, "PARTITION BY RANGE (created_at)") {
		t.Errorf("rendered table missing top-level PARTITION BY: %q", rendered)
	}
	if !strings.Contains(rendered, "PARTITION BY LIST (channel)") {
		t.Errorf("rendered table missing nested PARTITION BY: %q", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped sub-partitioned table failed to recompile: %v\n---\n%s", err, rendered)
	}
	var found *ir.Table
	for _, o := range compiled {
		if tb, ok := o.(*ir.Table); ok && tb.Name == "metrics" {
			found = tb
		}
	}
	if found == nil {
		t.Fatalf("table metrics missing after recompile\n---\n%s", rendered)
	}
	if found.PartitionBy == nil || found.PartitionBy.Strategy != "RANGE" {
		t.Errorf("top-level PartitionBy did not round-trip: %+v", found.PartitionBy)
	}
	if len(found.Partitions) != 2 {
		t.Fatalf("expected 2 partitions after round-trip, got %d\n---\n%s", len(found.Partitions), rendered)
	}
	var sub *ir.Partition
	for _, p := range found.Partitions {
		if p.Name == "metrics_2026" {
			sub = p
		}
	}
	if sub == nil {
		t.Fatal("metrics_2026 partition missing after round-trip")
	}
	if sub.PartitionBy == nil || sub.PartitionBy.Strategy != "LIST" {
		t.Fatalf("metrics_2026 sub-partition PartitionBy did not round-trip: %+v", sub.PartitionBy)
	}
	if len(sub.Partitions) != 2 {
		t.Fatalf("expected 2 nested sub-partitions after round-trip, got %d", len(sub.Partitions))
	}
}

// TestRenderMaterializedViewCompiles guards a real bug found live-testing a
// demo project: renderObjectDPG's View case always emitted the bare "VIEW"
// keyword regardless of o.Materialized, and never rendered Owner, Comment,
// Grants, Revocations, or WITH NO DATA at all — a live MATERIALIZED VIEW
// round-tripped through dump as a plain VIEW, silently losing the object
// kind (confirmed against a real demo database: introspection correctly set
// Materialized=true, but the rendered source dropped it entirely).
func TestRenderMaterializedViewCompiles(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	owner := "app_role"
	comment := "precomputed totals"
	mv := &ir.View{
		Schema:       "public",
		Name:         "order_status_totals",
		Materialized: true,
		Query:        "SELECT status, count(*) AS c FROM orders GROUP BY status",
		Owner:        &owner,
		Comment:      &comment,
		Grants:       []ir.Grant{{Privileges: []string{"SELECT"}, Roles: []string{"app_readonly"}}},
		Revocations:  []ir.Revocation{{Privileges: []string{"SELECT"}, Roles: []string{"PUBLIC"}}},
		Indexes: []*ir.Index{
			{Name: "order_status_totals_status_uq", Unique: true, Columns: []pipeline.IndexColumn{{Name: "status"}}},
		},
	}

	var b strings.Builder
	renderObjectDPG(&b, mv, fmtOpts)
	rendered := b.String()

	if !strings.Contains(rendered, "MATERIALIZED VIEW order_status_totals") {
		t.Errorf("rendered materialized view does not use the MATERIALIZED VIEW keyword: %q", rendered)
	}
	if !strings.Contains(rendered, "OWNER app_role") {
		t.Errorf("rendered materialized view missing OWNER: %q", rendered)
	}
	if !strings.Contains(rendered, "GRANT SELECT TO app_readonly") {
		t.Errorf("rendered materialized view missing GRANT: %q", rendered)
	}
	if !strings.Contains(rendered, "REVOCATION SELECT FROM PUBLIC") {
		t.Errorf("rendered materialized view missing REVOCATION (RFC §11.3 Mode B singular keyword — not REVOKE, which the block parser doesn't recognize): %q", rendered)
	}
	if !strings.Contains(rendered, "INDICES") || !strings.Contains(rendered, "order_status_totals_status_uq") {
		t.Errorf("rendered materialized view missing INDICES block: %q", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped materialized view failed to recompile: %v\n---\n%s", err, rendered)
	}
	var found *ir.View
	for _, o := range compiled {
		if v, ok := o.(*ir.View); ok && v.Name == "order_status_totals" {
			found = v
		}
	}
	if found == nil {
		t.Fatalf("view order_status_totals missing after recompile\n---\n%s", rendered)
	}
	if !found.Materialized {
		t.Error("Materialized did not round-trip")
	}
	if found.Owner == nil || *found.Owner != "app_role" {
		t.Errorf("Owner did not round-trip: %v", found.Owner)
	}
	if found.Comment == nil || *found.Comment != "precomputed totals" {
		t.Errorf("Comment did not round-trip: %v", found.Comment)
	}
	if len(found.Grants) != 1 {
		t.Errorf("Grants did not round-trip: %v", found.Grants)
	}
	if len(found.Revocations) != 1 || found.Revocations[0].Roles[0] != "PUBLIC" {
		t.Errorf("Revocations did not round-trip: %v", found.Revocations)
	}
	if len(found.Indexes) != 1 || found.Indexes[0].Name != "order_status_totals_status_uq" || !found.Indexes[0].Unique {
		t.Errorf("Indexes did not round-trip: %v", found.Indexes)
	}
}

// TestRenderRecursiveViewRendersAsPlainViewCompiles guards a self-recursive
// view's dump output: it must NOT reuse the special "RECURSIVE VIEW (cols)
// AS ..." PG syntax, because that requires an explicit column list DPG
// doesn't track and dump only ever sees introspected objects, whose Query
// (from pg_get_viewdef) already comes back as a self-contained "WITH
// RECURSIVE ..." CTE — which is already valid and self-recursive under a
// plain CREATE VIEW (the same reconstruction pg_dump itself uses). Emitting
// "RECURSIVE VIEW" with that query shape is a PG syntax error.
func TestRenderRecursiveViewRendersAsPlainViewCompiles(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	v := &ir.View{
		Schema:    "public",
		Name:      "category_tree",
		Recursive: true,
		Query: "WITH RECURSIVE category_tree AS (" +
			"SELECT id FROM categories WHERE parent_id IS NULL " +
			"UNION ALL " +
			"SELECT c.id FROM categories c JOIN category_tree t ON c.parent_id = t.id" +
			") SELECT id FROM category_tree",
	}

	var b strings.Builder
	renderObjectDPG(&b, v, fmtOpts)
	rendered := b.String()

	if strings.Contains(rendered, "RECURSIVE VIEW") {
		t.Errorf("rendered view must not use the RECURSIVE VIEW keyword (query already self-contains WITH RECURSIVE): %q", rendered)
	}
	if !strings.Contains(rendered, "VIEW category_tree AS WITH RECURSIVE") {
		t.Errorf("rendered view should be a plain VIEW wrapping the WITH RECURSIVE query: %q", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped recursive view failed to recompile: %v\n---\n%s", err, rendered)
	}
	var found *ir.View
	for _, o := range compiled {
		if vv, ok := o.(*ir.View); ok && vv.Name == "category_tree" {
			found = vv
		}
	}
	if found == nil {
		t.Fatalf("view category_tree missing after recompile\n---\n%s", rendered)
	}
}

func TestRenderIndexUniqueConcurrentlyPrefixCompiles(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	tbl := &ir.Table{
		Schema: "public", Name: "t",
		Columns: []*ir.Column{{Name: "email", Type: ir.TypeRef{Name: "text"}}},
		Indexes: []*ir.Index{
			{Name: "t_email_uc", Unique: true, Concurrently: true, Columns: []pipeline.IndexColumn{{Name: "email"}}},
		},
	}

	var b strings.Builder
	renderObjectDPG(&b, tbl, fmtOpts)
	rendered := b.String()

	if !strings.Contains(rendered, "UNIQUE CONCURRENTLY t_email_uc (") {
		t.Errorf("rendered index does not place UNIQUE then CONCURRENTLY before the name: %q", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped table with a UNIQUE CONCURRENTLY index failed to recompile: %v\n---\n%s", err, rendered)
	}
	var found *ir.Index
	for _, o := range compiled {
		if tb, ok := o.(*ir.Table); ok {
			for _, idx := range tb.Indexes {
				if idx.Name == "t_email_uc" {
					found = idx
				}
			}
		}
	}
	if found == nil {
		t.Fatal("index t_email_uc missing after recompile")
	}
	if !found.Unique || !found.Concurrently {
		t.Errorf("Unique/Concurrently did not round-trip: got Unique=%v Concurrently=%v", found.Unique, found.Concurrently)
	}
}

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
		&ir.Subscription{Name: "my_sub", Body: "CREATE SUBSCRIPTION my_sub CONNECTION 'host=x' PUBLICATION my_pub"},
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
		"*ir.Publication": false, "*ir.Subscription": false, "*ir.EventTrigger": false, "*ir.Collation": false,
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

// TestRenderOpaqueObjectsWithCommentCompile guards a real bug found live-
// testing a demo project: renderOpaqueBody unconditionally terminated every
// opaque declaration with a bare ";", regardless of the object's Comment
// field — so a Comment correctly populated by introspection (see
// internal/introspect/opaque.go) was silently dropped by dump, never even
// reaching a "{ COMMENT ...; }" block, though the analogous FUNCTION/
// PROCEDURE path (writeFuncBlock) already did this correctly. This is
// TestRenderOpaqueObjectsCompile's exact object set, but with Comment set on
// every kind that has the field (all but UserMapping/Publication, which
// don't support COMMENT ON in PostgreSQL) — the prior test's all-nil
// Comments never exercised the "{ }" branch at all, which is exactly how
// this gap went unnoticed.
func TestRenderOpaqueObjectsWithCommentCompile(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	c := func(s string) *string { return &s }

	objs := []pipeline.IRObject{
		&ir.ForeignDataWrapper{Name: "dummy_fdw", Body: "CREATE FOREIGN DATA WRAPPER dummy_fdw", Comment: c("fdw comment")},
		&ir.ForeignServer{Name: "dummy_srv", Body: "CREATE SERVER dummy_srv FOREIGN DATA WRAPPER dummy_fdw", Comment: c("srv comment")},
		&ir.Subscription{Name: "my_sub", Body: "CREATE SUBSCRIPTION my_sub CONNECTION 'host=x' PUBLICATION my_pub", Comment: c("sub comment")},
		&ir.EventTrigger{Name: "et", Body: "CREATE EVENT TRIGGER et ON ddl_command_start EXECUTE FUNCTION f()", Comment: c("et comment")},
		&ir.Collation{Schema: "public", Name: "my_coll", Body: "CREATE COLLATION public.my_coll (locale = 'C')", Comment: c("coll comment")},
		&ir.Cast{SourceType: ir.TypeRef{Name: "int4"}, TargetType: ir.TypeRef{Name: "bool"},
			Body: "CREATE CAST (integer AS boolean) WITHOUT FUNCTION", Comment: c("cast comment")},
		&ir.StatisticsObject{Schema: "public", Name: "st",
			Body: "CREATE STATISTICS public.st (dependencies) ON a, b FROM public.t", Comment: c("stats comment")},
		&ir.Tablespace{Name: "ts", Body: "CREATE TABLESPACE ts LOCATION '/tmp/x'", Comment: c("ts comment")},
		&ir.Operator{Schema: "public", Name: "===",
			Body: "CREATE OPERATOR public.=== (FUNCTION = int4eq, LEFTARG = integer, RIGHTARG = integer)", Comment: c("op comment")},
		&ir.OperatorClass{Schema: "public", Name: "my_opc", AccessMethod: "btree",
			Body: "CREATE OPERATOR CLASS public.my_opc FOR TYPE integer USING btree AS OPERATOR 3 =, FUNCTION 1 btint4cmp(integer, integer)", Comment: c("opc comment")},
		&ir.OperatorFamily{Schema: "public", Name: "my_fam", AccessMethod: "btree",
			Body: "CREATE OPERATOR FAMILY public.my_fam USING btree", Comment: c("opf comment")},
		&ir.TSConfig{Schema: "public", Name: "my_cfg",
			Body: `CREATE TEXT SEARCH CONFIGURATION public.my_cfg (PARSER = pg_catalog."default")`, Comment: c("cfg comment")},
		&ir.TSDict{Schema: "public", Name: "my_dict",
			Body: "CREATE TEXT SEARCH DICTIONARY public.my_dict (TEMPLATE = pg_catalog.simple)", Comment: c("dict comment")},
		&ir.TSParser{Schema: "public", Name: "my_prs",
			Body: "CREATE TEXT SEARCH PARSER public.my_prs (START = prsd_start, GETTOKEN = prsd_nexttoken, END = prsd_end, LEXTYPES = prsd_lextype)", Comment: c("prs comment")},
		&ir.TSTemplate{Schema: "public", Name: "my_tmpl",
			Body: "CREATE TEXT SEARCH TEMPLATE public.my_tmpl (LEXIZE = dsimple_lexize)", Comment: c("tmpl comment")},
	}

	var b strings.Builder
	for _, o := range objs {
		renderObjectDPG(&b, o, fmtOpts)
	}
	rendered := b.String()

	if !strings.Contains(rendered, "{\n    COMMENT 'fdw comment';\n}") {
		t.Errorf("rendered output does not carry an expected COMMENT block:\n%s", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped .dpg (with comments) failed to recompile: %v\n---\n%s", err, rendered)
	}

	wantComments := map[string]string{
		"my_coll": "coll comment",
		"st":      "stats comment",
		"ts":      "ts comment",
		"my_opc":  "opc comment",
		"my_fam":  "opf comment",
		"my_cfg":  "cfg comment",
		"my_dict": "dict comment",
		"my_prs":  "prs comment",
		"my_tmpl": "tmpl comment",
	}
	found := map[string]bool{}
	for _, o := range compiled {
		var name string
		var comment *string
		switch v := o.(type) {
		case *ir.Collation:
			name, comment = v.Name, v.Comment
		case *ir.StatisticsObject:
			name, comment = v.Name, v.Comment
		case *ir.Tablespace:
			name, comment = v.Name, v.Comment
		case *ir.OperatorClass:
			name, comment = v.Name, v.Comment
		case *ir.OperatorFamily:
			name, comment = v.Name, v.Comment
		case *ir.TSConfig:
			name, comment = v.Name, v.Comment
		case *ir.TSDict:
			name, comment = v.Name, v.Comment
		case *ir.TSParser:
			name, comment = v.Name, v.Comment
		case *ir.TSTemplate:
			name, comment = v.Name, v.Comment
		default:
			continue
		}
		want, ok := wantComments[name]
		if !ok {
			continue
		}
		found[name] = true
		if comment == nil {
			t.Errorf("%s: Comment is nil after round-trip, want %q", name, want)
			continue
		}
		if *comment != want {
			t.Errorf("%s: Comment = %q after round-trip, want %q", name, *comment, want)
		}
	}
	for name := range wantComments {
		if !found[name] {
			t.Errorf("%s: object missing after round-trip compile", name)
		}
	}
}

// TestRenderTSConfigMappingCompiles guards RFC §12.1's MAPPING FOR block on
// a TEXT SEARCH CONFIGURATION — previously dropped entirely: renderOpaqueBody
// only ever knew about Comment/Grants, with no path at all for
// o.Mappings, so a declared (or introspected) mapping never round-tripped
// through dump. Covers both a single-dictionary mapping and the real PG
// multi-dictionary fallback-chain syntax (WITH dict1, dict2).
func TestRenderTSConfigMappingCompiles(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	comment := "demo search config"
	tc := &ir.TSConfig{
		Schema: "public", Name: "demo_search",
		ParserSchema: "pg_catalog", ParserName: "default",
		Body:    `CREATE TEXT SEARCH CONFIGURATION public.demo_search (PARSER = pg_catalog."default")`,
		Comment: &comment,
		Mappings: []pipeline.TSMappingDef{
			{TokenTypes: []string{"word", "hword"}, Dictionaries: []pipeline.Identifier{{Name: "unaccent"}, {Name: "english_stem"}}},
			{TokenTypes: []string{"asciiword"}, Dictionaries: []pipeline.Identifier{{Name: "english_stem"}}},
		},
	}

	var b strings.Builder
	renderObjectDPG(&b, tc, fmtOpts)
	rendered := b.String()

	if strings.Contains(rendered, "CREATE TEXT SEARCH CONFIGURATION") {
		t.Errorf("DPG source must not begin a declaration with CREATE, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "MAPPING FOR word, hword WITH unaccent, english_stem;") {
		t.Errorf("expected the fallback-chain mapping, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "MAPPING FOR asciiword WITH english_stem;") {
		t.Errorf("expected the single-dictionary mapping, got:\n%s", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped ts config failed to recompile: %v\n---\n%s", err, rendered)
	}
	var found *ir.TSConfig
	for _, o := range compiled {
		if c, ok := o.(*ir.TSConfig); ok && c.Name == "demo_search" {
			found = c
		}
	}
	if found == nil {
		t.Fatalf("ts config demo_search missing after recompile\n---\n%s", rendered)
	}
	if len(found.Mappings) != 2 {
		t.Fatalf("Mappings did not round-trip: got %d, want 2: %+v", len(found.Mappings), found.Mappings)
	}
	var sawChain, sawSingle bool
	for _, m := range found.Mappings {
		if len(m.Dictionaries) == 2 && m.Dictionaries[0].Name == "unaccent" && m.Dictionaries[1].Name == "english_stem" {
			sawChain = true
		}
		if len(m.TokenTypes) == 1 && m.TokenTypes[0] == "asciiword" && len(m.Dictionaries) == 1 {
			sawSingle = true
		}
	}
	if !sawChain {
		t.Errorf("fallback-chain mapping did not round-trip: %+v", found.Mappings)
	}
	if !sawSingle {
		t.Errorf("single-dictionary mapping did not round-trip: %+v", found.Mappings)
	}
}

// TestRenderCompositeAndRangeTypesCompile guards a real bug found
// live-testing a demo project: renderObjectDPG's *ir.Type switch only ever
// had cases for "ENUM" and "DOMAIN" — COMPOSITE and RANGE both fell through
// to a "-- type X (VARIANT) omitted" comment instead of real DPG source,
// even though introspection already fully captured COMPOSITE (via
// CompositeAttrs) and, after the matching introspectRangeBodies fix, RANGE
// too. Same discipline as TestRenderOpaqueObjectsCompile: render, write to
// a real file, recompile, and diff the recompiled object against an empty
// snapshot to prove createType actually succeeds for it, not just that the
// rendered text looks plausible.
func TestRenderCompositeAndRangeTypesCompile(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}

	objs := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "address", Variant: "COMPOSITE",
			CompositeAttrs: []*ir.Column{
				{Name: "street", Type: ir.TypeRef{Name: "text"}},
				{Name: "city", Type: ir.TypeRef{Name: "text"}},
			},
		},
		&ir.Type{
			Schema: "public", Name: "price_range", Variant: "RANGE",
			Body: "SUBTYPE = numeric",
		},
	}

	var b strings.Builder
	for _, o := range objs {
		renderObjectDPG(&b, o, fmtOpts)
	}
	rendered := b.String()

	if strings.Contains(rendered, "omitted") {
		t.Fatalf("rendered output still contains an omitted-type comment:\n%s", rendered)
	}
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

	wantVariants := map[string]bool{"COMPOSITE": false, "RANGE": false}
	for _, o := range compiled {
		if tp, ok := o.(*ir.Type); ok {
			if _, tracked := wantVariants[tp.Variant]; tracked {
				wantVariants[tp.Variant] = true
			}
		}
	}
	for v, seen := range wantVariants {
		if !seen {
			t.Errorf("variant %s missing after recompile", v)
		}
	}

	if _, err := diff.New().Diff(compiled, &pipeline.Snapshot{}); err != nil {
		t.Fatalf("diffing dumped composite/range types failed: %v", err)
	}
}

// TestRenderRoleAttributesCompile guards dump's Role rendering (RFC §11.1):
// every attribute except PASSWORD (never introspected, see the renderer's
// own doc comment) must round-trip through dump -> recompile with matching
// values — a bare "ROLE name;" would silently drop LOGIN/SUPERUSER/
// membership/etc. from dumped source, defeating dump's actual purpose of
// capturing a live role's real configuration (harmless for plan --live
// specifically, since "undeclared means unmanaged", but still a real
// fidelity gap for anyone reading or version-controlling the dumped file).
func TestRenderRoleAttributesCompile(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	boolp := func(v bool) *bool { return &v }
	intp := func(v int) *int { return &v }
	strp := func(v string) *string { return &v }
	comment := "application service account"

	role := &ir.Role{
		Name: "app_service", CanLogin: boolp(true), Superuser: boolp(false),
		CreateDB: boolp(false), CreateRole: boolp(false), Inherit: boolp(true),
		IsReplication: boolp(false), BypassRLS: boolp(false), ConnectionLimit: intp(20),
		ValidUntil: strp("2030-01-01"),
		InRole:     []string{"reader_group"}, RoleMembers: []string{"member_role"}, AdminRoles: []string{"admin_role"},
		Comment: &comment,
		// Password deliberately unset — never introspected, must never be
		// rendered (there's nothing to render: dump only ever sees
		// introspected Role objects, which never populate Password).
	}

	var b strings.Builder
	renderObjectDPG(&b, role, fmtOpts)
	rendered := b.String()

	if strings.Contains(rendered, "PASSWORD") {
		t.Errorf("rendered Role must never mention PASSWORD (never introspected): %s", rendered)
	}
	for line := range strings.SplitSeq(rendered, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "CREATE ") {
			t.Errorf("rendered declaration begins with CREATE (violates no-verb mandate): %q", line)
		}
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "roles.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped Role failed to recompile: %v\n---\n%s", err, rendered)
	}
	if len(compiled) != 1 {
		t.Fatalf("expected 1 compiled object, got %d", len(compiled))
	}
	got, ok := compiled[0].(*ir.Role)
	if !ok {
		t.Fatalf("expected *ir.Role, got %T", compiled[0])
	}

	check := func(name string, got, want any) {
		t.Helper()
		gv, wv := reflect.ValueOf(got), reflect.ValueOf(want)
		if gv.Kind() == reflect.Pointer && wv.Kind() == reflect.Pointer {
			if gv.IsNil() != wv.IsNil() {
				t.Errorf("%s: got nil=%v, want nil=%v", name, gv.IsNil(), wv.IsNil())
				return
			}
			if !gv.IsNil() && gv.Elem().Interface() != wv.Elem().Interface() {
				t.Errorf("%s: got %v, want %v", name, gv.Elem(), wv.Elem())
			}
			return
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", name, got, want)
		}
	}
	check("CanLogin", got.CanLogin, role.CanLogin)
	check("Superuser", got.Superuser, role.Superuser)
	check("CreateDB", got.CreateDB, role.CreateDB)
	check("CreateRole", got.CreateRole, role.CreateRole)
	check("Inherit", got.Inherit, role.Inherit)
	check("IsReplication", got.IsReplication, role.IsReplication)
	check("BypassRLS", got.BypassRLS, role.BypassRLS)
	check("ConnectionLimit", got.ConnectionLimit, role.ConnectionLimit)
	check("ValidUntil", got.ValidUntil, role.ValidUntil)
	check("Comment", got.Comment, role.Comment)
	if !reflect.DeepEqual(got.InRole, role.InRole) {
		t.Errorf("InRole: got %v, want %v", got.InRole, role.InRole)
	}
	if !reflect.DeepEqual(got.RoleMembers, role.RoleMembers) {
		t.Errorf("RoleMembers: got %v, want %v", got.RoleMembers, role.RoleMembers)
	}
	if !reflect.DeepEqual(got.AdminRoles, role.AdminRoles) {
		t.Errorf("AdminRoles: got %v, want %v", got.AdminRoles, role.AdminRoles)
	}
	if got.Password != nil {
		t.Errorf("Password: got %v, want nil", got.Password)
	}
}

// TestRenderBareRoleCompile guards the minimal case: a role with no
// attributes set at all (every pointer/slice nil) must still render valid,
// recompilable source — the pre-existing "ROLE name;" behavior, unchanged.
func TestRenderBareRoleCompile(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	var b strings.Builder
	renderObjectDPG(&b, &ir.Role{Name: "plain_role"}, fmtOpts)
	rendered := b.String()

	dir := t.TempDir()
	f := filepath.Join(dir, "roles.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped bare Role failed to recompile: %v\n---\n%s", err, rendered)
	}
	got, ok := compiled[0].(*ir.Role)
	if !ok || got.Name != "plain_role" {
		t.Fatalf("expected *ir.Role named plain_role, got %+v", compiled[0])
	}
	if got.CanLogin != nil || got.Superuser != nil || got.ConnectionLimit != nil {
		t.Errorf("expected all attributes nil for a bare role, got: %+v", got)
	}
}

func TestIsClusterScoped(t *testing.T) {
	cluster := []pipeline.IRObject{&ir.Role{Name: "r"}, &ir.Tablespace{Name: "ts"}}
	database := []pipeline.IRObject{
		&ir.ForeignDataWrapper{Name: "f"}, &ir.ForeignServer{Name: "s"},
		&ir.UserMapping{Server: "s"}, &ir.Publication{Name: "p"},
		&ir.Subscription{Name: "sub"},
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
			ReturnType:  ir.TypeRef{Name: "integer"},
			Attrs:       ir.FuncAttrs{Language: "plpgsql", Volatility: "IMMUTABLE", Strict: true, Body: fnBody},
			Comment:     &fnComment,
			Grants:      []ir.Grant{{Privileges: []string{"EXECUTE"}, Roles: []string{"app_user"}}},
			Revocations: []ir.Revocation{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
		&ir.Procedure{
			Schema: "public", Name: "recalc",
			Attrs:       ir.FuncAttrs{Language: "plpgsql", Body: procBody},
			Revocations: []ir.Revocation{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
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
	if !strings.Contains(rendered, "REVOCATION EXECUTE FROM PUBLIC;") {
		t.Errorf("expected function REVOCATION (RFC §11.3 REVOCATIONS, Mode B singular keyword), got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "PROCEDURE recalc(") {
		t.Errorf("expected a PROCEDURE declaration (previously dropped entirely), got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "NULL;") {
		t.Errorf("expected the real procedure body rendered, got:\n%s", rendered)
	}
	if strings.Count(rendered, "REVOCATION EXECUTE FROM PUBLIC;") != 2 {
		t.Errorf("expected both function and procedure REVOCATION, got:\n%s", rendered)
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
					len(v.Grants) == 1 && v.Attrs.Volatility == "IMMUTABLE" && v.Attrs.Strict &&
					len(v.Revocations) == 1 && v.Revocations[0].Roles[0] == "PUBLIC"
			}
		case *ir.Procedure:
			if v.Name == "recalc" {
				sawProc = v.BodyHash == ir.HashBody(procBody) &&
					len(v.Revocations) == 1 && v.Revocations[0].Roles[0] == "PUBLIC"
			}
		}
	}
	if !sawFunc {
		t.Error("recompile missing function body/comment/grant/revocation/attrs fidelity")
	}
	if !sawProc {
		t.Error("recompile missing procedure body/revocation fidelity")
	}
}

// TestRenderAggregateCompiles guards a real bug found live-testing a demo
// project: renderObjectDPG had NO case at all for *ir.Aggregate — an
// AGGREGATE object silently produced no output whatsoever (not even a wrong
// stub, unlike the earlier VIEW bug). Uses o.Options (structured
// SFUNC/STYPE/INITCOND list) rather than o.Body, since Body is the full
// "CREATE AGGREGATE ..." SQL text — a different, invalid shape for DPG
// source, which never starts a declaration with CREATE.
// TestRenderDefaultPrivilegesCompiles guards a real bug found live-testing a
// demo project: renderObjectDPG had no case at all for *ir.DefaultPrivileges
// (silent, total drop — same class as the earlier Aggregate bug), and
// introspection never populated the object in the first place. o.ObjectType
// is single-valued per object (Builder.BuildDefaultPrivileges splits a
// multi-type declaration into one object per type; introspectDefaultPrivileges
// does the same from pg_default_acl), so this renders one full DEFAULT
// PRIVILEGES declaration per object, each with its own ON <type> clause on
// every grant/revoke entry — matching real PostgreSQL's actual ALTER
// DEFAULT PRIVILEGES grammar (confirmed live via \h ALTER DEFAULT
// PRIVILEGES), not a DPG-invented whole-declaration wrapper.
func TestRenderDefaultPrivilegesCompiles(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	forRole := "app_admin"
	inSchema := "public"
	dp := &ir.DefaultPrivileges{
		ForRole:    &forRole,
		InSchema:   &inSchema,
		ObjectType: "TABLES",
		Grants:     []ir.Grant{{Privileges: []string{"SELECT"}, Roles: []string{"app_readonly"}}},
		Revocations: []ir.Revocation{
			{Privileges: []string{"INSERT"}, Roles: []string{"app_writer"}, Cascade: true},
		},
	}

	var b strings.Builder
	renderObjectDPG(&b, dp, fmtOpts)
	rendered := b.String()

	if !strings.Contains(rendered, "DEFAULT PRIVILEGES FOR ROLE app_admin IN SCHEMA public {") {
		t.Errorf("expected DEFAULT PRIVILEGES header, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "SELECT ON TABLES TO app_readonly;") {
		t.Errorf("expected GRANT entry with ON TABLES, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "INSERT ON TABLES FROM app_writer CASCADE;") {
		t.Errorf("expected REVOCATION entry with ON TABLES and CASCADE, got:\n%s", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped default privileges failed to recompile: %v\n---\n%s", err, rendered)
	}
	var found *ir.DefaultPrivileges
	for _, o := range compiled {
		if d, ok := o.(*ir.DefaultPrivileges); ok {
			found = d
		}
	}
	if found == nil {
		t.Fatalf("default privileges object missing after recompile\n---\n%s", rendered)
	}
	if found.ForRole == nil || *found.ForRole != "app_admin" {
		t.Errorf("ForRole did not round-trip: %v", found.ForRole)
	}
	if found.InSchema == nil || *found.InSchema != "public" {
		t.Errorf("InSchema did not round-trip: %v", found.InSchema)
	}
	if len(found.Grants) != 1 || found.Grants[0].Privileges[0] != "SELECT" {
		t.Errorf("Grants did not round-trip: %+v", found.Grants)
	}
	if len(found.Revocations) != 1 || !found.Revocations[0].Cascade {
		t.Errorf("Revocations (incl. CASCADE) did not round-trip: %+v", found.Revocations)
	}
}

func TestRenderAggregateCompiles(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	comment := "multiplicative aggregate"
	agg := &ir.Aggregate{
		Schema: "public", Name: "amount_product",
		Args: []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
		Options: []pipeline.StorageParam{
			{Key: "sfunc", Value: "numeric_mul"},
			{Key: "stype", Value: "numeric"},
			{Key: "initcond", Value: "'1'"},
		},
		Comment: &comment,
		Grants:  []ir.Grant{{Privileges: []string{"EXECUTE"}, Roles: []string{"app_service"}}},
	}

	var b strings.Builder
	renderObjectDPG(&b, agg, fmtOpts)
	rendered := b.String()

	if strings.Contains(rendered, "CREATE AGGREGATE") {
		t.Errorf("DPG source must not begin a declaration with CREATE, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "AGGREGATE amount_product(numeric) (SFUNC = numeric_mul, STYPE = numeric, INITCOND = '1')") {
		t.Errorf("expected the AGGREGATE declaration with its options, got:\n%s", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped aggregate failed to recompile: %v\n---\n%s", err, rendered)
	}
	var found *ir.Aggregate
	for _, o := range compiled {
		if a, ok := o.(*ir.Aggregate); ok && a.Name == "amount_product" {
			found = a
		}
	}
	if found == nil {
		t.Fatalf("aggregate amount_product missing after recompile\n---\n%s", rendered)
	}
	if len(found.Args) != 1 || found.Args[0].Type.Name != "numeric" {
		t.Errorf("Args did not round-trip: %+v", found.Args)
	}
	if found.Comment == nil || *found.Comment != comment {
		t.Errorf("Comment did not round-trip: %v", found.Comment)
	}
	if len(found.Grants) != 1 {
		t.Errorf("Grants did not round-trip: %v", found.Grants)
	}
	if !strings.Contains(found.Body, "sfunc = numeric_mul") || !strings.Contains(found.Body, "stype = numeric") {
		t.Errorf("Body should carry the recompiled options, got %q", found.Body)
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

// TestRenderFunctionReturnsTableRoundtrip guards the render -> recompile
// chain for RETURNS TABLE(...) specifically: TABLE-mode Args were previously
// rendered inline in the main parameter list as an invalid "TABLE a integer"
// literal (PostgreSQL rejects this as a syntax error) instead of inside a
// separate RETURNS TABLE(...) clause.
func TestRenderFunctionReturnsTableRoundtrip(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	objs := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f_table",
			ReturnType: ir.TypeRef{Name: "record", SetOf: true},
			Args: []ir.FuncArg{
				{Name: "n", Mode: "IN", Type: ir.TypeRef{Name: "integer"}},
				{Name: "a", Mode: "TABLE", Type: ir.TypeRef{Name: "integer"}},
				{Name: "b", Mode: "TABLE", Type: ir.TypeRef{Name: "text"}},
			},
			Attrs: ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT 1, 'x'"},
		},
	}
	var b strings.Builder
	for _, o := range objs {
		renderObjectDPG(&b, o, fmtOpts)
	}
	rendered := b.String()

	if !strings.Contains(rendered, "(n integer) RETURNS TABLE(a integer, b text)") {
		t.Errorf("expected (n integer) RETURNS TABLE(a integer, b text), got:\n%s", rendered)
	}
	if strings.Contains(rendered, "TABLE a integer") || strings.Contains(rendered, "TABLE b text") {
		t.Errorf("TABLE-mode columns must not render inline in the parameter list, got:\n%s", rendered)
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
	var found *ir.Function
	for _, o := range compiled {
		if fn, ok := o.(*ir.Function); ok && fn.Name == "f_table" {
			found = fn
		}
	}
	if found == nil {
		t.Fatal("f_table not found after recompile")
	}
	cols := ir.FuncTableColumns(found.Args)
	if got := ir.FormatTableColumns(cols); got != "a integer, b text" {
		t.Errorf("f_table TABLE column round-trip: got %q, want %q", got, "a integer, b text")
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

// TestRenderPolicyAndTriggerRoundtrip guards a real bug found live-testing a
// demo project: renderObjectDPG's Table case had no rendering path at all
// for POLICIES/TRIGGERS — despite both being genuinely diffed
// (diffPolicies/diffTriggers) and correctly introspected
// (introspectPolicies/introspectTriggers), a live table's RLS policy and
// trigger silently vanished from a dumped project entirely (confirmed
// against a real demo table: orders.owner_only / orders.set_updated_at).
func TestRenderPolicyAndTriggerRoundtrip(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	using := "user_id = current_setting('app.current_user_id', true)::bigint"
	withCheck := "user_id = current_setting('app.current_user_id', true)::bigint"
	whenCond := "OLD.status IS DISTINCT FROM NEW.status"
	tbl := &ir.Table{
		Schema: "public", Name: "orders",
		Columns: []*ir.Column{
			{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
			{Name: "user_id", Type: ir.TypeRef{Name: "bigint"}},
			{Name: "status", Type: ir.TypeRef{Name: "text"}},
		},
		Policies: []*ir.Policy{
			{Name: "owner_only", Command: "ALL", Permissive: true, Using: &using},
			{Name: "admin_write", Command: "INSERT", Permissive: false, Roles: []string{"admin_role"}, WithCheck: &withCheck},
		},
		Triggers: []*ir.Trigger{
			{Name: "set_updated_at", When: "BEFORE", Events: []string{"UPDATE"}, ForEach: "ROW", Function: "touch_updated_at"},
			{Name: "audit_status", When: "AFTER", Events: []string{"UPDATE"}, ForEach: "ROW", Condition: &whenCond, Function: "audit_status_change", Args: []string{"'orders'"}},
		},
	}

	var b strings.Builder
	renderObjectDPG(&b, tbl, fmtOpts)
	rendered := b.String()

	if !strings.Contains(rendered, "POLICIES {") || !strings.Contains(rendered, "TRIGGERS {") {
		t.Fatalf("expected POLICIES and TRIGGERS blocks, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "owner_only FOR ALL USING") {
		t.Errorf("expected permissive ALL policy without AS clause, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "admin_write AS RESTRICTIVE FOR INSERT TO admin_role WITH CHECK") {
		t.Errorf("expected restrictive policy with TO/WITH CHECK, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "set_updated_at BEFORE UPDATE FOR EACH ROW EXECUTE FUNCTION touch_updated_at();") {
		t.Errorf("expected simple trigger, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "audit_status AFTER UPDATE FOR EACH ROW WHEN") || !strings.Contains(rendered, "EXECUTE FUNCTION audit_status_change('orders');") {
		t.Errorf("expected WHEN-conditioned trigger with args, got:\n%s", rendered)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped policies/triggers failed to recompile: %v\n---\n%s", err, rendered)
	}
	var found *ir.Table
	for _, o := range compiled {
		if tb, ok := o.(*ir.Table); ok && tb.Name == "orders" {
			found = tb
		}
	}
	if found == nil {
		t.Fatalf("table orders missing after recompile\n---\n%s", rendered)
	}
	if len(found.Policies) != 2 {
		t.Fatalf("Policies did not round-trip: got %d, want 2", len(found.Policies))
	}
	if len(found.Triggers) != 2 {
		t.Fatalf("Triggers did not round-trip: got %d, want 2", len(found.Triggers))
	}
	for _, p := range found.Policies {
		if p.Name == "admin_write" {
			if p.Permissive {
				t.Error("admin_write: Permissive did not round-trip (should be RESTRICTIVE)")
			}
			if len(p.Roles) != 1 || p.Roles[0] != "admin_role" {
				t.Errorf("admin_write: Roles did not round-trip: %v", p.Roles)
			}
			if p.WithCheck == nil {
				t.Error("admin_write: WithCheck did not round-trip")
			}
		}
	}
	for _, trg := range found.Triggers {
		if trg.Name == "audit_status" {
			if trg.Condition == nil {
				t.Error("audit_status: Condition (WHEN) did not round-trip")
			}
			if len(trg.Args) != 1 || trg.Args[0] != "'orders'" {
				t.Errorf("audit_status: Args did not round-trip: %v", trg.Args)
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
