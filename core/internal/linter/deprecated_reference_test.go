package linter

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func hasRule(diags []pipeline.LintDiagnostic, rule string) bool {
	for _, d := range diags {
		if d.Rule == rule {
			return true
		}
	}
	return false
}

// TestLintDeprecatedReferenceFKTable proves a non-deprecated table's FK to a
// deprecated table fires the rule.
func TestLintDeprecatedReferenceFKTable(t *testing.T) {
	l := New()
	reason := "use accounts instead"
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "users", Deprecated: &reason},
		&ir.Table{
			Schema: "public", Name: "orders",
			Constraints: []*ir.Constraint{
				{Type: "FOREIGN KEY", Name: "fk_user", RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}},
			},
		},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(diags, "deprecated-reference") {
		t.Fatalf("expected deprecated-reference warning, got: %+v", diags)
	}
}

// TestLintDeprecatedReferenceFKTableUnqualifiedResolvesOwnSchema proves an
// unqualified FK target (RefSchema=="") resolves against the referencing
// table's own schema, not left unmatched.
func TestLintDeprecatedReferenceFKTableUnqualifiedResolvesOwnSchema(t *testing.T) {
	l := New()
	reason := "gone"
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "app", Name: "legacy", Deprecated: &reason},
		&ir.Table{
			Schema: "app", Name: "orders",
			Constraints: []*ir.Constraint{
				{Type: "FOREIGN KEY", Name: "fk_legacy", RefTable: "legacy", RefColumns: []string{"id"}},
			},
		},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(diags, "deprecated-reference") {
		t.Fatalf("expected deprecated-reference warning, got: %+v", diags)
	}
}

// TestLintDeprecatedReferenceFKDeprecatedTableOwnFKNoFire is the key negative
// case: a DEPRECATED table's own FK to another deprecated table must NOT
// fire — the rule is specifically about a non-deprecated object referencing
// a deprecated one.
func TestLintDeprecatedReferenceFKDeprecatedTableOwnFKNoFire(t *testing.T) {
	l := New()
	reason := "gone"
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "users", Deprecated: &reason},
		&ir.Table{
			Schema: "public", Name: "orders", Deprecated: &reason,
			Constraints: []*ir.Constraint{
				{Type: "FOREIGN KEY", Name: "fk_user", RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}},
			},
		},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if hasRule(diags, "deprecated-reference") {
		t.Fatalf("did not expect deprecated-reference warning from a deprecated table's own FK, got: %+v", diags)
	}
}

// TestLintDeprecatedReferenceFKColumn proves an FK referencing a deprecated
// COLUMN (on an otherwise non-deprecated table) also fires.
func TestLintDeprecatedReferenceFKColumn(t *testing.T) {
	l := New()
	reason := "renamed to uuid"
	objects := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "users",
			Columns: []*ir.Column{{Name: "legacy_id", Type: ir.TypeRef{Name: "int4"}, Deprecated: &reason}},
		},
		&ir.Table{
			Schema: "public", Name: "orders",
			Constraints: []*ir.Constraint{
				{Type: "FOREIGN KEY", Name: "fk_user", RefSchema: "public", RefTable: "users", RefColumns: []string{"legacy_id"}},
			},
		},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(diags, "deprecated-reference") {
		t.Fatalf("expected deprecated-reference warning, got: %+v", diags)
	}
}

// TestLintDeprecatedReferenceColumnType proves a non-deprecated column
// typed as a deprecated custom TYPE fires.
func TestLintDeprecatedReferenceColumnType(t *testing.T) {
	l := New()
	reason := "use status_v2"
	objects := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "status", Variant: "ENUM", EnumValues: []string{"a"}, Deprecated: &reason},
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns: []*ir.Column{{Name: "state", Type: ir.TypeRef{Schema: "public", Name: "status"}}},
		},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(diags, "deprecated-reference") {
		t.Fatalf("expected deprecated-reference warning, got: %+v", diags)
	}
}

// TestLintDeprecatedReferenceColumnTypeDeprecatedColumnNoFire is the
// column-type negative case: a column that's ITSELF deprecated referencing
// a deprecated type must not also fire the reference rule (its own
// "deprecated" diagnostic already covers it).
func TestLintDeprecatedReferenceColumnTypeDeprecatedColumnNoFire(t *testing.T) {
	l := New()
	reason := "use status_v2"
	objects := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "status", Variant: "ENUM", EnumValues: []string{"a"}, Deprecated: &reason},
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns: []*ir.Column{{Name: "state", Type: ir.TypeRef{Schema: "public", Name: "status"}, Deprecated: &reason}},
		},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if hasRule(diags, "deprecated-reference") {
		t.Fatalf("did not expect deprecated-reference warning from an already-deprecated column, got: %+v", diags)
	}
}

