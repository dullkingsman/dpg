package ir

import (
	"encoding/json"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// TableRef is a table reference statically found by ExtractTableRefs.
// Schema is "" when the reference was unqualified in source.
type TableRef struct {
	Schema string
	Name   string
}

// ExtractTableRefs walks body for every statically-present table reference —
// a RangeVar in a FROM/JOIN clause, an INSERT/UPDATE/DELETE target, a CTE, or
// a subquery — and returns the distinct references found. Order is
// unspecified (a plpgsql body's fragments are collected via a JSON object
// walk, whose key order Go's encoding/json makes no guarantee about) — never
// rely on it; callers only ever need the set. RFC §22.1 item 9 (audit item
// #30): used to build real function/
// procedure-body-to-table and view-to-table dependency edges, replacing a
// blunt "depends on every table" heuristic.
//
// language selects how body is parsed:
//
//   - "plpgsql" reuses the same fragment-parsing machinery as BodyHash (see
//     canonicalizePlpgsqlBody) — libpg_query's plpgsql parse entry point is
//     used to pull out each embedded SQL fragment (a FOR-loop query, an
//     EXECSQL statement, a RETURN QUERY, a bare expression that may itself
//     contain a sub-SELECT, an assignment's RHS, ...), each of which is
//     then re-parsed on its own. Unlike every other case here, body MUST be
//     a complete "CREATE FUNCTION/PROCEDURE ... LANGUAGE plpgsql AS
//     $$...$$" statement, not the bare function body — exactly
//     HashFunctionBody's own fullStatement parameter, for the same reason
//     (the plpgsql compiler resolves the function's own parameter names
//     against its declared argument list, which a bare body doesn't carry).
//     A caller without the real source text (as at dependency-graph build
//     time) builds the same kind of reconstructed, argument-accurate shim
//     HashFunctionBody's introspect-side caller already does, via
//     RenderCreateFunctionSQL/RenderCreateProcedureSQL.
//   - Anything else (including "sql", and a plain view query, which is
//     already standalone SQL either way) is parsed directly as body.
//
// Dynamic SQL (EXECUTE '...' as a plain string literal) is invisible to
// this walk — a known, accepted limitation matching real PostgreSQL's own
// inability to validate it either; only statically-present references are
// detected.
func ExtractTableRefs(language, body string) []TableRef {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return nil
	}

	var trees []*pg_query.ParseResult
	if strings.EqualFold(language, "plpgsql") {
		for _, frag := range plpgsqlQueryFragments(trimmed) {
			if tree, ok := parsePlpgsqlFragment(frag); ok {
				trees = append(trees, tree)
			}
		}
	} else if tree, err := pg_query.Parse(trimmed); err == nil {
		trees = append(trees, tree)
	}

	seen := map[TableRef]bool{}
	var refs []TableRef
	for _, tree := range trees {
		walkRangeVars(tree, func(rv *pg_query.RangeVar) {
			ref := TableRef{Schema: rv.Schemaname, Name: rv.Relname}
			if ref.Name == "" || seen[ref] {
				return
			}
			seen[ref] = true
			refs = append(refs, ref)
		})
	}
	return refs
}

// parsePlpgsqlFragment re-parses a single embedded SQL fragment extracted by
// plpgsqlQueryFragments, dispatched on its own parseMode exactly like
// canonicalizePlpgsqlExprQuery — see that function's doc comment for what
// each mode means. Returns ok=false on any parse failure, so one bad
// fragment doesn't abort the whole body's table-reference extraction.
func parsePlpgsqlFragment(frag plpgsqlFragment) (*pg_query.ParseResult, bool) {
	switch frag.parseMode {
	case 0:
		tree, err := pg_query.Parse(frag.query)
		return tree, err == nil
	case 2:
		tree, err := pg_query.ParsePlPgSqlExpr(frag.query)
		return tree, err == nil
	case 3:
		tree, err := pg_query.ParsePlPgSqlAssign1(frag.query)
		return tree, err == nil
	case 4:
		tree, err := pg_query.ParsePlPgSqlAssign2(frag.query)
		return tree, err == nil
	case 5:
		tree, err := pg_query.ParsePlPgSqlAssign3(frag.query)
		return tree, err == nil
	default:
		return nil, false
	}
}

