package linter

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func TestLintClean(t *testing.T) {
	l := New()
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "users"},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 {
		t.Errorf("expected no diags, got %d", len(diags))
	}
}

func TestLintDeprecatedTable(t *testing.T) {
	l := New()
	reason := "use accounts instead"
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "users", Deprecated: &reason},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) == 0 {
		t.Fatal("expected deprecated warning")
	}
	if diags[0].Rule != "deprecated" {
		t.Errorf("expected deprecated rule, got %s", diags[0].Rule)
	}
}

func TestLintMaxColumns(t *testing.T) {
	l := New()
	cols := make([]*ir.Column, 5)
	for i := range cols {
		cols[i] = &ir.Column{Name: "c", Type: ir.TypeRef{Name: "text"}}
	}
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "wide", Columns: cols},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{MaxColumnsPerTable: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) == 0 {
		t.Fatal("expected max-columns error")
	}
	if !diags[0].IsError {
		t.Errorf("expected IsError=true")
	}
	if diags[0].Rule != "max-columns" {
		t.Errorf("expected max-columns rule, got %s", diags[0].Rule)
	}
}

func TestLintRequireColumnComments(t *testing.T) {
	l := New()
	comment := "the email address"
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "users", Columns: []*ir.Column{
			{Name: "id", Type: ir.TypeRef{Name: "integer"}, Comment: nil},
			{Name: "email", Type: ir.TypeRef{Name: "text"}, Comment: &comment},
		}},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{RequireColumnComments: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "require-column-comments" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected require-column-comments warning for id column")
	}
	// email has a comment: no warning for it.
	warnCount := 0
	for _, d := range diags {
		if d.Rule == "require-column-comments" {
			warnCount++
		}
	}
	if warnCount != 1 {
		t.Errorf("expected 1 require-column-comments warning, got %d", warnCount)
	}
}

func TestLintHardcodedPassword(t *testing.T) {
	l := New()
	def := "'secret123'"
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "users", Columns: []*ir.Column{
			{Name: "password_hash", Type: ir.TypeRef{Name: "text"}, Default: &def},
		}},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{ForbidHardcodedPasswords: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "hardcoded-password" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected hardcoded-password error")
	}
}

func TestLintHardcodedRolePassword(t *testing.T) {
	l := New()
	pw := "hunter2"
	objects := []pipeline.IRObject{&ir.Role{Name: "app_service", Password: &pw}}
	diags, err := l.Lint(objects, pipeline.LinterConfig{ForbidHardcodedPasswords: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "hardcoded-role-password" {
			found = true
			if !d.IsError {
				t.Error("expected hardcoded ROLE PASSWORD to be an error, not a warning (RFC §11.1 MUST)")
			}
		}
	}
	if !found {
		t.Fatal("expected hardcoded-role-password error for a literal ROLE PASSWORD")
	}
}

func TestLintRolePasswordWithSecretReferenceOK(t *testing.T) {
	l := New()
	pw := "{{vault:secret/roles/app_service#pw}}"
	objects := []pipeline.IRObject{&ir.Role{Name: "app_service", Password: &pw}}
	diags, err := l.Lint(objects, pipeline.LinterConfig{ForbidHardcodedPasswords: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Rule == "hardcoded-role-password" {
			t.Errorf("did not expect hardcoded-role-password for a {{...}} secret reference, got: %v", d)
		}
	}
}

func TestLintRolePasswordRuleDisabled(t *testing.T) {
	l := New()
	pw := "hunter2"
	objects := []pipeline.IRObject{&ir.Role{Name: "app_service", Password: &pw}}
	diags, err := l.Lint(objects, pipeline.LinterConfig{ForbidHardcodedPasswords: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Rule == "hardcoded-role-password" {
			t.Errorf("did not expect hardcoded-role-password with the rule disabled, got: %v", d)
		}
	}
}

func TestLintHardcodedFDWPassword(t *testing.T) {
	l := New()
	objects := []pipeline.IRObject{&ir.UserMapping{
		User: "app", Server: "srv",
		Body: "CREATE USER MAPPING FOR app SERVER srv OPTIONS (user 'app', password 'hunter2')",
	}}
	diags, err := l.Lint(objects, pipeline.LinterConfig{ForbidHardcodedPasswords: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "hardcoded-fdw-password" {
			found = true
			if !d.IsError {
				t.Error("expected hardcoded FDW password to be an error (RFC §19.1 hardcoded_fdw_password)")
			}
		}
	}
	if !found {
		t.Fatal("expected hardcoded-fdw-password error for a literal OPTIONS password")
	}
}

func TestLintFDWPasswordWithSecretReferenceOK(t *testing.T) {
	l := New()
	objects := []pipeline.IRObject{&ir.UserMapping{
		User: "app", Server: "srv",
		Body: "CREATE USER MAPPING FOR app SERVER srv OPTIONS (user 'app', password '{{vault:secret/fdw/srv#pw}}')",
	}}
	diags, err := l.Lint(objects, pipeline.LinterConfig{ForbidHardcodedPasswords: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Rule == "hardcoded-fdw-password" {
			t.Errorf("did not expect hardcoded-fdw-password for a {{...}} secret reference, got: %v", d)
		}
	}
}

func TestLintFDWPasswordRuleDisabled(t *testing.T) {
	l := New()
	objects := []pipeline.IRObject{&ir.UserMapping{
		User: "app", Server: "srv",
		Body: "CREATE USER MAPPING FOR app SERVER srv OPTIONS (password 'hunter2')",
	}}
	diags, err := l.Lint(objects, pipeline.LinterConfig{ForbidHardcodedPasswords: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Rule == "hardcoded-fdw-password" {
			t.Errorf("did not expect hardcoded-fdw-password with the rule disabled, got: %v", d)
		}
	}
}

func TestLintUserMappingNoPasswordOptionOK(t *testing.T) {
	l := New()
	objects := []pipeline.IRObject{&ir.UserMapping{
		User: "app", Server: "srv",
		Body: "CREATE USER MAPPING FOR app SERVER srv OPTIONS (user 'app')",
	}}
	diags, err := l.Lint(objects, pipeline.LinterConfig{ForbidHardcodedPasswords: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Rule == "hardcoded-fdw-password" {
			t.Errorf("did not expect hardcoded-fdw-password for a mapping with no password option, got: %v", d)
		}
	}
}

func TestLintSecurityDefiner(t *testing.T) {
	l := New()
	objects := []pipeline.IRObject{
		&ir.Function{
			Schema: "public",
			Name:   "do_thing",
			Attrs: ir.FuncAttrs{
				Language:    "plpgsql",
				SecurityDef: true,
				Body:        "BEGIN END;",
			},
		},
	}
	diags, err := l.Lint(objects, pipeline.LinterConfig{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "security-definer-search-path" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected security-definer-search-path warning")
	}
}

func TestLintRegistration(t *testing.T) {
	l, ok := pipeline.Resolve[pipeline.Linter](pipeline.Default, pipeline.KeyLinter)
	if !ok {
		t.Fatal("Linter not registered")
	}
	if l == nil {
		t.Fatal("registered Linter is nil")
	}
}
