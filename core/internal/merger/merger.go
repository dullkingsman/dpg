// Package merger implements pipeline.Merger. It merges same-object IRObject
// declarations across multiple .dpg files per RFC Section 3.7 set/scalar merge rules.
package merger

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"github.com/thec1oud/dpg/internal/ir"
	"github.com/thec1oud/dpg/internal/pipeline"
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
// return a CompilerError. The second return value holds "scalar-merge-conflict"
// diagnostics (RFC Section 19.1) surfaced along the way — see pipeline.Merger's doc
// comment for why gating is the caller's job, not this function's.
func (m *Merger) Merge(objects []pipeline.IRObject) ([]pipeline.IRObject, []pipeline.LintDiagnostic, error) {
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
	var mergeDiags []pipeline.LintDiagnostic

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
		merged, groupDiags, err := mergeGroup(grp)
		mergeDiags = append(mergeDiags, groupDiags...)
		if err != nil {
			if diag, ok := err.(*pipeline.CompilerError); ok {
				diags = append(diags, diag)
				continue
			}
			return nil, mergeDiags, err
		}
		result = append(result, merged)
	}

	if diags.HasErrors() {
		return result, mergeDiags, diags
	}
	return result, mergeDiags, nil
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

// objDescFor returns a short, lowercase, human-readable description of obj
// for use in a scalar-merge-conflict diagnostic message (e.g. "table
// public.users", "foreign data wrapper my_fdw") — distinct from typeTag's
// grouping-key tag, which favors brevity over readability.
func objDescFor(obj pipeline.IRObject) string {
	switch o := obj.(type) {
	case *ir.Table:
		return "table " + o.QualifiedName()
	case *ir.View:
		return "view " + o.QualifiedName()
	case *ir.Function:
		return "function " + o.QualifiedName()
	case *ir.Procedure:
		return "procedure " + o.QualifiedName()
	case *ir.Aggregate:
		return "aggregate " + o.QualifiedName()
	case *ir.Type:
		if o.Variant == "DOMAIN" {
			return "domain " + o.QualifiedName()
		}
		return "type " + o.QualifiedName()
	case *ir.Sequence:
		return "sequence " + o.QualifiedName()
	case *ir.Schema:
		return "schema " + o.QualifiedName()
	case *ir.Extension:
		return "extension " + o.QualifiedName()
	case *ir.Role:
		return "role " + o.QualifiedName()
	case *ir.Tablespace:
		return "tablespace " + o.QualifiedName()
	case *ir.ForeignDataWrapper:
		return "foreign data wrapper " + o.QualifiedName()
	case *ir.ForeignServer:
		return "server " + o.QualifiedName()
	case *ir.UserMapping:
		return "user mapping " + o.QualifiedName()
	default:
		return fmt.Sprintf("%T %s", obj, obj.QualifiedName())
	}
}

// mergeGroup merges a non-empty slice of same-name, same-type IRObjects.
func mergeGroup(grp []pipeline.IRObject) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	switch base := grp[0].(type) {
	case *ir.Table:
		return mergeTables(grp, base)
	case *ir.View:
		return mergeViews(grp, base)
	case *ir.Function:
		return mergeFunctions(grp, base)
	case *ir.Procedure:
		return mergeProcedures(grp, base)
	case *ir.Aggregate:
		return mergeAggregates(grp, base)
	case *ir.Schema:
		return mergeSchemas(grp, base)
	case *ir.Type:
		return mergeTypes(grp, base)
	case *ir.Sequence:
		return mergeSequences(grp, base)
	case *ir.Extension:
		return mergeExtensions(grp, base)
	case *ir.Role:
		return mergeRoles(grp, base)
	case *ir.Tablespace:
		return mergeTablespaces(grp, base)
	case *ir.ForeignDataWrapper:
		return mergeForeignDataWrappers(grp, base)
	case *ir.ForeignServer:
		return mergeForeignServers(grp, base)
	case *ir.UserMapping:
		return mergeUserMappings(grp, base)
	default:
		// For all other types, last-declaration-wins completely (simple scalars).
		// No conflictTracker is used here — these are the opaque/reconstruction-
		// tier kinds (Publication, EventTrigger, Cast, TS*, ...) never scoped
		// into this workstream; adding conflict detection for them is future work.
		return grp[len(grp)-1], nil, nil
	}
}

