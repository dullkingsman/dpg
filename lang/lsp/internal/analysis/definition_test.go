package analysis

import (
	"strings"
	"testing"

	"github.com/dullkingsman/dpg-lsp/internal/workspace"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func TestDefinition_FoundInSameFile(t *testing.T) {
	src := "TABLE actor_type (\n    id integer\n);\n"
	ws := workspace.New()
	ws.OpenDocument("/test/schema.dpg", src)

	// Cursor over "actor_type" on line 0
	pos := protocol.Position{Line: 0, Character: 8}
	got := Definition(ws, "/test/schema.dpg", pos)

	if got == nil {
		t.Fatal("Definition returned nil, want a location")
	}
	if !strings.Contains(string(got.URI), "schema.dpg") {
		t.Errorf("URI = %q, should reference schema.dpg", got.URI)
	}
	if got.Range.Start.Line != 0 {
		t.Errorf("Start.Line = %d, want 0", got.Range.Start.Line)
	}
}

func TestDefinition_NotFound(t *testing.T) {
	ws := workspace.New()
	ws.OpenDocument("/test/schema.dpg", "TABLE users (id bigint);")

	pos := protocol.Position{Line: 0, Character: 3} // cursor on "BLE" in TABLE keyword
	got := Definition(ws, "/test/schema.dpg", pos)

	// "BLE" does not match any declared object name
	if got != nil {
		t.Errorf("Definition = %+v, want nil for non-object token", got)
	}
}

func TestDefinition_EmptyDoc_ReturnsNil(t *testing.T) {
	ws := workspace.New()
	got := Definition(ws, "/nonexistent/schema.dpg", protocol.Position{})
	if got != nil {
		t.Errorf("Definition on empty doc = %+v, want nil", got)
	}
}

func TestSearchFile_FoundByBareName(t *testing.T) {
	text := "TABLE users (id bigint);\n"
	ws := workspace.New()
	got := searchFile(ws, "/test.dpg", text, "users")

	if got == nil {
		t.Fatal("searchFile returned nil for existing object")
	}
	if got.Range.Start.Line != 0 {
		t.Errorf("Start.Line = %d, want 0", got.Range.Start.Line)
	}
}

func TestSearchFile_FoundBySchemaQualifiedName(t *testing.T) {
	text := "TABLE iam.identities (id bigint);\n"
	ws := workspace.New()

	// Search by bare name
	byBare := searchFile(ws, "/test.dpg", text, "identities")
	if byBare == nil {
		t.Error("searchFile should find object by bare name 'identities'")
	}

	// Search by full name
	byFull := searchFile(ws, "/test.dpg", text, "iam.identities")
	if byFull == nil {
		t.Error("searchFile should find object by full name 'iam.identities'")
	}
}

func TestSearchFile_NotFound(t *testing.T) {
	text := "TABLE users (id bigint);\n"
	ws := workspace.New()
	got := searchFile(ws, "/test.dpg", text, "nonexistent")
	if got != nil {
		t.Errorf("searchFile = %+v, want nil for missing object", got)
	}
}

func TestSearchFile_CaseInsensitive(t *testing.T) {
	text := "TABLE Users (id bigint);\n"
	ws := workspace.New()
	got := searchFile(ws, "/test.dpg", text, "users")
	if got == nil {
		t.Error("searchFile should be case-insensitive")
	}
}

func TestSearchFile_URIFormat(t *testing.T) {
	text := "TABLE foo (id bigint);\n"
	ws := workspace.New()
	got := searchFile(ws, "/my/project/schema.dpg", text, "foo")
	if got == nil {
		t.Fatal("expected non-nil location")
	}
	if !strings.HasPrefix(string(got.URI), "file://") {
		t.Errorf("URI = %q, should start with file://", got.URI)
	}
}

func TestDefinition_AllObjectKinds(t *testing.T) {
	src := `TABLE t1 (id bigint);
VIEW v1 AS SELECT 1;
FUNCTION f1() RETURNS void LANGUAGE sql AS $$ SELECT 1 $$;
ENUM e1 ('a', 'b');
`
	ws := workspace.New()
	ws.OpenDocument("/test/schema.dpg", src)

	tests := []struct {
		name string
		line uint32
		char uint32
	}{
		{"t1", 0, 7},
		{"v1", 1, 6},
		{"f1", 2, 10},
		{"e1", 3, 6},
	}

	for _, tt := range tests {
		pos := protocol.Position{Line: tt.line, Character: tt.char}
		got := Definition(ws, "/test/schema.dpg", pos)
		if got == nil {
			t.Errorf("Definition(%q) = nil, want location", tt.name)
		}
	}
}