// plpgsqlFragment is one embedded SQL fragment pulled out of a plpgsql
// body's JSON AST — a "query"/"parseMode" pair, the exact shape
// libpg_query's dump_expr (pg_query_json_plpgsql.c) emits for every
// expression field. See canonicalizePlpgsqlExprFragments's doc comment: this
// structural match (not an enumeration of plpgsql statement types) covers
// every plpgsql construct whose expression fields reach the JSON output.
type plpgsqlFragment struct {
	query     string
	parseMode int
}

// plpgsqlQueryFragments parses fullStatement (a full "CREATE FUNCTION ..."
// or "CREATE PROCEDURE ..." statement, or a bare plpgsql block) via
// libpg_query's plpgsql entry point and collects every embedded SQL
// fragment. Returns nil on any parse failure or an empty result — callers
// simply extract no table references from that body, rather than erroring
// the whole dependency-graph build.
func plpgsqlQueryFragments(fullStatement string) []plpgsqlFragment {
	jsonStr, err := pg_query.ParsePlPgSqlToJSON(fullStatement)
	if err != nil {
		return nil
	}
	if s := strings.TrimSpace(jsonStr); s == "" || s == "[]" {
		return nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil
	}
	var frags []plpgsqlFragment
	collectPlpgsqlQueryFragments(parsed, &frags)
	return frags
}

func collectPlpgsqlQueryFragments(v any, out *[]plpgsqlFragment) {
	switch t := v.(type) {
	case map[string]any:
		if q, hasQuery := t["query"]; hasQuery {
			if pm, hasMode := t["parseMode"]; hasMode {
				if qs, ok := q.(string); ok {
					if pmf, ok := pm.(float64); ok {
						*out = append(*out, plpgsqlFragment{query: qs, parseMode: int(pmf)})
					}
				}
			}
		}
		for _, child := range t {
			collectPlpgsqlQueryFragments(child, out)
		}
	case []any:
		for _, child := range t {
			collectPlpgsqlQueryFragments(child, out)
		}
	}
}

// walkRangeVars generically walks msg's protobuf field tree (any pg_query
// node — a *pg_query.ParseResult, a *pg_query.Node, or anything reachable
// from one) and calls visit for every *pg_query.RangeVar found, regardless
// of position — FROM, JOIN, a subquery, a CTE, an INSERT/UPDATE/DELETE
// target. Generic over the whole grammar via protoreflect rather than
// hand-enumerating every node kind that can carry a RangeVar (there is no
// such enumeration in libpg_query itself, and a hand-written one would
// silently miss whatever the next PostgreSQL grammar addition introduces).
func walkRangeVars(msg proto.Message, visit func(*pg_query.RangeVar)) {
	if msg == nil {
		return
	}
	mr := msg.ProtoReflect()
	if !mr.IsValid() {
		return
	}
	mr.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind && fd.Kind() != protoreflect.GroupKind {
			return true
		}
		if fd.IsList() {
			list := v.List()
			for i := 0; i < list.Len(); i++ {
				visitRangeVarMessage(list.Get(i).Message(), visit)
			}
			return true
		}
		if fd.IsMap() {
			// No pg_query node ever carries a map<..., message> field.
			return true
		}
		visitRangeVarMessage(v.Message(), visit)
		return true
	})
}

func visitRangeVarMessage(m protoreflect.Message, visit func(*pg_query.RangeVar)) {
	if !m.IsValid() {
		return
	}
	iface := m.Interface()
	if rv, ok := iface.(*pg_query.RangeVar); ok {
		visit(rv)
	}
	if pm, ok := iface.(proto.Message); ok {
		walkRangeVars(pm, visit)
	}
}