// ── scalar-merge-conflict tracking ──────────────────────────────────────────

// conflictTracker implements RFC Section 3.7's scalar-merge-conflict detection: for
// every "last file wins" scalar property this package merges, it records
// which file most recently supplied the property's current value, and emits
// a LintDiagnostic whenever a later file supplies a genuinely different
// value for the same property. Winning-value semantics are unaffected — the
// merge functions always keep applying last-file-wins regardless of what the
// tracker reports; this only adds visibility, matching RFC Section 3.7's own
// wording ("the compiler SHOULD emit a LintDiagnostic... The winning value
// is used regardless").
//
// baseFile is grp[0]'s source file. The first-processed object's own
// declared values are never checked against anything (nothing precedes
// them), but they still "own" whichever property they set until a later
// file's differing value is compared, so attributedFile falls back to
// baseFile for any property not yet recorded in lastFile — this is what
// lets a later conflict correctly cite the base file as the original
// source of a value nobody has overridden yet.
type conflictTracker struct {
	objDesc  string
	baseFile string
	lastFile map[string]string
	diags    []pipeline.LintDiagnostic
}

func newConflictTracker(objDesc, baseFile string) *conflictTracker {
	return &conflictTracker{objDesc: objDesc, baseFile: baseFile, lastFile: make(map[string]string)}
}

func (t *conflictTracker) attributedFile(property string) string {
	if f, ok := t.lastFile[property]; ok {
		return f
	}
	return t.baseFile
}

func (t *conflictTracker) conflict(property, curFmt, nextFmt, file string, pos pipeline.SourcePos) {
	t.diags = append(t.diags, pipeline.LintDiagnostic{
		Pos:  pos,
		Rule: "scalar-merge-conflict",
		Message: fmt.Sprintf("%s: %s set to %s in %s, overridden by %s from %s",
			t.objDesc, property, curFmt, t.attributedFile(property), nextFmt, file),
	})
}

// checkString compares cur (the currently-merged value) against next (the
// value the file being processed declares), recording a conflict when both
// are set and differ, then always notes file as the property's new last
// setter — even when the values agree, so that a THIRD file's later
// conflict is correctly attributed to this file, not whichever one set the
// property first (see TestMergeTablesOwnerThreeFileLastSetByTracking).
func (t *conflictTracker) checkString(property string, cur, next *string, file string, pos pipeline.SourcePos) {
	if next == nil {
		return
	}
	if cur != nil && *cur != *next {
		t.conflict(property, strconv.Quote(*cur), strconv.Quote(*next), file, pos)
	}
	t.lastFile[property] = file
}

func (t *conflictTracker) checkBool(property string, cur, next *bool, file string, pos pipeline.SourcePos) {
	if next == nil {
		return
	}
	if cur != nil && *cur != *next {
		t.conflict(property, strconv.FormatBool(*cur), strconv.FormatBool(*next), file, pos)
	}
	t.lastFile[property] = file
}

func (t *conflictTracker) checkInt64(property string, cur, next *int64, file string, pos pipeline.SourcePos) {
	if next == nil {
		return
	}
	if cur != nil && *cur != *next {
		t.conflict(property, strconv.FormatInt(*cur, 10), strconv.FormatInt(*next, 10), file, pos)
	}
	t.lastFile[property] = file
}

func (t *conflictTracker) checkInt(property string, cur, next *int, file string, pos pipeline.SourcePos) {
	if next == nil {
		return
	}
	if cur != nil && *cur != *next {
		t.conflict(property, strconv.Itoa(*cur), strconv.Itoa(*next), file, pos)
	}
	t.lastFile[property] = file
}

