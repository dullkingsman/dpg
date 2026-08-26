//go:build integration

package introspect_test

import (
	"context"
	"strings"
	"testing"

	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/introspect"
	"github.com/thec1oud/dpg/internal/ir"
	"github.com/thec1oud/dpg/internal/pipeline"
	"github.com/thec1oud/dpg/internal/testpg"
)

// setupOpaque connects to a fresh container, executes the given DDL statements
// in order, and returns the introspected objects.
func setupOpaque(t *testing.T, ddl ...string) []pipeline.IRObject {
	t.Helper()
	connStr := testpg.Start(t)
	ctx := context.Background()
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	for _, stmt := range ddl {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	objects, err := introspect.New().Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	return objects
}

// TestIntrospectOperatorClassAlwaysHasExplicitFamily guards the operator-family
// general fix: a class created WITHOUT an explicit FAMILY clause (the common
// case — PostgreSQL auto-creates a same-named family) must now be introspected
// with the family as its own standalone *ir.OperatorFamily object AND the
// class's reconstructed body must carry an explicit FAMILY clause naming it —
// mirroring pg_dump's own model exactly (confirmed live via `pg_dump -s`
// before writing this fix: it always dumps a separate CREATE OPERATOR FAMILY,
// even for an auto-created one, and always gives the class an explicit
// FAMILY). This is the inverse of the old, removed behavior (which skipped
// same-name families and omitted the class's FAMILY clause) — see CHANGELOG
// for why that old heuristic was unreliable by construction, not just wrong
// in a rare case.
func TestIntrospectOperatorClassAlwaysHasExplicitFamily(t *testing.T) {
	objects := setupOpaque(t, `CREATE OPERATOR CLASS rt_auto_opc FOR TYPE integer USING btree AS
        OPERATOR 3 =, FUNCTION 1 btint4cmp(integer, integer)`)

	var fam *ir.OperatorFamily
	var cls *ir.OperatorClass
	for _, o := range objects {
		if f, ok := o.(*ir.OperatorFamily); ok && f.Name == "rt_auto_opc" {
			fam = f
		}
		if c, ok := o.(*ir.OperatorClass); ok && c.Name == "rt_auto_opc" {
			cls = c
		}
	}
	if fam == nil {
		t.Fatal("auto-created family rt_auto_opc was NOT introspected as a standalone object (expected it to be, now)")
	}
	if fam.AccessMethod != "btree" {
		t.Errorf("family AccessMethod: got %q, want btree", fam.AccessMethod)
	}
	if cls == nil {
		t.Fatal("operator class rt_auto_opc was not introspected")
	}
	if !strings.Contains(strings.ToUpper(cls.Body), "FAMILY") {
		t.Errorf("class body does not name its family explicitly: %q", cls.Body)
	}
	if cls.FamilySchema != "public" || cls.FamilyName != "rt_auto_opc" {
		t.Errorf("class Family fields: got schema=%q name=%q, want schema=public name=rt_auto_opc", cls.FamilySchema, cls.FamilyName)
	}
}

// TestIntrospectOperatorFamilyExplicitSharingClassName is the direct
// regression guard for the ORIGINAL misclassification bug this fix closes: an
// EXPLICIT, separately-created family that happens to share its attached
// class's name (confirmed live that PostgreSQL's opclass→opfamily pg_depend
// row is deptype 'a' — DEPENDENCY_AUTO — for this case too, identical to a
// genuinely auto-created family, so no catalog signal alone could ever
// distinguish them). Before this fix, the old same-name heuristic silently
// dropped this family from the dump. Now, since every family is introspected
// unconditionally, this case needs no special handling at all — it "just
// works" the same as any other family.
func TestIntrospectOperatorFamilyExplicitSharingClassName(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE OPERATOR FAMILY same_name_ops USING btree`,
		`CREATE OPERATOR CLASS same_name_ops FOR TYPE integer USING btree FAMILY same_name_ops AS
            OPERATOR 3 =, FUNCTION 1 btint4cmp(integer, integer)`,
	)

	var fam *ir.OperatorFamily
	var cls *ir.OperatorClass
	for _, o := range objects {
		if f, ok := o.(*ir.OperatorFamily); ok && f.Name == "same_name_ops" {
			fam = f
		}
		if c, ok := o.(*ir.OperatorClass); ok && c.Name == "same_name_ops" {
			cls = c
		}
	}
	if fam == nil {
		t.Fatal("explicit family same_name_ops (sharing its class's name) was not introspected — the exact bug this fix closes")
	}
	if cls == nil {
		t.Fatal("operator class same_name_ops was not introspected")
	}
	if cls.FamilySchema != "public" || cls.FamilyName != "same_name_ops" {
		t.Errorf("class Family fields: got schema=%q name=%q, want schema=public name=same_name_ops", cls.FamilySchema, cls.FamilyName)
	}
}

// TestIntrospectTSConfigParserFields guards ParserSchema/ParserName wiring —
// needed for the new config→parser dependency edge in graph.go.
func TestIntrospectTSConfigParserFields(t *testing.T) {
	objects := setupOpaque(t, `CREATE TEXT SEARCH CONFIGURATION public.rt_cfg (PARSER = pg_catalog."default")`)
	var found *ir.TSConfig
	for _, o := range objects {
		if c, ok := o.(*ir.TSConfig); ok && c.Name == "rt_cfg" {
			found = c
		}
	}
	if found == nil {
		t.Fatal("TS config public.rt_cfg not found")
	}
	if found.ParserSchema != "pg_catalog" || found.ParserName != "default" {
		t.Errorf("Parser fields: got schema=%q name=%q, want schema=pg_catalog name=default", found.ParserSchema, found.ParserName)
	}
}

// TestIntrospectTSConfigMappings is the live-catalog guard for RFC Section 12.1's
// MAPPING FOR block: before this, pg_ts_config_map was never queried at
// all, so TSConfig.Mappings was always empty from introspection — a live
// config's mappings (including PG's own real multi-dictionary fallback-chain
// feature) were completely invisible to dump/diff. Also proves token types
// sharing an identical dictionary chain get grouped into one MAPPING FOR
// entry (matching how a human would naturally write it, and how the live
// config's own \dF+ output groups them) rather than one row per token type.
func TestIntrospectTSConfigMappings(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE TEXT SEARCH CONFIGURATION public.rt_map_cfg (PARSER = pg_catalog."default")`,
		`ALTER TEXT SEARCH CONFIGURATION public.rt_map_cfg ALTER MAPPING FOR word, hword WITH simple, english_stem`,
		`ALTER TEXT SEARCH CONFIGURATION public.rt_map_cfg ALTER MAPPING FOR asciiword WITH english_stem`,
	)
	var found *ir.TSConfig
	for _, o := range objects {
		if c, ok := o.(*ir.TSConfig); ok && c.Name == "rt_map_cfg" {
			found = c
		}
	}
	if found == nil {
		t.Fatal("TS config public.rt_map_cfg not found")
	}
	if len(found.Mappings) != 2 {
		t.Fatalf("Mappings: got %d, want 2 (one per distinct dictionary chain): %+v", len(found.Mappings), found.Mappings)
	}

	var sawChain, sawSingle bool
	for _, m := range found.Mappings {
		switch len(m.Dictionaries) {
		case 2:
			if m.Dictionaries[0].Name != "simple" || m.Dictionaries[1].Name != "english_stem" {
				t.Errorf("2-dict mapping: got %+v, want [simple english_stem]", m.Dictionaries)
			}
			gotTokens := map[string]bool{}
			for _, tt := range m.TokenTypes {
				gotTokens[tt] = true
			}
			if !gotTokens["word"] || !gotTokens["hword"] {
				t.Errorf("2-dict mapping token types: got %v, want word+hword grouped together", m.TokenTypes)
			}
			sawChain = true
		case 1:
			if m.Dictionaries[0].Name != "english_stem" {
				t.Errorf("1-dict mapping: got %+v, want [english_stem]", m.Dictionaries)
			}
			if len(m.TokenTypes) != 1 || m.TokenTypes[0] != "asciiword" {
				t.Errorf("1-dict mapping token types: got %v, want [asciiword] alone (different chain than word/hword)", m.TokenTypes)
			}
			sawSingle = true
		}
	}
	if !sawChain {
		t.Errorf("fallback-chain mapping (word, hword WITH simple, english_stem) not found: %+v", found.Mappings)
	}
	if !sawSingle {
		t.Errorf("single-dictionary mapping (asciiword WITH english_stem) not found: %+v", found.Mappings)
	}
}