// TestLintDeprecatedReferenceColumnTypeBuiltinNoFire proves an ordinary
// built-in type (pg_catalog) never falsely matches, even though an
// unqualified reference resolves against the referencing table's own
// schema the same way a real custom-type reference would.
func TestLintDeprecatedReferenceColumnTypeBuiltinNoFire(t *testing.T) {
	l := New()
	objects := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns: []*ir.Column{{Name: "note", Type: ir.TypeRef{Schema: "pg_catalog", Name: "text"}}},
		},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if hasRule(diags, "deprecated-reference") {
		t.Fatalf("did not expect deprecated-reference warning for a built-in type, got: %+v", diags)
	}
}

// TestLintDeprecatedReferenceFunctionArgType proves a non-deprecated
// function's parameter typed as a deprecated custom TYPE fires.
func TestLintDeprecatedReferenceFunctionArgType(t *testing.T) {
	l := New()
	reason := "use status_v2"
	objects := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "status", Variant: "ENUM", EnumValues: []string{"a"}, Deprecated: &reason},
		&ir.Function{
			Schema: "public", Name: "set_status",
			Args: []ir.FuncArg{{Name: "s", Type: ir.TypeRef{Name: "status"}}}, // unqualified, resolves to public
		},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(diags, "deprecated-reference") {
		t.Fatalf("expected deprecated-reference warning, got: %+v", diags)
	}
}

// TestLintDeprecatedReferenceFunctionReturnType proves a non-deprecated
// function's return type referencing a deprecated custom TYPE fires, and
// that a DEPRECATED function itself is correctly exempted (gating check
// bundled here since it's the cheapest place to prove both halves).
func TestLintDeprecatedReferenceFunctionReturnType(t *testing.T) {
	l := New()
	reason := "use status_v2"
	baseObjects := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "status", Variant: "ENUM", EnumValues: []string{"a"}, Deprecated: &reason},
	}

	fn := &ir.Function{
		Schema: "public", Name: "get_status",
		ReturnType: ir.TypeRef{Schema: "public", Name: "status"},
	}
	l1 := New()
	diags, err := l1.Lint(append(baseObjects, fn), pipeline.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(diags, "deprecated-reference") {
		t.Fatalf("expected deprecated-reference warning for return type, got: %+v", diags)
	}

	fnDeprecated := &ir.Function{
		Schema: "public", Name: "get_status_old",
		ReturnType: ir.TypeRef{Schema: "public", Name: "status"},
		Deprecated: &reason,
	}
	diags2, err := l.Lint(append(baseObjects, fnDeprecated), pipeline.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if hasRule(diags2, "deprecated-reference") {
		t.Fatalf("did not expect deprecated-reference warning from an already-deprecated function, got: %+v", diags2)
	}
}

// TestLintDeprecatedReferenceProcedureArgType proves the Procedure arm
// (which has no Deprecated field of its own to gate on) still fires for a
// parameter typed as a deprecated custom TYPE.
func TestLintDeprecatedReferenceProcedureArgType(t *testing.T) {
	l := New()
	reason := "use status_v2"
	objects := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "status", Variant: "ENUM", EnumValues: []string{"a"}, Deprecated: &reason},
		&ir.Procedure{
			Schema: "public", Name: "apply_status",
			Args: []ir.FuncArg{{Name: "s", Type: ir.TypeRef{Schema: "public", Name: "status"}}},
		},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(diags, "deprecated-reference") {
		t.Fatalf("expected deprecated-reference warning, got: %+v", diags)
	}
}

// TestLintDeprecatedReferenceGatedByWarnOnDeprecated proves the base
// WarnOnDeprecated config flag suppresses the rule entirely when false,
// even though a real reference to a deprecated table exists.
func TestLintDeprecatedReferenceGatedByWarnOnDeprecated(t *testing.T) {
	l := New()
	reason := "gone"
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "users", Deprecated: &reason},
		&ir.Table{
			Schema: "public", Name: "orders",
			Constraints: []*ir.Constraint{
				{Type: "FOREIGN KEY", Name: "fk_user", RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}},
			},
		},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{WarnOnDeprecated: false})
	if err != nil {
		t.Fatal(err)
	}
	if hasRule(diags, "deprecated-reference") {
		t.Fatalf("did not expect deprecated-reference warning with WarnOnDeprecated=false, got: %+v", diags)
	}
}

// TestLintDeprecatedReferenceRuleSeverityOverrideOff proves [linter.rules]
// can independently suppress deprecated-reference even with the base
// WarnOnDeprecated flag on, mirroring TestLintRuleSeverityOverrideOff for
// the base "deprecated" rule.
func TestLintDeprecatedReferenceRuleSeverityOverrideOff(t *testing.T) {
	l := New()
	reason := "gone"
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "users", Deprecated: &reason},
		&ir.Table{
			Schema: "public", Name: "orders",
			Constraints: []*ir.Constraint{
				{Type: "FOREIGN KEY", Name: "fk_user", RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}},
			},
		},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{
		WarnOnDeprecated: true,
		Rules:            map[string]string{"deprecated-reference": "off"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasRule(diags, "deprecated-reference") {
		t.Fatalf("did not expect deprecated-reference warning with rule overridden off, got: %+v", diags)
	}
}
