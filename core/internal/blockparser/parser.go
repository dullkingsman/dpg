// Package blockparser implements pipeline.BlockParser. It parses the raw text
// from a DPG { } block into a pipeline.BlockAST, handling all Part 2 directives
// defined in the DPG RFC §7.
package blockparser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	pipeline.Default.Register(pipeline.KeyBlockParser, New())
}

// Parser implements pipeline.BlockParser.
type Parser struct{}

// New returns a Parser ready to use.
func New() *Parser { return &Parser{} }

// Parse implements pipeline.BlockParser. part2 is the raw text INSIDE the { }
// braces (not including the braces themselves). pos is the position of the
// original declaration.
func (p *Parser) Parse(kind pipeline.ObjectKind, part2 string, pos pipeline.SourcePos) (pipeline.BlockAST, error) {
	if strings.TrimSpace(part2) == "" {
		return pipeline.BlockAST{Pos: pos}, nil
	}
	bp := &blockParser{
		src:  []byte(part2),
		file: pos.File,
		line: pos.Line,
		col:  pos.Col,
	}
	return bp.parseBlock(pos)
}

// ── internal parser ───────────────────────────────────────────────────────────

type blockParser struct {
	src  []byte
	pos  int
	file string
	line int
	col  int
}

type bpCursor struct{ pos, line, col int }

func (b *blockParser) cur() bpCursor { return bpCursor{b.pos, b.line, b.col} }
func (b *blockParser) restore(c bpCursor) {
	b.pos = c.pos
	b.line = c.line
	b.col = c.col
}

func (b *blockParser) eof() bool { return b.pos >= len(b.src) }

func (b *blockParser) peek() byte {
	if b.eof() {
		return 0
	}
	return b.src[b.pos]
}

func (b *blockParser) peekAt(n int) byte {
	if b.pos+n >= len(b.src) {
		return 0
	}
	return b.src[b.pos+n]
}

func (b *blockParser) advance() byte {
	if b.eof() {
		return 0
	}
	ch := b.src[b.pos]
	b.pos++
	if ch == '\n' {
		b.line++
		b.col = 1
	} else {
		b.col++
	}
	return ch
}

func (b *blockParser) srcPos() pipeline.SourcePos {
	return pipeline.SourcePos{File: b.file, Line: b.line, Col: b.col}
}

func (b *blockParser) errorf(format string, args ...any) error {
	pos := b.srcPos()
	return pipeline.Errorf(pos, format, args...)
}

// ── whitespace, comments, strings ────────────────────────────────────────────

func isWordStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}
func isWordChar(ch byte) bool { return isWordStart(ch) || (ch >= '0' && ch <= '9') }
func isDigit(ch byte) bool    { return ch >= '0' && ch <= '9' }

func (b *blockParser) skipWS() {
	for !b.eof() {
		switch b.peek() {
		case ' ', '\t', '\r', '\n':
			b.advance()
		case '-':
			if b.peekAt(1) == '-' {
				for !b.eof() && b.peek() != '\n' {
					b.advance()
				}
			} else {
				return
			}
		case '/':
			if b.peekAt(1) == '*' {
				b.advance()
				b.advance()
				for !b.eof() {
					if b.peek() == '*' && b.peekAt(1) == '/' {
						b.advance()
						b.advance()
						break
					}
					b.advance()
				}
			} else {
				return
			}
		default:
			return
		}
	}
}

func (b *blockParser) readWord() string {
	var buf []byte
	for !b.eof() && isWordChar(b.peek()) {
		buf = append(buf, b.advance())
	}
	return string(buf)
}

func (b *blockParser) peekWord() string {
	c := b.cur()
	w := b.readWord()
	b.restore(c)
	return w
}

// readQuotedString reads a double-quoted DPG string literal (RFC §3).
// The opening " must NOT have been consumed. Returns the unquoted content.
func (b *blockParser) readQuotedString() (string, error) {
	if b.peek() != '"' {
		return "", b.errorf("expected '\"', got %q", b.peek())
	}
	b.advance() // consume "
	var buf []byte
	for !b.eof() {
		ch := b.advance()
		if ch == '"' {
			if b.peek() == '"' {
				buf = append(buf, '"')
				b.advance()
			} else {
				return string(buf), nil
			}
		} else {
			buf = append(buf, ch)
		}
	}
	return "", b.errorf("unterminated string literal")
}

// readSingleQuotedString reads a SQL single-quoted string. Opening ' must not be consumed.
func (b *blockParser) readSingleQuotedString() (string, error) {
	if b.peek() != '\'' {
		return "", b.errorf("expected \"'\", got %q", b.peek())
	}
	b.advance()
	var buf []byte
	for !b.eof() {
		ch := b.advance()
		if ch == '\'' {
			if b.peek() == '\'' {
				buf = append(buf, '\'')
				b.advance()
			} else {
				return string(buf), nil
			}
		} else {
			buf = append(buf, ch)
		}
	}
	return "", b.errorf("unterminated string literal")
}

// expect reads the next word and errors if it doesn't match.
func (b *blockParser) expect(word string) error {
	b.skipWS()
	got := b.readWord()
	if !strings.EqualFold(got, word) {
		return b.errorf("expected %s, got %q", word, got)
	}
	return nil
}

// expectSemi consumes the trailing ';' after a directive.
func (b *blockParser) expectSemi() error {
	b.skipWS()
	if b.peek() != ';' {
		return b.errorf("expected ';' after directive, got %q", b.peek())
	}
	b.advance()
	return nil
}

// readIdentifier reads a (possibly schema-qualified) identifier.
func (b *blockParser) readIdentifier() (pipeline.Identifier, error) {
	b.skipWS()
	var name string
	if b.peek() == '"' {
		s, err := b.readQuotedString()
		if err != nil {
			return pipeline.Identifier{}, err
		}
		name = s
	} else {
		name = b.readWord()
		if name == "" {
			return pipeline.Identifier{}, b.errorf("expected identifier, got %q", b.peek())
		}
	}
	// Check for schema.name
	if b.peek() == '.' {
		b.advance()
		b.skipWS()
		var n2 string
		if b.peek() == '"' {
			s, err := b.readQuotedString()
			if err != nil {
				return pipeline.Identifier{}, err
			}
			n2 = s
		} else {
			n2 = b.readWord()
		}
		return pipeline.Identifier{Schema: name, Name: n2}, nil
	}
	return pipeline.Identifier{Name: name}, nil
}

