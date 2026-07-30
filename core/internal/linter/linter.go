// Package linter implements pipeline.Linter. It runs built-in lint rules
// over the merged IR and returns diagnostics.
package linter

import (
	"fmt"
	"regexp"
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
	case *ir.Role:
		diags = append(diags, checkRole(o, cfg)...)
	case *ir.UserMapping:
		diags = append(diags, checkUserMapping(o, cfg)...)
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

// ── Role rules ────────────────────────────────────────────────────────────────

// checkRole implements RFC §11.1's "Hardcoded passwords" rule: a ROLE
// PASSWORD with no {{secret-uri}} placeholder at all is a literal
// credential sitting in plaintext in the .dpg source file. IsError (not a
// warning) when cfg.ForbidHardcodedPasswords is enabled (default true),
// matching the RFC's "MUST emit an error" wording — the same severity the
// table-column "hardcoded-password" rule above uses for the analogous
// table-column-default case. Scheme-agnostic: checks for any {{...}}
// placeholder, not one specific scheme (an earlier RFC draft named
// env:VAR_NAME specifically, before four more backends existed).
// Rule ID is "hardcoded-role-password", not "hardcoded-password" (which
// would collide with the pre-existing table-column-default rule below,
// name-identical despite checking a completely different thing) — RFC
// §19.1's own rules table separately names this `hardcoded_password` and
// the table-column one is unlisted there at all, so neither the RFC's
// snake_case naming nor a shared name were adopted; kept kebab-case to
// match every other rule ID actually in code, and disambiguated the two
// checks now that both concretely exist under the same linter flag.
func checkRole(r *ir.Role, cfg pipeline.LinterConfig) []pipeline.LintDiagnostic {
	var diags []pipeline.LintDiagnostic

	if cfg.ForbidHardcodedPasswords && r.Password != nil && !strings.Contains(*r.Password, "{{") {
		diags = append(diags, pipeline.LintDiagnostic{
			Pos:     r.SrcPos,
			Rule:    "hardcoded-role-password",
			Message: fmt.Sprintf("role %s: PASSWORD is a literal value; use a {{secret-uri}} reference instead (e.g. {{vault:secret/roles/%s#password}})", r.Name, r.Name),
			IsError: true,
		})
	}

	return diags
}

// ── UserMapping rules ────────────────────────────────────────────────────────

// fdwPasswordLit matches a password OPTIONS entry inside a USER MAPPING's
// opaque Body text: password 'value' (PostgreSQL's own OPTIONS syntax,
// case-insensitive keyword, ” escaping supported like any SQL string
// literal). UserMapping stays fully opaque (no structured OPTIONS field
// exists to check directly, unlike Role.Password) — see
// internal/diff.userMappingCreateOp's doc comment for why OPTIONS keys
// aren't parsed out structurally at all (they're FDW-provider-specific,
// not fixed by DPG).
var fdwPasswordLit = regexp.MustCompile(`(?i)\bpassword\s+'((?:[^']|'')*)'`)

// checkUserMapping implements RFC §19.1's hardcoded_fdw_password rule: a
// literal `password 'value'` in a USER MAPPING's OPTIONS, with no
// {{secret-uri}} placeholder anywhere in it, when forbid_hardcoded_passwords
// is enabled — same severity and intent as checkRole, applied via a text
// match instead of a structured field since UserMapping has none.
func checkUserMapping(u *ir.UserMapping, cfg pipeline.LinterConfig) []pipeline.LintDiagnostic {
	var diags []pipeline.LintDiagnostic

	if !cfg.ForbidHardcodedPasswords {
		return diags
	}
	if m := fdwPasswordLit.FindStringSubmatch(u.Body); m != nil && !strings.Contains(m[1], "{{") {
		diags = append(diags, pipeline.LintDiagnostic{
			Pos:     u.SrcPos,
			Rule:    "hardcoded-fdw-password",
			Message: fmt.Sprintf("user mapping %s: OPTIONS password is a literal value; use a {{secret-uri}} reference instead (e.g. {{vault:secret/fdw/%s#password}})", u.QualifiedName(), u.Server),
			IsError: true,
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
