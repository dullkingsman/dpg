package ir

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// typeNameParts extracts the dotted name parts from a pg_query TypeName's
// Names list (e.g. ["pg_catalog", "int4"] or ["myschema", "mytype"] or just
// ["mytype"]). Shared by typeNameToRef and serialMarkerFromTypeName so both
// walk the same AST shape exactly once.
func typeNameParts(tn *pg_query.TypeName) []string {
	if tn == nil {
		return nil
	}
	names := make([]string, 0, len(tn.Names))
	for _, n := range tn.Names {
		if sv := n.GetString_(); sv != nil {
			names = append(names, sv.Sval)
		}
	}
	return names
}

// serialUnderlyingType maps a SERIAL-family type name (case-insensitive) to
// its real underlying integer type and canonical marker spelling, or returns
// ok=false if name isn't one. PostgreSQL's grammar recognizes SERIAL/
// SERIAL2/SERIAL4/SERIAL8/SMALLSERIAL/BIGSERIAL only as a bare, unqualified
// type name — pg_catalog has no such type at the catalog level, it's a
// parser-level macro expanded at CREATE TABLE time — so this is only ever
// meaningful against a single, unqualified name part.
func serialUnderlyingType(name string) (underlying, marker string, ok bool) {
	switch strings.ToLower(name) {
	case "smallserial", "serial2":
		return "smallint", "SMALLSERIAL", true
	case "serial", "serial4":
		return "integer", "SERIAL", true
	case "bigserial", "serial8":
		return "bigint", "BIGSERIAL", true
	default:
		return "", "", false
	}
}

// serialMarkerFromTypeName detects whether tn is one of the SERIAL-family
// pseudo-types and, if so, returns the canonical marker to store on
// ir.Column.Serial. Returns nil for every other type, including a
// schema-qualified reference to a real user type that happens to be named
// "serial" (SERIAL is only ever written bare in real PostgreSQL DDL).
func serialMarkerFromTypeName(tn *pg_query.TypeName) *string {
	names := typeNameParts(tn)
	if len(names) != 1 {
		return nil
	}
	if _, marker, ok := serialUnderlyingType(names[0]); ok {
		return &marker
	}
	return nil
}

// typeNameToRef converts a pg_query TypeName node into an ir.TypeRef.
func typeNameToRef(tn *pg_query.TypeName) TypeRef {
	if tn == nil {
		return TypeRef{Name: "unknown"}
	}
	ref := TypeRef{
		ArrayDims: len(tn.ArrayBounds),
		SetOf:     tn.Setof,
	}

	names := typeNameParts(tn)

	switch len(names) {
	case 0:
		ref.Name = "unknown"
	case 1:
		// SERIAL/BIGSERIAL/SMALLSERIAL are parser-level macros, not real
		// pg_catalog types: normalize to the real underlying integer type
		// here so Column.Type always matches what introspection reads back
		// from a live catalog (format_type() never returns "serial"). The
		// Serial marker itself is derived separately in buildColumn via
		// serialMarkerFromTypeName, since TypeRef has no room for it and
		// every other typeNameToRef caller (casts, function args unrelated
		// to columns) has no use for a Serial marker at all.
		if underlying, _, ok := serialUnderlyingType(names[0]); ok {
			ref.Name = underlying
		} else {
			// pg_query emits some built-in aliases (e.g. "timestamptz") as
			// a single-part name rather than ["pg_catalog", "timestamptz"].
			// Run them through PGCatalogName so the canonical form always
			// matches what format_type() returns during introspection.
			ref.Name = PGCatalogName(names[0])
		}
	case 2:
		if names[0] == "pg_catalog" {
			// Built-in: strip the catalog prefix and use the canonical name.
			ref.Name = PGCatalogName(names[1])
		} else {
			ref.Schema = names[0]
			ref.Name = names[1]
		}
	default:
		// 3+ parts: take last two as schema.name
		ref.Schema = names[len(names)-2]
		ref.Name = names[len(names)-1]
	}

	// Type modifiers (e.g. varchar(255) → typemod 259 = 255+4)
	// We reconstruct the display form from the mod value when possible.
	if len(tn.Typmods) > 0 {
		ref.Mods = typmodString(ref.Name, tn.Typmods)
	}

	return ref
}

