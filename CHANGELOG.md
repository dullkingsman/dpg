# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.2-alpha.6] — 2026-05-17

- No changes.

## [0.5.2-alpha.5] — 2026-05-17

- No changes.

## [vscode-v0.5.2] — 2026-05-17

- No changes.

## [idea-v0.5.2-alpha.4] — 2026-05-17

### Added

- Introduce Name Maps with tool-specific naming conventions
- Enhance syntax highlighting, macros, and object parsing
- Support `PREFERRED JSON FORMAT` directive for virtual types
- Add support for structured virtual types with JSONB resolution
- Add support for nested macro expansion with circular reference detection
- Enable project-scoped macro sharing across `.dpg` files
- Add support for `DEFAULT PRIVILEGES` in snapshots and diffs
- Add support for LSP methods `SetTrace`, `TextDocumentDidSave`, and `WorkspaceDidChangeWatchedFiles`

### Fixed

- Improve LSP script handling by downloading before execution
- Correctly handle array expansion for LSP install arguments in install script
- Update prerelease detection regex in release workflows

### Changed

- Switch COMMENT and DEPRECATED directives to single quotes for consistency
- Update snapshot schema spec for enhanced readability and structure
- Update `introspect_integration_test` to use `introspect.New()` instead of `New()` for improved clarity and consistency
- Rename `website` to `site` for consistent naming convention
- Move core logic and tests into `core` directory for better modularity
- Standardize directory structure by renaming `editors` to `plugins` and `editors/lsp` to `lang/lsp`

## [vscode-v0.5.2-alpha.4] — 2026-05-17

### Added

- Introduce Name Maps with tool-specific naming conventions
- Enhance syntax highlighting, macros, and object parsing
- Support `PREFERRED JSON FORMAT` directive for virtual types
- Add support for structured virtual types with JSONB resolution
- Add support for nested macro expansion with circular reference detection
- Enable project-scoped macro sharing across `.dpg` files
- Add support for `DEFAULT PRIVILEGES` in snapshots and diffs
- Add support for LSP methods `SetTrace`, `TextDocumentDidSave`, and `WorkspaceDidChangeWatchedFiles`

### Fixed

- Improve LSP script handling by downloading before execution
- Correctly handle array expansion for LSP install arguments in install script

### Changed

- Switch COMMENT and DEPRECATED directives to single quotes for consistency
- Update snapshot schema spec for enhanced readability and structure
- Update `introspect_integration_test` to use `introspect.New()` instead of `New()` for improved clarity and consistency
- Rename `website` to `site` for consistent naming convention
- Move core logic and tests into `core` directory for better modularity
- Standardize directory structure by renaming `editors` to `plugins` and `editors/lsp` to `lang/lsp`

## [0.5.2-alpha.4] — 2026-05-17

### Changed

- Switch COMMENT and DEPRECATED directives to single quotes for consistency

## [0.5.2-alpha.3] — 2026-05-17

### Added

- Introduce Name Maps with tool-specific naming conventions

## [0.5.2-alpha.2] — 2026-05-17

### Added

- Enhance syntax highlighting, macros, and object parsing
- Support `PREFERRED JSON FORMAT` directive for virtual types
- Add support for structured virtual types with JSONB resolution
- Add support for nested macro expansion with circular reference detection
- Enable project-scoped macro sharing across `.dpg` files
- Add support for `DEFAULT PRIVILEGES` in snapshots and diffs

### Changed

- Update snapshot schema spec for enhanced readability and structure
- Update `introspect_integration_test` to use `introspect.New()` instead of `New()` for improved clarity and consistency
- Rename `website` to `site` for consistent naming convention
- Move core logic and tests into `core` directory for better modularity
- Standardize directory structure by renaming `editors` to `plugins` and `editors/lsp` to `lang/lsp`

## [0.5.2] — 2026-05-16

### Added

- Add support for LSP methods `SetTrace`, `TextDocumentDidSave`, and `WorkspaceDidChangeWatchedFiles`

### Fixed

- Improve LSP script handling by downloading before execution
- Correctly handle array expansion for LSP install arguments in install script

## [0.5.1] — 2026-05-16

### Fixed

- Update prerelease detection regex in release workflows

## [0.5.1-alpha.9-rc.10] — 2026-05-16

- No changes.

## [0.5.1-alpha.9-rc.9] — 2026-05-16

- No changes.

## [0.5.1-alpha.9-rc.8] — 2026-05-16

- No changes.

## [0.5.1-alpha.9-rc.7] — 2026-05-16

- No changes.

## [0.5.1-alpha.9-rc.6] — 2026-05-16

- No changes.

## [0.1.0] — 2026-04-29

Initial release.

### Added

**CLI**
- `dpg plan` — diff source files against the committed snapshot and print the minimal SQL migration; supports `--live` to diff against a live database instead
- `dpg apply` — lint, diff, prompt for approval, execute the SQL migration, and update the committed snapshot; supports `--allow-destructive` and `--yes`
- `dpg verify` — connect to a live database and report any drift from the committed snapshot
- `dpg dump` — introspect a live database and generate initial `.dpg` source files and an initial snapshot
- `dpg diff` — diff two `.dpg` source directories and print the SQL between them (no database required)
- `dpg portability` — report PostgreSQL-specific constructs in use and suggest standard SQL alternatives
- `--cluster` and `--database` flags on all commands for multi-cluster/multi-database projects
- Cluster-level objects (roles) planned, applied, and snapshotted independently from databases

**Compiler**
- Source file scanning, parsing via `pg_query_go`, and IR construction for all supported object types
- Schema context inference from directory layout
- Dependency-ordered compilation with topological sort and `DEFERRABLE` cycle handling for circular foreign keys

**Object support**
- Tables: columns (including `IDENTITY`, `GENERATED`, computed), inline single-column constraints, table-level constraints, indexes, RLS, comments, grants
- Views and materialized views
- Functions and procedures
- Types: `ENUM`, `DOMAIN`, composite
- Sequences (user-defined; identity-owned sequences are filtered)
- Roles
- Extensions
- Schemas

**Differ**
- `CREATE`, `ALTER`, and `DROP` generation for all supported object types
- Safety classification: every generated statement tagged `SAFE`, `CAUTION`, `DESTRUCTIVE`, or `MANUAL`
- Destructive operations blocked by default; require `--allow-destructive`
- Warning on `dpg apply` for new tables created without a primary key

**Snapshot**
- JSON snapshot format committed alongside source files
- `dpg dump` rebuilds the snapshot from compiled source to ensure the first `dpg plan` after a dump produces no diff

**Linter**
- Configurable static analysis: deprecated column detection, hardcoded password detection, missing column comments
- Lint diagnostics printed to stderr before any migration is emitted

**Emit**
- Transactional wrapper (`BEGIN` / `COMMIT`) for all safe operations
- Non-transactional post-commit block for `CREATE INDEX CONCURRENTLY` and similar
- Safety labels and source position annotations in rendered output
- ANSI colour support

**Portability analysis**
- Detection and reporting of PostgreSQL-specific constructs with standard SQL alternatives

**Project structure**
- `dpg.toml` discovery with cluster and database directory layout
- Secret resolution via `env:` and `link:` URI schemes
- Migration archiving to a configurable directory

[Unreleased]: https://github.com/dullkingsman/dpg/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/dullkingsman/dpg/releases/tag/v0.1.0
