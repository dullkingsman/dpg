package analysis

import (
	"testing"

	"github.com/dullkingsman/dpg-lsp/internal/workspace"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestDiagnostics_Empty(t *testing.T) {
	ws := workspace.New()
	ws.SetDiagnostics("/fake/schema.dpg", nil)

	got := Diagnostics(ws, "/fake/schema.dpg")
	if len(got) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", len(got))
	}
}

func TestDiagnostics_ErrorSeverity(t *testing.T) {
	ws := workspace.New()
	ws.SetDiagnostics("/fake/schema.dpg", []workspace.Diagnostic{
		{Rule: "DPG-E006", Message: "forbidden verb", IsError: true},
	})

	got := Diagnostics(ws, "/fake/schema.dpg")
	if len(got) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(got))
	}
	if *got[0].Severity != protocol.DiagnosticSeverityError {
		t.Errorf("severity = %v, want Error", *got[0].Severity)
	}
	if got[0].Message != "forbidden verb" {
		t.Errorf("message = %q, want 'forbidden verb'", got[0].Message)
	}
}

func TestDiagnostics_WarningSeverity(t *testing.T) {
	ws := workspace.New()
	ws.SetDiagnostics("/fake/schema.dpg", []workspace.Diagnostic{
		{Rule: "deprecated", Message: "col is deprecated", IsError: false},
	})

	got := Diagnostics(ws, "/fake/schema.dpg")
	if *got[0].Severity != protocol.DiagnosticSeverityWarning {
		t.Errorf("severity = %v, want Warning", *got[0].Severity)
	}
}

func TestDiagnostics_PositionMapping(t *testing.T) {
	// DPG uses 1-based line/col; LSP uses 0-based.
	ws := workspace.New()
	ws.SetDiagnostics("/fake/schema.dpg", []workspace.Diagnostic{
		{Message: "err", Line: 3, Col: 5, IsError: true},
	})

	got := Diagnostics(ws, "/fake/schema.dpg")
	start := got[0].Range.Start
	if start.Line != 2 {
		t.Errorf("Range.Start.Line = %d, want 2 (0-based)", start.Line)
	}
	if start.Character != 4 {
		t.Errorf("Range.Start.Character = %d, want 4 (0-based)", start.Character)
	}
}

func TestDiagnostics_NoPosition_ZeroRange(t *testing.T) {
	ws := workspace.New()
	ws.SetDiagnostics("/fake/schema.dpg", []workspace.Diagnostic{
		{Message: "no position", Line: 0, Col: 0, IsError: true},
	})

	got := Diagnostics(ws, "/fake/schema.dpg")
	r := got[0].Range
	if r.Start.Line != 0 || r.Start.Character != 0 {
		t.Errorf("expected zero range, got %+v", r)
	}
}

func TestDiagnostics_RuleAsCode(t *testing.T) {
	ws := workspace.New()
	ws.SetDiagnostics("/fake/schema.dpg", []workspace.Diagnostic{
		{Rule: "DPG-E022", Message: "protected drop", IsError: true},
	})

	got := Diagnostics(ws, "/fake/schema.dpg")
	if got[0].Code == nil {
		t.Fatal("Code should be set when Rule is non-empty")
	}
	if got[0].Code.Value != "DPG-E022" {
		t.Errorf("Code.Value = %v, want DPG-E022", got[0].Code.Value)
	}
}

func TestDiagnostics_NoRule_NoCode(t *testing.T) {
	ws := workspace.New()
	ws.SetDiagnostics("/fake/schema.dpg", []workspace.Diagnostic{
		{Rule: "", Message: "generic error", IsError: true},
	})

	got := Diagnostics(ws, "/fake/schema.dpg")
	if got[0].Code != nil {
		t.Error("Code should be nil when Rule is empty")
	}
}

func TestDiagnostics_SourceIsAlwaysDpg(t *testing.T) {
	ws := workspace.New()
	ws.SetDiagnostics("/fake/schema.dpg", []workspace.Diagnostic{
		{Message: "x", IsError: true},
	})

	got := Diagnostics(ws, "/fake/schema.dpg")
	if *got[0].Source != "dpg" {
		t.Errorf("Source = %q, want dpg", *got[0].Source)
	}
}

func TestDiagnostics_MultiplePreservesOrder(t *testing.T) {
	ws := workspace.New()
	ws.SetDiagnostics("/fake/schema.dpg", []workspace.Diagnostic{
		{Rule: "A", Message: "first", IsError: true},
		{Rule: "B", Message: "second", IsError: false},
		{Rule: "C", Message: "third", IsError: true},
	})

	got := Diagnostics(ws, "/fake/schema.dpg")
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, want := range []string{"A", "B", "C"} {
		if got[i].Code.Value != want {
			t.Errorf("got[%d].Code = %v, want %s", i, got[i].Code.Value, want)
		}
	}
}

func TestDiagnostics_ColZeroBecomesZero(t *testing.T) {
	// Col=0 means no column info; character should be 0 not -1.
	ws := workspace.New()
	ws.SetDiagnostics("/fake/schema.dpg", []workspace.Diagnostic{
		{Message: "x", Line: 1, Col: 0, IsError: true},
	})

	got := Diagnostics(ws, "/fake/schema.dpg")
	if got[0].Range.Start.Character != 0 {
		t.Errorf("Character with Col=0 = %d, want 0", got[0].Range.Start.Character)
	}
}