// readRawUntil reads raw bytes stopping at any byte in stopChars at brace/paren depth 0,
// outside strings and comments. Stop char is NOT consumed.
func (b *blockParser) readRawUntil(stopChars string) (string, error) {
	start := b.pos
	parenDepth := 0
	braceDepth := 0
	for !b.eof() {
		ch := b.peek()
		if parenDepth == 0 && braceDepth == 0 && strings.ContainsRune(stopChars, rune(ch)) {
			return string(b.src[start:b.pos]), nil
		}
		switch ch {
		case '(':
			parenDepth++
			b.advance()
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
			b.advance()
		case '{':
			braceDepth++
			b.advance()
		case '}':
			if braceDepth > 0 {
				braceDepth--
				b.advance()
			} else {
				return string(b.src[start:b.pos]), nil
			}
		case '\'':
			if _, err := b.readSingleQuotedString(); err != nil {
				return "", err
			}
		case '-':
			if b.peekAt(1) == '-' {
				for !b.eof() && b.peek() != '\n' {
					b.advance()
				}
			} else {
				b.advance()
			}
		case '/':
			if b.peekAt(1) == '*' {
				b.advance()
				b.advance()
				for !b.eof() {
					if b.peek() == '*' && b.peekAt(1) == '/' {
						b.advance()
						b.advance()
						break
					}
					b.advance()
				}
			} else {
				b.advance()
			}
		default:
			b.advance()
		}
	}
	return string(b.src[start:b.pos]), nil
}

// readBraceBlock reads the content of a { } block. The opening { must already
// have been consumed.
func (b *blockParser) readBraceBlock() (string, error) {
	start := b.pos
	depth := 1
	for !b.eof() {
		ch := b.peek()
		switch ch {
		case '{':
			depth++
			b.advance()
		case '}':
			depth--
			if depth == 0 {
				text := string(b.src[start:b.pos])
				b.advance() // consume }
				return text, nil
			}
			b.advance()
		case '\'':
			if _, err := b.readSingleQuotedString(); err != nil {
				return "", err
			}
		case '-':
			if b.peekAt(1) == '-' {
				for !b.eof() && b.peek() != '\n' {
					b.advance()
				}
			} else {
				b.advance()
			}
		case '/':
			if b.peekAt(1) == '*' {
				b.advance()
				b.advance()
				for !b.eof() {
					if b.peek() == '*' && b.peekAt(1) == '/' {
						b.advance()
						b.advance()
						break
					}
					b.advance()
				}
			} else {
				b.advance()
			}
		default:
			b.advance()
		}
	}
	return "", b.errorf("unterminated { } block")
}

// consumeBrace expects and consumes the next '{'.
func (b *blockParser) consumeBrace() error {
	b.skipWS()
	if b.peek() != '{' {
		return b.errorf("expected '{', got %q", b.peek())
	}
	b.advance()
	return nil
}

// ── top-level block parser ────────────────────────────────────────────────────

func (b *blockParser) parseBlock(pos pipeline.SourcePos) (pipeline.BlockAST, error) {
	ast := pipeline.BlockAST{Pos: pos}
	for {
		b.skipWS()
		if b.eof() {
			break
		}
		dirPos := b.srcPos()
		word := strings.ToUpper(b.readWord())
		if word == "" {
			return ast, b.errorf("unexpected character %q in block", b.peek())
		}

		var err error
		switch word {
		case "COMMENT":
			ast.Comment, err = b.parseStringDirective(dirPos)
		case "OWNER":
			ast.Owner, err = b.parseIdentDirective(dirPos)
		case "RENAMED":
			ast.RenamedFrom, err = b.parseRenamedFrom(dirPos)
		case "PROTECTED":
			ast.Protected = true
			err = b.expectSemi()
		case "DEPRECATED":
			ast.Deprecated, err = b.parseStringDirective(dirPos)
		case "DROP":
			err = b.parseDrop(&ast)
		case "ENABLE":
			err = b.parseEnable(&ast, dirPos)
		case "FORCE":
			err = b.parseForce(&ast, dirPos)
		case "INDICES", "INDEX":
			var indices []pipeline.IndexDef
			indices, err = b.parseIndices(dirPos)
			ast.Indices = append(ast.Indices, indices...)
		case "COLUMN":
			var col pipeline.ColumnBlock
			col, err = b.parseColumnBlock(dirPos)
			ast.Columns = append(ast.Columns, col)
		case "COLUMNS":
			var cols []pipeline.ColumnBlock
			cols, err = b.parseColumnsBlock(dirPos)
			ast.Columns = append(ast.Columns, cols...)
		case "CONSTRAINT":
			var cst pipeline.ConstraintDef
			cst, err = b.parseConstraint(dirPos)
			ast.Constraints = append(ast.Constraints, cst)
		case "POLICIES":
			var policies []pipeline.PolicyDef
			policies, err = b.parsePolicies(dirPos)
			ast.Policies = append(ast.Policies, policies...)
		case "TRIGGERS":
			var triggers []pipeline.TriggerDef
			triggers, err = b.parseTriggers(dirPos)
			ast.Triggers = append(ast.Triggers, triggers...)
		case "GRANTS", "GRANT":
			var grants []pipeline.GrantEntry
			grants, err = b.parseGrantsBlock(dirPos)
			ast.Grants = append(ast.Grants, grants...)
		case "REVOCATIONS", "REVOCATION":
			var revs []pipeline.RevocationEntry
			revs, err = b.parseRevocationsBlock(dirPos)
			ast.Revocations = append(ast.Revocations, revs...)
		case "PARTITIONS":
			ast.Partitions, err = b.parsePartitionsBlock(dirPos)
		case "MIGRATE":
			ast.MigrateRemove, err = b.parseMigrateRemove(dirPos)
		case "DEFAULT":
			var dp pipeline.DefaultPrivilegesBlock
			dp, err = b.parseDefaultPrivileges(dirPos)
			ast.DefaultPrivileges = append(ast.DefaultPrivileges, dp)
		case "MAPPING":
			var m pipeline.TSMappingDef
			m, err = b.parseTSMapping(dirPos)
			ast.Mappings = append(ast.Mappings, m)
		case "STATISTICS":
			// STATISTICS can appear at column level; at object level it's unusual
			// but parse it for error resilience.
			n, e2 := b.parseStatisticsValue(dirPos)
			if e2 != nil {
				err = e2
			} else {
				_ = n // statistics at object level is ignored; only meaningful in COLUMN
			}
		case "PREFERRED":
			ast.PreferredJsonFormat, err = b.parsePreferredJsonFormat(dirPos)
		case "NAME":
			b.skipWS()
			w2 := strings.ToUpper(b.readWord())
			switch w2 {
			case "MAP":
				var entry pipeline.NameMapEntry
				entry, err = b.parseNameMapSingular(dirPos)
				if err == nil {
					ast.NameMaps = append(ast.NameMaps, entry)
				}
			case "MAPS":
				var entries []pipeline.NameMapEntry
				entries, err = b.parseNameMapsBlock(dirPos)
				if err == nil {
					ast.NameMaps = append(ast.NameMaps, entries...)
				}
			default:
				err = fmt.Errorf("%s: expected MAP or MAPS after NAME, got %q", dirPos, w2)
			}
		default:
			return ast, fmt.Errorf("%s: unknown block directive %q", dirPos, word)
		}
		if err != nil {
			return ast, err
		}
	}
	return ast, nil
}

