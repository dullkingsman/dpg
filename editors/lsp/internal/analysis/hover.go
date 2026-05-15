package analysis

import (
	"fmt"
	"strings"

	"github.com/dullkingsman/dpg-lsp/internal/workspace"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Hover returns hover information for the symbol at the given position.
// It scans the document for object declarations that contain the cursor position.
func Hover(ws *workspace.Workspace, path string, pos protocol.Position) *protocol.Hover {
	text := ws.GetText(path)
	if text == "" {
		return nil
	}

	word := wordAtPosition(text, pos)
	if word == "" {
		return nil
	}

	objs := workspace.ParseObjects(text, path)
	for _, obj := range objs {
		if strings.EqualFold(obj.Name, word) || strings.HasSuffix(obj.Name, "."+word) {
			md := fmt.Sprintf("**%s** `%s`", obj.Kind, obj.Name)
			if obj.Comment != "" {
				md += "\n\n" + obj.Comment
			}
			kind := protocol.MarkupKindMarkdown
			return &protocol.Hover{
				Contents: protocol.MarkupContent{Kind: kind, Value: md},
				Range: &protocol.Range{
					Start: protocol.Position{Line: uint32(obj.Line - 1), Character: 0},
					End:   protocol.Position{Line: uint32(obj.Line - 1), Character: uint32(len(obj.Kind) + len(obj.Name) + 1)},
				},
			}
		}
	}

	// Fallback: show keyword documentation
	if doc := keywordDoc(word); doc != "" {
		kind := protocol.MarkupKindMarkdown
		return &protocol.Hover{
			Contents: protocol.MarkupContent{Kind: kind, Value: doc},
		}
	}

	return nil
}

// wordAtPosition extracts the identifier word under the cursor.
func wordAtPosition(text string, pos protocol.Position) string {
	lines := strings.Split(text, "\n")
	if int(pos.Line) >= len(lines) {
		return ""
	}
	line := lines[pos.Line]
	col := int(pos.Character)
	if col > len(line) {
		col = len(line)
	}

	isIdent := func(c byte) bool {
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '$'
	}

	// If the cursor is on a non-ident character (and not at end-of-line),
	// there is no word under it.
	if col < len(line) && !isIdent(line[col]) {
		return ""
	}

	start := col
	for start > 0 && isIdent(line[start-1]) {
		start--
	}
	end := col
	for end < len(line) && isIdent(line[end]) {
		end++
	}
	return line[start:end]
}

func keywordDoc(word string) string {
	docs := map[string]string{
		"TABLE":       "**TABLE** — declares a persistent relation (DPG §3.1)",
		"VIEW":        "**VIEW** — declares a named query (DPG §3.2)",
		"FUNCTION":    "**FUNCTION** — declares a database function (DPG §3.4)",
		"PROCEDURE":   "**PROCEDURE** — declares a stored procedure (DPG §3.5)",
		"ENUM":        "**ENUM** — declares an enumerated type (DPG §3.8)",
		"TYPE":        "**TYPE** — declares a composite, range, or base type (DPG §3.9)",
		"DOMAIN":      "**DOMAIN** — declares a domain (constrained base type) (DPG §3.10)",
		"SEQUENCE":    "**SEQUENCE** — declares a sequence generator (DPG §3.11)",
		"ROLE":        "**ROLE** — declares a cluster-level role (DPG §3.13)",
		"MACRO":       "**MACRO** — file-scoped column-list or block template (DPG §6)",
		"INDICES":     "`INDICES { }` — index definitions block",
		"POLICIES":    "`POLICIES { }` — row-level security policy block",
		"TRIGGERS":    "`TRIGGERS { }` — trigger definition block",
		"GRANTS":      "`GRANTS { }` — privilege grant block",
		"REVOCATIONS": "`REVOCATIONS { }` — privilege revoke block",
		"RENAMED":     "`RENAMED FROM <old_name>;` — tracks a rename for safe migration",
		"DEPRECATED":  "`DEPRECATED \"message\";` — marks an object as deprecated",
		"PROTECTED":   "`PROTECTED;` — prevents accidental DROP (error DPG-E022)",
		"MIGRATE":     "`MIGRATE REMOVE (value) { DML }` — safe ENUM value removal",
	}
	return docs[strings.ToUpper(word)]
}
