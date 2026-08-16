package ir

import (
	"fmt"
	"regexp"
	"strings"
)

// qualIdentQuoted renders a possibly-schema-qualified identifier using this
// package's own quoteIdent (builder.go), quoting each part independently.
func qualIdentQuoted(schema, name string) string {
	if schema == "" {
		return quoteIdent(name)
	}
	return quoteIdent(schema) + "." + quoteIdent(name)
}

var dollarTagRe = regexp.MustCompile(`\$[A-Za-z0-9_]*\$`)

// safeDollarTag picks a dollar-quote tag guaranteed not to collide with any
// $..$-shaped sequence already present in body (e.g. dynamic SQL that
// itself uses dollar-quoted string literals). Falls back from the
// conventional "$$" through "$dpg$", "$dpg1$", "$dpg2$", ... on collision.
func safeDollarTag(body string) string {
	used := make(map[string]bool)
	for _, m := range dollarTagRe.FindAllString(body, -1) {
		used[m] = true
	}
	if !used["$$"] {
		return "$$"
	}
	for i := 0; ; i++ {
		tag := "$dpg$"
		if i > 0 {
			tag = fmt.Sprintf("$dpg%d$", i)
		}
		if !used[tag] {
			return tag
		}
	}
}

// RenderCreateFunctionSQL renders o as a standalone, valid
// "CREATE OR REPLACE FUNCTION ... AS <tag>...<tag>;" statement. Used both
// for real migration SQL emission (internal/diff) and to build a compile
// shim for HashFunctionBody's plpgsql canonicalisation (internal/introspect)
// — both need identical, argument-accurate rendering, so this is the single
// shared implementation rather than parallel ones.
func RenderCreateFunctionSQL(o *Function) string {
	var b strings.Builder
	b.WriteString("CREATE OR REPLACE FUNCTION ")
	b.WriteString(qualIdentQuoted(o.Schema, o.Name))
	b.WriteString("(")
	first := true
	for _, a := range o.Args {
		// RETURNS TABLE(...) columns are declared in a separate clause
		// below, never inline here — "TABLE a integer" is not valid
		// parameter syntax.
		if a.Mode == "TABLE" {
			continue
		}
		if !first {
			b.WriteString(", ")
		}
		first = false
		if a.Mode != "" && a.Mode != "IN" {
			b.WriteString(a.Mode)
			b.WriteString(" ")
		}
		if a.Name != "" {
			b.WriteString(a.Name)
			b.WriteString(" ")
		}
		b.WriteString(a.Type.String())
		if a.Default != nil {
			b.WriteString(" DEFAULT ")
			b.WriteString(*a.Default)
		}
	}
	b.WriteString(") RETURNS ")
	if tableCols := FuncTableColumns(o.Args); len(tableCols) > 0 {
		b.WriteString("TABLE(")
		b.WriteString(FormatTableColumns(tableCols))
		b.WriteString(")")
	} else {
		if o.ReturnType.SetOf {
			b.WriteString("SETOF ")
		}
		b.WriteString(o.ReturnType.String())
	}
	b.WriteString(" LANGUAGE ")
	b.WriteString(o.Attrs.Language)
	if o.Attrs.Volatility != "" && o.Attrs.Volatility != "VOLATILE" {
		b.WriteString(" ")
		b.WriteString(o.Attrs.Volatility)
	}
	if o.Attrs.Strict {
		b.WriteString(" STRICT")
	}
	if o.Attrs.SecurityDef {
		b.WriteString(" SECURITY DEFINER")
	}
	if o.Attrs.Parallel != "" && o.Attrs.Parallel != "UNSAFE" {
		b.WriteString(" PARALLEL ")
		b.WriteString(o.Attrs.Parallel)
	}
	if o.Attrs.Cost != nil {
		fmt.Fprintf(&b, " COST %v", *o.Attrs.Cost)
	}
	if o.Attrs.Rows != nil {
		fmt.Fprintf(&b, " ROWS %v", *o.Attrs.Rows)
	}
	tag := safeDollarTag(o.Attrs.Body)
	b.WriteString(" AS ")
	b.WriteString(tag)
	b.WriteString(o.Attrs.Body)
	b.WriteString(tag)
	b.WriteString(";")
	return b.String()
}

// RenderCreateProcedureSQL is RenderCreateFunctionSQL's PROCEDURE
// counterpart. Procedures never take STRICT/SECURITY DEFINER/PARALLEL/COST/
// ROWS in this codebase's existing emission (matches real PostgreSQL, which
// doesn't accept most of those on a procedure).
func RenderCreateProcedureSQL(o *Procedure) string {
	var b strings.Builder
	b.WriteString("CREATE OR REPLACE PROCEDURE ")
	b.WriteString(qualIdentQuoted(o.Schema, o.Name))
	b.WriteString("(")
	for i, a := range o.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		if a.Mode != "" && a.Mode != "IN" {
			b.WriteString(a.Mode)
			b.WriteString(" ")
		}
		if a.Name != "" {
			b.WriteString(a.Name)
			b.WriteString(" ")
		}
		b.WriteString(a.Type.String())
	}
	b.WriteString(") LANGUAGE ")
	b.WriteString(o.Attrs.Language)
	tag := safeDollarTag(o.Attrs.Body)
	b.WriteString(" AS ")
	b.WriteString(tag)
	b.WriteString(o.Attrs.Body)
	b.WriteString(tag)
	b.WriteString(";")
	return b.String()
}