// TestIntrospectTSDictTemplateFields guards TemplateSchema/TemplateName
// wiring — needed for the new dict→template dependency edge in graph.go.
func TestIntrospectTSDictTemplateFields(t *testing.T) {
	objects := setupOpaque(t, `CREATE TEXT SEARCH DICTIONARY public.rt_dict (TEMPLATE = pg_catalog.simple)`)
	var found *ir.TSDict
	for _, o := range objects {
		if d, ok := o.(*ir.TSDict); ok && d.Name == "rt_dict" {
			found = d
		}
	}
	if found == nil {
		t.Fatal("TS dictionary public.rt_dict not found")
	}
	if found.TemplateSchema != "pg_catalog" || found.TemplateName != "simple" {
		t.Errorf("Template fields: got schema=%q name=%q, want schema=pg_catalog name=simple", found.TemplateSchema, found.TemplateName)
	}
}

func TestIntrospectCollation(t *testing.T) {
	objects := setupOpaque(t, `CREATE COLLATION public.mycoll (LOCALE = 'C')`)
	var found *ir.Collation
	for _, obj := range objects {
		if c, ok := obj.(*ir.Collation); ok && c.Name == "mycoll" && c.Schema == "public" {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("collation public.mycoll not found")
	}
	if !strings.Contains(found.Body, "CREATE COLLATION") || !strings.Contains(found.Body, "mycoll") {
		t.Errorf("unexpected collation body: %q", found.Body)
	}
}

// TestIntrospectRangeType guards a real bug found live-testing a demo
// project: RANGE types had NO introspection at all before this — not even
// a partial reconstruction, so dump could only emit
// "-- type X (RANGE) omitted" for every range type, despite the RFC listing
// Range types as "Declared, Diffed" with no such caveat. Uses a
// non-collatable subtype (numeric) so this also guards the common case:
// SUBTYPE_OPCLASS/COLLATION both correctly suppressed when they match the
// subtype's own defaults, rather than always rendering them.
func TestIntrospectRangeType(t *testing.T) {
	objects := setupOpaque(t, `CREATE TYPE public.myrange AS RANGE (SUBTYPE = numeric)`)
	var found *ir.Type
	for _, obj := range objects {
		if tp, ok := obj.(*ir.Type); ok && tp.Name == "myrange" && tp.Variant == "RANGE" {
			found = tp
			break
		}
	}
	if found == nil {
		t.Fatal("range type public.myrange not found")
	}
	if found.Body != "SUBTYPE = numeric" {
		t.Errorf("Body: got %q, want %q (SUBTYPE_OPCLASS/COLLATION must be suppressed for numeric's defaults)", found.Body, "SUBTYPE = numeric")
	}
}

// TestIntrospectRangeTypeNonDefaultOpclass guards the opposite case: when a
// range type's SUBTYPE_OPCLASS genuinely differs from the subtype's default
// (text has multiple real opclasses, unlike numeric), it must actually be
// rendered, not suppressed by an overly-broad "always omit" heuristic. Also
// confirms COLLATION renders for a collatable subtype (text), unlike
// numeric's non-collatable case above.
func TestIntrospectRangeTypeNonDefaultOpclass(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE TYPE public.textpatrange AS RANGE (SUBTYPE = text, SUBTYPE_OPCLASS = text_pattern_ops)`)
	var found *ir.Type
	for _, obj := range objects {
		if tp, ok := obj.(*ir.Type); ok && tp.Name == "textpatrange" && tp.Variant == "RANGE" {
			found = tp
			break
		}
	}
	if found == nil {
		t.Fatal("range type public.textpatrange not found")
	}
	if !strings.Contains(found.Body, "SUBTYPE_OPCLASS = text_pattern_ops") {
		t.Errorf("expected non-default SUBTYPE_OPCLASS to be rendered, got: %q", found.Body)
	}
	if !strings.Contains(found.Body, "COLLATION = ") {
		t.Errorf("expected COLLATION to be rendered for a collatable subtype, got: %q", found.Body)
	}
}

// TestIntrospectDomainStructuredFields guards RFC Section 5.4's structured domain
// diffing inputs: introspectDomainBodies/introspectDomainConstraints
// previously concatenated base type, NOT NULL, DEFAULT, and every CHECK
// constraint into one opaque Body string (the same "just hash it" treatment
// RANGE/BASE get) — even though DiffType now diffs each property
// individually, so it needs them as separate fields, not one blob.
func TestIntrospectDomainStructuredFields(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE DOMAIN public.positive_integer AS integer DEFAULT 1 NOT NULL CONSTRAINT positive_only CHECK (VALUE > 0)`)
	var found *ir.Type
	for _, obj := range objects {
		if tp, ok := obj.(*ir.Type); ok && tp.Name == "positive_integer" && tp.Variant == "DOMAIN" {
			found = tp
			break
		}
	}
	if found == nil {
		t.Fatal("domain public.positive_integer not found")
	}
	if found.DomainBaseType.Name != "integer" {
		t.Errorf("DomainBaseType: got %q, want integer", found.DomainBaseType.Name)
	}
	if !found.DomainNotNull {
		t.Error("DomainNotNull: got false, want true")
	}
	if found.DomainDefault == nil || *found.DomainDefault != "1" {
		t.Errorf("DomainDefault: got %v, want \"1\"", found.DomainDefault)
	}
	if len(found.DomainConstraints) != 1 || found.DomainConstraints[0].Name != "positive_only" {
		t.Fatalf("DomainConstraints: got %+v", found.DomainConstraints)
	}
	if !strings.Contains(found.DomainConstraints[0].Expr, "VALUE > 0") {
		t.Errorf("DomainConstraints[0].Expr: got %q", found.DomainConstraints[0].Expr)
	}
}

