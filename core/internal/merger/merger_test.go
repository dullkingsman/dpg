package merger_test

import (
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/merger"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

var pos = pipeline.SourcePos{File: "a.dpg", Line: 1, Col: 1}
var pos2 = pipeline.SourcePos{File: "b.dpg", Line: 1, Col: 1}
var pos3 = pipeline.SourcePos{File: "c.dpg", Line: 1, Col: 1}

func ptr[T any](v T) *T { return &v }

func mergeAll(t *testing.T, objects ...pipeline.IRObject) ([]pipeline.IRObject, []pipeline.LintDiagnostic) {
	t.Helper()
	m := merger.New()
	out, diags, err := m.Merge(objects)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	return out, diags
}

func merge(t *testing.T, objects ...pipeline.IRObject) []pipeline.IRObject {
	t.Helper()
	out, _ := mergeAll(t, objects...)
	return out
}

// ── No-op: single objects pass through ────────────────────────────────────────

func TestMerge_SingleObjects(t *testing.T) {
	objects := []pipeline.IRObject{
		&ir.Schema{Name: "app", SrcPos: pos},
		&ir.Table{Schema: "app", Name: "users", SrcPos: pos},
	}
	out := merge(t, objects...)
	if len(out) != 2 {
		t.Errorf("expected 2, got %d", len(out))
	}
}

// ── Table merge ───────────────────────────────────────────────────────────────

func TestMerge_Table_ColumnsUnioned(t *testing.T) {
	a := &ir.Table{
		Schema: "app", Name: "users", SrcPos: pos,
		Columns: []*ir.Column{{Name: "id", SrcPos: pos}},
	}
	b := &ir.Table{
		Schema: "app", Name: "users", SrcPos: pos2,
		Columns: []*ir.Column{{Name: "email", SrcPos: pos2}},
	}
	out := merge(t, a, b)
	if len(out) != 1 {
		t.Fatalf("expected 1 merged table, got %d", len(out))
	}
	tbl := out[0].(*ir.Table)
	if len(tbl.Columns) != 2 {
		t.Errorf("Columns: expected 2, got %d", len(tbl.Columns))
	}
	if tbl.Columns[0].Name != "id" || tbl.Columns[1].Name != "email" {
		t.Errorf("column names: got %q, %q", tbl.Columns[0].Name, tbl.Columns[1].Name)
	}
}

func TestMerge_Table_ScalarLastWins(t *testing.T) {
	owner1 := ptr("alice")
	owner2 := ptr("bob")
	a := &ir.Table{Schema: "app", Name: "t", SrcPos: pos, Owner: owner1}
	b := &ir.Table{Schema: "app", Name: "t", SrcPos: pos2, Owner: owner2}
	out := merge(t, a, b)
	tbl := out[0].(*ir.Table)
	if *tbl.Owner != "bob" {
		t.Errorf("Owner (last-wins): got %q", *tbl.Owner)
	}
}

func TestMerge_Table_IndexesUnioned(t *testing.T) {
	a := &ir.Table{
		Schema: "app", Name: "t", SrcPos: pos,
		Indexes: []*ir.Index{{Name: "idx_a", Columns: []pipeline.IndexColumn{{Name: "a"}}}},
	}
	b := &ir.Table{
		Schema: "app", Name: "t", SrcPos: pos2,
		Indexes: []*ir.Index{{Name: "idx_b", Columns: []pipeline.IndexColumn{{Name: "b"}}}},
	}
	out := merge(t, a, b)
	tbl := out[0].(*ir.Table)
	if len(tbl.Indexes) != 2 {
		t.Errorf("Indexes: expected 2, got %d", len(tbl.Indexes))
	}
}

func TestMerge_Table_DuplicateIndexDeduped(t *testing.T) {
	idx := &ir.Index{Name: "idx_same", Columns: []pipeline.IndexColumn{{Name: "x"}}}
	a := &ir.Table{Schema: "app", Name: "t", SrcPos: pos, Indexes: []*ir.Index{idx}}
	b := &ir.Table{Schema: "app", Name: "t", SrcPos: pos2, Indexes: []*ir.Index{idx}}
	out := merge(t, a, b)
	tbl := out[0].(*ir.Table)
	if len(tbl.Indexes) != 1 {
		t.Errorf("Indexes: duplicate should be deduped; got %d", len(tbl.Indexes))
	}
}

func TestMerge_Table_ConstraintsUnioned(t *testing.T) {
	a := &ir.Table{
		Schema: "app", Name: "t", SrcPos: pos,
		Constraints: []*ir.Constraint{{Name: "pk_t", Type: "PRIMARY KEY", Expr: "PRIMARY KEY (id)"}},
	}
	b := &ir.Table{
		Schema: "app", Name: "t", SrcPos: pos2,
		Constraints: []*ir.Constraint{{Name: "uq_t_email", Type: "UNIQUE", Expr: "UNIQUE (email)"}},
	}
	out := merge(t, a, b)
	tbl := out[0].(*ir.Table)
	if len(tbl.Constraints) != 2 {
		t.Errorf("Constraints: expected 2, got %d", len(tbl.Constraints))
	}
}

func TestMerge_Table_ProtectedAndDropCascadeOrred(t *testing.T) {
	a := &ir.Table{Schema: "app", Name: "t", SrcPos: pos, Protected: true}
	b := &ir.Table{Schema: "app", Name: "t", SrcPos: pos2, DropCascade: true}
	out := merge(t, a, b)
	tbl := out[0].(*ir.Table)
	if !tbl.Protected {
		t.Error("Protected should be true")
	}
	if !tbl.DropCascade {
		t.Error("DropCascade should be true")
	}
}

// ── Schema merge ──────────────────────────────────────────────────────────────

func TestMerge_Schema_OwnerLastWins(t *testing.T) {
	a := &ir.Schema{Name: "app", SrcPos: pos, Owner: ptr("alice")}
	b := &ir.Schema{Name: "app", SrcPos: pos2, Owner: ptr("bob")}
	out := merge(t, a, b)
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
	s := out[0].(*ir.Schema)
	if *s.Owner != "bob" {
		t.Errorf("Owner: got %q", *s.Owner)
	}
}

// ── View merge ────────────────────────────────────────────────────────────────

func TestMerge_View_GrantsAppended(t *testing.T) {
	a := &ir.View{
		Schema: "app", Name: "v", SrcPos: pos,
		Grants: []ir.Grant{{Roles: []string{"r1"}, Privileges: []string{"SELECT"}}},
	}
	b := &ir.View{
		Schema: "app", Name: "v", SrcPos: pos2,
		Grants: []ir.Grant{{Roles: []string{"r2"}, Privileges: []string{"SELECT"}}},
	}
	out := merge(t, a, b)
	v := out[0].(*ir.View)
	if len(v.Grants) != 2 {
		t.Errorf("Grants: expected 2, got %d", len(v.Grants))
	}
}

// ── Type (ENUM) merge ─────────────────────────────────────────────────────────

func TestMerge_EnumValuesUnioned(t *testing.T) {
	a := &ir.Type{Schema: "app", Name: "status", Variant: "ENUM", SrcPos: pos, EnumValues: []string{"active", "inactive"}}
	b := &ir.Type{Schema: "app", Name: "status", Variant: "ENUM", SrcPos: pos2, EnumValues: []string{"inactive", "pending"}}
	out := merge(t, a, b)
	tp := out[0].(*ir.Type)
	if len(tp.EnumValues) != 3 {
		t.Errorf("EnumValues: expected 3, got %d: %v", len(tp.EnumValues), tp.EnumValues)
	}
}

// ── Function merge ────────────────────────────────────────────────────────────

func TestMerge_Function_GrantsAppended(t *testing.T) {
	a := &ir.Function{Schema: "app", Name: "f", SrcPos: pos, Grants: []ir.Grant{{Roles: []string{"r1"}, Privileges: []string{"EXECUTE"}}}}
	b := &ir.Function{Schema: "app", Name: "f", SrcPos: pos2, Grants: []ir.Grant{{Roles: []string{"r2"}, Privileges: []string{"EXECUTE"}}}}
	out := merge(t, a, b)
	f := out[0].(*ir.Function)
	if len(f.Grants) != 2 {
		t.Errorf("Grants: expected 2, got %d", len(f.Grants))
	}
}

// ── Different types with same name are separate objects ────────────────────────

func TestMerge_SameNameDifferentType(t *testing.T) {
	tbl := &ir.Table{Schema: "app", Name: "status", SrcPos: pos}
	tp := &ir.Type{Schema: "app", Name: "status", Variant: "ENUM", SrcPos: pos2}
	out := merge(t, tbl, tp)
	if len(out) != 2 {
		t.Errorf("expected 2 objects (different types, same name), got %d", len(out))
	}
}

// ── Column-level merge ────────────────────────────────────────────────────────

func TestMerge_ColumnScalarLastWins(t *testing.T) {
	col := func(comment string, pos pipeline.SourcePos) *ir.Column {
		c := &ir.Column{Name: "email", SrcPos: pos}
		c.Comment = ptr(comment)
		return c
	}
	a := &ir.Table{Schema: "app", Name: "users", SrcPos: pos, Columns: []*ir.Column{col("old", pos)}}
	b := &ir.Table{Schema: "app", Name: "users", SrcPos: pos2, Columns: []*ir.Column{col("new", pos2)}}
	out := merge(t, a, b)
	tbl := out[0].(*ir.Table)
	if *tbl.Columns[0].Comment != "new" {
		t.Errorf("Column.Comment (last-wins): got %q", *tbl.Columns[0].Comment)
	}
}

// ── scalar-merge-conflict detection (RFC Section 3.7) ─────────────────────────────────

func findRule(diags []pipeline.LintDiagnostic, rule string) []pipeline.LintDiagnostic {
	var out []pipeline.LintDiagnostic
	for _, d := range diags {
		if d.Rule == rule {
			out = append(out, d)
		}
	}
	return out
}

func TestMergeTablesOwnerConflict(t *testing.T) {
	a := &ir.Table{Schema: "app", Name: "t", SrcPos: pos, Owner: ptr("alice")}
	b := &ir.Table{Schema: "app", Name: "t", SrcPos: pos2, Owner: ptr("bob")}
	out, diags := mergeAll(t, a, b)

	conflicts := findRule(diags, "scalar-merge-conflict")
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 scalar-merge-conflict diagnostic, got %d: %v", len(conflicts), diags)
	}
	if conflicts[0].IsError {
		t.Error("scalar-merge-conflict should be a warning by default, not an error")
	}

	// Winning value is unaffected by the diagnostic: last-file-wins still applies.
	tbl := out[0].(*ir.Table)
	if *tbl.Owner != "bob" {
		t.Errorf("Owner (last-wins, regardless of conflict): got %q", *tbl.Owner)
	}
}

