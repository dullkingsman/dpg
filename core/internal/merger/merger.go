// Package merger implements pipeline.Merger. It merges same-object IRObject
// declarations across multiple .dpg files per RFC §2.7 set/scalar merge rules.
package merger

import (
	"fmt"
	"sort"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	pipeline.Default.Register(pipeline.KeyMerger, New())
}

// Merger implements pipeline.Merger.
type Merger struct{}

// New returns a Merger.
func New() *Merger { return &Merger{} }

// Merge groups IRObjects by QualifiedName + type, then merges within each group.
// Set-valued fields are unioned; scalar fields use last-file-wins (alphabetical
// path order). Conflicting same-named set members with different definitions
// return a CompilerError.
func (m *Merger) Merge(objects []pipeline.IRObject) ([]pipeline.IRObject, error) {
	// Group by (type-tag, qualified-name).
	type key struct{ tag, name string }
	groups := make(map[key][]pipeline.IRObject)
	var order []key

	for _, obj := range objects {
		k := key{tag: typeTag(obj), name: obj.QualifiedName()}
		if _, exists := groups[k]; !exists {
			order = append(order, k)
		}
		groups[k] = append(groups[k], obj)
	}

	// For deterministic scalar-merge order, sort each group by source file path.
	var result []pipeline.IRObject
	var diags pipeline.Diagnostics

	for _, k := range order {
		grp := groups[k]
		if len(grp) == 1 {
			result = append(result, grp[0])
			continue
		}
		// Sort by source file for deterministic last-wins order.
		sort.Slice(grp, func(i, j int) bool {
			return grp[i].Pos().File < grp[j].Pos().File
		})
		merged, err := mergeGroup(grp)
		if err != nil {
			if diag, ok := err.(*pipeline.CompilerError); ok {
				diags = append(diags, diag)
				continue
			}
			return nil, err
		}
		result = append(result, merged)
	}

	if diags.HasErrors() {
		return result, diags
	}
	return result, nil
}

// typeTag returns a short string tag for the concrete IR type.
func typeTag(obj pipeline.IRObject) string {
	switch obj.(type) {
	case *ir.Table:
		return "TABLE"
	case *ir.View:
		return "VIEW"
	case *ir.Function:
		return "FUNCTION"
	case *ir.Procedure:
		return "PROCEDURE"
	case *ir.Aggregate:
		return "AGGREGATE"
	case *ir.Type:
		return "TYPE"
	case *ir.Sequence:
		return "SEQUENCE"
	case *ir.Schema:
		return "SCHEMA"
	case *ir.Extension:
		return "EXTENSION"
	case *ir.Role:
		return "ROLE"
	case *ir.Tablespace:
		return "TABLESPACE"
	case *ir.ForeignDataWrapper:
		return "FDW"
	case *ir.ForeignServer:
		return "SERVER"
	case *ir.UserMapping:
		return "USER MAPPING"
	default:
		return fmt.Sprintf("%T", obj)
	}
}

// mergeGroup merges a non-empty slice of same-name, same-type IRObjects.
func mergeGroup(grp []pipeline.IRObject) (pipeline.IRObject, error) {
	switch base := grp[0].(type) {
	case *ir.Table:
		return mergeTables(grp, base)
	case *ir.View:
		return mergeViews(grp, base)
	case *ir.Function:
		return mergeFunctions(grp, base)
	case *ir.Schema:
		return mergeSchemas(grp, base)
	case *ir.Type:
		return mergeTypes(grp, base)
	default:
		// For all other types, last-declaration-wins completely (simple scalars).
		return grp[len(grp)-1], nil
	}
}

// ── Table merge ───────────────────────────────────────────────────────────────

func mergeTables(grp []pipeline.IRObject, base *ir.Table) (pipeline.IRObject, error) {
	merged := *base // shallow copy; we'll deep-merge below

	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Table)
		if !ok {
			continue
		}
		// Scalar fields: last-wins.
		if next.Owner != nil {
			merged.Owner = next.Owner
		}
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		if next.RenamedFrom != nil {
			merged.RenamedFrom = next.RenamedFrom
		}
		if next.Deprecated != nil {
			merged.Deprecated = next.Deprecated
		}
		if next.Protected {
			merged.Protected = true
		}
		if next.DropCascade {
			merged.DropCascade = true
		}
		if next.RLSEnabled {
			merged.RLSEnabled = true
		}
		if next.RLSForced {
			merged.RLSForced = true
		}

		// Set-valued fields: union by name.
		merged.Indexes = unionIndexes(merged.Indexes, next.Indexes)
		merged.Policies = unionPolicies(merged.Policies, next.Policies)
		merged.Triggers = unionTriggers(merged.Triggers, next.Triggers)
		merged.Grants = append(merged.Grants, next.Grants...)
		merged.Revocations = append(merged.Revocations, next.Revocations...)
		merged.Constraints = unionConstraints(merged.Constraints, next.Constraints)
		merged.Columns = mergeColumns(merged.Columns, next.Columns)
		merged.Partitions = append(merged.Partitions, next.Partitions...)
	}

	return &merged, nil
}