// checkTypeRef is checkString's ir.TypeRef-typed sibling, for DOMAIN's
// BaseType (a comparable struct, not a pointer — the zero value IS "unset"
// for this field, since Schema=="" && Name=="" can never be a real type).
func (t *conflictTracker) checkTypeRef(property string, cur, next ir.TypeRef, file string, pos pipeline.SourcePos) {
	if next.Name == "" {
		return
	}
	if cur.Name != "" && cur != next {
		t.conflict(property, strconv.Quote(cur.String()), strconv.Quote(next.String()), file, pos)
	}
	t.lastFile[property] = file
}

// checkStorageParams merges next into cur, key by key: each OPTIONS key is
// its own scalar property (addressed as "prefix[key]" in diagnostics), so
// two files setting the same key to different values is exactly the same
// class of conflict as two files disagreeing on Owner — just addressed by
// key instead of by field name. Order-preserving: an existing key's
// position is kept; a new key is appended in the order its file declares it.
func (t *conflictTracker) checkStorageParams(propertyPrefix string, cur, next []pipeline.StorageParam, file string, pos pipeline.SourcePos) []pipeline.StorageParam {
	result := append([]pipeline.StorageParam(nil), cur...)
	index := make(map[string]int, len(result))
	for i, p := range result {
		index[p.Key] = i
	}
	for _, p := range next {
		property := propertyPrefix + "[" + p.Key + "]"
		if i, ok := index[p.Key]; ok {
			curVal, nextVal := result[i].Value, p.Value
			t.checkString(property, &curVal, &nextVal, file, pos)
			result[i].Value = p.Value
		} else {
			t.lastFile[property] = file
			result = append(result, p)
			index[p.Key] = len(result) - 1
		}
	}
	return result
}

// ── Table merge ───────────────────────────────────────────────────────────────

func mergeTables(grp []pipeline.IRObject, base *ir.Table) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base // shallow copy; we'll deep-merge below
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)

	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Table)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		// Scalar fields: last-wins, conflict-tracked.
		tracker.checkString("owner", merged.Owner, next.Owner, file, pos)
		if next.Owner != nil {
			merged.Owner = next.Owner
		}
		tracker.checkString("comment", merged.Comment, next.Comment, file, pos)
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		tracker.checkString("renamed from", merged.RenamedFrom, next.RenamedFrom, file, pos)
		if next.RenamedFrom != nil {
			merged.RenamedFrom = next.RenamedFrom
		}
		tracker.checkString("deprecated", merged.Deprecated, next.Deprecated, file, pos)
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
		var err error
		merged.Indexes, err = unionIndexes(merged.Indexes, next.Indexes)
		if err != nil {
			return nil, nil, err
		}
		merged.Policies, err = unionPolicies(merged.Policies, next.Policies)
		if err != nil {
			return nil, nil, err
		}
		merged.Triggers, err = unionTriggers(merged.Triggers, next.Triggers)
		if err != nil {
			return nil, nil, err
		}
		merged.Grants = append(merged.Grants, next.Grants...)
		merged.Revocations = append(merged.Revocations, next.Revocations...)
		merged.Constraints, err = unionConstraints(merged.Constraints, next.Constraints)
		if err != nil {
			return nil, nil, err
		}
		merged.Columns = mergeColumns(merged.Columns, next.Columns)
		merged.Partitions = append(merged.Partitions, next.Partitions...)
	}

	return &merged, tracker.diags, nil
}

// equalIgnoringPos reports whether two set-valued members are identical set
// down to every field except Pos (source position necessarily differs
// between the two files declaring "the same" member) — RFC Section 3.7:
// "Identical duplicate entries (same name AND same definition) are silently
// deduplicated. Entries with the same name but different definitions are a
// compiler error (DPG-E005, conflicting set member)."
func equalIgnoringPos[T any](a, b T, clearPos func(*T)) bool {
	ac, bc := a, b
	clearPos(&ac)
	clearPos(&bc)
	return reflect.DeepEqual(ac, bc)
}