// ParseTypeText converts a source-written type name ("int4", "integer",
// "varchar(20)", "myschema.mytype") into canonical TypeRef form, so a
// hand-written OPERATOR FAMILY member's op_type (RFC Section 14.4) compares equal
// to introspection's `::regtype::text` form. canonicalDDL/canonicalizeSQLBody
// can't do this: pg_query only rewrites an explicit "pg_catalog.int4"
// reference, never a bare "int4" (it has no catalog access at parse time to
// know the two name the same type), so a bare cast round-trips through
// Parse/Deparse unchanged. Going through TypeName directly — the same node
// typeNameToRef already reads off a real column/cast — is the only way to
// get the canonical form offline. Falls back to the raw, trimmed input on
// any parse failure: this only affects op_type comparison, never a hard
// error, so an input ParseTypeText can't handle just shows as ordinary
// (harmless) drift rather than blocking compilation.
func ParseTypeText(s string) TypeRef {
	s = strings.TrimSpace(s)
	res, err := pg_query.Parse("SELECT NULL::" + s)
	if err != nil || len(res.Stmts) == 0 {
		return TypeRef{Name: s}
	}
	sel := res.Stmts[0].GetStmt().GetSelectStmt()
	if sel == nil || len(sel.TargetList) == 0 {
		return TypeRef{Name: s}
	}
	rt := sel.TargetList[0].GetResTarget()
	if rt == nil {
		return TypeRef{Name: s}
	}
	tc := rt.GetVal().GetTypeCast()
	if tc == nil {
		return TypeRef{Name: s}
	}
	return typeNameToRef(tc.TypeName)
}

// PGCatalogName maps pg_catalog internal type names to their SQL equivalents
// (e.g. "int4" -> "integer", "varchar" -> "character varying") — the same
// canonical form format_type() returns, for the subset of built-in aliases
// this function currently handles. Exported so other packages (e.g.
// internal/diff's implicit-cast table) can normalize a raw pg_catalog type
// name into the exact string TypeRef.String() would itself produce, rather
// than duplicating this mapping.
func PGCatalogName(internal string) string {
	switch internal {
	case "int2":
		return "smallint"
	case "int4":
		return "integer"
	case "int8":
		return "bigint"
	case "float4":
		return "real"
	case "float8":
		return "double precision"
	case "bool":
		return "boolean"
	case "bpchar":
		return "character"
	case "varchar":
		return "character varying"
	case "timetz":
		return "time with time zone"
	case "timestamptz":
		return "timestamp with time zone"
	case "time":
		return "time without time zone"
	case "timestamp":
		return "timestamp without time zone"
	case "varbit":
		return "bit varying"
	default:
		return internal
	}
}

// typmodInt extracts an int64 value from a typmod list element. Confirmed
// live (probed directly against pg_query.Parse): a TypeName's Typmods list
// element is an A_Const-wrapped Integer (e.g. "numeric(10,2)"'s "10" and
// "2"), never a bare Integer node — the same A_Const-vs-bare-Integer split
// already documented and handled for sequence DefElem options in
// seqOptionInt (builder.go). The bare-Integer branch is kept as a fallback
// only, matching seqOptionInt's own defensive shape, not because it's been
// observed for typmods specifically.
func typmodInt(n *pg_query.Node) (int64, bool) {
	if ic := n.GetInteger(); ic != nil {
		return int64(ic.Ival), true
	}
	if ac := n.GetAConst(); ac != nil {
		if ic := ac.GetIval(); ic != nil {
			return int64(ic.Ival), true
		}
	}
	return 0, false
}

