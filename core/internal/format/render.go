package format

import (
	"strings"
)

// Format parses src (from file path) and returns the canonically formatted
// output. If the file cannot be parsed, the original source is returned
// unchanged so the formatter never corrupts a file.
func Format(path string, src []byte, opts Options) ([]byte, error) {
	f, err := Parse(path, src)
	if err != nil {
		return src, err
	}
	return []byte(render(f, opts)), nil
}

// render converts a FormatAST back to source text.
func render(f *File, opts Options) string {
	var b strings.Builder

	for _, c := range f.LeadingComments {
		b.WriteString(c)
		b.WriteByte('\n')
	}

	for i, obj := range f.Objects {
		if i > 0 {
			b.WriteByte('\n') // blank line between top-level declarations
		}
		renderObject(&b, obj, opts, 0)
		b.WriteByte('\n')
	}

	return b.String()
}

func renderObject(b *strings.Builder, obj ObjectNode, opts Options, depth int) {
	ind := strings.Repeat(opts.indent(), depth)

	// Write leading comments.
	for _, c := range obj.GetLeadingComments() {
		b.WriteString(ind)
		b.WriteString(c)
		b.WriteByte('\n')
	}

	switch n := obj.(type) {
	case *OpaqueNode:
		renderOpaque(b, n, opts, ind)
	case *TableNode:
		renderTable(b, n, opts, ind)
	case *SchemaBlockNode:
		renderSchemaBlock(b, n, opts, depth)
	case *MacroNode:
		renderMacro(b, n, opts, ind)
	}
}

func renderOpaque(b *strings.Builder, n *OpaqueNode, opts Options, ind string) {
	b.WriteString(ind)
	if n.KindKeyword != "" {
		b.WriteString(opts.keyword(n.KindKeyword))
		if n.RawPart1 != "" {
			b.WriteByte(' ')
		}
	}
	b.WriteString(rekeyword(n.RawPart1, opts))
	if n.RawPart2 != "" {
		b.WriteString(" {")
		b.WriteString(sortBlock(n.RawPart2))
		b.WriteString("}")
	} else {
		b.WriteByte(';')
	}
}

func renderTable(b *strings.Builder, n *TableNode, opts Options, ind string) {
	kw := opts.keyword("TABLE")
	if n.Unlogged {
		b.WriteString(ind)
		b.WriteString(opts.keyword("UNLOGGED"))
		b.WriteByte(' ')
		b.WriteString(kw)
	} else {
		b.WriteString(ind)
		b.WriteString(kw)
	}
	b.WriteByte(' ')
	b.WriteString(n.Name)
	b.WriteString(" (")

	colInd := ind + opts.indent()
	cols := sortColumns(n.Columns)
	for i, col := range cols {
		// Preserve blank line before this column's section block.
		if col.BlankLineBefore {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
		for _, c := range col.LeadingComments {
			b.WriteString(colInd)
			b.WriteString(c)
			b.WriteByte('\n')
		}
		if col.RawText != "" {
			b.WriteString(colInd)
			b.WriteString(rekeyword(col.RawText, opts))
			if i < len(cols)-1 {
				b.WriteByte(',')
			}
			if col.TrailingComment != "" {
				b.WriteString("  ")
				b.WriteString(col.TrailingComment)
			}
		}
	}
	b.WriteByte('\n')
	b.WriteString(ind)
	b.WriteByte(')')
	if n.RawPart2 != "" {
		b.WriteString(" {")
		b.WriteString(sortBlock(n.RawPart2))
		b.WriteString("}")
	} else {
		b.WriteByte(';')
	}
}

func renderMacro(b *strings.Builder, n *MacroNode, opts Options, ind string) {
	b.WriteString(ind)
	b.WriteString(opts.keyword("MACRO"))
	b.WriteByte(' ')
	b.WriteString(n.RawAfterKeyword)
}

func renderSchemaBlock(b *strings.Builder, n *SchemaBlockNode, opts Options, depth int) {
	ind := strings.Repeat(opts.indent(), depth)
	innerInd := strings.Repeat(opts.indent(), depth+1)

	b.WriteString(ind)
	b.WriteString(opts.keyword("SCHEMA"))
	b.WriteByte(' ')
	b.WriteString(n.Name)
	b.WriteString(" {")

	if n.RawAttrs != "" {
		// Split schema-level directives into chunks, sort them, and render each
		// at the inner indentation level.
		chunks, _ := splitBlockDirectives(sortBlock(n.RawAttrs))
		for _, chunk := range chunks {
			trimmed := strings.TrimLeft(chunk.text, " \t\r\n")
			if trimmed == "" {
				continue
			}
			b.WriteByte('\n')
			b.WriteString(innerInd)
			b.WriteString(rekeyword(trimmed, opts))
		}
	}

	for i, child := range n.Objects {
		if i == 0 && n.RawAttrs != "" {
			b.WriteByte('\n') // blank line between schema attrs and first nested object
		}
		b.WriteByte('\n')
		renderObject(b, child, opts, depth+1)
		b.WriteByte('\n')
	}
	b.WriteString(ind)
	b.WriteString("}")
}

// rekeyword rewrites known SQL/DPG keywords in text according to opts.KeywordCase.
// It operates on a whitespace-split token stream to avoid mangling identifiers.
func rekeyword(text string, opts Options) string {
	// Fast path: if no case preference, return as-is.
	if opts.KeywordCase == "" {
		return text
	}
	tokens := Lex("", []byte(text))
	var b strings.Builder
	for _, tok := range tokens {
		if tok.Type == TokEOF {
			break
		}
		if tok.Type == TokKeyword {
			b.WriteString(opts.keyword(tok.Text))
		} else {
			b.WriteString(tok.Text)
		}
	}
	return b.String()
}