func TestMergeTablesOwnerNoConflictSameValue(t *testing.T) {
	a := &ir.Table{Schema: "app", Name: "t", SrcPos: pos, Owner: ptr("alice")}
	b := &ir.Table{Schema: "app", Name: "t", SrcPos: pos2, Owner: ptr("alice")}
	_, diags := mergeAll(t, a, b)

	if conflicts := findRule(diags, "scalar-merge-conflict"); len(conflicts) != 0 {
		t.Errorf("identical values across files should not be flagged as a conflict: %v", conflicts)
	}
}

// TestMergeTablesOwnerThreeFileLastSetByTracking proves conflictTracker's
// lastFile bookkeeping is load-bearing, not just "compare to the previous
// file": file1 and file2 agree on Owner, file3 disagrees — the resulting
// diagnostic must cite file2 (the file that most recently set the current
// value) as the source of the conflicting value, not file1 (which set it
// first but was then reconfirmed, not overridden, by file2).
func TestMergeTablesOwnerThreeFileLastSetByTracking(t *testing.T) {
	a := &ir.Table{Schema: "app", Name: "t", SrcPos: pos, Owner: ptr("alice")}
	b := &ir.Table{Schema: "app", Name: "t", SrcPos: pos2, Owner: ptr("alice")}
	c := &ir.Table{Schema: "app", Name: "t", SrcPos: pos3, Owner: ptr("carol")}
	_, diags := mergeAll(t, a, b, c)

	conflicts := findRule(diags, "scalar-merge-conflict")
	if len(conflicts) != 1 {
		t.Fatalf("expected exactly 1 conflict (b agreeing with a is not itself a conflict), got %d: %v", len(conflicts), diags)
	}
	msg := conflicts[0].Message
	if !strings.Contains(msg, pos2.File) {
		t.Errorf("conflict should be attributed to %s (the file that last set the value), got: %s", pos2.File, msg)
	}
	if strings.Contains(msg, "in "+pos.File) {
		t.Errorf("conflict should NOT be attributed to %s (superseded by %s before the real conflict), got: %s", pos.File, pos2.File, msg)
	}
}

