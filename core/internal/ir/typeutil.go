package ir

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// typeNameToRef converts a pg_query TypeName node into an ir.TypeRef.
func typeNameToRef(tn *pg_query.TypeName) TypeRef {
	if tn == nil {
		return TypeRef{Name: "unknown"}
	}
	ref := TypeRef{
		ArrayDims: len(tn.ArrayBounds),
		SetOf:     tn.Setof,
	}

	// Extract schema and type name from the Names list.
	// For built-in types pg_query emits ["pg_catalog", "int4"] etc.
	// For custom types it emits ["myschema", "mytype"] or just ["mytype"].
	names := make([]string, 0, len(tn.Names))
	for _, n := range tn.Names {
		if sv := n.GetString_(); sv != nil {
			names = append(names, sv.Sval)
		}
	}

	switch len(names) {
	case 0:
		ref.Name = "unknown"
	case 1:
		// pg_query emits some built-in aliases (e.g. "timestamptz") as a
		// single-part name rather than ["pg_catalog", "timestamptz"]. Run
		// them through pgCatalogName so the canonical form always matches
		// what format_type() returns during introspection.
		ref.Name = pgCatalogName(names[0])
	case 2:
		if names[0] == "pg_catalog" {
			// Built-in: strip the catalog prefix and use the canonical name.
			ref.Name = pgCatalogName(names[1])
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
// hand-written OPERATOR FAMILY member's op_type (RFC §14.4) compares equal
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

// pgCatalogName maps pg_catalog internal type names to their SQL equivalents.
func pgCatalogName(internal string) string {
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
		case "time", "timetz", "time with time zone",
			"timestamp", "timestamptz", "timestamp with time zone", "interval":
			// timetz/timestamptz never actually reach this switch under their
			// short internal name: typeNameToRef runs ref.Name through
			// pgCatalogName first, which maps both to their long canonical
			// form ("time with time zone" / "timestamp with time zone")
			// before typmodString ever sees it — confirmed live via the same
			// pg_query.Parse probe used for the A_Const fix above. Both forms
			// are kept here (matching the varchar/bpchar case's existing
			// belt-and-suspenders style) rather than relying solely on
			// pgCatalogName's current behavior.
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
// §9.5.
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
// under pure reformatting with zero semantic change), and re-marshals the
// result for hashing. Confirmed by direct inspection of libpg_query's
// parser/pg_query_json_plpgsql.c: "location" and "stmtid" were hypothesized
// volatile before investigation but do not exist anywhere in this emitter's
// output — "lineno" is the entire volatility surface. json.Marshal on a
// map[string]any sorts keys alphabetically, so the re-marshaled output is
// deterministic regardless of libpg_query's emission order.
//
// Known residual limitation, deliberately not addressed here: every
// PLpgSQL_expr node's "query" field is a raw, unparsed substring of the
// original source (conditions, assignment RHS, RETURN expressions, RAISE
// params, cursor queries, embedded SQL) — this emitter does not re-parse or
// normalize it. A whitespace-only change *inside* one of these fragments
// (e.g. "a||b" vs "a || b") still changes the hash after this
// canonicalization; only the outer control-flow shape (statement
// ordering/nesting, declarations) is canonicalised. Many such fragments
// (e.g. a bare condition like "n IS NULL") are not complete standalone SQL
// statements, so pg_query.Parse/Deparse would routinely fail on them, and a
// reliable fragment-vs-statement heuristic to make per-expression
// sub-canonicalisation safe is materially more scope than this fix — left
// for a future pass if real false-positive reports justify it. See RFC
// §9.5.
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