func TestIntrospectCast(t *testing.T) {
	// A user-defined cast between a custom enum and integer (no built-in exists).
	objects := setupOpaque(t,
		`CREATE TYPE public.rgb AS ENUM ('r', 'g', 'b')`,
		`CREATE FUNCTION public.rgb_to_int(public.rgb) RETURNS integer AS 'SELECT 1' LANGUAGE sql IMMUTABLE`,
		`CREATE CAST (public.rgb AS integer) WITH FUNCTION public.rgb_to_int(public.rgb) AS ASSIGNMENT`)
	var found *ir.Cast
	for _, obj := range objects {
		if c, ok := obj.(*ir.Cast); ok && c.TargetType.Name == "integer" && strings.Contains(c.SourceType.Name, "rgb") {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("cast (rgb AS integer) not found")
	}
	if !strings.Contains(found.Body, "CREATE CAST") || !strings.Contains(found.Body, "rgb_to_int") {
		t.Errorf("unexpected cast body: %q", found.Body)
	}
}

// TestIntrospectCastExcludesInternallyDependent guards a real bug found
// live-testing a demo project: PostgreSQL auto-creates a CAST from a RANGE
// type to its own auto-generated multirange companion type the moment the
// range type is declared — a real pg_cast row, but one with
// pg_depend.deptype = 'i' ("internal"), meaning it's not an independently
// manageable object: it can't be explicitly re-created from scratch (CREATE
// CAST on that exact pairing fails "already exists" even against a database
// that never declared one) and PostgreSQL refuses to drop it directly.
// introspectCasts had no filter for this, so `dpg dump` reconstructed it as
// an ordinary declared CAST — and a subsequent `dpg plan` proposed `DROP
// CAST` on it, a migration step confirmed live to fail outright.
func TestIntrospectCastExcludesInternallyDependent(t *testing.T) {
	objects := setupOpaque(t, `CREATE TYPE public.zz_range AS RANGE (subtype = int4)`)
	for _, obj := range objects {
		if c, ok := obj.(*ir.Cast); ok && strings.Contains(c.SourceType.Name, "zz_range") {
			t.Errorf("expected the auto-created range->multirange cast to be excluded, got: %+v", c)
		}
	}
}

func TestIntrospectStatistics(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE TABLE public.st (a int, b int)`,
		`CREATE STATISTICS public.st_stats (dependencies) ON a, b FROM public.st`)
	var found *ir.StatisticsObject
	for _, obj := range objects {
		if s, ok := obj.(*ir.StatisticsObject); ok && s.Name == "st_stats" {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatal("statistics public.st_stats not found")
	}
	if !strings.Contains(found.Body, "CREATE STATISTICS") {
		t.Errorf("unexpected statistics body: %q", found.Body)
	}
}

func TestIntrospectEventTrigger(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE FUNCTION public.log_ddl() RETURNS event_trigger AS 'BEGIN END' LANGUAGE plpgsql`,
		`CREATE EVENT TRIGGER my_et ON ddl_command_start EXECUTE FUNCTION public.log_ddl()`)
	var found *ir.EventTrigger
	for _, obj := range objects {
		if e, ok := obj.(*ir.EventTrigger); ok && e.Name == "my_et" {
			found = e
			break
		}
	}
	if found == nil {
		t.Fatal("event trigger my_et not found")
	}
	if !strings.Contains(found.Body, "CREATE EVENT TRIGGER") || !strings.Contains(found.Body, "ddl_command_start") {
		t.Errorf("unexpected event trigger body: %q", found.Body)
	}
}