// ── simple directives ─────────────────────────────────────────────────────────

// parsePreferredJsonFormat reads: JSON FORMAT ( json | jsonb ) ;
func (b *blockParser) parsePreferredJsonFormat(pos pipeline.SourcePos) (string, error) {
	b.skipWS()
	if strings.ToUpper(b.readWord()) != "JSON" {
		return "", b.errorf("PREFERRED: expected JSON after PREFERRED at %s", pos)
	}
	b.skipWS()
	if strings.ToUpper(b.readWord()) != "FORMAT" {
		return "", b.errorf("PREFERRED JSON: expected FORMAT at %s", pos)
	}
	b.skipWS()
	val := strings.ToLower(b.readWord())
	if val != "json" && val != "jsonb" {
		return "", b.errorf("PREFERRED JSON FORMAT: expected 'json' or 'jsonb', got %q at %s", val, pos)
	}
	if err := b.expectSemi(); err != nil {
		return "", err
	}
	return val, nil
}

// parseStringDirective reads: "text"; and returns a *StringLit.
func (b *blockParser) parseStringDirective(pos pipeline.SourcePos) (*pipeline.StringLit, error) {
	b.skipWS()
	val, err := b.readQuotedString()
	if err != nil {
		return nil, err
	}
	if err := b.expectSemi(); err != nil {
		return nil, err
	}
	return &pipeline.StringLit{Value: val, Pos: pos}, nil
}

// parseIdentDirective reads: "name"; or name; and returns a *Identifier.
func (b *blockParser) parseIdentDirective(pos pipeline.SourcePos) (*pipeline.Identifier, error) {
	b.skipWS()
	var name string
	var err error
	if b.peek() == '"' {
		name, err = b.readQuotedString()
		if err != nil {
			return nil, err
		}
	} else {
		name = b.readWord()
		if name == "" {
			return nil, b.errorf("expected identifier after directive")
		}
	}
	if err := b.expectSemi(); err != nil {
		return nil, err
	}
	return &pipeline.Identifier{Name: name}, nil
}

// parseRenamedFrom reads: FROM old_name;
func (b *blockParser) parseRenamedFrom(pos pipeline.SourcePos) (*pipeline.Identifier, error) {
	if err := b.expect("FROM"); err != nil {
		return nil, err
	}
	b.skipWS()
	id, err := b.readIdentifier()
	if err != nil {
		return nil, err
	}
	if err := b.expectSemi(); err != nil {
		return nil, err
	}
	return &id, nil
}

// parseDrop reads: CASCADE;
func (b *blockParser) parseDrop(ast *pipeline.BlockAST) error {
	if err := b.expect("CASCADE"); err != nil {
		return err
	}
	ast.DropCascade = true
	return b.expectSemi()
}

// parseEnable reads: ROW LEVEL SECURITY;
func (b *blockParser) parseEnable(ast *pipeline.BlockAST, pos pipeline.SourcePos) error {
	b.skipWS()
	w := strings.ToUpper(b.readWord())
	if w != "ROW" {
		return fmt.Errorf("%s: expected ROW LEVEL SECURITY after ENABLE, got %q", pos, w)
	}
	if err := b.expect("LEVEL"); err != nil {
		return err
	}
	if err := b.expect("SECURITY"); err != nil {
		return err
	}
	ast.EnableRLS = true
	return b.expectSemi()
}

// parseForce reads: ROW LEVEL SECURITY;
func (b *blockParser) parseForce(ast *pipeline.BlockAST, pos pipeline.SourcePos) error {
	b.skipWS()
	w := strings.ToUpper(b.readWord())
	if w != "ROW" {
		return fmt.Errorf("%s: expected ROW LEVEL SECURITY after FORCE, got %q", pos, w)
	}
	if err := b.expect("LEVEL"); err != nil {
		return err
	}
	if err := b.expect("SECURITY"); err != nil {
		return err
	}
	ast.ForceRLS = true
	return b.expectSemi()
}

// ── INDICES ───────────────────────────────────────────────────────────────────

// parseIndices reads: { idx1 [UNIQUE] (cols) [USING m] [WHERE pred] [INCLUDE (...)] [WITH (...)] [TABLESPACE t] [CONCURRENTLY bool]; ... }
func (b *blockParser) parseIndices(pos pipeline.SourcePos) ([]pipeline.IndexDef, error) {
	if err := b.consumeBrace(); err != nil {
		return nil, err
	}
	var indices []pipeline.IndexDef
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		idx, err := b.parseOneIndex()
		if err != nil {
			return nil, err
		}
		indices = append(indices, idx)
	}
	b.skipWS()
	if b.peek() != '}' {
		return nil, b.errorf("expected '}' to close INDICES block")
	}
	b.advance()
	return indices, nil
}

