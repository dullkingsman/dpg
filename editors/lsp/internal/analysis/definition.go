package analysis

import (
	"strings"

	"github.com/dullkingsman/dpg-lsp/internal/workspace"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Definition returns the location of the definition for the symbol at pos,
// or nil if none is found.
func Definition(ws *workspace.Workspace, path string, pos protocol.Position) *protocol.Location {
	text := ws.GetText(path)
	if text == "" {
		return nil
	}

	word := wordAtPosition(text, pos)
	if word == "" {
		return nil
	}

	// Search the current file first
	if loc := searchFile(ws, path, text, word); loc != nil {
		return loc
	}

	// Search other open files in the same project root
	root := ws.Root()
	if root == "" {
		return nil
	}

	// Walk .dpg files under root (shallow — only same schema dir for now)
	dir := workspace.FindProjectRoot(path)
	files := workspace.ListDPGFiles(dir)
	for _, f := range files {
		if f == path {
			continue
		}
		t := ws.GetText(f)
		if loc := searchFile(ws, f, t, word); loc != nil {
			return loc
		}
	}
	return nil
}

func searchFile(ws *workspace.Workspace, filePath, text, word string) *protocol.Location {
	objs := workspace.ParseObjects(text, filePath)
	for _, obj := range objs {
		bare := obj.Name
		if idx := strings.LastIndex(bare, "."); idx >= 0 {
			bare = bare[idx+1:]
		}
		if strings.EqualFold(bare, word) || strings.EqualFold(obj.Name, word) {
			line := uint32(obj.Line - 1)
			return &protocol.Location{
				URI: protocol.DocumentUri(pathToURI(filePath)),
				Range: protocol.Range{
					Start: protocol.Position{Line: line, Character: 0},
					End:   protocol.Position{Line: line, Character: uint32(len(obj.Kind) + len(obj.Name) + 2)},
				},
			}
		}
	}
	return nil
}

func pathToURI(path string) string {
	if len(path) > 0 && path[0] == '/' {
		return "file://" + path
	}
	return "file:///" + path
}
