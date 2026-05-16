package tree_sitter_dpg_test

import (
	"testing"

	tree_sitter "github.com/smacker/go-tree-sitter"
	"github.com/tree-sitter/tree-sitter-dpg"
)

func TestCanLoadGrammar(t *testing.T) {
	language := tree_sitter.NewLanguage(tree_sitter_dpg.Language())
	if language == nil {
		t.Errorf("Error loading Dpg grammar")
	}
}