func (b *blockParser) parseOneIndex() (pipeline.IndexDef, error) {
	pos := b.srcPos()
	name, err := b.readIdentifier()
	if err != nil {
		return pipeline.IndexDef{}, err
	}
	idx := pipeline.IndexDef{Name: name, Pos: pos}

	b.skipWS()
	// Check optional UNIQUE keyword
	c := b.cur()
	w := strings.ToUpper(b.readWord())
	if w == "UNIQUE" {
		idx.Unique = true
	} else {
		b.restore(c)
	}

	// Expect (columns)
	b.skipWS()
	if b.peek() != '(' {
		return idx, b.errorf("expected '(' for index columns after index name %s", name)
	}
	b.advance() // consume (
	colsRaw, err := b.readRawUntil(")")
	if err != nil {
		return idx, err
	}
	b.advance() // consume )
	idx.Columns = parseIndexColumns(colsRaw)

	// Parse optional clauses
	for {
		b.skipWS()
		c := b.cur()
		kw := strings.ToUpper(b.peekWord())
		switch kw {
		case "USING":
			b.readWord()
			b.skipWS()
			method, err2 := b.readIdentifier()
			if err2 != nil {
				return idx, err2
			}
			idx.Method = &method
		case "WHERE":
			b.readWord()
			b.skipWS()
			raw, err2 := b.readRawUntil(";,}")
			if err2 != nil {
				return idx, err2
			}
			idx.Where = &pipeline.RawExpr{Text: strings.TrimSpace(raw), Pos: b.srcPos()}
		case "INCLUDE":
			b.readWord()
			b.skipWS()
			if b.peek() != '(' {
				return idx, b.errorf("expected '(' after INCLUDE")
			}
			b.advance()
			raw, err2 := b.readRawUntil(")")
			if err2 != nil {
				return idx, err2
			}
			b.advance()
			for _, s := range strings.Split(raw, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					idx.Include = append(idx.Include, pipeline.Identifier{Name: s})
				}
			}
		case "WITH":
			b.readWord()
			b.skipWS()
			if b.peek() != '(' {
				return idx, b.errorf("expected '(' after WITH")
			}
			b.advance()
			raw, err2 := b.readRawUntil(")")
			if err2 != nil {
				return idx, err2
			}
			b.advance()
			idx.With = parseStorageParams(raw)
		case "TABLESPACE":
			b.readWord()
			b.skipWS()
			ts, err2 := b.readIdentifier()
			if err2 != nil {
				return idx, err2
			}
			idx.Tablespace = &ts
		case "CONCURRENTLY":
			b.readWord()
			b.skipWS()
			boolWord := strings.ToUpper(b.readWord())
			idx.Concurrently = boolWord != "FALSE" && boolWord != "0"
		default:
			b.restore(c)
			goto doneIndexClauses
		}
	}
doneIndexClauses:

	b.skipWS()
	if b.peek() == ';' {
		b.advance()
	}
	return idx, nil
}

func parseIndexColumns(raw string) []pipeline.IndexColumn {
	var cols []pipeline.IndexColumn
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cols = append(cols, pipeline.IndexColumn{Name: part})
	}
	return cols
}

func parseStorageParams(raw string) []pipeline.StorageParam {
	var params []pipeline.StorageParam
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if kv := strings.SplitN(part, "=", 2); len(kv) == 2 {
			params = append(params, pipeline.StorageParam{
				Key:   strings.TrimSpace(kv[0]),
				Value: strings.TrimSpace(kv[1]),
			})
		}
	}
	return params
}

// ── COLUMN / COLUMNS ──────────────────────────────────────────────────────────

func (b *blockParser) parseColumnBlock(pos pipeline.SourcePos) (pipeline.ColumnBlock, error) {
	b.skipWS()
	name, err := b.readIdentifier()
	if err != nil {
		return pipeline.ColumnBlock{}, err
	}
	col := pipeline.ColumnBlock{Name: name, Pos: pos}

	if err := b.consumeBrace(); err != nil {
		return col, err
	}
	if err := b.fillColumnBlock(&col); err != nil {
		return col, err
	}
	b.skipWS()
	if b.peek() != '}' {
		return col, b.errorf("expected '}' to close COLUMN %s block", name)
	}
	b.advance()
	// Optional trailing ;
	b.skipWS()
	if b.peek() == ';' {
		b.advance()
	}
	return col, nil
}

func (b *blockParser) parseColumnsBlock(pos pipeline.SourcePos) ([]pipeline.ColumnBlock, error) {
	if err := b.consumeBrace(); err != nil {
		return nil, err
	}
	var cols []pipeline.ColumnBlock
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		dirPos := b.srcPos()
		col, err := b.parseColumnBlock(dirPos)
		if err != nil {
			return nil, err
		}
		cols = append(cols, col)
	}
	b.skipWS()
	if b.peek() != '}' {
		return nil, b.errorf("expected '}' to close COLUMNS block")
	}
	b.advance()
	return cols, nil
}

