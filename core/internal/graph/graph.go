// Package graph implements pipeline.DependencyResolver. It performs
// topological sort using Kahn's algorithm and resolves circular FK dependencies
// via DEFERRABLE constraints.
package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	pipeline.Default.Register(pipeline.KeyDependencyResolver, New())
}

// Resolver implements pipeline.DependencyResolver.
type Resolver struct{}

// New returns a Resolver.
func New() *Resolver { return &Resolver{} }

// Sort performs a topological sort of the IR objects, respecting the dependency
// edges described in RFC Phase 7. Circular FK dependencies that are all
// DEFERRABLE are resolved by emitting the tables without the circular FK and
// appending the FK as a deferred ALTER TABLE statement.
func (r *Resolver) Sort(objects []pipeline.IRObject) ([]pipeline.IRObject, error) {
	n := len(objects)
	if n == 0 {
		return nil, nil
	}

	// Build index: qualifiedName → position. Also record the set of schemas
	// declared in source — references whose schema is in this set must resolve;
	// references into schemas not in source (e.g. extension-managed `extensions.geometry`)
	// are out of DPG's scope and silently allowed.
	idx := make(map[string]int, n)
	schemaSet := make(map[string]bool)
	for i, obj := range objects {
		idx[obj.QualifiedName()] = i
		if s, ok := obj.(*ir.Schema); ok {
			schemaSet[s.Name] = true
		}
	}

	var diags pipeline.Diagnostics

	// edges[i] = set of j where i must come BEFORE j (i → j).
	// Equivalently: j depends on i.
	edges := make([]map[int]bool, n)
	for i := range edges {
		edges[i] = make(map[int]bool)
	}

	// mustPrecede(before, after) records that `before` must be emitted before `after`.
	mustPrecede := func(before, after int) {
		if before != after {
			edges[before][after] = true
		}
	}

	// dependsOn(obj, dep) records that obj depends on dep (dep must come first).
	dependsOn := func(obj, dep int) {
		mustPrecede(dep, obj)
	}

	// schemaEdge adds a dependency from a schema-scoped object to its schema.
	schemaEdge := func(objIdx int, schema string) {
		if schema == "" {
			return
		}
		if schemaIdx, ok := idx[schema]; ok {
			dependsOn(objIdx, schemaIdx)
		}
	}

	// refEdge adds a dependency from objIdx to whatever object is keyed by
	// qualifiedKey, if one exists in the object set. Like the unqualified
	// custom-type case in the *ir.Table branch below, this never errors when
	// the reference isn't found in source — the referenced object (an
	// operator family, TS parser, or TS template) is very commonly a
	// pg_catalog built-in that legitimately isn't part of the managed set,
	// unlike an FK target, which DOES error when unresolved.
	refEdge := func(objIdx int, qualifiedKey string) {
		if refIdx, ok := idx[qualifiedKey]; ok {
			dependsOn(objIdx, refIdx)
		}
	}

	// defaultSchema returns schema if non-empty, else fallback — mirroring how
	// an unqualified reference (FAMILY name, PARSER name, TEMPLATE name)
	// resolves to the referencing object's own schema, the same convention
	// already used for unqualified column type references below.
	defaultSchema := func(schema, fallback string) string {
		if schema != "" {
			return schema
		}
		return fallback
	}

	// funcRefKey builds the desired-object index key for a trigger or event
	// trigger's EXECUTE FUNCTION reference, which arrives as a single,
	// possibly schema-qualified plain string (Trigger.Function is parsed by
	// the DPG blockparser, EventTrigger.Function by pg_query's Funcname list
	// — neither is a structured Schema+Name pair the way a column type or FK
	// target is) — mirroring how Function.QualifiedName() always includes
	// an arg-type suffix, empty for the zero-argument functions a trigger or
	// event trigger can call.
	funcRefKey := func(ref, fallbackSchema string) string {
		if ref == "" {
			return ""
		}
		schema, name := fallbackSchema, ref
		if i := strings.LastIndex(ref, "."); i >= 0 {
			schema, name = ref[:i], ref[i+1:]
		}
		if schema == "" {
			return name + "()"
		}
		return schema + "." + name + "()"
	}

	// funcPrefixEdge adds a dependency from objIdx to every Function object
	// whose "schema.name(" prefix matches ref — used for CAST's WITH
	// FUNCTION reference, which (unlike a trigger's always-zero-argument
	// function) takes the cast's source type as a real argument, so an
	// exact funcRefKey "()" match would never hit. Matching by prefix
	// rather than resolving the exact overload avoids needing to convert
	// CreateCastStmt's argument TypeName into the identical ArgsKey format
	// Function.QualifiedName() uses; over-matching a same-named overload
	// only adds a harmless extra ordering constraint, never a wrong result.
	funcPrefixEdge := func(objIdx int, ref, fallbackSchema string) {
		if ref == "" {
			return
		}
		schema, name := fallbackSchema, ref
		if i := strings.LastIndex(ref, "."); i >= 0 {
			schema, name = ref[:i], ref[i+1:]
		}
		prefix := name + "("
		if schema != "" {
			prefix = schema + "." + name + "("
		}
		for key, j := range idx {
			if strings.HasPrefix(key, prefix) {
				dependsOn(objIdx, j)
			}
		}
	}

	// Circular FK edges that can be deferred.
	type deferredFK struct {
		table *ir.Table
		fk    *ir.Constraint
	}
	var deferred []deferredFK

	for i, obj := range objects {
		switch o := obj.(type) {
		case *ir.Table:
			// Table depends on its schema.
			schemaEdge(i, o.Schema)

			// Table depends on any custom types used in columns. If the schema is
			// in source but the type isn't defined, that's a real bug — surface it
			// rather than silently dropping the dependency.
			for _, col := range o.Columns {
				if col.Type.Schema == "pg_catalog" {
					continue
				}
				if col.Type.Schema == "" {
					// Unqualified type: could be a built-in or a custom type in the
					// table's own schema (this is how introspected columns look —
					// e.g. an enum column reads as "mood", not "public.mood"). Add a
					// dependency only when a matching custom type is defined; never
					// error, since built-ins legitimately aren't in the index.
					if o.Schema != "" {
						if j, ok := idx[o.Schema+"."+col.Type.Name]; ok {
							dependsOn(i, j)
						}
					}
					continue
				}
				typeKey := col.Type.Schema + "." + col.Type.Name
				if j, ok := idx[typeKey]; ok {
					dependsOn(i, j)
				} else if schemaSet[col.Type.Schema] {
					diags = append(diags, pipeline.Errorf(col.SrcPos,
						"unresolved type reference %q used by column %s.%s.%s — no such type defined in source",
						typeKey, o.Schema, o.Name, col.Name))
				}
			}

			// Table depends on FK-referenced tables. Like type refs, if the
			// referenced schema is managed in source, an unresolved FK target is
			// reported as an error so user typos surface at plan time.
			for _, cst := range o.Constraints {
				if cst.Type != "FOREIGN KEY" {
					continue
				}
				refSchema, refTable := extractFKRefParts(cst.Expr)
				if refTable == "" {
					continue
				}
				resolvedKey, ok := resolveFKTarget(idx, refSchema, refTable, o.Schema)
				if ok {
					dependsOn(i, idx[resolvedKey])
					continue
				}
				effectiveSchema := refSchema
				if effectiveSchema == "" {
					effectiveSchema = o.Schema
				}
				if effectiveSchema == "" || schemaSet[effectiveSchema] {
					displayRef := refTable
					if effectiveSchema != "" {
						displayRef = effectiveSchema + "." + refTable
					}
					diags = append(diags, pipeline.Errorf(cst.Pos,
						"unresolved FK reference %q from %s — no such table defined in source",
						displayRef, o.QualifiedName()))
				}
			}

			// Table depends on the function(s) its own triggers call — a
			// trigger created before its function exists fails at apply
			// time (confirmed live: "function ... does not exist"). Never
			// errors when unresolved: a trigger function is very commonly a
			// pg_catalog/extension-provided one (e.g. moddatetime()) that
			// legitimately isn't part of the managed object set.
			for _, trg := range o.Triggers {
				if j, ok := idx[funcRefKey(trg.Function, o.Schema)]; ok {
					dependsOn(i, j)
				}
			}

		case *ir.EventTrigger:
			// Event trigger depends on the function it calls — same
			// ordering hazard and the same reasoning for never erroring on
			// an unresolved reference as the table-trigger case above.
			// Event triggers aren't schema-scoped, so an unqualified
			// function reference falls back to "public" (this project's
			// own default-schema convention), matching how dump.go and
			// other opaque-object rendering already assume public as the
			// default when no schema is otherwise known.
			if j, ok := idx[funcRefKey(o.Function, "public")]; ok {
				dependsOn(i, j)
			}

		case *ir.View:
			// View depends on its schema.
			schemaEdge(i, o.Schema)
			// Heuristic: all views depend on all tables (query AST analysis deferred).
			for j, dep := range objects {
				if j != i {
					if _, ok := dep.(*ir.Table); ok {
						dependsOn(i, j)
					}
				}
			}

		case *ir.Cast:
			// Cast depends on its WITH FUNCTION target — same ordering
			// hazard as Trigger/EventTrigger above, found the same way
			// (live apply failure: "function ... does not exist"). Casts
			// aren't schema-scoped in PostgreSQL, so an unqualified
			// function reference falls back to "public".
			funcPrefixEdge(i, o.Function, "public")

		case *ir.Type:
			// Type/domain/enum depends on its schema.
			schemaEdge(i, o.Schema)

		case *ir.Function:
			schemaEdge(i, o.Schema)

		case *ir.Procedure:
			schemaEdge(i, o.Schema)

		case *ir.Aggregate:
			schemaEdge(i, o.Schema)

		case *ir.Sequence:
			schemaEdge(i, o.Schema)

		case *ir.Collation:
			schemaEdge(i, o.Schema)

		case *ir.Operator:
			schemaEdge(i, o.Schema)
			// Operator depends on its PROCEDURE target — same ordering
			// hazard, found the same way, as Trigger/EventTrigger/Cast
			// above: an operator created before its function exists fails
			// at apply time. Matched by prefix like Cast (a real argument
			// list, not the zero-arg shape a trigger function always has).
			funcPrefixEdge(i, o.Function, o.Schema)

		case *ir.OperatorClass:
			schemaEdge(i, o.Schema)
			// Class depends on its FAMILY (must be CREATEd first — PostgreSQL's
			// CREATE OPERATOR FAMILY has no IF NOT EXISTS, so ordering isn't
			// self-healing the way, say, a redundant CREATE SCHEMA IF NOT EXISTS
			// would be). o.FamilyName is empty only for hand-written source that
			// omits FAMILY (relying on PG's own same-name auto-creation) — every
			// introspected class always has one now (see introspectOperatorClasses).
			if o.FamilyName != "" {
				famSchema := defaultSchema(o.FamilySchema, o.Schema)
				refEdge(i, famSchema+"."+o.FamilyName+" USING "+o.AccessMethod+" FAMILY")
			}
			// Class depends on every support FUNCTION it declares — same
			// ordering hazard as Trigger/EventTrigger/Cast/Operator above,
			// found the same way (live apply failure). Unlike those single-
			// function cases, a class can declare several (numbered support
			// slots), so this loops over all of them.
			for _, fn := range o.Functions {
				funcPrefixEdge(i, fn, o.Schema)
			}

		case *ir.OperatorFamily:
			schemaEdge(i, o.Schema)

		case *ir.StatisticsObject:
			schemaEdge(i, o.Schema)

		case *ir.TSConfig:
			schemaEdge(i, o.Schema)
			// Config depends on its PARSER (see refEdge: only an edge if the
			// parser is itself part of the managed object set — most commonly
			// it's a pg_catalog built-in like "default", which correctly gets no
			// edge at all).
			if o.ParserName != "" {
				refEdge(i, defaultSchema(o.ParserSchema, o.Schema)+"."+o.ParserName)
			}

		case *ir.TSDict:
			schemaEdge(i, o.Schema)
			// Dict depends on its TEMPLATE, same reasoning as TSConfig's PARSER.
			if o.TemplateName != "" {
				refEdge(i, defaultSchema(o.TemplateSchema, o.Schema)+"."+o.TemplateName)
			}

		case *ir.TSParser:
			schemaEdge(i, o.Schema)
			// Parser depends on its START/GETTOKEN/END/LEXTYPES/HEADLINE
			// support functions — same ordering hazard as
			// Trigger/EventTrigger/Cast/Operator/OperatorClass above. In
			// practice these almost always name a pg_catalog built-in
			// (custom ones require C), so funcPrefixEdge's silent no-op on
			// an unresolved reference is the common case, not the
			// exception.
			for _, fn := range o.Functions {
				funcPrefixEdge(i, fn, o.Schema)
			}

		case *ir.TSTemplate:
			schemaEdge(i, o.Schema)
			// Template depends on its INIT/LEXIZE support functions — same
			// reasoning as TSParser above.
			for _, fn := range o.Functions {
				funcPrefixEdge(i, fn, o.Schema)
			}
		}
	}

	if diags.HasErrors() {
		return nil, diags
	}

	// Kahn's algorithm.
	// inDegree[i] = number of objects that must come before i.
	inDegree := make([]int, n)
	for i := range edges {
		for j := range edges[i] {
			inDegree[j]++
		}
	}

	var queue []int
	for i, d := range inDegree {
		if d == 0 {
			queue = append(queue, i)
		}
	}

	var sorted []pipeline.IRObject
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		sorted = append(sorted, objects[cur])
		var newlyReady []int
		for j := range edges[cur] {
			inDegree[j]--
			if inDegree[j] == 0 {
				newlyReady = append(newlyReady, j)
			}
		}
		// Sort by original position to make the output deterministic and stable
		// (respects source file order as tiebreaker between independent objects).
		sort.Ints(newlyReady)
		queue = append(queue, newlyReady...)
	}

	if len(sorted) != n {
		// There is a cycle. Detect DEFERRABLE FKs that could break the cycle.
		cycle := findCycle(edges, n)
		if canDefer(objects, cycle) {
			cycleSet := make(map[int]bool, len(cycle))
			for _, i := range cycle {
				cycleSet[i] = true
			}

			// Remove circular FKs from tables in the cycle, collecting them as deferred.
			modified := make([]pipeline.IRObject, len(objects))
			copy(modified, objects)

			for i, obj := range modified {
				if !cycleSet[i] {
					continue
				}
				tbl, ok := obj.(*ir.Table)
				if !ok {
					continue
				}
				var keepConstraints []*ir.Constraint
				for _, cst := range tbl.Constraints {
					if cst.Type == "FOREIGN KEY" && cst.Deferrable {
						ref := extractFKRef(cst.Expr)
						if ref != "" {
							if j, ok := idx[ref]; ok && cycleSet[j] {
								deferred = append(deferred, deferredFK{table: tbl, fk: cst})
								continue
							}
						}
					}
					keepConstraints = append(keepConstraints, cst)
				}
				tblCopy := *tbl
				tblCopy.Constraints = keepConstraints
				modified[i] = &tblCopy
			}

			reResolved, err := (&Resolver{}).Sort(modified)
			if err != nil {
				return objects, nil
			}

			for _, df := range deferred {
				for _, obj := range reResolved {
					if t, ok := obj.(*ir.Table); ok && t.Schema == df.table.Schema && t.Name == df.table.Name {
						t.Constraints = append(t.Constraints, df.fk)
						break
					}
				}
			}
			return reResolved, nil
		}
		members := make([]string, 0, len(cycle))
		for _, i := range cycle {
			members = append(members, objects[i].QualifiedName())
		}
		return nil, pipeline.Errorf(pipeline.SourcePos{}, "circular dependency cycle with no DEFERRABLE FK: %s",
			strings.Join(members, " → "))
	}

	return sorted, nil
}

