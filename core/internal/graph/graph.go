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
				if col.Type.Schema == "" || col.Type.Schema == "pg_catalog" {
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

		case *ir.OperatorClass:
			schemaEdge(i, o.Schema)

		case *ir.OperatorFamily:
			schemaEdge(i, o.Schema)

		case *ir.StatisticsObject:
			schemaEdge(i, o.Schema)

		case *ir.TSConfig:
			schemaEdge(i, o.Schema)

		case *ir.TSDict:
			schemaEdge(i, o.Schema)

		case *ir.TSParser:
			schemaEdge(i, o.Schema)

		case *ir.TSTemplate:
			schemaEdge(i, o.Schema)
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
