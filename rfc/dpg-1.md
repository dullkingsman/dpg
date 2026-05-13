```
DPG Working Group                                          D. Tsegaw
Request for Comments: 1                                    Independent
Category: Standards Track                                  May 2026
ISSN: (N/A — project-internal specification)


         Declarative PG (DPG): A Declarative State-Based Superset
                        of PostgreSQL DDL

Abstract

   This document specifies Declarative PG (DPG), a declarative,
   state-based superset of PostgreSQL SQL that compiles to idiomatic
   PostgreSQL DDL.  DPG source files describe the desired state of a
   PostgreSQL database; the DPG compiler computes the minimal, safe,
   ordered set of DDL statements required to transition the current
   state to the desired state.  This specification defines the DPG
   source language, the compilation pipeline, the snapshot interchange
   format, the migration output format, and all associated tooling
   behaviour.

Status of This Memo

   This document specifies a Standards Track protocol for the DPG
   project ecosystem.  Distribution of this memo is unlimited.

   This document is the authoritative specification for DPG version
   0.8.1 and supersedes all prior informal design documents including
   rfc/v0.8.0.md.  The Go implementation at github.com/dullkingsman/dpg
   MUST conform to every normative statement in this document.

Copyright Notice

   Copyright (c) 2026 Daniel Tsegaw.  All rights reserved.

   Redistribution and use of this specification, with or without
   modification, is permitted provided that the above copyright notice
   and this permission notice appear in all copies.
```

---

## Table of Contents

```
1.  Introduction ....................................................  1
    1.1.  Purpose and Scope ........................................  1
    1.2.  Problem Statement ........................................  2
    1.3.  Prior Art ................................................  2
    1.4.  Core Design Tenets .......................................  3
    1.5.  Terminology ..............................................  4
2.  Conventions Used in This Document ..............................  5
    2.1.  Requirements Notation ....................................  5
    2.2.  Syntax Notation ..........................................  5
    2.3.  Examples .................................................  5
3.  Project Structure and Configuration ............................  6
    3.1.  Directory Layout .........................................  6
    3.2.  Root dpg.toml ............................................  7
    3.3.  Cluster dpg.toml .........................................  8
    3.4.  Database dpg.toml ........................................  9
    3.5.  Cluster-Level Objects Directory ..........................  9
    3.6.  Discovery Algorithm ......................................  9
    3.7.  Block Merge Conflict Resolution .......................... 10
4.  Language Fundamentals .......................................... 11
    4.1.  Source File Format ....................................... 11
    4.2.  The Two-Part Syntax Model ................................ 11
    4.3.  The No-Verb Mandate ...................................... 12
    4.4.  Structural Scoping ....................................... 13
    4.5.  Statement Terminators .................................... 13
    4.6.  Dollar-Quoted String Parsing ............................. 14
    4.7.  Macro Preprocessor ....................................... 15
    4.8.  Dual Definition Modes .................................... 17
    4.9.  Block Merging ............................................ 17
    4.10. Identifiers .............................................. 18
5.  Type System .................................................... 19
    5.1.  ENUM Types ............................................... 19
    5.2.  Composite Types .......................................... 21
    5.3.  Range Types .............................................. 22
    5.4.  Domain Types ............................................. 22
    5.5.  Base (Shell) Types ....................................... 23
    5.6.  Virtual Types ............................................ 23
6.  Schema and Namespace Objects ................................... 25
    6.1.  SCHEMA ................................................... 25
    6.2.  EXTENSION ................................................ 26
7.  Tables ......................................................... 27
    7.1.  Table Declaration Syntax ................................. 27
    7.2.  Column Definitions ....................................... 28
    7.3.  Constraints .............................................. 30
    7.4.  The COLUMN Reference Block ............................... 33
    7.5.  Column-Level Grants ...................................... 35
    7.6.  Column Renaming .......................................... 35
    7.7.  Indexes .................................................. 37
    7.8.  Row Level Security ....................................... 39
    7.9.  Triggers ................................................. 40
    7.10. Table-Level Grants and Revocations ....................... 42
    7.11. Table Lifecycle Directives ............................... 42
    7.12. Unlogged and Foreign Tables .............................. 43
    7.13. Partitioned Tables ....................................... 44
8.  Views .......................................................... 47
    8.1.  Regular Views ............................................ 47
    8.2.  Materialized Views ....................................... 49
    8.3.  Recursive Views .......................................... 50
9.  Functions and Procedures ....................................... 51
    9.1.  Function Declaration Syntax .............................. 51
    9.2.  Function Attributes ...................................... 52
    9.3.  Procedures ............................................... 54
    9.4.  Aggregate Functions ...................................... 54
    9.5.  Function Body Diffing Semantics .......................... 55
10. Sequences ...................................................... 56
11. Access Control ................................................. 57
    11.1. Roles .................................................... 57
    11.2. Grants — the Additive Model .............................. 58
    11.3. Revocations .............................................. 59
    11.4. Default Privileges ....................................... 60
12. Full-Text Search Objects ....................................... 61
    12.1. Text Search Configurations ............................... 61
    12.2. Text Search Dictionaries ................................. 62
    12.3. Text Search Parsers ...................................... 62
    12.4. Text Search Templates .................................... 63
13. Logical Replication ............................................ 64
    13.1. Publications ............................................. 64
    13.2. Subscriptions ............................................ 65
14. Advanced PostgreSQL Objects .................................... 66
    14.1. Event Triggers ........................................... 66
    14.2. Collations ............................................... 66
    14.3. Operators ................................................ 67
    14.4. Operator Classes and Families ............................ 68
    14.5. Casts .................................................... 69
    14.6. Extended Statistics Objects .............................. 69
    14.7. Tablespaces .............................................. 70
    14.8. Foreign Data Wrappers .................................... 70
    14.9. Foreign Servers .......................................... 71
    14.10.User Mappings ............................................ 71
15. Compilation Pipeline ........................................... 72
    15.1. Phases Overview .......................................... 72
    15.2. Phase 1 — File Discovery ................................. 73
    15.3. Phase 2 — Macro Preprocessing ............................ 73
    15.4. Phase 3 — Tokenization ................................... 74
    15.5. Phase 4 — PG SQL Parsing ................................. 75
    15.6. Phase 5 — Block Parsing .................................. 76
    15.7. Phase 6 — IR Construction ................................ 76
    15.8. Phase 7 — Merging ........................................ 77
    15.9. Phase 8 — Dependency Resolution .......................... 78
    15.10.Phase 9 — Differencing ................................... 79
    15.11.Phase 10 — Emission ...................................... 80
16. Snapshot Format ................................................ 81
    16.1. Purpose and Placement .................................... 81
    16.2. Top-Level Fields ......................................... 81
    16.3. Per-Object Snapshot Schema ............................... 82
    16.4. Versioning ............................................... 88
17. Migration Output Format ........................................ 89
    17.1. Output Structure ......................................... 89
    17.2. Safety Classification .................................... 90
    17.3. Transactional vs Non-Transactional Steps ................. 91
    17.4. Idempotency Requirement .................................. 92
18. CLI Commands ................................................... 93
    18.1. dpg plan ................................................. 93
    18.2. dpg apply ................................................ 94
    18.3. dpg verify ............................................... 95
    18.4. dpg dump ................................................. 96
    18.5. dpg diff ................................................. 96
    18.6. dpg validate ............................................. 97
    18.7. dpg fmt .................................................. 97
    18.8. dpg portability .......................................... 98
    18.9. dpg init ................................................. 98
    18.10.dpg completion ........................................... 98
19. The Linter ..................................................... 99
    19.1. Built-in Rules ........................................... 99
    19.2. Configuration ........................................... 100
20. Introspection Engine .......................................... 101
    20.1. Catalog Tables Read ..................................... 101
    20.2. Drift Detection ......................................... 102
21. Per-Object Diff Algorithms .................................... 103
22. Dependency Ordering ........................................... 110
    22.1. Topological Sort ........................................ 110
    22.2. Circular Dependency Resolution .......................... 111
23. Deferred Features ............................................. 112
24. Security Considerations ....................................... 113
25. Feature Coverage Matrix ....................................... 115
Appendix A.  ABNF Grammar Summary ................................ 120
Appendix B.  Complete Example Project ............................ 126
Appendix C.  Error Code Reference ................................ 132
Appendix D.  Corrections and Additions to Earlier Sections ....... 138
    D.1.  Snapshot Format Corrections ............................. 138
    D.2.  CLI Command Corrections ................................. 142
    D.3.  Linter Rule ID Corrections .............................. 145
    D.4.  Pipeline Registry Key Constants ......................... 145
    D.5.  SecretResolver Protocol Specification ................... 146
    D.6.  Source Revision Detection ............................... 147
    D.7.  Additional CLI Error Codes .............................. 147
    D.8.  Root dpg.toml Missing Sections ......................... 148
    D.9.  CLI Command Corrections ................................ 149
Appendix E.  Revision History ..................................... 151
Normative References .............................................. 152
Informative References ............................................ 153
Author's Address .................................................. 154
```

---

## 1. Introduction

### 1.1. Purpose and Scope

   This document specifies the Declarative PG (DPG) language, its
   compilation model, and all associated tooling.  DPG is a
   declarative, state-based superset of PostgreSQL SQL.  A DPG source
   tree describes the desired state of one or more PostgreSQL databases.
   The DPG compiler ingests that description, compares it against a
   committed schema snapshot, and emits the minimal ordered set of
   PostgreSQL DDL statements required to transition the database to the
   desired state.

   The scope of this specification covers:

   a)  The DPG source language syntax, including all object declaration
       forms, the two-part syntax model, the macro preprocessor, and
       all lifecycle directives.

   b)  Every category of PostgreSQL object that DPG can manage,
       including the precise DDL each declaration produces.

   c)  The full compilation pipeline from file discovery through DDL
       emission, including intermediate representations.

   d)  The snapshot interchange format and its versioning contract.

   e)  The migration output format, safety classification system, and
       idempotency guarantees.

   f)  All CLI commands, their flags, and their observable behaviour.

   g)  The static analysis linter and its built-in rules.

   h)  The live catalog introspection engine and drift detection.

   This specification does NOT cover:

   -   The internal data structures of the Go reference implementation
       beyond what is required to define observable behaviour.
   -   PostgreSQL runtime behaviour (query planning, execution, etc.).
   -   Data manipulation language (SELECT, INSERT, UPDATE, DELETE).
   -   Inline data seeding (deferred; see Section 23).

### 1.2. Problem Statement

   PostgreSQL DDL is fundamentally imperative: to change a database one
   issues commands (`CREATE TABLE`, `ALTER TABLE ADD COLUMN`,
   `DROP INDEX`).  SQL files in version control therefore describe
   *actions taken at a point in time*, not the *current intended state*
   of the schema.  This creates four well-known failure modes:

   (1) **Schema drift.**  A production database that has been patched,
       hotfixed, or manually altered over time no longer matches its
       migration history.  There is no reliable way to tell whether a
       given migration file has been applied.

   (2) **No single source of truth.**  To understand the current schema
       a developer must mentally replay every migration, in order, from
       the beginning.  This is error-prone and does not scale.

   (3) **Idempotency is illusory.**  Running a migration file twice
       fails or corrupts state.  Most teams work around this with
       `IF NOT EXISTS` guards that must be written manually.

   (4) **Redundant context.**  PostgreSQL forces re-statement of context
       that is already structurally known.  `ALTER TABLE public.users
       ADD CONSTRAINT ...` repeats schema and table name in every
       alteration even when the context is fixed.

   DPG resolves all four problems by inverting the model: the developer
   writes a *description of the desired state*; the compiler produces
   the imperative commands.

### 1.3. Prior Art

   **Atlas (ariga.io)** uses HCL as its schema description language.
   HCL is a foreign DSL to a PostgreSQL developer — a new vocabulary
   must be learned to describe objects already expressible in SQL.

   **Prisma Schema** invents parallel concepts for PostgreSQL objects
   (`@id` instead of `PRIMARY KEY`).  PostgreSQL-specific features not
   modelled by Prisma are inaccessible.

   **Flyway / Liquibase / Sqitch** are migration-based, not
   declarative.  They manage the history of imperative changes rather
   than the desired state.

   **The DPG position:**  PostgreSQL SQL is already a nearly complete
   declarative schema language.  The only missing pieces are structural
   scaffolding to remove redundancy and a diff engine to translate state
   changes into safe migrations.  DPG adds exactly those two things.

### 1.4. Core Design Tenets

   **Tenet 1 — Full PostgreSQL feature parity.**
   DPG MUST be capable of expressing anything that raw PostgreSQL DDL
   can express.  A PostgreSQL feature that cannot be declared in DPG
   is a defect in DPG, not an out-of-scope request.

   **Tenet 2 — Prefer PG syntax exactly.**
   When PostgreSQL already has a declarative way to express something,
   DPG uses it verbatim.  DPG removes imperative verbs and adds
   structural scoping but does not invent new keywords for concepts
   PostgreSQL already names well.

   **Tenet 3 — Standard SQL / PG-extension boundary is tracked
   internally.**
   The compiler knows which constructs are ISO/IEC 9075 Standard SQL
   and which are PostgreSQL-specific.  Users never annotate portability.
   The compiler surfaces this via the `dpg portability` command.

   **Tenet 4 — Offline-first diffing.**
   DPG MUST NOT require a live database connection to generate a
   migration.  The primary workflow compares `.dpg` source files against
   a committed schema snapshot.  Live catalog introspection is available
   for verification and bootstrap but is never required for day-to-day
   operation.

   **Tenet 5 — The `{ }` block holds only what PG SQL cannot.**
   The native PostgreSQL DDL definition of an object — its column list,
   its options, its clauses, its dollar-quoted body — MUST be written
   exactly as PostgreSQL SQL dictates.  The trailing `{ }` block exists
   exclusively for things PostgreSQL SQL expresses as separate DDL
   statements (indexes, grants, policies, comments, per-column storage
   attributes) and for DPG lifecycle directives (`RENAMED FROM`,
   `PROTECTED`, `DEPRECATED`).  Nothing that has a natural place in
   PostgreSQL SQL's own syntax SHALL be moved into the `{ }` block.

### 1.5. Terminology

   The following terms carry precise meanings throughout this document:

   **DPG source file** — A UTF-8 text file with the `.dpg` extension
   containing one or more DPG object declarations.

   **Part 1** — The native PostgreSQL SQL portion of a DPG declaration,
   written with the leading imperative verb removed.

   **Part 2** — The optional trailing `{ }` block of a DPG declaration,
   holding sub-objects and lifecycle directives.

   **Object** — A named, independently manageable PostgreSQL schema
   entity (table, view, function, type, role, etc.).

   **Snapshot** — A committed JSON file representing the compiler's
   normalised view of the database state after the most recent
   successful `dpg apply`.

   **IR** (Internal Representation) — The fully-qualified, typed
   in-memory form of a DPG object, produced by the IR Builder phase.

   **DiffOp** — A single DDL statement produced by the Differ,
   annotated with safety class and source position.

   **Migration** — The complete ordered output of the Emitter: a set of
   transactional DiffOps and a set of non-transactional DiffOps,
   together with a header block.

   **Safety class** — One of `SAFE`, `CAUTION`, `DESTRUCTIVE`, or
   `MANUAL`.  Defined in Section 17.2.

   **Cluster** — A single running PostgreSQL instance, hosting one or
   more databases.  Maps to one cluster directory in the project tree.

   **Database** — A single named PostgreSQL database within a cluster.
   Maps to one database directory in the project tree.

---

## 2. Conventions Used in This Document

### 2.1. Requirements Notation

   The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
   "SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
   "OPTIONAL" in this document are to be interpreted as described in
   BCP 14 [RFC2119] [RFC8174] when, and only when, they appear in all
   capitals, as shown here.

### 2.2. Syntax Notation

   ABNF grammar rules are specified using the notation defined in
   [RFC5234], Augmented BNF for Syntax Specifications.  The following
   core rules from [RFC5234] Appendix B are used without redefinition:
   `ALPHA`, `DIGIT`, `SP`, `HTAB`, `CRLF`, `LF`, `DQUOTE`.

   Where existing PostgreSQL DDL syntax is referenced, it is cited in
   terms of the PostgreSQL 14+ `CREATE` statement grammar as documented
   in the PostgreSQL official documentation [PGDOC14].

   Inline examples appear in monospace code blocks.  Within prose, DPG
   keywords appear in `monospace`.  Non-terminal grammar symbols appear
   in *italics*.  Normative text and examples are separated; an example
   does not in itself constitute a normative requirement unless
   explicitly stated.

### 2.3. Examples

   Examples marked `-- OK` illustrate valid DPG source.  Examples
   marked `-- ERROR` illustrate input that MUST be rejected by the
   compiler with a diagnostic.  Example PostgreSQL DDL output is marked
   `-- emitted SQL`.

---

## 3. Project Structure and Configuration

### 3.1. Directory Layout

   A DPG project is a directory tree whose structure encodes the
   physical topology of the managed PostgreSQL deployment.  The layout
   MUST conform to the following schema:

```
<project-root>/
├── dpg.toml                        (REQUIRED) root tool configuration
│
├── <cluster-name>/                 (one per cluster)
│   ├── dpg.toml                    (REQUIRED) cluster configuration
│   ├── <cluster-objects-dir>/      (default: "cluster")
│   │   ├── roles.dpg
│   │   ├── tablespaces.dpg
│   │   └── ...
│   │
│   └── <database-name>/            (one per database)
│       ├── dpg.toml                (REQUIRED) database configuration
│       ├── extensions.dpg
│       └── schemas/
│           └── <schema-name>/
│               ├── types.dpg
│               ├── tables/
│               │   └── <table>.dpg
│               ├── views.dpg
│               └── functions.dpg
│
└── .dpg/
    └── snapshots/
        └── <cluster-name>/
            └── <database-name>.json
```

   **Discovery rules:**

   1.  The project root is the directory containing the root `dpg.toml`.

   2.  A cluster directory is any immediate subdirectory of the project
       root that contains a `dpg.toml` with a `[cluster]` section.

   3.  A database directory is any immediate subdirectory of a cluster
       directory that contains a `dpg.toml` with a `[database]`
       section, excluding the cluster objects directory.

   4.  The cluster objects directory name is taken from the
       `cluster.cluster_objects_dir` field.  It defaults to `"cluster"`.
       No database within the cluster MAY share this name.

   5.  All `.dpg` files descending from a database directory are
       automatically scoped to that database.  No in-file database
       header keyword exists.

   6.  `.dpg` files under the cluster objects directory are scoped to
       the cluster (no database context).

   The compiler MUST traverse the entire subtree of each database
   directory recursively.  Files at any depth are included.  Files
   whose names do not end in `.dpg` are silently ignored.

### 3.2. Root dpg.toml

   The root `dpg.toml` configures global compiler and linter behaviour.
   All fields are OPTIONAL; unspecified fields take the defaults shown.

```toml
[compiler]
# default_drop_behavior controls whether DROP statements include
# CASCADE or RESTRICT. Valid values: "restrict" (default), "cascade".
# Per-object DROP CASCADE overrides this setting.
default_drop_behavior = "restrict"

# concurrent_indexes controls whether index additions on existing
# tables emit CREATE INDEX CONCURRENTLY (true, default) or
# CREATE INDEX (false). Per-index override: CONCURRENTLY false.
concurrent_indexes = true

[linter]
# Emit a warning when any DEPRECATED object or column is referenced.
warn_on_deprecated = true

# Emit an error when any column lacks a COMMENT.
require_column_comments = false

# Emit an error when a ROLE PASSWORD value is a hardcoded string
# rather than an env: URI.
forbid_hardcoded_passwords = true

# Emit a warning when a table has more than this many columns.
# 0 = disabled.
max_columns_per_table = 50

# Emit a warning when two .dpg files set conflicting scalar values
# for the same object (last-declaration-wins applies silently without
# this flag).
warn_on_scalar_merge_conflict = true

[snapshots]
# Directory where snapshot JSON files are stored, relative to the
# project root.
directory = ".dpg/snapshots"
```

   The compiler MUST reject any key in `dpg.toml` that is not listed
   above with error DPG-E001 (unknown configuration key).

### 3.3. Cluster dpg.toml

   Located at `<cluster-dir>/dpg.toml`.  Configures the cluster
   connection and options.

```toml
[cluster]
# Human-readable name. Used in snapshot file paths and migration
# headers. REQUIRED.
name = "production"

# Reserved directory name for cluster-level objects.
# MUST NOT match any database directory name within the cluster.
cluster_objects_dir = "cluster"   # default

# Inline PostgreSQL connection string (libpq URI or keyword/value
# format). Mutually exclusive with `link`. OPTIONAL for offline use.
url = "postgresql://user@host:5432/postgres"

# Secrets-provider URI resolved at connection time. The reference
# implementation supports the following URI schemes:
#   env:<VAR>  — resolves to os.Getenv(<VAR>)
# Future schemes (vault, aws-secrets-manager) MAY be added.
# Mutually exclusive with `url`. OPTIONAL.
# link = "env:PRIMARY_DB_URL"

[cluster.options]
# If true, the snapshot is updated atomically after every successful
# dpg apply. Default: true.
snapshot_on_apply = true
```

   **Constraint:** `url` and `link` are mutually exclusive.  If both
   are present the compiler MUST abort with error DPG-E002 (ambiguous
   connection).  If neither is present, commands that require a live
   database connection (`dpg apply`, `dpg verify`, `dpg dump`) MUST
   fail with error DPG-E003 (no connection configured).

### 3.4. Database dpg.toml

   Located at `<cluster-dir>/<database-dir>/dpg.toml`.

```toml
[database]
# The name of the PostgreSQL database as it appears in pg_database.
# REQUIRED.
name = "myapp"

# The default schema for objects declared without an explicit schema
# qualifier. REQUIRED.
default_schema = "public"
```

### 3.5. Cluster-Level Objects Directory

   Files in the cluster objects directory declare objects that belong to
   the cluster, not to any individual database: roles, tablespaces, and
   (in the rare case of custom C-implemented FDWs) foreign data
   wrappers.  The compiler resolves the cluster objects directory name
   from `cluster.cluster_objects_dir` and MUST reject any database
   directory whose name matches it with error DPG-E004 (reserved name
   conflict).

### 3.6. Discovery Algorithm

   The compiler's file discovery phase MUST execute the following
   algorithm:

   1.  Locate the project root by searching from the current working
       directory upward for a `dpg.toml` with a `[compiler]` or
       `[linter]` or `[snapshots]` section.

   2.  For each cluster directory (immediate subdirectory of the project
       root whose `dpg.toml` contains `[cluster]`):

       a.  Parse the cluster `dpg.toml`.

       b.  For each database directory (immediate subdirectory of the
           cluster directory whose `dpg.toml` contains `[database]`,
           excluding the cluster objects directory):

           i.   Parse the database `dpg.toml`.

           ii.  Walk the database directory tree recursively.  Collect
                every file whose name ends in `.dpg`, in
                lexicographic order by full path.  This ordering is
                the canonical file order used for last-declaration-wins
                scalar conflict resolution.

       c.  Walk the cluster objects directory recursively in the same
           manner.

   3.  Pass the collected file sets to the macro preprocessor (Phase 2,
       Section 15.3).

### 3.7. Block Merge Conflict Resolution

   The DPG compiler accumulates all declarations across all `.dpg`
   files for the same logical database before compiling.  When the same
   named object appears in multiple files, its declared attributes are
   merged according to the following rules.

   **Set-valued properties** — columns, constraints, indexes, policies,
   triggers, grants, revocations, column sub-blocks — are merged by
   taking the UNION of all declared values.  Identical duplicate entries
   (same name AND same definition) are silently deduplicated.  Entries
   with the same name but different definitions are a compiler error
   (DPG-E005, conflicting set member).

   **Scalar properties** — owner, comment, tablespace, RLS flags,
   `PROTECTED`, `DEPRECATED`, `DROP CASCADE`, `RENAMED FROM`, drop
   behaviour — apply last-declaration-wins semantics.  Files are
   ordered lexicographically by their fully-qualified path relative to
   the project root.  The declaration in the alphabetically last file
   wins.  This ordering is deterministic, reproducible on any machine,
   and independent of filesystem directory-entry ordering.

   When `warn_on_scalar_merge_conflict` is enabled in the linter
   configuration (default: `true`), the compiler SHOULD emit a
   `LintDiagnostic` (not a hard error) whenever two files provide
   conflicting values for the same scalar property of the same object.
   The winning value (lexicographically last file) is used regardless.

---

---

## 4. Language Fundamentals

### 4.1. Source File Format

   DPG source files MUST be encoded in UTF-8 [RFC3629].  Byte-order
   marks (U+FEFF) MUST be silently stripped if present at the start of
   a file.  Line endings MAY be LF (U+000A) or CRLF (U+000D U+000A);
   the compiler MUST normalise all line endings to LF before processing.

   Comments follow PostgreSQL's double-dash convention (`--`) and C-
   style block comments (`/* ... */`).  Comments are stripped by the
   tokenizer before any deeper parsing.  Block comments do NOT nest.

   Identifiers are case-insensitive in conformance with PostgreSQL's
   unquoted identifier rules, except when enclosed in double-quotes,
   in which case they are case-sensitive and may contain any Unicode
   character.

### 4.2. The Two-Part Syntax Model

   Every DPG object declaration consists of at most two parts:

   **Part 1 — The native PG SQL definition.**
   Written exactly as PostgreSQL SQL dictates with only the leading
   imperative verb (CREATE, ALTER, DROP) removed.  Part 1 uses the
   same keywords, the same clause ordering, and the same syntax as the
   corresponding PostgreSQL `CREATE` statement.  The compiler prepends
   the correct verb internally when invoking the PostgreSQL parser; the
   developer never writes it.

   **Part 2 — The DPG structural block `{ ... }`.**
   An OPTIONAL trailing block that contains exclusively things
   PostgreSQL SQL expresses as *separate* DDL statements (`CREATE INDEX`,
   `GRANT`, `COMMENT ON`, `ALTER TABLE ... ALTER COLUMN SET STATISTICS`)
   plus DPG lifecycle directives (`RENAMED FROM`, `PROTECTED`,
   `DEPRECATED`, `DROP CASCADE`).

   **Decision rule:** If PostgreSQL writes it as part of `CREATE OBJECT`,
   it is Part 1.  If PostgreSQL writes it as a separate statement, it
   is Part 2.  This rule MUST be applied consistently; no clause that
   belongs in Part 1 SHALL be moved to Part 2, and no Part-2-only
   directive SHALL appear in Part 1.

