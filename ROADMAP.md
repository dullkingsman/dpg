# Roadmap

This document describes the planned direction for DPG. It is a living document and will be updated as priorities shift. Items are not binding commitments.

For the full language specification see [rfc/dpg-1.md](rfc/dpg-1.md).

---

## v0.1.x — Stability (current)

Focus: correctness and reliability for the object types already supported.

- [x] CI pipeline (GitHub Actions) — `go test`, `go vet` on push and PR
- [x] Release pipeline — cross-platform binaries and checksums on tag push
- [x] Differ: error instead of silent no-op when a pass-through object body is not captured
- [x] Tests for `graph`, `merger`, `compiler`, `project`, `config` packages
- [x] Silent no-op correctness fixes: grant diffing (tables/views/functions), column STORAGE/COMPRESSION/STATISTICS, table INHERITS, materialized view WITH NO DATA, recursive view snapshot tracking, MIGRATE REMOVE fails loudly instead of silently
- [x] Tests for `introspect` package — integration test suite (`-tags integration`) using testcontainers; covers introspect, executor, and full compile→plan→apply→verify roundtrip
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
- [x] Canonical column/constraint ordering within a table block: column defs (source order) → FOREIGN KEY references (alpha) → other constraints (alpha); RENAMED FROM first in `{ }` blocks
- [x] Format-on-save integration guide for editors ([editor integration](https://dullkingsman.github.io/dpg/docs/getting-started/editor-integration/)) — VS Code (Run on Save), Neovim (autocmd), Helix (formatter wrapper), JetBrains (File Watchers), CI gate (`--check`)

---

## v0.3.0 — Extensibility

Focus: allow third-party tools to integrate with the DPG pipeline without forking.

- [x] `pkg/dpg` public API — re-exports all IR types, `Compile`, `Lint`, `Diff`, and `Discover`; `Registry` and `Default` exposed for extension
- [x] Open `internal/pipeline` — `pkg/dpg` re-exports `Linter`, `Differ`, `Emitter`, `SecretResolver` interfaces, `DiffOp`/`Safety`/`Migration`/`MigrationMeta` types, all registry key constants, `NewRegistry`, `ResolveLinter`, `NewChainLinter`, and registers `diff`/`emit` built-ins on import
- [x] Documented extension points and an example plugin (`examples/plugin/`) — custom `tableCommentLinter` implementing `dpg.Linter`, showing both replace and chain patterns; imports only `pkg/dpg`
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
- [x] Partitioning strategies: `SnapPartition`, `createTable` PARTITION BY emission, `diffPartitions` (add/remove/bounds-change/strategy-change), `introspectPartitions` via `pg_partitioned_table`
- [x] `MIGRATE REMOVE` full implementation — shadow type creation, DML passthrough, row verification, column type migration, drop+rename, comment re-apply
- [x] Column-level grant tracking: `SnapColumn.Grants`, snapshot population, differ support, introspection via `information_schema.column_privileges`
- [x] Semantic diffing for aggregates: structural changes (SFUNC, STYPE, etc.) emit DROP+CREATE (DESTRUCTIVE); comment/grant changes emit non-destructive ops without DROP

---

## v0.5.0 — Developer Experience

Focus: quality-of-life improvements for day-to-day use.

- [x] `dpg init` — scaffold a new project with standard layout; flags for cluster/database/schema/url; skips existing files
- [x] `dpg validate` — offline compile + lint without diffing; `--format json` for machine-readable output; exits non-zero on errors
- [x] Watch mode for `dpg plan --watch` — polls source file mtimes every 500 ms, re-runs on any change; exits on SIGINT/SIGTERM
- [x] JSON output via `--format json` on `dpg plan` and `dpg validate`
- [x] Shell completion (Bash, Zsh, Fish, PowerShell) via Cobra — `dpg completion <shell>`; documented in the [CLI reference](https://dullkingsman.github.io/dpg/docs/reference/commands/)

---

## v1.0.0 — Stable

Milestone criteria for a stable release:

- Public API (post v0.3.0) has been stable for at least two minor releases
- Core object types (tables, views, functions, types, roles, sequences, extensions) are fully covered by the differ and tested
- CI is green on Linux (amd64, arm64) and macOS (arm64)
- No known correctness bugs in plan/apply
- RFC v1.0.0 ratified

---

## Ecosystem

The following components exist in this repository and are developed alongside the core tool.

### Language Server (`lang/lsp/`)

A Language Server Protocol implementation for `.dpg` files (`dpg-lsp`), enabling rich editor support across any LSP-compatible editor (VS Code, Neovim, Helix, JetBrains, etc.).

- Diagnostics: syntax errors, unresolved type references, linter warnings in-editor
- Hover: column type, constraint details, object documentation
- Go-to-definition: navigate from a `REFERENCES` target or type name to its declaration
- Completions: table names, column names, constraint types, DPG keywords
- Code actions: apply lint fixes, add missing `PRIMARY KEY`

### Tree-sitter Grammar (`lang/grammar/`)

A [tree-sitter](https://tree-sitter.github.io) grammar for `.dpg` files.

- Syntax highlighting for editors that use tree-sitter (Neovim, Helix, Zed, GitHub)
- Structural queries for the language server and other tooling
- Foundation for `dpg fmt` parser-based formatting

### IDE Plugins (`plugins/`)

Editor integrations built on top of the language server and tree-sitter grammar:

- **VS Code** (`plugins/vscode/`) — syntax highlighting, LSP client, schema explorer sidebar
- **JetBrains** (`plugins/idea/`) — file type support, LSP integration, inspections
- **Neovim** (`plugins/nvim/`) — tree-sitter grammar, LSP config, and Telescope integration
- **Helix** (`plugins/helix/`) — language configuration and queries

---

## Upstream Integration

These items require PRs to external repositories. They are not blockers for any DPG release but improve discoverability and out-of-the-box experience. Each has prerequisites listed below.

### GitHub Linguist — `.dpg` syntax recognition

- **What**: Add DPG as a language in [github-linguist/linguist](https://github.com/github-linguist/linguist) so GitHub renders syntax highlighting and language statistics for `.dpg` files automatically.
- **What's needed**: A stable `grammar-v*` tagged release; sample `.dpg` files added to `vendor/grammars/`; entry in `lib/linguist/languages.yml` pointing to the grammar.
- **Prerequisites**: At least one `grammar-v*` release published; highlight queries reviewed and stable.

### nvim-treesitter — `:TSInstall dpg`

- **What**: Add DPG to the official parser list in [nvim-treesitter/nvim-treesitter](https://github.com/nvim-treesitter/nvim-treesitter) so users can install via `:TSInstall dpg` without any manual configuration.
- **What's needed**: The monorepo grammar at a stable `grammar-v*` tag; `highlights.scm`, `injections.scm`, and `locals.scm` queries passing nvim-treesitter's CI; an entry in `lua/nvim-treesitter/parsers.lua` pointing to `lang/grammar` in this repo.
- **Prerequisites**: `grammar-v*` releases stable for at least one minor version cycle; queries tested against a corpus of real `.dpg` files.

### Helix — built-in language support

- **What**: Add DPG as a built-in language in [helix-editor/helix](https://github.com/helix-editor/helix) so users get syntax highlighting, formatting, and LSP support without copying `languages.toml` manually.
- **What's needed**: Grammar and queries accepted upstream; `languages.toml` entry (prepared in `plugins/helix/`) merged into Helix's `languages.toml`; `dpg-lsp` documented in Helix's language server registry.
- **Prerequisites**: Helix's grammar review process requires the grammar to be stable and queries to be idiomatic (no regexes where structured rules suffice). At minimum one `grammar-v*` release; LSP tested against Helix's LSP client.

---

## Not Planned

- A graphical UI (the CLI is the interface)
- Support for databases other than PostgreSQL
- An ORM or query builder layer
- Automatic schema migration without explicit `dpg apply` approval
