// Package blockparser implements pipeline.BlockParser. It parses the raw text
// from a DPG { } block into a pipeline.BlockAST, handling all Part 2 directives
// defined in the DPG RFC §7.
package blockparser

import (
	"fmt"
	"regexp"
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

// ParseDefaultPrivileges implements pipeline.BlockParser.
func (p *Parser) ParseDefaultPrivileges(header, body string, pos pipeline.SourcePos) (pipeline.DefaultPrivilegesBlock, error) {
	return ParseDefaultPrivileges(header, body, pos)
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

// parseTrailingCommentBlock parses an INDICES/POLICIES/TRIGGERS/CONSTRAINTS
// entry's terminator: a bare ";" (no comment), or a "{ COMMENT '...'; }"
// block — the same "bare ; vs { } block" convention every other
// comment-bearing kind already uses (mirrors dump's writeViewBlock/
// writeFuncBlock rendering), rather than a bespoke trailing clause. None of
// these four sub-kinds support any block-level directive today, so this is
// the minimal shared addition rather than one bespoke parser per kind.
func (b *blockParser) parseTrailingCommentBlock() (*pipeline.StringLit, error) {
	b.skipWS()
	if b.peek() == '{' {
		b.advance() // consume {
		b.skipWS()
		dirPos := b.srcPos()
		word := strings.ToUpper(b.readWord())
		if word != "COMMENT" {
			return nil, b.errorf("expected COMMENT inside block, got %q", word)
		}
		comment, err := b.parseStringDirective(dirPos)
		if err != nil {
			return nil, err
		}
		b.skipWS()
		if b.peek() != '}' {
			return nil, b.errorf("expected '}' to close block, got %q", b.peek())
		}
		b.advance()
		return comment, nil
	}
	if b.peek() == ';' {
		b.advance()
	}
	return nil, nil
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
		case "INDICES":
			var indices []pipeline.IndexDef
			indices, err = b.parseIndices(dirPos)
			ast.Indices = append(ast.Indices, indices...)
		case "INDEX":
			// Mode B (§4.8 Dual Definition Modes): the singular keyword precedes
			// a single entry outside a plural block, so unlike INDICES this must
			// not consume a wrapping '{ }'.
			var idx pipeline.IndexDef
			idx, err = b.parseOneIndex(false)
			ast.Indices = append(ast.Indices, idx)
		case "UNIQUE":
			// Mode B's "UNIQUE INDEX name (...)" form, mirroring real
			// PostgreSQL's CREATE UNIQUE INDEX order exactly (UNIQUE has no
			// other meaning as a bare top-level block directive). presetUnique
			// tells parseOneIndex UNIQUE was already consumed here, so it
			// doesn't try to read it again.
			b.skipWS()
			c := b.cur()
			w2 := strings.ToUpper(b.readWord())
			if w2 != "INDEX" {
				b.restore(c)
				return ast, b.errorf("expected INDEX after UNIQUE, got %q", w2)
			}
			var idx pipeline.IndexDef
			idx, err = b.parseOneIndex(true)
			ast.Indices = append(ast.Indices, idx)
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
		case "CONSTRAINTS":
			// Mode A (§4.8 Dual Definition Modes): plural block header wrapping
			// multiple entries, each omitting the singular keyword — completes
			// the pattern already offered for the other 7 collection types.
			var csts []pipeline.ConstraintDef
			csts, err = b.parseConstraintsBlock(dirPos)
			ast.Constraints = append(ast.Constraints, csts...)
		case "POLICIES":
			var policies []pipeline.PolicyDef
			policies, err = b.parsePolicies(dirPos)
			ast.Policies = append(ast.Policies, policies...)
		case "POLICY":
			// Mode B (§4.8 Dual Definition Modes): singular entry, no wrapping '{ }'.
			var pol pipeline.PolicyDef
			pol, err = b.parseOnePolicy()
			ast.Policies = append(ast.Policies, pol)
		case "TRIGGERS":
			var triggers []pipeline.TriggerDef
			triggers, err = b.parseTriggers(dirPos)
			ast.Triggers = append(ast.Triggers, triggers...)
		case "TRIGGER":
			// Mode B: singular entry, no wrapping '{ }'.
			var trig pipeline.TriggerDef
			trig, err = b.parseOneTrigger()
			ast.Triggers = append(ast.Triggers, trig)
		case "GRANTS":
			var grants []pipeline.GrantEntry
			grants, err = b.parseGrantsBlock(dirPos)
			ast.Grants = append(ast.Grants, grants...)
		case "GRANT":
			// Mode B (§4.8 Dual Definition Modes): singular entry, no wrapping '{ }'.
			var g pipeline.GrantEntry
			g, err = b.parseOneGrant(dirPos)
			ast.Grants = append(ast.Grants, g)
		case "REVOCATIONS":
			var revs []pipeline.RevocationEntry
			revs, err = b.parseRevocationsBlock(dirPos)
			ast.Revocations = append(ast.Revocations, revs...)
		case "REVOCATION":
			// Mode B: singular entry, no wrapping '{ }'.
			var r pipeline.RevocationEntry
			r, err = b.parseOneRevocation(dirPos)
			ast.Revocations = append(ast.Revocations, r)
		case "PARTITIONS":
			// PartitionDef wraps a list, so a PARTITIONS block must MERGE into
			// any partitions a prior Mode-B PARTITION entry already added —
			// assigning outright would silently discard them.
			var pd *pipeline.PartitionDef
			pd, err = b.parsePartitionsBlock(dirPos)
			if err == nil {
				if ast.Partitions == nil {
					ast.Partitions = pd
				} else {
					ast.Partitions.Partitions = append(ast.Partitions.Partitions, pd.Partitions...)
				}
			}
		case "PARTITION":
			// Mode B: singular entry, no wrapping '{ }'. PartitionDef wraps a list
			// (unlike Policies/Triggers/Indices/Grants/Revocations, PARTITIONS
			// isn't merged elsewhere), so lazily create it on first use.
			var bound pipeline.PartitionBound
			bound, err = b.parseOnePartitionBound()
			if err == nil {
				if ast.Partitions == nil {
					ast.Partitions = &pipeline.PartitionDef{Pos: dirPos}
				}
				ast.Partitions.Partitions = append(ast.Partitions.Partitions, bound)
			}
		case "MIGRATE":
			ast.MigrateRemove, err = b.parseMigrateRemove(dirPos)
		case "DEFAULT":
			// Overloaded keyword, disambiguated by what follows: "DEFAULT
			// PRIVILEGES ..." (existing) vs. RFC §5.4's DOMAIN-only bare
			// "DEFAULT expr;" directive. peekWord doesn't consume, so a
			// non-match falls through to parseDefaultPrivileges's own
			// b.expect("PRIVILEGES") — which would just fail with a
			// clearer error for an actually-malformed DEFAULT PRIVILEGES,
			// so this stays a strict two-way split rather than a silent
			// fallback.
			b.skipWS()
			if strings.ToUpper(b.peekWord()) == "PRIVILEGES" {
				var dp pipeline.DefaultPrivilegesBlock
				dp, err = b.parseDefaultPrivileges(dirPos)
				ast.DefaultPrivileges = append(ast.DefaultPrivileges, dp)
			} else {
				raw, e2 := b.readRawUntil(";")
				if e2 != nil {
					err = e2
				} else {
					ast.DomainDefault = &pipeline.RawExpr{Text: strings.TrimSpace(raw), Pos: dirPos}
					b.advance() // consume ;
				}
			}
		case "NOT":
			// RFC §5.4's DOMAIN-only "NOT NULL;" block directive.
			b.skipWS()
			w2 := strings.ToUpper(b.readWord())
			if w2 != "NULL" {
				return ast, b.errorf("expected NULL after NOT, got %q", w2)
			}
			ast.DomainNotNull = true
			err = b.expectSemi()
		case "MAPPING":
			var m pipeline.TSMappingDef
			m, err = b.parseTSMapping(dirPos)
			ast.Mappings = append(ast.Mappings, m)
		case "OPERATOR", "FUNCTION":
			// RFC §14.4's OPERATOR FAMILY { } members — real PG's own
			// ALTER OPERATOR FAMILY ... ADD list-item grammar, minus the
			// ALTER/family header. Each item restates its own OPERATOR/
			// FUNCTION keyword (unlike the real ALTER statement's single
			// "ADD" prefix), so this is dispatched exactly like every other
			// top-level directive rather than as a nested sub-list.
			var m pipeline.OpFamilyMember
			m, err = b.parseOpFamilyMember(dirPos, word == "FUNCTION")
			ast.OpFamilyMembers = append(ast.OpFamilyMembers, m)
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

// parseStringDirective reads: 'text'; and returns a *StringLit.
func (b *blockParser) parseStringDirective(pos pipeline.SourcePos) (*pipeline.StringLit, error) {
	b.skipWS()
	val, err := b.readSingleQuotedString()
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
		idx, err := b.parseOneIndex(false)
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

// parseOneIndex parses one index declaration. presetUnique is true when the
// caller (Mode B's top-level "UNIQUE INDEX ..." dispatch) already consumed a
// leading UNIQUE keyword itself — Mode A (an entry inside INDICES { }, with
// no INDEX keyword) has parseOneIndex consume its own leading UNIQUE.
func (b *blockParser) parseOneIndex(presetUnique bool) (pipeline.IndexDef, error) {
	pos := b.srcPos()

	unique := presetUnique
	if !presetUnique {
		b.skipWS()
		c := b.cur()
		w := strings.ToUpper(b.readWord())
		if w == "UNIQUE" {
			unique = true
		} else {
			b.restore(c)
		}
	}

	// [CONCURRENTLY] is a bare presence keyword, mirroring real PostgreSQL's
	// own CREATE [UNIQUE] INDEX [CONCURRENTLY] name — CONCURRENTLY has no
	// boolean value in real PG (it's either written or it isn't), so DPG
	// must not invent one either. This used to be a trailing
	// "CONCURRENTLY <bool>;" clause with no PG equivalent; presence here
	// means "explicitly concurrent", absence means "let the compiler apply
	// the project's concurrent_indexes default" (see internal/diff
	// createIndex), not "explicitly non-concurrent" — PG has no way to
	// spell that either.
	b.skipWS()
	c := b.cur()
	w := strings.ToUpper(b.readWord())
	concurrently := false
	if w == "CONCURRENTLY" {
		concurrently = true
	} else {
		b.restore(c)
	}

	name, err := b.readIdentifier()
	if err != nil {
		return pipeline.IndexDef{}, err
	}
	idx := pipeline.IndexDef{Name: name, Unique: unique, Concurrently: concurrently, Pos: pos}

	// Check optional USING method — mirrors real PostgreSQL's own
	// CREATE INDEX name ON table USING method (columns) order, and matches
	// the RFC's ABNF and every one of its worked examples (e.g.
	// "idx_location USING gist (location);"). This used to be parsed only
	// after the column list, which silently rejected the RFC's own
	// examples — confirmed live: `idx USING gin (tags);` failed to parse
	// with "expected '(' for index columns after index name idx" until
	// this fix.
	b.skipWS()
	c = b.cur()
	w = strings.ToUpper(b.readWord())
	if w == "USING" {
		b.skipWS()
		method, err2 := b.readIdentifier()
		if err2 != nil {
			return idx, err2
		}
		idx.Method = &method
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
				// Strip quotes like parseIndexColumnEntry does for key columns, so
				// the differ (which quotes on output) doesn't double-quote a
				// hand-written INCLUDE ("col").
				s = strings.Trim(strings.TrimSpace(s), `"`)
				if s != "" {
					idx.Include = append(idx.Include, pipeline.Identifier{Name: s})
				}
			}
		case "NULLS":
			b.readWord()
			b.skipWS()
			w2 := strings.ToUpper(b.readWord())
			switch w2 {
			case "NOT":
				b.skipWS()
				w3 := strings.ToUpper(b.readWord())
				if w3 != "DISTINCT" {
					return idx, b.errorf("expected DISTINCT after NULLS NOT, got %q", w3)
				}
				idx.NullsNotDistinct = true
			case "DISTINCT":
				// Explicit spelling of the default; nothing to record.
			default:
				return idx, b.errorf("expected NOT or DISTINCT after NULLS, got %q", w2)
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
		default:
			b.restore(c)
			goto doneIndexClauses
		}
	}
doneIndexClauses:

	comment, err := b.parseTrailingCommentBlock()
	if err != nil {
		return idx, err
	}
	idx.Comment = comment
	return idx, nil
}

func parseIndexColumns(raw string) []pipeline.IndexColumn {
	var cols []pipeline.IndexColumn
	for _, part := range splitTopLevel(raw, ',') {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cols = append(cols, parseIndexColumnEntry(part))
	}
	return cols
}

// splitTopLevel splits s on sep, ignoring separators nested inside parentheses
// (so an expression column like "coalesce(a, b)" is not split at its comma).
func splitTopLevel(s string, sep rune) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	for _, ch := range s {
		switch {
		case ch == '(':
			depth++
			cur.WriteRune(ch)
		case ch == ')':
			depth--
			cur.WriteRune(ch)
		case ch == sep && depth == 0:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(ch)
		}
	}
	parts = append(parts, cur.String())
	return parts
}

// parseIndexColumnEntry parses one index-column entry: an expression "(…)" or a
// (possibly quoted) column name, followed by optional ASC/DESC and NULLS
// FIRST/LAST. It mirrors the introspector's parseIndexColumn so a dumped index
// column ("col DESC NULLS LAST") round-trips to the same IndexColumn rather than
// being stored — and then quoted — as one literal identifier.
func parseIndexColumnEntry(s string) pipeline.IndexColumn {
	col := pipeline.IndexColumn{}
	upper := strings.ToUpper(s)
	if strings.HasSuffix(upper, " NULLS LAST") {
		col.Nulls = "LAST"
		s = strings.TrimSpace(s[:len(s)-len(" NULLS LAST")])
		upper = strings.ToUpper(s)
	} else if strings.HasSuffix(upper, " NULLS FIRST") {
		col.Nulls = "FIRST"
		s = strings.TrimSpace(s[:len(s)-len(" NULLS FIRST")])
		upper = strings.ToUpper(s)
	}
	if strings.HasSuffix(upper, " DESC") {
		col.SortOrder = "DESC"
		s = strings.TrimSpace(s[:len(s)-len(" DESC")])
	} else if strings.HasSuffix(upper, " ASC") {
		col.SortOrder = "ASC"
		s = strings.TrimSpace(s[:len(s)-len(" ASC")])
	}
	if strings.ContainsRune(s, '(') {
		col.Expr = &pipeline.RawExpr{Text: s}
	} else {
		col.Name = strings.Trim(s, `"`)
	}
	return col
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
		case "GRANTS":
			grants, e2 := b.parseGrantsBlock(dirPos)
			if e2 != nil {
				err = e2
			} else {
				col.Grants = append(col.Grants, grants...)
			}
		case "GRANT":
			// Mode B (§4.8 Dual Definition Modes): singular entry, no wrapping '{ }'.
			g, e2 := b.parseOneGrant(dirPos)
			if e2 != nil {
				err = e2
			} else {
				col.Grants = append(col.Grants, g)
			}
		case "REVOCATIONS":
			revs, e2 := b.parseRevocationsBlock(dirPos)
			if e2 != nil {
				err = e2
			} else {
				col.Revocations = append(col.Revocations, revs...)
			}
		case "REVOCATION":
			// Mode B: singular entry, no wrapping '{ }'.
			r, e2 := b.parseOneRevocation(dirPos)
			if e2 != nil {
				err = e2
			} else {
				col.Revocations = append(col.Revocations, r)
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

	// Read everything up to ';', "NOT VALID;", or a trailing "{ COMMENT
	// '...'; }" block — the brace is a real terminator here, not part of the
	// expression, so it must stop readRawUntil the same way ';' does.
	raw, err := b.readRawUntil(";{")
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
	comment, err := b.parseTrailingCommentBlock()
	if err != nil {
		return cst, err
	}
	cst.Comment = comment
	return cst, nil
}

// parseConstraintsBlock parses a CONSTRAINTS { } block (Mode A), each entry
// omitting the singular CONSTRAINT keyword. Mirrors parsePolicies/
// parseTriggers's relationship to their own per-entry parser; unlike those,
// parseConstraint takes an explicit pos rather than computing its own, so
// each entry's position is captured fresh here before calling it.
func (b *blockParser) parseConstraintsBlock(pos pipeline.SourcePos) ([]pipeline.ConstraintDef, error) {
	if err := b.consumeBrace(); err != nil {
		return nil, err
	}
	var constraints []pipeline.ConstraintDef
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		cPos := b.srcPos()
		cst, err := b.parseConstraint(cPos)
		if err != nil {
			return nil, err
		}
		constraints = append(constraints, cst)
	}
	b.skipWS()
	if b.peek() != '}' {
		return nil, b.errorf("expected '}' to close CONSTRAINTS block")
	}
	b.advance()
	return constraints, nil
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
	comment, err := b.parseTrailingCommentBlock()
	if err != nil {
		return pol, err
	}
	pol.Comment = comment
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

	comment, err := b.parseTrailingCommentBlock()
	if err != nil {
		return trig, err
	}
	trig.Comment = comment
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
		bound, err := b.parseOnePartitionBound()
		if err != nil {
			return nil, err
		}
		pd.Partitions = append(pd.Partitions, bound)
	}
	b.skipWS()
	if b.peek() != '}' {
		return nil, b.errorf("expected '}' to close PARTITIONS block")
	}
	b.advance()
	return pd, nil
}

// subPartitionByRe matches a trailing "PARTITION BY <strategy> (<cols>)" clause
// at the end of a partition entry's raw bounds text, i.e. RFC §7.13
// sub-partitioning. Bounds-clause literals never contain the word PARTITION,
// so a plain trailing match is unambiguous.
var subPartitionByRe = regexp.MustCompile(`(?is)^(.*?)\bPARTITION\s+BY\s+(RANGE|LIST|HASH)\s*\(([^)]*)\)\s*$`)

// parseOnePartitionBound parses a single "name bounds [PARTITION BY strategy
// (cols) { PARTITIONS {...} }];" entry, shared by parsePartitionsBlock
// (Mode A, inside a PARTITIONS { } block) and the PARTITION singular-keyword
// dispatch case (Mode B). The trailing PARTITION BY clause is optional and
// describes sub-partitioning (RFC §7.13): a partition entry may itself be
// further partitioned, recursively.
func (b *blockParser) parseOnePartitionBound() (pipeline.PartitionBound, error) {
	pPos := b.srcPos()
	name, err := b.readIdentifier()
	if err != nil {
		return pipeline.PartitionBound{}, err
	}
	// Read everything up to ';', a nested sub-partition '{', or end of block.
	raw, err := b.readRawUntil(";{}")
	if err != nil {
		return pipeline.PartitionBound{}, err
	}

	bound := pipeline.PartitionBound{Name: name, Pos: pPos}

	if m := subPartitionByRe.FindStringSubmatch(raw); m != nil {
		if b.peek() != '{' {
			return pipeline.PartitionBound{}, b.errorf("expected '{' after PARTITION BY %s clause", m[2])
		}
		bound.Bounds = pipeline.RawExpr{Text: strings.TrimSpace(m[1]), Pos: pPos}
		bound.SubStrategy = strings.ToUpper(m[2])
		for _, c := range splitTopLevel(m[3], ',') {
			c = strings.TrimSpace(c)
			if c != "" {
				bound.SubColumns = append(bound.SubColumns, c)
			}
		}

		if err := b.consumeBrace(); err != nil {
			return pipeline.PartitionBound{}, err
		}
		b.skipWS()
		if err := b.expect("PARTITIONS"); err != nil {
			return pipeline.PartitionBound{}, err
		}
		nestedPos := b.srcPos()
		nested, err := b.parsePartitionsBlock(nestedPos)
		if err != nil {
			return pipeline.PartitionBound{}, err
		}
		bound.SubPartitions = nested.Partitions
		b.skipWS()
		if b.peek() != '}' {
			return pipeline.PartitionBound{}, b.errorf("expected '}' to close nested PARTITION BY block")
		}
		b.advance()
	} else {
		if b.peek() == '{' {
			return pipeline.PartitionBound{}, b.errorf("unexpected '{' in partition bounds (missing PARTITION BY clause?)")
		}
		bound.Bounds = pipeline.RawExpr{Text: strings.TrimSpace(raw), Pos: pPos}
	}

	b.skipWS()
	if b.peek() == ';' {
		b.advance()
	}
	return bound, nil
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

// parseDefaultPrivileges is the nested-directive entry point (dispatched
// from parseBlock's "DEFAULT" case when DEFAULT PRIVILEGES appears inside
// an enclosing object's block, e.g. nested inside SCHEMA — the caller has
// already consumed "DEFAULT").
func (b *blockParser) parseDefaultPrivileges(pos pipeline.SourcePos) (pipeline.DefaultPrivilegesBlock, error) {
	if err := b.expect("PRIVILEGES"); err != nil {
		return pipeline.DefaultPrivilegesBlock{}, err
	}
	return b.parseDefaultPrivilegesBody(pos)
}

// ParseDefaultPrivileges is the top-level entry point for a DEFAULT
// PRIVILEGES declaration that is NOT nested inside another object's block.
// Unlike every other DPG object kind, DEFAULT PRIVILEGES is never split into
// a pg_query-parsed Part 1 and a blockparser-parsed Part 2: real
// PostgreSQL's ALTER DEFAULT PRIVILEGES statement requires its GRANT/REVOKE
// action inline in the same statement, so a header-only fragment like
// "FOR ROLE x IN SCHEMA y" is never valid PG SQL on its own (confirmed live:
// "syntax error at end of input"). header is the raw text between "DEFAULT
// PRIVILEGES" and the opening '{' (e.g. "FOR ROLE x IN SCHEMA y"); body is
// the text inside that '{ }' (matching pipeline.BlockParser.Parse's own
// part2 convention — braces excluded).
func ParseDefaultPrivileges(header, body string, pos pipeline.SourcePos) (pipeline.DefaultPrivilegesBlock, error) {
	hp := &blockParser{src: []byte(header), file: pos.File, line: pos.Line, col: pos.Col}
	dp, err := hp.parseDefaultPrivilegesHeader(pos)
	if err != nil {
		return dp, err
	}
	bp := &blockParser{src: []byte(body), file: pos.File, line: pos.Line, col: pos.Col}
	if err := bp.parseDefaultPrivilegesEntries(&dp); err != nil {
		return dp, err
	}
	return dp, nil
}

// parseDefaultPrivilegesBody parses "[IN SCHEMA x] [FOR ROLE y] { entries }"
// from a single source (the nested-directive case, where header and braces
// all come from the same enclosing block's text).
func (b *blockParser) parseDefaultPrivilegesBody(pos pipeline.SourcePos) (pipeline.DefaultPrivilegesBlock, error) {
	dp, err := b.parseDefaultPrivilegesHeader(pos)
	if err != nil {
		return dp, err
	}
	if err := b.consumeBrace(); err != nil {
		return dp, err
	}
	if err := b.parseDefaultPrivilegesEntries(&dp); err != nil {
		return dp, err
	}
	b.skipWS()
	if b.peek() != '}' {
		return dp, b.errorf("expected '}' to close DEFAULT PRIVILEGES block")
	}
	b.advance()
	return dp, nil
}

// parseDefaultPrivilegesHeader parses the optional "[IN SCHEMA x] [FOR ROLE
// y]" clauses, in either order, consuming nothing past them (no brace).
func (b *blockParser) parseDefaultPrivilegesHeader(pos pipeline.SourcePos) (pipeline.DefaultPrivilegesBlock, error) {
	dp := pipeline.DefaultPrivilegesBlock{Pos: pos}
	for {
		b.skipWS()
		c := b.cur()
		word := strings.ToUpper(b.peekWord())
		switch word {
		case "IN":
			if dp.InSchema != nil {
				return dp, b.errorf("IN SCHEMA specified more than once")
			}
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
		case "FOR":
			if dp.ForRole != nil {
				return dp, b.errorf("FOR ROLE specified more than once")
			}
			b.readWord()
			b.skipWS()
			_ = b.readWord() // ROLE keyword
			b.skipWS()
			r, err := b.readIdentifier()
			if err != nil {
				return dp, err
			}
			dp.ForRole = &r
		default:
			b.restore(c)
			return dp, nil
		}
	}
}

// parseDefaultPrivilegesEntries parses zero or more GRANTS/GRANT/
// REVOCATIONS/REVOCATION directives (Mode A/B, same dual-definition
// convention as every other block) until EOF or an unconsumed '}'.
func (b *blockParser) parseDefaultPrivilegesEntries(dp *pipeline.DefaultPrivilegesBlock) error {
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			return nil
		}
		dirPos := b.srcPos()
		word := strings.ToUpper(b.readWord())
		switch word {
		case "GRANTS":
			grants, err := b.parseDefaultPrivilegeGrantsBlock(dirPos)
			if err != nil {
				return err
			}
			dp.Grants = append(dp.Grants, grants...)
		case "GRANT":
			// Mode B (§4.8 Dual Definition Modes): singular entry, no wrapping '{ }'.
			g, err := b.parseOneDefaultPrivilegeGrant(dirPos)
			if err != nil {
				return err
			}
			dp.Grants = append(dp.Grants, g)
		case "REVOCATIONS":
			revs, err := b.parseDefaultPrivilegeRevocationsBlock(dirPos)
			if err != nil {
				return err
			}
			dp.Revocations = append(dp.Revocations, revs...)
		case "REVOCATION":
			// Mode B: singular entry, no wrapping '{ }'.
			r, err := b.parseOneDefaultPrivilegeRevocation(dirPos)
			if err != nil {
				return err
			}
			dp.Revocations = append(dp.Revocations, r)
		default:
			return fmt.Errorf("%s: unexpected directive %q in DEFAULT PRIVILEGES block", dirPos, word)
		}
	}
}

