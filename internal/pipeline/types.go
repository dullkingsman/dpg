package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SourcePos identifies a location in a .dpg source file.
type SourcePos struct {
	File string
	Line int
	Col  int
}

func (p SourcePos) String() string {
	if p.File == "" {
		return "<unknown>"
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// ObjectKind identifies the type of a DPG declaration.
type ObjectKind int

const (
	KindUnknown ObjectKind = iota
	KindSchema
	KindExtension
	KindTable
	KindUnloggedTable
	KindForeignTable
	KindView
	KindMaterializedView
	KindRecursiveView
	KindFunction
	KindProcedure
	KindAggregate
	KindEnum
	KindCompositeType
	KindRangeType
	KindDomainType
	KindBaseType
	KindSequence
	KindRole
	KindTablespace
	KindFDW
	KindServer
	KindUserMapping
	KindPublication
	KindSubscription
	KindEventTrigger
	KindCollation
	KindOperator
	KindOperatorClass
	KindOperatorFamily
	KindCast
	KindStatisticsObject
	KindTSConfig
	KindTSDict
	KindTSParser
	KindTSTemplate
	KindDefaultPrivileges
	KindVirtualType
	KindMacro
)

func (k ObjectKind) String() string {
	switch k {
	case KindSchema:
		return "SCHEMA"
	case KindExtension:
		return "EXTENSION"
	case KindTable:
		return "TABLE"
	case KindUnloggedTable:
		return "UNLOGGED TABLE"
	case KindForeignTable:
		return "FOREIGN TABLE"
	case KindView:
		return "VIEW"
	case KindMaterializedView:
		return "MATERIALIZED VIEW"
	case KindRecursiveView:
		return "RECURSIVE VIEW"
	case KindFunction:
		return "FUNCTION"
	case KindProcedure:
		return "PROCEDURE"
	case KindAggregate:
		return "AGGREGATE"
	case KindEnum:
		return "ENUM"
	case KindCompositeType:
		return "COMPOSITE TYPE"
	case KindRangeType:
		return "RANGE TYPE"
	case KindDomainType:
		return "DOMAIN"
	case KindBaseType:
		return "BASE TYPE"
	case KindSequence:
		return "SEQUENCE"
	case KindRole:
		return "ROLE"
	case KindTablespace:
		return "TABLESPACE"
	case KindFDW:
		return "FOREIGN DATA WRAPPER"
	case KindServer:
		return "SERVER"
	case KindUserMapping:
		return "USER MAPPING"
	case KindPublication:
		return "PUBLICATION"
	case KindSubscription:
		return "SUBSCRIPTION"
	case KindEventTrigger:
		return "EVENT TRIGGER"
	case KindCollation:
		return "COLLATION"
	case KindOperator:
		return "OPERATOR"
	case KindOperatorClass:
		return "OPERATOR CLASS"
	case KindOperatorFamily:
		return "OPERATOR FAMILY"
	case KindCast:
		return "CAST"
	case KindStatisticsObject:
		return "STATISTICS"
	case KindTSConfig:
		return "TEXT SEARCH CONFIGURATION"
	case KindTSDict:
		return "TEXT SEARCH DICTIONARY"
	case KindTSParser:
		return "TEXT SEARCH PARSER"
	case KindTSTemplate:
		return "TEXT SEARCH TEMPLATE"
	case KindDefaultPrivileges:
		return "DEFAULT PRIVILEGES"
	case KindVirtualType:
		return "VIRTUAL TYPE"
	case KindMacro:
		return "MACRO"
	default:
		return "UNKNOWN"
	}
}

// RawObject is the output of the Tokenizer stage: a single DPG declaration
// split into its two parts before any deeper parsing.
type RawObject struct {
	Kind ObjectKind
	// Part1 is the raw PG SQL text of the declaration, with the leading DPG
	// keyword(s) stripped. The PGSQLParser prepends the correct CREATE verb.
	Part1 string
	// Part2 is the raw text of the trailing { } block, or "" if absent.
	Part2 string
	// Schema is the enclosing schema name when this declaration was found inside
	// a SCHEMA { } block. Empty for top-level declarations.
	Schema string
	Pos    SourcePos
}

// Safety classifies the risk of a migration operation.
type Safety int

const (
	Safe        Safety = iota
	Caution            // locks or performance impact possible
	Destructive        // data loss possible; blocked by default
	Manual             // cannot run inside a transaction (e.g. CREATE INDEX CONCURRENTLY)
)

func (s Safety) String() string {
	switch s {
	case Safe:
		return "SAFE"
	case Caution:
		return "CAUTION"
	case Destructive:
		return "DESTRUCTIVE"
	case Manual:
		return "MANUAL"
	default:
		return "UNKNOWN"
	}
}

// DiffOp represents a single migration operation produced by the Differ.
type DiffOp interface {
	SQL() string
	Safety() Safety
	Pos() SourcePos
	// Transactional returns false for MANUAL ops (concurrent indexes, enum ADD VALUE)
	// which must execute outside a transaction block.
	Transactional() bool
}

// PGParseResult wraps the output of the PGSQLParser.
// Raw holds the pg_query parse result (type *pg_query.ParseResult from
// github.com/pganalyze/pg_query_go/v5, added in Phase 4a). In Phase 1 it is nil.
// For passthrough kinds (KindVirtualType, KindMacro), Raw holds the raw Part1
// string rather than a pg_query parse tree.
type PGParseResult struct {
	Raw any
	// Kind is the ObjectKind that produced this result. Non-zero only for
	// passthrough kinds (KindVirtualType) where Raw is not a pg_query tree.
	Kind ObjectKind
	Pos  SourcePos
	// SchemaContext is the enclosing SCHEMA name when this object was declared
	// inside a SCHEMA { } block. The IR builder uses it as a schema fallback
	// for objects whose Part1 text has no schema qualifier.
	SchemaContext string
}

// IRObject is the common interface for all fully-resolved internal representation
// objects. Concrete types are defined in internal/ir (Phase 5).
type IRObject interface {
	QualifiedName() string
	Pos() SourcePos
}

// Snapshot is the committed state written to .dpg/snapshots/ after each apply.
// Objects maps each object's QualifiedName to its JSON-encoded IR snapshot form.
type Snapshot struct {
	DPGVersion     string                     `json:"dpg_version"`
	Cluster        string                     `json:"cluster"`
	Database       string                     `json:"database,omitempty"`
	AppliedAt      string                     `json:"applied_at"`
	SourceRevision string                     `json:"source_revision"`
	Objects        map[string]json.RawMessage `json:"objects,omitempty"`
}

// SetObject stores obj under key in the snapshot's Objects map.
func (s *Snapshot) SetObject(key string, obj any) error {
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	if s.Objects == nil {
		s.Objects = make(map[string]json.RawMessage)
	}
	s.Objects[key] = data
	return nil
}

// GetObject decodes the snapshot object for key into dst.
func (s *Snapshot) GetObject(key string, dst any) (bool, error) {
	if s.Objects == nil {
		return false, nil
	}
	raw, ok := s.Objects[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, dst)
}

// MigrationMeta holds the header metadata written to every migration output.
type MigrationMeta struct {
	GeneratedAt    time.Time
	SourceRevision string
	Cluster        string
	Database       string
}

// Migration is the output of the Emitter stage.
type Migration struct {
	Meta             MigrationMeta
	Transactional    []DiffOp // wrapped in BEGIN/COMMIT
	NonTransactional []DiffOp // executed after COMMIT
}

// LintDiagnostic is a single finding from the Linter.
type LintDiagnostic struct {
	Pos     SourcePos
	Rule    string
	Message string
	IsError bool // true when --strict promotes this warning to an error
}

// PortabilityIssue is a single finding from the PortabilityAnalyzer.
type PortabilityIssue struct {
	Pos         SourcePos
	Construct   string // the PG-specific construct name
	Alternative string // standard SQL alternative, if any
}

// LinterConfig holds the resolved linter settings passed to Linter.Lint.
// Populated from config.LinterConfig by the CLI layer.
type LinterConfig struct {
	WarnOnDeprecated          bool
	RequireColumnComments     bool
	ForbidHardcodedPasswords  bool
	MaxColumnsPerTable        int
	WarnOnScalarMergeConflict bool
}

// Conn abstracts a database connection so the pipeline package does not import pgx.
// The pgx implementation in internal/executor wraps pgx.Conn to satisfy this interface.
type Conn interface {
	Exec(ctx context.Context, sql string, args ...any) (int64, error)
	Begin(ctx context.Context) (Tx, error)
	Close(ctx context.Context) error
}

// Tx abstracts a database transaction.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) (int64, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Rows is an iterator over query result rows. Mirrors pgx.Rows.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

// Querier extends Conn with row-returning query support.
// Used by the Introspector to read the live PG catalog.
type Querier interface {
	Conn
	QueryRows(ctx context.Context, sql string, args ...any) (Rows, error)
}

// CompilerError is a structured error produced by any pipeline stage,
// with a precise source location.
type CompilerError struct {
	Pos     SourcePos
	Message string
}

func (e *CompilerError) Error() string {
	return fmt.Sprintf("%s: %s", e.Pos, e.Message)
}

// Diagnostics is an ordered collection of CompilerErrors. It implements
// the error interface so it can be returned from any stage.
type Diagnostics []*CompilerError

func (d Diagnostics) Error() string {
	msgs := make([]string, len(d))
	for i, e := range d {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n")
}

func (d Diagnostics) HasErrors() bool {
	return len(d) > 0
}

// Errorf constructs a CompilerError at the given position.
func Errorf(pos SourcePos, format string, args ...any) *CompilerError {
	return &CompilerError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}
