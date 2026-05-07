package dpg

import (
	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/project"

	// Concrete implementations register into pipeline.Default via init().
	_ "github.com/dullkingsman/dpg/internal/blockparser"
	_ "github.com/dullkingsman/dpg/internal/diff"
	_ "github.com/dullkingsman/dpg/internal/emit"
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

// ── Pipeline AST sub-types (appear in IR field types) ────────────────────────

// RawExpr is an opaque SQL expression stored as raw text. Appears in Index.Columns
// (expression indexes), Index.Where (partial indexes), and MigrateRemoveBlock.SQL.
type RawExpr = pipeline.RawExpr

// Identifier is a (possibly schema-qualified) SQL identifier. Appears in
// Index.Columns (collation, operator class) and TSMappingDef.Dictionary.
type Identifier = pipeline.Identifier

// IndexColumn is one entry in an Index.Columns slice. Name is set for simple
// column references; Expr is set for expression indexes.
type IndexColumn = pipeline.IndexColumn

// StorageParam is a key=value pair from a WITH (...) storage clause. Appears in
// Index.With.
type StorageParam = pipeline.StorageParam

// TSMappingDef is a MAPPING FOR { } entry in a TEXT SEARCH CONFIGURATION block.
// Appears in TSConfig.Mappings.
type TSMappingDef = pipeline.TSMappingDef

// MigrateRemoveBlock is the MIGRATE REMOVE { } directive on an ENUM type. Holds
// the optional DML SQL to run against columns referencing the type before the
// enum values are removed. Appears in Type.MigrateRemove.
type MigrateRemoveBlock = pipeline.MigrateRemoveBlock

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

// Registry holds named pipeline stage implementations. Register custom
// implementations with Register and override any built-in stage.
// Use NewRegistry for a clean registry or Default to extend the built-ins.
type Registry = pipeline.Registry

// NewRegistry returns an empty Registry. Use it to build an isolated registry
// independent of the process-wide Default.
func NewRegistry() *Registry { return pipeline.NewRegistry() }

// Default is the process-wide registry populated by the built-in
// implementations on import. Register custom extensions here to have them
// picked up by Compile, Lint, and Diff.
var Default = pipeline.Default

// Well-known registry keys — pass to Register / ResolveLinter etc.
const (
	KeyTokenizer          = pipeline.KeyTokenizer
	KeyPGSQLParser        = pipeline.KeyPGSQLParser
	KeyBlockParser        = pipeline.KeyBlockParser
	KeyIRBuilder          = pipeline.KeyIRBuilder
	KeyMerger             = pipeline.KeyMerger
	KeyDependencyResolver = pipeline.KeyDependencyResolver
	KeySnapshotStore      = pipeline.KeySnapshotStore
	KeyDiffer             = pipeline.KeyDiffer
	KeyEmitter            = pipeline.KeyEmitter
	KeyApplyExecutor      = pipeline.KeyApplyExecutor
	KeyIntrospector       = pipeline.KeyIntrospector
	KeyLinter             = pipeline.KeyLinter
	KeySecretResolver     = pipeline.KeySecretResolver
)

// ── Extension interfaces ──────────────────────────────────────────────────────

// Linter runs lint rules over compiled IR and returns diagnostics.
// Implement and register with Default.Register(KeyLinter, myLinter).
// Use NewChainLinter to augment rather than replace the built-in linter.
type Linter = pipeline.Linter

// Differ compares desired IR state against a snapshot and returns DiffOps.
// Implement and register with Default.Register(KeyDiffer, myDiffer).
type Differ = pipeline.Differ

// Emitter converts ordered DiffOps into a Migration.
// Implement and register with Default.Register(KeyEmitter, myEmitter).
type Emitter = pipeline.Emitter

// SecretResolver resolves secret URI strings to plaintext connection values.
// Implement and register with Default.Register(KeySecretResolver, myResolver).
type SecretResolver = pipeline.SecretResolver

// ── Types needed to implement the extension interfaces ────────────────────────

// DiffOp is a single migration operation produced by a Differ.
type DiffOp = pipeline.DiffOp

// Safety classifies the risk of a migration operation.
type Safety = pipeline.Safety

const (
	Safe        = pipeline.Safe
	Caution     = pipeline.Caution
	Destructive = pipeline.Destructive
	Manual      = pipeline.Manual
)

// Migration is the output of an Emitter.
type Migration = pipeline.Migration

// MigrationMeta holds header metadata written into every migration output.
type MigrationMeta = pipeline.MigrationMeta

// CompilerError is a structured error with source position from any pipeline stage.
type CompilerError = pipeline.CompilerError

// Diagnostics is an ordered collection of CompilerErrors.
type Diagnostics = pipeline.Diagnostics

// ── Registry helpers ──────────────────────────────────────────────────────────

// ResolveLinter returns the Linter registered in r, or (nil, false) if none.
func ResolveLinter(r *Registry) (Linter, bool) {
	return pipeline.Resolve[pipeline.Linter](r, pipeline.KeyLinter)
}

// ResolveDiffer returns the Differ registered in r, or (nil, false) if none.
func ResolveDiffer(r *Registry) (Differ, bool) {
	return pipeline.Resolve[pipeline.Differ](r, pipeline.KeyDiffer)
}

// ResolveEmitter returns the Emitter registered in r, or (nil, false) if none.
func ResolveEmitter(r *Registry) (Emitter, bool) {
	return pipeline.Resolve[pipeline.Emitter](r, pipeline.KeyEmitter)
}

// ResolveSecretResolver returns the SecretResolver registered in r, or (nil, false) if none.
func ResolveSecretResolver(r *Registry) (SecretResolver, bool) {
	return pipeline.Resolve[pipeline.SecretResolver](r, pipeline.KeySecretResolver)
}

// NewChainLinter returns a Linter that runs each provided linter in order and
// merges their diagnostics. Use it to augment the built-in linter rather than
// replace it:
//
//	builtin, _ := dpg.ResolveLinter(dpg.Default)
//	chained := dpg.NewChainLinter(builtin, &myCustomLinter{})
func NewChainLinter(linters ...Linter) Linter {
	return &chainLinter{linters: linters}
}

type chainLinter struct{ linters []Linter }

func (c *chainLinter) Lint(objects []IRObject, cfg LinterConfig) ([]LintDiagnostic, error) {
	var all []LintDiagnostic
	for _, l := range c.linters {
		diags, err := l.Lint(objects, cfg)
		if err != nil {
			return nil, err
		}
		all = append(all, diags...)
	}
	return all, nil
}

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

// Diff compares desired IR state against snap and returns the ordered set of
// DiffOps needed to migrate from snap to desired. It uses the Differ registered
// in the Default registry.
func Diff(desired []IRObject, snap *Snapshot) ([]DiffOp, error) {
	differ, err := pipeline.MustResolve[pipeline.Differ](pipeline.Default, pipeline.KeyDiffer)
	if err != nil {
		return nil, err
	}
	return differ.Diff(desired, snap)
}

// Discover walks up from dir until it finds a dpg.toml project root, then
// builds and returns the fully-resolved Project (all clusters and databases
// with their source file lists).
func Discover(dir string) (*Project, error) {
	return project.Discover(dir)
}