func (b *blockParser) parseDefaultPrivilegeGrantsBlock(pos pipeline.SourcePos) ([]pipeline.DefaultPrivilegeGrant, error) {
	if err := b.consumeBrace(); err != nil {
		return nil, err
	}
	var grants []pipeline.DefaultPrivilegeGrant
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		g, err := b.parseOneDefaultPrivilegeGrant(b.srcPos())
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

// parseOneDefaultPrivilegeGrant parses "priv[, ...] ON objtype TO role[, ...]
// [WITH GRANT OPTION];" — real PostgreSQL's own ALTER DEFAULT PRIVILEGES
// grant-clause grammar, confirmed against real PG (not a DPG invention):
// the object type is part of the GRANT clause itself, unlike a regular
// table/view GRANTS block where the object is implicit from the enclosing
// declaration.
func (b *blockParser) parseOneDefaultPrivilegeGrant(pos pipeline.SourcePos) (pipeline.DefaultPrivilegeGrant, error) {
	g := pipeline.DefaultPrivilegeGrant{Pos: pos}
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
		g.Privileges = nil // nil = ALL
	} else {
		b.restore(c)
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
			b.skipWS()
			if strings.ToUpper(b.peekWord()) == "ON" {
				break
			}
		}
	}
	if err := b.expect("ON"); err != nil {
		return g, err
	}
	b.skipWS()
	g.ObjectType = strings.ToUpper(b.readWord())
	if g.ObjectType == "" {
		return g, b.errorf("expected an object type (TABLES/SEQUENCES/FUNCTIONS/TYPES/SCHEMAS) after ON")
	}
	if err := b.expect("TO"); err != nil {
		return g, err
	}
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

