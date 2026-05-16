package examples_test

// TestLinterInAction shows the built-in linter catching common mistakes:
//   - deprecated table (warn)
//   - hardcoded password in column default (error)
//   - SECURITY DEFINER function without SET search_path (warn)
//
// Run:
//
//	go test ./examples/... -v -run TestLinter

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

func TestLinterInAction(t *testing.T) {
	objects := compileDPG(t, "fixtures/linting/schema.dpg")

	linter, ok := pipeline.Resolve[pipeline.Linter](pipeline.Default, pipeline.KeyLinter)
	if !ok {
		t.Fatal("linter not registered in pipeline.Default")
	}

	cfg := pipeline.LinterConfig{
		WarnOnDeprecated:         true,
		ForbidHardcodedPasswords: true,
	}
	diags, err := linter.Lint(objects, cfg)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	t.Logf("\n=== Linter diagnostics for linting/schema.dpg ===")
	for _, d := range diags {
		level := "warn"
		if d.IsError {
			level = "error"
		}
		t.Logf("  %s [%s] %s:%d — %s", level, d.Rule, d.Pos.File, d.Pos.Line, d.Message)
	}

	if len(diags) == 0 {
		t.Fatal("expected lint diagnostics, got none")
	}

	ruleSet := make(map[string]bool)
	for _, d := range diags {
		ruleSet[d.Rule] = true
	}

	if !ruleSet["deprecated"] {
		t.Error("expected 'deprecated' diagnostic for legacy_sessions table")
	}
	if !ruleSet["hardcoded-password"] {
		t.Error("expected 'hardcoded-password' diagnostic for service_accounts.password")
	}
	if !ruleSet["security-definer-search-path"] {
		t.Error("expected 'security-definer-search-path' diagnostic for unsafe_get_user")
	}
}

// TestLinterErrorVsWarning distinguishes errors (block the migration) from
// warnings (printed to stderr but don't stop the plan).
func TestLinterErrorVsWarning(t *testing.T) {
	objects := compileDPG(t, "fixtures/linting/schema.dpg")

	linter, ok := pipeline.Resolve[pipeline.Linter](pipeline.Default, pipeline.KeyLinter)
	if !ok {
		t.Fatal("linter not registered")
	}

	diags, _ := linter.Lint(objects, pipeline.LinterConfig{
		WarnOnDeprecated:         true,
		ForbidHardcodedPasswords: true,
	})

	var errors, warnings []pipeline.LintDiagnostic
	for _, d := range diags {
		if d.IsError {
			errors = append(errors, d)
		} else {
			warnings = append(warnings, d)
		}
	}

	t.Logf("\n=== Errors vs Warnings ===")
	t.Logf("Errors (%d):", len(errors))
	for _, d := range errors {
		t.Logf("  [%s] %s", d.Rule, d.Message)
	}
	t.Logf("Warnings (%d):", len(warnings))
	for _, d := range warnings {
		t.Logf("  [%s] %s", d.Rule, d.Message)
	}

	if len(errors) == 0 {
		t.Error("expected at least one error-level diagnostic (hardcoded-password)")
	}
	if len(warnings) == 0 {
		t.Error("expected at least one warning-level diagnostic (deprecated, security-definer-search-path)")
	}
}

// TestLinterCleanSchema verifies that a well-formed schema produces no diagnostics.
func TestLinterCleanSchema(t *testing.T) {
	objects := compileDPG(t, "fixtures/v1/schema.dpg")

	linter, ok := pipeline.Resolve[pipeline.Linter](pipeline.Default, pipeline.KeyLinter)
	if !ok {
		t.Fatal("linter not registered")
	}

	diags, err := linter.Lint(objects, pipeline.LinterConfig{
		WarnOnDeprecated:         true,
		ForbidHardcodedPasswords: true,
	})
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	t.Logf("\n=== Linter on clean schema (v1) ===")
	if len(diags) == 0 {
		t.Log("  (no diagnostics — schema is clean)")
	} else {
		for _, d := range diags {
			t.Logf("  [%s] %s", d.Rule, d.Message)
		}
		t.Errorf("expected 0 diagnostics from clean schema, got %d", len(diags))
	}
}

// TestLinterRequireColumnComments shows the optional RequireColumnComments rule
// that enforces every column has a COMMENT directive in its block.
func TestLinterRequireColumnComments(t *testing.T) {
	objects := compileDPG(t, "fixtures/v1/schema.dpg")

	linter, ok := pipeline.Resolve[pipeline.Linter](pipeline.Default, pipeline.KeyLinter)
	if !ok {
		t.Fatal("linter not registered")
	}

	diags, err := linter.Lint(objects, pipeline.LinterConfig{
		RequireColumnComments: true,
	})
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	t.Logf("\n=== Require-column-comments rule on v1/schema.dpg ===")
	if len(diags) == 0 {
		t.Log("  all columns already have comments")
		return
	}
	t.Logf("  columns missing comments (%d):", len(diags))
	seen := make(map[string]bool)
	for _, d := range diags {
		key := fmt.Sprintf("[%s] %s", d.Rule, d.Message)
		if !seen[key] {
			t.Logf("  warn  %s", key)
			seen[key] = true
		}
	}

	// Every diagnostic should be a require-column-comments warning.
	for _, d := range diags {
		if d.Rule != "require-column-comments" {
			t.Errorf("unexpected rule %q", d.Rule)
		}
		if strings.Contains(d.Rule, "error") {
			t.Errorf("require-column-comments should be a warning, not an error")
		}
	}
}
