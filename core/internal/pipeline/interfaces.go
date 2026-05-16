package pipeline

import "context"

// Tokenizer scans .dpg source files and splits each declaration into its
// raw Part 1 (PG SQL) and Part 2 ({ } block) text.
// Default implementation: internal/scanner.
type Tokenizer interface {
	Scan(path string, src []byte) ([]RawObject, error)
}

// PGSQLParser parses the Part 1 PG SQL text of a declaration by prepending
// the correct CREATE verb and feeding it to the real PostgreSQL parser.
// Default implementation: internal/pgparser.LibPQParser (uses libpg_query via
// github.com/pganalyze/pg_query_go/v5).
// Alternative: internal/pgparser.NativeParser (no CGo; reduced coverage).
type PGSQLParser interface {
	Parse(kind ObjectKind, part1 string, pos SourcePos) (PGParseResult, error)
}

// BlockParser parses the Part 2 { } block text of a declaration into a BlockAST.
// Default implementation: internal/blockparser.
type BlockParser interface {
	Parse(kind ObjectKind, part2 string, pos SourcePos) (BlockAST, error)
}

// IRBuilder converts a (PGParseResult, BlockAST) pair into a fully-resolved
// IRObject. All names are fully qualified and cross-file references resolved.
// Default implementation: internal/ir.Builder.
type IRBuilder interface {
	Build(pg PGParseResult, block BlockAST) (IRObject, error)
}

// Merger merges same-object IRObject declarations from multiple .dpg files
// according to RFC §2.7 set/scalar merge rules.
// Default implementation: internal/merger.
type Merger interface {
	Merge(objects []IRObject) ([]IRObject, error)
}

// DependencyResolver performs topological sort and circular-FK resolution
// on the merged object graph.
// Default implementation: internal/graph.
type DependencyResolver interface {
	Sort(objects []IRObject) ([]IRObject, error)
}

// SnapshotStore reads and writes the committed schema snapshot.
// Default implementation: internal/snapshot.FileStore (JSON file on disk).
// Alternatives: GitStore (git object store), DBStore (dedicated PG table).
type SnapshotStore interface {
	Load(cluster, database string) (*Snapshot, error)
	Save(cluster, database string, s *Snapshot) error
}

// Differ compares desired IR state against the snapshot and produces an
// ordered list of DiffOps.
// Default implementation: internal/diff.StandardDiffer.
// Alternative: internal/diff.NullDiffer (always empty, for bootstrap).
type Differ interface {
	Diff(desired []IRObject, snap *Snapshot) ([]DiffOp, error)
}

// Emitter converts ordered DiffOps into a Migration.
// Default implementation: internal/emit.SQLEmitter (RFC §20.2 SQL format).
// Alternatives: JSONEmitter (machine-readable), DryRunEmitter (human-readable plan).
type Emitter interface {
	Emit(ops []DiffOp, meta MigrationMeta) (Migration, error)
}

// ApplyExecutor executes a Migration against a live database connection.
// Default implementation: internal/executor.PgxExecutor.
type ApplyExecutor interface {
	Apply(ctx context.Context, m Migration, conn Conn) error
}

// Introspector reads a live PG catalog and returns an IRObject slice
// equivalent to what the compiler would produce from .dpg source files.
// Default implementation: internal/introspect.CatalogIntrospector.
// Alternative: internal/introspect.SnapshotIntrospector.
type Introspector interface {
	Introspect(ctx context.Context, conn Querier) ([]IRObject, error)
}

// Linter runs lint rules over the merged IR and returns diagnostics.
// Default implementation: internal/linter.BuiltinLinter.
// Compose multiple linters with a ChainLinter.
type Linter interface {
	Lint(objects []IRObject, cfg LinterConfig) ([]LintDiagnostic, error)
}

// PortabilityAnalyzer walks the IR and reports PG-specific constructs.
// Default implementation: internal/portability.Analyzer.
type PortabilityAnalyzer interface {
	Analyze(objects []IRObject) ([]PortabilityIssue, error)
}

// SecretResolver resolves secret URIs to plaintext values at connection time.
//   - "env:VAR_NAME"        → os.Getenv("VAR_NAME")
//   - "link:vault://..."    → vault lookup (stub until vault support is added)
//
// Default implementation: internal/secrets.EnvResolver.
// Compose resolvers with ChainResolver (tries each in order, first non-error wins).
type SecretResolver interface {
	Resolve(uri string) (string, error)
}