func (b *blockParser) parseDefaultPrivilegeRevocationsBlock(pos pipeline.SourcePos) ([]pipeline.DefaultPrivilegeRevocation, error) {
	if err := b.consumeBrace(); err != nil {
		return nil, err
	}
	var revs []pipeline.DefaultPrivilegeRevocation
	for {
		b.skipWS()
		if b.eof() || b.peek() == '}' {
			break
		}
		r, err := b.parseOneDefaultPrivilegeRevocation(b.srcPos())
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

// parseOneDefaultPrivilegeRevocation mirrors parseOneDefaultPrivilegeGrant —
// real PostgreSQL's ALTER DEFAULT PRIVILEGES ... REVOKE clause: "priv[, ...]
// ON objtype FROM role[, ...] [CASCADE];".
func (b *blockParser) parseOneDefaultPrivilegeRevocation(pos pipeline.SourcePos) (pipeline.DefaultPrivilegeRevocation, error) {
	r := pipeline.DefaultPrivilegeRevocation{Pos: pos}
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
			if strings.ToUpper(b.peekWord()) == "ON" {
				break
			}
		}
	}
	if err := b.expect("ON"); err != nil {
		return r, err
	}
	b.skipWS()
	r.ObjectType = strings.ToUpper(b.readWord())
	if r.ObjectType == "" {
		return r, b.errorf("expected an object type (TABLES/SEQUENCES/FUNCTIONS/TYPES/SCHEMAS) after ON")
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
	for {
		b.skipWS()
		dict, err := b.readIdentifier()
		if err != nil {
			return m, err
		}
		m.Dictionaries = append(m.Dictionaries, dict)
		b.skipWS()
		if b.peek() != ',' {
			break
		}
		b.advance()
	}
	if err := b.expectSemi(); err != nil {
		return m, err
	}
	return m, nil
}

// ── OPERATOR FAMILY loose members ────────────────────────────────────────────

// isOperatorChar reports whether ch is one of PostgreSQL's own operator
// characters (real PG grammar, not a DPG invention).
func isOperatorChar(ch byte) bool {
	return strings.IndexByte("+-*/<>=~!@#%^&|`?", ch) >= 0
}

// readOperatorSymbol reads a (possibly schema-qualified) operator name —
// real PG's "any_operator" production (ColId '.' any_operator | all_Op),
// e.g. "<", "@>", "pg_catalog.=". Word characters and operator characters
// are disjoint, so a leading schema and the operator symbol never collide.
func (b *blockParser) readOperatorSymbol() (pipeline.Identifier, error) {
	b.skipWS()
	schema := ""
	if isWordStart(b.peek()) {
		c := b.cur()
		w := b.readWord()
		b.skipWS()
		if b.peek() == '.' {
			b.advance()
			schema = w
		} else {
			b.restore(c)
		}
	}
	b.skipWS()
	var buf []byte
	for !b.eof() && isOperatorChar(b.peek()) {
		buf = append(buf, b.advance())
	}
	if len(buf) == 0 {
		return pipeline.Identifier{}, b.errorf("expected an operator symbol, got %q", b.peek())
	}
	return pipeline.Identifier{Schema: schema, Name: string(buf)}, nil
}

// readTypeListParen reads a parenthesized, comma-separated list of raw type
// text, e.g. "(int4, int8)" or "(numeric(10,2))" or "()" (empty). The
// opening '(' must not have been consumed yet. readRawUntil is paren/brace/
// quote-depth aware, so type modifiers like numeric(10,2) survive intact.
func (b *blockParser) readTypeListParen() ([]string, error) {
	b.skipWS()
	if b.peek() != '(' {
		return nil, b.errorf("expected '(', got %q", b.peek())
	}
	b.advance()
	b.skipWS()
	var types []string
	if b.peek() == ')' {
		b.advance()
		return types, nil
	}
	for {
		raw, err := b.readRawUntil(",)")
		if err != nil {
			return nil, err
		}
		types = append(types, strings.TrimSpace(raw))
		b.skipWS()
		if b.peek() == ',' {
			b.advance()
			continue
		}
		break
	}
	if b.peek() != ')' {
		return nil, b.errorf("expected ')', got %q", b.peek())
	}
	b.advance()
	return types, nil
}

// parseOpFamilyMember parses one OPERATOR/FUNCTION item inside an
// OPERATOR FAMILY { } block (RFC §14.4) — real PG's own
// "ALTER OPERATOR FAMILY ... ADD" list-item grammar, just without the
// ALTER/family header (already stated by the family's own declaration). The
// leading OPERATOR/FUNCTION keyword has already been consumed by the
// caller's directive dispatch. Terminates on ',' (more members follow),
// ';' (directive ends), or EOF (last member in the block) — all three are
// accepted so both the approved comma-separated list style and DPG's usual
// one-directive-per-';' style parse identically.
func (b *blockParser) parseOpFamilyMember(pos pipeline.SourcePos, isFunction bool) (pipeline.OpFamilyMember, error) {
	kind := "OPERATOR"
	if isFunction {
		kind = "FUNCTION"
	}
	m := pipeline.OpFamilyMember{IsFunction: isFunction, Pos: pos}

	b.skipWS()
	var numBuf []byte
	for !b.eof() && isDigit(b.peek()) {
		numBuf = append(numBuf, b.advance())
	}
	if len(numBuf) == 0 {
		return m, b.errorf("expected a strategy/support number after %s", kind)
	}
	n, err := strconv.Atoi(string(numBuf))
	if err != nil {
		return m, b.errorf("invalid %s number: %v", kind, err)
	}
	m.Number = n

	if isFunction {
		// Optional "(op_type [, op_type])" — defaults are resolved later by
		// the IR builder (ir.normalizeOpFamilyMembers), not here; the parser
		// only records what was actually written.
		b.skipWS()
		if b.peek() == '(' {
			types, err := b.readTypeListParen()
			if err != nil {
				return m, err
			}
			switch len(types) {
			case 1:
				m.LeftType, m.RightType = types[0], types[0]
			case 2:
				m.LeftType, m.RightType = types[0], types[1]
			default:
				return m, b.errorf("FUNCTION member's optional (op_type[, op_type]) must have 1 or 2 entries, got %d", len(types))
			}
		}
		name, err := b.readIdentifier()
		if err != nil {
			return m, err
		}
		m.Name = name
		args, err := b.readTypeListParen()
		if err != nil {
			return m, err
		}
		m.FuncArgs = args
	} else {
		sym, err := b.readOperatorSymbol()
		if err != nil {
			return m, err
		}
		m.Name = sym
		types, err := b.readTypeListParen()
		if err != nil {
			return m, err
		}
		if len(types) != 2 {
			return m, b.errorf("OPERATOR member requires exactly 2 op_types (left, right), got %d", len(types))
		}
		m.LeftType, m.RightType = types[0], types[1]

		b.skipWS()
		if strings.EqualFold(b.peekWord(), "FOR") {
			b.readWord()
			b.skipWS()
			w2 := strings.ToUpper(b.readWord())
			switch w2 {
			case "SEARCH":
				// Default; nothing to set.
			case "ORDER":
				if err := b.expect("BY"); err != nil {
					return m, err
				}
				fam, err := b.readIdentifier()
				if err != nil {
					return m, err
				}
				m.OrderBy = true
				m.SortFamily = fam
			default:
				return m, b.errorf("expected SEARCH or ORDER BY after FOR, got %q", w2)
			}
		}
	}

	b.skipWS()
	switch b.peek() {
	case ',', ';':
		b.advance()
	case 0:
		// EOF: last member in the block, no trailing punctuation required.
	default:
		return m, b.errorf("expected ',' or ';' after operator family member, got %q", b.peek())
	}
	return m, nil
}
