package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

// stubLinter is a Linter that always returns a fixed set of diagnostics.
type stubLinter struct {
	diags []pipeline.LintDiagnostic
}

func (s *stubLinter) Lint(_ []pipeline.IRObject, _ pipeline.LinterConfig) ([]pipeline.LintDiagnostic, error) {
	return s.diags, nil
}

func dpgTempFile(t *testing.T, content string) (file, dir string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return file, dir
}

func TestRunValidateStrictOff(t *testing.T) {
	file, dir := dpgTempFile(t, "")
	stub := &stubLinter{diags: []pipeline.LintDiagnostic{
		{Rule: "deprecated", Message: "old table", IsError: false},
	}}

	hasError, err := runValidate("cl", "db", []string{file}, dir, stub, pipeline.LinterConfig{}, "text", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasError {
		t.Error("expected no error when strict=false and only warnings present")
	}
}

func TestRunValidateStrictOn(t *testing.T) {
	file, dir := dpgTempFile(t, "")
	stub := &stubLinter{diags: []pipeline.LintDiagnostic{
		{Rule: "deprecated", Message: "old table", IsError: false},
	}}

	hasError, err := runValidate("cl", "db", []string{file}, dir, stub, pipeline.LinterConfig{}, "text", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasError {
		t.Error("expected error when strict=true promotes warning to error")
	}
}

func TestRunValidateStrictNoEffect_AlreadyErrors(t *testing.T) {
	// When diagnostics are already errors, strict has no additional effect on outcome.
	file, dir := dpgTempFile(t, "")
	stub := &stubLinter{diags: []pipeline.LintDiagnostic{
		{Rule: "hardcoded-password", Message: "bad password", IsError: true},
	}}

	withoutStrict, _ := runValidate("cl", "db", []string{file}, dir, stub, pipeline.LinterConfig{}, "text", false)
	withStrict, _ := runValidate("cl", "db", []string{file}, dir, stub, pipeline.LinterConfig{}, "text", true)

	if !withoutStrict {
		t.Error("expected error for error-level diagnostic (no strict)")
	}
	if !withStrict {
		t.Error("expected error for error-level diagnostic (with strict)")
	}
}

func TestRunValidateStrictNoErrorsOrWarnings(t *testing.T) {
	file, dir := dpgTempFile(t, "")
	stub := &stubLinter{diags: nil}

	hasError, err := runValidate("cl", "db", []string{file}, dir, stub, pipeline.LinterConfig{}, "text", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasError {
		t.Error("expected no error when no diagnostics, even with strict=true")
	}
}

func TestRunValidateJSONFormat(t *testing.T) {
	file, dir := dpgTempFile(t, "")
	stub := &stubLinter{diags: []pipeline.LintDiagnostic{
		{Rule: "deprecated", Message: "old", IsError: false},
	}}

	// Capture stdout.
	r, w, _ := os.Pipe()
	orig := os.Stdout
	os.Stdout = w

	hasError, err := runValidate("mycluster", "mydb", []string{file}, dir, stub, pipeline.LinterConfig{}, "json", true)

	w.Close()
	os.Stdout = orig

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasError {
		t.Error("expected hasError=true with strict=true and a warning")
	}

	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, _ := r.Read(buf)
		if n == 0 {
			break
		}
		out.Write(buf[:n])
	}

	var parsed validateJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out.String())
	}
	if parsed.Cluster != "mycluster" {
		t.Errorf("cluster = %q, want %q", parsed.Cluster, "mycluster")
	}
	if parsed.Database != "mydb" {
		t.Errorf("database = %q, want %q", parsed.Database, "mydb")
	}
	// strict promotes the warning to an error, so it should appear in Errors.
	if len(parsed.Errors) == 0 {
		t.Error("expected at least one error in JSON output (promoted from warning by strict)")
	}
}