func TestMergeProceduresCommentConflict(t *testing.T) {
	a := &ir.Procedure{Schema: "app", Name: "p", SrcPos: pos, Comment: ptr("old")}
	b := &ir.Procedure{Schema: "app", Name: "p", SrcPos: pos2, Comment: ptr("new")}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 1 {
		t.Fatalf("expected 1 conflict, got: %v", diags)
	}
	if *out[0].(*ir.Procedure).Comment != "new" {
		t.Errorf("Comment (last-wins): got %q", *out[0].(*ir.Procedure).Comment)
	}
}

func TestMergeAggregatesCommentConflict(t *testing.T) {
	a := &ir.Aggregate{Schema: "app", Name: "agg", SrcPos: pos, Comment: ptr("old")}
	b := &ir.Aggregate{Schema: "app", Name: "agg", SrcPos: pos2, Comment: ptr("new")}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 1 {
		t.Fatalf("expected 1 conflict, got: %v", diags)
	}
	if *out[0].(*ir.Aggregate).Comment != "new" {
		t.Errorf("Comment (last-wins): got %q", *out[0].(*ir.Aggregate).Comment)
	}
}

func TestMergeSequencesConflict(t *testing.T) {
	a := &ir.Sequence{Schema: "app", Name: "s", SrcPos: pos, IncrementBy: ptr(int64(1))}
	b := &ir.Sequence{Schema: "app", Name: "s", SrcPos: pos2, IncrementBy: ptr(int64(2))}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 1 {
		t.Fatalf("expected 1 conflict, got: %v", diags)
	}
	if *out[0].(*ir.Sequence).IncrementBy != 2 {
		t.Errorf("IncrementBy (last-wins): got %d", *out[0].(*ir.Sequence).IncrementBy)
	}
}

