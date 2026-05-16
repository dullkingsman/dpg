package format

import "strings"

// Options controls how a .dpg file is formatted.
type Options struct {
	// IndentSize is the number of spaces per indent level. Default: 4.
	IndentSize int
	// KeywordCase controls keyword casing: "upper" (default) or "lower".
	KeywordCase string
}

// Indent returns the indent string (IndentSize spaces, minimum 4).
func (o Options) Indent() string {
	n := o.IndentSize
	if n <= 0 {
		n = 4
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

// Keyword returns k with casing applied: uppercase (default) or lowercase.
func (o Options) Keyword(k string) string {
	if o.KeywordCase == "lower" {
		return strings.ToLower(k)
	}
	return strings.ToUpper(k)
}

// keep unexported aliases so internal callers don't need updating
func (o Options) indent() string          { return o.Indent() }
func (o Options) keyword(k string) string { return o.Keyword(k) }