func unionIndexes(a, b []*ir.Index) ([]*ir.Index, error) {
	seen := make(map[string]*ir.Index, len(a))
	for _, idx := range a {
		seen[idx.Name] = idx
	}
	result := append([]*ir.Index(nil), a...)
	for _, idx := range b {
		existing, exists := seen[idx.Name]
		if !exists {
			result = append(result, idx)
			seen[idx.Name] = idx
			continue
		}
		if !equalIgnoringPos(*existing, *idx, func(x *ir.Index) { x.Pos = pipeline.SourcePos{} }) {
			return nil, pipeline.ErrorfCode(idx.Pos, "DPG-E005",
				"index %q declared with conflicting definitions across files", idx.Name)
		}
		// Same name + same def → silently deduplicate.
	}
	return result, nil
}

func unionPolicies(a, b []*ir.Policy) ([]*ir.Policy, error) {
	seen := make(map[string]*ir.Policy, len(a))
	for _, p := range a {
		seen[p.Name] = p
	}
	result := append([]*ir.Policy(nil), a...)
	for _, p := range b {
		existing, exists := seen[p.Name]
		if !exists {
			result = append(result, p)
			seen[p.Name] = p
			continue
		}
		if !equalIgnoringPos(*existing, *p, func(x *ir.Policy) { x.Pos = pipeline.SourcePos{} }) {
			return nil, pipeline.ErrorfCode(p.Pos, "DPG-E005",
				"policy %q declared with conflicting definitions across files", p.Name)
		}
	}
	return result, nil
}

func unionTriggers(a, b []*ir.Trigger) ([]*ir.Trigger, error) {
	seen := make(map[string]*ir.Trigger, len(a))
	for _, t := range a {
		seen[t.Name] = t
	}
	result := append([]*ir.Trigger(nil), a...)
	for _, t := range b {
		existing, exists := seen[t.Name]
		if !exists {
			result = append(result, t)
			seen[t.Name] = t
			continue
		}
		if !equalIgnoringPos(*existing, *t, func(x *ir.Trigger) { x.Pos = pipeline.SourcePos{} }) {
			return nil, pipeline.ErrorfCode(t.Pos, "DPG-E005",
				"trigger %q declared with conflicting definitions across files", t.Name)
		}
	}
	return result, nil
}

func unionConstraints(a, b []*ir.Constraint) ([]*ir.Constraint, error) {
	seen := make(map[string]*ir.Constraint, len(a))
	for _, c := range a {
		if c.Name != "" {
			seen[c.Name] = c
		}
	}
	result := append([]*ir.Constraint(nil), a...)
	for _, c := range b {
		existing, exists := seen[c.Name]
		if c.Name == "" || !exists {
			result = append(result, c)
			if c.Name != "" {
				seen[c.Name] = c
			}
			continue
		}
		if !equalIgnoringPos(*existing, *c, func(x *ir.Constraint) { x.Pos = pipeline.SourcePos{} }) {
			return nil, pipeline.ErrorfCode(c.Pos, "DPG-E005",
				"constraint %q declared with conflicting definitions across files", c.Name)
		}
	}
	return result, nil
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

func mergeViews(grp []pipeline.IRObject, base *ir.View) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.View)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		tracker.checkString("owner", merged.Owner, next.Owner, file, pos)
		if next.Owner != nil {
			merged.Owner = next.Owner
		}
		tracker.checkString("comment", merged.Comment, next.Comment, file, pos)
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		tracker.checkString("renamed from", merged.RenamedFrom, next.RenamedFrom, file, pos)
		if next.RenamedFrom != nil {
			merged.RenamedFrom = next.RenamedFrom
		}
		tracker.checkString("deprecated", merged.Deprecated, next.Deprecated, file, pos)
		if next.Deprecated != nil {
			merged.Deprecated = next.Deprecated
		}
		merged.Grants = append(merged.Grants, next.Grants...)
		merged.Revocations = append(merged.Revocations, next.Revocations...)
	}
	return &merged, tracker.diags, nil
}

// ── Function merge ────────────────────────────────────────────────────────────

