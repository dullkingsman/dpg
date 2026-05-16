// Package linter implements pipeline.Linter. It runs built-in lint rules
// over the merged IR and returns diagnostics.
package linter

import (
	"fmt"
	"strings"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	pipeline.Default.Register(pipeline.KeyLinter, New())
}

// BuiltinLinter implements pipeline.Linter.
type BuiltinLinter struct{}

// New returns a BuiltinLinter.
func New() *BuiltinLinter { return &BuiltinLinter{} }

// Lint checks objects against all enabled rules.
func (l *BuiltinLinter) Lint(objects []pipeline.IRObject, cfg pipeline.LinterConfig) ([]pipeline.LintDiagnostic, error) {
	var diags []pipeline.LintDiagnostic

	for _, obj := range objects {
		diags = append(diags, checkObject(obj, cfg)...)
	}

	return diags, nil
}

func checkObject(obj pipeline.IRObject, cfg pipeline.LinterConfig) []pipeline.LintDiagnostic {
	var diags []pipeline.LintDiagnostic

	switch o := obj.(type) {
	case *ir.Table:
		diags = append(diags, checkTable(o, cfg)...)
	case *ir.Function:
		diags = append(diags, checkFunction(o, cfg)...)
	case *ir.View:
		diags = append(diags, checkView(o, cfg)...)
	default:
		_ = o
	}

	return diags
}

// ── Table rules ───────────────────────────────────────────────────────────────

func checkTable(t *ir.Table, cfg pipeline.LinterConfig) []pipeline.LintDiagnostic {
	var diags []pipeline.LintDiagnostic
	pos := t.SrcPos

	// DEPRECATED warning.
	if cfg.WarnOnDeprecated && t.Deprecated != nil {
		diags = append(diags, pipeline.LintDiagnostic{
			Pos:     pos,
			Rule:    "deprecated",
			Message: fmt.Sprintf("table %s is deprecated: %s", t.QualifiedName(), *t.Deprecated),
		})
	}

	// Max columns.
	if cfg.MaxColumnsPerTable > 0 && len(t.Columns) > cfg.MaxColumnsPerTable {
		diags = append(diags, pipeline.LintDiagnostic{
			Pos:     pos,
			Rule:    "max-columns",
			Message: fmt.Sprintf("table %s has %d columns (max %d)", t.QualifiedName(), len(t.Columns), cfg.MaxColumnsPerTable),
			IsError: true,
		})
	}

	for _, col := range t.Columns {
		// Require column comments.
		if cfg.RequireColumnComments && col.Comment == nil {
			diags = append(diags, pipeline.LintDiagnostic{
				Pos:     col.SrcPos,
				Rule:    "require-column-comments",
				Message: fmt.Sprintf("column %s.%s has no comment", t.QualifiedName(), col.Name),
			})
		}
		// Deprecated column.
		if cfg.WarnOnDeprecated && col.Deprecated != nil {
			diags = append(diags, pipeline.LintDiagnostic{
				Pos:     col.SrcPos,
				Rule:    "deprecated",
				Message: fmt.Sprintf("column %s.%s is deprecated: %s", t.QualifiedName(), col.Name, *col.Deprecated),
			})
		}
		// Hardcoded passwords: look for default values that look like password strings.
		if cfg.ForbidHardcodedPasswords && col.Default != nil {
			if looksLikePassword(col.Name, *col.Default) {
				diags = append(diags, pipeline.LintDiagnostic{
					Pos:     col.SrcPos,
					Rule:    "hardcoded-password",
					Message: fmt.Sprintf("column %s.%s default may contain a hardcoded password", t.QualifiedName(), col.Name),
					IsError: true,
				})
			}
		}
	}

	return diags
}

// ── Function rules ────────────────────────────────────────────────────────────

func checkFunction(f *ir.Function, cfg pipeline.LinterConfig) []pipeline.LintDiagnostic {
	var diags []pipeline.LintDiagnostic

	if cfg.WarnOnDeprecated && f.Deprecated != nil {
		diags = append(diags, pipeline.LintDiagnostic{
			Pos:     f.SrcPos,
			Rule:    "deprecated",
			Message: fmt.Sprintf("function %s is deprecated: %s", f.QualifiedName(), *f.Deprecated),
		})
	}
	// Warn on SECURITY DEFINER without explicit search_path.
	if f.Attrs.SecurityDef && !strings.Contains(f.Attrs.Body, "search_path") {
		diags = append(diags, pipeline.LintDiagnostic{
			Pos:     f.SrcPos,
			Rule:    "security-definer-search-path",
			Message: fmt.Sprintf("SECURITY DEFINER function %s should set search_path", f.QualifiedName()),
		})
	}

	return diags
}

// ── View rules ────────────────────────────────────────────────────────────────

func checkView(v *ir.View, cfg pipeline.LinterConfig) []pipeline.LintDiagnostic {
	var diags []pipeline.LintDiagnostic

	if cfg.WarnOnDeprecated && v.Deprecated != nil {
		diags = append(diags, pipeline.LintDiagnostic{
			Pos:     v.SrcPos,
			Rule:    "deprecated",
			Message: fmt.Sprintf("view %s is deprecated: %s", v.QualifiedName(), *v.Deprecated),
		})
	}

	return diags
}

// ── helpers ───────────────────────────────────────────────────────────────────

var passwordColNames = []string{"password", "passwd", "pwd", "secret", "passphrase"}

func looksLikePassword(colName, defaultExpr string) bool {
	lower := strings.ToLower(colName)
	for _, kw := range passwordColNames {
		if strings.Contains(lower, kw) {
			// Check if the default is a string literal (starts with ').
			trimmed := strings.TrimSpace(defaultExpr)
			if strings.HasPrefix(trimmed, "'") {
				return true
			}
		}
	}
	return false
}

var _ pipeline.Linter = (*BuiltinLinter)(nil)