// TestIntrospectOpaqueComments guards the introspection-side half of the
// Comment fix: the earlier fix (see differ.go's commentOnOpaqueSQL) only
// wired declare-in-source -> apply-to-live-DB for these 14 opaque kinds, but
// never wired live-catalog -> reconstructed-IR for 11 of them, discovered
// live testing a demo project (`dpg dump` silently dropped every comment
// that was genuinely present in pg_description). Every kind below sets a
// real COMMENT ON ... and asserts the introspected object's Comment field
// round-trips it exactly, via the matching obj_description(oid, catalog)
// call now added to each query. Tablespace/ForeignDataWrapper/ForeignServer/
// Subscription are not covered here since they already had correct
// obj_description/shobj_description wiring before this fix.
func TestIntrospectOpaqueComments(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE FUNCTION public.rt_cm_log_ddl() RETURNS event_trigger AS 'BEGIN END' LANGUAGE plpgsql`,
		`CREATE EVENT TRIGGER rt_cm_et ON ddl_command_start EXECUTE FUNCTION public.rt_cm_log_ddl()`,
		`COMMENT ON EVENT TRIGGER rt_cm_et IS 'et comment'`,

		`CREATE COLLATION public.rt_cm_coll (LOCALE = 'C')`,
		`COMMENT ON COLLATION public.rt_cm_coll IS 'coll comment'`,

		`CREATE TYPE public.rt_cm_enum AS ENUM ('a', 'b')`,
		`CREATE FUNCTION public.rt_cm_enum_to_int(public.rt_cm_enum) RETURNS integer AS 'SELECT 1' LANGUAGE sql IMMUTABLE`,
		`CREATE CAST (public.rt_cm_enum AS integer) WITH FUNCTION public.rt_cm_enum_to_int(public.rt_cm_enum) AS ASSIGNMENT`,
		`COMMENT ON CAST (public.rt_cm_enum AS integer) IS 'cast comment'`,

		`CREATE TABLE public.rt_cm_st (a int, b int)`,
		`CREATE STATISTICS public.rt_cm_stats (dependencies) ON a, b FROM public.rt_cm_st`,
		`COMMENT ON STATISTICS public.rt_cm_stats IS 'stats comment'`,

		`CREATE FUNCTION public.rt_cm_op_fn(integer, integer) RETURNS boolean AS 'SELECT $1 > $2' LANGUAGE sql IMMUTABLE`,
		`CREATE OPERATOR public.=#> (LEFTARG = integer, RIGHTARG = integer, FUNCTION = rt_cm_op_fn)`,
		`COMMENT ON OPERATOR public.=#> (integer, integer) IS 'operator comment'`,

		`CREATE OPERATOR FAMILY public.rt_cm_fam USING btree`,
		`CREATE OPERATOR CLASS public.rt_cm_class FOR TYPE integer USING btree FAMILY public.rt_cm_fam AS
            OPERATOR 3 =, FUNCTION 1 btint4cmp(integer, integer)`,
		`COMMENT ON OPERATOR FAMILY public.rt_cm_fam USING btree IS 'family comment'`,
		`COMMENT ON OPERATOR CLASS public.rt_cm_class USING btree IS 'class comment'`,

		`CREATE TEXT SEARCH PARSER public.rt_cm_parser (START = prsd_start, GETTOKEN = prsd_nexttoken, END = prsd_end, LEXTYPES = prsd_lextype)`,
		`COMMENT ON TEXT SEARCH PARSER public.rt_cm_parser IS 'parser comment'`,

		`CREATE TEXT SEARCH TEMPLATE public.rt_cm_tmpl (LEXIZE = dsimple_lexize)`,
		`COMMENT ON TEXT SEARCH TEMPLATE public.rt_cm_tmpl IS 'template comment'`,

		`CREATE TEXT SEARCH DICTIONARY public.rt_cm_dict (TEMPLATE = pg_catalog.simple)`,
		`COMMENT ON TEXT SEARCH DICTIONARY public.rt_cm_dict IS 'dict comment'`,

		`CREATE TEXT SEARCH CONFIGURATION public.rt_cm_cfg (PARSER = pg_catalog."default")`,
		`COMMENT ON TEXT SEARCH CONFIGURATION public.rt_cm_cfg IS 'config comment'`,
	)

	want := map[string]string{
		"EventTrigger":     "et comment",
		"Collation":        "coll comment",
		"Cast":             "cast comment",
		"StatisticsObject": "stats comment",
		"Operator":         "operator comment",
		"OperatorFamily":   "family comment",
		"OperatorClass":    "class comment",
		"TSParser":         "parser comment",
		"TSTemplate":       "template comment",
		"TSDict":           "dict comment",
		"TSConfig":         "config comment",
	}
	got := map[string]*string{}

	for _, o := range objects {
		switch v := o.(type) {
		case *ir.EventTrigger:
			if v.Name == "rt_cm_et" {
				got["EventTrigger"] = v.Comment
			}
		case *ir.Collation:
			if v.Name == "rt_cm_coll" {
				got["Collation"] = v.Comment
			}
		case *ir.Cast:
			if strings.Contains(v.SourceType.Name, "rt_cm_enum") {
				got["Cast"] = v.Comment
			}
		case *ir.StatisticsObject:
			if v.Name == "rt_cm_stats" {
				got["StatisticsObject"] = v.Comment
			}
		case *ir.Operator:
			if v.Name == "=#>" {
				got["Operator"] = v.Comment
			}
		case *ir.OperatorFamily:
			if v.Name == "rt_cm_fam" {
				got["OperatorFamily"] = v.Comment
			}
		case *ir.OperatorClass:
			if v.Name == "rt_cm_class" {
				got["OperatorClass"] = v.Comment
			}
		case *ir.TSParser:
			if v.Name == "rt_cm_parser" {
				got["TSParser"] = v.Comment
			}
		case *ir.TSTemplate:
			if v.Name == "rt_cm_tmpl" {
				got["TSTemplate"] = v.Comment
			}
		case *ir.TSDict:
			if v.Name == "rt_cm_dict" {
				got["TSDict"] = v.Comment
			}
		case *ir.TSConfig:
			if v.Name == "rt_cm_cfg" {
				got["TSConfig"] = v.Comment
			}
		}
	}

	for kind, wantComment := range want {
		c, found := got[kind]
		if !found {
			t.Errorf("%s: object not found in introspection results", kind)
			continue
		}
		if c == nil {
			t.Errorf("%s: Comment is nil, want %q", kind, wantComment)
			continue
		}
		if *c != wantComment {
			t.Errorf("%s: Comment = %q, want %q", kind, *c, wantComment)
		}
	}
}