func (b *blockParser) fillColumnBlock(col *pipeline.ColumnBlock) error {
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		dirPos := b.srcPos()
		word := strings.ToUpper(b.readWord())
		var err error
		switch word {
		case "COMMENT":
			col.Comment, err = b.parseStringDirective(dirPos)
		case "STATISTICS":
			n, e2 := b.parseStatisticsValue(dirPos)
			if e2 != nil {
				err = e2
			} else {
				col.Statistics = &n
			}
		case "COMPRESSION":
			col.Compression, err = b.parseIdentDirective(dirPos)
		case "STORAGE":
			col.Storage, err = b.parseIdentDirective(dirPos)
		case "DEPRECATED":
			col.Deprecated, err = b.parseStringDirective(dirPos)
		case "RENAMED":
			col.RenamedFrom, err = b.parseRenamedFrom(dirPos)
		case "USING":
			b.skipWS()
			raw, e2 := b.readRawUntil(";")
			if e2 != nil {
				err = e2
			} else {
				col.Using = &pipeline.RawExpr{Text: strings.TrimSpace(raw), Pos: dirPos}
				b.advance() // consume ;
			}
		case "GRANTS", "GRANT":
			grants, e2 := b.parseGrantsBlock(dirPos)
			if e2 != nil {
				err = e2
			} else {
				col.Grants = append(col.Grants, grants...)
			}
		case "REVOCATIONS", "REVOCATION":
			revs, e2 := b.parseRevocationsBlock(dirPos)
			if e2 != nil {
				err = e2
			} else {
				col.Revocations = append(col.Revocations, revs...)
			}
		case "NAME":
			b.skipWS()
			w2 := strings.ToUpper(b.readWord())
			switch w2 {
			case "MAP":
				entry, e2 := b.parseNameMapSingular(dirPos)
				if e2 != nil {
					err = e2
				} else {
					col.NameMaps = append(col.NameMaps, entry)
				}
			case "MAPS":
				entries, e2 := b.parseNameMapsBlock(dirPos)
				if e2 != nil {
					err = e2
				} else {
					col.NameMaps = append(col.NameMaps, entries...)
				}
			default:
				err = fmt.Errorf("%s: expected MAP or MAPS after NAME, got %q", dirPos, w2)
			}
		default:
			return fmt.Errorf("%s: unknown column directive %q", dirPos, word)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *blockParser) parseStatisticsValue(pos pipeline.SourcePos) (int, error) {
	b.skipWS()
	var buf []byte
	for !b.eof() && isDigit(b.peek()) {
		buf = append(buf, b.advance())
	}
	if len(buf) == 0 {
		return 0, fmt.Errorf("%s: expected integer after STATISTICS", pos)
	}
	n, err := strconv.Atoi(string(buf))
	if err != nil {
		return 0, fmt.Errorf("%s: invalid STATISTICS value: %w", pos, err)
	}
	if err := b.expectSemi(); err != nil {
		return 0, err
	}
	return n, nil
}

// ── CONSTRAINT ────────────────────────────────────────────────────────────────

func (b *blockParser) parseConstraint(pos pipeline.SourcePos) (pipeline.ConstraintDef, error) {
	b.skipWS()
	name, err := b.readIdentifier()
	if err != nil {
		return pipeline.ConstraintDef{}, err
	}
	cst := pipeline.ConstraintDef{Name: name, Pos: pos}

	// Read everything up to ';' or "NOT VALID;"
	raw, err := b.readRawUntil(";")
	if err != nil {
		return cst, err
	}
	raw = strings.TrimSpace(raw)
	// Check for NOT VALID suffix
	upper := strings.ToUpper(raw)
	if strings.HasSuffix(upper, "NOT VALID") {
		cst.NotValid = true
		raw = strings.TrimSpace(raw[:len(raw)-len("NOT VALID")])
	}
	cst.Expr = pipeline.RawExpr{Text: raw, Pos: pos}
	b.advance() // consume ;
	return cst, nil
}

// ── POLICIES ─────────────────────────────────────────────────────────────────

func (b *blockParser) parsePolicies(pos pipeline.SourcePos) ([]pipeline.PolicyDef, error) {
	if err := b.consumeBrace(); err != nil {
		return nil, err
	}
	var policies []pipeline.PolicyDef
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		pol, err := b.parseOnePolicy()
		if err != nil {
			return nil, err
		}
		policies = append(policies, pol)
	}
	b.skipWS()
	if b.peek() != '}' {
		return nil, b.errorf("expected '}' to close POLICIES block")
	}
	b.advance()
	return policies, nil
}

// Policy syntax: name [FOR command] [AS PERMISSIVE|RESTRICTIVE]
//
//	[TO role, ...]
//	[USING (expr)]
//	[WITH CHECK (expr)];
func (b *blockParser) parseOnePolicy() (pipeline.PolicyDef, error) {
	pos := b.srcPos()
	name, err := b.readIdentifier()
	if err != nil {
		return pipeline.PolicyDef{}, err
	}
	pol := pipeline.PolicyDef{Name: name, Permissive: true, Pos: pos}

	for {
		b.skipWS()
		c := b.cur()
		kw := strings.ToUpper(b.readWord())
		switch kw {
		case "FOR":
			b.skipWS()
			pol.Command = strings.ToUpper(b.readWord())
		case "AS":
			b.skipWS()
			perm := strings.ToUpper(b.readWord())
			pol.Permissive = perm != "RESTRICTIVE"
		case "TO":
			for {
				b.skipWS()
				r, err2 := b.readIdentifier()
				if err2 != nil {
					return pol, err2
				}
				pol.Roles = append(pol.Roles, r)
				b.skipWS()
				if b.peek() != ',' {
					break
				}
				b.advance()
			}
		case "USING":
			b.skipWS()
			if b.peek() != '(' {
				return pol, b.errorf("expected '(' after USING")
			}
			b.advance()
			raw, err2 := b.readRawUntil(")")
			if err2 != nil {
				return pol, err2
			}
			b.advance()
			pol.Using = &pipeline.RawExpr{Text: strings.TrimSpace(raw), Pos: pos}
		case "WITH":
			if err := b.expect("CHECK"); err != nil {
				return pol, err
			}
			b.skipWS()
			if b.peek() != '(' {
				return pol, b.errorf("expected '(' after WITH CHECK")
			}
			b.advance()
			raw, err2 := b.readRawUntil(")")
			if err2 != nil {
				return pol, err2
			}
			b.advance()
			pol.WithCheck = &pipeline.RawExpr{Text: strings.TrimSpace(raw), Pos: pos}
		default:
			b.restore(c)
			goto donePolicy
		}
	}
donePolicy:
	b.skipWS()
	if b.peek() == ';' {
		b.advance()
	}
	return pol, nil
}

// ── TRIGGERS ─────────────────────────────────────────────────────────────────

func (b *blockParser) parseTriggers(pos pipeline.SourcePos) ([]pipeline.TriggerDef, error) {
	if err := b.consumeBrace(); err != nil {
		return nil, err
	}
	var triggers []pipeline.TriggerDef
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		trig, err := b.parseOneTrigger()
		if err != nil {
			return nil, err
		}
		triggers = append(triggers, trig)
	}
	b.skipWS()
	if b.peek() != '}' {
		return nil, b.errorf("expected '}' to close TRIGGERS block")
	}
	b.advance()
	return triggers, nil
}