func TestMergeExtensionsVersionConflict(t *testing.T) {
	a := &ir.Extension{Name: "pgcrypto", SrcPos: pos, Version: ptr("1.1")}
	b := &ir.Extension{Name: "pgcrypto", SrcPos: pos2, Version: ptr("1.2")}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 1 {
		t.Fatalf("expected 1 conflict, got: %v", diags)
	}
	if *out[0].(*ir.Extension).Version != "1.2" {
		t.Errorf("Version (last-wins): got %q", *out[0].(*ir.Extension).Version)
	}
}

func TestMergeRolesConnectionLimitConflict(t *testing.T) {
	a := &ir.Role{Name: "app_user", SrcPos: pos, ConnectionLimit: ptr(5)}
	b := &ir.Role{Name: "app_user", SrcPos: pos2, ConnectionLimit: ptr(10)}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 1 {
		t.Fatalf("expected 1 conflict, got: %v", diags)
	}
	if *out[0].(*ir.Role).ConnectionLimit != 10 {
		t.Errorf("ConnectionLimit (last-wins): got %d", *out[0].(*ir.Role).ConnectionLimit)
	}
}

func TestMergeRolesMembershipsAppendedNotConflicted(t *testing.T) {
	a := &ir.Role{Name: "app_user", SrcPos: pos, Memberships: []ir.RoleMembership{{Role: "readonly", Direction: "IN_ROLE"}}}
	b := &ir.Role{Name: "app_user", SrcPos: pos2, Memberships: []ir.RoleMembership{{Role: "readwrite", Direction: "IN_ROLE"}}}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 0 {
		t.Errorf("Memberships is set-valued (RFC audit item #32), not a scalar conflict: %v", diags)
	}
	role := out[0].(*ir.Role)
	if len(role.Memberships) != 2 {
		t.Errorf("Memberships: expected 2 entries from both files, got %d: %+v", len(role.Memberships), role.Memberships)
	}
}

func TestMergeTablespacesLocationConflict(t *testing.T) {
	a := &ir.Tablespace{Name: "fast_ssd", SrcPos: pos, Location: "/data/ssd1"}
	b := &ir.Tablespace{Name: "fast_ssd", SrcPos: pos2, Location: "/data/ssd2"}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 1 {
		t.Fatalf("expected 1 conflict, got: %v", diags)
	}
	if out[0].(*ir.Tablespace).Location != "/data/ssd2" {
		t.Errorf("Location (last-wins): got %q", out[0].(*ir.Tablespace).Location)
	}
}

func TestMergeForeignDataWrappersHandlerConflict(t *testing.T) {
	a := &ir.ForeignDataWrapper{Name: "my_fdw", SrcPos: pos, Handler: "handler_a"}
	b := &ir.ForeignDataWrapper{Name: "my_fdw", SrcPos: pos2, Handler: "handler_b"}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 1 {
		t.Fatalf("expected 1 conflict, got: %v", diags)
	}
	if out[0].(*ir.ForeignDataWrapper).Handler != "handler_b" {
		t.Errorf("Handler (last-wins): got %q", out[0].(*ir.ForeignDataWrapper).Handler)
	}
}