```abnf
dpg-declaration  = part1 [ part2 ]

part1            = object-keyword WSP object-body terminator
                 ; object-body is the PG SQL text with CREATE verb absent

part2            = "{" WSP*
                     ( block-directive WSP* ";" WSP* )*
                   "}"

object-keyword   = "SCHEMA" / "TABLE" / "UNLOGGED TABLE" /
                   "FOREIGN TABLE" / "VIEW" / "MATERIALIZED VIEW" /
                   "RECURSIVE VIEW" / "FUNCTION" / "PROCEDURE" /
                   "AGGREGATE" / "ENUM" / "TYPE" / "DOMAIN" /
                   "SEQUENCE" / "ROLE" / "TABLESPACE" /
                   "FOREIGN DATA WRAPPER" / "SERVER" / "USER MAPPING" /
                   "PUBLICATION" / "SUBSCRIPTION" / "EVENT TRIGGER" /
                   "COLLATION" / "OPERATOR" / "OPERATOR CLASS" /
                   "OPERATOR FAMILY" / "CAST" / "STATISTICS" /
                   "TEXT SEARCH CONFIGURATION" /
                   "TEXT SEARCH DICTIONARY" /
                   "TEXT SEARCH PARSER" / "TEXT SEARCH TEMPLATE" /
                   "DEFAULT PRIVILEGES" / "VIRTUAL TYPE" /
                   "EXTENSION" / "MACRO"
```

### 4.3. The No-Verb Mandate

   The keywords `CREATE`, `ALTER`, and `DROP` are PROHIBITED in DPG
   source files at the declaration level.  The compiler MUST reject any
   source file containing these keywords outside of:

   a)  Dollar-quoted function, procedure, or aggregate bodies
       (`AS $$...$$`, `AS $tag$...$tag$`), which are opaque text and
       not interpreted by the compiler (Section 4.6).

   b)  `MIGRATE REMOVE { ... }` blocks on ENUM types (Section 5.1),
       whose body is DML passthrough.

   c)  The value of a `link =` field in `dpg.toml` (not a `.dpg` file).

   Rationale: the presence of imperative verbs indicates a migration
   file rather than a state description.  Prohibiting them enforces the
   declarative contract at parse time, producing an early diagnostic
   (DPG-E006) rather than silent misuse.

### 4.4. Structural Scoping

   The `{ }` block of any container provides a scope.  Nested object
   declarations inherit their containing context:

   -   A `TABLE` declared inside a `SCHEMA { }` block inherits the
       schema name; the developer does not repeat it.

   -   An `INDEX` declared inside a `TABLE`'s `{ }` block inherits the
       table and schema; the developer writes only the index name and
       column list.

   -   A `POLICY` declared inside a `TABLE`'s `POLICIES { }` block
       inherits the table, schema, and database.

   Schemas have no `( )` list.  Their `{ }` block directly holds all
   schema-level attributes and nested objects.

   **Explicit schema qualification is always legal** in any DPG
   declaration.  An explicit schema qualifier overrides any containing
   schema scope.

### 4.5. Statement Terminators

   The terminator rules are as follows.  These rules are NORMATIVE; a
   compiler that silently accepts deviations is non-conforming.

```abnf
terminator = paren-close SP* [ with-clause / tablespace-clause /
                                inherits-clause / partition-clause /
                                server-clause ]
           / dollar-close ";"
           / ";"
```

   **Rule T1.** A declaration whose Part 1 ends with a closing
   parenthesis `)` — tables, composite types, range types, aggregates
   — MUST NOT have a semicolon between `)` and the `{ }` block.  The
   `)` (optionally followed by `WITH`, `TABLESPACE`, `INHERITS`, or
   `PARTITION BY` clauses) is the Part 1 terminator.

   **Rule T2.** A declaration whose Part 1 ends with a dollar-quoted
   body — functions, procedures — terminates with `$$;` or `$tag$;`.
   The semicolon is mandatory after the closing delimiter.  An optional
   `{ }` block MUST follow immediately after, with no intervening
   whitespace beyond optional newlines.

   **Rule T3.** All other declarations — views, ENUM types, sequences,
   roles, publications, subscriptions, extensions, schemas without
   nested objects — terminate their Part 1 with `;`.  An optional
   `{ }` block follows immediately after `;`.

   **Rule T4.** A declaration with a `{ }` block but no Part 1
   terminator issue is the `SCHEMA` object: the schema body IS the
   `{ }` block.  The schema declaration ends when the `}` closes.

   **Rule T5.** When no `{ }` block is present, the `;` (or `$$;` or
   `)`) is the sole terminator of the complete declaration.  No further
   punctuation follows.

   Examples:

```sql
-- T1: table with { } block, no semicolon before {
TABLE users (
    id BIGINT GENERATED ALWAYS AS IDENTITY,
    CONSTRAINT pk PRIMARY KEY (id)
)
{
    INDICES { idx_email (email); }
}

-- T3: view with { } block
VIEW active_users AS SELECT id FROM users WHERE active;
{
    GRANTS { SELECT TO app_readonly; }
}

-- T2: function with { } block
FUNCTION foo() RETURNS TEXT LANGUAGE sql STABLE
AS $$
    SELECT 'hello';
$$;
{
    COMMENT "Returns hello";
}

-- T1: table with no { } block
TABLE log (
    id   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    msg  TEXT NOT NULL
);
```

### 4.6. Dollar-Quoted String Parsing

   Dollar-quoted strings use the PostgreSQL syntax `$$...$$` or
   `$tag$...$tag$` where *tag* is any identifier string (including the
   empty string).

   The compiler's dollar-quote parser MUST implement the following
   algorithm:

   1.  On encountering the token `AS` followed by optional whitespace
       followed by a dollar-quoted delimiter `$[tag]$`, record the
       opening delimiter string `D`.

   2.  Scan forward byte-by-byte without interpreting any content.
       No brace counting, no keyword scanning, no SQL parsing occurs
       inside the dollar-quoted region.  Embedded `{`, `}`, `;`,
       `CREATE`, `ALTER`, `DROP`, or any other DPG keyword are treated
       as plain text.

   3.  The first occurrence of the exact byte sequence `D` (the same
       opening delimiter) encountered during the scan terminates the
       dollar-quoted region.  Partial matches do NOT terminate.

   4.  The bytes immediately following the closing `D` MUST be `;` (per
       Rule T2).  The semicolon is the Part 1 terminator.

   5.  Named dollar-quoting (`$body$`, `$func$`, `$sql$`, or any
       `$identifier$`) is fully supported.  The opening and closing tag
       MUST match exactly, including case.

   This algorithm allows function bodies to contain any content
   including embedded SQL DML, PL/pgSQL blocks, Python, Perl,
   JavaScript, nested dollar-quoted strings, or arbitrary binary data
   encoded as text — without any escaping or modification.

### 4.7. Macro Preprocessor

   The macro preprocessor runs as the first phase of compilation,
   before any parsing.  Macros are source-level text fragments that are
   expanded inline at points of use.

#### 4.7.1. Macro Declaration

   A macro declaration uses the `MACRO` keyword and has one of two
   body forms.

```abnf
macro-decl  = "MACRO" WSP identifier WSP paren-body
            / "MACRO" WSP identifier WSP brace-body

paren-body  = "(" *( column-def "," ) column-def ")"
brace-body  = "{" *( block-directive ";" ) "}"
```

   -   A **paren-body** macro contains a comma-separated list of column
       definitions, exactly as they would appear inside a `TABLE ( )`
       list.  The opening `(` and closing `)` are part of the body and
       are stripped when the macro is expanded.

   -   A **brace-body** macro contains zero or more block directives,
       exactly as they would appear inside a `{ }` block.  The opening
       `{` and closing `}` are part of the body and are stripped when
       the macro is expanded.

   A `MACRO` declaration generates no SQL whatsoever.  It MUST appear
   at the top level of a `.dpg` file (not inside any `{ }` or `( )`
   block).  A `MACRO` declaration inside a block is a compiler error
   (DPG-E007).

   Examples:

```sql
MACRO common_timestamps (
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ
)

MACRO audit_block {
    OWNER "app_admin";
    ENABLE ROW LEVEL SECURITY;
}
```

#### 4.7.2. Macro Spread

   The spread operator `...name` expands a macro inline at the point
   of use.

```abnf
spread = "..." identifier
```

   A paren-body macro MUST only be spread inside a `( )` list.
   Spreading it inside a `{ }` block is a compiler error (DPG-E008).

   A brace-body macro MUST only be spread inside a `{ }` block.
   Spreading it inside a `( )` list is a compiler error (DPG-E009).

   Spreading an undefined macro name is a compiler error (DPG-E010).

   Example:

```sql
TABLE accounts (
    id         UUID NOT NULL DEFAULT gen_random_uuid(),
    ...common_timestamps,
    CONSTRAINT pk_accounts PRIMARY KEY (id)
)
{
    ...audit_block
}
```

   The compiler expands `...common_timestamps` to:

```
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ
```

   and `...audit_block` to:

```
    OWNER "app_admin";
    ENABLE ROW LEVEL SECURITY;
```

   Expansion is performed textually before tokenization.  The resulting
   source text is then tokenized and parsed as if written verbatim.

#### 4.7.3. Macro Scoping Rules

   -   Macros are file-scoped in the current version.  A macro defined
       in `tables/users.dpg` is NOT visible in `tables/orders.dpg`.
       Cross-file macro sharing is deferred (Section 23).

   -   A macro name MUST be unique within its file.  Redefining a macro
       name in the same file is a compiler error (DPG-E011).

   -   Macros MAY NOT be recursive (a paren-body may not contain a
       spread of itself or of any other macro that eventually spreads
       it).  Circular macro references are a compiler error (DPG-E012).

   -   The `MACRO` keyword is a DPG preprocessor keyword and does not
       violate the No-Verb Mandate (Section 4.3).

### 4.8. Dual Definition Modes

   For every collection-typed sub-object (indexes, policies, triggers,
   grants, revocations, partitions, column blocks), DPG supports two
   equivalent syntactic modes that MAY be freely mixed within the same
   file:

   **Mode A — Plural block:** The collection keyword is the block
   header; individual entries omit the singular keyword.

```sql
INDICES {
    idx_email  (email);
    idx_status (status) WHERE (status = 'active');
}
```

   **Mode B — Singular keyword:** The singular keyword precedes each
   individual entry outside a plural block.

```sql
INDEX idx_email  (email);
INDEX idx_status (status) WHERE (status = 'active');
```

   Both forms are semantically identical.  The compiler MUST merge them
   into a single logical collection before differencing.

   The complete mapping of plural to singular keywords is:

   | Plural block header  | Singular keyword   |
   |----------------------|--------------------|
   | `INDICES`            | `INDEX`            |
   | `POLICIES`           | `POLICY`           |
   | `TRIGGERS`           | `TRIGGER`          |
   | `GRANTS`             | `GRANT`            |
   | `REVOCATIONS`        | `REVOCATION`       |
   | `PARTITIONS`         | `PARTITION`        |
   | `COLUMNS`            | `COLUMN`           |
   | `CONSTRAINTS`        | `CONSTRAINT`       |

### 4.9. Block Merging

   When the same object is declared across multiple `.dpg` files (the
   same schema-qualified name and kind), the compiler MUST merge all
   declarations into a single logical object before IR construction.
   Merge semantics follow Section 3.7.

   Block merging occurs after macro expansion and before tokenization.
   The merged result is treated as if all declarations had appeared in
   a single file.

### 4.10. Identifiers

   DPG follows PostgreSQL's identifier rules:

   -   An **unquoted identifier** consists of letters (including Unicode
       letters), digits, dollar signs, and underscores.  It MUST begin
       with a letter or underscore.  Unquoted identifiers are
       case-folded to lowercase.

   -   A **quoted identifier** is enclosed in double-quotes (`"`).  It
       may contain any character except a double-quote.  To include a
       literal double-quote, write `""`.  Quoted identifiers are
       case-sensitive and preserve their original casing.

   Schema-qualified names use the form `schema.name` or, for nested
   objects, `schema.table.column`.  The compiler resolves unqualified
   names using the enclosing scope context established by `SCHEMA { }`
   blocks, falling back to the `database.default_schema` configuration
   value.

---

---

## 5. Type System

### 5.1. ENUM Types

   ENUM types use PostgreSQL's natural parenthesised list syntax.
   The Part 1 body is the value list enclosed in `( )`, terminated with
   `;` per Rule T3.  An optional `{ }` block holds comments and
   value-removal migration directives.

   **PG equivalent:** `CREATE TYPE name AS ENUM ('v1', 'v2', ...)`

```abnf
enum-decl     = "ENUM" WSP identifier WSP
                "(" enum-values ")" ";"
                [ "{" enum-block "}" ]

enum-values   = DQUOTE identifier DQUOTE
                *( "," WSP DQUOTE identifier DQUOTE )

enum-block    = *( enum-directive ";" )

enum-directive = comment-dir
               / migrate-remove-dir
```

   Examples:

```sql
-- Minimal ENUM
ENUM user_status ('active', 'suspended', 'deleted');

-- ENUM with comment
ENUM invoice_status ('draft', 'sent', 'paid', 'void', 'overdue');
{
    COMMENT "Billing lifecycle states for customer invoices";
}

-- ENUM with value removal
ENUM order_status ('pending', 'confirmed', 'shipped', 'delivered');
{
    COMMENT "Order lifecycle states";
    MIGRATE REMOVE ('cancelled') {
        UPDATE orders SET status = 'delivered' WHERE status = 'cancelled';
    }
}
```

#### 5.1.1. Adding ENUM Values

   When a new value appears in the DPG source that is absent from the
   snapshot, the compiler emits:

```sql
ALTER TYPE <schema>.<name> ADD VALUE '<new_value>';
```

   `ALTER TYPE ... ADD VALUE` MUST be emitted as a non-transactional
   step (Safety: `MANUAL`) because PostgreSQL does not permit it inside
   a transaction block in versions prior to 16.  For PostgreSQL 16+, it
   MAY be placed in the transactional block; the compiler SHOULD detect
   the server version at apply time and choose accordingly.  When the
   server version is unknown (offline plan), the compiler MUST emit it
   as non-transactional.

#### 5.1.2. Removing ENUM Values

   Removing a value from an ENUM is not directly supported by
   PostgreSQL.  The `MIGRATE REMOVE` directive provides a safe migration
   path.  The `MIGRATE REMOVE` body is DML that runs *before* the type
   is rebuilt to migrate existing data away from the removed value.

   When a value is absent from the DPG source but present in the
   snapshot, and a `MIGRATE REMOVE` directive covers it, the compiler
   MUST emit the following sequence, all within a single transaction:

   1.  `CREATE TYPE <schema>.<name>__dpg_new AS ENUM (<reduced-values>);`
       — a new type with the value removed.

   2.  Execute each DML statement in the `MIGRATE REMOVE` body verbatim.

   3.  Verify that no rows in any column typed as `<name>` still hold
       the removed value.  If any remain, the compiler MUST abort the
       transaction and report error DPG-E013 with a table-by-table row
       count.

   4.  For each table column typed as `<name>`:

       ```sql
       ALTER TABLE <schema>.<table>
           ALTER COLUMN <col> TYPE <schema>.<name>__dpg_new
           USING <col>::text::<schema>.<name>__dpg_new;
       ```

   5.  `DROP TYPE <schema>.<name>;`

   6.  `ALTER TYPE <schema>.<name>__dpg_new RENAME TO <name>;`

   On any failure in steps 2–6: `DROP TYPE IF EXISTS <schema>.<name>__dpg_new;`
   and rollback.

   When a value is absent from the source but no `MIGRATE REMOVE`
   directive covers it, the compiler MUST emit a diagnostic (DPG-E014,
   unguarded ENUM value removal) and refuse to proceed without the
   `--allow-destructive` flag.  Even with `--allow-destructive`, the
   compiler MUST attempt to verify that no rows hold the removed value
   before proceeding.

#### 5.1.3. Reordering ENUM Values

   PostgreSQL ENUM values have a fixed ordering.  Reordering values
   is treated as a remove + add and classified as `DESTRUCTIVE` unless
   all affected values are brand new (not in the snapshot).

### 5.2. Composite Types

   Composite types declare a row-structured type with named, typed
   attributes.  The attribute list uses `( )` per Rule T1.

   **PG equivalent:** `CREATE TYPE name AS (attr1 type1, attr2 type2, ...)`

```abnf
composite-decl = "TYPE" WSP schema-name WSP "AS" WSP
                 "(" attribute-list ")" ";"
```

   Example:

```sql
SCHEMA public {
    TYPE address AS (
        street      TEXT,
        city        TEXT,
        state       CHAR(2),
        postal_code TEXT,
        country     CHAR(2)
    );
}
```

   **Diffing semantics:**

   -   Adding an attribute: `ALTER TYPE <name> ADD ATTRIBUTE <col> <type>` — `SAFE`.
   -   Dropping an attribute: `ALTER TYPE <name> DROP ATTRIBUTE <col>` — `DESTRUCTIVE`.
   -   Changing an attribute type: `ALTER TYPE <name> ALTER ATTRIBUTE <col> TYPE <new>` — `DESTRUCTIVE`.
   -   Renaming an attribute: Use `RENAMED FROM` inside a `COLUMN`-equivalent
       sub-block (see Section 7.6 for the syntax; the same mechanism applies
       to composite type attributes).

### 5.3. Range Types

   Range types use two `( )` groups: the first is the keyword `AS RANGE`
   following the type name; the body is the options list.

   **PG equivalent:** `CREATE TYPE name AS RANGE (options)`

```abnf
range-decl = "TYPE" WSP schema-name WSP "AS RANGE" WSP
             "(" range-options ")" ";"
```

   Example:

```sql
SCHEMA public {
    TYPE float8range AS RANGE (
        SUBTYPE      = float8,
        SUBTYPE_DIFF = float8mi
    );
}
```

   **Diffing semantics:** Any change to a range type's options requires
   `DROP TYPE CASCADE` followed by `CREATE TYPE`.  This is classified
   as `DESTRUCTIVE`.

### 5.4. Domain Types

   Domains add constraints and a default to an existing base type.
   The base type appears after `AS`.  Constraints and default appear in
   the `{ }` block per Tenet 5.

   **PG equivalent:**
   `CREATE DOMAIN name AS base_type [DEFAULT expr] [CONSTRAINT name CHECK (expr)] ...`

```abnf
domain-decl   = "DOMAIN" WSP schema-name WSP "AS" WSP type-name ";"
                "{" domain-block "}"

domain-block  = *( domain-directive ";" )

domain-directive = "DEFAULT" WSP expr
                 / "CONSTRAINT" WSP identifier WSP "CHECK" WSP "(" expr ")"
                 / "NOT NULL"
                 / comment-dir
```

   Example:

```sql
SCHEMA public {
    DOMAIN positive_integer AS INTEGER {
        DEFAULT 1;
        CONSTRAINT positive_only  CHECK (VALUE > 0);
        CONSTRAINT reasonable_max CHECK (VALUE < 1000000);
    }
}
```

   **Diffing semantics:**

   -   Adding a `DEFAULT`: `ALTER DOMAIN <name> SET DEFAULT <expr>` — `SAFE`.
   -   Dropping a `DEFAULT`: `ALTER DOMAIN <name> DROP DEFAULT` — `SAFE`.
   -   Adding a constraint: `ALTER DOMAIN <name> ADD CONSTRAINT <name> CHECK (...)` — `CAUTION`.
   -   Dropping a constraint: `ALTER DOMAIN <name> DROP CONSTRAINT <name>` — `SAFE`.
   -   Changing the base type: requires `DROP DOMAIN CASCADE` + `CREATE DOMAIN` — `DESTRUCTIVE`.

### 5.5. Base (Shell) Types

   Base types implement a custom storage type using C-defined input and
   output functions.  The body is the PostgreSQL `CREATE TYPE` options
   list.  Diffing is by text hash only (`SAFE` for additions;
   `DESTRUCTIVE` for any change or removal).

   **PG equivalent:** `CREATE TYPE name (INPUT = func, OUTPUT = func, ...)`

```sql
SCHEMA public {
    TYPE mytype (
        INPUT          = mytype_in,
        OUTPUT         = mytype_out,
        INTERNALLENGTH = 16,
        ALIGNMENT      = double
    );
}
```

### 5.6. Virtual Types

   Virtual types are DPG-native annotations with no PostgreSQL
   equivalent.  They generate NO SQL whatsoever.  Their purpose is to
   carry type information for downstream consumers such as ORM generators
   and type-safe query builders that read the DPG snapshot or IR via
   the `pkg/dpg` API.

```abnf
virtual-type-decl = "VIRTUAL TYPE" WSP schema-name WSP "AS" WSP vtype-body ";"
                    [ "{" vtype-block "}" ]

vtype-body  = <arbitrary text not containing ";" or "{"
               at brace depth 0>
vtype-block = *( comment-dir ";" )
```

   The body after `AS` is stored verbatim.  The compiler MUST NOT
   interpret or validate it.  No type checking is performed.

   Examples:

```sql
VIRTUAL TYPE user_state AS "active" | "suspended" | "deleted";

VIRTUAL TYPE billing.payment_method AS
    { kind: "card", last4: string, brand: string }
    | { kind: "bank_ach", routing: string }
    | { kind: "wallet" };
{
    COMMENT "Payment method discriminated union for type generation";
}
```

   **Rules:**

   -   `VIRTUAL TYPE` MAY be schema-qualified; if unqualified it
       defaults to `default_schema`.

   -   No `CREATE TYPE`, `ALTER TYPE`, or `DROP TYPE` is EVER emitted
       for a virtual type.

   -   Virtual types appear in the snapshot under `"kind": "virtual_type"`
       for round-trip consistency.  `dpg plan` produces no SQL for
       additions, modifications, or removals of virtual types.

   -   The `{ }` block accepts ONLY `COMMENT`.  Any other directive is
       a compiler error (DPG-E015).

---

## 6. Schema and Namespace Objects

### 6.1. SCHEMA

   Schemas have no `( )` list.  Their `{ }` block directly holds all
   schema-level attributes and nested objects.

   **PG equivalent:** `CREATE SCHEMA [IF NOT EXISTS] name [AUTHORIZATION role]`

```abnf
schema-decl = "SCHEMA" WSP identifier
              "{" schema-block "}"

schema-block = *( schema-directive / nested-object )

schema-directive = owner-dir
                 / comment-dir
                 / renamed-from-dir
                 / grants-block
                 / default-privileges-decl
```

   Examples:

```sql
SCHEMA public {
    -- Objects in the public schema declared inline here
}

SCHEMA analytics {
    OWNER "analytics_role";
    COMMENT "Derived tables and event aggregations";

    TABLE events ( ... ) { ... }
    FUNCTION compute_daily() RETURNS VOID LANGUAGE plpgsql AS $$ ... $$;
}
```

   **Renaming:**

```sql
SCHEMA reporting {
    RENAMED FROM old_reporting;
}
```

   Emits: `ALTER SCHEMA old_reporting RENAME TO reporting;` — Safety `CAUTION`.

   **`RENAMED FROM` with no other content** is valid:

```sql
SCHEMA new_name {
    RENAMED FROM old_name;
}
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New schema | `CREATE SCHEMA name` | `SAFE` |
   | RENAMED FROM | `ALTER SCHEMA old RENAME TO new` | `CAUTION` |
   | OWNER change | `ALTER SCHEMA name OWNER TO role` | `SAFE` |
   | COMMENT change | `COMMENT ON SCHEMA name IS '...'` | `SAFE` |
   | Schema removed | `DROP SCHEMA name [RESTRICT\|CASCADE]` | `DESTRUCTIVE` |

### 6.2. EXTENSION

   Extensions are database-level objects and MUST be declared in a
   database `.dpg` file (not in the cluster objects directory).

   **PG equivalent:**
   `CREATE EXTENSION [IF NOT EXISTS] name [SCHEMA schema] [VERSION version] [CASCADE]`

```abnf
extension-decl = "EXTENSION" WSP identifier
                 [ WSP "SCHEMA" WSP identifier ]
                 [ WSP "VERSION" WSP SQUOTE version SQUOTE ]
                 [ WSP "CASCADE" ]
                 ";"
```

   Examples:

```sql
EXTENSION pgcrypto;
EXTENSION postgis SCHEMA public VERSION '3.3';
EXTENSION pg_trgm CASCADE;
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New extension | `CREATE EXTENSION IF NOT EXISTS name [SCHEMA ...] [VERSION ...] [CASCADE]` | `SAFE` |
   | VERSION change | `ALTER EXTENSION name UPDATE [TO version]` | `CAUTION` |
   | Extension removed | `DROP EXTENSION name [CASCADE]` | `DESTRUCTIVE` |
   | SCHEMA change | Drop + recreate | `DESTRUCTIVE` |

---

---

## 7. Tables

### 7.1. Table Declaration Syntax

   Tables are the most syntactically rich object type in DPG.  The
   complete grammar is:

