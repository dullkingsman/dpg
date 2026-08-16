package diff

import (
	"strings"

	"github.com/dullkingsman/dpg/internal/ir"
)

// implicitCastPairs is the raw pg_catalog.pg_type.typname pairs (source,
// target) of every implicit cast (pg_cast.castcontext = 'i') in a real
// PostgreSQL 17 instance, extracted verbatim from a live catalog — NOT
// guessed or reconstructed from memory (the exact risk this project's own
// conventions call out; see e.g. Table/Index STORAGE's live pg_type.typstorage
// join, chosen for the identical reason). Extracted 2026-08-17 via:
//
//	SELECT s.typname, t.typname
//	FROM pg_cast c
//	JOIN pg_type s ON s.oid = c.castsource
//	JOIN pg_type t ON t.oid = c.casttarget
//	WHERE c.castcontext = 'i' AND c.castsource != c.casttarget
//	ORDER BY 1, 2;
//
// against a real `postgres:17` container — the exact image every live
// integration test in this repo already standardizes on. This is the
// authoritative source RFC §17.2's "ALTER TABLE ALTER COLUMN TYPE (implicit
// cast) -> CAUTION" row means by "implicit cast": PostgreSQL's own
// ATExecAlterColumnType (tablecmds.c) builds a default `USING col::newtype`
// when none is given, and a plain `::` cast succeeds for ANY registered cast
// regardless of context (explicit/assignment/implicit) — so this table is
// deliberately narrower than "what PG's ALTER COLUMN TYPE would technically
// accept without erroring." It answers a different, RFC-specific question:
// "is this change provably lossless," which is exactly what castcontext='i'
// means (implicit casts are, by PostgreSQL's own definition, always safe to
// apply automatically in any context, including comparisons and function
// argument matching — the strongest cast-safety guarantee PG has).
//
// TestImplicitCastsMatchesLiveCatalog (implicit_casts_integration_test.go)
// re-runs this exact query against a live container and asserts an exact
// match — a canary against a future PostgreSQL version silently adding,
// removing, or changing an implicit cast, so this table can never go stale
// without a test failing first.
//
// Every pg_catalog "reg*" object-identifier type (regclass, regproc, ...)
// and internal-only catalog type (pg_node_tree, pg_ndistinct, ...) is
// included for completeness/traceability even though a DPG user would
// essentially never declare a column of one of these types — filtering them
// out would be an editorial judgment call with no benefit (a lookup miss is
// exactly as harmless as a lookup that was never possible), so the table is
// kept as the verbatim, unfiltered extraction.
var implicitCastPairs = [][2]string{
	{"bit", "varbit"},
	{"bpchar", "name"},
	{"bpchar", "text"},
	{"bpchar", "varchar"},
	{"char", "text"},
	{"cidr", "inet"},
	{"date", "timestamp"},
	{"date", "timestamptz"},
	{"float4", "float8"},
	{"int2", "float4"},
	{"int2", "float8"},
	{"int2", "int4"},
	{"int2", "int8"},
	{"int2", "numeric"},
	{"int2", "oid"},
	{"int2", "regclass"},
	{"int2", "regcollation"},
	{"int2", "regconfig"},
	{"int2", "regdictionary"},
	{"int2", "regnamespace"},
	{"int2", "regoper"},
	{"int2", "regoperator"},
	{"int2", "regproc"},
	{"int2", "regprocedure"},
	{"int2", "regrole"},
	{"int2", "regtype"},
	{"int4", "float4"},
	{"int4", "float8"},
	{"int4", "int8"},
	{"int4", "numeric"},
	{"int4", "oid"},
	{"int4", "regclass"},
	{"int4", "regcollation"},
	{"int4", "regconfig"},
	{"int4", "regdictionary"},
	{"int4", "regnamespace"},
	{"int4", "regoper"},
	{"int4", "regoperator"},
	{"int4", "regproc"},
	{"int4", "regprocedure"},
	{"int4", "regrole"},
	{"int4", "regtype"},
	{"int8", "float4"},
	{"int8", "float8"},
	{"int8", "numeric"},
	{"int8", "oid"},
	{"int8", "regclass"},
	{"int8", "regcollation"},
	{"int8", "regconfig"},
	{"int8", "regdictionary"},
	{"int8", "regnamespace"},
	{"int8", "regoper"},
	{"int8", "regoperator"},
	{"int8", "regproc"},
	{"int8", "regprocedure"},
	{"int8", "regrole"},
	{"int8", "regtype"},
	{"macaddr", "macaddr8"},
	{"macaddr8", "macaddr"},
	{"name", "text"},
	{"numeric", "float4"},
	{"numeric", "float8"},
	{"oid", "regclass"},
	{"oid", "regcollation"},
	{"oid", "regconfig"},
	{"oid", "regdictionary"},
	{"oid", "regnamespace"},
	{"oid", "regoper"},
	{"oid", "regoperator"},
	{"oid", "regproc"},
	{"oid", "regprocedure"},
	{"oid", "regrole"},
	{"oid", "regtype"},
	{"pg_dependencies", "bytea"},
	{"pg_dependencies", "text"},
	{"pg_mcv_list", "bytea"},
	{"pg_mcv_list", "text"},
	{"pg_ndistinct", "bytea"},
	{"pg_ndistinct", "text"},
	{"pg_node_tree", "text"},
	{"regclass", "oid"},
	{"regcollation", "oid"},
	{"regconfig", "oid"},
	{"regdictionary", "oid"},
	{"regnamespace", "oid"},
	{"regoper", "oid"},
	{"regoper", "regoperator"},
	{"regoperator", "oid"},
	{"regoperator", "regoper"},
	{"regproc", "oid"},
	{"regproc", "regprocedure"},
	{"regprocedure", "oid"},
	{"regprocedure", "regproc"},
	{"regrole", "oid"},
	{"regtype", "oid"},
	{"text", "bpchar"},
	{"text", "name"},
	{"text", "regclass"},
	{"text", "varchar"},
	{"time", "interval"},
	{"time", "timetz"},
	{"timestamp", "timestamptz"},
	{"varbit", "bit"},
	{"varchar", "bpchar"},
	{"varchar", "name"},
	{"varchar", "regclass"},
	{"varchar", "text"},
}