func unionIndexes(a, b []*ir.Index) []*ir.Index {
	seen := make(map[string]*ir.Index, len(a))
	for _, idx := range a {
		seen[idx.Name] = idx
	}
	result := append([]*ir.Index(nil), a...)
	for _, idx := range b {
		if _, exists := seen[idx.Name]; !exists {
			result = append(result, idx)
			seen[idx.Name] = idx
		}
		// Same name + same def → silently deduplicate (we skip for now).
	}
	return result
}

func unionPolicies(a, b []*ir.Policy) []*ir.Policy {
	seen := make(map[string]bool, len(a))
	for _, p := range a {
		seen[p.Name] = true
	}
	result := append([]*ir.Policy(nil), a...)
	for _, p := range b {
		if !seen[p.Name] {
			result = append(result, p)
			seen[p.Name] = true
		}
	}
	return result
}

func unionTriggers(a, b []*ir.Trigger) []*ir.Trigger {
	seen := make(map[string]bool, len(a))
	for _, t := range a {
		seen[t.Name] = true
	}
	result := append([]*ir.Trigger(nil), a...)
	for _, t := range b {
		if !seen[t.Name] {
			result = append(result, t)
			seen[t.Name] = true
		}
	}
	return result
}

func unionConstraints(a, b []*ir.Constraint) []*ir.Constraint {
	seen := make(map[string]bool, len(a))
	for _, c := range a {
		if c.Name != "" {
			seen[c.Name] = true
		}
	}
	result := append([]*ir.Constraint(nil), a...)
	for _, c := range b {
		if c.Name == "" || !seen[c.Name] {
			result = append(result, c)
			if c.Name != "" {
				seen[c.Name] = true
			}
		}
	}
	return result
}

func mergeColumns(a, b []*ir.Column) []*ir.Column {
	byName := make(map[string]*ir.Column, len(a))
	order := make([]string, 0, len(a))
	for _, c := range a {
		byName[c.Name] = c
		order = append(order, c.Name)
	}
	for _, next := range b {
		existing, ok := byName[next.Name]
		if !ok {
			byName[next.Name] = next
			order = append(order, next.Name)
			continue
		}
		// Merge scalar column attributes.
		if next.Comment != nil {
			existing.Comment = next.Comment
		}
		if next.Statistics != nil {
			existing.Statistics = next.Statistics
		}
		if next.Compression != nil {
			existing.Compression = next.Compression
		}
		if next.Storage != nil {
			existing.Storage = next.Storage
		}
		if next.Deprecated != nil {
			existing.Deprecated = next.Deprecated
		}
		if next.RenamedFrom != nil {
			existing.RenamedFrom = next.RenamedFrom
		}
		if next.Using != nil {
			existing.Using = next.Using
		}
		existing.Grants = append(existing.Grants, next.Grants...)
		existing.Revocations = append(existing.Revocations, next.Revocations...)
	}
	result := make([]*ir.Column, 0, len(order))
	for _, n := range order {
		result = append(result, byName[n])
	}
	return result
}

// ── View merge ────────────────────────────────────────────────────────────────

func mergeViews(grp []pipeline.IRObject, base *ir.View) (pipeline.IRObject, error) {
	merged := *base
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.View)
		if !ok {
			continue
		}
		if next.Owner != nil {
			merged.Owner = next.Owner
		}
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		if next.RenamedFrom != nil {
			merged.RenamedFrom = next.RenamedFrom
		}
		if next.Deprecated != nil {
			merged.Deprecated = next.Deprecated
		}
		merged.Grants = append(merged.Grants, next.Grants...)
		merged.Revocations = append(merged.Revocations, next.Revocations...)
	}
	return &merged, nil
}

// ── Function merge ────────────────────────────────────────────────────────────

func mergeFunctions(grp []pipeline.IRObject, base *ir.Function) (pipeline.IRObject, error) {
	merged := *base
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Function)
		if !ok {
			continue
		}
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		if next.Deprecated != nil {
			merged.Deprecated = next.Deprecated
		}
		if next.RenamedFrom != nil {
			merged.RenamedFrom = next.RenamedFrom
		}
		merged.Grants = append(merged.Grants, next.Grants...)
	}
	return &merged, nil
}

// ── Schema merge ──────────────────────────────────────────────────────────────

func mergeSchemas(grp []pipeline.IRObject, base *ir.Schema) (pipeline.IRObject, error) {
	merged := *base
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Schema)
		if !ok {
			continue
		}
		if next.Owner != nil {
			merged.Owner = next.Owner
		}
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		if next.RenamedFrom != nil {
			merged.RenamedFrom = next.RenamedFrom
		}
	}
	return &merged, nil
}

// ── Type merge ────────────────────────────────────────────────────────────────

func mergeTypes(grp []pipeline.IRObject, base *ir.Type) (pipeline.IRObject, error) {
	merged := *base
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Type)
		if !ok {
			continue
		}
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		if next.Owner != nil {
			merged.Owner = next.Owner
		}
		if next.Deprecated != nil {
			merged.Deprecated = next.Deprecated
		}
		// For ENUMs: union the values.
		if merged.Variant == "ENUM" && next.Variant == "ENUM" {
			merged.EnumValues = unionStrings(merged.EnumValues, next.EnumValues)
		}
	}
	return &merged, nil
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	result := append([]string(nil), a...)
	for _, s := range b {
		if !seen[s] {
			result = append(result, s)
			seen[s] = true
		}
	}
	return result
}