// typmodString reconstructs the typemod display string from pg_query Typmods nodes.
func typmodString(typeName string, mods []*pg_query.Node) string {
	if len(mods) == 0 {
		return ""
	}
	// For most types, the first typemod is an integer constant.
	if val, ok := typmodInt(mods[0]); ok {
		switch typeName {
		case "bit", "varbit", "bit varying":
			// Confirmed live: PostgreSQL's own bare-word defaults differ
			// dramatically between these two (bit -> bit(1), bit varying ->
			// unbounded), so silently dropping an explicit length here isn't
			// just lossy formatting — it changes the actual column PostgreSQL
			// creates. A source-declared `BIT(4)` with no case here compiled
			// down to a bare `bit` in emitted DDL, which PostgreSQL silently
			// interprets as `bit(1)`: applying it created a real column 4x
			// narrower than declared, with no error or warning anywhere in
			// the pipeline. bit's typmod is a bare length exactly like
			// character/varchar (no VARHDRSZ-style live-catalog offset
			// concern here either, for the same "source-parsed TypeName, not
			// live atttypmod" reason documented on that case below).
			if val > 0 {
				return fmt.Sprintf("(%d)", val)
			}
		case "character", "character varying", "bpchar", "varchar":
			// Confirmed live (pg_query.Parse probe): unlike a live catalog's
			// atttypmod (which PostgreSQL internally offsets by VARHDRSZ, i.e.
			// length+4), the parse tree's Typmods literal for a source-declared
			// varchar(n)/char(n) is the plain, unencoded length exactly as
			// written — this function is only ever fed from typeNameToRef,
			// which is only ever called on source-parsed TypeName nodes
			// (columns, function args/return types, casts), never against a
			// live-introspected atttypmod (introspection renders its own type
			// string directly via format_type(), a separate path entirely).
			// A prior version of this case subtracted 4, silently producing a
			// wrong-by-4 length whenever it ran — masked until now because a
			// separate bug (GetInteger() on an A_Const-wrapped node, see
			// typmodInt) meant this whole function always returned "" before.
			if val > 0 {
				return fmt.Sprintf("(%d)", val)
			}
		case "numeric":
			if len(mods) >= 2 {
				if val2, ok2 := typmodInt(mods[1]); ok2 {
					return fmt.Sprintf("(%d,%d)", val, val2)
				}
			}
			return fmt.Sprintf("(%d)", val)
		case "time", "timetz", "time with time zone", "time without time zone",
			"timestamp", "timestamptz", "timestamp with time zone", "timestamp without time zone",
			"interval":
			// timetz/timestamptz/time/timestamp never actually reach this
			// switch under their short internal name: typeNameToRef runs
			// ref.Name through PGCatalogName first, which maps all four to
			// their long canonical form ("time with/without time zone" /
			// "timestamp with/without time zone") before typmodString ever
			// sees it — confirmed live via the same pg_query.Parse probe
			// used for the A_Const fix above. The bare "time"/"timestamp"
			// forms are kept here anyway (matching the varchar/bpchar case's
			// existing belt-and-suspenders style) rather than relying solely
			// on PGCatalogName's current behavior — this exact gap (a case
			// list going stale after PGCatalogName gained a new mapping, so
			// this switch silently stopped matching and dropped the typmod
			// entirely) is precisely what happened to "time"/"timestamp"
			// themselves when the "without time zone" mapping was added,
			// caught live via a real apply+plan round-trip before shipping.
			if val >= 0 {
				return fmt.Sprintf("(%d)", val)
			}
		}
	}
	return ""
}

// HashBody returns the SHA-256 of a normalised function/procedure body.
// Normalisation: trim leading/trailing whitespace; collapse internal
// whitespace runs to a single space. Used by both the IR builder and the
// introspect package (which hashes prosrc from pg_proc to produce a
// comparable digest without dollar-quote delimiters).
func HashBody(body string) string {
	normalised := strings.Join(strings.Fields(strings.TrimSpace(body)), " ")
	sum := sha256.Sum256([]byte(normalised))
	return fmt.Sprintf("%x", sum)
}

// HashFunctionBody is HashBody's language-aware variant.
//
// For a LANGUAGE SQL body (case-insensitive), it canonicalises via
// pg_query.Parse/Deparse before hashing — the same trick
// internal/introspect/opaque.go's canonicalDDL already uses for
// whole-statement reconstruction of the opaque-tier object kinds — so
// whitespace, quote-style, and clause-order differences no longer cause a
// spurious CREATE OR REPLACE.
//
// For a LANGUAGE plpgsql body, it canonicalises structurally via
// canonicalizePlpgsqlBody, given fullStatement — a complete
// "CREATE FUNCTION/PROCEDURE ... LANGUAGE plpgsql AS $$...$$" statement
// (the real source text on the builder side; a reconstructed,
// argument-accurate shim via RenderCreateFunctionSQL/RenderCreateProcedureSQL
// on the introspect side). A bare body string is not enough here, unlike
// LANGUAGE SQL: the PL/pgSQL compiler resolves the function's own parameter
// names against its declared argument list at compile time, so
// canonicalization needs the real signature, not just the body. If
// fullStatement is empty or canonicalization fails for any reason, this
// falls back to HashBody(body) unchanged.
//
// On any failure in either language's canonicalization it falls back to
// HashBody(body) unchanged; a hashing nicety must never block a build or
// introspection. Every other language (c, internal, other PL extensions) is
// NOT canonicalised and behaves exactly as HashBody always has. See RFC
// Section 9.5.
func HashFunctionBody(language, body, fullStatement string) string {
	if strings.EqualFold(language, "sql") {
		if canon, ok := canonicalizeSQLBody(body); ok {
			return HashBody(canon)
		}
		return HashBody(body)
	}
	if strings.EqualFold(language, "plpgsql") && fullStatement != "" {
		if canon, ok := canonicalizePlpgsqlBody(fullStatement); ok {
			return HashBody(canon)
		}
	}
	return HashBody(body)
}