func TestIntrospectForeignInfra(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE FOREIGN DATA WRAPPER dummy_fdw`,
		`CREATE SERVER dummy_srv FOREIGN DATA WRAPPER dummy_fdw OPTIONS (host 'localhost')`,
		`CREATE USER MAPPING FOR PUBLIC SERVER dummy_srv OPTIONS ("user" 'alice')`)

	var fdw *ir.ForeignDataWrapper
	var srv *ir.ForeignServer
	var um *ir.UserMapping
	for _, obj := range objects {
		switch o := obj.(type) {
		case *ir.ForeignDataWrapper:
			if o.Name == "dummy_fdw" {
				fdw = o
			}
		case *ir.ForeignServer:
			if o.Name == "dummy_srv" {
				srv = o
			}
		case *ir.UserMapping:
			if o.Server == "dummy_srv" {
				um = o
			}
		}
	}
	if fdw == nil || !strings.Contains(fdw.Body, "CREATE FOREIGN DATA WRAPPER") {
		t.Errorf("foreign data wrapper not introspected: %+v", fdw)
	}
	if srv == nil || !strings.Contains(srv.Body, "dummy_fdw") {
		t.Errorf("foreign server not introspected: %+v", srv)
	}
	if um == nil || !strings.Contains(um.Body, "CREATE USER MAPPING") {
		t.Errorf("user mapping not introspected: %+v", um)
	}
	if um != nil && um.User != "" {
		t.Errorf("FOR PUBLIC user mapping should have empty User, got %q", um.User)
	}
}

// TestIntrospectForeignTable guards a real, multi-layer bug found live-
// testing a demo project: every table-related introspection query filtered
// relkind IN ('r', 'p') — foreign tables (relkind 'f') were entirely
// invisible to dpg dump/verify/plan --live, and even after being made
// visible, SERVER/OPTIONS were never captured at all. This creates a real
// (non-connectable, but catalog-valid) FDW/server/foreign-table stack and
// confirms the introspected *ir.Table carries Foreign, ForeignServer, and
// ForeignOptions correctly.
func TestIntrospectForeignTable(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE FOREIGN DATA WRAPPER dummy_fdw`,
		`CREATE SERVER dummy_srv FOREIGN DATA WRAPPER dummy_fdw OPTIONS (host 'localhost')`,
		`CREATE FOREIGN TABLE remote_events (id BIGINT, payload JSONB)
            SERVER dummy_srv OPTIONS (table_name 'events', schema_name 'public')`,
	)

	var found *ir.Table
	for _, obj := range objects {
		if tb, ok := obj.(*ir.Table); ok && tb.Name == "remote_events" {
			found = tb
		}
	}
	if found == nil {
		t.Fatal("foreign table public.remote_events not found (relkind 'f' excluded from introspection?)")
	}
	if !found.Foreign {
		t.Error("expected Foreign = true")
	}
	if found.ForeignServer == nil || *found.ForeignServer != "dummy_srv" {
		t.Errorf("ForeignServer: got %v", found.ForeignServer)
	}
	wantOpts := map[string]string{"table_name": "events", "schema_name": "public"}
	if len(found.ForeignOptions) != len(wantOpts) {
		t.Fatalf("ForeignOptions: expected %d entries, got %v", len(wantOpts), found.ForeignOptions)
	}
	for _, p := range found.ForeignOptions {
		if wantOpts[p.Key] != p.Value {
			t.Errorf("ForeignOptions[%q]: got %q, want %q", p.Key, p.Value, wantOpts[p.Key])
		}
	}
	if len(found.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d: %v", len(found.Columns), found.Columns)
	}
}

