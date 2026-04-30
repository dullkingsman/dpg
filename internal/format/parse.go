package format

import (
	"strings"

	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/scanner"
)

// kindKeyword returns the leading keyword(s) for a given ObjectKind, matching
// what the scanner stripped from Part1.
func kindKeyword(k pipeline.ObjectKind) string {
	switch k {
	case pipeline.KindTable:
		return "TABLE"
	case pipeline.KindUnloggedTable:
		return "UNLOGGED TABLE"
	case pipeline.KindForeignTable:
		return "FOREIGN TABLE"
	case pipeline.KindView:
		return "VIEW"
	case pipeline.KindMaterializedView:
		return "MATERIALIZED VIEW"
	case pipeline.KindRecursiveView:
		return "RECURSIVE VIEW"
	case pipeline.KindFunction:
		return "FUNCTION"
	case pipeline.KindProcedure:
		return "PROCEDURE"
	case pipeline.KindAggregate:
		return "AGGREGATE"
	case pipeline.KindEnum:
		return "ENUM"
	case pipeline.KindCompositeType, pipeline.KindRangeType, pipeline.KindBaseType:
		return "TYPE"
	case pipeline.KindDomainType:
		return "DOMAIN"
	case pipeline.KindSchema:
		return "SCHEMA"
	case pipeline.KindExtension:
		return "EXTENSION"
	case pipeline.KindSequence:
		return "SEQUENCE"
	case pipeline.KindRole:
		return "ROLE"
	case pipeline.KindTablespace:
		return "TABLESPACE"
	case pipeline.KindFDW:
		return "FOREIGN DATA WRAPPER"
	case pipeline.KindServer:
		return "SERVER"
	case pipeline.KindUserMapping:
		return "USER MAPPING"
	case pipeline.KindPublication:
		return "PUBLICATION"
	case pipeline.KindSubscription:
		return "SUBSCRIPTION"
	case pipeline.KindEventTrigger:
		return "EVENT TRIGGER"
	case pipeline.KindCollation:
		return "COLLATION"
	case pipeline.KindOperator:
		return "OPERATOR"
	case pipeline.KindOperatorClass:
		return "OPERATOR CLASS"
	case pipeline.KindOperatorFamily:
		return "OPERATOR FAMILY"
	case pipeline.KindCast:
		return "CAST"
	case pipeline.KindStatisticsObject:
		return "STATISTICS"
	case pipeline.KindTSConfig:
		return "TEXT SEARCH CONFIGURATION"
	case pipeline.KindTSDict:
		return "TEXT SEARCH DICTIONARY"
	case pipeline.KindTSParser:
		return "TEXT SEARCH PARSER"
	case pipeline.KindTSTemplate:
		return "TEXT SEARCH TEMPLATE"
	case pipeline.KindDefaultPrivileges:
		return "DEFAULT PRIVILEGES"
	default:
		return ""
	}
}

// objectEndLine returns the last source line occupied by raw (inclusive).
// This is used to exclude intra-object comments from inter-object comment
// collection.
func objectEndLine(raw pipeline.RawObject) int {
	line := raw.Pos.Line + strings.Count(raw.Part1, "\n")
	if raw.Part2 != "" {
		line += strings.Count(raw.Part2, "\n")
	}
	return line
}

// Parse builds a FormatAST from the source file at path with content src.
// The FormatAST preserves comments for re-injection by the renderer.
func Parse(path string, src []byte) (*File, error) {
	sc := scanner.New()
	raws, err := sc.Scan(path, src)
	if err != nil {
		return nil, err
	}

	tokens := Lex(path, src)

	f := &File{Path: path}

	// Collect all comment tokens from the full token stream.
	var allComments []commentEntry
	for _, tok := range tokens {
		if tok.Type == TokLineComment || tok.Type == TokBlockComment {
			allComments = append(allComments, commentEntry{tok.Line, tok.Text})
		}
	}

	// File-level leading comments are those before the first object's start line.
	var fileLeading []string
	if len(raws) > 0 {
		firstLine := raws[0].Pos.Line
		for _, c := range allComments {
			if c.line < firstLine {
				fileLeading = append(fileLeading, c.text)
			}
		}
	} else {
		for _, c := range allComments {
			fileLeading = append(fileLeading, c.text)
		}
	}
	f.LeadingComments = fileLeading

	// Track the last line occupied by file-level leading comments so that
	// buildNodes doesn't re-collect them as the first object's leading comments.
	fileLeadingEndLine := 0
	if len(raws) > 0 {
		firstLine := raws[0].Pos.Line
		for _, c := range allComments {
			if c.line < firstLine {
				fileLeadingEndLine = c.line
			}
		}
	}

	p := &parser{
		src:      src,
		tokens:   tokens,
		comments: allComments,
	}
	f.Objects = p.buildNodes(raws, fileLeadingEndLine)
	return f, nil
}

type commentEntry struct {
	line int
	text string
}

type parser struct {
	src      []byte
	tokens   []Token
	comments []commentEntry
}

// commentsInRange returns comments with prevEndLine < line < targetLine.
// prevEndLine is the last line of the previous object (so intra-object
// comments are excluded); targetLine is the start line of the next object.
func (p *parser) commentsInRange(prevEndLine, targetLine int) []string {
	var out []string
	for _, c := range p.comments {
		if c.line > prevEndLine && c.line < targetLine {
			out = append(out, c.text)
		}
	}
	return out
}