// Trigger syntax: name BEFORE|AFTER|INSTEAD OF event[, event]
//
//	FOR EACH ROW|STATEMENT
//	[WHEN (cond)]
//	EXECUTE FUNCTION func_name(args);
func (b *blockParser) parseOneTrigger() (pipeline.TriggerDef, error) {
	pos := b.srcPos()
	name, err := b.readIdentifier()
	if err != nil {
		return pipeline.TriggerDef{}, err
	}
	trig := pipeline.TriggerDef{Name: name, Pos: pos}

	// Timing: BEFORE | AFTER | INSTEAD OF
	b.skipWS()
	timing := strings.ToUpper(b.readWord())
	switch timing {
	case "BEFORE", "AFTER":
		trig.When = timing
	case "INSTEAD":
		if err := b.expect("OF"); err != nil {
			return trig, err
		}
		trig.When = "INSTEAD OF"
	default:
		return trig, b.errorf("expected BEFORE/AFTER/INSTEAD OF, got %q", timing)
	}

	// Events: INSERT | UPDATE [OF cols] | DELETE | TRUNCATE [OR ...]
	for {
		b.skipWS()
		evt := strings.ToUpper(b.readWord())
		switch evt {
		case "INSERT", "DELETE", "TRUNCATE":
			trig.Events = append(trig.Events, evt)
		case "UPDATE":
			trig.Events = append(trig.Events, "UPDATE")
			// Optional OF col, col, ...
			b.skipWS()
			c := b.cur()
			if strings.ToUpper(b.peekWord()) == "OF" {
				b.readWord()
				for {
					b.skipWS()
					b.readWord() // column name (discard; stored in Part1 trigger def if needed)
					b.skipWS()
					if b.peek() != ',' {
						break
					}
					b.advance()
					// Check not OR
					b.skipWS()
					if strings.ToUpper(b.peekWord()) != "" &&
						!strings.EqualFold(b.peekWord(), "OR") {
						continue
					}
					break
				}
			} else {
				b.restore(c)
			}
		default:
			return trig, b.errorf("expected trigger event, got %q", evt)
		}
		b.skipWS()
		if strings.ToUpper(b.peekWord()) == "OR" {
			b.readWord()
		} else {
			break
		}
	}

	// FOR EACH ROW | STATEMENT
	if err := b.expect("FOR"); err != nil {
		return trig, err
	}
	if err := b.expect("EACH"); err != nil {
		return trig, err
	}
	b.skipWS()
	trig.ForEach = strings.ToUpper(b.readWord())

	// Optional WHEN (cond)
	b.skipWS()
	c := b.cur()
	if strings.ToUpper(b.peekWord()) == "WHEN" {
		b.readWord()
		b.skipWS()
		if b.peek() != '(' {
			return trig, b.errorf("expected '(' after WHEN")
		}
		b.advance()
		raw, err2 := b.readRawUntil(")")
		if err2 != nil {
			return trig, err2
		}
		b.advance()
		trig.Condition = &pipeline.RawExpr{Text: strings.TrimSpace(raw), Pos: pos}
	} else {
		b.restore(c)
	}

	// EXECUTE FUNCTION func_name(args)
	if err := b.expect("EXECUTE"); err != nil {
		return trig, err
	}
	b.skipWS()
	// FUNCTION or PROCEDURE
	b.readWord()
	b.skipWS()
	fn, err := b.readIdentifier()
	if err != nil {
		return trig, err
	}
	trig.Function = fn
	// Args
	b.skipWS()
	if b.peek() == '(' {
		b.advance()
		raw, err2 := b.readRawUntil(")")
		if err2 != nil {
			return trig, err2
		}
		b.advance()
		for _, a := range strings.Split(raw, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				trig.Args = append(trig.Args, a)
			}
		}
	}

	b.skipWS()
	if b.peek() == ';' {
		b.advance()
	}
	return trig, nil
}

// ── GRANTS ────────────────────────────────────────────────────────────────────

// GRANTS { SELECT, INSERT TO role1, role2; ... }
func (b *blockParser) parseGrantsBlock(pos pipeline.SourcePos) ([]pipeline.GrantEntry, error) {
	if err := b.consumeBrace(); err != nil {
		return nil, err
	}
	var grants []pipeline.GrantEntry
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		g, err := b.parseOneGrant(pos)
		if err != nil {
			return nil, err
		}
		grants = append(grants, g)
	}
	b.skipWS()
	if b.peek() != '}' {
		return nil, b.errorf("expected '}' to close GRANTS block")
	}
	b.advance()
	return grants, nil
}

// Syntax: [ALL PRIVILEGES | priv1, priv2] TO role1[, role2] [WITH GRANT OPTION];
func (b *blockParser) parseOneGrant(pos pipeline.SourcePos) (pipeline.GrantEntry, error) {
	g := pipeline.GrantEntry{Pos: pos}

	// Privileges
	b.skipWS()
	c := b.cur()
	first := strings.ToUpper(b.readWord())
	if first == "ALL" {
		b.skipWS()
		// optional PRIVILEGES keyword
		c2 := b.cur()
		if strings.ToUpper(b.peekWord()) == "PRIVILEGES" {
			b.readWord()
		} else {
			b.restore(c2)
		}
		g.Privileges = nil // nil = ALL
	} else {
		b.restore(c)
		// Read comma-separated privileges
		for {
			b.skipWS()
			priv := strings.ToUpper(b.readWord())
			if priv == "" {
				break
			}
			g.Privileges = append(g.Privileges, priv)
			b.skipWS()
			if b.peek() != ',' {
				break
			}
			b.advance()
			// Stop if next token is TO
			b.skipWS()
			if strings.ToUpper(b.peekWord()) == "TO" {
				break
			}
		}
	}

	// TO
	if err := b.expect("TO"); err != nil {
		return g, err
	}

	// Roles
	for {
		b.skipWS()
		r, err := b.readIdentifier()
		if err != nil {
			return g, err
		}
		g.Roles = append(g.Roles, r)
		b.skipWS()
		if b.peek() != ',' {
			break
		}
		b.advance()
		b.skipWS()
		if strings.ToUpper(b.peekWord()) == "WITH" {
			break
		}
	}

	// Optional WITH GRANT OPTION
	b.skipWS()
	c = b.cur()
	if strings.ToUpper(b.peekWord()) == "WITH" {
		b.readWord()
		b.skipWS()
		if strings.ToUpper(b.peekWord()) == "GRANT" {
			b.readWord()
			if err := b.expect("OPTION"); err != nil {
				return g, err
			}
			g.WithGrant = true
		} else {
			b.restore(c)
		}
	}

	b.skipWS()
	if b.peek() == ';' {
		b.advance()
	}
	return g, nil
}

