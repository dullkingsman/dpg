package workspace

import (
	"testing"
)

func TestFileMatches_EmptyDiagFile(t *testing.T) {
	// Empty diagFile matches anything
	if !fileMatches("", "/real/path.dpg", "/tmp/lsp-tmp-123.dpg") {
		t.Error("empty diagFile should match any path")
	}
}

func TestFileMatches_OriginalPath(t *testing.T) {
	if !fileMatches("/real/schema.dpg", "/real/schema.dpg", "/tmp/tmp.dpg") {
		t.Error("diagFile equal to original should match")
	}
}

func TestFileMatches_TmpPath(t *testing.T) {
	if !fileMatches("/tmp/dpg-lsp-abc.dpg", "/real/schema.dpg", "/tmp/dpg-lsp-abc.dpg") {
		t.Error("diagFile equal to tmp path should match")
	}
}

func TestFileMatches_UnrelatedPath(t *testing.T) {
	if fileMatches("/other/schema.dpg", "/real/schema.dpg", "/tmp/tmp.dpg") {
		t.Error("unrelated diagFile should not match")
	}
}
