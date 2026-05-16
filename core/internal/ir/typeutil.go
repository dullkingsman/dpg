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

// typmodString reconstructs the typemod display string from pg_query Typmods nodes.
func typmodString(typeName string, mods []*pg_query.Node) string {
	if len(mods) == 0 {
		return ""
	}
	// For most types, the first typemod is an integer constant.
	if ic := mods[0].GetInteger(); ic != nil {
		val := ic.Ival
		switch typeName {
		case "character", "character varying", "bpchar", "varchar":
			// PG stores length+4 in typmod
			if val > 4 {
				return fmt.Sprintf("(%d)", val-4)
			}
		case "numeric":
			if len(mods) >= 2 {
				if ic2 := mods[1].GetInteger(); ic2 != nil {
					return fmt.Sprintf("(%d,%d)", val, ic2.Ival)
				}
			}
			return fmt.Sprintf("(%d)", val)
		case "time", "timetz", "timestamp", "timestamptz", "interval":
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