```abnf
table-decl  = [ "UNLOGGED" WSP ] "TABLE" WSP schema-table-name WSP
              "(" column-list ")"
              *( table-clause )
              [ ";" ]
              [ "{" table-block "}" ]
            / "FOREIGN TABLE" WSP schema-table-name WSP
              "(" column-list ")"
              *( table-clause )
              WSP "SERVER" WSP identifier
              [ WSP "OPTIONS" WSP "(" option-list ")" ]
              [ ";" ]
              [ "{" table-block "}" ]

table-clause = WITH "(" storage-params ")"
             / TABLESPACE identifier
             / INHERITS "(" table-ref-list ")"
             / PARTITION-BY-clause

table-block  = *( table-directive )

table-directive = owner-dir
                / comment-dir
                / renamed-from-dir
                / protected-dir
                / deprecated-dir
                / drop-cascade-dir
                / rls-enable-dir
                / rls-force-dir
                / column-block
                / indices-block
                / policies-block
                / triggers-block
                / grants-block
                / revocations-block
                / constraint-dir
                / partitions-block
```

   Per Rule T1, when the table has a `{ }` block, there is NO
   semicolon between `)` (or the last `table-clause`) and `{`.  When
   the table has NO `{ }` block, the Part 1 is terminated with `;`
   after the last `table-clause` (or directly after `)`).

### 7.2. Column Definitions

   Column definitions appear inside the `( )` list and follow
   PostgreSQL's `CREATE TABLE` column syntax exactly.

```abnf
column-def  = col-name WSP type-ref
              *( col-constraint )

col-constraint = "NOT NULL"
               / "NULL"
               / "DEFAULT" WSP expr
               / "GENERATED ALWAYS AS" WSP "(" expr ")" WSP "STORED"
               / "GENERATED ALWAYS AS IDENTITY" [ identity-opts ]
               / "GENERATED BY DEFAULT AS IDENTITY" [ identity-opts ]
               / "PRIMARY KEY" [ conflict-clause ]
               / "UNIQUE" [ nulls-distinct ] [ conflict-clause ]
               / "REFERENCES" WSP table-ref [ ref-opts ]
               / "CHECK" WSP "(" expr ")" [ no-inherit ]
               / "CONSTRAINT" WSP identifier WSP col-constraint
               / "COMPRESSION" WSP method
               / "COLLATE" WSP collation-name

identity-opts = "(" "START WITH" int
                    [ "INCREMENT BY" int ]
                    [ "MINVALUE" int / "NO MINVALUE" ]
                    [ "MAXVALUE" int / "NO MAXVALUE" ]
                    [ "CACHE" int ]
                    [ "CYCLE" / "NO CYCLE" ]
                ")"

ref-opts      = [ "MATCH FULL" / "MATCH PARTIAL" / "MATCH SIMPLE" ]
                [ "ON DELETE" ref-action ]
                [ "ON UPDATE" ref-action ]

ref-action    = "NO ACTION" / "RESTRICT" / "CASCADE" /
                "SET NULL" / "SET DEFAULT"
```

   **PRIMARY KEY implies NOT NULL:** PostgreSQL enforces that every
   PRIMARY KEY column is implicitly NOT NULL.  The DPG compiler MUST
   apply the same inference:

   -   Writing `NOT NULL` on a PRIMARY KEY column in the source is
       accepted but is silently treated as redundant.
   -   The compiler MUST NOT emit a redundant `NOT NULL` clause for
       PRIMARY KEY columns in generated `CREATE TABLE` DDL.
   -   The compiler MUST NOT emit a spurious `ALTER COLUMN SET NOT NULL`
       when diffing a PRIMARY KEY column.

   **Inline vs. table-level constraints:** Both forms are accepted and
   treated as semantically equivalent.  The compiler MUST normalise
   single-column `PRIMARY KEY`, `UNIQUE`, `CHECK`, and `REFERENCES`
   constraints to the inline form in its emitted `CREATE TABLE`.
   Multi-column constraints MUST remain table-level.  Named
   single-column constraints (e.g. `CONSTRAINT pk_x PRIMARY KEY`) are
   emitted inline in the `CREATE TABLE` output.

   Examples:

```sql
-- Inline form (preferred for single-column constraints)
TABLE accounts (
    id   UUID    NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    slug TEXT    NOT NULL UNIQUE,
    org  UUID    NOT NULL REFERENCES organisations (id) ON DELETE CASCADE
);

-- Named inline constraint
TABLE accounts (
    id UUID NOT NULL DEFAULT gen_random_uuid()
        CONSTRAINT pk_accounts PRIMARY KEY
);

-- Table-level (required for multi-column constraints)
TABLE order_items (
    order_id   BIGINT NOT NULL,
    product_id BIGINT NOT NULL,
    CONSTRAINT pk_order_items PRIMARY KEY (order_id, product_id)
);
```

   **Column type change diffing:**

   -   A type change that PostgreSQL can apply implicitly
       (e.g., `VARCHAR(10)` → `VARCHAR(20)`) is classified `CAUTION`.
   -   A type change requiring an explicit cast is classified
       `DESTRUCTIVE` unless a `USING` expression is present in the
       `COLUMN name { USING expr; }` block, in which case it is
       classified `CAUTION`.
   -   The `ALTER TABLE <t> ALTER COLUMN <c> TYPE <new> [USING <expr>]`
       statement acquires an `ACCESS EXCLUSIVE` lock for the duration
       of the rewrite, which is why it is always at least `CAUTION`.

### 7.3. Constraints

   Table constraints may appear in the `( )` list, in the `{ }` block,
   or in both.  The compiler identifies constraints by name and merges
   them into a single logical set.  Same name + same definition =
   deduplicated.  Same name + different definition = DPG-E005.

```abnf
table-constraint = "CONSTRAINT" WSP identifier WSP table-constraint-body
                   [ "NOT VALID" ]
                   [ "DEFERRABLE" [ "INITIALLY DEFERRED" / "INITIALLY IMMEDIATE" ] ]

table-constraint-body
    = "PRIMARY KEY" WSP "(" col-list ")"
    / "UNIQUE" [ "NULLS NOT DISTINCT" ] WSP "(" col-list ")"
    / "CHECK" WSP "(" expr ")" [ "NO INHERIT" ]
    / "FOREIGN KEY" WSP "(" col-list ")" WSP
      "REFERENCES" WSP table-ref WSP "(" col-list ")" ref-opts
    / "EXCLUDE" WSP "USING" WSP method WSP "(" excl-list ")"
      [ "WHERE" "(" expr ")" ]
```

   **`NOT VALID` lifecycle:**

   1.  First migration: `ALTER TABLE <t> ADD CONSTRAINT <name> ... NOT VALID;`
       — Safety `CAUTION`.

   2.  When `NOT VALID` is removed in source, the compiler emits:
       `ALTER TABLE <t> VALIDATE CONSTRAINT <name>;` — Safety `CAUTION`.

   3.  After validation, the constraint MAY be moved to the `( )` list.
       The compiler identifies it by name and treats it as already
       existing (no new DDL emitted).

   **`NOT VALID` placement:** A constraint with `NOT VALID` MUST be
   declared in the `{ }` block.  Writing `NOT VALID` in the `( )` list
   is a compiler error (DPG-E016) because PostgreSQL itself does not
   support `NOT VALID` in `CREATE TABLE`.

   **DEFERRABLE FK cycles:** When two tables have a circular foreign
   key dependency and both FKs are `DEFERRABLE`, the compiler emits
   both `CREATE TABLE` statements first, then the circular FK as a
   subsequent `ALTER TABLE ADD CONSTRAINT ... DEFERRABLE`.  If a cycle
   exists with no `DEFERRABLE` FK, the compiler emits error DPG-E017
   with the full dependency cycle listed.

   Examples:

```sql
TABLE orders (
    id     BIGINT GENERATED ALWAYS AS IDENTITY,
    amount NUMERIC(10,2) NOT NULL,
    CONSTRAINT pk_orders PRIMARY KEY (id)
)
{
    -- NOT VALID must be in the { } block
    CONSTRAINT ck_amount_positive CHECK (amount > 0) NOT VALID;

    CONSTRAINT fk_account FOREIGN KEY (account_id)
        REFERENCES accounts (id)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED;
}
```

### 7.4. The COLUMN Reference Block

   Inside a table's `{ }` block, `COLUMN name { ... }` references an
   existing column declared in the `( )` list and holds attributes
   that PostgreSQL expresses as separate `ALTER TABLE ... ALTER COLUMN`
   statements.

```abnf
column-block    = "COLUMN" WSP col-name WSP "{" col-block-body "}"
               /  "COLUMNS" WSP "{" *col-named-block "}"

col-named-block = col-name WSP "{" col-block-body "}"

col-block-body  = *( col-block-directive ";" )

col-block-directive
    = "COMMENT" WSP DQUOTE text DQUOTE
    / "STATISTICS" WSP integer
    / "COMPRESSION" WSP method
    / "STORAGE" WSP storage-type
    / "DEPRECATED" WSP DQUOTE text DQUOTE
    / "USING" WSP expr
    / "RENAMED FROM" WSP col-name
    / grants-block
    / revocations-block
```

   The complete attribute table:

   | Directive | PostgreSQL DDL emitted |
   |-----------|------------------------|
   | `COMMENT "text"` | `COMMENT ON COLUMN t.c IS '...'` |
   | `STATISTICS n` | `ALTER TABLE t ALTER COLUMN c SET STATISTICS n` |
   | `COMPRESSION method` | `ALTER TABLE t ALTER COLUMN c SET COMPRESSION m` |
   | `STORAGE type` | `ALTER TABLE t ALTER COLUMN c SET STORAGE s` |
   | `DEPRECATED "msg"` | `COMMENT ON COLUMN t.c IS '[DEPRECATED] msg'` |
   | `USING expr` | `ALTER TABLE t ALTER COLUMN c TYPE ... USING expr` |
   | `RENAMED FROM old` | `ALTER TABLE t RENAME COLUMN old TO new` |
   | `GRANTS { ... }` | `GRANT priv (col) ON TABLE t TO role` |
   | `REVOCATIONS { ... }` | `REVOKE priv (col) ON TABLE t FROM role` |

   **Validation rules:**

   -   Every `COLUMN name { }` MUST reference a column name that exists
       in the `( )` list.  A reference to a non-existent column is a
       compiler error (DPG-E018).

   -   `COLUMN` blocks do NOT declare new columns.  New columns are
       declared only in the `( )` list.

   -   After a rename, the `COLUMN` block MUST use the new name;
       `RENAMED FROM` carries the old name.

   -   After any column rename, all index and constraint declarations in
       the `{ }` block MUST reference the new column name.  Any
       reference to the old column name is a compiler error (DPG-E019).

   **Statistics target values:**

   | Value | Meaning |
   |-------|---------|
   | `-1` | Reset to table default (normally 100 at cluster level) |
   | `0` | Disable statistics collection for this column |
   | `1–10000` | Explicit target; above 100 gives more detail at higher ANALYZE cost |

   Values above `10000` are a compiler error (DPG-E020).

   **Storage types:** `plain`, `main`, `extended`, `external`.

   **Compression methods:** `pglz`, `lz4` (requires PostgreSQL 14+
   compiled with LZ4 support).

### 7.5. Column-Level Grants

   Column-level grants use the `GRANTS { }` / `REVOCATIONS { }` syntax
   inside a `COLUMN name { }` block.  The column scope is inferred by
   the compiler.  The emitted DDL is:

```sql
GRANT privilege (col) ON TABLE schema.table TO role;
```

   Column-level grants follow the same additive model as table-level
   grants (Section 11.2): DPG only emits `GRANT`; it NEVER auto-revokes.

```sql
TABLE users ( id BIGINT ..., email TEXT, ssn TEXT )
{
    COLUMN email {
        GRANTS {
            SELECT TO reporting_role;
            SELECT TO analytics_role;
        }
    }

    COLUMN ssn {
        STORAGE extended;
        GRANTS       { SELECT TO compliance_role; }
        REVOCATIONS  { ALL PRIVILEGES FROM PUBLIC; }
    }

    GRANTS { SELECT, INSERT, UPDATE TO app_service; }
}
```

### 7.6. Column Renaming

   Column renames are DPG lifecycle directives declared in
   `COLUMN new_name { RENAMED FROM old_name; }`.  The new name appears
   in both the `( )` list and the `COLUMN` block.  The old name appears
   only inside `RENAMED FROM`.

```sql
TABLE users (
    email_address TEXT NOT NULL,
    CONSTRAINT uq_users_email UNIQUE (email_address)
)
{
    COLUMN email_address {
        RENAMED FROM email;
        COMMENT "Verified email address";
    }
}
```

   **Compiler resolution algorithm:**

   1.  The compiler sees `email_address` in the `( )` list and
       `COLUMN email_address { RENAMED FROM email; }` in the `{ }` block.

   2.  It looks up `email` in the snapshot.
       -   If `email` is in the snapshot and `email_address` is NOT:
           this is State A (fresh rename). Emit
           `ALTER TABLE users RENAME COLUMN email TO email_address;`
           (Safety: `CAUTION`).
       -   If `email_address` is already in the snapshot:
           the rename has already been applied (State B). Treat as a
           normal column update; the `RENAMED FROM` directive is a no-op.
       -   If neither `email` nor `email_address` is in the snapshot:
           this is a new column with a stale `RENAMED FROM`. Emit error
           DPG-E021 (stale RENAMED FROM directive).

   3.  After emitting the rename, all constraint and index declarations
       MUST use `email_address`.  The compiler validates and emits
       DPG-E019 on any reference to `email`.

   **`RENAMED FROM` for tables and schemas** follows the same algorithm
   with `QualifiedName()` substituted for column name:

   -   `TABLE user_accounts { RENAMED FROM users; }` →
       `ALTER TABLE users RENAME TO user_accounts;`
   -   `SCHEMA reporting { RENAMED FROM old_reporting; }` →
       `ALTER SCHEMA old_reporting RENAME TO reporting;`

### 7.7. Indexes

   Indexes are declared in the `INDICES { }` block (or using the
   singular `INDEX` keyword) inside a table's `{ }` block.

```abnf
index-decl  = index-name WSP
              [ "UNIQUE" WSP ]
              [ "USING" WSP method WSP ]
              "(" index-col-list ")"
              [ "INCLUDE" WSP "(" col-list ")" ]
              [ "WITH" WSP "(" storage-params ")" ]
              [ "WHERE" WSP "(" predicate ")" ]
              [ "TABLESPACE" WSP identifier ]
              [ "CONCURRENTLY" WSP boolean ]
              ";"

index-col-list = index-col *( "," index-col )
index-col   = ( col-name / "(" expr ")" )
              [ "ASC" / "DESC" ]
              [ "NULLS FIRST" / "NULLS LAST" ]
              [ "COLLATE" WSP identifier ]
              [ "opclass" ]
```

   **Concurrency behaviour:**

   -   By default, index additions on existing tables emit
       `CREATE INDEX CONCURRENTLY`.  This is a `MANUAL` operation
       (non-transactional; emitted after `COMMIT`).

   -   When `concurrent_indexes = false` in the root `dpg.toml`, or
       when `CONCURRENTLY false` is specified on the individual index,
       the compiler emits `CREATE INDEX` (transactional; Safety `CAUTION`
       for index additions on non-empty tables).

   -   For brand-new tables (no rows), concurrent index creation is
       equivalent to non-concurrent.  The compiler MAY still emit
       `CONCURRENTLY` for consistency; it is not harmful.

   **Index identity:** An index is uniquely identified by its name
   within a schema.  Two indexes with the same name but different
   definitions are a compiler error (DPG-E005).

   **Partial index predicates** are stored as normalised text and
   diffed by text equality.  Whitespace normalisation is applied:
   all runs of whitespace are collapsed to a single space.

   **Expression indexes:** The column expression `( expr )` is treated
   as opaque text and diffed by text equality after whitespace
   normalisation.

   **Covering indexes:** `INCLUDE (col1, col2)` adds columns to the
   index leaf pages without participating in the search key.  Adding or
   removing `INCLUDE` columns requires dropping and recreating the
   index (Safety: `CAUTION`; no data loss but requires a lock and a
   full index rebuild).

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New index (existing table) | `CREATE [UNIQUE] INDEX [CONCURRENTLY] [IF NOT EXISTS] name ON ...` | `MANUAL` (default) or `CAUTION` |
   | Index removed | `DROP INDEX [CONCURRENTLY] name` | `CAUTION` |
   | Any structural change | Drop + recreate | `CAUTION` or `MANUAL` |

   Indexes on new tables (emitted in the same migration as the
   `CREATE TABLE`) are emitted as non-concurrent `CREATE INDEX` in the
   same transactional block.

   Examples:

```sql
TABLE users ( email TEXT, status user_status, ... )
{
    INDICES {
        idx_email          (email);
        idx_unique_slug    UNIQUE (slug);
        idx_tenant_created (tenant_id ASC, created_at DESC);
        idx_active_users   (email) WHERE (status = 'active');
        idx_location       USING gist  (location);
        idx_tags           USING gin   (tags);
        idx_lower_email    (lower(email));
        idx_covering       (user_id) INCLUDE (email, created_at);
        idx_brin           USING brin (created_at)
                               WITH (pages_per_range = 128);
        idx_archived       (archived_at) TABLESPACE archive_space;
        idx_no_concurrent  (payload) CONCURRENTLY false;
    }
}
```

### 7.8. Row Level Security

   Row Level Security (RLS) is enabled and configured in the table's
   `{ }` block.

```abnf
rls-enable-dir = "ENABLE ROW LEVEL SECURITY"
rls-force-dir  = "FORCE ROW LEVEL SECURITY"

policy-decl    = policy-name WSP "FOR" WSP command
                 [ WSP "AS" WSP permissiveness ]
                 [ WSP "TO" WSP role-list ]
                 [ WSP "USING" WSP "(" expr ")" ]
                 [ WSP "WITH CHECK" WSP "(" expr ")" ]
                 ";"

command        = "ALL" / "SELECT" / "INSERT" / "UPDATE" / "DELETE"
permissiveness = "PERMISSIVE" / "RESTRICTIVE"
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | `ENABLE ROW LEVEL SECURITY` added | `ALTER TABLE t ENABLE ROW LEVEL SECURITY` | `SAFE` |
   | `FORCE ROW LEVEL SECURITY` added | `ALTER TABLE t FORCE ROW LEVEL SECURITY` | `SAFE` |
   | Both removed | `ALTER TABLE t DISABLE ROW LEVEL SECURITY` | `SAFE` |
   | New policy | `CREATE POLICY name ON t FOR ... TO ... USING (...) WITH CHECK (...)` | `SAFE` |
   | Policy changed | `DROP POLICY name ON t; CREATE POLICY ...` | `SAFE` |
   | Policy removed | `DROP POLICY name ON t` | `SAFE` |

   Example:

```sql
TABLE orders ( ... )
{
    ENABLE ROW LEVEL SECURITY;
    FORCE ROW LEVEL SECURITY;

    POLICIES {
        view_own FOR SELECT
            USING (user_id = auth.uid());

        insert_own FOR INSERT
            WITH CHECK (user_id = auth.uid());

        update_own FOR UPDATE
            USING     (user_id = auth.uid())
            WITH CHECK (user_id = auth.uid() AND status != 'locked');

        restrict_deleted AS RESTRICTIVE FOR ALL
            USING (deleted_at IS NULL);

        admin_all FOR ALL
            TO admin_role
            USING (true);

        service_read FOR SELECT
            TO service_role, readonly_role
            USING (true);
    }
}
```

### 7.9. Triggers

   Triggers are declared in the `TRIGGERS { }` block inside a table's
   `{ }` block.

```abnf
trigger-decl = trigger-name WSP timing WSP event-list
               [ WSP "FROM" WSP table-ref ]
               [ WSP deferrable-clause ]
               [ WSP referencing-clause ]
               WSP for-each
               [ WSP "WHEN" WSP "(" expr ")" ]
               WSP "EXECUTE FUNCTION" WSP func-ref "(" arg-list ")"
               ";"

timing       = "BEFORE" / "AFTER" / "INSTEAD OF"
event-list   = event *( "OR" WSP event )
event        = "INSERT" / "UPDATE" [ "OF" col-list ] / "DELETE" / "TRUNCATE"
for-each     = "FOR EACH ROW" / "FOR EACH STATEMENT"

referencing-clause = "REFERENCING"
    ( "OLD TABLE AS" identifier [ "NEW TABLE AS" identifier ]
    / "NEW TABLE AS" identifier [ "OLD TABLE AS" identifier ] )
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New trigger | `CREATE [CONSTRAINT] TRIGGER name ... ON t ...` | `SAFE` |
   | Trigger changed | `DROP TRIGGER name ON t; CREATE TRIGGER ...` | `SAFE` |
   | Trigger removed | `DROP TRIGGER name ON t` | `SAFE` |

   Trigger identity is `(schema, table, trigger_name)`.

   Example:

```sql
TABLE users ( ... )
{
    TRIGGERS {
        before_insert BEFORE INSERT
            FOR EACH ROW
            EXECUTE FUNCTION set_defaults();

        after_email_change AFTER UPDATE OF email
            FOR EACH ROW
            WHEN (OLD.email IS DISTINCT FROM NEW.email)
            EXECUTE FUNCTION notify_email_change();

        audit_changes AFTER INSERT OR UPDATE OR DELETE
            REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
            FOR EACH STATEMENT
            EXECUTE FUNCTION audit_table_changes();

        check_ref CONSTRAINT AFTER INSERT OR UPDATE
            FROM orders
            DEFERRABLE INITIALLY DEFERRED
            FOR EACH ROW
            EXECUTE FUNCTION check_ref_integrity();
    }
}
```

### 7.10. Table-Level Grants and Revocations

   Table-level grants and revocations are declared in `GRANTS { }` and
   `REVOCATIONS { }` blocks inside the table's `{ }` block.

```abnf
grants-block      = "GRANTS" WSP "{" *( grant-entry ";" ) "}"
revocations-block = "REVOCATIONS" WSP "{" *( revoke-entry ";" ) "}"

grant-entry  = privilege-list WSP "TO" WSP role-list
               [ WSP "WITH GRANT OPTION" ]
revoke-entry = ( privilege-list / "ALL PRIVILEGES" ) WSP
               "FROM" WSP role-list
               [ WSP "CASCADE" ]

privilege-list = privilege *( "," privilege )
privilege      = "SELECT" / "INSERT" / "UPDATE" / "DELETE" /
                 "TRUNCATE" / "REFERENCES" / "TRIGGER" /
                 "USAGE" / "EXECUTE" / "CREATE" / "CONNECT" /
                 "TEMPORARY" / "ALL" / "ALL PRIVILEGES"
```

   Grants follow the additive model (Section 11.2).

### 7.11. Table Lifecycle Directives

```abnf
renamed-from-dir = "RENAMED FROM" WSP identifier
protected-dir    = "PROTECTED"
deprecated-dir   = "DEPRECATED" WSP DQUOTE text DQUOTE
drop-cascade-dir = "DROP CASCADE"
owner-dir        = "OWNER" WSP DQUOTE identifier DQUOTE
comment-dir      = "COMMENT" WSP DQUOTE text DQUOTE
```

   **`PROTECTED`:** The compiler MUST refuse to emit a `DROP TABLE`
   for a protected table even when the table is absent from the desired
   state.  Removing a protected table requires first removing the
   `PROTECTED` directive.  Safety: any attempt to drop a protected
   table is error DPG-E022.

   **`DEPRECATED "msg"`:** The compiler emits a `COMMENT ON TABLE`
   prefixed with `[DEPRECATED]` and the message text.  The linter emits
   a warning when any other object references a deprecated table
   (if `warn_on_deprecated = true`).

   **`DROP CASCADE`:** Overrides `default_drop_behavior` for this
   specific object.  The compiler emits `DROP TABLE name CASCADE` when
   removing this table.

   **`RENAMED FROM`:** See Section 7.6.

### 7.12. Unlogged and Foreign Tables

   **Unlogged tables:**

```sql
UNLOGGED TABLE session_cache (
    key   TEXT NOT NULL PRIMARY KEY,
    value JSONB
);
```

   Emits `CREATE UNLOGGED TABLE`.  Changing a regular table to unlogged
   (or vice versa) requires `DROP TABLE CASCADE` + `CREATE TABLE`:
   classified as `DESTRUCTIVE`.

   **Temporary tables** are session-scoped.  DPG MUST NOT manage them.
   A `TEMPORARY TABLE` keyword anywhere in a `.dpg` file is a compiler
   error (DPG-E023).

   **Foreign tables:** `SERVER` and `OPTIONS` are Part 1 clauses
   appearing after `)` per Tenet 5.  They MUST NOT be moved to the
   `{ }` block.

```sql
FOREIGN TABLE remote_events (
    id         BIGINT,
    payload    JSONB,
    created_at TIMESTAMPTZ
) SERVER log_server OPTIONS (table_name 'events', schema_name 'public')
{
    COLUMN id { COMMENT "Remote event primary key"; }
    GRANTS { SELECT TO app_readonly; }
}
```

### 7.13. Partitioned Tables

   Partitioning uses the `PARTITION BY` clause in Part 1 (after `)`,
   per Tenet 5).

```abnf
partition-by-clause = "PARTITION BY" WSP partition-strategy
                      WSP "(" partition-col-list ")"

partition-strategy  = "RANGE" / "LIST" / "HASH"

partition-col-list  = partition-col *( "," partition-col )
partition-col       = col-name / "(" expr ")"
```

   Partitions are declared in the `PARTITIONS { }` block inside the
   table's `{ }` block.

```abnf
partition-decl  = partition-name WSP "FOR VALUES" WSP bounds-clause ";"
                / partition-name WSP "DEFAULT" ";"

bounds-clause   = "FROM" WSP "(" literal-list ")"
                  "TO" WSP "(" literal-list ")"        -- RANGE
                / "IN" WSP "(" literal-list ")"        -- LIST
                / "WITH" WSP "(" modulus-remainder ")" -- HASH
```

   **Sub-partitioning:** A partition entry MAY have its own
   `PARTITION BY` sub-clause and a nested `{ PARTITIONS { ... } }`
   block:

