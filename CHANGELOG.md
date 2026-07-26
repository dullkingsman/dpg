# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Introspect nine previously-unsupported object types (tablespaces, foreign data
  wrappers, foreign servers, user mappings, publications, event triggers,
  collations, casts, extended statistics), so `dump`/`verify`/`plan --live`
  cover them. Reconstructed DDL is canonicalised through pg_query to match the
  compiler's output. Extension-owned and system objects are filtered out.
- Introspect seven further object types — operators, operator families, operator
  classes (with their full `OPERATOR`/`FUNCTION` member list), text-search
  parsers, templates, dictionaries, and configurations — completing catalog
  coverage for the opaque tier. Previously `plan --live` proposed a spurious
  `CREATE` for each of these because introspection returned nothing. Operator
  class/family members are scoped by their internal `pg_depend` link to the
  class, so family-level members added later by `ALTER` are not mis-attributed;
  operator families auto-created for a class are skipped.
- `dump` now writes database-scoped schemaless objects to `<db>/objects.dpg`
  (previously misfiled under the cluster roles file) and can emit these object
  types as DPG declarations.
- Indexes now support `WITH (...)` storage parameters and `NULLS NOT DISTINCT`
  end to end: parsed in hand-written source, introspected from the live
  catalog (via `pg_get_indexdef`, so it works on any supported PG version
  without a version-gated catalog column), emitted in `CREATE INDEX` SQL, and
  rendered back by `dump`. `WITH` was previously parsed into the IR (`idx.With`)
  but silently dropped everywhere downstream; `NULLS NOT DISTINCT` wasn't
  represented at all.
