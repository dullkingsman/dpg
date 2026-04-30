package dpg

import (
	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/project"

	// Concrete implementations register into pipeline.Default via init().
	_ "github.com/dullkingsman/dpg/internal/blockparser"
	_ "github.com/dullkingsman/dpg/internal/graph"
	_ "github.com/dullkingsman/dpg/internal/linter"
	_ "github.com/dullkingsman/dpg/internal/merger"
	_ "github.com/dullkingsman/dpg/internal/pgparser"
	_ "github.com/dullkingsman/dpg/internal/scanner"
)

// ── Source positions ──────────────────────────────────────────────────────────

type SourcePos = pipeline.SourcePos

// ── IR interface and object kinds ─────────────────────────────────────────────

type IRObject = pipeline.IRObject
type ObjectKind = pipeline.ObjectKind

const (
	KindUnknown           = pipeline.KindUnknown
	KindSchema            = pipeline.KindSchema
	KindExtension         = pipeline.KindExtension
	KindTable             = pipeline.KindTable
	KindUnloggedTable     = pipeline.KindUnloggedTable
	KindForeignTable      = pipeline.KindForeignTable
	KindView              = pipeline.KindView
	KindMaterializedView  = pipeline.KindMaterializedView
	KindRecursiveView     = pipeline.KindRecursiveView
	KindFunction          = pipeline.KindFunction
	KindProcedure         = pipeline.KindProcedure
	KindAggregate         = pipeline.KindAggregate
	KindEnum              = pipeline.KindEnum
	KindCompositeType     = pipeline.KindCompositeType
	KindRangeType         = pipeline.KindRangeType
	KindDomainType        = pipeline.KindDomainType
	KindBaseType          = pipeline.KindBaseType
	KindSequence          = pipeline.KindSequence
	KindRole              = pipeline.KindRole
	KindTablespace        = pipeline.KindTablespace
	KindFDW               = pipeline.KindFDW
	KindServer            = pipeline.KindServer
	KindUserMapping       = pipeline.KindUserMapping
	KindPublication       = pipeline.KindPublication
	KindSubscription      = pipeline.KindSubscription
	KindEventTrigger      = pipeline.KindEventTrigger
	KindCollation         = pipeline.KindCollation
	KindOperator          = pipeline.KindOperator
	KindOperatorClass     = pipeline.KindOperatorClass
	KindOperatorFamily    = pipeline.KindOperatorFamily
	KindCast              = pipeline.KindCast
	KindStatisticsObject  = pipeline.KindStatisticsObject
	KindTSConfig          = pipeline.KindTSConfig
	KindTSDict            = pipeline.KindTSDict
	KindTSParser          = pipeline.KindTSParser
	KindTSTemplate        = pipeline.KindTSTemplate
	KindDefaultPrivileges = pipeline.KindDefaultPrivileges
)

// ── Concrete IR object types ──────────────────────────────────────────────────

type Schema = ir.Schema
type Extension = ir.Extension
type Table = ir.Table
type View = ir.View
type Function = ir.Function
type Procedure = ir.Procedure
type Aggregate = ir.Aggregate
type Type = ir.Type
type Sequence = ir.Sequence
type Role = ir.Role
type Tablespace = ir.Tablespace
type ForeignDataWrapper = ir.ForeignDataWrapper
type ForeignServer = ir.ForeignServer
type UserMapping = ir.UserMapping
type Publication = ir.Publication
type Subscription = ir.Subscription
type EventTrigger = ir.EventTrigger
type Collation = ir.Collation
type Operator = ir.Operator
type OperatorClass = ir.OperatorClass
type OperatorFamily = ir.OperatorFamily
type Cast = ir.Cast
type StatisticsObject = ir.StatisticsObject
type TSConfig = ir.TSConfig
type TSDict = ir.TSDict
type TSParser = ir.TSParser
type TSTemplate = ir.TSTemplate
type DefaultPrivileges = ir.DefaultPrivileges

// ── IR sub-types ──────────────────────────────────────────────────────────────

type Column = ir.Column
type TypeRef = ir.TypeRef
type Index = ir.Index
type Constraint = ir.Constraint
type Policy = ir.Policy
type Trigger = ir.Trigger
type Grant = ir.Grant
type Revocation = ir.Revocation
type FuncArg = ir.FuncArg
type FuncAttrs = ir.FuncAttrs
type PartitionSpec = ir.PartitionSpec
type Partition = ir.Partition
type Generated = ir.Generated
type Identity = ir.Identity

// ── Linter types ──────────────────────────────────────────────────────────────

type LintDiagnostic = pipeline.LintDiagnostic
type LinterConfig = pipeline.LinterConfig

// ── Snapshot ──────────────────────────────────────────────────────────────────

type Snapshot = pipeline.Snapshot

// ── Project structure ─────────────────────────────────────────────────────────

type Project = project.Project
type Cluster = project.Cluster
type Database = project.Database

// ── Plugin registry ───────────────────────────────────────────────────────────

// Registry is the pipeline plugin registry. External packages can register
// custom Linter, Emitter, and SecretResolver implementations before calling
// Compile or Lint. The zero value is not valid; use pipeline.NewRegistry() or
// the Default registry.
//
// Custom differ/emitter extension points are planned for v0.3.0.
type Registry = pipeline.Registry

// Default is the process-wide registry populated by built-in implementations.
var Default = pipeline.Default

// ── Public API functions ──────────────────────────────────────────────────────

// Compile reads the given .dpg source files rooted at dbDir, runs them
// through the full compilation pipeline (scan → parse → IR → merge →
// topological sort), and returns a sorted slice of fully-resolved IRObjects.
//
// The Default registry is used. All built-in pipeline stages are registered
// automatically via init() when this package is imported.
func Compile(files []string, dbDir string) ([]IRObject, error) {
	return compiler.Compile(files, dbDir, pipeline.Default)
}

// Lint runs the built-in linter rules over the compiled IR and returns
// diagnostics. It uses the Linter registered in the Default registry.
func Lint(objects []IRObject, cfg LinterConfig) ([]LintDiagnostic, error) {
	linter, err := pipeline.MustResolve[pipeline.Linter](pipeline.Default, pipeline.KeyLinter)
	if err != nil {
		return nil, err
	}
	return linter.Lint(objects, cfg)
}

// Discover walks up from dir until it finds a dpg.toml project root, then
// builds and returns the fully-resolved Project (all clusters and databases
// with their source file lists).
func Discover(dir string) (*Project, error) {
	return project.Discover(dir)
}
