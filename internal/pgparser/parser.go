// Package pgparser implements pipeline.PGSQLParser using libpg_query via
// github.com/pganalyze/pg_query_go/v6. It reconstructs a valid CREATE statement
// from the (kind, part1) pair and delegates all parsing to the real PG C parser.
package pgparser

import (
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	pipeline.Default.Register(pipeline.KeyPGSQLParser, New())
}

// Parser implements pipeline.PGSQLParser using the real PostgreSQL parser.
type Parser struct{}

// New returns a Parser ready to use.
func New() *Parser { return &Parser{} }

// Parse implements pipeline.PGSQLParser. It prepends the correct CREATE verb,
// calls pg_query.Parse, and returns the parse tree wrapped in a PGParseResult.
// Passthrough kinds (KindVirtualType) bypass pg_query and return Part1 as Raw.
func (p *Parser) Parse(kind pipeline.ObjectKind, part1 string, pos pipeline.SourcePos) (pipeline.PGParseResult, error) {
	if kind == pipeline.KindVirtualType {
		return pipeline.PGParseResult{Raw: part1, Kind: kind, Pos: pos}, nil
	}
	sql := Reconstruct(kind, part1)
	result, err := pg_query.Parse(sql)
	if err != nil {
		return pipeline.PGParseResult{}, pipeline.Errorf(pos, "PG parse error: %s", translateError(err, sql))
	}
	return pipeline.PGParseResult{Raw: result, Pos: pos}, nil
}

// translateError extracts the message from a pg_query parse error, stripping
// the synthetic position information that refers to the reconstructed SQL rather
// than the original DPG source file.
func translateError(err error, sql string) string {
	msg := err.Error()
	// pg_query errors often contain "ERROR:  ... at character N" or
	// "syntax error at or near ..." — strip the redundant location suffix.
	if idx := strings.Index(msg, " (line "); idx != -1 {
		msg = msg[:idx]
	}
	if idx := strings.Index(msg, " at character "); idx != -1 {
		msg = msg[:idx]
	}
	_ = sql
	return fmt.Sprintf("%s", strings.TrimSpace(msg))
}