// extractFKRef extracts the referenced table's qualified name from a FK constraint
// Expr. The Expr looks like `FOREIGN KEY ("col") REFERENCES "schema"."table" ("col2")`.
// Returns the name in the unquoted form used as index keys (e.g. "schema.table" or "table").
func extractFKRef(expr string) string {
	schema, table := extractFKRefParts(expr)
	if table == "" {
		return ""
	}
	if schema != "" {
		return schema + "." + table
	}
	return table
}

// extractFKRefParts splits the FK target into (schema, table). schema is "" when
// the source text wrote an unqualified reference. Quotes around either component
// are stripped.
func extractFKRefParts(expr string) (schema, table string) {
	upper := strings.ToUpper(expr)
	i := strings.Index(upper, "REFERENCES")
	if i < 0 {
		return "", ""
	}
	rest := strings.TrimSpace(expr[i+len("REFERENCES"):])
	ref := unquoteIdent(extractFirstIdent(rest))
	if ref == "" {
		return "", ""
	}
	if dot := strings.Index(ref, "."); dot >= 0 {
		return ref[:dot], ref[dot+1:]
	}
	return "", ref
}

// resolveFKTarget looks up the FK target in idx, falling back to the referencing
// table's own schema when the source wrote an unqualified reference. Returns the
// resolved index key and whether a hit was found.
func resolveFKTarget(idx map[string]int, refSchema, refTable, ownSchema string) (string, bool) {
	if refSchema != "" {
		key := refSchema + "." + refTable
		if _, ok := idx[key]; ok {
			return key, true
		}
		return "", false
	}
	if _, ok := idx[refTable]; ok {
		return refTable, true
	}
	if ownSchema != "" {
		key := ownSchema + "." + refTable
		if _, ok := idx[key]; ok {
			return key, true
		}
	}
	return "", false
}