// ── REVOCATIONS ───────────────────────────────────────────────────────────────

func (b *blockParser) parseRevocationsBlock(pos pipeline.SourcePos) ([]pipeline.RevocationEntry, error) {
	if err := b.consumeBrace(); err != nil {
		return nil, err
	}
	var revs []pipeline.RevocationEntry
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		r, err := b.parseOneRevocation(pos)
		if err != nil {
			return nil, err
		}
		revs = append(revs, r)
	}
	b.skipWS()
	if b.peek() != '}' {
		return nil, b.errorf("expected '}' to close REVOCATIONS block")
	}
	b.advance()
	return revs, nil
}

// Syntax: [ALL PRIVILEGES | priv1, priv2] FROM role1[, role2] [CASCADE];
func (b *blockParser) parseOneRevocation(pos pipeline.SourcePos) (pipeline.RevocationEntry, error) {
	r := pipeline.RevocationEntry{Pos: pos}

	b.skipWS()
	c := b.cur()
	first := strings.ToUpper(b.readWord())
	if first == "ALL" {
		b.skipWS()
		c2 := b.cur()
		if strings.ToUpper(b.peekWord()) == "PRIVILEGES" {
			b.readWord()
		} else {
			b.restore(c2)
		}
		r.Privileges = nil
	} else {
		b.restore(c)
		for {
			b.skipWS()
			priv := strings.ToUpper(b.readWord())
			if priv == "" {
				break
			}
			r.Privileges = append(r.Privileges, priv)
			b.skipWS()
			if b.peek() != ',' {
				break
			}
			b.advance()
			b.skipWS()
			if strings.ToUpper(b.peekWord()) == "FROM" {
				break
			}
		}
	}

	if err := b.expect("FROM"); err != nil {
		return r, err
	}

	for {
		b.skipWS()
		role, err := b.readIdentifier()
		if err != nil {
			return r, err
		}
		r.Roles = append(r.Roles, role)
		b.skipWS()
		if b.peek() != ',' {
			break
		}
		b.advance()
		b.skipWS()
		if strings.ToUpper(b.peekWord()) == "CASCADE" {
			break
		}
	}

	b.skipWS()
	c = b.cur()
	if strings.ToUpper(b.peekWord()) == "CASCADE" {
		b.readWord()
		r.Cascade = true
	} else {
		b.restore(c)
	}

	b.skipWS()
	if b.peek() == ';' {
		b.advance()
	}
	return r, nil
}

// ── PARTITIONS ────────────────────────────────────────────────────────────────

func (b *blockParser) parsePartitionsBlock(pos pipeline.SourcePos) (*pipeline.PartitionDef, error) {
	if err := b.consumeBrace(); err != nil {
		return nil, err
	}
	pd := &pipeline.PartitionDef{Pos: pos}
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		pPos := b.srcPos()
		name, err := b.readIdentifier()
		if err != nil {
			return nil, err
		}
		// Read everything up to ';' or end of block as bounds
		raw, err := b.readRawUntil(";}")
		if err != nil {
			return nil, err
		}
		if b.peek() == ';' {
			b.advance()
		}
		pd.Partitions = append(pd.Partitions, pipeline.PartitionBound{
			Name:   name,
			Bounds: pipeline.RawExpr{Text: strings.TrimSpace(raw), Pos: pPos},
			Pos:    pPos,
		})
	}
	b.skipWS()
	if b.peek() != '}' {
		return nil, b.errorf("expected '}' to close PARTITIONS block")
	}
	b.advance()
	return pd, nil
}

// ── MIGRATE REMOVE ────────────────────────────────────────────────────────────

func (b *blockParser) parseMigrateRemove(pos pipeline.SourcePos) (*pipeline.MigrateRemoveBlock, error) {
	if err := b.expect("REMOVE"); err != nil {
		return nil, err
	}
	// Optional reason in parens
	b.skipWS()
	var reason string
	if b.peek() == '(' {
		b.advance()
		raw, err := b.readRawUntil(")")
		if err != nil {
			return nil, err
		}
		b.advance()
		reason = strings.TrimSpace(raw)
	}
	// consumeBrace consumes '{'; readBraceBlock reads content until '}' (inclusive).
	if err := b.consumeBrace(); err != nil {
		return nil, err
	}
	sqlRaw, err := b.readBraceBlock()
	if err != nil {
		return nil, err
	}
	return &pipeline.MigrateRemoveBlock{
		Reason: reason,
		SQL:    pipeline.RawExpr{Text: strings.TrimSpace(sqlRaw), Pos: pos},
		Pos:    pos,
	}, nil
}

// ── DEFAULT PRIVILEGES ────────────────────────────────────────────────────────

