package format_test

import (
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/format"
)

// ── Lexer ─────────────────────────────────────────────────────────────────────

func TestLex_Keywords(t *testing.T) {
	src := `TABLE VIEW FUNCTION`
	tokens := format.Lex("", []byte(src))
	var kws []string
	for _, tok := range tokens {
		if tok.Type == format.TokKeyword {
			kws = append(kws, tok.Text)
		}
	}
	if len(kws) != 3 {
		t.Errorf("expected 3 keywords, got %d: %v", len(kws), kws)
	}
}

func TestLex_LineComment(t *testing.T) {
	src := "-- this is a comment\nTABLE"
	tokens := format.Lex("", []byte(src))
	var got []format.TokType
	for _, tok := range tokens {
		if tok.Type != format.TokWhitespace && tok.Type != format.TokNewline && tok.Type != format.TokEOF {
			got = append(got, tok.Type)
		}
	}
	if len(got) < 2 || got[0] != format.TokLineComment || got[1] != format.TokKeyword {
		t.Errorf("unexpected token sequence: %v", got)
	}
}

func TestLex_BlockComment(t *testing.T) {
	src := `/* nested /* block */ comment */`
	tokens := format.Lex("", []byte(src))
	found := false
	for _, tok := range tokens {
		if tok.Type == format.TokBlockComment {
			found = true
			if tok.Text != src {
				t.Errorf("block comment text: got %q", tok.Text)
			}
		}
	}
	if !found {
		t.Error("block comment token not found")
	}
}

func TestLex_DollarQuote(t *testing.T) {
	src := `$$SELECT 1;$$`
	tokens := format.Lex("", []byte(src))
	found := false
	for _, tok := range tokens {
		if tok.Type == format.TokDollarQuote {
			found = true
			if tok.Text != src {
				t.Errorf("dollar quote text: got %q", tok.Text)
			}
		}
	}
	if !found {
		t.Error("dollar quote token not found")
	}
}

func TestLex_LineNumbers(t *testing.T) {
	src := "TABLE\nVIEW"
	tokens := format.Lex("", []byte(src))
	viewLine := -1
	for _, tok := range tokens {
		if tok.Text == "VIEW" {
			viewLine = tok.Line
		}
	}
	if viewLine != 2 {
		t.Errorf("VIEW should be on line 2, got %d", viewLine)
	}
}

// ── Format (round-trip) ────────────────────────────────────────────────────────

func formatSrc(t *testing.T, src string, opts format.Options) string {
	t.Helper()
	out, err := format.Format("test.dpg", []byte(src), opts)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	return string(out)
}

var defaultOpts = format.Options{IndentSize: 4, KeywordCase: "upper"}

func TestFormat_SimpleTable(t *testing.T) {
	src := `table users (
    id bigint not null,
    email text not null
);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "TABLE") {
		t.Errorf("keywords not uppercased in: %s", out)
	}
	if !strings.Contains(out, "users") {
		t.Error("table name 'users' missing")
	}
}

func TestFormat_KeywordCaseLower(t *testing.T) {
	opts := format.Options{IndentSize: 4, KeywordCase: "lower"}
	src := `TABLE users (id BIGINT NOT NULL);`
	out := formatSrc(t, src, opts)
	if !strings.Contains(out, "table") {
		t.Errorf("expected lowercase keywords in: %s", out)
	}
	if strings.Contains(out, "TABLE") {
		t.Errorf("unexpected uppercase keywords in: %s", out)
	}
}

func TestFormat_IndentSize2(t *testing.T) {
	opts := format.Options{IndentSize: 2, KeywordCase: "upper"}
	src := `TABLE users (id BIGINT NOT NULL, email TEXT NOT NULL);`
	out := formatSrc(t, src, opts)
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, " ") {
			if strings.HasPrefix(line, "    ") {
				t.Errorf("expected 2-space indent, found 4+ space indent in: %q", line)
			}
			break
		}
	}
}

func TestFormat_BlankLineBetweenObjects(t *testing.T) {
	src := `TABLE a (id BIGINT NOT NULL);
TABLE b (id BIGINT NOT NULL);`
	out := formatSrc(t, src, defaultOpts)
	// There should be a blank line between the two table declarations.
	if !strings.Contains(out, ";\n\nTABLE") {
		t.Errorf("expected blank line between top-level declarations, got:\n%s", out)
	}
}

func TestFormat_LeadingCommentPreserved(t *testing.T) {
	src := `-- Users table
TABLE users (id BIGINT NOT NULL);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "-- Users table") {
		t.Errorf("leading comment lost, got:\n%s", out)
	}
}

