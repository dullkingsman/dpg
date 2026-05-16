// Package analysis provides LSP feature implementations (diagnostics, hover,
// definition, completion) backed by dpg CLI invocations and lightweight source parsing.
package analysis

import (
	"github.com/dullkingsman/dpg-lsp/internal/workspace"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Diagnostics converts the cached workspace diagnostics for a file into LSP Diagnostic values.
func Diagnostics(ws *workspace.Workspace, path string) []protocol.Diagnostic {
	raw := ws.GetDiagnostics(path)
	out := make([]protocol.Diagnostic, 0, len(raw))
	for _, d := range raw {
		sev := lspSeverity(d.IsError)
		diag := protocol.Diagnostic{
			Severity: &sev,
			Message:  d.Message,
			Source:   strPtr("dpg"),
		}
		if d.Line > 0 {
			line := uint32(d.Line - 1) // LSP is 0-based
			col := uint32(0)
			if d.Col > 0 {
				col = uint32(d.Col - 1)
			}
			diag.Range = protocol.Range{
				Start: protocol.Position{Line: line, Character: col},
				End:   protocol.Position{Line: line, Character: col + 1},
			}
		}
		if d.Rule != "" {
			code := protocol.IntegerOrString{Value: d.Rule}
			diag.Code = &code
		}
		out = append(out, diag)
	}
	return out
}

func lspSeverity(isError bool) protocol.DiagnosticSeverity {
	if isError {
		return protocol.DiagnosticSeverityError
	}
	return protocol.DiagnosticSeverityWarning
}

func strPtr(s string) *string { return &s }