```sql
TABLE events ( ... ) PARTITION BY RANGE (created_at)
{
    PARTITIONS {
        events_2024 FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')
            PARTITION BY LIST (region) {
                PARTITIONS {
                    events_2024_us FOR VALUES IN ('us-east', 'us-west');
                    events_2024_eu FOR VALUES IN ('eu-west', 'eu-central');
                }
            };
    }
}
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New partition | `CREATE TABLE <name> PARTITION OF <parent> FOR VALUES ...` | `SAFE` |
   | Partition removed | `DROP TABLE <name>` | `DESTRUCTIVE` |
   | Partition strategy change | Requires `--approve-partition-rebuild` | `MANUAL` |

   **Partition strategy change procedure** (requires
   `--approve-partition-rebuild`):

   1.  Create new partitioned table with target strategy.
   2.  Create all declared partitions.
   3.  `INSERT INTO new_table SELECT * FROM old_table`.
   4.  Verify row counts match.
   5.  `DROP TABLE old_table`.
   6.  `ALTER TABLE new_table RENAME TO old_table`.
   7.  Recreate all indexes, constraints, grants, and policies.

   This sequence is emitted as a mix of `SAFE` and `MANUAL` steps
   with inline human-readable comments explaining each step.

   Indexes declared at the parent partitioned table level are
   automatically inherited by all partitions.  The compiler MUST NOT
   emit duplicate `CREATE INDEX` statements on partitions that already
   inherit a parent-level index.

---

---

## 8. Views

### 8.1. Regular Views

   Views use `AS <query>` per PostgreSQL's `CREATE VIEW` syntax.  The
   query text is Part 1, terminated with `;` per Rule T3.  An optional
   `{ }` block holds grants, a comment, owner, and other externally-
   attachable concerns.

   **PG equivalent:**
   `CREATE [OR REPLACE] VIEW name [(col-list)] [WITH (options)] AS query [WITH CHECK OPTION]`

```abnf
view-decl   = "VIEW" WSP schema-view-name
              [ WSP "(" col-name-list ")" ]
              [ WSP "WITH" WSP "(" view-options ")" ]
              WSP "AS" WSP query ";"
              [ "{" view-block "}" ]

view-block  = *( view-directive ";" )

view-directive = owner-dir / comment-dir / renamed-from-dir
               / deprecated-dir / grants-block / revocations-block
```

   The `WITH CHECK OPTION` (and `WITH LOCAL CHECK OPTION`) clause
   MUST appear at the END of the query text, immediately before the
   terminating `;`, per PostgreSQL syntax.

   Examples:

```sql
SCHEMA public {
    -- Minimal view
    VIEW active_users AS
        SELECT id, email, created_at
        FROM users
        WHERE status = 'active' AND deleted_at IS NULL;

    -- With named column list
    VIEW user_summary (user_id, email, order_count) AS
        SELECT u.id, u.email, COUNT(o.id)
        FROM users u
        LEFT JOIN orders o ON o.user_id = u.id
        GROUP BY u.id, u.email;

    -- With security_barrier option
    VIEW secure_view WITH (security_barrier = true) AS
        SELECT id, email FROM users WHERE tenant_id = current_tenant();

    -- With check option
    VIEW active_orders AS
        SELECT * FROM orders WHERE status != 'cancelled'
        WITH LOCAL CHECK OPTION;

    -- With { } block
    VIEW admin_summary AS
        SELECT id, email, created_at FROM users WHERE role = 'admin';
    {
        COMMENT "Admin user summary view";
        GRANTS { SELECT TO app_readonly; }
    }
}
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New view | `CREATE VIEW ...` | `SAFE` |
   | Query changed, same output column list | `CREATE OR REPLACE VIEW ...` | `SAFE` |
   | Output column list changed (any way) | `DROP VIEW CASCADE; CREATE VIEW` | `DESTRUCTIVE` |
   | Owner changed | `ALTER VIEW name OWNER TO role` | `SAFE` |
   | Comment changed | `COMMENT ON VIEW name IS '...'` | `SAFE` |
   | View removed | `DROP VIEW name [CASCADE]` | `DESTRUCTIVE` |

   **Output column list comparison:** The compiler compares the columns
   produced by the view query by name and ordinal position.  If
   either the name or the position of any output column differs, the
   change is classified as `DESTRUCTIVE`.

### 8.2. Materialized Views

   Materialized views use the same syntax as regular views, prefixed
   with `MATERIALIZED`.

   **PG equivalent:**
   `CREATE MATERIALIZED VIEW [IF NOT EXISTS] name [WITH (options)] [TABLESPACE ts] AS query [WITH NO DATA]`

```abnf
matview-decl = "MATERIALIZED VIEW" WSP schema-view-name
               [ WSP "WITH" WSP "(" storage-params ")" ]
               [ WSP "TABLESPACE" WSP identifier ]
               WSP "AS" WSP query
               [ WSP "WITH NO DATA" ]
               ";"
               [ "{" matview-block "}" ]

matview-block = *( matview-directive ";" )
matview-directive = owner-dir / comment-dir / indices-block
                  / grants-block / revocations-block
```

   The `{ }` block of a materialized view MAY contain `INDICES { }`
   to declare indexes on the materialized view.

   Example:

```sql
SCHEMA analytics {
    MATERIALIZED VIEW daily_revenue AS
        SELECT
            date_trunc('day', created_at) AS day,
            SUM(total_amount)             AS revenue,
            COUNT(*)                      AS order_count
        FROM orders
        WHERE status = 'completed'
        GROUP BY 1;

    MATERIALIZED VIEW product_stats
    WITH (fillfactor = 90)
    TABLESPACE analytics_space AS
        SELECT product_id, COUNT(*) AS purchases, AVG(price) AS avg_price
        FROM order_items
        GROUP BY product_id
    WITH NO DATA;
    {
        INDICES   { idx_product_stats_id (product_id); }
        GRANTS    { SELECT TO app_readonly; }
    }
}
```

   **Diffing semantics:** Any change to the query text of a
   materialized view requires `DROP MATERIALIZED VIEW` followed by
   `CREATE MATERIALIZED VIEW` — classified as `DESTRUCTIVE`.
   `REFRESH MATERIALIZED VIEW` is a runtime operation and is out of
   scope for DPG (Section 23).

### 8.3. Recursive Views

   Recursive views use the `RECURSIVE` keyword and require a column
   name list.

   **PG equivalent:**
   `CREATE RECURSIVE VIEW name (col1, ...) AS query`

```sql
SCHEMA public {
    RECURSIVE VIEW org_tree (id, parent_id, depth, path) AS
        SELECT id, parent_id, 0, ARRAY[id]
        FROM departments WHERE parent_id IS NULL
        UNION ALL
        SELECT d.id, d.parent_id, t.depth + 1, t.path || d.id
        FROM departments d JOIN org_tree t ON d.parent_id = t.id;
}
```

   Diffing semantics identical to regular views.

---

## 9. Functions and Procedures

### 9.1. Function Declaration Syntax

   Functions are written in complete, unmodified PostgreSQL SQL syntax
   with only the `CREATE OR REPLACE` verb removed.  The dollar-quoted
   body is Part 1 (terminated with `$$;` or `$tag$;` per Rule T2).
   The optional `{ }` block is Part 2.

```abnf
function-decl = "FUNCTION" WSP schema-func-name "(" [ arg-list ] ")"
                WSP return-clause
                *( func-attribute )
                WSP "AS" WSP dollar-string ";"
                [ "{" func-block "}" ]

return-clause  = "RETURNS" WSP return-type
               / "RETURNS TABLE" WSP "(" col-def-list ")"
               / "RETURNS SETOF" WSP type-ref

func-attribute = "LANGUAGE" WSP lang-name
               / "VOLATILE" / "STABLE" / "IMMUTABLE"
               / "CALLED ON NULL INPUT" / "RETURNS NULL ON NULL INPUT"
                 / "STRICT"
               / "SECURITY DEFINER" / "SECURITY INVOKER"
               / "PARALLEL UNSAFE" / "PARALLEL RESTRICTED" / "PARALLEL SAFE"
               / "COST" WSP number
               / "ROWS" WSP number
               / "SUPPORT" WSP func-ref
               / "WINDOW"
               / "SET" WSP identifier WSP "=" WSP expr
               / "SET" WSP identifier WSP "FROM CURRENT"

func-block     = *( func-directive ";" )
func-directive = comment-dir / grants-block / deprecated-dir
               / renamed-from-dir
```

   Function attributes MUST appear in PostgreSQL's own documented
   ordering.  The compiler does not reorder them.

   All attributes listed in `func-attribute` above correspond exactly
   to options accepted by `CREATE FUNCTION` in PostgreSQL 14+.  The
   compiler passes them through verbatim when reconstructing the `CREATE
   OR REPLACE FUNCTION` statement.

### 9.2. Function Attributes Reference

   | Attribute | Meaning |
   |-----------|---------|
   | `VOLATILE` | Default. May modify DB; result may differ per call. |
   | `STABLE` | Constant within a single transaction for given inputs. Cannot modify DB. |
   | `IMMUTABLE` | Constant for all time for given inputs. Index-eligible. |
   | `STRICT` | Alias for `RETURNS NULL ON NULL INPUT`. Returns NULL if any argument is NULL. |
   | `SECURITY DEFINER` | Executes with the privileges of the function owner. |
   | `SECURITY INVOKER` | Default. Executes with the privileges of the calling role. |
   | `PARALLEL SAFE` | Safe for parallel execution in any worker. |
   | `PARALLEL RESTRICTED` | Parallel-safe but must run in the leader process. |
   | `PARALLEL UNSAFE` | Default. Cannot run in parallel. |
   | `COST n` | Estimated execution cost in `cpu_operator_cost` units. |
   | `ROWS n` | Estimated number of rows returned (set-returning functions only). |
   | `SUPPORT func` | Planner support function (PostgreSQL 12+). |
   | `SET param = value` | Sets the named GUC to `value` for the duration of the call. |
   | `SET param FROM CURRENT` | Sets the named GUC from its current session value. |
   | `WINDOW` | Declares the function as a window function. |

   **`SECURITY DEFINER` + `search_path`:** Functions declared with
   `SECURITY DEFINER` SHOULD include `SET search_path = schema [, ...]`
   to prevent search path injection.  The linter SHOULD warn when a
   `SECURITY DEFINER` function lacks an explicit `search_path` setting
   (rule: `security_definer_search_path`).

   Examples:

```sql
SCHEMA public {
    -- Simple SQL function
    FUNCTION active_user_count() RETURNS BIGINT
    LANGUAGE sql STABLE PARALLEL SAFE
    AS $$
        SELECT COUNT(*) FROM users WHERE status = 'active';
    $$;

    -- PL/pgSQL function
    FUNCTION get_user(p_email TEXT) RETURNS users
    LANGUAGE plpgsql STABLE SECURITY DEFINER
    SET search_path = public
    AS $$
    DECLARE v_user users;
    BEGIN
        SELECT * INTO STRICT v_user FROM users WHERE email = p_email;
        RETURN v_user;
    EXCEPTION
        WHEN NO_DATA_FOUND THEN
            RAISE EXCEPTION 'User not found: %', p_email;
    END;
    $$;
    {
        COMMENT "Fetch a user record by verified email address";
        GRANTS { EXECUTE TO app_service; }
    }

    -- Named dollar-quote (avoids conflict when body contains $$)
    FUNCTION format_price(p_amount NUMERIC) RETURNS TEXT
    LANGUAGE plpgsql IMMUTABLE STRICT
    AS $func$
    BEGIN
        RETURN '$' || TO_CHAR(p_amount, 'FM999,999,990.00');
    END;
    $func$;
    {
        GRANTS { EXECUTE TO app_readonly, app_service; }
    }
}
```

### 9.3. Procedures

   Procedures follow the same model as functions.  They omit the
   `RETURNS` clause.  Procedures MAY issue `COMMIT` mid-execution.

   **PG equivalent:** `CREATE [OR REPLACE] PROCEDURE name (...) LANGUAGE lang AS $$...$$`

```sql
SCHEMA public {
    PROCEDURE process_settlements()
    LANGUAGE plpgsql SECURITY DEFINER
    AS $$
    DECLARE v_id settlements.id%TYPE;
    BEGIN
        FOR v_id IN SELECT id FROM settlements WHERE processed = false LOOP
            PERFORM settle_order(v_id);
            COMMIT;
        END LOOP;
    END;
    $$;
    {
        GRANTS { EXECUTE TO scheduler_role; }
    }
}
```

   Procedure diffing semantics are identical to function diffing
   (Section 9.5).  Procedure identity is `(schema, name, arg_types)`
   where `OUT` and `TABLE` mode parameters are excluded from the type
   key per PostgreSQL's overloading rules.

### 9.4. Aggregate Functions

   Aggregates use two `( )` groups per PostgreSQL's `CREATE AGGREGATE`
   syntax — both are Part 1 per Tenet 5.

   **PG equivalent:**
   `CREATE [OR REPLACE] AGGREGATE name (input_types) (SFUNC = ..., STYPE = ..., ...)`

```abnf
aggregate-decl = "AGGREGATE" WSP schema-func-name
                 WSP "(" agg-input-list ")"
                 WSP "(" agg-options ")"
                 [ "{" func-block "}" ]

agg-input-list = "*"
               / [ mode WSP ] type-ref *( "," [ mode WSP ] type-ref )
               / ordered-set-sig

ordered-set-sig = type-ref *( "," type-ref )
                  WSP "ORDER BY" WSP type-ref *( "," type-ref )
```

   Example:

```sql
SCHEMA public {
    AGGREGATE product (DOUBLE PRECISION) (
        SFUNC    = float8mul,
        STYPE    = DOUBLE PRECISION,
        INITCOND = '1'
    )
    {
        COMMENT "Multiplicative aggregate over DOUBLE PRECISION values";
        GRANTS { EXECUTE TO app_service; }
    }
}
```

   **Diffing semantics:** Aggregate identity is `(schema, name, input_types)`.
   Changes to `SFUNC`, `STYPE`, `INITCOND`, `FINALFUNC`, `COMBINEFUNC`,
   or `SERIALFUNC` require `DROP AGGREGATE CASCADE` followed by
   `CREATE AGGREGATE` — classified as `DESTRUCTIVE`.

### 9.5. Function Body Diffing Semantics

   The compiler stores a SHA-256 hash of the normalised function body
   in the snapshot (see Section 16.3).  Normalisation consists of:

   1.  Stripping leading and trailing whitespace from the body text.
   2.  Collapsing all internal runs of whitespace (spaces, tabs,
       newlines) to a single space character.

   Any change to the body — including whitespace-only changes after
   normalisation — changes the hash and causes the compiler to emit:

   ```sql
   CREATE OR REPLACE FUNCTION schema.name(...) RETURNS ... AS $$...$$;
   ```

   No semantic analysis of procedural code is performed.  The compiler
   does not detect semantically equivalent reformulations.  This is a
   known, accepted limitation.

   **Function signature changes** (argument types, return type,
   language, `SECURITY DEFINER`, `STRICT`, any attribute) are handled
   as follows:

   -   Changes to attributes that `CREATE OR REPLACE FUNCTION` can
       update (`SECURITY DEFINER`, `STRICT`, `VOLATILE`/`STABLE`/
       `IMMUTABLE`, `PARALLEL`, `COST`, `ROWS`, `SET` options):
       emit `CREATE OR REPLACE FUNCTION` — Safety `SAFE`.

   -   Changes to the argument list or return type: PostgreSQL does
       not support `CREATE OR REPLACE` for these.  The compiler emits
       `DROP FUNCTION CASCADE` followed by `CREATE FUNCTION` —
       classified as `DESTRUCTIVE`.

---

## 10. Sequences

   Sequences are schema-level objects used for auto-incrementing values
   not backed by `GENERATED AS IDENTITY` or `SERIAL`.

   **PG equivalent:**
   `CREATE SEQUENCE name [AS type] [INCREMENT BY n] [MINVALUE n] [MAXVALUE n] [START WITH n] [CACHE n] [CYCLE|NO CYCLE] [OWNED BY table.col]`

```abnf
sequence-decl  = "SEQUENCE" WSP schema-name
                 *( sequence-option )
                 ";"
                 [ "{" sequence-block "}" ]

sequence-option = "AS" WSP seq-type
                / "INCREMENT BY" WSP integer
                / "MINVALUE" WSP integer / "NO MINVALUE"
                / "MAXVALUE" WSP integer / "NO MAXVALUE"
                / "START WITH" WSP integer
                / "CACHE" WSP integer
                / "CYCLE" / "NO CYCLE"
                / "OWNED BY" WSP table-col-ref

seq-type        = "SMALLINT" / "INTEGER" / "BIGINT"

sequence-block  = *( ( owner-dir / comment-dir / grants-block ) ";" )
```

   **Rule:** Sequences backing `GENERATED AS IDENTITY` or `SERIAL`
   columns are managed automatically by PostgreSQL and MUST NOT be
   declared in DPG.  The compiler MUST emit a warning (lint rule
   `serial_sequence_declared`) if a declared sequence name matches
   the pattern `<table>_<col>_seq` and a column with that identity
   pattern exists.

   Example:

```sql
SCHEMA public {
    SEQUENCE order_number_seq
        AS BIGINT
        START WITH  10000
        INCREMENT BY 1
        MINVALUE     10000
        MAXVALUE     99999999
        CACHE        50
        NO CYCLE
        OWNED BY orders.order_number;
}
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New sequence | `CREATE SEQUENCE ...` | `SAFE` |
   | Increment/min/max/cache/cycle changed | `ALTER SEQUENCE name ...` | `SAFE` |
   | `OWNED BY` changed | `ALTER SEQUENCE name OWNED BY ...` | `SAFE` |
   | `AS type` changed | `DROP SEQUENCE; CREATE SEQUENCE` | `DESTRUCTIVE` |
   | Sequence removed | `DROP SEQUENCE name` | `DESTRUCTIVE` |

---

## 11. Access Control

### 11.1. Roles

   Roles are cluster-level objects declared in `.dpg` files inside the
   cluster objects directory.

   **PG equivalent:**
   `CREATE ROLE name [WITH options]`

```abnf
role-decl  = "ROLE" WSP identifier WSP "{" role-block "}"

role-block = *( role-directive ";" )

role-directive = "LOGIN" / "NOLOGIN"
               / "SUPERUSER" [ WSP boolean ] / "NOSUPERUSER"
               / "CREATEDB" [ WSP boolean ] / "NOCREATEDB"
               / "CREATEROLE" [ WSP boolean ] / "NOCREATEROLE"
               / "INHERIT" / "NOINHERIT"
               / "REPLICATION" / "NOREPLICATION"
               / "BYPASSRLS" / "NOBYPASSRLS"
               / "CONNECTION LIMIT" WSP integer
               / "PASSWORD" WSP ( SQUOTE text SQUOTE / env-uri )
               / "VALID UNTIL" WSP SQUOTE timestamp SQUOTE
               / "IN ROLE" WSP role-list
               / "ROLE" WSP role-list
               / "ADMIN" WSP role-list
               / comment-dir

env-uri    = SQUOTE "env:" identifier SQUOTE
```

   **Hardcoded passwords:** The linter MUST emit an error (not a
   warning) when `PASSWORD 'literal'` is used and `forbid_hardcoded_passwords`
   is enabled (default: `true`).  Passwords MUST use the `env:VAR_NAME`
   syntax.  The compiler MUST NOT store plaintext password values in
   the snapshot; it stores a boolean `has_password` only.

   Example:

```sql
-- production/cluster/roles.dpg

ROLE app_readonly {
    NOLOGIN;
    COMMENT "Read-only access for reporting tools";
}

ROLE app_service {
    LOGIN;
    PASSWORD 'env:APP_SERVICE_PW';
    CONNECTION LIMIT 20;
    VALID UNTIL '2030-01-01';
}

ROLE app_admin {
    LOGIN;
    SUPERUSER  false;
    CREATEDB   false;
    CREATEROLE false;
    INHERIT;
    IN ROLE pg_read_all_stats;
}
```

   **Diffing semantics:** Role changes emit `ALTER ROLE name ...`
   with the changed options.  Role removal emits `DROP ROLE name`
   (Safety: `DESTRUCTIVE`).

### 11.2. Grants — The Additive Model

   DPG follows PostgreSQL's own additive privilege model:

   **Declaring a grant emits a `GRANT` statement.**

   **Removing a grant declaration emits nothing.** If revocation is
   intended, an explicit `REVOCATIONS { }` entry MUST be added.

   This is a deliberate design choice.  DPG does not attempt to manage
   the full privilege graph.  It only tracks the grants it declared and
   ensures they are present.  Grants applied by other means (e.g., by
   a DBA directly) are not disturbed.

   `dpg verify` reports as drift any DPG-declared grant that is absent
   from the live catalog.  It does NOT report extra grants present in
   the live catalog but absent from DPG source.

   **`WITH GRANT OPTION`:** Including `WITH GRANT OPTION` in a
   `GRANTS { }` entry causes the compiler to emit
   `GRANT ... TO role WITH GRANT OPTION`.  Removing `WITH GRANT OPTION`
   while keeping the grant emits nothing (removing the grant option
   requires an explicit revoke-and-regrant cycle which the operator
   SHOULD perform manually).

### 11.3. Revocations

   Explicit revocations are declared in `REVOCATIONS { }` blocks and
   cause the compiler to emit `REVOKE` statements.

   Unlike grants, revocations are NOT idempotent by default: running
   `REVOKE` when the privilege is already absent is an error in
   PostgreSQL.  The compiler MUST emit `REVOKE ... FROM role` without
   any `IF EXISTS` guard, relying on the operator to verify the privilege
   existed before running the migration (or relying on the transaction
   to roll back on failure).

   **However**, the compiler SHOULD check the snapshot: if the
   revocation targets a role that was never granted the privilege by
   DPG, it MAY emit a warning (lint rule: `unnecessary_revocation`).

### 11.4. Default Privileges

   Default privileges apply to future objects created by a role.

   **PG equivalent:**
   `ALTER DEFAULT PRIVILEGES [FOR ROLE role] [IN SCHEMA schema] GRANT ... / REVOKE ...`

```abnf
default-privileges-decl =
    "DEFAULT PRIVILEGES"
    [ WSP "FOR ROLE" WSP identifier ]
    [ WSP "IN SCHEMA" WSP identifier ]
    "{" dp-block "}"

dp-block = *( ( grants-block / revocations-block ) ";" )
```

   Inside the `GRANTS { }` / `REVOCATIONS { }` sub-blocks, the object
   type is specified with an `ON` clause:

```sql
GRANTS {
    SELECT   ON TABLES    TO app_readonly;
    EXECUTE  ON FUNCTIONS TO app_service;
    USAGE    ON SEQUENCES TO app_service;
}
```

   Example:

```sql
SCHEMA public {
    DEFAULT PRIVILEGES FOR ROLE app_admin {
        GRANTS {
            SELECT   ON TABLES    TO app_readonly;
            EXECUTE  ON FUNCTIONS TO app_service;
            USAGE    ON SEQUENCES TO app_service;
        }
    }
}
```

   Emits:

```sql
ALTER DEFAULT PRIVILEGES FOR ROLE app_admin IN SCHEMA public
    GRANT SELECT ON TABLES TO app_readonly;
ALTER DEFAULT PRIVILEGES FOR ROLE app_admin IN SCHEMA public
    GRANT EXECUTE ON FUNCTIONS TO app_service;
ALTER DEFAULT PRIVILEGES FOR ROLE app_admin IN SCHEMA public
    GRANT USAGE ON SEQUENCES TO app_service;
```

---

---

## 12. Full-Text Search Objects

### 12.1. Text Search Configurations

   Text search configurations define how documents are parsed and
   tokenised.  The `MAPPING FOR` sub-block is a declarative way to
   set token mappings; the compiler emits
   `ALTER TEXT SEARCH CONFIGURATION ... ALTER MAPPING FOR ...`.

   **PG equivalent:**
   `CREATE TEXT SEARCH CONFIGURATION name (COPY = source [, PARSER = parser])`

```abnf
tsconfig-decl = "TEXT SEARCH CONFIGURATION" WSP schema-name
                WSP "(" tsconfig-opts ")"
                [ "{" tsconfig-block "}" ]
                ";"

tsconfig-opts  = "COPY" WSP "=" WSP qual-name
               / "PARSER" WSP "=" WSP qual-name

tsconfig-block = *( ( comment-dir / mapping-dir ) ";" )

mapping-dir = "MAPPING FOR" WSP token-type-list
              WSP "WITH" WSP dict-list
```

   Example:

```sql
SCHEMA public {
    TEXT SEARCH CONFIGURATION english_unaccented (COPY = pg_catalog.english) {
        MAPPING FOR hword, hword_part, word
            WITH unaccent, english_stem;
    }
}
```

   Emits:

```sql
CREATE TEXT SEARCH CONFIGURATION public.english_unaccented (COPY = pg_catalog.english);
ALTER TEXT SEARCH CONFIGURATION public.english_unaccented
    ALTER MAPPING FOR hword, hword_part, word WITH unaccent, english_stem;
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New config | `CREATE TEXT SEARCH CONFIGURATION ...` | `SAFE` |
   | Mapping added/changed | `ALTER TEXT SEARCH CONFIGURATION ... ALTER MAPPING FOR ...` | `SAFE` |
   | Mapping removed | `ALTER TEXT SEARCH CONFIGURATION ... DROP MAPPING FOR ...` | `SAFE` |
   | Config removed | `DROP TEXT SEARCH CONFIGURATION name` | `DESTRUCTIVE` |

### 12.2. Text Search Dictionaries

   **PG equivalent:**
   `CREATE TEXT SEARCH DICTIONARY name (TEMPLATE = tmpl, [options])`

```sql
SCHEMA public {
    TEXT SEARCH DICTIONARY english_ispell (
        TEMPLATE  = ispell,
        DictFile  = english,
        AffFile   = english,
        StopWords = english
    );
}
```

   Any change to a text search dictionary's options requires
   `DROP TEXT SEARCH DICTIONARY` followed by recreation —
   classified as `DESTRUCTIVE` if the dictionary is in use.

### 12.3. Text Search Parsers

   Text search parsers are low-level objects, typically installed via
   an extension.  Explicit declaration is provided for completeness.

   **PG equivalent:**
   `CREATE TEXT SEARCH PARSER name (START = func, GETTOKEN = func, END = func, LEXTYPES = func [, HEADLINE = func])`

