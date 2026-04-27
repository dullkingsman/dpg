// Package graph implements pipeline.DependencyResolver. It performs
// topological sort using Kahn's algorithm and resolves circular FK dependencies
// via DEFERRABLE constraints.
package graph

import (
	"fmt"
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

	// Build index: qualifiedName → position.
	idx := make(map[string]int, n)
	for i, obj := range objects {
		idx[obj.QualifiedName()] = i
	}

	// Build adjacency: edges[i] = set of j that i depends on (j must come before i).
	edges := make([]map[int]bool, n)
	for i := range edges {
		edges[i] = make(map[int]bool)
	}

	// Circular FK edges that can be deferred.
	type deferredFK struct {
		table *ir.Table
		fk    *ir.Constraint
	}
	var deferred []deferredFK

	addEdge := func(from, to int) {
		if from != to {
			edges[from][to] = true
		}
	}

	for i, obj := range objects {
		switch o := obj.(type) {
		case *ir.Table:
			// FK constraints → referenced table.
			for _, cst := range o.Constraints {
				if cst.Type == "FOREIGN KEY" && cst.Name != "" {
					// Try to identify the referenced table from the constraint Expr.
					// The Expr for FK is the raw text like "REFERENCES foo (id)".
					ref := extractFKRef(cst.Expr)
					if ref != "" {
						if j, ok := idx[ref]; ok {
							addEdge(i, j)
						}
					}
				}
			}
			// Columns referencing custom types.
			for _, col := range o.Columns {
				if col.Type.Schema != "" && col.Type.Schema != "pg_catalog" {
					typeKey := col.Type.Schema + "." + col.Type.Name
					if j, ok := idx[typeKey]; ok {
						addEdge(i, j)
					}
				}
			}
		case *ir.View:
			// Heuristic: all tables must precede all views (query AST analysis deferred).
			for j, dep := range objects {
				if j != i {
					if _, ok := dep.(*ir.Table); ok {
						addEdge(i, j)
					}
				}
			}
		case *ir.Function:
			_ = o
		}
	}

	// Kahn's algorithm.
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
		for j := range edges[cur] {
			inDegree[j]--
			if inDegree[j] == 0 {
				queue = append(queue, j)
			}
		}
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
								// This is a circular FK — defer it.
								deferred = append(deferred, deferredFK{table: tbl, fk: cst})
								continue
							}
						}
					}
					keepConstraints = append(keepConstraints, cst)
				}
				// Create a modified copy of the table without the circular FK.
				tblCopy := *tbl
				tblCopy.Constraints = keepConstraints
				modified[i] = &tblCopy
			}

			// Re-sort without the circular FKs.
			reResolved, err := (&Resolver{}).Sort(modified)
			if err != nil {
				// Still a cycle after removing circular FKs — fall back to original order.
				return objects, nil
			}

			// Add the deferred FKs back to their tables in the sorted result so the
			// differ can generate the ALTER TABLE ... ADD CONSTRAINT statements.
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

// extractFKRef extracts the referenced table name from a FK constraint Expr text.
// The Expr looks like "FOREIGN KEY (col) REFERENCES schema.table (col2)".
func extractFKRef(expr string) string {
	upper := strings.ToUpper(expr)
	idx := strings.Index(upper, "REFERENCES")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(expr[idx+len("REFERENCES"):])
	// rest now starts with the table name, possibly schema-qualified.
	parts := strings.Fields(rest)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
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