func TestFormat_IntraTableCommentPreserved(t *testing.T) {
	src := `TABLE users (
    id    BIGINT NOT NULL,
    -- email column
    email TEXT NOT NULL
);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "-- email column") {
		t.Errorf("intra-table comment lost, got:\n%s", out)
	}
}

func TestFormat_BlankLineInsideTablePreserved(t *testing.T) {
	src := `TABLE users (
    id    BIGINT NOT NULL,

    -- lifecycle
    created_at TIMESTAMPTZ NOT NULL
);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "\n\n") {
		t.Errorf("blank line inside table lost, got:\n%s", out)
	}
	if !strings.Contains(out, "-- lifecycle") {
		t.Errorf("section comment lost, got:\n%s", out)
	}
}

func TestFormat_TrailingCommentPreserved(t *testing.T) {
	src := `TABLE users (
    id    BIGINT NOT NULL, -- primary key
    email TEXT NOT NULL
);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "-- primary key") {
		t.Errorf("trailing comment lost, got:\n%s", out)
	}
}

func TestFormat_OpaqueObjectPreserved(t *testing.T) {
	src := `EXTENSION "uuid-ossp";`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "EXTENSION") {
		t.Errorf("EXTENSION keyword missing, got:\n%s", out)
	}
	if !strings.Contains(out, "uuid-ossp") {
		t.Errorf("extension name missing, got:\n%s", out)
	}
}

func TestFormat_UnloggedTable(t *testing.T) {
	src := `UNLOGGED TABLE cache (key TEXT NOT NULL, value TEXT NOT NULL);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "UNLOGGED") {
		t.Errorf("UNLOGGED keyword missing, got:\n%s", out)
	}
	if !strings.Contains(out, "TABLE") {
		t.Errorf("TABLE keyword missing, got:\n%s", out)
	}
}

func TestFormat_ParseError_ReturnsOriginal(t *testing.T) {
	// Intentionally broken SQL; Format should return original unchanged.
	src := `NOT VALID SQL @@@`
	out, err := format.Format("bad.dpg", []byte(src), defaultOpts)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if string(out) != src {
		t.Errorf("on error, original should be returned unchanged")
	}
}

func TestFormat_EmptyFile(t *testing.T) {
	out := formatSrc(t, "", defaultOpts)
	if out != "" {
		t.Errorf("empty file: expected empty output, got %q", out)
	}
}

// ── Idempotence ───────────────────────────────────────────────────────────────

func TestFormat_Idempotent(t *testing.T) {
	src := `-- Users table
TABLE users (
    id    BIGINT NOT NULL,

    -- lifecycle
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

TABLE orders (
    id      BIGINT NOT NULL,
    user_id BIGINT NOT NULL
);`
	first := formatSrc(t, src, defaultOpts)
	second := formatSrc(t, first, defaultOpts)
	if first != second {
		t.Errorf("format is not idempotent.\nFirst:\n%s\nSecond:\n%s", first, second)
	}
}

// ── Options ───────────────────────────────────────────────────────────────────

func TestOptions_IndentDefault(t *testing.T) {
	opts := format.Options{}
	ind := opts.Indent()
	if ind != "    " {
		t.Errorf("default indent: got %q", ind)
	}
}

func TestOptions_IndentCustom(t *testing.T) {
	opts := format.Options{IndentSize: 2}
	if opts.Indent() != "  " {
		t.Errorf("2-space indent: got %q", opts.Indent())
	}
}

func TestOptions_KeywordUpper(t *testing.T) {
	opts := format.Options{KeywordCase: "upper"}
	if opts.Keyword("table") != "TABLE" {
		t.Errorf("Keyword upper: got %q", opts.Keyword("table"))
	}
}

func TestOptions_KeywordLower(t *testing.T) {
	opts := format.Options{KeywordCase: "lower"}
	if opts.Keyword("TABLE") != "table" {
		t.Errorf("Keyword lower: got %q", opts.Keyword("TABLE"))
	}
}

func TestOptions_KeywordDefaultUpper(t *testing.T) {
	opts := format.Options{}
	if opts.Keyword("table") != "TABLE" {
		t.Errorf("Keyword default (upper): got %q", opts.Keyword("table"))
	}
}