func TestMergeForeignDataWrappersEmptyHandlerNotAConflict(t *testing.T) {
	// "" means NO HANDLER/omitted — a later file saying nothing about
	// Handler must never look like a conflict with an earlier real value,
	// and must never clobber it either.
	a := &ir.ForeignDataWrapper{Name: "my_fdw", SrcPos: pos, Handler: "handler_a"}
	b := &ir.ForeignDataWrapper{Name: "my_fdw", SrcPos: pos2, Handler: ""}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 0 {
		t.Errorf("empty Handler should never be treated as a conflicting value: %v", diags)
	}
	if out[0].(*ir.ForeignDataWrapper).Handler != "handler_a" {
		t.Errorf("Handler: empty value from a later file must not clobber an earlier real value, got %q", out[0].(*ir.ForeignDataWrapper).Handler)
	}
}

func TestMergeForeignServersTypeConflict(t *testing.T) {
	a := &ir.ForeignServer{Name: "srv", SrcPos: pos, FDWName: "postgres_fdw", Type: ptr("primary")}
	b := &ir.ForeignServer{Name: "srv", SrcPos: pos2, FDWName: "postgres_fdw", Type: ptr("replica")}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 1 {
		t.Fatalf("expected 1 conflict, got: %v", diags)
	}
	if *out[0].(*ir.ForeignServer).Type != "replica" {
		t.Errorf("Type (last-wins): got %q", *out[0].(*ir.ForeignServer).Type)
	}
}

func TestMergeUserMappingsOptionsConflict(t *testing.T) {
	a := &ir.UserMapping{User: "alice", Server: "srv", SrcPos: pos,
		Options: []pipeline.StorageParam{{Key: "host", Value: "db1.internal"}}}
	b := &ir.UserMapping{User: "alice", Server: "srv", SrcPos: pos2,
		Options: []pipeline.StorageParam{{Key: "host", Value: "db2.internal"}}}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 1 {
		t.Fatalf("expected 1 conflict, got: %v", diags)
	}
	um := out[0].(*ir.UserMapping)
	if len(um.Options) != 1 || um.Options[0].Value != "db2.internal" {
		t.Errorf("Options[host] (last-wins): got %+v", um.Options)
	}
}

func TestMergeUserMappingsOptionsDistinctKeysNoConflict(t *testing.T) {
	a := &ir.UserMapping{User: "alice", Server: "srv", SrcPos: pos,
		Options: []pipeline.StorageParam{{Key: "host", Value: "db1.internal"}}}
	b := &ir.UserMapping{User: "alice", Server: "srv", SrcPos: pos2,
		Options: []pipeline.StorageParam{{Key: "port", Value: "5432"}}}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 0 {
		t.Errorf("distinct OPTIONS keys across files should never conflict: %v", diags)
	}
	um := out[0].(*ir.UserMapping)
	if len(um.Options) != 2 {
		t.Errorf("Options: expected both keys present, got %+v", um.Options)
	}
}

func TestMergeTypesDomainBaseTypeConflict(t *testing.T) {
	a := &ir.Type{Schema: "app", Name: "positive_int", Variant: "DOMAIN", SrcPos: pos,
		DomainBaseType: ir.TypeRef{Name: "int4"}}
	b := &ir.Type{Schema: "app", Name: "positive_int", Variant: "DOMAIN", SrcPos: pos2,
		DomainBaseType: ir.TypeRef{Name: "int8"}}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 1 {
		t.Fatalf("expected 1 conflict, got: %v", diags)
	}
	if out[0].(*ir.Type).DomainBaseType.Name != "int8" {
		t.Errorf("DomainBaseType (last-wins): got %q", out[0].(*ir.Type).DomainBaseType.Name)
	}
}

func TestMergeTypesDomainDefaultAndNotNullNotConflicted(t *testing.T) {
	// NotNull is OR-merged like Table's Protected/DropCascade booleans, not
	// last-wins — two files disagreeing (false vs true) isn't a conflict,
	// it's an accumulation, matching every other OR-merged bool in this file.
	a := &ir.Type{Schema: "app", Name: "d", Variant: "DOMAIN", SrcPos: pos, DomainNotNull: false}
	b := &ir.Type{Schema: "app", Name: "d", Variant: "DOMAIN", SrcPos: pos2, DomainNotNull: true}
	out, diags := mergeAll(t, a, b)
	if len(findRule(diags, "scalar-merge-conflict")) != 0 {
		t.Errorf("DomainNotNull is OR-merged, not conflict-checked: %v", diags)
	}
	if !out[0].(*ir.Type).DomainNotNull {
		t.Error("DomainNotNull should be true (OR-merged)")
	}
}
