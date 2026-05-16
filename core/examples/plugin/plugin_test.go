// Package plugin_test demonstrates how to register a custom linter extension
// using only the public github.com/dullkingsman/dpg/pkg/dpg API.
//
// No internal packages are imported — everything required to build and register
// a custom pipeline stage is available through pkg/dpg.
//
// Run:
//
//	go test ./examples/plugin/... -v
package plugin_test

import (
	"fmt"
	"testing"

	"github.com/dullkingsman/dpg/pkg/dpg"
)

// tableCommentLinter warns whenever a table has no COMMENT directive.
// It implements the dpg.Linter interface.
type tableCommentLinter struct{}

func (l *tableCommentLinter) Lint(objects []dpg.IRObject, _ dpg.LinterConfig) ([]dpg.LintDiagnostic, error) {
	var diags []dpg.LintDiagnostic
	for _, obj := range objects {
		t, ok := obj.(*dpg.Table)
		if !ok {
			continue
		}
		if t.Comment == nil {
			diags = append(diags, dpg.LintDiagnostic{
				Pos:     t.SrcPos,
				Rule:    "require-table-comment",
				Message: fmt.Sprintf("table %s has no COMMENT directive", t.QualifiedName()),
			})
		}
	}
	return diags, nil
}

// TestCustomLinter registers a custom linter that replaces the built-in one
// and verifies it flags the table without a comment.
func TestCustomLinter(t *testing.T) {
	objects, err := dpg.Compile([]string{"testdata/schema.dpg"}, ".")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	// Save the built-in linter and restore it after the test so other tests
	// in the binary are not affected by this override.
	original, _ := dpg.ResolveLinter(dpg.Default)
	dpg.Default.Register(dpg.KeyLinter, &tableCommentLinter{})
	t.Cleanup(func() { dpg.Default.Register(dpg.KeyLinter, original) })

	diags, err := dpg.Lint(objects, dpg.LinterConfig{})
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	for _, d := range diags {
		t.Logf("[%s] %s — %s", d.Rule, d.Pos, d.Message)
	}

	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic (for 'users' table), got %d", len(diags))
	}
	if diags[0].Rule != "require-table-comment" {
		t.Errorf("unexpected rule %q", diags[0].Rule)
	}
}

// TestChainLinter shows how to augment the built-in linter rather than replace
// it, using dpg.NewChainLinter.
func TestChainLinter(t *testing.T) {
	objects, err := dpg.Compile([]string{"testdata/schema.dpg"}, ".")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	builtin, ok := dpg.ResolveLinter(dpg.Default)
	if !ok {
		t.Fatal("built-in linter not registered")
	}

	chained := dpg.NewChainLinter(builtin, &tableCommentLinter{})
	diags, err := chained.Lint(objects, dpg.LinterConfig{WarnOnDeprecated: true})
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	t.Logf("chained linter found %d diagnostics:", len(diags))
	for _, d := range diags {
		t.Logf("  [%s] %s", d.Rule, d.Message)
	}

	hasCustomRule := false
	for _, d := range diags {
		if d.Rule == "require-table-comment" {
			hasCustomRule = true
		}
	}
	if !hasCustomRule {
		t.Error("expected require-table-comment from custom linter")
	}
}