func TestIntrospectPublication(t *testing.T) {
	objects := setupOpaque(t, `CREATE PUBLICATION my_pub FOR ALL TABLES`)
	var found *ir.Publication
	for _, obj := range objects {
		if p, ok := obj.(*ir.Publication); ok && p.Name == "my_pub" {
			found = p
			break
		}
	}
	if found == nil {
		t.Fatal("publication my_pub not found")
	}
	if !strings.Contains(found.Body, "FOR ALL TABLES") {
		t.Errorf("unexpected publication body: %q", found.Body)
	}
}

// TestIntrospectPublicationComment guards a real bug found live-testing a
// demo project: ir.Publication had no Comment field at all, despite real
// PostgreSQL genuinely supporting COMMENT ON PUBLICATION (confirmed live
// via \h COMMENT) — Publication was excluded from the original 14-kind
// Comment/Grant fix on the mistaken assumption that it didn't apply, so
// introspectPublications never called obj_description at all.
func TestIntrospectPublicationComment(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE PUBLICATION commented_pub FOR ALL TABLES`,
		`COMMENT ON PUBLICATION commented_pub IS 'replication stream for all tables'`,
	)
	var found *ir.Publication
	for _, obj := range objects {
		if p, ok := obj.(*ir.Publication); ok && p.Name == "commented_pub" {
			found = p
			break
		}
	}
	if found == nil {
		t.Fatal("publication commented_pub not found")
	}
	if found.Comment == nil || *found.Comment != "replication stream for all tables" {
		t.Errorf("Comment: got %v, want \"replication stream for all tables\"", found.Comment)
	}
}

// TestIntrospectPublicationMixedTargets guards the single-FOR grammar: a
// publication over both a table AND a schema must reconstruct to one FOR clause.
// The buggy form (FOR TABLE … FOR TABLES IN SCHEMA …) is a syntax error that
// canonicalDDL would silently pass through, so re-executing the body catches it.
func TestIntrospectPublicationMixedTargets(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	for _, stmt := range []string{
		`CREATE SCHEMA s1`,
		`CREATE TABLE public.t1 (id int)`,
		`CREATE PUBLICATION mix_pub FOR TABLE public.t1, TABLES IN SCHEMA s1`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	objects, err := introspect.New().Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var found *ir.Publication
	for _, obj := range objects {
		if p, ok := obj.(*ir.Publication); ok && p.Name == "mix_pub" {
			found = p
			break
		}
	}
	if found == nil {
		t.Fatal("publication mix_pub not found")
	}
	// The reconstructed body must be valid SQL: drop and re-create it.
	if _, err := conn.Exec(ctx, `DROP PUBLICATION mix_pub`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := conn.Exec(ctx, found.Body); err != nil {
		t.Fatalf("reconstructed publication body is invalid SQL: %v\nbody: %s", err, found.Body)
	}
}

// TestIntrospectCollationFiltersSystem guards the namespace filter: initdb
// imports hundreds of system locale collations into pg_catalog with normal
// OIDs. A database with no user collations must yield zero introspected ones.
func TestIntrospectCollationFiltersSystem(t *testing.T) {
	objects := setupOpaque(t) // no user objects created
	for _, obj := range objects {
		if c, ok := obj.(*ir.Collation); ok {
			t.Errorf("system collation leaked into introspection: %s.%s", c.Schema, c.Name)
		}
	}
}

// TestIntrospectSubscription guards items 6z/6ff's closing piece: introspection
// must reconstruct every Subscription attribute except subconninfo (never
// selected — see subscriptionConnInfoPlaceholder's doc comment), non-default
// WITH options included, while never leaking the real connection string
// (which, unlike every other reliable-tier kind's Body, holds a real
// credential in the live catalog) anywhere into the reconstructed Body.
// connect = false avoids needing a real reachable publisher: this test
// proves introspection reads the row correctly, not that replication works
// (TestSubscriptionConnectionSecretRoundtrip in integration/ already proves
// a real replication connection separately).
func TestIntrospectSubscription(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.StartLogical(t)
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	for _, stmt := range []string{
		`CREATE PUBLICATION pub_a FOR ALL TABLES`,
		`CREATE SUBSCRIPTION my_sub CONNECTION ` +
			`'host=127.0.0.1 port=5432 dbname=dpgtest user=dpg password=supersecret' ` +
			`PUBLICATION pub_a WITH (connect = false, create_slot = false, ` +
			`binary = true, streaming = parallel, disable_on_error = true, ` +
			`password_required = false, synchronous_commit = remote_apply, ` +
			`slot_name = my_custom_slot)`,
		`COMMENT ON SUBSCRIPTION my_sub IS 'covers §6ff'`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	objects, err := introspect.New().Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var found *ir.Subscription
	for _, obj := range objects {
		if s, ok := obj.(*ir.Subscription); ok && s.Name == "my_sub" {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatal("subscription my_sub not found")
	}
	if !found.Reconstructed {
		t.Error("expected Reconstructed = true")
	}
	if strings.Contains(found.Body, "supersecret") || strings.Contains(found.Body, "127.0.0.1") {
		t.Fatalf("introspected Body leaked the live connection string: %q", found.Body)
	}
	if found.ConnInfo != "" && strings.Contains(found.ConnInfo, "supersecret") {
		t.Fatalf("ConnInfo leaked the live connection string: %q", found.ConnInfo)
	}
	for _, want := range []string{
		// connect/create_slot/enabled are always forced false — the placeholder
		// CONNECTION is never dialable, see introspectSubscriptions' doc comment.
		"connect = false", "create_slot = false", "enabled = false",
		// canonicalDDL quotes "binary" (reserved word in this grammar context) —
		// not a bug, just how pg_query's own deparse renders it.
		`"binary" = true`, "streaming = parallel", "disable_on_error = true",
		"password_required = false", "synchronous_commit = 'remote_apply'",
		"slot_name = 'my_custom_slot'",
	} {
		if !strings.Contains(found.Body, want) {
			t.Errorf("expected Body to contain %q, got: %s", want, found.Body)
		}
	}
	if found.Comment == nil || *found.Comment != "covers §6ff" {
		t.Errorf("expected comment %q, got %v", "covers §6ff", found.Comment)
	}

	// The reconstructed body must be valid, re-executable SQL. Detach the
	// slot first: create_slot = false above means no physical replication
	// slot actually exists on a (nonexistent, connect = false) publisher for
	// PostgreSQL to drop, and a bare DROP SUBSCRIPTION always tries.
	if _, err := conn.Exec(ctx, `ALTER SUBSCRIPTION my_sub SET (slot_name = NONE)`); err != nil {
		t.Fatalf("detach slot before drop: %v", err)
	}
	if _, err := conn.Exec(ctx, `DROP SUBSCRIPTION my_sub`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := conn.Exec(ctx, found.Body); err != nil {
		t.Fatalf("reconstructed subscription body is invalid SQL: %v\nbody: %s", err, found.Body)
	}
}

// TestIntrospectSubscriptionCrossDatabaseIsolation guards the pg_subscription
// shared-catalog filter: pg_subscription lives in pg_global (confirmed
// live — every row from every database in the cluster is visible from any
// single database's connection, unlike every other reliable-tier catalog
// here, which is already database-local), so introspectSubscriptions MUST
// filter by subdbid explicitly. Without that filter, introspecting database
// A would leak database B's subscriptions into A's dump/plan --live.
func TestIntrospectSubscriptionCrossDatabaseIsolation(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.StartLogical(t)
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `CREATE DATABASE other_db`); err != nil {
		t.Fatalf("create other_db: %v", err)
	}
	otherConnStr := strings.Replace(connStr, "/dpgtest", "/other_db", 1)
	otherConn, err := executor.Connect(ctx, otherConnStr)
	if err != nil {
		t.Fatalf("connect other_db: %v", err)
	}
	defer otherConn.Close(ctx)
	for _, stmt := range []string{
		`CREATE PUBLICATION pub_other FOR ALL TABLES`,
		`CREATE SUBSCRIPTION other_sub CONNECTION 'host=127.0.0.1 port=5432 dbname=other_db user=dpg password=x' ` +
			`PUBLICATION pub_other WITH (connect = false, create_slot = false)`,
	} {
		if _, err := otherConn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q against other_db: %v", stmt, err)
		}
	}

	objects, err := introspect.New().Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect dpgtest: %v", err)
	}
	for _, obj := range objects {
		if s, ok := obj.(*ir.Subscription); ok {
			t.Errorf("other_db's subscription %q leaked into dpgtest's introspection", s.Name)
		}
	}
}