```sql
SCHEMA public {
    TEXT SEARCH PARSER my_parser (
        START    = prsd_start,
        GETTOKEN = prsd_nexttoken,
        END      = prsd_end,
        LEXTYPES = prsd_lextype,
        HEADLINE = prsd_headline
    );
}
```

   Any change to a parser requires drop + recreate (`DESTRUCTIVE`).

### 12.4. Text Search Templates

   **PG equivalent:**
   `CREATE TEXT SEARCH TEMPLATE name ([INIT = func,] LEXIZE = func)`

```sql
SCHEMA public {
    TEXT SEARCH TEMPLATE ispell_template (
        LEXIZE = dispell_lexize,
        INIT   = dispell_init
    );
}
```

   Any change to a template requires drop + recreate (`DESTRUCTIVE`).

---

## 13. Logical Replication

### 13.1. Publications

   Publications are database-level objects.  The Part 1 body follows
   PostgreSQL's `CREATE PUBLICATION` syntax exactly.

   **PG equivalent:**
   `CREATE PUBLICATION name [FOR TABLE table[, ...] | FOR ALL TABLES] [WITH (options)]`

```abnf
publication-decl = "PUBLICATION" WSP identifier
                   WSP publication-scope
                   [ WSP "WITH" WSP "(" pub-options ")" ]
                   ";"
                   [ "{" pub-block "}" ]

publication-scope = "FOR ALL TABLES"
                  / "FOR TABLE" WSP pub-table-list
                  / "FOR ALL TABLES IN SCHEMA" WSP schema-list

pub-table-list    = pub-table *( "," WSP pub-table )
pub-table         = schema-table-name
                    [ "(" col-list ")" ]
                    [ "WHERE" WSP "(" predicate ")" ]

pub-block = *( ( comment-dir / grants-block ) ";" )
```

   Examples:

```sql
PUBLICATION user_data
    FOR TABLE users, profiles
    WITH (publish = 'insert, update, delete');
{
    COMMENT "Primary replication stream for user data";
}

PUBLICATION all_tables FOR ALL TABLES;

PUBLICATION filtered_orders
    FOR TABLE orders (id, customer_id, status, total)
    WHERE (status != 'draft');
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New publication | `CREATE PUBLICATION ...` | `SAFE` |
   | Table list changed | `ALTER PUBLICATION name SET TABLE ...` | `SAFE` |
   | Options changed | `ALTER PUBLICATION name SET (...)` | `SAFE` |
   | Publication removed | `DROP PUBLICATION name` | `DESTRUCTIVE` |

### 13.2. Subscriptions

   Subscriptions are database-level objects.

   **PG equivalent:**
   `CREATE SUBSCRIPTION name CONNECTION 'connstr' PUBLICATION pub [, ...] [WITH (options)]`

```abnf
subscription-decl = "SUBSCRIPTION" WSP identifier
                    WSP "CONNECTION" WSP SQUOTE connstr SQUOTE
                    WSP "PUBLICATION" WSP identifier-list
                    [ WSP "WITH" WSP "(" sub-options ")" ]
                    ";"
```

   Example:

```sql
SUBSCRIPTION replica_users
    CONNECTION 'host=primary.db.internal dbname=myapp user=replicator'
    PUBLICATION user_data
    WITH (enabled = true, copy_data = true);
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New subscription | `CREATE SUBSCRIPTION ...` | `SAFE` |
   | `CONNECTION` changed | `ALTER SUBSCRIPTION name CONNECTION 'newstr'` | `DESTRUCTIVE` |
   | `enabled` changed | `ALTER SUBSCRIPTION name ENABLE` / `DISABLE` | `SAFE` |
   | Publication list changed | `ALTER SUBSCRIPTION name SET PUBLICATION ...` | `SAFE` |
   | Subscription removed | `DROP SUBSCRIPTION name` | `DESTRUCTIVE` |

---

## 14. Advanced PostgreSQL Objects

### 14.1. Event Triggers

   Event triggers fire on DDL events cluster-wide.  They are database-
   level objects (not cluster-level).

   **PG equivalent:**
   `CREATE EVENT TRIGGER name ON event [WHEN TAG IN ('tag', ...)] EXECUTE FUNCTION func()`

```abnf
event-trigger-decl = "EVENT TRIGGER" WSP identifier
                     WSP "ON" WSP event-type
                     [ WSP "WHEN TAG IN" WSP "(" tag-list ")" ]
                     WSP "EXECUTE FUNCTION" WSP func-ref "()"
                     ";"

event-type = "ddl_command_start" / "ddl_command_end" /
             "table_rewrite" / "sql_drop"
```

   Example:

```sql
EVENT TRIGGER prevent_drop_table
    ON sql_drop
    WHEN TAG IN ('DROP TABLE', 'DROP SCHEMA')
    EXECUTE FUNCTION abort_drop();
```

   **Diffing semantics:** Any change requires `DROP EVENT TRIGGER` +
   `CREATE EVENT TRIGGER` (`SAFE`; no data involved).

### 14.2. Collations

   **PG equivalent:**
   `CREATE COLLATION [IF NOT EXISTS] name (LOCALE = locale | LC_COLLATE = lc, LC_CTYPE = lc | PROVIDER = provider [, DETERMINISTIC = bool])`

```sql
SCHEMA public {
    COLLATION case_insensitive (
        PROVIDER      = icu,
        LOCALE        = 'und-u-ks-level2',
        DETERMINISTIC = false
    );
}
```

   **Diffing semantics:** Any property change requires `DROP COLLATION`
   + `CREATE COLLATION` — classified as `DESTRUCTIVE` (dependent objects
   must be dropped and recreated).

### 14.3. Operators

   **PG equivalent:**
   `CREATE OPERATOR symbol (LEFTARG = t, RIGHTARG = t, FUNCTION = func, [COMMUTATOR = op, NEGATOR = op, RESTRICT = func, JOIN = func, HASHES, MERGES])`

```sql
SCHEMA public {
    OPERATOR === (
        LEFTARG    = complex,
        RIGHTARG   = complex,
        PROCEDURE  = complex_eq,
        COMMUTATOR = ===,
        NEGATOR    = !==,
        RESTRICT   = eqsel,
        JOIN       = eqjoinsel,
        HASHES,
        MERGES
    );
}
```

   **Operator identity:** `(schema, symbol, leftarg_type, rightarg_type)`.

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New operator | `CREATE OPERATOR ...` | `SAFE` |
   | `PROCEDURE`/`FUNCTION` changed | `DROP OPERATOR; CREATE OPERATOR` | `DESTRUCTIVE` |
   | Optimizer hint changes (`RESTRICT`, `JOIN`, `COMMUTATOR`, `NEGATOR`, `HASHES`, `MERGES`) | `ALTER OPERATOR ... (...)` | `SAFE` |
   | Operator removed | `DROP OPERATOR symbol (leftarg, rightarg)` | `DESTRUCTIVE` |

### 14.4. Operator Classes and Families

   **PG equivalent (family):**
   `CREATE OPERATOR FAMILY name USING access_method`

   **PG equivalent (class):**
   `CREATE OPERATOR CLASS name [DEFAULT] FOR TYPE type USING access_method [FAMILY family] AS ...`

```sql
SCHEMA public {
    OPERATOR FAMILY my_family USING btree;

    OPERATOR CLASS my_ops USING btree FOR TYPE mytype (
        OPERATOR 1 <  ,
        OPERATOR 2 <= ,
        OPERATOR 3 =  ,
        OPERATOR 4 >= ,
        OPERATOR 5 >  ,
        FUNCTION 1 mytype_cmp(mytype, mytype)
    );
}
```

   **Diffing:** Identity is `(schema, name, access_method)`.  The
   body is diffed as normalised text (passthrough).  Any change to
   the member list requires drop + recreate (`DESTRUCTIVE`).

### 14.5. Casts

   **PG equivalent:**
   `CREATE CAST (source AS target) (WITH FUNCTION func | WITHOUT FUNCTION | WITH INOUT) [AS IMPLICIT | AS ASSIGNMENT]`

```sql
SCHEMA public {
    CAST (mytype AS TEXT)
        WITH FUNCTION mytype_to_text(mytype)
        AS IMPLICIT;

    CAST (TEXT AS mytype)
        WITH FUNCTION text_to_mytype(TEXT);
}
```

   **Cast identity:** `(source_type, target_type)`.

   **Diffing semantics:** PostgreSQL provides no `ALTER CAST`.  Any
   change to a cast requires `DROP CAST` followed by `CREATE CAST` —
   classified as `DESTRUCTIVE`.  The compiler MUST check for dependent
   objects before emitting `DROP CAST`.

### 14.6. Extended Statistics Objects

   **PG equivalent:**
   `CREATE STATISTICS [IF NOT EXISTS] name [(kinds)] ON col1, col2 [, ...] FROM table`

```sql
SCHEMA public {
    STATISTICS orders_stats (dependencies, ndistinct, mcv)
        ON customer_id, created_at
        FROM orders;
}
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New statistics object | `CREATE STATISTICS ...` | `SAFE` |
   | `statistics_target` changed | `ALTER STATISTICS name SET STATISTICS n` | `SAFE` |
   | Column list or kinds changed | `DROP STATISTICS; CREATE STATISTICS` | `DESTRUCTIVE` |
   | Object removed | `DROP STATISTICS name` | `SAFE` |

### 14.7. Tablespaces

   Tablespaces are cluster-level objects.

   **PG equivalent:**
   `CREATE TABLESPACE name [OWNER owner] LOCATION 'path'`

```sql
-- production/cluster/tablespaces.dpg

TABLESPACE fast_ssd LOCATION '/mnt/nvme/pg_data';
TABLESPACE archive  LOCATION '/mnt/hdd/pg_archive';
```

   **Diffing semantics:** `LOCATION` cannot be changed after creation.
   Any location change requires `DROP TABLESPACE` + `CREATE TABLESPACE`
   (`DESTRUCTIVE`).  Dropping a non-empty tablespace fails at the
   PostgreSQL level; the compiler classifies it as `DESTRUCTIVE` and
   additionally emits a warning comment noting that it will fail if
   any objects reside in the tablespace.

### 14.8. Foreign Data Wrappers

   In the common case FDWs are installed via extension.  The explicit
   declaration is reserved for custom C-implemented FDWs and is placed
   in the cluster objects directory.

   **PG equivalent:**
   `CREATE FOREIGN DATA WRAPPER name [HANDLER func] [VALIDATOR func] [OPTIONS (...)]`

```sql
FOREIGN DATA WRAPPER myfdw
    HANDLER   myfdw_handler
    VALIDATOR myfdw_validator;
```

   Any change to a FDW requires drop + recreate (`DESTRUCTIVE`).

### 14.9. Foreign Servers

   Foreign servers are database-level objects.

   **PG equivalent:**
   `CREATE SERVER [IF NOT EXISTS] name [TYPE 'type'] [VERSION 'version'] FOREIGN DATA WRAPPER fdw [OPTIONS (...)]`

```sql
SERVER analytics_warehouse
    FOREIGN DATA WRAPPER postgres_fdw
    OPTIONS (host 'warehouse.internal', dbname 'analytics', port '5432');
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New server | `CREATE SERVER ...` | `SAFE` |
   | OPTIONS changed | `ALTER SERVER name OPTIONS (SET key 'value', ...)` | `SAFE` |
   | FDW changed | Drop + recreate | `DESTRUCTIVE` |
   | Server removed | `DROP SERVER name [CASCADE]` | `DESTRUCTIVE` |

### 14.10. User Mappings

   User mappings associate a local PostgreSQL role with credentials for
   a foreign server.

   **PG equivalent:**
   `CREATE USER MAPPING [IF NOT EXISTS] FOR user SERVER server [OPTIONS (...)]`

```sql
USER MAPPING FOR app_service
    SERVER analytics_warehouse
    OPTIONS (user 'fdw_user', password 'env:FDW_PASSWORD');
```

   The password value in `OPTIONS` MUST use `env:VAR_NAME` syntax.
   Hardcoded passwords are rejected by the linter (rule:
   `hardcoded_fdw_password`).

   The compiler MUST NOT store the resolved password value in the
   snapshot; it stores only the env-URI key.

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New mapping | `CREATE USER MAPPING ...` | `SAFE` |
   | OPTIONS changed | `ALTER USER MAPPING FOR user SERVER server OPTIONS (...)` | `SAFE` |
   | Mapping removed | `DROP USER MAPPING FOR user SERVER server` | `SAFE` |

---

---

## 15. Compilation Pipeline

### 15.1. Phases Overview

   The DPG compilation pipeline processes source files through ten
   sequential phases.  Each phase is an independently composable
   component.  The reference implementation registers each phase
   implementation in `internal/pipeline.Registry`.

```
Phase 1:  File Discovery
Phase 2:  Macro Preprocessing
Phase 3:  Tokenization (Tokenizer)
Phase 4a: PG SQL Parsing (PGSQLParser)
Phase 4b: Block Parsing (BlockParser)
Phase 5:  IR Construction (IRBuilder)
Phase 6:  Merging (Merger)
Phase 7:  Dependency Resolution (DependencyResolver)
Phase 8:  Linting (Linter)       [dpg apply / dpg plan only]
Phase 9:  Differencing (Differ)
Phase 10: Emission (Emitter)
```

   Phases 4a and 4b operate in parallel on each raw object.  Phase 8
   (Linting) runs after Merging; its diagnostics are advisory by default
   (warnings), with `--strict` promoting them to hard errors.

### 15.2. Phase 1 — File Discovery

   Implements Section 3.6.  The output is an ordered list of
   `(cluster, database, []filepath)` tuples.  Files within a single
   database tuple are sorted lexicographically by full path.

### 15.3. Phase 2 — Macro Preprocessing

   The macro preprocessor performs two passes over each source file:

   **Pass 1 — Collection:** Scan for `MACRO name (body)` and
   `MACRO name {body}` declarations.  Record each macro's name, body
   type, and expanded text.  Emit error DPG-E007 if a macro declaration
   is found inside a block.  Emit error DPG-E011 if a name is
   redeclared.  Remove all `MACRO` declarations from the output text.

   **Pass 2 — Expansion:** Scan for `...name` spread operators.
   Expand each spread inline by substituting the recorded body text.
   Emit error DPG-E010 if a name is not found.  Emit error DPG-E008 or
   DPG-E009 if the body type does not match the context.

   Pass 2 is applied iteratively until no `...name` tokens remain, to
   handle macros whose bodies contain other spread operators.  If
   iteration does not converge (circular reference), emit DPG-E012.

   The output is a set of source files with all macros resolved and
   `MACRO` declarations removed.

### 15.4. Phase 3 — Tokenization

   The Tokenizer (interface `pipeline.Tokenizer`) scans the pre-
   processed source text and splits each complete DPG declaration into
   a `pipeline.RawObject` value containing:

   -   `Kind` — the `pipeline.ObjectKind` identified from the leading
       keyword(s).

   -   `Part1` — the raw Part 1 text, with the leading DPG keyword(s)
       stripped.  The PGSQLParser prepends the correct `CREATE` verb.

   -   `Part2` — the raw Part 2 `{ }` block text (the braces are
       stripped), or the empty string if absent.

   -   `Schema` — the name of the enclosing `SCHEMA` block if this
       declaration is nested inside one; otherwise empty.

   -   `Pos` — the `SourcePos` of the first token.

   The tokenizer MUST handle:

   -   Comments (`--` and `/* ... */`): stripped before keyword
       detection.  Line numbers MUST be preserved for `SourcePos`
       accuracy.

   -   Dollar-quoted strings: per the algorithm in Section 4.6.  The
       tokenizer MUST NOT interpret content inside dollar-quoted regions.

   -   Nested `{ }` blocks: the tokenizer MUST count brace depth
       correctly so that nested blocks (e.g., `MIGRATE REMOVE { }`,
       sub-partitions) are included in the correct Part 2.

   -   Schema blocks: a `SCHEMA name { ... }` block is tokenized as a
       container.  The objects inside it are tokenized with `Schema`
       set to the enclosing schema name.

   The tokenizer MUST emit error DPG-E006 upon encountering `CREATE`,
   `ALTER`, or `DROP` at brace depth 0 outside a dollar-quoted region.

### 15.5. Phase 4a — PG SQL Parsing

   The PGSQLParser (interface `pipeline.PGSQLParser`) takes a
   `RawObject.Part1` text and the `ObjectKind`, prepends the
   appropriate `CREATE [OR REPLACE]` verb, and invokes the PostgreSQL
   parser (via `github.com/pganalyze/pg_query_go/v5`, which wraps
   libpg_query — the same parser used by PostgreSQL itself).

   The result is a `pipeline.PGParseResult` holding the pg_query parse
   tree (a `*pg_query.ParseResult`).

   **Special cases:**

   -   `VIRTUAL TYPE` and `MACRO` are DPG-native and have no
       PostgreSQL `CREATE` equivalent.  The PGSQLParser returns a
       `PGParseResult` with `Kind` set to the appropriate object kind
       and `Raw` set to the raw Part1 text string (not a parse tree).

   -   The `SchemaContext` field of the returned `PGParseResult` is
       populated from `RawObject.Schema` for use by the IR Builder.

   The PGSQLParser MUST NOT modify the Part1 text in any way beyond
   prepending the verb.  If the PostgreSQL parser rejects the input,
   the PGSQLParser MUST propagate the parser error as a `CompilerError`
   at the source position.

   **Alternative parser:** A `NativeParser` (no CGo dependency) is
   provided for environments where libpg_query cannot be compiled.
   It supports a reduced feature set; use of the native parser MAY
   produce less accurate error messages and MUST be documented as
   a reduced-capability mode.

### 15.6. Phase 4b — Block Parsing

   The BlockParser (interface `pipeline.BlockParser`) takes a
   `RawObject.Part2` text and parses it into a `pipeline.BlockAST`.
   The `BlockAST` is a structured representation of all the directives
   in the `{ }` block.

   The block parser handles:

   -   All directives listed in the grammar for each object kind.
   -   Nested blocks (`INDICES { }`, `POLICIES { }`, `TRIGGERS { }`,
       `GRANTS { }`, `REVOCATIONS { }`, `PARTITIONS { }`, `COLUMNS { }`,
       `MIGRATE REMOVE { }`, `DEFAULT PRIVILEGES { }`).
   -   Spread operators (`...name`) that were not resolved in Phase 2
       (this should not happen; the preprocessor guarantees resolution,
       but the block parser MUST emit DPG-E010 if encountered).
   -   Unknown directives: emits error DPG-E024 (unknown block
       directive for this object kind).

### 15.7. Phase 5 — IR Construction

   The IRBuilder (interface `pipeline.IRBuilder`) takes a
   `(PGParseResult, BlockAST)` pair and produces a fully-resolved
   `pipeline.IRObject`.

   IR construction includes:

   -   Extracting all fields from the pg_query parse tree into the
       strongly-typed IR structs defined in `internal/ir/types.go`.

   -   Applying schema context: if a column type or a foreign key
       reference uses an unqualified name, the IR builder resolves it
       against the enclosing schema context established by
       `SchemaContext` or the database's `default_schema`.

   -   Applying PRIMARY KEY → NOT NULL inference (Section 7.2).

   -   Normalising constraint forms: inline single-column constraints
       are converted to their named table-level equivalents internally.
       The emitter converts them back to inline form in the output DDL.

   -   Computing function body hashes (SHA-256 of normalised body text,
       Section 9.5).

   -   Attaching `SourcePos` to every sub-object.

   The IR Builder MUST emit error DPG-E018 if a `COLUMN name { }`
   block references a column not present in the `( )` list.

### 15.8. Phase 6 — Merging

   The Merger (interface `pipeline.Merger`) accumulates all `IRObject`
   instances for the same database and merges declarations of the same
   logical object (same qualified name and kind) per the rules of
   Section 3.7.

   The Merger produces a flat, deduplicated list of `IRObject` values
   where each qualified name appears exactly once.