// canonicalizePlpgsqlBody parses fullStatement via
// pg_query.ParsePlPgSqlToJSON, strips the one confirmed-volatile field
// ("lineno" — the PL/pgSQL compiler's source line number, which shifts
// under pure reformatting with zero semantic change), canonicalises every
// embedded PLpgSQL_expr fragment's "query" text in place (see
// canonicalizePlpgsqlExprFragments), and re-marshals the result for
// hashing. Confirmed by direct inspection of libpg_query's
// parser/pg_query_json_plpgsql.c: "location" and "stmtid" were hypothesized
// volatile before investigation but do not exist anywhere in this emitter's
// output — "lineno" is the entire volatility surface. json.Marshal on a
// map[string]any sorts keys alphabetically, so the re-marshaled output is
// deterministic regardless of libpg_query's emission order.
//
// Every PLpgSQL_expr node's "query" field is a raw, unparsed substring of
// the original source (conditions, assignment left/right-hand sides,
// RETURN expressions, RAISE params, cursor queries, embedded SQL) — this
// emitter does not re-parse or normalize it on its own. This is closed by
// separately re-parsing and re-deparsing each fragment according to its own
// "parseMode", using github.com/thec1oud/dpg_query_go's raw-parse-mode
// entry points (RAW_PARSE_PLPGSQL_EXPR/ASSIGN1/2/3 — the exact modes
// PostgreSQL's own PL/pgSQL compiler uses for these fragments, confirmed by
// reading libpg_query's parser.h and pl_gram.c directly, not guessed) —
// see canonicalizePlpgsqlExprFragments. This closes every parseMode this
// version of libpg_query actually emits (confirmed empirically and by
// reading pl_gram.c/pl_comp.c for every parseMode assignment site); a
// future libpg_query version introducing a new parseMode would fail open
// per-fragment (raw text retained for that one fragment) rather than block
// canonicalization of the rest of the body.
//
// Returns ok=false — caller falls back to raw HashBody(body) — on any parse
// error, an empty "[]" result (fullStatement didn't produce a compiled
// plpgsql function/DO block, e.g. because a reconstructed shim's argument
// list didn't match the body's own references), or a JSON unmarshal
// failure.
func canonicalizePlpgsqlBody(fullStatement string) (string, bool) {
	trimmed := strings.TrimSpace(fullStatement)
	if trimmed == "" {
		return "", false
	}
	jsonStr, err := pg_query.ParsePlPgSqlToJSON(trimmed)
	if err != nil {
		return "", false
	}
	if s := strings.TrimSpace(jsonStr); s == "" || s == "[]" {
		return "", false
	}
	var parsed any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return "", false
	}
	stripPlpgsqlVolatileFields(parsed)
	canonicalizePlpgsqlExprFragments(parsed)
	out, err := json.Marshal(parsed)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// stripPlpgsqlVolatileFields recursively deletes the "lineno" key from
// every map in v, mutating map[string]any values in place and recursing
// into []any elements.
func stripPlpgsqlVolatileFields(v any) {
	switch t := v.(type) {
	case map[string]any:
		delete(t, "lineno")
		for _, child := range t {
			stripPlpgsqlVolatileFields(child)
		}
	case []any:
		for _, child := range t {
			stripPlpgsqlVolatileFields(child)
		}
	}
}