func mergeFunctions(grp []pipeline.IRObject, base *ir.Function) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Function)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		tracker.checkString("comment", merged.Comment, next.Comment, file, pos)
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		tracker.checkString("deprecated", merged.Deprecated, next.Deprecated, file, pos)
		if next.Deprecated != nil {
			merged.Deprecated = next.Deprecated
		}
		tracker.checkString("renamed from", merged.RenamedFrom, next.RenamedFrom, file, pos)
		if next.RenamedFrom != nil {
			merged.RenamedFrom = next.RenamedFrom
		}
		merged.Grants = append(merged.Grants, next.Grants...)
	}
	return &merged, tracker.diags, nil
}

// ── Procedure merge ───────────────────────────────────────────────────────────

func mergeProcedures(grp []pipeline.IRObject, base *ir.Procedure) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Procedure)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		tracker.checkString("comment", merged.Comment, next.Comment, file, pos)
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		merged.Grants = append(merged.Grants, next.Grants...)
		merged.Revocations = append(merged.Revocations, next.Revocations...)
	}
	return &merged, tracker.diags, nil
}

// ── Aggregate merge ───────────────────────────────────────────────────────────

func mergeAggregates(grp []pipeline.IRObject, base *ir.Aggregate) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Aggregate)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		tracker.checkString("comment", merged.Comment, next.Comment, file, pos)
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		merged.Grants = append(merged.Grants, next.Grants...)
	}
	return &merged, tracker.diags, nil
}

// ── Schema merge ──────────────────────────────────────────────────────────────

func mergeSchemas(grp []pipeline.IRObject, base *ir.Schema) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Schema)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		tracker.checkString("owner", merged.Owner, next.Owner, file, pos)
		if next.Owner != nil {
			merged.Owner = next.Owner
		}
		tracker.checkString("comment", merged.Comment, next.Comment, file, pos)
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		tracker.checkString("renamed from", merged.RenamedFrom, next.RenamedFrom, file, pos)
		if next.RenamedFrom != nil {
			merged.RenamedFrom = next.RenamedFrom
		}
	}
	return &merged, tracker.diags, nil
}

// ── Type merge ────────────────────────────────────────────────────────────────

func mergeTypes(grp []pipeline.IRObject, base *ir.Type) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Type)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		tracker.checkString("comment", merged.Comment, next.Comment, file, pos)
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		tracker.checkString("owner", merged.Owner, next.Owner, file, pos)
		if next.Owner != nil {
			merged.Owner = next.Owner
		}
		tracker.checkString("deprecated", merged.Deprecated, next.Deprecated, file, pos)
		if next.Deprecated != nil {
			merged.Deprecated = next.Deprecated
		}
		// For ENUMs: union the values.
		if merged.Variant == "ENUM" && next.Variant == "ENUM" {
			merged.EnumValues = unionStrings(merged.EnumValues, next.EnumValues)
		}
		// For DOMAINs: RFC Section 5.4's structured diffing inputs are themselves
		// scalar properties (a domain's base type / default / not-null are
		// each set once, same as Owner/Comment), so they get the same
		// conflict-tracked last-wins treatment — bundled into this
		// workstream per the approved plan, same file/same mechanical
		// pattern as the rest of this function.
		if merged.Variant == "DOMAIN" && next.Variant == "DOMAIN" {
			tracker.checkTypeRef("domain base type", merged.DomainBaseType, next.DomainBaseType, file, pos)
			if next.DomainBaseType.Name != "" {
				merged.DomainBaseType = next.DomainBaseType
			}
			tracker.checkString("domain default", merged.DomainDefault, next.DomainDefault, file, pos)
			if next.DomainDefault != nil {
				merged.DomainDefault = next.DomainDefault
			}
			if next.DomainNotNull {
				merged.DomainNotNull = true
			}
			var err error
			merged.DomainConstraints, err = unionConstraints(merged.DomainConstraints, next.DomainConstraints)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return &merged, tracker.diags, nil
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

// ── Sequence merge ────────────────────────────────────────────────────────────

func mergeSequences(grp []pipeline.IRObject, base *ir.Sequence) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Sequence)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		tracker.checkString("owner", merged.Owner, next.Owner, file, pos)
		if next.Owner != nil {
			merged.Owner = next.Owner
		}
		tracker.checkString("comment", merged.Comment, next.Comment, file, pos)
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		tracker.checkInt64("increment by", merged.IncrementBy, next.IncrementBy, file, pos)
		if next.IncrementBy != nil {
			merged.IncrementBy = next.IncrementBy
		}
		tracker.checkInt64("min value", merged.MinValue, next.MinValue, file, pos)
		if next.MinValue != nil {
			merged.MinValue = next.MinValue
		}
		tracker.checkInt64("max value", merged.MaxValue, next.MaxValue, file, pos)
		if next.MaxValue != nil {
			merged.MaxValue = next.MaxValue
		}
		tracker.checkInt64("start value", merged.StartValue, next.StartValue, file, pos)
		if next.StartValue != nil {
			merged.StartValue = next.StartValue
		}
		tracker.checkInt64("cache", merged.Cache, next.Cache, file, pos)
		if next.Cache != nil {
			merged.Cache = next.Cache
		}
		tracker.checkBool("cycle", merged.Cycle, next.Cycle, file, pos)
		if next.Cycle != nil {
			merged.Cycle = next.Cycle
		}
		merged.Grants = append(merged.Grants, next.Grants...)
	}
	return &merged, tracker.diags, nil
}