// extractFirstIdent reads the leading identifier (possibly schema."name" or "schema"."name")
// stopping before the first space or '('.
func extractFirstIdent(s string) string {
	end := strings.IndexAny(s, " \t\n(")
	if end < 0 {
		return s
	}
	return s[:end]
}

// unquoteIdent removes double-quotes from a (possibly schema-qualified) identifier
// and returns the canonical "schema.name" or "name" form used in the dependency index.
func unquoteIdent(s string) string {
	s = strings.ReplaceAll(s, `""`, `"`) // unescape embedded double-quotes
	s = strings.ReplaceAll(s, `"`, "")   // strip delimiter quotes
	return s
}

// findCycle finds nodes involved in a cycle using DFS.
func findCycle(edges []map[int]bool, n int) []int {
	color := make([]int, n) // 0=white, 1=gray, 2=black
	var cycle []int
	var dfs func(v int) bool
	dfs = func(v int) bool {
		color[v] = 1
		for w := range edges[v] {
			if color[w] == 1 {
				cycle = append(cycle, w, v)
				return true
			}
			if color[w] == 0 && dfs(w) {
				return true
			}
		}
		color[v] = 2
		return false
	}
	for i := 0; i < n; i++ {
		if color[i] == 0 {
			if dfs(i) {
				return cycle
			}
		}
	}
	return nil
}

// canDefer returns true if all FK constraints among cycle members are DEFERRABLE.
func canDefer(objects []pipeline.IRObject, cycle []int) bool {
	if len(cycle) == 0 {
		return false
	}
	for _, i := range cycle {
		tbl, ok := objects[i].(*ir.Table)
		if !ok {
			continue
		}
		for _, cst := range tbl.Constraints {
			if cst.Type == "FOREIGN KEY" && !cst.Deferrable {
				return false
			}
		}
	}
	return true
}

// Ensure Resolver implements pipeline.DependencyResolver.
var _ pipeline.DependencyResolver = (*Resolver)(nil)

// suppress unused import
var _ = fmt.Sprintf
