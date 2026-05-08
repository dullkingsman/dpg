---
title: "Pipeline Overview"
description: "The 6-stage DPG compilation pipeline, the Registry pattern, and where extension points live."
weight: 1
---

## The DPG Pipeline

Every `dpg` command drives the same underlying pipeline. The stages run in order; each receives the output of the previous stage.

```
.dpg files
    │
    ▼
Tokenizer          — scan files, split each declaration into Part 1 (PG SQL) + Part 2 ({ } block)
    │
    ▼
PGSQLParser        — prepend CREATE verb, parse Part 1 through libpg_query
    │
    ▼
BlockParser        — parse Part 2 { } block into a BlockAST
    │
    ▼
IRBuilder          — combine (PGParseResult, BlockAST) → typed IRObject
    │
    ▼
Merger             — merge same-object declarations from multiple files (RFC §2.7)
    │
    ▼
DependencyResolver — topological sort; circular FK → DEFERRABLE
    │
    ▼  (desired []IRObject)
    ├──→ Linter              — lint rules over merged IR → []LintDiagnostic
    ├──→ PortabilityAnalyzer — PG-specific construct report
    │
    ▼
Differ             — desired IR vs Snapshot → []DiffOp
    │
    ▼
Emitter            — []DiffOp + metadata → Migration (SQL text)
    │
    ▼
ApplyExecutor      — execute Migration against live PG connection (apply only)
    │
    ▼
SnapshotStore      — persist updated Snapshot after successful apply
```

The `Linter` and `PortabilityAnalyzer` run over the merged IR in parallel with diffing; they do not block or modify the migration.

---

## The Registry Pattern

Every stage implementation is stored in a `Registry` under a well-known key. At startup, each concrete implementation package registers itself in an `init()` function. Commands resolve implementations through the registry.

```go
// The process-wide registry — concrete packages register into this in init().
var Default = pipeline.NewRegistry()

// Register replaces the implementation for a key.
Default.Register(pipeline.KeyLinter, myLinter)

// Resolve retrieves a typed implementation.
linter, ok := pipeline.Resolve[pipeline.Linter](Default, pipeline.KeyLinter)
```

`pkg/dpg` re-exports the registry API so plugin authors never need to import internal packages:

```go
import "github.com/dullkingsman/dpg/pkg/dpg"

dpg.Default.Register(dpg.KeyLinter, myLinter)
```

### Registry Keys

| Key constant | Interface | Default implementation |
|---|---|---|
| `KeyTokenizer` | `Tokenizer` | `internal/scanner` |
| `KeyPGSQLParser` | `PGSQLParser` | `internal/pgparser.LibPQParser` |
| `KeyBlockParser` | `BlockParser` | `internal/blockparser` |
| `KeyIRBuilder` | `IRBuilder` | `internal/ir.Builder` |
| `KeyMerger` | `Merger` | `internal/merger` |
| `KeyDependencyResolver` | `DependencyResolver` | `internal/graph` |
| `KeySnapshotStore` | `SnapshotStore` | `internal/snapshot.FileStore` |
| `KeyDiffer` | `Differ` | `internal/diff.StandardDiffer` |
| `KeyEmitter` | `Emitter` | `internal/emit.SQLEmitter` |
| `KeyApplyExecutor` | `ApplyExecutor` | `internal/executor.PgxExecutor` |
| `KeyIntrospector` | `Introspector` | `internal/introspect.CatalogIntrospector` |
| `KeyLinter` | `Linter` | `internal/linter.BuiltinLinter` |
| `KeyPortabilityAnalyzer` | `PortabilityAnalyzer` | `internal/portability.Analyzer` |
| `KeySecretResolver` | `SecretResolver` | `internal/secrets.EnvResolver` |

### Isolated Registries

For testing or embedding DPG in a larger application, create an isolated registry instead of modifying `Default`:

```go
r := dpg.NewRegistry()
r.Register(dpg.KeyLinter, myLinter)
r.Register(dpg.KeySnapshotStore, myStore)
// pass r to compiler/executor functions that accept a *dpg.Registry
```

---

## Extension Points for Plugin Authors

The most useful extension points are:

| Extension point | Use case |
|---|---|
| `Linter` | Add project-specific lint rules (naming conventions, required comments, etc.) |
| `SnapshotStore` | Store snapshots in a database or object store instead of the filesystem |
| `ApplyExecutor` | Wrap migration execution with audit logging, dry-run mode, or approval gates |
| `SecretResolver` | Add custom secret providers (Vault, AWS Secrets Manager, etc.) |

Replacing core pipeline stages (`Tokenizer`, `IRBuilder`, `Differ`, etc.) is possible but not recommended — these interfaces are internal contracts and may change between minor versions. Only `Linter`, `SnapshotStore`, `ApplyExecutor`, and `SecretResolver` are considered stable extension surfaces.
