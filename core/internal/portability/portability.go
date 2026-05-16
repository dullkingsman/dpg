// Package portability implements pipeline.PortabilityAnalyzer. It walks IR
// objects and reports PG-specific constructs, noting standard SQL alternatives
// where they exist.
package portability

import (
	"strings"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	pipeline.Default.Register(pipeline.KeyPortabilityAnalyzer, New())
}

// Analyzer implements pipeline.PortabilityAnalyzer.
type Analyzer struct{}

// New returns an Analyzer.
func New() *Analyzer { return &Analyzer{} }

// Analyze walks objects and reports PG-specific constructs.
func (a *Analyzer) Analyze(objects []pipeline.IRObject) ([]pipeline.PortabilityIssue, error) {
	var issues []pipeline.PortabilityIssue

	for _, obj := range objects {
		issues = append(issues, analyzeObject(obj)...)
	}

	return issues, nil
}

func analyzeObject(obj pipeline.IRObject) []pipeline.PortabilityIssue {
	var issues []pipeline.PortabilityIssue

	switch o := obj.(type) {
	case *ir.Table:
		issues = append(issues, analyzeTable(o)...)
	case *ir.Function:
		issues = append(issues, analyzeFunction(o)...)
	case *ir.Type:
		issues = append(issues, analyzeType(o)...)
	case *ir.Extension:
		issues = append(issues, pipeline.PortabilityIssue{
			Pos:         o.SrcPos,
			Construct:   "CREATE EXTENSION",
			Alternative: "Extensions are PostgreSQL-specific; no standard SQL equivalent.",
		})
	}

	return issues
}

// ── Table ─────────────────────────────────────────────────────────────────────

func analyzeTable(t *ir.Table) []pipeline.PortabilityIssue {
	var issues []pipeline.PortabilityIssue
	pos := t.SrcPos

	if t.Unlogged {
		issues = append(issues, pipeline.PortabilityIssue{
			Pos:         pos,
			Construct:   "UNLOGGED TABLE",
			Alternative: "Not in SQL standard; use regular TABLE for portability.",
		})
	}
	if t.RLSEnabled || t.RLSForced {
		issues = append(issues, pipeline.PortabilityIssue{
			Pos:         pos,
			Construct:   "ROW LEVEL SECURITY",
			Alternative: "PG-specific; use application-level access control for portability.",
		})
	}
	if t.Policies != nil {
		issues = append(issues, pipeline.PortabilityIssue{
			Pos:         pos,
			Construct:   "CREATE POLICY",
			Alternative: "PG-specific row-security policy; no standard SQL equivalent.",
		})
	}
	if t.PartitionBy != nil {
		issues = append(issues, pipeline.PortabilityIssue{
			Pos:         pos,
			Construct:   "PARTITION BY",
			Alternative: "Declarative partitioning is PG 10+; syntax differs across vendors.",
		})
	}

	for _, col := range t.Columns {
		issues = append(issues, analyzeColumn(col, t.QualifiedName())...)
	}

	for _, idx := range t.Indexes {
		if idx.Method != "" && idx.Method != "btree" {
			issues = append(issues, pipeline.PortabilityIssue{
				Pos:         idx.Pos,
				Construct:   "INDEX USING " + strings.ToUpper(idx.Method),
				Alternative: "Only BTREE index type is in the SQL standard.",
			})
		}
	}

	return issues
}

func analyzeColumn(col *ir.Column, table string) []pipeline.PortabilityIssue {
	var issues []pipeline.PortabilityIssue

	typName := strings.ToLower(col.Type.Name)

	// PG-specific types.
	pgSpecificTypes := map[string]string{
		"jsonb":     "Use JSON (standard) instead of JSONB for portability.",
		"uuid":      "UUID is not in SQL standard; use CHAR(36) or BINARY(16).",
		"bytea":     "Use BLOB / BINARY for portability.",
		"tsquery":   "PG full-text search type; no standard equivalent.",
		"tsvector":  "PG full-text search type; no standard equivalent.",
		"inet":      "PG network type; use VARCHAR for portability.",
		"cidr":      "PG network type; use VARCHAR for portability.",
		"macaddr":   "PG network type; use VARCHAR for portability.",
		"point":     "PG geometric type; use PostGIS or GEOMETRY for portability.",
		"hstore":    "PG key-value type; use JSON/JSONB for portability.",
		"xml":       "XML is in SQL standard but rarely portable across vendors.",
		"int4range": "PG range type; no standard equivalent.",
		"int8range": "PG range type; no standard equivalent.",
		"numrange":  "PG range type; no standard equivalent.",
		"tsrange":   "PG range type; no standard equivalent.",
		"tstzrange": "PG range type; no standard equivalent.",
		"daterange": "PG range type; no standard equivalent.",
	}

	for pgType, alt := range pgSpecificTypes {
		if strings.HasPrefix(typName, pgType) {
			issues = append(issues, pipeline.PortabilityIssue{
				Pos:         col.SrcPos,
				Construct:   col.Type.Name,
				Alternative: alt,
			})
		}
	}

	if col.Compression != nil {
		issues = append(issues, pipeline.PortabilityIssue{
			Pos:         col.SrcPos,
			Construct:   "COMPRESSION",
			Alternative: "Column-level compression is PG 14+; no standard equivalent.",
		})
	}

	return issues
}

// ── Function ──────────────────────────────────────────────────────────────────

func analyzeFunction(f *ir.Function) []pipeline.PortabilityIssue {
	var issues []pipeline.PortabilityIssue
	pos := f.SrcPos

	lang := strings.ToLower(f.Attrs.Language)
	if lang == "plpgsql" {
		issues = append(issues, pipeline.PortabilityIssue{
			Pos:         pos,
			Construct:   "LANGUAGE plpgsql",
			Alternative: "PL/pgSQL is PG-specific; use SQL functions for portability.",
		})
	}
	if f.Attrs.SecurityDef {
		issues = append(issues, pipeline.PortabilityIssue{
			Pos:         pos,
			Construct:   "SECURITY DEFINER",
			Alternative: "SECURITY DEFINER is PG-specific; standard SQL uses roles.",
		})
	}

	return issues
}

// ── Type ──────────────────────────────────────────────────────────────────────

func analyzeType(t *ir.Type) []pipeline.PortabilityIssue {
	var issues []pipeline.PortabilityIssue
	pos := t.SrcPos

	switch t.Variant {
	case "ENUM":
		issues = append(issues, pipeline.PortabilityIssue{
			Pos:         pos,
			Construct:   "CREATE TYPE AS ENUM",
			Alternative: "PG ENUM is non-standard; use a lookup table with a FK constraint.",
		})
	case "RANGE":
		issues = append(issues, pipeline.PortabilityIssue{
			Pos:         pos,
			Construct:   "CREATE TYPE AS RANGE",
			Alternative: "PG range types have no standard SQL equivalent.",
		})
	case "BASE":
		issues = append(issues, pipeline.PortabilityIssue{
			Pos:         pos,
			Construct:   "CREATE TYPE (base/shell)",
			Alternative: "Base types are PG-specific; no standard equivalent.",
		})
	}

	return issues
}

var _ pipeline.PortabilityAnalyzer = (*Analyzer)(nil)
