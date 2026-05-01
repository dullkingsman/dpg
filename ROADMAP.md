# Roadmap

This document describes the planned direction for DPG. It is a living document and will be updated as priorities shift. Items are not binding commitments.

For the full language specification see [rfc/v0.8.0.md](rfc/v0.8.0.md).

---

## v0.1.x — Stability (current)

Focus: correctness and reliability for the object types already supported.

- [x] CI pipeline (GitHub Actions) — `go test`, `go vet` on push and PR
- [x] Release pipeline — cross-platform binaries and checksums on tag push
- [x] Differ: error instead of silent no-op when a pass-through object body is not captured
- [x] Tests for `graph`, `merger`, `compiler`, `project`, `config` packages
- [x] Silent no-op correctness fixes: grant diffing (tables/views/functions), column STORAGE/COMPRESSION/STATISTICS, table INHERITS, materialized view WITH NO DATA, recursive view snapshot tracking, MIGRATE REMOVE fails loudly instead of silently
- [ ] Tests for `introspect` package (requires live PG; tracked separately as integration tests)
- [ ] Bug fixes driven by early adopter feedback

---

## v0.2.0 — Formatter

Focus: canonical formatting for `.dpg` source files.

- [x] `dpg fmt` — reformat `.dpg` source files in place
  - Consistent indentation and spacing
  - Normalised keyword casing
  - Idempotent: running twice produces no change
  - Comment-preserving: line and block comments are re-attached to the nearest node
- [x] `--check` flag for use in CI — exits non-zero if any file would change
- [x] `--diff` flag — prints unified diff without writing files
- [x] `[fmt]` section in `dpg.toml` — `indent` and `keyword_case` options
- [ ] Canonical column/constraint ordering within a table block (planned for v0.2.x)
- [ ] Format-on-save integration guide for editors (via a `dpg fmt` shell wrapper)

---

## v0.3.0 — Extensibility

Focus: allow third-party tools to integrate with the DPG pipeline without forking.

- [x] `pkg/dpg` public API — re-exports all IR types, `Compile`, `Lint`, and `Discover`; `Registry` and `Default` exposed for future extension
- [ ] Open `internal/pipeline` — promote the registry and core interfaces for external packages to register custom differs, emitters, linters, and secret resolvers
- [ ] Documented extension points and an example plugin
- [ ] Stable Go module API (no breaking changes to public packages within the v0.x line)

---

## v0.4.0 — Broader Object Coverage

Focus: close the gaps in PostgreSQL object support per RFC Appendix A.

- [x] Triggers (`CREATE TRIGGER` / `DROP TRIGGER`)
- [x] Full-text search objects (`TEXT SEARCH CONFIGURATION`, `DICTIONARY`, `PARSER`, `TEMPLATE`)
- [x] Foreign Data Wrappers (`FOREIGN DATA WRAPPER`, `SERVER`, `USER MAPPING`, `FOREIGN TABLE`)
- [x] Replication publications and subscriptions
- [x] Tablespaces
- [x] Row-level security policies (`POLICY` inside `{ }` blocks)
- [ ] Partitioning strategies: `SnapPartition`, differ support, `pg_partitioned_table` introspection, `--approve-partition-rebuild` flag
- [ ] `MIGRATE REMOVE` full implementation — 7-step enum value removal procedure (RFC §20.2)
- [ ] Column-level grant tracking: `SnapColumn.Grants`, snapshot population, differ support
- [ ] Semantic diffing for aggregates (currently body-hash only; any change triggers DROP+CREATE)

---

## v0.5.0 — Developer Experience

Focus: quality-of-life improvements for day-to-day use.

- [ ] `dpg init` — scaffold a new project interactively
- [ ] `dpg validate` — offline schema validation without diffing (syntax, type resolution, constraint sanity)
- [ ] Watch mode for `dpg plan` — re-run on source file changes
- [ ] JSON / machine-readable output flag for all commands
- [ ] Shell completion (Bash, Zsh, Fish) via Cobra

---

## v1.0.0 — Stable

Milestone criteria for a stable release:

- Public API (post v0.3.0) has been stable for at least two minor releases
- Core object types (tables, views, functions, types, roles, sequences, extensions) are fully covered by the differ and tested
- CI is green on Linux (amd64, arm64) and macOS (arm64)
- No known correctness bugs in plan/apply
- RFC v1.0.0 ratified

---

## Ecosystem (post v1.0.0)

These projects are planned as separate repositories once the core tool is stable.

### Language Server (dpg-lsp)

A Language Server Protocol implementation for `.dpg` files, enabling rich editor support across any LSP-compatible editor (VS Code, Neovim, Helix, JetBrains, etc.).

- Diagnostics: syntax errors, unresolved type references, linter warnings in-editor
- Hover: column type, constraint details, object documentation
- Go-to-definition: navigate from a `REFERENCES` target or type name to its declaration
- Completions: table names, column names, constraint types, DPG keywords
- Code actions: apply lint fixes, add missing `PRIMARY KEY`

### Tree-sitter Grammar (tree-sitter-dpg)

A [tree-sitter](https://tree-sitter.github.io) grammar for `.dpg` files.

- Syntax highlighting for editors that use tree-sitter (Neovim, Helix, Zed, GitHub)
- Structural queries for the language server and other tooling
- Foundation for `dpg fmt` parser-based formatting

### IDE Plugins

Built on top of the language server and tree-sitter grammar, as separate repositories per editor:

- **VS Code** (`vscode-dpg`) — syntax highlighting, LSP client, schema explorer sidebar
- **JetBrains** (`intellij-dpg`) — file type support, LSP integration, inspections

---

## Not Planned

- A graphical UI (the CLI is the interface)
- Support for databases other than PostgreSQL
- An ORM or query builder layer
- Automatic schema migration without explicit `dpg apply` approval