- `INDEX name (...)` (Mode B, RFC §4.8's singular-keyword form) now parses for
  hand-written indexes. Previously the parser routed both `INDEX` and
  `INDICES` to the same brace-requiring block parser, so the singular form was
  a hard parse error (`expected '{', got '('`), not just silently unsupported.
  Mode A (`INDICES { ... }`) is unchanged and the two may be freely mixed, as
  the RFC requires.

### Fixed

- **`diffIndexes` now compares a same-named index's actual definition instead
  of only checking whether the name exists.** Previously an index present in
  both desired and snapshot was never compared at all — editing its method,
  uniqueness, columns, sort order/`NULLS` placement, `WHERE`, `INCLUDE`,
  `WITH`, or `NULLS NOT DISTINCT` while keeping the name was a silent no-op on
  `plan`/`apply`. `SnapIndex` gained `Include`/`With`/`NullsNotDistinct` fields
  (previously only `Name`/`Unique`/`Method`/`Columns`/`Where` were stored) and
  `Columns` now also carries sort-order/`NULLS` suffixes; any difference now
  emits a `DROP INDEX` + `CREATE INDEX` pair, the same statement already used
  when an index is removed outright. `translateIndexCols` (used to suppress a
  redundant `DROP INDEX` when `DROP COLUMN` already cascades to it) was
  updated to strip these new suffixes before matching column names — the
  `Columns` format change would otherwise have broken that cascade check for
  any sorted index.
  Fixing this surfaced two additional spurious-drift bugs that only appear
  once an index's definition is actually compared against a live catalog:
  `pg_get_indexdef` always quotes `WITH` storage-parameter values (e.g.
  `fillfactor='70'`) while hand-written source may not, and it adds an
  explicit `::typename` cast to string literals inside expression index
  columns (e.g. `to_tsvector('english'::regconfig, e)`) that source doesn't
  have. Both are now normalized before comparison (quote-stripping in
  `snapshot.ToSnapIndex`; the existing `stripStringLiteralCasts` — already
  used for column defaults — applied to `WHERE` and expression columns during
  introspection), so an unchanged index no longer shows drift against a real
  database.
- `GRANT name (...)`/`REVOCATION name (...)` (Mode B, RFC §4.8's singular-
  keyword form) now parse in all three places they're accepted — table level,
  column level, and inside a `DEFAULT PRIVILEGES { }` block. Previously the
  parser routed `GRANT`/`GRANTS` (and `REVOCATION`/`REVOCATIONS`) to the same
  brace-requiring block parser at each of those three sites, so the singular
  form was a hard parse error, identical to the `INDEX`/`INDICES` conflation
  fixed earlier in this release. Mode A (`GRANTS { ... }`/`REVOCATIONS { ... }`)
  is unchanged and the two may be freely mixed, as the RFC requires.
- Explicit `REVOCATION`/`REVOCATIONS` (RFC §11.3) on a table, column, or view
  now actually emits `REVOKE`, in either syntax mode. Found while live-testing
  the fix above: it was parsed into the AST/IR correctly but
  `internal/diff/differ.go` never read `ir.Table.Revocations`,
  `ir.Column.Revocations`, or `ir.View.Revocations` anywhere — only
  `DEFAULT PRIVILEGES` revocations were diffed/emitted, so declaring one on a
  table/column/view was a complete silent no-op. Mirrors the existing
  `DEFAULT PRIVILEGES` revocation semantics: a revocation is tracked in the
  snapshot once applied, so a later unchanged run doesn't re-issue `REVOKE`
  (kept for tidy migration output, not because re-running it would error —
  `REVOKE` on an already-absent privilege is a harmless no-op in PostgreSQL),
  and removing a `REVOCATION` declaration re-`GRANT`s the privilege to
  restore it. `CASCADE` is supported at the table/view level and column
  level.
- `POLICY name ...`/`TRIGGER name ...`/`PARTITION name ...` (Mode B, RFC
  §4.8's singular-keyword form) now parse. Unlike `INDEX`/`GRANT`/
  `REVOCATION`, these three weren't even in the block dispatch switch, so the
  singular keyword was "unknown block directive", not just a brace mismatch.
  `POLICY`/`TRIGGER` reuse the existing per-entry parser their plural block
  already called internally; `PARTITION` needed one extracted
  (`parseOnePartitionBound`), since `PartitionDef` wraps a list unlike the
  other four collection types. Fixing `PARTITION` surfaced two further bugs,
  both fixed alongside it:
  - **`CREATE TABLE ... PARTITION OF ... FOR VALUES ...` had a duplicated
    `FOR VALUES`, a syntax error.** Both the parser (raw text captured after
    the partition name) and introspection (`pg_get_expr` on `relpartbound`)
    already include the `FOR VALUES ...` (or `DEFAULT`) clause in a
    partition's stored bounds text, but the SQL-emission code prepended a
    second, hardcoded `FOR VALUES` on top. Pre-existing, present for both
    Mode A and Mode B, in both the initial-create and update-diff paths —
    just never live-tested against a real database before now.
  - **A `PARTITIONS { }` block silently discarded any partitions a preceding
    Mode-B `PARTITION` entry had already added**, because `PartitionDef`
    wraps a list and the `PARTITIONS` dispatch case assigned outright
    (`ast.Partitions = ...`) instead of merging into an existing value. Only
    manifests when `PARTITION` precedes `PARTITIONS` on the same table; the
    reverse order happened to work by accident.
  Known limitation found live-testing this fix, **not fixed here**:
  introspecting a partitioned parent table and re-diffing against source
  reports spurious drift — columns show as missing (`ADD COLUMN` proposed for
  columns that already exist) and triggers show as changed (`DROP`/`CREATE`
  proposed for unchanged triggers). Pre-existing, unrelated to Mode A vs B —
  no integration test exercised a partitioned table with columns or triggers
  before now. Root cause not investigated; likely in how partitioned tables'
  columns/triggers are matched during introspection or live-snapshot
  filtering. `dpg apply` itself is unaffected (the generated SQL is correct
  and applies cleanly); only `verify`/`plan --live` on an already-applied
  partitioned table would show this.
- `CONSTRAINTS { }` (Mode A, RFC §4.8's plural-block form) now parses.
  Previously only the singular `CONSTRAINT name ...;` form worked at all —
  unlike the other 7 collection types in RFC §4.8's Dual Definition Modes
  table, `CONSTRAINTS` had no parser whatsoever, not even a buggy one. The
  RFC lists `CONSTRAINTS`/`CONSTRAINT` as a pair but never otherwise
  specifies or exemplifies the plural form anywhere in the document; judged
  a gap worth closing for consistency with the other 7 rather than a
  deliberate omission, since nothing calls out constraints as an intentional
  exception. Reuses the existing `parseConstraint` unchanged — each entry
  in the block gets its own position captured before calling it. May be
  freely mixed with the singular form on the same table.
- `dump` output now recompiles **and re-applies** for real-world tables:
  identifiers that are reserved keywords or otherwise non-bare (table/column/view/
  sequence/role/index/constraint names) are quoted; indexes render as the accepted
  `INDICES { … }` block (was the unparseable `INDEX name (…)` form) with bare
  column names (the differ quotes them); `COMMENT` values render as SQL string
  literals (was Go `%q` double-quotes); and enums render as `ENUM <name> AS ENUM
  (…)` (was a missing `AS ENUM`).
- `CREATE INDEX` now emits the partial-index `WHERE` predicate and `INCLUDE`
  columns, which were silently dropped — a declared partial/covering index was
  created as a plain index.
- Index column sort order (`ASC`/`DESC`) and `NULLS FIRST`/`LAST` now round-trip:
  the block parser previously stored `col DESC NULLS LAST` as one literal column
  name, so a dumped sorted index failed to apply. Covering (`INCLUDE`) columns are
  now introspected and dumped, so a dumped covering index stays covering.
- The dependency resolver now orders a custom type before a table that uses it
  even when the column type is written unqualified (as introspected columns are),
  so a dumped project whose table precedes its enum applies without
  "type … does not exist".
- `dump` now emits `SCHEMA` and `EXTENSION` declarations, so non-public schemas
  and installed extensions are no longer dropped by `plan --live`.
- `dump` now renders the seven deferred-tier types it learned to introspect
  (operators, operator classes/families, text-search parsers/templates/
  dictionaries/configurations); previously it silently omitted them, so a `dump`
  of a database using any of them was immediately followed by a destructive
  `DROP` for each on the next `plan --live`.
- An operator class and the operator family PostgreSQL auto-creates for it share
  the same qualified name, which collided in the flat, name-keyed snapshot and
  diff maps and silently dropped one of the two objects. Operator class/family
  identity now includes the access method and a class/family discriminator, so
  the two can never overwrite each other (this changes their snapshot keys — see
  upgrade notes).
- Operator-family introspection no longer emits the same-named family that a
  `CREATE OPERATOR CLASS` auto-creates as a standalone object (which would emit a
  redundant `CREATE OPERATOR FAMILY` that conflicts with the auto-creation on
  re-apply), and a class's reconstructed body no longer names that implicit
  family in a `FAMILY` clause. The previous discriminator relied on a `pg_depend`
  deptype that is identical for auto-created and explicitly-attached families; the
  reliable signal is the family's matching name, mirroring PostgreSQL's own rule.
  Known limitation: an *explicit* operator family that happens to share a class's
  name (rare, but legal) is treated as the implicit auto-created one and omitted
  from the dump; operator-family members added via `ALTER OPERATOR FAMILY … ADD`
  are not modeled in either case.
- Introspection now excludes extension-owned functions, procedures, aggregates,
  types, tables, views, and sequences (e.g. `hstore`'s ~60 functions), so they
  are not dumped or proposed for dropping.
- `DROP OPERATOR CLASS`/`DROP OPERATOR FAMILY` now use the object's real index
  access method instead of a hardcoded `USING btree` (legacy snapshots without
  the recorded method fall back to `btree`).
- `DROP USER MAPPING` for a `FOR PUBLIC` mapping no longer emits a zero-length
  identifier.
- `dump` no longer emits `COLLATION`/`STATISTICS` declarations with an empty
  body (which aborted `plan`/`apply` with "body not captured").
- A source-side body edit to an opaque object (tablespace, FDW, foreign server,
  user mapping, publication, subscription, event trigger, collation, operator
  class/family, cast, extended statistics, or any text-search object) now emits
  a real `DROP ... IF EXISTS` followed by the new `CREATE`, reusing the exact
  statement `dropObject` already emits when the object is removed outright.
  Previously `plan`/`apply` only emitted a `-- WARNING: ... manual DROP +
  recreate required` SQL comment — a body edit was silently a no-op unless
  someone read migration output and ran the DROP/CREATE by hand. Operators are
  excluded from this fix and keep the old warning. Known limitation: `DROP
  OPERATOR` requires a mandatory `(lefttype, righttype)` clause that
  `ir.Operator` does not capture (a pre-existing gap, not introduced by this
  fix — it also affects an operator's removal from source, not just its body
  edits); operator names can also be overloaded across operand types, which the
  flat `schema.name` snapshot key cannot disambiguate. Emitting invalid or
  misdirected DDL would be worse than the manual warning, so operator body
  edits keep the placeholder until operand types are modelled end-to-end.
- Reconstructed opaque-object bodies no longer report spurious destructive
  "body changed" drift on `verify`/`plan --live` when hand-written source uses an
  equivalent-but-differently-spelled form; offline `plan`/`apply` still detect
  genuine body edits.

### Changed

- Cluster/database object routing is unified across `dump`, `plan`, and `verify`:
  only roles and tablespaces are cluster-scoped.

### Upgrade notes

- **Drift on first run against an existing database.** Because these object types
  are now introspected, a live-but-undeclared publication, FDW, event trigger,
  collation, etc. becomes a drop candidate on `plan --live` (standard declarative
  semantics — declare the object in source to keep it). This is a behavior change
  only for upgrade-in-place; freshly dumped projects are unaffected.
- **Body-change detection self-heals on next apply.** Snapshots written before
  this release have no stored body hash for these types, so offline detection of
  a body edit stays dormant until the next `apply` re-snapshots from source.
- **Operator class/family snapshot keys changed.** Their snapshot keys now embed
  the access method and a class/family marker (e.g. `public.x USING btree` vs
  `public.x USING btree FAMILY`). Any operator class or family in a pre-existing
  snapshot re-keys on the next run: the old key reads as a removed object and the
  new key as a new one, so `plan` emits a **DESTRUCTIVE** `DROP` + `CREATE` pair
  (requires `--allow-destructive` to apply). For a class or family with no
  dependents this applies cleanly and the next `plan` is a genuine no-op. If
  anything depends on the object — e.g. an index built with a custom operator
  class, the usual reason to have one — the `DROP` **fails outright** with a
  Postgres "other objects depend on it" error (loud failure, not silent
  corruption); drop/rebuild the dependents around the apply, or avoid the churn
  entirely by hand-editing the snapshot key to the new format before running.
- **Index content-comparison self-heals on next apply.** `SnapIndex` gained
  `Include`/`With`/`NullsNotDistinct` fields and `Columns` now carries sort-
  order/`NULLS` suffixes. Snapshots written before this release don't have
  this data for indexes that already use these properties (covering columns,
  storage parameters, `NULLS NOT DISTINCT`, or non-default sort order), so the
  first `plan`/`apply` after upgrading recreates those indexes once (a
  `CAUTION`-level `DROP INDEX` + `CREATE INDEX`, not `DESTRUCTIVE` — same as a
  plain index change) to bring the snapshot up to date; every run after that
  is a genuine no-op.
- **Table/column/view `REVOCATION` now actually runs.** Previously a silent
  no-op, so an existing `REVOCATION`/`REVOCATIONS` declaration may not
  reflect current reality — the target role might already lack the privilege,
  or might still have it because DPG never revoked it. On the first
  `plan`/`apply` after upgrading, `REVOKE` runs for every declared revocation
  not yet recorded in the snapshot. This is safe to run even if the privilege
  was never actually granted — `REVOKE` on an already-absent privilege
  succeeds as a no-op in PostgreSQL, it does not error. The one thing that
  does error is revoking from a role that no longer exists; if you have a
  stale `REVOCATION` naming a dropped role, fix or remove it before applying.

## [idea-v0.5.2-alpha.13] — 2026-05-22

### Added

- Add support for macro spread references, resolution logic, and related tests across LSP and IDEA plugin
- Enforce blank line between schema attributes and first nested object in formatter and add related test
- Add support for DPG code formatting in IDEA plugin, including indentation, blank lines, and spacing adjustments
- Add support for macro declarations, rendering, and schema-level nesting in formatter

### Changed

- Simplify schema node comment collection logic in parser

## [0.5.5-alpha.1] — 2026-05-21

### Added

- Enforce blank line between schema attributes and first nested object in formatter and add related test
- Add support for DPG code formatting in IDEA plugin, including indentation, blank lines, and spacing adjustments
- Add support for macro declarations, rendering, and schema-level nesting in formatter
- Add support for macro references, completions, and spread expressions in IDEA plugin

### Changed

- Simplify schema node comment collection logic in parser
- Migrated idea plugin from LSP-based implementation to plugin-native parsing, highlighting, and tooling

## [lsp-v0.5.5-alpha.1] — 2026-05-21

- No changes.

## [lsp-v0.5.2-alpha.11] — 2026-05-21

### Added

- Enforce blank line between schema attributes and first nested object in formatter and add related test
- Add support for DPG code formatting in IDEA plugin, including indentation, blank lines, and spacing adjustments
- Add support for macro declarations, rendering, and schema-level nesting in formatter
- Add support for macro references, completions, and spread expressions in IDEA plugin

### Changed

- Simplify schema node comment collection logic in parser
- Migrated idea plugin from LSP-based implementation to plugin-native parsing, highlighting, and tooling

## [idea-v0.5.2-alpha.12] — 2026-05-20

### Added

- Add support for macro references, completions, and spread expressions in IDEA plugin

## [idea-v0.5.2-alpha.11] — 2026-05-20

### Changed

- Migrated idea plugin from LSP-based implementation to plugin-native parsing, highlighting, and tooling

## [0.5.4] — 2026-05-17

- No changes.

## [0.5.3] — 2026-05-17

- No changes.

## [0.5.2-alpha.21] — 2026-05-17

- No changes.

## [0.5.2-alpha.20] — 2026-05-17

- No changes.

## [0.5.2-alpha.18] — 2026-05-17

- No changes.

## [0.5.2-alpha.17] — 2026-05-17

- No changes.

## [0.5.2-alpha.16] — 2026-05-17

- No changes.

## [0.5.2-alpha.15] — 2026-05-17

- No changes.

## [0.5.2-alpha.14] — 2026-05-17

- No changes.

## [0.5.2-alpha.13] — 2026-05-17

- No changes.

## [0.5.2-alpha.12] — 2026-05-17

- No changes.

## [0.5.2-alpha.11] — 2026-05-17

- No changes.

## [idea-v0.5.2-alpha.10] — 2026-05-17

- No changes.

## [vscode-v0.5.2-alpha.10] — 2026-05-17

- No changes.

## [nvim-v0.5.2-alpha.10] — 2026-05-17

### Added

- Introduce Name Maps with tool-specific naming conventions
- Enhance syntax highlighting, macros, and object parsing
- Support `PREFERRED JSON FORMAT` directive for virtual types
- Add support for structured virtual types with JSONB resolution
- Add support for nested macro expansion with circular reference detection
- Enable project-scoped macro sharing across `.dpg` files
- Add support for `DEFAULT PRIVILEGES` in snapshots and diffs
- Add support for LSP methods `SetTrace`, `TextDocumentDidSave`, and `WorkspaceDidChangeWatchedFiles`
- Add shortcodes, workflows, and versioning improvements for docs and LSP
- Add install-lsp script and integrate versioning enhancements for dpg-lsp
- Enable local-first Gradle distribution for faster offline builds
- Add test data and refactor LSP smoke testing root logic
- Add installer script and documentation for streamlined binary installation
- Add workflow support and bindings for editor integrations
- Add comprehensive DPG support to editors, tests, and tooling
- Introduce RFC DPG-1 specification for Declarative PG
- Add RFC for Declarative PG (DPG) specification
- Add comprehensive documentation for all schema objects and advanced features
- Add RenderAll for multi-migration output and refactor migration planning
- Add `VIRTUAL TYPE` and macro preprocessing support
- Inline single-column constraints and suppress redundant NOT NULL for PostgreSQL
- Normalize NOT NULL handling for PostgreSQL PRIMARY KEY columns
- Add macro preprocessing and support for virtual type declaration
- Improve introspection and schema handling for PostgreSQL
- Add minimal home page layout for website
- Add new commands and editor integration for improved `.dpg` workflow
- Add plugin example for custom linter registration and chaining
- Enhance aggregate and enum diffing with grants and migration support
- Enhance column and partition handling in snapshot diffing
- Enhance snapshot diffing with grant tracking and structural updates
- Introduce base infrastructure for DPG compiler, formatter, and public API

### Fixed

- Improve LSP script handling by downloading before execution
- Correctly handle array expansion for LSP install arguments in install script
- Update prerelease detection regex in release workflows
- Return error for missing body in `createOpaque` to prevent silent no-op

### Changed

- Switch COMMENT and DEPRECATED directives to single quotes for consistency
- Update snapshot schema spec for enhanced readability and structure
- Update `introspect_integration_test` to use `introspect.New()` instead of `New()` for improved clarity and consistency
- Rename `website` to `site` for consistent naming convention
- Move core logic and tests into `core` directory for better modularity
- Standardize directory structure by renaming `editors` to `plugins` and `editors/lsp` to `lang/lsp`
- Simplify argument signature generation with ArgsKey and dropObject cleanup
- prefer user-local Hugo binary in Makefile and setup script
- Add editor integration guide and extend `dpg fmt` documentation
- Improve `setup.sh` script and update website configs
- add development guide and setup script
- add CLI documentation generation and embedding in release builds
- Add comprehensive CLI and documentation overhaul
- Update documentation for commands, directory structure, and test workflows
- Filled in gaps and added test containers for integrations tests
- Enforce column existence and reject unknown columns in TABLE builds per RFC §7.2

## [lsp-v0.5.2-alpha.10] — 2026-05-17

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

## [grammar-v0.5.2-alpha.10] — 2026-05-17

### Added

- Introduce Name Maps with tool-specific naming conventions
- Enhance syntax highlighting, macros, and object parsing
- Support `PREFERRED JSON FORMAT` directive for virtual types
- Add support for structured virtual types with JSONB resolution
- Add support for nested macro expansion with circular reference detection
- Enable project-scoped macro sharing across `.dpg` files
- Add support for `DEFAULT PRIVILEGES` in snapshots and diffs
- Add support for LSP methods `SetTrace`, `TextDocumentDidSave`, and `WorkspaceDidChangeWatchedFiles`
- Add shortcodes, workflows, and versioning improvements for docs and LSP
- Add install-lsp script and integrate versioning enhancements for dpg-lsp
- Enable local-first Gradle distribution for faster offline builds
- Add test data and refactor LSP smoke testing root logic
- Add installer script and documentation for streamlined binary installation
- Add workflow support and bindings for editor integrations
- Add comprehensive DPG support to editors, tests, and tooling
- Introduce RFC DPG-1 specification for Declarative PG
- Add RFC for Declarative PG (DPG) specification
- Add comprehensive documentation for all schema objects and advanced features
- Add RenderAll for multi-migration output and refactor migration planning
- Add `VIRTUAL TYPE` and macro preprocessing support
- Inline single-column constraints and suppress redundant NOT NULL for PostgreSQL
- Normalize NOT NULL handling for PostgreSQL PRIMARY KEY columns
- Add macro preprocessing and support for virtual type declaration
- Improve introspection and schema handling for PostgreSQL
- Add minimal home page layout for website
- Add new commands and editor integration for improved `.dpg` workflow
- Add plugin example for custom linter registration and chaining
- Enhance aggregate and enum diffing with grants and migration support
- Enhance column and partition handling in snapshot diffing
- Enhance snapshot diffing with grant tracking and structural updates
- Introduce base infrastructure for DPG compiler, formatter, and public API

### Fixed

- Improve LSP script handling by downloading before execution
- Correctly handle array expansion for LSP install arguments in install script
- Update prerelease detection regex in release workflows
- Return error for missing body in `createOpaque` to prevent silent no-op

### Changed

- Switch COMMENT and DEPRECATED directives to single quotes for consistency
- Update snapshot schema spec for enhanced readability and structure
- Update `introspect_integration_test` to use `introspect.New()` instead of `New()` for improved clarity and consistency
- Rename `website` to `site` for consistent naming convention
- Move core logic and tests into `core` directory for better modularity
- Standardize directory structure by renaming `editors` to `plugins` and `editors/lsp` to `lang/lsp`
- Simplify argument signature generation with ArgsKey and dropObject cleanup
- prefer user-local Hugo binary in Makefile and setup script
- Add editor integration guide and extend `dpg fmt` documentation
- Improve `setup.sh` script and update website configs
- add development guide and setup script
- add CLI documentation generation and embedding in release builds
- Add comprehensive CLI and documentation overhaul
- Update documentation for commands, directory structure, and test workflows
- Filled in gaps and added test containers for integrations tests
- Enforce column existence and reject unknown columns in TABLE builds per RFC §7.2

## [0.5.2-alpha.10] — 2026-05-17

- No changes.

## [0.5.2-alpha.9] — 2026-05-17

- No changes.

## [0.5.2-alpha.8] — 2026-05-17

- No changes.

## [0.5.2-alpha.7] — 2026-05-17

- No changes.

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