// implicitCasts maps a source type name to the set of target type names it
// has an implicit cast to, both sides normalized via ir.PGCatalogName so the
// keys/values are in exactly the same "dialect" resolveColType/TypeRef.String
// themselves produce (built-in aliases like "int4"/"varchar" rendered as
// "integer"/"character varying") — guaranteeing this table's strings match
// what the differ actually compares by construction, not by coincidence.
var implicitCasts = buildImplicitCasts()

func buildImplicitCasts() map[string]map[string]bool {
	m := make(map[string]map[string]bool, len(implicitCastPairs))
	for _, pair := range implicitCastPairs {
		from := ir.PGCatalogName(pair[0])
		to := ir.PGCatalogName(pair[1])
		if m[from] == nil {
			m[from] = make(map[string]bool)
		}
		m[from][to] = true
	}
	return m
}

// hasImplicitCast reports whether PostgreSQL has an implicit cast from the
// fully-rendered type text from to the fully-rendered type text to (the same
// strings resolveColType/TypeRef.String() produce, typmod and array
// brackets included — bareTypeName strips both before the lookup). Stripping
// array brackets is deliberate, not just harmless: confirmed live against a
// real PostgreSQL 17 container that an array-to-array type change (e.g.
// integer[] -> bigint[]) is itself implicit exactly when the corresponding
// ELEMENT types are (PostgreSQL's own coercion code derives an array cast's
// applicable context from the element cast's context — pg_cast has no array
// OIDs in it at all, so there is no separate array-level entry to look up).
func hasImplicitCast(from, to string) bool {
	return implicitCasts[bareTypeName(from)][bareTypeName(to)]
}

// bareTypeName is TypeRef.String()'s (ir/types.go) exact inverse: it strips
// trailing "[]" array-dimension markers (always outermost/last — see
// hasImplicitCast's doc comment for why this is stripped deliberately, not
// just for convenience), then removes a parenthesized modifier from
// wherever it appears — not necessarily at the end, since "with time zone"
// types insert theirs mid-string (e.g. "timestamp(3) with time zone", never
// "timestamp with time zone(3)") — recovering the bare base type name
// implicitCasts is keyed on. A schema-qualified user-defined type is never
// in implicitCasts regardless (the table only holds bare pg_catalog scalar
// names, unqualified), so this function leaves schema-qualification intact
// and a lookup for one simply misses — correctly, since implicit casts
// involving user-defined types aren't covered by this table at all.
func bareTypeName(rendered string) string {
	s := rendered
	for strings.HasSuffix(s, "[]") {
		s = strings.TrimSuffix(s, "[]")
	}
	base, _ := splitTypeNameAndMods(s)
	return base
}