// canonicalizePlpgsqlExprFragments recursively walks v looking for
// PLpgSQL_expr node value-maps — structurally, any map[string]any holding
// both a "query" and a "parseMode" key, the exact shape libpg_query's
// dump_expr (pg_query_json_plpgsql.c) emits for every expression field
// ("cond"/"expr"/"value"/params/etc. each wrap a single "PLpgSQL_expr" key
// whose value is {"query": "...", "parseMode": N}) — and replaces
// "query"'s raw source-substring value with a canonical, re-deparsed form.
// Mutates map[string]any values in place; recurses into []any elements.
//
// This match is structural, not an enumeration of plpgsql statement types:
// confirmed by grepping the whole of pg_query_json_plpgsql.c that dump_expr
// is the ONLY place in the file that ever writes "query" and "parseMode"
// together, so any construct whose expression fields reach the JSON output
// — including ones not specifically tested, e.g. RETURN QUERY, CASE
// statements, exception handlers, array-subscript or array-of-composite
// assignment targets, all confirmed working — is covered by this walk the
// same way, without needing a case for each one. See
// TestHashFunctionBodyPlpgsql* in typeutil_test.go for the verified shapes.
func canonicalizePlpgsqlExprFragments(v any) {
	switch t := v.(type) {
	case map[string]any:
		if q, hasQuery := t["query"]; hasQuery {
			if pm, hasMode := t["parseMode"]; hasMode {
				if qs, ok := q.(string); ok {
					if canon, ok := canonicalizePlpgsqlExprQuery(qs, pm); ok {
						t["query"] = canon
					}
				}
			}
		}
		for _, child := range t {
			canonicalizePlpgsqlExprFragments(child)
		}
	case []any:
		for _, child := range t {
			canonicalizePlpgsqlExprFragments(child)
		}
	}
}

// canonicalizePlpgsqlExprQuery canonicalizes a single PLpgSQL_expr
// fragment's raw query text, dispatched on its own parseMode (unmarshaled
// from JSON as float64, not int — a real pitfall, guarded explicitly
// below):
//   - 0 (RAW_PARSE_DEFAULT): a full, standalone SQL statement (a FOR-loop
//     query, a cursor's query) — canonicalise via the same
//     Parse/Deparse pattern canonicalizeSQLBody already uses.
//   - 2 (RAW_PARSE_PLPGSQL_EXPR): a bare expression fragment (condition,
//     RETURN value, RAISE/ASSERT param, default value, ...) — not valid
//     standalone SQL — canonicalise via pg_query.ParsePlPgSqlExpr/Deparse.
//   - 3/4/5 (RAW_PARSE_PLPGSQL_ASSIGN1/2/3): an assignment statement whose
//     query text is the whole "target := expr", not just the RHS —
//     canonicalise via canonicalizePlpgsqlAssign.
//   - anything else (confirmed by reading libpg_query's pl_gram.c/pl_comp.c
//     that no other value ever occurs here — kept only as a defensive
//     no-op, matching this file's fail-open philosophy): left untouched.
//
// Returns ok=false on any failure, so the caller leaves that one fragment's
// query text as raw source rather than aborting the whole body's
// canonicalization.
func canonicalizePlpgsqlExprQuery(query string, parseModeRaw any) (string, bool) {
	pm, ok := parseModeRaw.(float64)
	if !ok {
		return "", false
	}
	switch int(pm) {
	case 0:
		return canonicalizeSQLBody(query)
	case 2:
		res, err := pg_query.ParsePlPgSqlExpr(query)
		if err != nil || len(res.Stmts) == 0 {
			return "", false
		}
		out, err := pg_query.Deparse(res)
		if err != nil {
			return "", false
		}
		return out, true
	case 3, 4, 5:
		return canonicalizePlpgsqlAssign(query, int(pm))
	default:
		return "", false
	}
}