// ── Extension merge ───────────────────────────────────────────────────────────

func mergeExtensions(grp []pipeline.IRObject, base *ir.Extension) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Extension)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		tracker.checkString("schema", merged.Schema, next.Schema, file, pos)
		if next.Schema != nil {
			merged.Schema = next.Schema
		}
		tracker.checkString("version", merged.Version, next.Version, file, pos)
		if next.Version != nil {
			merged.Version = next.Version
		}
	}
	return &merged, tracker.diags, nil
}

// ── Role merge ────────────────────────────────────────────────────────────────

func mergeRoles(grp []pipeline.IRObject, base *ir.Role) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Role)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		tracker.checkBool("login", merged.CanLogin, next.CanLogin, file, pos)
		if next.CanLogin != nil {
			merged.CanLogin = next.CanLogin
		}
		tracker.checkBool("superuser", merged.Superuser, next.Superuser, file, pos)
		if next.Superuser != nil {
			merged.Superuser = next.Superuser
		}
		tracker.checkBool("createdb", merged.CreateDB, next.CreateDB, file, pos)
		if next.CreateDB != nil {
			merged.CreateDB = next.CreateDB
		}
		tracker.checkBool("createrole", merged.CreateRole, next.CreateRole, file, pos)
		if next.CreateRole != nil {
			merged.CreateRole = next.CreateRole
		}
		tracker.checkBool("inherit", merged.Inherit, next.Inherit, file, pos)
		if next.Inherit != nil {
			merged.Inherit = next.Inherit
		}
		tracker.checkBool("replication", merged.IsReplication, next.IsReplication, file, pos)
		if next.IsReplication != nil {
			merged.IsReplication = next.IsReplication
		}
		tracker.checkBool("bypassrls", merged.BypassRLS, next.BypassRLS, file, pos)
		if next.BypassRLS != nil {
			merged.BypassRLS = next.BypassRLS
		}
		tracker.checkInt("connection limit", merged.ConnectionLimit, next.ConnectionLimit, file, pos)
		if next.ConnectionLimit != nil {
			merged.ConnectionLimit = next.ConnectionLimit
		}
		tracker.checkString("password", merged.Password, next.Password, file, pos)
		if next.Password != nil {
			merged.Password = next.Password
		}
		tracker.checkString("valid until", merged.ValidUntil, next.ValidUntil, file, pos)
		if next.ValidUntil != nil {
			merged.ValidUntil = next.ValidUntil
		}
		tracker.checkString("comment", merged.Comment, next.Comment, file, pos)
		if next.Comment != nil {
			merged.Comment = next.Comment
		}

		// Role-membership entries are additive across files (RFC Section 3.7's
		// set-valued treatment — the point of splitting a role's memberships
		// across files is usually to combine them, not to have one file's
		// list silently discarded), matching Grants' identical simple-append
		// convention elsewhere in this function (see e.g. mergeTablespaces).
		merged.Memberships = append(merged.Memberships, next.Memberships...)
		merged.Configs = append(merged.Configs, next.Configs...)
	}
	return &merged, tracker.diags, nil
}