// buildNodes converts a slice of pipeline.RawObject into ObjectNodes, correctly
// attaching inter-object comments. Comments inside a previous object's body are
// excluded by tracking the object's end line rather than its start line.
// fileLeadingEndLine is the last line of any file-level leading comments; it
// prevents those comments from being re-collected as the first object's leading.
func (p *parser) buildNodes(raws []pipeline.RawObject, fileLeadingEndLine int) []ObjectNode {
	var nodes []ObjectNode
	prevEndLine := fileLeadingEndLine
	for _, raw := range raws {
		leading := p.commentsInRange(prevEndLine, raw.Pos.Line)
		node := p.buildNode(raw, leading)
		nodes = append(nodes, node)
		prevEndLine = objectEndLine(raw)
	}
	return nodes
}

// buildNode converts one RawObject into an ObjectNode.
func (p *parser) buildNode(raw pipeline.RawObject, leading []string) ObjectNode {
	switch raw.Kind {
	case pipeline.KindTable, pipeline.KindUnloggedTable:
		return p.buildTableNode(raw, leading)
	default:
		return &OpaqueNode{
			baseNode:    baseNode{LeadingComments: leading},
			KindKeyword: kindKeyword(raw.Kind),
			RawPart1:    raw.Part1,
			RawPart2:    raw.Part2,
		}
	}
}

// buildTableNode parses a TABLE declaration's column list from Part1.
func (p *parser) buildTableNode(raw pipeline.RawObject, leading []string) ObjectNode {
	columns := parseColumns(raw.Part1)
	if len(columns) == 0 {
		return &OpaqueNode{
			baseNode:    baseNode{LeadingComments: leading},
			KindKeyword: kindKeyword(raw.Kind),
			RawPart1:    raw.Part1,
			RawPart2:    raw.Part2,
		}
	}

	return &TableNode{
		baseNode: baseNode{LeadingComments: leading},
		Unlogged: raw.Kind == pipeline.KindUnloggedTable,
		Name:     extractTableName(raw.Part1),
		Columns:  columns,
		RawPart2: raw.Part2,
	}
}

// extractTableName returns the table name (and schema qualifier if present)
// from the Part1 text. Part1 looks like `schema.name (col1 type, ...)`.
func extractTableName(part1 string) string {
	part1 = strings.TrimSpace(part1)
	if cut, _, ok := strings.Cut(part1, "("); ok {
		return strings.TrimSpace(cut)
	}
	return part1
}

// parseColumns splits Part1 of a TABLE declaration into ColumnNodes.
// It uses the format lexer so that comments and blank lines within the column
// list are detected and attached correctly.
func parseColumns(part1 string) []*ColumnNode {
	// Find the outer paren that holds the column list.
	open := strings.IndexByte(part1, '(')
	if open < 0 {
		return nil
	}
	close := findMatchingParen(part1, open)
	if close < 0 {
		return nil
	}
	inner := part1[open+1 : close]
	return splitColumnDefs(inner)
}

// findMatchingParen returns the index of the ')' matching '(' at openIdx,
// accounting for single-quoted strings.
func findMatchingParen(s string, openIdx int) int {
	depth := 0
	inSQ := false
	for i := openIdx; i < len(s); i++ {
		c := s[i]
		if inSQ {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++
				} else {
					inSQ = false
				}
			}
			continue
		}
		switch c {
		case '\'':
			inSQ = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitColumnDefs splits the inner column-list text into ColumnNodes.
// It uses the format lexer to correctly handle:
//   - Blank lines before section comments (sets BlankLineBefore)
//   - Line and block comments as leading comments for the next column
//   - Nested parentheses (don't split on commas inside them)
//   - Dollar-quoted and single-quoted strings
func splitColumnDefs(inner string) []*ColumnNode {
	tokens := Lex("", []byte(inner))

	var cols []*ColumnNode

	// Current accumulator state.
	var (
		pendingComments []string
		blankBefore     bool // blank line seen before any content in this chunk
		blankPending    bool // two consecutive newlines seen (blank line)
		prevNewline     bool // last meaningful token was a newline
		colText         strings.Builder
		hasText         bool // non-WS/comment content written to colText
		depth           int
	)

	flush := func() {
		text := strings.TrimSpace(colText.String())
		if text != "" || len(pendingComments) > 0 {
			cols = append(cols, &ColumnNode{
				BlankLineBefore: blankBefore,
				LeadingComments: pendingComments,
				RawText:         text,
			})
		}
		pendingComments = nil
		blankBefore = false
		blankPending = false
		prevNewline = false
		colText.Reset()
		hasText = false
	}

	for _, tok := range tokens {
		switch tok.Type {
		case TokEOF:
			flush()

		case TokNewline:
			if prevNewline {
				blankPending = true
			}
			prevNewline = true
			if hasText {
				colText.WriteString(tok.Text)
			}

		case TokWhitespace:
			if hasText {
				colText.WriteString(tok.Text)
			}
			// Don't update prevNewline — whitespace doesn't break newline tracking.

		case TokLineComment, TokBlockComment:
			if !hasText {
				// Leading comment for the upcoming column.
				if blankPending {
					blankBefore = true
					blankPending = false
				}
				pendingComments = append(pendingComments, tok.Text)
			} else {
				colText.WriteString(tok.Text)
			}
			prevNewline = false

		case TokComma:
			if depth == 0 {
				flush()
			} else {
				colText.WriteString(tok.Text)
				hasText = true
			}
			prevNewline = false

		case TokLParen:
			depth++
			if blankPending && !hasText {
				blankBefore = true
				blankPending = false
			}
			colText.WriteString(tok.Text)
			hasText = true
			prevNewline = false

		case TokRParen:
			depth--
			if depth >= 0 {
				colText.WriteString(tok.Text)
			}
			prevNewline = false

		default:
			if blankPending && !hasText {
				blankBefore = true
				blankPending = false
			}
			colText.WriteString(tok.Text)
			hasText = true
			prevNewline = false
		}
	}

	return cols
}
