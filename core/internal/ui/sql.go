package ui

import (
	"strings"
	"unicode"
)

// sqlKeywords is the set of SQL/PG DDL keywords that get highlighted bold-blue.
// Stored uppercase; matching is case-insensitive.
var sqlKeywords = map[string]bool{
	// DML / clauses
	"SELECT": true, "FROM": true, "WHERE": true, "AS": true, "ON": true,
	"AND": true, "OR": true, "NOT": true, "IS": true, "IN": true,
	"WITH": true, "HAVING": true, "GROUP": true, "ORDER": true, "BY": true,
	"LIMIT": true, "OFFSET": true, "UNION": true, "ALL": true, "DISTINCT": true,
	// DDL verbs
	"CREATE": true, "ALTER": true, "DROP": true, "REPLACE": true,
	"TRUNCATE": true, "RENAME": true, "SET": true, "TO": true,
	"ADD": true, "ENABLE": true, "DISABLE": true,
	// Objects
	"TABLE": true, "COLUMN": true, "SCHEMA": true, "DATABASE": true,
	"VIEW": true, "MATERIALIZED": true, "RECURSIVE": true,
	"INDEX": true, "SEQUENCE": true, "EXTENSION": true,
	"FUNCTION": true, "PROCEDURE": true, "TRIGGER": true, "RULE": true,
	"TYPE": true, "ENUM": true, "DOMAIN": true,
	"ROLE": true, "USER": true, "TABLESPACE": true,
	"POLICY": true, "PUBLICATION": true, "SUBSCRIPTION": true,
	// Constraints / modifiers
	"CONSTRAINT": true, "PRIMARY": true, "KEY": true, "FOREIGN": true,
	"REFERENCES": true, "UNIQUE": true, "CHECK": true,
	"DEFAULT": true, "NULL": true, "GENERATED": true,
	"ALWAYS": true, "IDENTITY": true, "DEFERRABLE": true,
	"INITIALLY": true, "DEFERRED": true, "IMMEDIATE": true,
	"CASCADE": true, "RESTRICT": true, "ACTION": true, "NO": true,
	"ROW": true, "LEVEL": true, "SECURITY": true,
	// Transaction control
	"BEGIN": true, "COMMIT": true, "ROLLBACK": true, "TRANSACTION": true,
	"SAVEPOINT": true, "RELEASE": true,
	// Misc DDL
	"CONCURRENTLY": true, "IF": true, "EXISTS": true, "OWNER": true,
	"COMMENT": true, "LANGUAGE": true, "RETURNS": true,
	"WITHOUT": true, "OIDS": true, "UNLOGGED": true,
	"TEMPORARY": true, "TEMP": true, "GLOBAL": true, "LOCAL": true,
	"GRANT": true, "REVOKE": true,
	// Types (PG built-ins commonly found in migrations)
	"BIGINT": true, "INT": true, "INTEGER": true, "SMALLINT": true,
	"TEXT": true, "VARCHAR": true, "CHAR": true, "CHARACTER": true, "VARYING": true,
	"BOOLEAN": true, "BOOL": true,
	"FLOAT": true, "DOUBLE": true, "REAL": true, "PRECISION": true,
	"NUMERIC": true, "DECIMAL": true,
	"DATE": true, "TIME": true, "TIMESTAMP": true, "TIMESTAMPTZ": true,
	"INTERVAL": true, "ZONE": true,
	"JSON": true, "JSONB": true, "UUID": true, "BYTEA": true,
}

// HighlightSQL applies ANSI colour to SQL text.
// When color is false it is a no-op and returns sql unchanged.
// The highlighter handles:
//   - SQL keywords → bold blue
//   - Single-quoted string literals → yellow
//   - Dollar-quoted strings ($$ … $$ / $tag$ … $tag$) → yellow
//   - Line comments (-- …) → dim
func HighlightSQL(sql string, color bool) string {
	if !color {
		return sql
	}
	var out strings.Builder
	out.Grow(len(sql) + len(sql)/4)
	i := 0
	for i < len(sql) {
		// Line comment: -- to end of line.
		if i+1 < len(sql) && sql[i] == '-' && sql[i+1] == '-' {
			j := i
			for j < len(sql) && sql[j] != '\n' {
				j++
			}
			out.WriteString(paint(ansiDim, sql[i:j], true))
			i = j
			continue
		}
		// Dollar-quoted string.
		if sql[i] == '$' {
			if tag, end := parseDollarQuote(sql, i); end >= 0 {
				out.WriteString(paint(ansiYellow, sql[i:end], true))
				i = end
				_ = tag
				continue
			}
		}
		// Single-quoted string literal.
		if sql[i] == '\'' {
			j := i + 1
			for j < len(sql) {
				if sql[j] == '\'' {
					j++
					if j < len(sql) && sql[j] == '\'' {
						j++ // escaped quote: ''
						continue
					}
					break
				}
				j++
			}
			out.WriteString(paint(ansiYellow, sql[i:j], true))
			i = j
			continue
		}
		// Identifier or keyword: starts with letter or underscore.
		if isIdentStart(rune(sql[i])) {
			j := i + 1
			for j < len(sql) && isIdentPart(rune(sql[j])) {
				j++
			}
			word := sql[i:j]
			if sqlKeywords[strings.ToUpper(word)] {
				out.WriteString(paint(ansiBlue, word, true))
			} else {
				out.WriteString(word)
			}
			i = j
			continue
		}
		out.WriteByte(sql[i])
		i++
	}
	return out.String()
}

func isIdentStart(r rune) bool { return unicode.IsLetter(r) || r == '_' }
func isIdentPart(r rune) bool  { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// parseDollarQuote finds a $tag$...$tag$ literal starting at pos.
// Returns (tag, endIndex) or ("", -1) if pos does not begin a dollar quote.
func parseDollarQuote(sql string, pos int) (string, int) {
	if pos >= len(sql) || sql[pos] != '$' {
		return "", -1
	}
	// Scan to the closing $ of the opening delimiter.
	j := pos + 1
	for j < len(sql) {
		if sql[j] == '$' {
			break
		}
		if !isIdentPart(rune(sql[j])) {
			return "", -1
		}
		j++
	}
	if j >= len(sql) {
		return "", -1
	}
	tag := sql[pos : j+1] // e.g. "$$" or "$body$"
	// Find the matching closing delimiter.
	rest := sql[j+1:]
	idx := strings.Index(rest, tag)
	if idx < 0 {
		return "", -1
	}
	return tag, j + 1 + idx + len(tag)
}
