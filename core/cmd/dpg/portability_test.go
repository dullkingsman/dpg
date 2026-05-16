package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

func TestWritePortabilityJSONEmpty(t *testing.T) {
	r, w, _ := os.Pipe()
	orig := os.Stdout
	os.Stdout = w

	err := writePortabilityJSON("prod", "myapp", nil)

	w.Close()
	os.Stdout = orig

	if err != nil {
		t.Fatalf("writePortabilityJSON returned error: %v", err)
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

	var parsed portabilityJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if parsed.Cluster != "prod" {
		t.Errorf("cluster = %q, want %q", parsed.Cluster, "prod")
	}
	if parsed.Database != "myapp" {
		t.Errorf("database = %q, want %q", parsed.Database, "myapp")
	}
	if len(parsed.Issues) != 0 {
		t.Errorf("expected 0 issues, got %d", len(parsed.Issues))
	}
}

func TestWritePortabilityJSONWithIssues(t *testing.T) {
	issues := []pipeline.PortabilityIssue{
		{
			Pos:         pipeline.SourcePos{File: "schemas/public/tables.dpg", Line: 10, Col: 5},
			Construct:   "jsonb",
			Alternative: "JSON",
		},
		{
			Pos:         pipeline.SourcePos{File: "schemas/public/functions.dpg", Line: 3},
			Construct:   "LANGUAGE plpgsql",
			Alternative: "",
		},
	}

	r, w, _ := os.Pipe()
	orig := os.Stdout
	os.Stdout = w

	err := writePortabilityJSON("staging", "store", issues)

	w.Close()
	os.Stdout = orig

	if err != nil {
		t.Fatalf("writePortabilityJSON returned error: %v", err)
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

	var parsed portabilityJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}

	if len(parsed.Issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(parsed.Issues))
	}

	first := parsed.Issues[0]
	if first.Construct != "jsonb" {
		t.Errorf("issues[0].construct = %q, want %q", first.Construct, "jsonb")
	}
	if first.Alternative != "JSON" {
		t.Errorf("issues[0].alternative = %q, want %q", first.Alternative, "JSON")
	}
	if first.File != "schemas/public/tables.dpg" {
		t.Errorf("issues[0].file = %q, want %q", first.File, "schemas/public/tables.dpg")
	}
	if first.Line != 10 {
		t.Errorf("issues[0].line = %d, want 10", first.Line)
	}
	if first.Col != 5 {
		t.Errorf("issues[0].col = %d, want 5", first.Col)
	}

	second := parsed.Issues[1]
	if second.Construct != "LANGUAGE plpgsql" {
		t.Errorf("issues[1].construct = %q, want %q", second.Construct, "LANGUAGE plpgsql")
	}
	if second.Alternative != "" {
		t.Errorf("issues[1].alternative should be empty (omitempty), got %q", second.Alternative)
	}
}