### 15.9. Phase 7 — Dependency Resolution

   The DependencyResolver (interface `pipeline.DependencyResolver`)
   performs a topological sort of the merged IR object list.

   **Edge creation rules** (object A depends on object B if):

   -   A column of A has a type defined by B (a user-defined type or
       domain in B's schema).
   -   A column of A has a `REFERENCES` constraint to table B.
   -   A view's query text references table or view B.
   -   A function's body references table or view B (if extractable).
   -   An index on A uses an operator class defined in B.
   -   A partition of A specifies B as its parent.

   **Circular dependency resolution:**

   When a cycle is detected:

   1.  If every FK in the cycle is `DEFERRABLE`, the resolver emits the
       tables in any order (all tables first, then circular FKs as
       `ALTER TABLE ADD CONSTRAINT ... DEFERRABLE INITIALLY DEFERRED`
       statements).

   2.  If any FK in the cycle is NOT `DEFERRABLE`, the resolver emits
       error DPG-E017 with the complete cycle path listed.

   The output is an ordered `[]IRObject` slice such that every object
   appears after all objects it depends on.

### 15.10. Phase 9 — Differencing

   The Differ (interface `pipeline.Differ`) takes:

   -   `desired []IRObject` — the output of Phase 7.
   -   `*Snapshot` — the committed snapshot (Section 16).

   It produces an ordered `[]DiffOp` representing the minimal set of
   DDL changes needed to transition the current state (snapshot) to the
   desired state.

   The differ performs three passes:

   **Pass 1 — Rename detection:** For each desired object with a
   non-empty `RenamedFrom` field, apply the rename resolution algorithm
   (Section 7.6, generalised to all renameable objects).  Renamed
   objects are removed from the snapshot under the old key and inserted
   under the new key for the purpose of subsequent diff passes.

   **Pass 2 — Object-level diff:** For each desired object:

   -   If absent from the snapshot: emit `CREATE ...` ops.
   -   If present in the snapshot: compare field-by-field and emit
       `ALTER ...` ops for each changed property.  Per-object diff
       algorithms are specified in Section 21.

   **Pass 3 — Deletion:** For each snapshot object absent from the
   desired state (and not consumed by a rename in Pass 1): emit
   `DROP ...` ops.  Objects with `PROTECTED = true` in their snapshot
   record are skipped with error DPG-E022 emitted instead.

   DiffOps are appended in the topological order established by Phase 7,
   with DELETE ops appended after all CREATE/ALTER ops in reverse
   topological order (dependents dropped before their dependencies).

### 15.11. Phase 10 — Emission

   The Emitter (interface `pipeline.Emitter`) splits the `[]DiffOp`
   into two groups:

   -   **Transactional:** `op.Transactional() == true` — wrapped in
       `BEGIN; ... COMMIT;` by the executor.

   -   **Non-transactional:** `op.Transactional() == false` — emitted
       after `COMMIT` and executed without a wrapping transaction.

   The Emitter returns a `pipeline.Migration` value (Section 17).

---

## 16. Snapshot Format

### 16.1. Purpose and Placement

   The snapshot is a committed JSON file that represents the compiler's
   normalised view of the database state after the most recent
   successful `dpg apply`.  It is the "current state" input to the
   Differ.

   The snapshot MUST be committed to version control.  Snapshots are
   not secrets (they contain no plaintext passwords; see Sections 11.1
   and 14.10).

   **Path:** `.dpg/snapshots/<cluster-name>/<database-name>.json`

   The snapshot directory is configurable via `[snapshots] directory`
   in the root `dpg.toml` (Section 3.2).

### 16.2. Top-Level Fields

```json
{
  "dpg_version":     "0.8.1",
  "cluster":         "production",
  "database":        "myapp",
  "applied_at":      "2025-09-15T14:32:00Z",
  "source_revision": "a3f7c91",
  "objects": { ... }
}
```

   | Field | Type | Description |
   |-------|------|-------------|
   | `dpg_version` | string | The DPG version that wrote this snapshot. |
   | `cluster` | string | The cluster name from the cluster `dpg.toml`. |
   | `database` | string | The database name from the database `dpg.toml`. Absent for cluster-level snapshots. |
   | `applied_at` | RFC 3339 string | UTC timestamp of the last successful `dpg apply`. |
   | `source_revision` | string | The git commit hash at apply time, if available. Empty if git is unavailable. |
   | `objects` | object | Map from `QualifiedName()` to per-object snapshot record. |

### 16.3. Per-Object Snapshot Schema

   Each entry in `objects` maps the object's `QualifiedName()` to a
   JSON object.  The `kind` field is REQUIRED on all entries.

   **Schema:**

```json
{
  "public.users": {
    "kind": "table",
    "schema": "public",
    "name": "users",
    "owner": "app_role",
    "comment": "Primary identity store",
    "rls_enabled": true,
    "rls_forced": false,
    "protected": false,
    "drop_cascade": false,
    "unlogged": false,
    "columns": { ... },
    "constraints": { ... },
    "indexes": { ... },
    "policies": { ... },
    "triggers": { ... },
    "grants": [ ... ]
  }
}
```

   **Column snapshot record:**

```json
"email": {
  "type": "text",
  "nullable": false,
  "default": null,
  "identity": null,
  "generated": null,
  "comment": "Verified email address",
  "statistics_target": 300,
  "compression": null,
  "storage": null,
  "grants": [
    { "grantee": "reporting_role", "privileges": ["SELECT"] }
  ]
}
```

   **Constraint snapshot record:**

```json
"pk_users": {
  "type": "PRIMARY KEY",
  "columns": ["id"],
  "not_valid": false,
  "deferrable": false,
  "initially_deferred": false
}
```

   **Index snapshot record:**

```json
"idx_users_email": {
  "unique": false,
  "method": "btree",
  "columns": [{ "name": "email", "direction": "asc", "nulls": null }],
  "include": [],
  "where": null,
  "with": {},
  "tablespace": null,
  "concurrently": true
}
```

   **Policy snapshot record:**

```json
"view_own": {
  "command": "SELECT",
  "permissive": true,
  "using": "user_id = auth.uid()",
  "with_check": null,
  "roles": []
}
```

   **Trigger snapshot record:**

```json
"after_email_change": {
  "when": "AFTER",
  "events": ["UPDATE"],
  "update_of": ["email"],
  "for_each": "ROW",
  "condition": "OLD.email IS DISTINCT FROM NEW.email",
  "function": "public.notify_email_change",
  "args": []
}
```

   **Function snapshot record:**

```json
"public.get_user(text)": {
  "kind": "function",
  "schema": "public",
  "name": "get_user",
  "args": [{ "name": "p_email", "type": "text", "mode": "IN" }],
  "return_type": "public.users",
  "language": "plpgsql",
  "volatility": "STABLE",
  "strict": false,
  "security_definer": true,
  "parallel": "UNSAFE",
  "body_hash": "sha256:a3f7c91d...",
  "comment": "Fetch a user record by verified email address",
  "grants": [{ "grantee": "app_service", "privileges": ["EXECUTE"] }]
}
```

   **Virtual type snapshot record:**

```json
"public.user_state": {
  "kind": "virtual_type",
  "schema": "public",
  "name": "user_state",
  "body": "\"active\" | \"suspended\" | \"deleted\"",
  "comment": null
}
```

   **Grant record (used in all per-object grant arrays):**

```json
{ "grantee": "app_readonly", "privileges": ["SELECT"], "with_grant": false }
```

   **Function body hash:** The `body_hash` field is the string
   `"sha256:"` followed by the lowercase hex-encoded SHA-256 digest of
   the normalised function body (Section 9.5).  The full body text is
   NOT stored in the snapshot; it lives in the `.dpg` source files.

### 16.4. Versioning

   The `dpg_version` field in the snapshot records the compiler version
   that wrote it.  Future major versions of DPG that change the snapshot
   schema MUST provide a migration path.  Minor-version changes MUST be
   backward compatible: a newer compiler MUST be able to read a snapshot
   written by an older minor version without data loss.

   The compiler MUST emit a warning when reading a snapshot whose
   `dpg_version` major component differs from the running compiler.

---

## 17. Migration Output Format

### 17.1. Output Structure

   The migration output is a plain-text SQL file (or stream) in a
   standardised format.  It consists of:

   1.  A **header block** with metadata comments.
   2.  A **transactional section** wrapped in `BEGIN;` / `COMMIT;`.
   3.  Optionally a **non-transactional section** outside any
       transaction block.

   When there are no changes, the output is the header block followed
   by `-- (no changes)`.

   **Header format:**

```sql
-- DPG Migration
-- Generated:       <RFC 3339 UTC timestamp>
-- Source revision: <git SHA or empty>
-- Cluster:         <cluster name>
-- Database:        <database name>
```

   **Per-operation annotations:**

   Each DiffOp is preceded by an annotation comment line:

```
-- [source: <file>:<line>[, safety: <class>]]
```

   The `source:` annotation is always present when the source position
   is known.  The `safety:` annotation is omitted for `SAFE` ops
   (since that is the expected normal case) and present for `CAUTION`,
   `DESTRUCTIVE`, and `MANUAL` ops.

   **Full example output:**

```sql
-- DPG Migration
-- Generated:       2025-09-15T14:32:00Z
-- Source revision: a3f7c91
-- Cluster:         production
-- Database:        myapp

-- transactional
BEGIN;

-- source: schemas/public/tables/users.dpg:4
CREATE TABLE public.users (
    id    BIGINT GENERATED ALWAYS AS IDENTITY CONSTRAINT "pk_users" PRIMARY KEY,
    email TEXT NOT NULL CONSTRAINT "uq_users_email" UNIQUE
);

-- source: schemas/public/tables/users.dpg:4
COMMENT ON TABLE public.users IS 'Primary identity store';

-- source: schemas/public/tables/users.dpg:12
COMMENT ON COLUMN public.users.email IS 'Verified email address';

-- source: schemas/public/tables/users.dpg:13
ALTER TABLE public.users ALTER COLUMN email SET STATISTICS 300;

-- source: schemas/public/tables/users.dpg:8
GRANT SELECT         ON TABLE public.users TO app_readonly;

-- source: schemas/public/tables/users.dpg:8
GRANT SELECT (email) ON TABLE public.users TO reporting_role;

COMMIT;

--------

-- non-transactional
-- source: schemas/public/tables/users.dpg:20, safety: MANUAL
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_users_email ON public.users (email);
```

   **Multi-database output (`RenderAll`):**

   When a single `dpg plan` or `dpg apply` targets multiple databases
   in the same cluster, the transactional section wraps ALL databases
   in a single `BEGIN`/`COMMIT` pair, with each database's ops
   introduced by a `-- Database: <name>` label.  The non-transactional
   section is structured the same way.

### 17.2. Safety Classification

   Every DiffOp MUST be assigned exactly one safety class.

   | Class | Criteria | Default behaviour |
   |-------|----------|-------------------|
   | `SAFE` | No data loss possible; no excessive locking. | Applied automatically. |
   | `CAUTION` | Acquires `ACCESS EXCLUSIVE` lock for non-trivial duration; or reorders data; or may affect query plans. | Applied with a warning logged to stderr. |
   | `DESTRUCTIVE` | Data loss is possible (DROP TABLE, DROP COLUMN, type change without USING, etc.). | Blocked unless `--allow-destructive` is passed to `dpg apply`. |
   | `MANUAL` | Cannot run inside a transaction (e.g., `CREATE INDEX CONCURRENTLY`, `ALTER TYPE ... ADD VALUE` on PG < 16); or requires a human-operator step (partition strategy change instruction). | Executable MANUAL ops are emitted after `COMMIT` in the non-transactional section. Instruction-only MANUAL ops (prefixed with `--`) are displayed in the plan but never executed. `--approve-partition-rebuild` is required to acknowledge instruction-only MANUAL ops. |

   The full per-operation safety classification table is:

   | Operation | Safety |
   |-----------|--------|
   | `CREATE TABLE` | `SAFE` |
   | `CREATE TABLE ... PARTITION OF ...` | `SAFE` |
   | `ALTER TABLE ADD COLUMN ... [NOT NULL] [DEFAULT ...]` | `SAFE` (PG 11+ DDL-only NOT NULL) |
   | `ALTER TABLE ALTER COLUMN TYPE` (implicit cast) | `CAUTION` |
   | `ALTER TABLE ALTER COLUMN TYPE ... USING` | `CAUTION` |
   | `ALTER TABLE ALTER COLUMN TYPE` (no implicit cast, no USING) | `DESTRUCTIVE` |
   | `ALTER TABLE DROP COLUMN` | `DESTRUCTIVE` |
   | `ALTER TABLE ADD CONSTRAINT ... NOT VALID` | `CAUTION` |
   | `ALTER TABLE VALIDATE CONSTRAINT` | `CAUTION` |
   | `ALTER TABLE RENAME COLUMN` | `CAUTION` |
   | `ALTER TABLE RENAME TO` | `CAUTION` |
   | `ALTER TABLE ENABLE ROW LEVEL SECURITY` | `SAFE` |
   | `ALTER TABLE DISABLE ROW LEVEL SECURITY` | `SAFE` |
   | `DROP TABLE` | `DESTRUCTIVE` |
   | `CREATE INDEX` (new table) | `SAFE` |
   | `CREATE INDEX` (existing table, not concurrent) | `CAUTION` |
   | `CREATE INDEX CONCURRENTLY` | `MANUAL` |
   | `DROP INDEX` | `CAUTION` |
   | `CREATE VIEW` | `SAFE` |
   | `CREATE OR REPLACE VIEW` | `SAFE` |
   | `DROP VIEW CASCADE` | `DESTRUCTIVE` |
   | `CREATE MATERIALIZED VIEW` | `SAFE` |
   | `DROP MATERIALIZED VIEW CASCADE` | `DESTRUCTIVE` |
   | `CREATE FUNCTION` / `CREATE OR REPLACE FUNCTION` | `SAFE` |
   | `DROP FUNCTION CASCADE` | `DESTRUCTIVE` |
   | `ALTER TYPE ... ADD VALUE` | `MANUAL` |
   | `DROP TYPE CASCADE` | `DESTRUCTIVE` |
   | `CREATE POLICY` | `SAFE` |
   | `DROP POLICY` | `SAFE` |
   | `CREATE TRIGGER` | `SAFE` |
   | `DROP TRIGGER` | `SAFE` |
   | `GRANT ...` | `SAFE` |
   | `REVOKE ...` | `SAFE` |
   | `CREATE SEQUENCE` | `SAFE` |
   | `DROP SEQUENCE` | `DESTRUCTIVE` |
   | `ALTER SCHEMA RENAME TO` | `CAUTION` |
   | `DROP SCHEMA CASCADE` | `DESTRUCTIVE` |
   | `DROP EXTENSION CASCADE` | `DESTRUCTIVE` |
   | `DROP ROLE` | `DESTRUCTIVE` |

### 17.3. Transactional vs Non-Transactional Steps

   A `DiffOp` is non-transactional (`Transactional() == false`) when:

   -   Its SQL is `CREATE INDEX CONCURRENTLY ...` (cannot run in a
       transaction block).
   -   Its SQL is `DROP INDEX CONCURRENTLY ...`.
   -   Its SQL is `ALTER TYPE ... ADD VALUE` (required for PostgreSQL
       versions below 16).
   -   Its safety class is `MANUAL` and it requires being outside a
       transaction.

   All other ops are transactional.

   The executor MUST execute all transactional ops within a single
   transaction per database.  If any transactional op fails, the
   entire transaction is rolled back.

   Non-transactional ops are executed sequentially after `COMMIT`,
   each as an individual statement outside any transaction.  If a
   non-transactional op fails, it does NOT roll back the already-
   committed transactional ops.  The operator MUST manually handle the
   partial failure.

### 17.4. Idempotency Requirement

   The idempotency guarantee is:

   > Running `dpg apply` on a database that already exactly matches
   > the desired state MUST produce zero SQL statements.

   Any violation of this guarantee is a defect in the DPG compiler.

   Idempotency is enforced through:

   -   The snapshot accurately reflecting the post-apply state.
   -   The Differ emitting ops only when the snapshot and desired state
       genuinely differ.
   -   `CREATE INDEX IF NOT EXISTS` in concurrent index creation.
   -   `CREATE EXTENSION IF NOT EXISTS` for extensions.

   If a `dpg apply` is interrupted partway through, the snapshot will
   not have been updated (the snapshot is written only upon full
   success).  On the next `dpg apply`, the transactional ops will be
   re-attempted.  The already-executed non-transactional ops (e.g.,
   concurrent index creations) will result in `IF NOT EXISTS` no-ops.

---

---

## 18. CLI Commands

### 18.1. dpg plan

   Computes the migration that would be applied and prints it to
   stdout.  No database connection is required by default.

```
dpg plan [options] [<cluster>[/<database>]]

Options:
  --live                 Diff against the live catalog instead of the
                         committed snapshot. Requires a database connection.
  --allow-destructive    Include DESTRUCTIVE operations in the output
                         (they are shown but still only printed, not applied).
  --format <fmt>         Output format: sql (default), json.
  --no-color             Disable ANSI colour annotations.
  --strict               Promote linter warnings to errors.
  --cluster <name>       Target a specific cluster (default: all clusters).
  --database <name>      Target a specific database (default: all databases).
```

   Exit codes: 0 = success (no changes); 1 = changes computed; 2 = error.

### 18.2. dpg apply

   Runs the linter, computes the migration, prompts for operator
   approval, executes the SQL, and updates the snapshot.

```
dpg apply [options] [<cluster>[/<database>]]

Options:
  --allow-destructive        Allow DESTRUCTIVE operations (required if any
                             exist; operator must confirm interactively unless
                             --yes is also set).
  --approve-partition-rebuild  Acknowledge MANUAL partition-rebuild steps.
  --yes / -y                 Skip interactive approval prompt (non-interactive
                             mode; implies --allow-destructive is acknowledged).
  --dry-run                  Compute and print the migration but do not execute.
  --no-snapshot              Do not update the snapshot after apply.
  --strict                   Promote linter warnings to errors. Apply is
                             blocked if any lint errors exist.
  --cluster <name>
  --database <name>
```

   **Apply procedure:**

   1.  Run the linter.  If any error-level diagnostics exist (or
       any warnings with `--strict`), abort and print diagnostics.
   2.  Compute the migration (`dpg plan`).
   3.  If `--dry-run`, print and exit.
   4.  If the migration contains `DESTRUCTIVE` ops and `--allow-destructive`
       is absent, abort with error DPG-E025.
   5.  Print the migration SQL.
   6.  Unless `--yes`, prompt: `Apply this migration? [y/N]`.  Abort if
       the operator does not confirm.
   7.  Execute transactional ops in a single `BEGIN`/`COMMIT` per
       database.
   8.  Execute non-transactional ops sequentially.
   9.  Update the snapshot atomically (write to a temp file, rename).
   10. Print `Migration applied successfully.` to stdout.

   If step 7 fails, the transaction is rolled back.  The snapshot is
   not updated.  The error from the database is printed with the
   failing SQL statement highlighted.

### 18.3. dpg verify

   Introspects the live database catalog and reports drift: any
   divergence between the snapshot and the live state.

```
dpg verify [options] [<cluster>[/<database>]]

Options:
  --cluster <name>
  --database <name>
  --format <fmt>     sql (default), json, text
```

   **Drift detection model:**

   -   **Reports:** any DPG-declared object property that is absent
       from or differs in the live catalog.

   -   **Reports:** any DPG-declared grant that is absent from the
       live catalog.

   -   **Does NOT report:** extra grants present in the live catalog
       but not declared in DPG source (additive model, Section 11.2).

   -   **Does NOT report:** objects present in the live catalog but
       absent from DPG source (unmanaged objects).

   Exit codes: 0 = no drift; 1 = drift detected; 2 = connection error.

### 18.4. dpg dump

   Introspects a live database catalog and produces an initial `.dpg`
   source tree and snapshot, suitable for bootstrapping a new DPG
   project from an existing database.

```
dpg dump [options]

Options:
  --cluster <name>   REQUIRED. The cluster to introspect.
  --database <name>  REQUIRED. The database to introspect.
  --out <dir>        Output directory for .dpg files (default: ./schemas).
  --overwrite        Overwrite existing .dpg files.
```

   The dump output is a best-effort conversion.  Objects whose DDL
   cannot be cleanly reconstructed from catalog information are emitted
   as comments with a `-- dpg:manual` marker.

### 18.5. dpg diff

   Diffs two DPG source directories targeting the same logical database,
   without requiring a snapshot or a live database connection.

```
dpg diff --from <dir> --to <dir> [options]

Options:
  --from <dir>         REQUIRED. "Before" source directory.
  --to <dir>           REQUIRED. "After" source directory.
  --format <fmt>       sql (default), json.
  --allow-destructive
```

   Both directories MUST contain a `dpg.toml` with a `[database]`
   section identifying the same logical database.

### 18.6. dpg validate

   Compiles and lints `.dpg` source files offline.  No snapshot or
   database connection required.

```
dpg validate [options]

Options:
  --strict           Promote linter warnings to errors.
  --format <fmt>     text (default), json.
```

   Exit codes: 0 = no errors; 1 = errors found; 2 = internal error.

   With `--format json` the output is a JSON array of diagnostic
   objects:

```json
[
  {
    "file": "schemas/public/tables/users.dpg",
    "line": 12,
    "col": 5,
    "rule": "hardcoded_password",
    "message": "Role password must use env:VAR_NAME syntax",
    "is_error": true
  }
]
```

### 18.7. dpg fmt

   Reformats `.dpg` source files in place according to a canonical style.

```
dpg fmt [options] [<file> ...]

Options:
  --check    Exit with code 1 if any file would be reformatted.
             Does not modify files. Useful as a CI gate.
  --diff     Print a unified diff of the proposed reformatting.
             Does not modify files.
```

   Canonical style rules:

   -   Indentation: 4 spaces.
   -   Keyword casing: uppercase for all DPG and PostgreSQL keywords.
   -   Identifier casing: unquoted identifiers are lowercased.
   -   Column alignment: column names and types are aligned in `( )` lists.
   -   Trailing whitespace: stripped.
   -   Blank lines: one blank line between top-level declarations.
   -   Comment style: `--` for single-line, `/* */` for multi-line.

### 18.8. dpg portability

   Reports all PostgreSQL-specific constructs in use with SQL standard
   alternatives noted where available.

```
dpg portability [options]

Options:
  --format <fmt>   text (default), json.
```

   This command is OPTIONAL; it MUST NOT be a compilation gate.

### 18.9. dpg init

   Scaffolds a new project with the standard directory layout and
   `dpg.toml` files.

```
dpg init [options] [<dir>]

Options:
  --cluster <name>   Cluster name (default: "main").
  --database <name>  Database name (default: "myapp").
  --schema <name>    Default schema (default: "public").
```

### 18.10. dpg completion

   Generates shell completion scripts.

```
dpg completion <shell>

<shell>: bash | zsh | fish | powershell
```

---

## 19. The Linter

### 19.1. Built-in Rules

   | Rule ID | Description | Default Level |
   |---------|-------------|---------------|
   | `hardcoded_password` | `ROLE PASSWORD 'literal'` detected. Use `env:VAR_NAME`. | Error |
   | `hardcoded_fdw_password` | `USER MAPPING OPTIONS (password 'literal')` detected. | Error |
   | `deprecated_reference` | A non-deprecated object references a deprecated object or column. | Warning |
   | `missing_column_comment` | A column has no `COMMENT` and `require_column_comments = true`. | Warning (configurable to Error) |
   | `column_count_exceeded` | A table has more columns than `max_columns_per_table`. | Warning |
   | `scalar_merge_conflict` | Two files provide conflicting scalar values for the same object property. | Warning |
   | `security_definer_search_path` | A `SECURITY DEFINER` function has no explicit `SET search_path`. | Warning |
   | `serial_sequence_declared` | A sequence is declared with a name matching an auto-managed `SERIAL`/`IDENTITY` sequence. | Warning |
   | `unnecessary_revocation` | A revocation targets a role that was never granted the privilege by DPG. | Warning |
   | `stale_renamed_from` | `RENAMED FROM` directive references a name not in the snapshot (DPG-E021). | Error |
   | `unguarded_enum_removal` | An ENUM value is removed without a `MIGRATE REMOVE` block. | Error |
   | `protected_drop_attempt` | The diff would drop a `PROTECTED` object (DPG-E022). | Error |

### 19.2. Configuration

   All linter rules are configurable in the root `dpg.toml`
   `[linter]` section.  The `warn_on_deprecated`, `require_column_comments`,
   `forbid_hardcoded_passwords`, `max_columns_per_table`, and
   `warn_on_scalar_merge_conflict` fields are described in Section 3.2.

   Individual rules MAY be set to `"error"`, `"warning"`, or `"off"`
   via `[linter.rules]`:

```toml
[linter.rules]
security_definer_search_path = "error"
serial_sequence_declared      = "off"
```

---

## 20. Introspection Engine

### 20.1. Catalog Tables Read

   The introspection engine (interface `pipeline.Introspector`)
   connects to a live PostgreSQL 14+ catalog and reads the following
   system tables and views:

   | Catalog object | Used for |
   |----------------|----------|
   | `pg_class` | Tables, views, materialized views, sequences, indexes |
   | `pg_attribute` | Column definitions (incl. `attstattarget`, `attcompression`, `attstorage`) |
   | `pg_constraint` | Table constraints |
   | `pg_index` | Index definitions |
   | `pg_proc` | Functions, procedures, aggregates |
   | `pg_trigger` | Trigger definitions |
   | `pg_policy` | Row security policies |
   | `pg_type` | Types (ENUMs, composites, domains, ranges, base) |
   | `pg_enum` | ENUM values |
   | `pg_namespace` | Schemas |
   | `pg_extension` | Installed extensions |
   | `pg_publication` | Publications |
   | `pg_subscription` | Subscriptions |
   | `pg_foreign_table` | Foreign tables |
   | `pg_foreign_server` | Foreign servers |
   | `pg_user_mapping` | User mappings |
   | `pg_foreign_data_wrapper` | Foreign data wrappers |
   | `pg_statistic_ext` | Extended statistics objects |
   | `pg_event_trigger` | Event triggers |
   | `pg_collation` | Collations |
   | `pg_operator` | Operators |
   | `pg_opclass` | Operator classes |
   | `pg_opfamily` | Operator families |
   | `pg_cast` | Casts |
   | `pg_partitioned_table` | Partitioning metadata |
   | `pg_inherits` | Table inheritance |
   | `pg_sequence` | Sequence parameters |
   | `pg_ts_config` | Text search configurations |
   | `pg_ts_dict` | Text search dictionaries |
   | `pg_ts_parser` | Text search parsers |
   | `pg_ts_template` | Text search templates |
   | `information_schema.column_privileges` | Column-level grants |
   | `pg_roles` (or `pg_authid`) | Roles and role memberships |
   | `pg_tablespace` | Tablespaces |

### 20.2. Drift Detection

   The `dpg verify` command compares the snapshot with the live catalog:

   1.  Introspect the live catalog → produce a `[]IRObject` of live state.
   2.  Load the committed snapshot → produce a `[]IRObject` of snapshot state.
   3.  Compute the diff between snapshot state (desired) and live state
       (current) — i.e., treat the snapshot as the "desired" input and
       the live catalog as the "snapshot" input to the Differ.
   4.  Any non-empty DiffOps represent drift.

   **Grant drift:** Report as drift any DPG-declared grant absent from
   the live catalog.  Do NOT report extra grants present in the live
   catalog.

---

## 21. Per-Object Diff Algorithms

   This section specifies, for each object type, the precise field
   comparison performed by the Differ and the DDL emitted for each
   type of change.

   **TABLE:**

   | Field | Change detection | DDL | Safety |
   |-------|-----------------|-----|--------|
   | Columns (added) | New col name not in snapshot | `ALTER TABLE t ADD COLUMN c TYPE [DEFAULT ...]` | `SAFE` |
   | Columns (dropped) | Col name in snapshot, absent in desired | `ALTER TABLE t DROP COLUMN c` | `DESTRUCTIVE` |
   | Column type changed | `TypeRef.String()` differs | `ALTER TABLE t ALTER COLUMN c TYPE newtype [USING expr]` | `CAUTION` or `DESTRUCTIVE` |
   | Column NOT NULL added | `nullable` false→true (was true in snap) | `ALTER TABLE t ALTER COLUMN c SET NOT NULL` | `CAUTION` |
   | Column NOT NULL removed | `nullable` true→false | `ALTER TABLE t ALTER COLUMN c DROP NOT NULL` | `SAFE` |
   | Column DEFAULT added | `default` was nil | `ALTER TABLE t ALTER COLUMN c SET DEFAULT expr` | `SAFE` |
   | Column DEFAULT changed | `default` text differs | `ALTER TABLE t ALTER COLUMN c SET DEFAULT expr` | `SAFE` |
   | Column DEFAULT removed | `default` is nil | `ALTER TABLE t ALTER COLUMN c DROP DEFAULT` | `SAFE` |
   | Column statistics | `statistics_target` differs | `ALTER TABLE t ALTER COLUMN c SET STATISTICS n` | `SAFE` |
   | Column compression | `compression` differs | `ALTER TABLE t ALTER COLUMN c SET COMPRESSION m` | `SAFE` |
   | Column storage | `storage` differs | `ALTER TABLE t ALTER COLUMN c SET STORAGE s` | `SAFE` |
   | Column comment | `comment` differs | `COMMENT ON COLUMN t.c IS '...'` | `SAFE` |
   | Constraint added | Name absent in snapshot | `ALTER TABLE t ADD CONSTRAINT name ...` | `CAUTION` |
   | Constraint dropped | Name absent in desired | `ALTER TABLE t DROP CONSTRAINT name` | `DESTRUCTIVE` |
   | Constraint changed | Body text differs | Drop + re-add | `DESTRUCTIVE` |
   | NOT VALID removed | `not_valid` false in desired | `ALTER TABLE t VALIDATE CONSTRAINT name` | `CAUTION` |
   | Index added (existing table) | Name absent in snapshot | `CREATE [UNIQUE] INDEX [CONCURRENTLY] ...` | `MANUAL` or `CAUTION` |
   | Index dropped | Name absent in desired | `DROP INDEX [CONCURRENTLY] name` | `CAUTION` |
   | Index changed | Any field differs | Drop + recreate | `CAUTION`/`MANUAL` |
   | RLS enabled | `rls_enabled` changed | `ALTER TABLE t ENABLE ROW LEVEL SECURITY` | `SAFE` |
   | RLS disabled | `rls_enabled` changed | `ALTER TABLE t DISABLE ROW LEVEL SECURITY` | `SAFE` |
   | Policy added | Name absent in snapshot | `CREATE POLICY name ON t ...` | `SAFE` |
   | Policy changed | Any field differs | Drop + recreate | `SAFE` |
   | Policy dropped | Name absent in desired | `DROP POLICY name ON t` | `SAFE` |
   | Trigger added | Name absent in snapshot | `CREATE TRIGGER name ...` | `SAFE` |
   | Trigger changed | Any field differs | Drop + recreate | `SAFE` |
   | Trigger dropped | Name absent in desired | `DROP TRIGGER name ON t` | `SAFE` |
   | Grant added | Not in snapshot grant list | `GRANT privs ON TABLE t TO role` | `SAFE` |
   | Owner changed | `owner` differs | `ALTER TABLE t OWNER TO role` | `SAFE` |
   | Comment changed | `comment` differs | `COMMENT ON TABLE t IS '...'` | `SAFE` |
   | Table renamed | `renamed_from` set | `ALTER TABLE old RENAME TO new` | `CAUTION` |
   | Table dropped | Absent in desired, not PROTECTED | `DROP TABLE t [CASCADE]` | `DESTRUCTIVE` |

   **FUNCTION / PROCEDURE:**

   | Field | Change | DDL | Safety |
   |-------|--------|-----|--------|
   | New function | Absent in snapshot | `CREATE OR REPLACE FUNCTION ...` | `SAFE` |
   | Body hash changed | `body_hash` differs | `CREATE OR REPLACE FUNCTION ...` | `SAFE` |
   | Attribute changed (volatility, strict, security, parallel, cost, rows, set options) | Field differs | `CREATE OR REPLACE FUNCTION ...` | `SAFE` |
   | Argument list or return type changed | Type key differs | `DROP FUNCTION CASCADE; CREATE FUNCTION` | `DESTRUCTIVE` |
   | Grant added | Not in snapshot | `GRANT EXECUTE ON FUNCTION ...` | `SAFE` |
   | Comment changed | `comment` differs | `COMMENT ON FUNCTION ...` | `SAFE` |
   | Function dropped | Absent in desired | `DROP FUNCTION name(...) [CASCADE]` | `DESTRUCTIVE` |

   **VIEW:**

   | Change | DDL | Safety |
   |--------|-----|--------|
   | New view | `CREATE VIEW ...` | `SAFE` |
   | Query changed, same column list | `CREATE OR REPLACE VIEW ...` | `SAFE` |
   | Column list changed | `DROP VIEW CASCADE; CREATE VIEW` | `DESTRUCTIVE` |
   | View dropped | `DROP VIEW CASCADE` | `DESTRUCTIVE` |

   **ENUM:**

   | Change | DDL | Safety |
   |--------|-----|--------|
   | New value | `ALTER TYPE name ADD VALUE 'v'` | `MANUAL` |
   | Value removed (guarded) | MIGRATE REMOVE procedure (§5.1.2) | `DESTRUCTIVE` |
   | Value removed (unguarded) | Error DPG-E014 (or with `--allow-destructive`) | `DESTRUCTIVE` |
   | Comment changed | `COMMENT ON TYPE name IS '...'` | `SAFE` |

   **SEQUENCE:**

   | Change | DDL | Safety |
   |--------|-----|--------|
   | New sequence | `CREATE SEQUENCE ...` | `SAFE` |
   | Numeric parameters changed | `ALTER SEQUENCE name [INCREMENT BY n] [MINVALUE n] ...` | `SAFE` |
   | `AS type` changed | Drop + recreate | `DESTRUCTIVE` |
   | Sequence dropped | `DROP SEQUENCE name` | `DESTRUCTIVE` |

   **ROLE:**

   | Change | DDL | Safety |
   |--------|-----|--------|
   | New role | `CREATE ROLE name WITH ...` | `SAFE` |
   | Any option changed | `ALTER ROLE name WITH [options]` | `SAFE` |
   | Role dropped | `DROP ROLE name` | `DESTRUCTIVE` |

---

## 22. Dependency Ordering

### 22.1. Topological Sort

   The dependency resolver builds a directed acyclic graph (DAG) where
   nodes are `IRObject` values and a directed edge from A to B means
   "A depends on B" (B must be created before A).

   **Edge sources:**

   1.  A table column whose type is a user-defined type or domain
       creates an edge from the table to the type/domain.

   2.  A `REFERENCES` FK constraint creates an edge from the source
       table to the target table.

   3.  A view's query that mentions table or view B creates an edge
       from the view to B.

   4.  A function's `search_path` or `SECURITY DEFINER` context
       creates an edge from the function to the schema.

   5.  An index that uses a custom operator class creates an edge from
       the index (and transitively its table) to the operator class.

   6.  A trigger function reference creates an edge from the table to
       the trigger function.

   7.  A domain whose base type is a user-defined type creates an edge
       from the domain to the type.

   8.  A partition creates an edge from the partition to its parent
       partitioned table.

   The topological sort MUST use Kahn's algorithm or an equivalent
   O(V + E) algorithm.  The sort is deterministic: among nodes with no
   remaining incoming edges, the one with the lexicographically smallest
   `QualifiedName()` is selected first.

### 22.2. Circular Dependency Resolution

   When the dependency graph contains a cycle, the resolver applies the
   following procedure:

   1.  Identify all strongly connected components (SCCs) using
       Tarjan's algorithm.

   2.  For each SCC with more than one node:

       a.  Verify that every FK edge within the SCC is `DEFERRABLE`.
           If any non-deferrable FK is found, emit error DPG-E017
           listing all nodes in the cycle.

       b.  Remove the circular FK edges from the graph (these will be
           emitted as `ALTER TABLE ADD CONSTRAINT ... DEFERRABLE`
           after all tables in the SCC are created).

       c.  Re-run the topological sort on the cycle-free graph.

   3.  After the topological sort, append `ALTER TABLE ADD CONSTRAINT`
       ops for all deferred circular FKs.

---

## 23. Deferred Features

   The following features are formally out of scope for this version
   of the specification.  They are documented here to establish the
   intended direction for future versions.

   **Inline data seeding (`SEED { }` blocks):**
   Deferred to a future `dpg-seed` extension specification.  Inline
   seeding blurs the boundary between schema management and data
   management and requires careful specification of merge semantics,
   idempotency, and truncate-vs-upsert strategies.

   **Minimum PostgreSQL version targeting:**
   Planned for v1.1.  The compiler's internal portability annotation
   infrastructure is already in place.  Per-object version gating will
   allow users to declare `MIN_PG_VERSION = 15` and have the compiler
   omit or adapt DDL for features not available on older servers.

   **Cross-file macro sharing:**
   Currently `MACRO` definitions are file-scoped.  A future version
   will add a global macro registry so shared column sets can live in
   a dedicated `macros.dpg` file and be imported by any other file.

   **Rule (REWRITE) objects:**
   PostgreSQL `CREATE RULE` is a legacy feature superseded by triggers
   and updatable views.  DPG explicitly does not manage rules.

   **`IMPORT FOREIGN SCHEMA`:**
   Runtime discovery operation; not appropriate for declarative schema
   management.

   **`REFRESH MATERIALIZED VIEW`:**
   Runtime DML operation; out of scope.

   **Temporary tables:**
   Session-scoped; cannot be meaningfully managed by a schema tool.

---

## 24. Security Considerations

   **Secret handling:** DPG MUST NOT store plaintext secret values in
   any persisted file.  This includes:

   -   Role passwords: the snapshot stores only a boolean `has_password`.
   -   FDW / user mapping passwords: the snapshot stores the `env:` URI,
       not the resolved value.
   -   Connection strings in `dpg.toml`: if `link = "env:VAR"` is used,
       the resolved value is never written to disk.  If `url =` is used,
       the connection string may contain embedded credentials and SHOULD
       NOT be committed to a public repository; this is the operator's
       responsibility.

   **SQL injection in generated DDL:** All identifier names read from
   source files are validated against PostgreSQL's identifier rules
   before being interpolated into generated SQL.  The compiler MUST
   quote all identifiers using PostgreSQL's double-quote quoting
   (`"identifier"`) in generated DDL to prevent injection via crafted
   identifier names.

   **SECURITY DEFINER functions:** The linter warns on `SECURITY
   DEFINER` functions lacking explicit `SET search_path` to mitigate
   search path injection attacks (rule: `security_definer_search_path`).

   **Snapshot integrity:** The snapshot is a plain JSON file.  It MUST
   be committed to version control to prevent tampering.  An attacker
   with write access to the snapshot could cause the differ to omit
   real changes or generate incorrect migrations.  Snapshot integrity
   SHOULD be enforced via the same commit signing and branch protection
   mechanisms applied to source code.

   **Privilege escalation via DEFAULT PRIVILEGES:** `ALTER DEFAULT
   PRIVILEGES` grants can confer privileges on future objects.  Operators
   SHOULD review all `DEFAULT PRIVILEGES` declarations carefully before
   applying.

   **`DROP ... CASCADE`:** The `DROP CASCADE` directive and the
   `default_drop_behavior = "cascade"` setting cause the compiler to
   emit `DROP ... CASCADE`, which silently drops all dependent objects.
   Operators MUST review `DESTRUCTIVE` ops carefully.  DPG's safety
   classification system exists precisely to make this review tractable.

---

## 25. Feature Coverage Matrix

   **Legend:**
   - **Declared** — DPG syntax fully specified in this document.
   - **Diffed** — The compiler computes structured per-field changes and emits precise DDL.
   - **Passthrough** — Treated as opaque text; diffed by text equality only.
   - **No SQL** — DPG-native; generates no PostgreSQL DDL.
   - **Out of scope** — Not managed by DPG.
   - **Deferred** — Explicitly out of scope for this version; planned.

   | Feature | Status | Notes |
   |---------|--------|-------|
   | Tables (regular) | Declared, Diffed | Full per-field diff |
   | Tables (unlogged) | Declared, Diffed | `UNLOGGED` prefix |
   | Tables (foreign) | Declared, Diffed | `SERVER`/`OPTIONS` after `)` |
   | Tables (temporary) | Out of scope | Session-scoped |
   | Columns — all built-in types | Declared, Diffed | In `()` list |
   | Columns — generated (`ALWAYS AS`) | Declared, Diffed | In `()` list |
   | Columns — identity (`AS IDENTITY`) | Declared, Diffed | In `()` list |
   | Column `COMPRESSION` | Declared, Diffed | `COLUMN c { COMPRESSION m; }` |
   | Column `STORAGE` | Declared, Diffed | `COLUMN c { STORAGE s; }` |
   | Column statistics targets | Declared, Diffed | `COLUMN c { STATISTICS n; }` |
   | Column comments | Declared, Diffed | `COLUMN c { COMMENT "..."; }` |
   | Column `DEPRECATED` | Declared, Diffed | `COLUMN c { DEPRECATED "..."; }` |
   | Column `USING` (type change) | Declared, Diffed | `COLUMN c { USING expr; }` |
   | Column renames | Declared, Diffed | `COLUMN new { RENAMED FROM old; }` |
   | Column-level grants | Declared, Diffed | `COLUMN c { GRANTS { ... } }` |
   | Column-level revocations | Declared, Diffed | `COLUMN c { REVOCATIONS { ... } }` |
   | Inline constraints (PK, UNIQUE, CHECK, FK) | Declared, Diffed | Single-column emitted inline |
   | Named constraints in `()` list | Declared, Diffed | Emitted inline for single-column |
   | Constraints in `{}` block | Declared, Diffed | `NOT VALID` required here |
   | `EXCLUSION` constraints | Declared, Diffed | In `()` or `{}` block |
   | `NOT VALID` / `VALIDATE CONSTRAINT` | Declared, Diffed | Multi-migration lifecycle |
   | Indexes — all access methods | Declared, Diffed | btree, hash, gin, gist, brin, spgist, bloom |
   | Indexes — partial | Declared, Diffed | `WHERE` predicate as text |
   | Indexes — expression | Declared, Diffed | Expression as text |
   | Indexes — covering (`INCLUDE`) | Declared, Diffed | Drop + recreate on change |
   | Indexes — concurrent creation | Declared, Manual | `CREATE INDEX CONCURRENTLY` |
   | ENUM types | Declared, Diffed | `MIGRATE REMOVE` for value removal |
   | Composite types | Declared, Diffed | |
   | Range types | Declared, Diffed | Any change = DESTRUCTIVE |
   | Domain types | Declared, Diffed | |
   | Base (shell) types | Declared, Passthrough | |
   | Virtual types | Declared, No SQL | DPG-native; snapshot only |
   | Views | Declared, Diffed | Column list change = DESTRUCTIVE |
   | Materialized views | Declared, Diffed | Query change = DESTRUCTIVE |
   | Recursive views | Declared, Diffed | |
   | Functions — all languages | Declared, Passthrough body | Body hash-diffed |
   | Procedures | Declared, Passthrough body | |
   | Aggregates | Declared, Diffed | Option change = DESTRUCTIVE |
   | Window functions | Declared, Passthrough body | |
   | Row Level Security | Declared, Diffed | |
   | Triggers | Declared, Diffed | |
   | Event triggers | Declared, Diffed | |
   | Sequences | Declared, Diffed | |
   | Schemas | Declared, Diffed | |
   | Extensions | Declared, Diffed | |
   | Roles | Declared, Diffed | Cluster-level |
   | Table-level grants | Declared, Diffed | Additive model |
   | Column-level grants | Declared, Diffed | Additive model |
   | Explicit revocations | Declared, Diffed | |
   | Default Privileges | Declared, Diffed | |
   | Tablespaces | Declared, Diffed | Cluster-level |
   | Foreign Data Wrappers | Declared, Diffed | Cluster-level |
   | Foreign Servers | Declared, Diffed | |
   | User Mappings | Declared, Diffed | |
   | Foreign Tables | Declared, Diffed | |
   | Partitioned Tables | Declared, Diffed | |
   | Sub-partitioning | Declared, Diffed | |
   | Publications | Declared, Diffed | |
   | Subscriptions | Declared, Diffed | |
   | Collations | Declared, Diffed | Change = DESTRUCTIVE |
   | Operators | Declared, Diffed | PROCEDURE change = DESTRUCTIVE |
   | Operator Classes / Families | Declared, Passthrough | |
   | Casts | Declared, Diffed | Any change = DESTRUCTIVE |
   | Extended Statistics Objects | Declared, Diffed | |
   | Text Search Configurations | Declared, Diffed | |
   | Text Search Dictionaries | Declared, Diffed | |
   | Text Search Parsers | Declared, Diffed | |
   | Text Search Templates | Declared, Diffed | |
   | Macro preprocessor | Declared, No SQL | Compile-time text expansion |
   | Rules (REWRITE) | Out of scope | Legacy |
   | `IMPORT FOREIGN SCHEMA` | Out of scope | Runtime discovery |
   | `REFRESH MATERIALIZED VIEW` | Out of scope | Runtime DML |
   | Temporary tables | Out of scope | Session-scoped |
   | Inline data seeding | Deferred | See §23 |
   | Minimum PG version targeting | Deferred | See §23; planned v1.1 |
   | Cross-file macro sharing | Deferred | See §23 |

---

## Appendix A. ABNF Grammar Summary

   The following is a consolidated summary of all ABNF productions
   defined throughout this document.  Individual productions were
   specified inline in their respective sections.  This appendix
   collects them for reference.

```abnf
; Top-level source file
dpg-file = *( WSP / comment / macro-decl / top-level-decl )

top-level-decl = schema-decl
               / extension-decl
               / role-decl
               / tablespace-decl
               / fdw-decl
               / publication-decl
               / subscription-decl
               / event-trigger-decl
               / default-privileges-decl

; Common terminals
WSP      = *( SP / HTAB / CRLF / LF )
SQUOTE   = %x27                        ; single quote '
DQUOTE   = %x22                        ; double quote "
integer  = [ "-" ] 1*DIGIT
boolean  = "true" / "false"
expr     = <arbitrary SQL expression text>
qual-name = identifier *( "." identifier )
schema-name = identifier *( "." identifier )

; Identifier
identifier = unquoted-id / quoted-id
unquoted-id = ( ALPHA / "_" ) *( ALPHA / DIGIT / "_" / "$" )
quoted-id   = DQUOTE *( safe-char / DQUOTE DQUOTE ) DQUOTE
safe-char   = <any Unicode character except DQUOTE>

; Common directives
owner-dir        = "OWNER" WSP DQUOTE identifier DQUOTE
comment-dir      = "COMMENT" WSP DQUOTE <text> DQUOTE
renamed-from-dir = "RENAMED FROM" WSP identifier
protected-dir    = "PROTECTED"
deprecated-dir   = "DEPRECATED" WSP DQUOTE <text> DQUOTE
drop-cascade-dir = "DROP CASCADE"

; Dollar-quoted string
dollar-string = dollar-delim *<any byte> dollar-delim
dollar-delim  = "$" *( ALPHA / DIGIT / "_" ) "$"

; Type reference
type-ref  = qual-name [ "(" type-mods ")" ] *( "[]" )
type-mods = integer *( "," integer )

; Grants
grants-block      = "GRANTS" WSP "{" *( grant-entry ";" ) "}"
revocations-block = "REVOCATIONS" WSP "{" *( revoke-entry ";" ) "}"
grant-entry  = privilege-list WSP "TO" WSP role-list
               [ WSP "WITH GRANT OPTION" ]
revoke-entry = ( privilege-list / "ALL PRIVILEGES" ) WSP
               "FROM" WSP role-list [ WSP "CASCADE" ]
privilege-list = privilege *( "," WSP privilege )
privilege = "SELECT" / "INSERT" / "UPDATE" / "DELETE" / "TRUNCATE" /
            "REFERENCES" / "TRIGGER" / "USAGE" / "EXECUTE" / "CREATE" /
            "CONNECT" / "TEMPORARY" / "ALL" / "ALL PRIVILEGES"
role-list  = identifier *( "," WSP identifier )

; Macro preprocessor
macro-decl  = "MACRO" WSP identifier WSP ( paren-body / brace-body )
paren-body  = "(" *column-def ")"
brace-body  = "{" *( block-directive ";" ) "}"
spread      = "..." identifier

; See individual sections for all other productions.
```

---

## Appendix B. Complete Example Project

   This appendix shows a complete, coherent DPG project for a
   multi-tenant SaaS application.

```toml
# dpg.toml
[compiler]
default_drop_behavior = "restrict"
concurrent_indexes    = true

[linter]
warn_on_deprecated           = true
forbid_hardcoded_passwords   = true
max_columns_per_table        = 50
warn_on_scalar_merge_conflict = true

[snapshots]
directory = ".dpg/snapshots"
```

```toml
# production/dpg.toml
[cluster]
name                = "production"
cluster_objects_dir = "cluster"
link                = "env:PRODUCTION_DB_URL"

[cluster.options]
snapshot_on_apply = true
```

```toml
# production/myapp/dpg.toml
[database]
name           = "myapp"
default_schema = "public"
```

```sql
-- production/cluster/roles.dpg

ROLE app_service  {
    LOGIN;
    PASSWORD 'env:APP_SERVICE_PW';
    CONNECTION LIMIT 20;
}
ROLE app_readonly { NOLOGIN; }
ROLE app_admin    { LOGIN; SUPERUSER false; CREATEDB false; INHERIT; }
```

```sql
-- production/myapp/extensions.dpg

EXTENSION pgcrypto;
EXTENSION pg_trgm CASCADE;
```

```sql
-- production/myapp/schemas/public/types.dpg

SCHEMA public {
    ENUM account_status ('trial', 'active', 'suspended', 'cancelled');
    {
        COMMENT "Top-level account lifecycle states";
    }

    ENUM invoice_status ('draft', 'sent', 'paid', 'void', 'overdue');
    {
        COMMENT "Billing lifecycle states for customer invoices";
    }

    DOMAIN positive_money AS NUMERIC(12, 2) {
        CONSTRAINT must_be_positive CHECK (VALUE >= 0);
    }
}
```

```sql
-- production/myapp/schemas/public/tables/accounts.dpg

MACRO audit_timestamps (
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
)

SCHEMA public {
    TABLE accounts (
        id     UUID           NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
        name   TEXT           NOT NULL,
        status account_status NOT NULL DEFAULT 'trial',
        ...audit_timestamps
    )
    {
        COMMENT "Top-level tenant accounts";
        ENABLE ROW LEVEL SECURITY;

        COLUMN status     { STATISTICS 300; }
        COLUMN created_at { STATISTICS 200; }

        INDICES {
            idx_accounts_status (status) WHERE (deleted_at IS NULL);
        }

        POLICIES {
            isolate_tenants FOR ALL
                USING (id = current_setting('app.account_id')::UUID);
        }

        GRANTS {
            SELECT, INSERT, UPDATE TO app_service;
            SELECT                 TO app_readonly;
        }

        REVOCATIONS {
            ALL PRIVILEGES FROM PUBLIC;
        }
    }
}
```

```sql
-- production/myapp/schemas/public/tables/invoices.dpg

SCHEMA public {
    TABLE invoices (
        id         UUID           NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
        account_id UUID           NOT NULL,
        status     invoice_status NOT NULL DEFAULT 'draft',
        total      positive_money NOT NULL DEFAULT 0,
        issued_at  TIMESTAMPTZ,
        due_at     TIMESTAMPTZ,
        created_at TIMESTAMPTZ    NOT NULL DEFAULT now(),
        CONSTRAINT fk_account FOREIGN KEY (account_id)
            REFERENCES accounts (id) ON DELETE CASCADE,
        CONSTRAINT ck_due_after_issued CHECK (due_at IS NULL OR due_at > issued_at)
    )
    {
        COLUMN created_at { STATISTICS 200; }

        INDICES {
            idx_invoices_account (account_id);
            idx_invoices_status  (status) WHERE (status NOT IN ('paid', 'void'));
            idx_invoices_due     (due_at) WHERE (status = 'sent');
        }

        ENABLE ROW LEVEL SECURITY;

        POLICIES {
            isolate_tenants FOR ALL
                USING (account_id = current_setting('app.account_id')::UUID);
        }

        TRIGGERS {
            after_status_change AFTER UPDATE OF status
                FOR EACH ROW
                WHEN (OLD.status IS DISTINCT FROM NEW.status)
                EXECUTE FUNCTION notify_invoice_status_change();
        }

        GRANTS { SELECT, INSERT, UPDATE TO app_service; }
    }
}
```

```sql
-- production/myapp/schemas/public/functions.dpg

SCHEMA public {

    FUNCTION notify_invoice_status_change() RETURNS TRIGGER
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path = public
    AS $$
    BEGIN
        PERFORM pg_notify(
            'invoice_status_changed',
            json_build_object(
                'invoice_id', NEW.id,
                'old_status', OLD.status,
                'new_status', NEW.status
            )::TEXT
        );
        RETURN NEW;
    END;
    $$;

    MATERIALIZED VIEW account_billing_summary AS
        SELECT
            a.id,
            a.name,
            COUNT(i.id)                                                  AS total_invoices,
            COALESCE(SUM(i.total) FILTER (WHERE i.status = 'paid'),   0) AS paid_total,
            COALESCE(SUM(i.total) FILTER (WHERE i.status = 'overdue'),0) AS overdue_total
        FROM accounts a
        LEFT JOIN invoices i ON i.account_id = a.id
        WHERE a.deleted_at IS NULL
        GROUP BY a.id, a.name
    WITH NO DATA;
    {
        GRANTS { SELECT TO app_readonly; }
    }
}
```

---

## Appendix C. Error Code Reference

   | Code | Name | Description |
   |------|------|-------------|
   | DPG-E001 | `unknown_config_key` | Unknown key in `dpg.toml`. |
   | DPG-E002 | `ambiguous_connection` | Both `url` and `link` set in cluster config. |
   | DPG-E003 | `no_connection_configured` | Command requires a connection but neither `url` nor `link` is set. |
   | DPG-E004 | `reserved_name_conflict` | A database directory name matches the cluster objects directory name. |
   | DPG-E005 | `conflicting_set_member` | Same-named set-valued property has conflicting definitions across files. |
   | DPG-E006 | `forbidden_verb` | `CREATE`, `ALTER`, or `DROP` at declaration level in a `.dpg` file. |
   | DPG-E007 | `macro_inside_block` | `MACRO` declaration found inside a block. |
   | DPG-E008 | `paren_macro_in_block` | Paren-body macro spread inside a `{ }` block. |
   | DPG-E009 | `brace_macro_in_paren` | Brace-body macro spread inside a `( )` list. |
   | DPG-E010 | `undefined_macro` | Spread of undefined macro name. |
   | DPG-E011 | `duplicate_macro` | Macro name redeclared in the same file. |
   | DPG-E012 | `circular_macro` | Circular macro reference detected. |
   | DPG-E013 | `enum_migration_data_remains` | Rows still hold a removed ENUM value after the MIGRATE REMOVE DML ran. |
   | DPG-E014 | `unguarded_enum_removal` | ENUM value removed without a `MIGRATE REMOVE` block. |
   | DPG-E015 | `invalid_virtual_type_directive` | `VIRTUAL TYPE { }` block contains a directive other than `COMMENT`. |
   | DPG-E016 | `not_valid_in_paren_list` | `NOT VALID` used in a column or constraint inside the `( )` list. |
   | DPG-E017 | `unresolvable_cycle` | Circular FK dependency with no `DEFERRABLE` FK. |
   | DPG-E018 | `unknown_column_reference` | `COLUMN name { }` block references a column not in the `( )` list. |
   | DPG-E019 | `stale_column_name_in_index` | Index or constraint references old column name after a rename. |
   | DPG-E020 | `statistics_target_out_of_range` | `STATISTICS n` value is outside `[-1, 10000]`. |
   | DPG-E021 | `stale_renamed_from` | `RENAMED FROM` references a name absent from both source and snapshot. |
   | DPG-E022 | `protected_drop_attempt` | Diff would drop a `PROTECTED` object. |
   | DPG-E023 | `temporary_table_declared` | `TEMPORARY TABLE` keyword found in a `.dpg` file. |
   | DPG-E024 | `unknown_block_directive` | Unknown directive for this object kind in a `{ }` block. |
   | DPG-E025 | `destructive_ops_blocked` | Migration contains `DESTRUCTIVE` ops but `--allow-destructive` not passed. |

---

## Normative References

   [RFC2119]  Bradner, S., "Key words for use in RFCs to Indicate
              Requirement Levels", BCP 14, RFC 2119,
              DOI 10.17487/RFC2119, March 1997,
              <https://www.rfc-editor.org/rfc/rfc2119>.

   [RFC5234]  Crocker, D. and P. Overell, "Augmented BNF for Syntax
              Specifications: ABNF", STD 68, RFC 5234,
              DOI 10.17487/RFC5234, January 2008,
              <https://www.rfc-editor.org/rfc/rfc5234>.

   [RFC8174]  Leiba, B., "Ambiguity of Uppercase vs Lowercase in
              RFC 2119 Key Words", BCP 14, RFC 8174,
              DOI 10.17487/RFC8174, May 2017,
              <https://www.rfc-editor.org/rfc/rfc8174>.

   [RFC3629]  Yergeau, F., "UTF-8, a transformation format of
              ISO 10646", STD 63, RFC 3629,
              DOI 10.17487/RFC3629, November 2003,
              <https://www.rfc-editor.org/rfc/rfc3629>.

   [PGDOC14]  The PostgreSQL Global Development Group, "PostgreSQL 14
              Documentation", 2021,
              <https://www.postgresql.org/docs/14/>.

## Informative References

   [ATLAS]    Ariga, "Atlas — Database Schema Management",
              <https://atlasgo.io/>.

   [PRISMA]   Prisma Data, "Prisma ORM — Schema reference",
              <https://www.prisma.io/docs/concepts/components/prisma-schema>.

   [FLYWAY]   Redgate, "Flyway — Database Migrations Made Easy",
              <https://flywaydb.org/>.

   [KAHN62]   Kahn, A.B., "Topological sorting of large networks",
              Communications of the ACM, 5(11), pp. 558–562, 1962.

   [TARJAN72] Tarjan, R., "Depth-first search and linear graph
              algorithms", SIAM Journal on Computing, 1(2),
              pp. 146–160, 1972.

---

## Author's Address

   Daniel Tsegaw
   Independent

   Email: danieltsegaw.b@gmail.com

---

## Appendix D. Corrections and Additions to Earlier Sections

   This appendix records normative corrections and additions discovered
   by cross-referencing the reference implementation after the main
   document was written.  These entries have the same normative weight
   as the sections they amend.

### D.1. Snapshot Format — Actual Wire Schema (amends §16)

#### D.1.1. The SnapObject Discriminated Union

   Each entry in the `objects` map is NOT a flat object with a `kind`
   field directly on the object record.  It is a **discriminated union
   wrapper** (`SnapObject`) whose `kind` field selects which sub-object
   field is populated:

```json
{
  "public.users": {
    "kind": "table",
    "table": { <SnapTable fields> }
  },
  "public.get_user(text)": {
    "kind": "function",
    "function": { <SnapFunction fields> }
  },
  "public.user_state": {
    "kind": "virtual_type",
    "virtual_type": { <SnapVirtualType fields> }
  }
}
```

   The `kind` string values and their corresponding sub-object fields:

   | `kind` value | Sub-object field | Object types covered |
   |---|---|---|
   | `"table"` | `table` | TABLE, UNLOGGED TABLE, FOREIGN TABLE |
   | `"view"` | `view` | VIEW, MATERIALIZED VIEW, RECURSIVE VIEW |
   | `"function"` | `function` | FUNCTION |
   | `"type"` | `type` | ENUM, COMPOSITE, RANGE, DOMAIN, BASE |
   | `"schema"` | `schema` | SCHEMA |
   | `"extension"` | `extension` | EXTENSION |
   | `"sequence"` | `sequence` | SEQUENCE |
   | `"role"` | `role` | ROLE |
   | `"virtual_type"` | `virtual_type` | VIRTUAL TYPE |
   | `"procedure"`, `"aggregate"`, `"tablespace"`, `"fdw"`, `"server"`, `"user_mapping"`, `"publication"`, `"subscription"`, `"event_trigger"`, `"collation"`, `"operator"`, `"operator_class"`, `"operator_family"`, `"cast"`, `"statistics"`, `"ts_config"`, `"ts_dict"`, `"ts_parser"`, `"ts_template"` | `opaque` | All passthrough objects |

#### D.1.2. SnapOpaque — Passthrough Object Records

   Objects whose diff is body-text based (procedures, aggregates,
   tablespaces, FDWs, servers, user mappings, publications,
   subscriptions, event triggers, collations, operators, operator
   classes, operator families, casts, statistics objects, and all four
   text search object types) are stored as `SnapOpaque`:

```json
{
  "kind": "procedure",
  "opaque": {
    "kind": "procedure",
    "schema": "public",
    "name": "process_settlements",
    "args": "",
    "body_hash": "sha256:b4f2a1...",
    "comment": null,
    "grants": []
  }
}
```

   Fields:

   | Field | Type | Description |
   |-------|------|-------------|
   | `kind` | string | Object kind (same as outer `kind`). |
   | `schema` | string | Schema name; empty for cluster-level objects. |
   | `name` | string | Object name. |
   | `args` | string | Type-only argument key for overloaded objects (procedures, aggregates). Empty for non-overloaded. |
   | `body_hash` | string | `"sha256:<hex>"` of the normalised Part 1 body text. Empty string means body was empty. |
   | `comment` | string\|null | Comment text if any. |
   | `grants` | array | Grant records (for aggregate and procedure grants). |

   The differ compares `body_hash` for changes.  Any change to the body
   hash causes the compiler to emit `DROP ... CASCADE` + `CREATE ...`
   for the object (Safety class per Section 17.2 for each type).

#### D.1.3. Corrected Field Names in SnapColumn

   The column snapshot record uses `not_null` (boolean, `true` means
   NOT NULL), NOT `nullable` as described in §16.3.  The `identity`
   field holds the string `"ALWAYS"` or `"BY DEFAULT"`, NOT a nested
   object.

   Corrected column snapshot record:

```json
"columns": [
  {
    "name": "email",
    "type": "text",
    "not_null": true,
    "default": null,
    "identity": null,
    "generated": null,
    "comment": "Verified email address",
    "statistics": 300,
    "compression": null,
    "storage": null,
    "deprecated": null,
    "renamed_from": null,
    "grants": []
  }
]
```

   Note: `columns`, `constraints`, `indexes`, `policies`, `triggers`,
   and `grants` at the table level are all **ordered slices (arrays)**,
   NOT maps.  The object's `name` field within each element identifies
   it.

#### D.1.4. Corrected SnapConstraint Fields

   `SnapConstraint` does NOT have `columns` or `initially_deferred`
   fields at the top level.  Instead it has `expr` (the raw constraint
   expression/definition text) and `deferrable`:

```json
{
  "name": "pk_users",
  "type": "PRIMARY KEY",
  "expr": "(id)",
  "not_valid": false,
  "deferrable": false
}
```

#### D.1.5. Corrected SnapIndex Fields

   `SnapIndex` stores columns as a single comma-separated string, NOT
   as an array of objects:

```json
{
  "name": "idx_users_email",
  "unique": false,
  "method": "btree",
  "columns": "email",
  "where": null
}
```

#### D.1.6. Corrected SnapTrigger Fields

   `SnapTrigger` is simplified — it does not have `update_of`,
   `condition`, or `args` as separate fields.  The events are stored
   as a comma-separated string:

```json
{
  "name": "after_email_change",
  "when": "AFTER",
  "events": "UPDATE",
  "for_each": "ROW",
  "function": "public.notify_email_change"
}
```

#### D.1.7. Corrected SnapGrant Fields

   The grant record uses `roles` (an array of role names), NOT
   `grantee` (a single string):

```json
{
  "privileges": ["SELECT"],
  "roles": ["app_readonly", "app_readonly2"],
  "with_grant": false
}
```

#### D.1.8. SnapSchema, SnapExtension, SnapType, SnapSequence, SnapRole

   Complete records for all named sub-object types:

   **SnapSchema:**

```json
{
  "name": "analytics",
  "owner": "analytics_role",
  "comment": "Derived tables",
  "renamed_from": null
}
```

   **SnapExtension:**

```json
{
  "name": "pgcrypto",
  "schema": null,
  "version": null
}
```

   **SnapType** (ENUM):

```json
{
  "schema": "public",
  "name": "invoice_status",
  "variant": "ENUM",
  "values": ["draft", "sent", "paid", "void", "overdue"],
  "comment": "Billing lifecycle states"
}
```

   **SnapType** (COMPOSITE):

```json
{
  "schema": "public",
  "name": "address",
  "variant": "COMPOSITE",
  "composite_attrs": [
    { "name": "street", "type": "text" },
    { "name": "city",   "type": "text" }
  ]
}
```

   **SnapSequence:**

```json
{
  "schema": "public",
  "name": "order_number_seq",
  "comment": null,
  "increment_by": 1,
  "min_value": 10000,
  "max_value": 99999999,
  "start_value": 10000,
  "cache": 50,
  "cycle": false
}
```

   **SnapRole:**

```json
{
  "name": "app_service",
  "comment": null
}
```

   Note: Role attributes (LOGIN, PASSWORD, CONNECTION LIMIT, etc.) are
   NOT stored in the snapshot beyond the name and comment.  The differ
   compares roles by name presence only.  Attribute changes are
   re-applied via `ALTER ROLE` on each `dpg apply` run by comparing the
   desired IR against a live catalog introspection.

#### D.1.9. Cluster-Level Snapshot File

   Cluster-level objects (roles, tablespaces) are stored in a SEPARATE
   snapshot file from database-level objects.  The path is:

   `.dpg/snapshots/<cluster-name>/_cluster.json`

   The `database` field in the top-level snapshot record is absent
   (empty string / omitted) for cluster-level snapshots.  The compiler
   identifies a snapshot as cluster-level when `database` is empty.

---

### D.2. CLI Command Corrections (amends §18)

#### D.2.1. `dpg plan` — Corrected Flags

   The `--format` flag accepts `text` (default) or `json`, NOT `sql`.
   The format `sql` is not a valid value.

   Additional flag not previously documented:

```
  --watch    Re-run plan automatically whenever any .dpg source file's
             modification time changes.  Polls every 500 milliseconds.
             Exits cleanly on SIGINT (Ctrl-C) or SIGTERM.
```

   The `--watch` mode runs the plan once immediately, then enters a
   polling loop.  Each iteration of the loop compares the modification
   times of all discovered `.dpg` files against the previous snapshot.
   If any file's mtime has changed, or if any files have been added or
   removed, the plan is re-run.  Plan errors are printed to stderr but
   do not stop the watch loop.

   **All flags for `dpg plan`:**

```
dpg plan [--cluster name] [--database name]
         [--live] [--format text|json] [--watch]
```

#### D.2.2. `dpg validate` — Corrected Format Flag

   Same correction: `--format text|json`, not `--format sql|json`.

#### D.2.3. `--env` Flag — `.env` File Loading

   Commands that require a live database connection (`dpg apply`,
   `dpg verify`, `dpg dump`, `dpg plan --live`) support an `--env`
   flag that specifies the path to a `.env` file containing environment
   variable definitions used to resolve `link =` connection strings.

```
  --env <path>   Path to a .env file.  Defaults to <project-root>/.env
                 if a .env file exists there.  Non-fatal if absent.
```

   **`.env` file loading rules:**

   1.  Loading is only performed when at least one cluster uses a `link`
       connection string (i.e., `cl.IsLink() == true`).  Clusters using
       inline `url =` strings do not trigger `.env` loading.

   2.  Path resolution order:
       a.  The path given by `--env <path>`, if provided.
       b.  `<project-root>/.env`, if it exists.

   3.  Existing process environment variables are NEVER overwritten.
       The `.env` file only sets variables that are not already present
       in `os.Environ()`.  ("process env wins")

   4.  **`.env` file format:**
       -   Lines that are blank or start with `#` are ignored.
       -   Lines may begin with `export ` (stripped before parsing).
       -   Format: `KEY=VALUE` or `KEY = VALUE`.
       -   Values wrapped in single or double quotes have the quotes
           stripped.
       -   Variables already set in the process environment are NOT
           overwritten.

   5.  A missing `.env` file is NOT an error.  The command proceeds
       using only the process environment.

   **Example `.env`:**

```
# Production cluster credentials
export PRODUCTION_DB_URL='postgresql://admin@db.prod:5432/postgres'
APP_SERVICE_PW="s3cr3t"
```

#### D.2.4. Target Auto-Selection Rules

   When `--cluster` or `--database` are not specified, the compiler
   applies the following auto-selection algorithm:

   **Cluster auto-selection:**

   1.  If there is exactly one cluster in the project, it is selected
       automatically.  No `--cluster` flag is required.
   2.  If there are multiple clusters and `--cluster` is not set, the
       compiler MUST emit error DPG-E026 listing all available cluster
       names.
   3.  If `--cluster` is set to a name that does not exist, the
       compiler MUST emit error DPG-E027 with the available cluster
       names.

   **Database auto-selection** (within a selected cluster):

   1.  If there is exactly one database in the cluster, it is selected
       automatically.
   2.  If there are multiple databases and `--database` is not set, the
       compiler MUST emit error DPG-E028 listing the available database
       names.
   3.  If `--database` is set to a name that does not exist, the
       compiler MUST emit error DPG-E029 with the available database
       names.

   These rules apply to: `dpg plan`, `dpg apply`, `dpg verify`,
   `dpg dump`.

#### D.2.5. `dpg plan --format json` — Output Schema

   When `--format json` is used, each database's plan is serialised to
   stdout as a JSON object with the following schema:

```json
{
  "cluster":         "production",
  "database":        "myapp",
  "generated_at":    "2025-09-15T14:32:00Z",
  "source_revision": "a3f7c91",
  "ops": [
    {
      "sql":    "CREATE TABLE public.users (...);",
      "safety": "SAFE",
      "file":   "schemas/public/tables/users.dpg",
      "line":   4
    }
  ],
  "empty": false
}
```

   Field descriptions:

   | Field | Type | Description |
   |-------|------|-------------|
   | `cluster` | string | Cluster name. |
   | `database` | string | Database name. Empty for cluster-level plans. |
   | `generated_at` | RFC 3339 string | UTC timestamp of plan generation. |
   | `source_revision` | string | Git short SHA, or empty if unavailable. |
   | `ops` | array | Ordered list of DiffOp objects. |
   | `ops[].sql` | string | The SQL statement text. |
   | `ops[].safety` | string | One of `"SAFE"`, `"CAUTION"`, `"DESTRUCTIVE"`, `"MANUAL"`. |
   | `ops[].file` | string | Source file path relative to project root, or omitted if unknown. |
   | `ops[].line` | integer | 1-based source line number, or omitted if unknown. |
   | `empty` | boolean | `true` when `ops` is empty (no changes). |

   When targeting multiple databases in one run, each database produces
   one JSON object.  Multiple JSON objects are printed sequentially to
   stdout, separated by newlines.  Each object is complete and valid
   JSON; the stream is NDJSON (Newline-Delimited JSON, [NDJSON]).

---

### D.3. Linter Rule ID Corrections (amends §19)

   The actual built-in linter rule identifiers use hyphens, NOT
   underscores.  The corrected rule ID table:

   | Rule ID (actual) | Description | Default Level |
   |---|---|---|
   | `hardcoded-password` | Column `DEFAULT` or ROLE `PASSWORD` contains a hardcoded string. | Error |
   | `deprecated` | Object or column is marked `DEPRECATED`. Applied to tables, columns, views, functions. | Warning |
   | `require-column-comments` | Column lacks a `COMMENT` when `require_column_comments = true`. | Warning |
   | `max-columns` | Table exceeds `max_columns_per_table` columns. | Error |
   | `security-definer-search-path` | `SECURITY DEFINER` function body does not reference `search_path`. | Warning |

   **Implementation note on `hardcoded-password` for columns:** The
   linter checks column `DEFAULT` expressions for patterns that suggest
   a hardcoded password.  Specifically, if the column name contains any
   of the substrings `password`, `passwd`, `pwd`, `secret`, or
   `passphrase` (case-insensitive), AND the default expression is a
   single-quoted string literal (starts with `'`), the linter emits
   this rule as an error.

   **Note:** The rules `scalar_merge_conflict`, `serial_sequence_declared`,
   `unnecessary_revocation`, `stale_renamed_from`, `unguarded_enum_removal`,
   and `protected_drop_attempt` listed in §19.1 are compiler-phase
   diagnostics rather than linter rules in the current implementation.
   They are emitted during diffing or IR construction, not by the
   `Linter.Lint` interface.  They are included in §19.1 for conceptual
   completeness.

---

### D.4. Pipeline Registry Key Constants (amends §15)

   The `pipeline.Registry` component system uses the following string
   keys to register and resolve pipeline components:

   | Key constant | Interface | Default implementation |
   |---|---|---|
   | `pipeline.KeyTokenizer` | `pipeline.Tokenizer` | `internal/scanner` |
   | `pipeline.KeyPGSQLParser` | `pipeline.PGSQLParser` | `internal/pgparser.LibPQParser` |
   | `pipeline.KeyBlockParser` | `pipeline.BlockParser` | `internal/blockparser` |
   | `pipeline.KeyIRBuilder` | `pipeline.IRBuilder` | `internal/ir.Builder` |
   | `pipeline.KeyMerger` | `pipeline.Merger` | `internal/merger` |
   | `pipeline.KeyDependencyResolver` | `pipeline.DependencyResolver` | `internal/graph` |
   | `pipeline.KeySnapshotStore` | `pipeline.SnapshotStore` | `internal/snapshot.FileStore` |
   | `pipeline.KeyDiffer` | `pipeline.Differ` | `internal/diff.Differ` |
   | `pipeline.KeyEmitter` | `pipeline.Emitter` | `internal/emit.Emitter` |
   | `pipeline.KeyApplyExecutor` | `pipeline.ApplyExecutor` | `internal/executor.PgxExecutor` |
   | `pipeline.KeyIntrospector` | `pipeline.Introspector` | `internal/introspect.CatalogIntrospector` |
   | `pipeline.KeyLinter` | `pipeline.Linter` | `internal/linter.BuiltinLinter` |
   | `pipeline.KeyPortabilityAnalyzer` | `pipeline.PortabilityAnalyzer` | `internal/portability.Analyzer` |
   | `pipeline.KeySecretResolver` | `pipeline.SecretResolver` | `internal/secrets.EnvResolver` |

   Each default implementation registers itself in its package's `init()`
   function.  Alternative implementations MAY be registered using
   `pipeline.Default.Register(key, impl)` before any command runs.
   The `pipeline.Default` registry is a package-level singleton.

   `pipeline.MustResolve[T]` panics if the component is not registered.
   `pipeline.Resolve[T]` returns `(T, bool)` and does not panic.

---

### D.5. SecretResolver Protocol Specification (amends §3.3)

   The `pipeline.SecretResolver` interface resolves a URI string to a
   plaintext secret value at connection time.  The interface is:

```go
type SecretResolver interface {
    Resolve(uri string) (string, error)
}
```

   The reference implementation (`internal/secrets.EnvResolver`) supports
   the following URI schemes:

   **`env:<VAR_NAME>`**

   Resolves to `os.Getenv(VAR_NAME)`.  If the variable is not set
   (empty string), the resolver returns an error.  The variable name
   MUST consist only of ASCII letters, digits, and underscores.

   Example: `link = "env:PRODUCTION_DB_URL"` → resolves `PRODUCTION_DB_URL`
   from the process environment (which may have been populated from the
   `.env` file per §D.2.3).

   **Future schemes (planned, not yet implemented):**

   -   `vault:<path>` — HashiCorp Vault secret read.
   -   `aws-sm:<secret-id>` — AWS Secrets Manager lookup.
   -   `gcp-sm:<resource-name>` — GCP Secret Manager lookup.

   A `ChainResolver` implementation is provided that tries each resolver
   in order and returns the first non-error result.

---

### D.6. Source Revision Detection (amends §16.2)

   The `source_revision` field in the snapshot and migration header is
   populated by reading `.git/HEAD` directly (no `git` binary required).

   **Algorithm:**

   1.  Read `.git/HEAD` relative to the current working directory.
       If not present, `source_revision` is empty string.

   2.  If the content starts with `"ref: "`, strip the prefix and read
       the file at `.git/<ref-path>`.  Example: `"ref: refs/heads/main"`
       → read `.git/refs/heads/main`.

   3.  Otherwise, the content itself is the commit hash (detached HEAD).

   4.  Trim whitespace.  If the result is at least 7 characters, take
       the first 7 characters as the short hash.  Otherwise, empty.

   This method works without the `git` binary and without any network
   access.  It is intentionally simple; it does not handle packed refs
   or shallow clones differently from full clones.

---

### D.7. Additional CLI Error Codes (extends Appendix C)

   | Code | Name | Description |
   |------|------|-------------|
   | DPG-E026 | `multiple_clusters_no_flag` | Multiple clusters found; `--cluster` required. |
   | DPG-E027 | `cluster_not_found` | `--cluster` value does not match any cluster. |
   | DPG-E028 | `multiple_databases_no_flag` | Multiple databases found; `--database` required. |
   | DPG-E029 | `database_not_found` | `--database` value does not match any database. |

---

### D.8. Root dpg.toml Missing Sections (amends §3.2)

   Section 3.2 omits two TOML sections that are defined and in use in
   the reference implementation:

#### D.8.1. [fmt] — Formatter Configuration

   The `[fmt]` section controls the behaviour of `dpg fmt`:

```toml
[fmt]
# Number of spaces per indentation level. Default: 4.
indent = 4

# Keyword casing applied to all DPG and PostgreSQL keywords.
# Valid values: "upper" (default), "lower".
keyword_case = "upper"
```

   Note: The TOML key is `indent`, NOT `indent_size`.

#### D.8.2. [migrations] — Migration Archive Configuration

   The `[migrations]` section controls where applied SQL files are
   archived after a successful `dpg apply`:

```toml
[migrations]
# Relative path from the project root where applied migration SQL files
# are written. Default: ".dpg/migrations".
# Set to "" to disable archiving entirely.
directory = ".dpg/migrations"
```

   On each successful `dpg apply`, the emitted SQL is saved to:

```
<directory>/<cluster>/<database>/<timestamp>_<short-hash>.sql
```

   This directory SHOULD be committed to version control.

---

### D.9. CLI Command Corrections (amends §18)

#### D.9.1. dpg validate — Correct Flags and JSON Schema (amends §18.6)

   The actual `dpg validate` flags are:

```
dpg validate [options]

Options:
  --cluster <name>   cluster to validate (default: all)
  --database <name>  database to validate (default: all)
  --format <fmt>     output format: text or json (default: text)
```

   There is NO `--strict` flag on `dpg validate`.  Linter rule severity
   is configured exclusively through `dpg.toml [linter]` settings.

   The `--format json` output is a **single JSON object** per
   cluster/database scope, NOT an array:

```json
{
  "cluster": "production",
  "database": "myapp",
  "objects": 42,
  "errors": [
    {
      "rule": "hardcoded-password",
      "message": "column 'password' has a hardcoded string default",
      "file": "schemas/public/tables/users.dpg",
      "line": 12,
      "col": 5
    }
  ],
  "warnings": []
}
```

   Fields:

   | Field | Type | Description |
   |-------|------|-------------|
   | `cluster` | string | Cluster name. |
   | `database` | string | Database name; `"(cluster)"` for cluster-level objects. |
   | `objects` | integer | Number of IR objects successfully compiled. |
   | `errors` | array | Diagnostics with `IsError = true`. Empty array if none. |
   | `warnings` | array | Diagnostics with `IsError = false`. Empty array if none. |

   Each diagnostic object has `rule`, `message`, `file`, `line`, `col`.
   Note: `rule` uses hyphen-separated IDs (e.g., `"hardcoded-password"`)
   per the correction in §D.3.

   Exit codes: 0 = no errors; non-zero = errors found or internal
   error.  Multiple scopes each emit a separate JSON object (one line
   per scope is NOT guaranteed — each is a complete JSON object).

#### D.9.2. dpg portability — No --format Flag (amends §18.8)

   The `dpg portability` command does NOT support `--format`.  Its
   output is text only.  The actual flags are:

```
dpg portability [options]

Options:
  --cluster <name>   cluster to analyze (default: all)
  --database <name>  database to analyze (default: all)
```

#### D.9.3. dpg init — Correct Defaults and Flags (amends §18.9)

   The actual `dpg init` defaults and flags are:

```
dpg init [options] [<dir>]

Options:
  --cluster <name>   Cluster directory name (default: "production")
  --database <name>  Database directory name (default: "myapp")
  --schema <name>    Default schema name (default: "public")
  --url <url>        PostgreSQL connection URL (can be set later in dpg.toml)
```

   Note: the default cluster name is `"production"`, NOT `"main"`.

   Files created:

```
<dir>/dpg.toml                              root config
<dir>/<cluster>/dpg.toml                    cluster config
<dir>/<cluster>/<database>/dpg.toml         database config
<dir>/<cluster>/cluster/                    cluster objects dir (empty)
<dir>/<cluster>/<database>/schemas/<schema>/  schema source dir (empty)
<dir>/.dpg/snapshots/                       snapshot storage
```

   Existing files are skipped (not overwritten).  Directories are
   created unconditionally with `os.MkdirAll`.

#### D.9.4. dpg fmt — Correct Config Key Names (amends §18.7)

   The `[fmt]` section in `dpg.toml` uses the TOML key `indent` (not
   `indent_size`).  The `keyword_case` valid values are `"upper"` and
   `"lower"` (not `"uppercase"` or `"lowercase"`).

   The formatter applies:
   -   Indentation: configurable (default 4 spaces).
   -   Keyword casing: `"upper"` uppercases DPG/PG keywords;
       `"lower"` lowercases them.

   The RFC canonical-style list in §18.7 (column alignment, identifier
   lowercasing) SHOULD be treated as aspirational.  The reference
   implementation DOES NOT currently enforce column alignment or
   identifier casing beyond keywords.

---

## Appendix E. Revision History

   This appendix records all substantive changes to this document after
   its initial publication.

   | Revision | Date | Description |
   |----------|------|-------------|
   | E.1 | 2026-05-13 | Initial publication. Formal IETF-style RFC superseding the informal design document `rfc/v0.8.0.md`. All sections written from scratch with normative RFC 2119 language, ABNF grammars, and exhaustive per-object specifications. |
   | E.2 | 2026-05-13 | Appendix D added. Corrections to §16 (snapshot wire format: `SnapObject` discriminated union, `SnapOpaque`, corrected field names), §18 (`--format text` default, `--watch` flag, `.env` loading protocol, `planJSON` schema, target auto-selection), §19 (linter rule IDs use hyphens). Pipeline Registry key constants table and SecretResolver protocol specification added. Source revision detection algorithm formalised. |
   | E.3 | 2026-05-13 | §D.8–§D.9 added. Root `dpg.toml` `[fmt]` and `[migrations]` sections documented. CLI corrections: `dpg validate` JSON schema, `dpg portability` flag set, `dpg init` default cluster name (`"production"`), `dpg fmt` TOML key names. ToC updated to include Appendix D subsections. |

---

*End of RFC 1 — Declarative PG (DPG) v0.8.1*
