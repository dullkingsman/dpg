package server

import (
	"testing"
)

// ── uriToPath ─────────────────────────────────────────────────────────────────

func TestUriToPath_fileScheme(t *testing.T) {
	got := uriToPath("file:///home/user/schema.dpg")
	want := "/home/user/schema.dpg"
	if got != want {
		t.Errorf("uriToPath = %q, want %q", got, want)
	}
}

func TestUriToPath_noScheme(t *testing.T) {
	got := uriToPath("/tmp/schema.dpg")
	if got != "/tmp/schema.dpg" {
		t.Errorf("uriToPath = %q, want /tmp/schema.dpg", got)
	}
}

func TestUriToPath_empty(t *testing.T) {
	if got := uriToPath(""); got != "" {
		t.Errorf("uriToPath(\"\") = %q, want empty", got)
	}
}

// ── pathToURI ─────────────────────────────────────────────────────────────────

func TestPathToURI_absolute(t *testing.T) {
	got := pathToURI("/home/user/schema.dpg")
	want := "file:///home/user/schema.dpg"
	if got != want {
		t.Errorf("pathToURI = %q, want %q", got, want)
	}
}

func TestPathToURI_relative(t *testing.T) {
	got := pathToURI("schema.dpg")
	if got != "file:///schema.dpg" {
		t.Errorf("pathToURI = %q, want file:///schema.dpg", got)
	}
}

func TestPathToURI_roundTrip(t *testing.T) {
	path := "/var/dpg/project/tables.dpg"
	if got := uriToPath(pathToURI(path)); got != path {
		t.Errorf("round-trip failed: got %q, want %q", got, path)
	}
}

// ── countLines ────────────────────────────────────────────────────────────────

func TestCountLines_empty(t *testing.T) {
	if n := countLines(""); n != 0 {
		t.Errorf("countLines(\"\") = %d, want 0", n)
	}
}

func TestCountLines_single(t *testing.T) {
	if n := countLines("hello\n"); n != 1 {
		t.Errorf("countLines = %d, want 1", n)
	}
}

func TestCountLines_multi(t *testing.T) {
	if n := countLines("a\nb\nc\n"); n != 3 {
		t.Errorf("countLines = %d, want 3", n)
	}
}

func TestCountLines_noTrailingNewline(t *testing.T) {
	if n := countLines("a\nb"); n != 1 {
		t.Errorf("countLines = %d, want 1", n)
	}
}

// ── lastLineLength ────────────────────────────────────────────────────────────

func TestLastLineLength_empty(t *testing.T) {
	if n := lastLineLength(""); n != 0 {
		t.Errorf("lastLineLength(\"\") = %d, want 0", n)
	}
}

func TestLastLineLength_singleLine(t *testing.T) {
	if n := lastLineLength("hello"); n != 5 {
		t.Errorf("lastLineLength = %d, want 5", n)
	}
}

func TestLastLineLength_trailingNewline(t *testing.T) {
	// "abc\n" — last line after the newline is empty (length 0)
	if n := lastLineLength("abc\n"); n != 0 {
		t.Errorf("lastLineLength = %d, want 0", n)
	}
}

func TestLastLineLength_multiLine(t *testing.T) {
	if n := lastLineLength("first line\nsecond"); n != 6 {
		t.Errorf("lastLineLength = %d, want 6", n)
	}
}

// ── formatDocument (file I/O path) ───────────────────────────────────────────

func TestFormatDocument_missingFile(t *testing.T) {
	edits, err := formatDocument("/nonexistent/path/schema.dpg")
	// readFile fails → error propagated, no edits
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
	if edits != nil {
		t.Errorf("expected nil edits on error, got %v", edits)
	}
}