// canonicalizePlpgsqlAssign canonicalizes a PL/pgSQL assignment statement's
// raw "target := expr" text (parseMode 3/4/5 = a single-part, two-part, or
// three-part dotted target respectively). Parses via the matching
// pg_query.ParsePlPgSqlAssign{1,2,3} raw-parse mode, which returns a
// PLAssignStmt whose Val field is already an ordinary *pg_query.SelectStmt
// (deparseable directly) and whose Name/Indirection fields hold the
// target — reconstructed back to text via pg_query.MakeColumnRefNode,
// reusing the exact same dotted/subscripted-reference deparse logic
// PostgreSQL already uses for ordinary column references. libpg_query's own
// deparser has no support for PLAssignStmt as a top-level node at all
// (confirmed: no such case exists in postgres_deparse.c), so the target and
// value are deparsed independently as two synthetic SELECT-shaped
// ParseResults rather than deparsing the whole PLAssignStmt in one call.
// The exact separator/spacing of the reassembled "<target> := <value>"
// text doesn't need to match the original source: it is internal-only text
// used solely as hash input, never rendered to a user.
func canonicalizePlpgsqlAssign(query string, parseMode int) (string, bool) {
	var tree *pg_query.ParseResult
	var err error
	switch parseMode {
	case 3:
		tree, err = pg_query.ParsePlPgSqlAssign1(query)
	case 4:
		tree, err = pg_query.ParsePlPgSqlAssign2(query)
	case 5:
		tree, err = pg_query.ParsePlPgSqlAssign3(query)
	default:
		return "", false
	}
	if err != nil || len(tree.Stmts) == 0 {
		return "", false
	}
	assign := tree.Stmts[0].GetStmt().GetPlassignStmt()
	if assign == nil || assign.Val == nil {
		return "", false
	}

	// assign.Val is already a full *SelectStmt (the PL/pgSQL raw parser
	// wraps the right-hand side of an assignment as one internally, the
	// same way RAW_PARSE_PLPGSQL_EXPR does) — wrap it directly as a
	// top-level statement, no ResTarget indirection needed.
	valOut, err := pg_query.Deparse(&pg_query.ParseResult{
		Stmts: []*pg_query.RawStmt{
			{Stmt: &pg_query.Node{Node: &pg_query.Node_SelectStmt{SelectStmt: assign.Val}}},
		},
	})
	if err != nil {
		return "", false
	}

	// The target (Name + Indirection) is a bare reference, not a statement
	// — wrap it as a ResTarget inside a synthetic SELECT to deparse it.
	targetFields := append([]*pg_query.Node{pg_query.MakeStrNode(assign.Name)}, assign.Indirection...)
	targetNode := pg_query.MakeColumnRefNode(targetFields, 0)
	targetOut, err := pg_query.Deparse(&pg_query.ParseResult{
		Stmts: []*pg_query.RawStmt{
			{
				Stmt: &pg_query.Node{
					Node: &pg_query.Node_SelectStmt{
						SelectStmt: &pg_query.SelectStmt{
							TargetList: []*pg_query.Node{pg_query.MakeResTargetNodeWithVal(targetNode, 0)},
						},
					},
				},
			},
		},
	})
	if err != nil {
		return "", false
	}

	return targetOut + " := " + valOut, true
}

// canonicalizeSQLBody parses body as one or more SQL statements and
// deparses them back — the same approach as internal/introspect/opaque.go's
// canonicalDDL, reimplemented locally here rather than imported: introspect
// already depends on ir, not the reverse, so ir must not import introspect.
// Returns ok=false on any parse/deparse failure so the caller falls back to
// raw-text hashing.
func canonicalizeSQLBody(body string) (string, bool) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "", false
	}
	res, err := pg_query.Parse(trimmed)
	if err != nil || len(res.Stmts) == 0 {
		return "", false
	}
	out, err := pg_query.Deparse(res)
	if err != nil {
		return "", false
	}
	return out, true
}

// extractFuncBody extracts the dollar-quoted body text from a function's
// Part1 string (which includes the body). Returns the text between the
// outermost dollar-quote delimiters, or "" if not found.
func extractFuncBody(part1 string) string {
	// Find the first $...$ delimiter.
	first := strings.Index(part1, "$")
	if first < 0 {
		return ""
	}
	// Find the end of the opening tag.
	tagEnd := strings.Index(part1[first+1:], "$")
	if tagEnd < 0 {
		return ""
	}
	tag := part1[first : first+tagEnd+2] // e.g. "$$" or "$body$"
	// Find opening tag occurrence in full string.
	start := strings.Index(part1, tag)
	if start < 0 {
		return ""
	}
	inner := part1[start+len(tag):]
	// Find closing tag.
	end := strings.Index(inner, tag)
	if end < 0 {
		return ""
	}
	return inner[:end]
}