// ── Tablespace merge ──────────────────────────────────────────────────────────

func mergeTablespaces(grp []pipeline.IRObject, base *ir.Tablespace) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.Tablespace)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		curLocation := &merged.Location
		nextLocation := &next.Location
		tracker.checkString("location", curLocation, nextLocation, file, pos)
		merged.Location = next.Location

		tracker.checkString("comment", merged.Comment, next.Comment, file, pos)
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
	}
	return &merged, tracker.diags, nil
}

// ── ForeignDataWrapper merge ──────────────────────────────────────────────────

func mergeForeignDataWrappers(grp []pipeline.IRObject, base *ir.ForeignDataWrapper) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.ForeignDataWrapper)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		// Handler/Validator: "" means NO HANDLER/NO VALIDATOR or omitted —
		// both mean the same thing to PostgreSQL (see the field doc comment
		// on ir.ForeignDataWrapper), so a later file's "" can never override
		// an earlier file's real value, and never counts as a conflict.
		tracker.checkString("handler", strPtrIfSet(merged.Handler), strPtrIfSet(next.Handler), file, pos)
		if next.Handler != "" {
			merged.Handler = next.Handler
		}
		tracker.checkString("validator", strPtrIfSet(merged.Validator), strPtrIfSet(next.Validator), file, pos)
		if next.Validator != "" {
			merged.Validator = next.Validator
		}
		tracker.checkString("comment", merged.Comment, next.Comment, file, pos)
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		merged.Options = tracker.checkStorageParams("options", merged.Options, next.Options, file, pos)
	}
	return &merged, tracker.diags, nil
}

// ── ForeignServer merge ───────────────────────────────────────────────────────

func mergeForeignServers(grp []pipeline.IRObject, base *ir.ForeignServer) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.ForeignServer)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		// FDWName is mandatory in real PG grammar (never legitimately ""),
		// so unlike Handler/Validator above there is no "unset" case to
		// special-case here — any later file names a real wrapper.
		tracker.checkString("foreign data wrapper", strPtrIfSet(merged.FDWName), strPtrIfSet(next.FDWName), file, pos)
		if next.FDWName != "" {
			merged.FDWName = next.FDWName
		}
		tracker.checkString("type", merged.Type, next.Type, file, pos)
		if next.Type != nil {
			merged.Type = next.Type
		}
		tracker.checkString("version", merged.Version, next.Version, file, pos)
		if next.Version != nil {
			merged.Version = next.Version
		}
		tracker.checkString("comment", merged.Comment, next.Comment, file, pos)
		if next.Comment != nil {
			merged.Comment = next.Comment
		}
		merged.Options = tracker.checkStorageParams("options", merged.Options, next.Options, file, pos)
	}
	return &merged, tracker.diags, nil
}

// ── UserMapping merge ─────────────────────────────────────────────────────────

func mergeUserMappings(grp []pipeline.IRObject, base *ir.UserMapping) (pipeline.IRObject, []pipeline.LintDiagnostic, error) {
	merged := *base
	tracker := newConflictTracker(objDescFor(base), base.Pos().File)
	for _, obj := range grp[1:] {
		next, ok := obj.(*ir.UserMapping)
		if !ok {
			continue
		}
		file, pos := next.Pos().File, next.Pos()

		// UserMapping has no metadata scalar fields (no Comment/Owner) —
		// Options is its only mergeable property.
		merged.Options = tracker.checkStorageParams("options", merged.Options, next.Options, file, pos)
	}
	return &merged, tracker.diags, nil
}

// strPtrIfSet returns nil for "" and &s otherwise, letting a plain (non-
// pointer) string field reuse conflictTracker.checkString's pointer-based
// "unset" convention without a dedicated non-pointer variant.
func strPtrIfSet(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
