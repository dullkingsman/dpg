package ir

import (
	"crypto/sha256"
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