func (b *blockParser) parseDefaultPrivileges(pos pipeline.SourcePos) (pipeline.DefaultPrivilegesBlock, error) {
	if err := b.expect("PRIVILEGES"); err != nil {
		return pipeline.DefaultPrivilegesBlock{}, err
	}
	dp := pipeline.DefaultPrivilegesBlock{Pos: pos}
	// Optional IN SCHEMA name
	b.skipWS()
	c := b.cur()
	if strings.ToUpper(b.peekWord()) == "IN" {
		b.readWord()
		if err := b.expect("SCHEMA"); err != nil {
			return dp, err
		}
		b.skipWS()
		s, err := b.readIdentifier()
		if err != nil {
			return dp, err
		}
		dp.InSchema = &s
	} else {
		b.restore(c)
	}
	// Optional FOR ROLE name
	b.skipWS()
	c = b.cur()
	if strings.ToUpper(b.peekWord()) == "FOR" {
		b.readWord()
		b.skipWS()
		_ = b.readWord() // ROLE keyword
		b.skipWS()
		r, err := b.readIdentifier()
		if err != nil {
			return dp, err
		}
		dp.ForRole = &r
	} else {
		b.restore(c)
	}
	// FOR object_type
	b.skipWS()
	c = b.cur()
	if strings.ToUpper(b.peekWord()) == "FOR" {
		b.readWord()
		b.skipWS()
		dp.ObjectType = strings.ToUpper(b.readWord())
	} else {
		b.restore(c)
	}
	// { grants/revocations }
	if err := b.consumeBrace(); err != nil {
		return dp, err
	}
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		dirPos := b.srcPos()
		word := strings.ToUpper(b.readWord())
		switch word {
		case "GRANTS", "GRANT":
			grants, err := b.parseGrantsBlock(dirPos)
			if err != nil {
				return dp, err
			}
			dp.Grants = append(dp.Grants, grants...)
		case "REVOCATIONS", "REVOCATION":
			revs, err := b.parseRevocationsBlock(dirPos)
			if err != nil {
				return dp, err
			}
			dp.Revocations = append(dp.Revocations, revs...)
		default:
			return dp, fmt.Errorf("%s: unexpected directive %q in DEFAULT PRIVILEGES block", dirPos, word)
		}
	}
	b.skipWS()
	if b.peek() != '}' {
		return dp, b.errorf("expected '}' to close DEFAULT PRIVILEGES block")
	}
	b.advance()
	return dp, nil
}

// ── NAME MAP / NAME MAPS ──────────────────────────────────────────────────────

// parseNameMapSingular parses the tail of a NAME MAP directive:
//
//	TO <value> ;               (implicit "default" tool)
//	<tool> TO <value> ;        (explicit tool name)
func (b *blockParser) parseNameMapSingular(pos pipeline.SourcePos) (pipeline.NameMapEntry, error) {
	b.skipWS()
	c := b.cur()
	next := strings.ToUpper(b.peekWord())
	var tool string
	if next == "TO" {
		b.readWord() // consume TO
		tool = "default"
	} else {
		b.restore(c)
		tool = strings.ToLower(b.readWord())
		if tool == "" {
			return pipeline.NameMapEntry{}, b.errorf("expected tool name or TO after NAME MAP")
		}
		if err := b.expect("TO"); err != nil {
			return pipeline.NameMapEntry{}, err
		}
	}
	return b.parseNameMapValue(pos, tool)
}

// parseNameMapsBlock parses a grouped NAME MAPS { ... } block where each
// entry is: <tool> TO <value> ;
func (b *blockParser) parseNameMapsBlock(pos pipeline.SourcePos) ([]pipeline.NameMapEntry, error) {
	if err := b.consumeBrace(); err != nil {
		return nil, err
	}
	var entries []pipeline.NameMapEntry
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		entryPos := b.srcPos()
		tool := strings.ToLower(b.readWord())
		if tool == "" {
			return nil, b.errorf("expected tool name in NAME MAPS block")
		}
		if err := b.expect("TO"); err != nil {
			return nil, err
		}
		entry, err := b.parseNameMapValue(entryPos, tool)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	b.skipWS()
	if b.peek() != '}' {
		return nil, b.errorf("expected '}' to close NAME MAPS block")
	}
	b.advance()
	return entries, nil
}

// parseNameMapValue parses the value part of a NAME MAP directive:
//
//	"LiteralName" ;   → IsLiteral=true
//	RULE_KEYWORD ;    → IsLiteral=false, must be in ValidNameMapRules
func (b *blockParser) parseNameMapValue(pos pipeline.SourcePos, tool string) (pipeline.NameMapEntry, error) {
	b.skipWS()
	if b.peek() == '"' {
		name, err := b.readQuotedString()
		if err != nil {
			return pipeline.NameMapEntry{}, err
		}
		if err := b.expectSemi(); err != nil {
			return pipeline.NameMapEntry{}, err
		}
		return pipeline.NameMapEntry{Tool: tool, Value: name, IsLiteral: true, Pos: pos}, nil
	}
	rule := strings.ToUpper(b.readWord())
	if !pipeline.ValidNameMapRules[rule] {
		return pipeline.NameMapEntry{}, fmt.Errorf("%s: unknown name map rule %q; valid rules: LOWER_SNAKE_CASE, UPPER_SNAKE_CASE, LOWER_CAMEL_CASE, UPPER_CAMEL_CASE, LOWER_KEBAB_CASE, UPPER_KEBAB_CASE, TRAIN_CASE, LOWER_CASE, UPPER_CASE, PASCAL_SNAKE_CASE", pos, rule)
	}
	if err := b.expectSemi(); err != nil {
		return pipeline.NameMapEntry{}, err
	}
	return pipeline.NameMapEntry{Tool: tool, Value: rule, IsLiteral: false, Pos: pos}, nil
}

// ── TEXT SEARCH MAPPING ───────────────────────────────────────────────────────

// MAPPING FOR token_type [, ...] WITH dictionary;
func (b *blockParser) parseTSMapping(pos pipeline.SourcePos) (pipeline.TSMappingDef, error) {
	if err := b.expect("FOR"); err != nil {
		return pipeline.TSMappingDef{}, err
	}
	m := pipeline.TSMappingDef{Pos: pos}
	for {
		b.skipWS()
		tt := b.readWord()
		if tt == "" {
			break
		}
		m.TokenTypes = append(m.TokenTypes, tt)
		b.skipWS()
		if b.peek() != ',' {
			break
		}
		b.advance()
	}
	if err := b.expect("WITH"); err != nil {
		return m, err
	}
	b.skipWS()
	dict, err := b.readIdentifier()
	if err != nil {
		return m, err
	}
	m.Dictionary = dict
	if err := b.expectSemi(); err != nil {
		return m, err
	}
	return m, nil
}
