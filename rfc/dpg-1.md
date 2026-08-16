---
title: "RFC DPG-1: Declarative PG"
rfc_number: "DPG-001"
rfc_status: "Standards Track"
rfc_version: "0.9.0"
rfc_date: "2026-05-13"
rfc_target: "PostgreSQL 14+"
rfc_authors: "Daniel Tsegaw"
layout: "rfc"
weight: 1
description: "Full normative specification for DPG — a declarative, state-based superset of PostgreSQL SQL that compiles to idiomatic PG DDL."
---

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
    D.10. Name Maps .............................................. 150
    D.11. SERIAL / BIGSERIAL / SMALLSERIAL Column Sugar ........... 151
Appendix E.  Revision History ..................................... 152
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
   -   Inline data seeding (out of scope; DPG is a schema tool).

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

# Secrets-provider URI resolved at connection time. Supported schemes
# (see §D.5): env:<VAR>, vault:<mount>/<path>#<field>, aws-sm:<secret-id>,
# gcp-sm:<project>/<secret-id>, azure-kv:<vault-name>/<secret-name>.
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
    COMMENT 'Returns hello';
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

   -   Macros are **project-scoped**.  A macro defined in any `.dpg`
       file within the compilation scope (all files for a given database
       pass through the compiler together) is visible in every other
       file.  The compiler performs a global collection pre-pass over
       all source files before any file is tokenized, so declaration
       order across files does not matter.

   -   When the same macro name is defined in multiple files, the
       file-local definition takes precedence over any globally-collected
       definition.  This allows individual files to override a shared
       macro with a specialised version.

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

enum-values   = SQUOTE identifier SQUOTE
                *( "," WSP SQUOTE identifier SQUOTE )

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
    COMMENT 'Billing lifecycle states for customer invoices';
}

-- ENUM with value removal
ENUM order_status ('pending', 'confirmed', 'shipped', 'delivered');
{
    COMMENT 'Order lifecycle states';
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

   Virtual types are DPG-native DDL constructs that give a structural
   schema to `JSON` / `JSONB` columns and `JSON` array columns.  They
   have no backing PostgreSQL type — no `CREATE TYPE`, `ALTER TYPE`, or
   `DROP TYPE` is ever emitted.  A table column or composite type
   attribute may declare its type as a virtual type name; DPG resolves
   that reference to `jsonb` (scalar) or `jsonb[]` (array) in all
   generated SQL.  The structured body is stored in the snapshot so
   downstream consumers (ORM generators, type-safe query builders) can
   read type information via the `pkg/dpg` API or the snapshot JSON.

```abnf
virtual-type-decl = "VIRTUAL TYPE" WSP schema-name WSP "AS" WSP
                    vtype-body ";" [ "{" vtype-block "}" ]

vtype-body      = vtype-union
vtype-union     = vtype-term *( WSP "|" WSP vtype-term )
vtype-term      = vtype-composite / vtype-typeref
vtype-composite = "(" vtype-field *( "," WSP vtype-field ) ")"
vtype-field     = identifier WSP vtype-typeref
vtype-typeref   = [ schema-name "." ] identifier [ "[]" ]

vtype-block = *( comment-dir / preferred-json-format-dir ) ";"

preferred-json-format-dir = "PREFERRED" WSP "JSON" WSP "FORMAT" WSP
                            ( "json" / "jsonb" )
```

   The compiler MUST parse and validate the body.  A `vtype-typeref`
   that appears in a column or composite attribute position resolves to
   the virtual type's preferred JSON format (`json` or `jsonb`, default
   `jsonb`) in SQL output; the virtual type name is never written to the
   database.

   **Body forms:**

   -   **Type reference** — a single PG built-in or user-defined type
       name, or a reference to another declared `VIRTUAL TYPE`:

       ```sql
       VIRTUAL TYPE label AS text;
       VIRTUAL TYPE metric AS numeric;
       VIRTUAL TYPE named_point AS point;        -- references virtual type "point"
       VIRTUAL TYPE tags AS text[];              -- array form
       ```

   -   **Composite** — an inline record with named, typed fields.
       Field types MAY themselves be virtual type references:

       ```sql
       VIRTUAL TYPE point AS (x float8, y float8);

       VIRTUAL TYPE line_item AS (
           sku      text,
           quantity integer,
           price    numeric
       );
       ```

   -   **Union** — two or more terms joined with `|`.  Any combination
       of composites and type references is valid:

       ```sql
       VIRTUAL TYPE payment AS
           (kind text, amount numeric, currency text)
           | (kind text, token text)
           | text;
       ```

   **Usage as a column or field type:**

   ```sql
   TABLE orders (
       id    bigint,
       items line_item[]   -- virtual type ref: emits "jsonb[]" in SQL
   ) { }

   TYPE address AS (
       street text,
       detail address_detail   -- virtual type ref: emits "jsonb"
   );
   ```

   Adding `[]` to the reference signals a JSON array column and causes
   DPG to emit `jsonb[]` instead of `jsonb`.

   **Rules:**

   -   `VIRTUAL TYPE` MAY be schema-qualified; if unqualified it
       defaults to `default_schema`.

   -   No `CREATE TYPE`, `ALTER TYPE`, or `DROP TYPE` is EVER emitted
       for a virtual type.

   -   Virtual types appear in the snapshot under `"kind": "virtual_type"`
       for round-trip consistency.  `dpg plan` produces no SQL for
       additions, modifications, or removals of virtual types.

   -   When a column or composite attribute type resolves to a virtual
       type, the generated SQL uses `jsonb` / `jsonb[]`.  The virtual
       type name is never written to the database catalog.

   -   The `{ }` block accepts `COMMENT` and `PREFERRED JSON FORMAT`.
       Any other directive is a compiler error (DPG-E015).

   -   `PREFERRED JSON FORMAT json` causes DPG to emit `json` / `json[]`
       instead of the default `jsonb` / `jsonb[]` when this virtual type
       is used as a column or attribute type.

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
    COMMENT 'Derived tables and event aggregations';

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

   **Implementation status of "PostgreSQL can apply implicitly" (as of
   this writing):** this bullet's own example, `VARCHAR(10)` →
   `VARCHAR(20)`, is a same-base-type typmod (length/precision) widening
   — real PostgreSQL permits it with no `USING` and no data loss because
   the *type itself* hasn't changed, only its length constraint, which is
   a different mechanism from `pg_cast`. This specific case is **NOT YET
   implemented** — it still falls through to the conservative
   `DESTRUCTIVE` default until DPG has type-specific typmod
   comparison logic (comparing two `numeric(p,s)` precision/scale pairs,
   two `varchar(n)` lengths, etc. — genuinely different per type, unlike
   the different-base-type case below). What **IS** implemented: a type
   change between two genuinely *different* base types (e.g. `smallint`
   → `integer`, `real` → `double precision`) is classified `CAUTION`
   when PostgreSQL has a real implicit cast (`pg_cast.castcontext = 'i'`)
   between them, and `DESTRUCTIVE` otherwise — see `internal/diff/
   implicit_casts.go` for the full table (extracted verbatim from a live
   PostgreSQL 17 catalog, not reconstructed from memory, with a live
   integration test cross-checking it against a fresh container so it can
   never silently drift out of sync with a future PostgreSQL version) and
   `.dpg-notes/dpg-tracker.md` for the closure writeup.

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
    = "COMMENT" WSP SQUOTE text SQUOTE
    / "STATISTICS" WSP integer
    / "COMPRESSION" WSP method
    / "STORAGE" WSP storage-type
    / "DEPRECATED" WSP SQUOTE text SQUOTE
    / "USING" WSP expr
    / "RENAMED FROM" WSP col-name
    / grants-block
    / revocations-block
```

   The complete attribute table:

   | Directive | PostgreSQL DDL emitted |
   |-----------|------------------------|
   | `COMMENT 'text'` | `COMMENT ON COLUMN t.c IS '...'` |
   | `STATISTICS n` | `ALTER TABLE t ALTER COLUMN c SET STATISTICS n` |
   | `COMPRESSION method` | `ALTER TABLE t ALTER COLUMN c SET COMPRESSION m` |
   | `STORAGE type` | `ALTER TABLE t ALTER COLUMN c SET STORAGE s` |
   | `DEPRECATED 'msg'` | `COMMENT ON COLUMN t.c IS '[DEPRECATED] msg'` |
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
        COMMENT 'Verified email address';
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
index-decl  = [ "UNIQUE" WSP ]
              [ "CONCURRENTLY" WSP ]
              index-name WSP
              [ "USING" WSP method WSP ]
              "(" index-col-list ")"
              [ "INCLUDE" WSP "(" col-list ")" ]
              [ "WITH" WSP "(" storage-params ")" ]
              [ "WHERE" WSP "(" predicate ")" ]
              [ "TABLESPACE" WSP identifier ]
              ";"

index-col-list = index-col *( "," index-col )
index-col   = ( col-name / "(" expr ")" )
              [ "ASC" / "DESC" ]
              [ "NULLS FIRST" / "NULLS LAST" ]
              [ "COLLATE" WSP identifier ]
              [ "opclass" ]
```

   `UNIQUE` and `CONCURRENTLY` are both prefix keywords before the index
   name, in that fixed order — mirroring real PostgreSQL's own
   `CREATE UNIQUE INDEX CONCURRENTLY name ON table USING method (columns)`
   exactly (only the implicit `INDEX`/`ON table` are dropped, since DPG's
   `INDICES { }` block already establishes both). In **Mode B** (§4.8),
   which does carry the literal `INDEX` keyword, the same order applies
   with `INDEX` inserted where PostgreSQL puts it:
   `[ "UNIQUE" WSP ] "INDEX" WSP [ "CONCURRENTLY" WSP ] index-name ...`.

   **`CONCURRENTLY` is a bare presence keyword, not a boolean** — this
   matches real PostgreSQL exactly: `CONCURRENTLY` is either written or it
   isn't, there is no `CONCURRENTLY false` and no project-wide default that
   turns it on implicitly. Writing it is the only way an index is ever
   created concurrently; omitting it always means plain `CREATE INDEX`.

   **Concurrency behaviour:**

   -   Writing `CONCURRENTLY` on an individual index emits
       `CREATE INDEX CONCURRENTLY`. This is a `MANUAL` operation
       (non-transactional; emitted after `COMMIT`) — *except* for a
       brand-new table's own indexes (see below), where it is silently
       ignored.

   -   Omitting `CONCURRENTLY` emits plain `CREATE INDEX` (transactional;
       Safety `CAUTION` for index additions on non-empty tables). There is
       no configuration knob that changes this — matching real PostgreSQL,
       which has no such default either.

   -   **Indexes on a brand-new table are always non-concurrent —
       this is a hard PostgreSQL restriction, not a preference.** A new
       table's indexes are emitted in the SAME transactional migration as
       its `CREATE TABLE`, and PostgreSQL rejects `CREATE INDEX
       CONCURRENTLY` inside a transaction block. The compiler therefore
       forces these non-concurrent unconditionally — an explicit
       `CONCURRENTLY` on such an index has no effect until a later
       migration adds the index to the now-existing table.

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
   | New index (existing table) | `CREATE [UNIQUE] INDEX [CONCURRENTLY] [IF NOT EXISTS] name ON ...` | `MANUAL` if `CONCURRENTLY` written, else `CAUTION` (default) |
   | Index removed | `DROP INDEX [CONCURRENTLY] name` | `CAUTION` |
   | Any structural change | Drop + recreate | `CAUTION` or `MANUAL` |

   Indexes on new tables (emitted in the same migration as the
   `CREATE TABLE`) are always emitted as non-concurrent `CREATE INDEX` in
   the same transactional block — see **Concurrency behaviour** above.

   Examples:

```sql
TABLE users ( email TEXT, status user_status, ... )
{
    INDICES {
        idx_email             (email);
        UNIQUE idx_unique_slug (slug);
        idx_tenant_created    (tenant_id ASC, created_at DESC);
        idx_active_users      (email) WHERE (status = 'active');
        idx_location          USING gist (location);
        idx_tags              USING gin  (tags);
        idx_lower_email       (lower(email));
        idx_covering          (user_id) INCLUDE (email, created_at);
        idx_brin              USING brin (created_at)
                                  WITH (pages_per_range = 128);
        idx_archived          (archived_at) TABLESPACE archive_space;
        CONCURRENTLY idx_forced_concurrent (payload);
        UNIQUE CONCURRENTLY idx_unique_concurrent (external_id);
    }
}

-- Mode B (§4.8): the literal INDEX keyword carries UNIQUE/CONCURRENTLY in
-- the exact same order PostgreSQL's own CREATE UNIQUE INDEX CONCURRENTLY
-- statement does.
UNIQUE INDEX idx_unique_slug (slug);
INDEX CONCURRENTLY idx_forced_concurrent (payload);
UNIQUE INDEX CONCURRENTLY idx_unique_concurrent (external_id);
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
deprecated-dir   = "DEPRECATED" WSP SQUOTE text SQUOTE
drop-cascade-dir = "DROP CASCADE"
owner-dir        = "OWNER" WSP DQUOTE identifier DQUOTE
comment-dir      = "COMMENT" WSP SQUOTE text SQUOTE
```

   **`PROTECTED`:** The compiler MUST refuse to emit a `DROP TABLE`
   for a protected table even when the table is absent from the desired
   state.  Removing a protected table requires first removing the
   `PROTECTED` directive.  Safety: any attempt to drop a protected
   table is error DPG-E022.

   **`DEPRECATED 'msg'`:** The compiler emits a `COMMENT ON TABLE`
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
    COLUMN id { COMMENT 'Remote event primary key'; }
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
        COMMENT 'Admin user summary view';
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
   (rule: `security-definer-search-path`).

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
        COMMENT 'Fetch a user record by verified email address';
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
        COMMENT 'Multiplicative aggregate over DOUBLE PRECISION values';
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
   in the snapshot (see Section 16.3).  Normalisation depends on the
   function's `LANGUAGE`:

   -   **`LANGUAGE SQL`:** the body is parsed and re-deparsed via the
       same PostgreSQL-grammar parser the compiler uses for every other
       statement, then hashed. This canonicalisation absorbs whitespace,
       quote-style, and clause-formatting differences — the same
       mechanism already relied on for reconstructing the opaque-tier
       object kinds (Tablespace, FDW, Collation, etc. — see Section 25).
       On any parse failure (a body the parser rejects for any reason),
       the compiler falls back to the plain normalisation below rather
       than erroring.
   -   **`LANGUAGE plpgsql`:** the body is compiled through the real
       PL/pgSQL compiler (via `libpg_query`'s PL/pgSQL parse-to-JSON
       entry point, fed a full, argument-accurate
       `CREATE FUNCTION`/`PROCEDURE` statement — a bare body string is
       not sufficient: the PL/pgSQL compiler resolves the body's own
       parameter references, e.g. an assignment target, against the
       declared argument list at compile time), and the resulting parse
       tree is stripped of its one confirmed source-position field
       (`lineno`) before hashing. This absorbs whitespace, blank-line,
       and comment differences in the outer control-flow shape
       (statement ordering/nesting, declarations, labels, flags).
       Every embedded expression fragment (a condition, an assignment's
       left- and right-hand sides, a `RETURN` expression, a `RAISE`
       parameter, an embedded SQL statement) is *also* independently
       re-parsed and re-deparsed before hashing, using the exact raw
       parser mode `libpg_query` itself records for that fragment
       (`RAW_PARSE_PLPGSQL_EXPR` for a bare expression,
       `RAW_PARSE_PLPGSQL_ASSIGN1`/`2`/`3` for a one-, two-, or
       three-part dotted assignment target, or the ordinary statement
       parser for a fragment that is already a complete standalone
       statement) — the same modes PostgreSQL's own PL/pgSQL compiler
       uses for these fragments, exposed via a small, purely additive
       patch to `libpg_query`'s Go bindings (these raw-parse-mode entry
       points already existed as public, stable C functions; they had
       simply never been wrapped in Go). This closes the previous gap
       where only the outer control-flow shape was canonicalised: a
       whitespace-only change inside an embedded fragment (e.g.
       `a||b` reformatted to `a || b`) is now absorbed too, for every
       fragment shape the current `libpg_query` version actually
       produces. On any failure at any step — a body the PL/pgSQL
       compiler rejects, a compile error from a mismatched shim, or an
       individual fragment that fails to re-parse — the affected
       fragment (or, if the failure is at the whole-body level, the
       entire body) falls back to the plain normalisation below rather
       than erroring; a hashing nicety must never block a build.
   -   **Every other language** (`c`, `internal`, and any procedural-
       language extension other than `plpgsql`): plain text
       normalisation only —
       1.  Stripping leading and trailing whitespace from the body text.
       2.  Collapsing all internal runs of whitespace (spaces, tabs,
           newlines) to a single space character.

   Any change to the body that survives normalisation — including,
   for languages with no canonicalisation, changes that are cosmetic but
   not reducible to whitespace alone (comment wording, quote style,
   capitalisation) — changes the hash and causes the compiler to emit:

   ```sql
   CREATE OR REPLACE FUNCTION schema.name(...) RETURNS ... AS $$...$$;
   ```

   No semantic analysis of procedural code is performed for any
   language. For `LANGUAGE SQL`, syntactic reformulations that
   re-deparse identically are absorbed by the canonicalisation above;
   genuinely equivalent but differently-*structured* SQL (e.g. a
   rewritten join order) is still detected as changed, by design — this
   is syntactic canonicalisation, not semantic equivalence. For
   `LANGUAGE plpgsql`, both the outer control-flow shape and every
   embedded expression fragment are canonicalised, on the same
   syntactic (not semantic) basis: a genuinely equivalent but
   differently-*structured* fragment (e.g. rewriting `a OR b` to the
   logically equivalent `NOT (NOT a AND NOT b)`) is still detected as
   changed, by design. This canonicalisation covers every fragment shape
   the current `libpg_query` version's PL/pgSQL parser actually
   produces (verified directly against the grammar source, not assumed)
   — a future `libpg_query` version introducing a new fragment shape
   would fail open (that fragment's raw text retained, same as before
   this fix) rather than error. For every other language, no
   canonicalisation is performed at all: the compiler does not detect
   semantically (or even cosmetically, beyond whitespace) equivalent
   reformulations.

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
   cluster objects directory. Every attribute below is native PostgreSQL
   `CREATE ROLE`/`ALTER ROLE` grammar, parsed directly by the real PG
   parser (`CreateRoleStmt.Options`) — not a DPG-invented reformulation.
   This corrects an earlier draft of this section, which wrapped every
   attribute in a DPG-only `{ }` block (with `SUPERUSER`/`CREATEDB`/
   `CREATEROLE`/`REPLICATION`/`BYPASSRLS` incorrectly shown taking a
   boolean argument — real PG has no such form; they're toggle-pairs, e.g.
   `SUPERUSER`/`NOSUPERUSER`, no argument at all). The `{ }` block is
   reserved for genuinely DPG-only additions — currently just `COMMENT`.

   **PG equivalent:**
   `CREATE ROLE name [option [...]]`

```abnf
role-decl   = "ROLE" WSP identifier
             *( WSP role-option )
             ";"
             [ "{" role-block "}" ]

role-option = "LOGIN" / "NOLOGIN"
            / "SUPERUSER" / "NOSUPERUSER"
            / "CREATEDB" / "NOCREATEDB"
            / "CREATEROLE" / "NOCREATEROLE"
            / "INHERIT" / "NOINHERIT"
            / "REPLICATION" / "NOREPLICATION"
            / "BYPASSRLS" / "NOBYPASSRLS"
            / "CONNECTION" WSP "LIMIT" WSP integer
            / "PASSWORD" WSP SQUOTE password-literal SQUOTE
            / "VALID" WSP "UNTIL" WSP SQUOTE timestamp SQUOTE
            / "IN" WSP "ROLE" WSP role-list
            / "ROLE" WSP role-list
            / "ADMIN" WSP role-list

role-block  = *( comment-dir ";" )
```

   Any option a declaration omits is simply not managed by DPG for that
   role — offline diffing only ever compares options the source explicitly
   sets (the same "declared, so managed" convention already used elsewhere,
   e.g. Sequence's optional params), never PostgreSQL's own defaults for
   whatever was left unstated.

   `password-literal` is an ordinary string, optionally containing one or
   more `{{<secret-uri>}}` placeholders — the exact same mechanism as
   SUBSCRIPTION `CONNECTION` (§13.2, §D.5): each placeholder is resolved
   independently at apply time via `pipeline.ResolveTemplate` and
   substituted in place (the whole value, or just a fragment); a literal
   with no `{{...}}` at all never touches the resolver. Resolution happens
   once, immediately before the `CREATE ROLE`/`ALTER ROLE ... PASSWORD`
   statement executes — never during `plan`/`diff`.

   **Hardcoded passwords:** The linter MUST emit an error (not a warning)
   when `PASSWORD` is set to a value with no `{{...}}` placeholder at all
   and `forbid_hardcoded_passwords` is enabled (default: `true`). This
   supersedes an earlier draft of this rule, which named `env:VAR_NAME`
   specifically — the check is scheme-agnostic now that five backends
   exist (§D.5), not tied to one.

   **Password drift detection:** the snapshot stores a hash of the
   *declared* `PASSWORD` text (the literal or `{{...}}` reference exactly
   as written, trimmed, hashed — never the resolved value), the same
   pattern SUBSCRIPTION `CONNECTION`'s body-hash diffing already uses. This
   supersedes an earlier draft of this section, which specified storing
   only a boolean `has_password` — under that design DPG could never
   detect a rotated secret reference or changed literal, only whether a
   password existed at all. Hashing the declared text instead is no less
   safe (a literal password, if used despite the lint warning above, is
   already sitting in cleartext in the `.dpg` source file itself — hashing
   it into the snapshot exposes nothing the source didn't already) and
   enables real rotation detection: editing the `{{...}}` reference (or a
   literal) re-diffs as a genuine change.

   `PASSWORD` cannot be live-introspected at all: PostgreSQL restricts
   `pg_authid` (where role passwords, hashed, actually live) to superuser
   — confirmed live, `pg_roles.rolpassword` itself returns the fixed
   placeholder string `'********'` for any non-superuser caller regardless
   of whether a password is actually set, so `rolpassword IS NOT NULL` is
   unusable as a proxy. Every other attribute below introspects normally
   (plain, unrestricted `pg_roles`/`pg_auth_members` columns).

   Example:

```sql
-- production/cluster/roles.dpg

ROLE app_readonly NOLOGIN
{
    COMMENT 'Read-only access for reporting tools';
}

ROLE app_service
    LOGIN
    PASSWORD '{{vault:secret/roles/app_service#pw}}'
    CONNECTION LIMIT 20
    VALID UNTIL '2030-01-01';

ROLE app_admin
    LOGIN
    NOSUPERUSER
    NOCREATEDB
    NOCREATEROLE
    INHERIT
    IN ROLE pg_read_all_stats;
```

   **Diffing semantics:**

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New role | `CREATE ROLE name <options>` | `SAFE` |
   | `LOGIN`/`SUPERUSER`/`CREATEDB`/`CREATEROLE`/`INHERIT`/`REPLICATION`/`BYPASSRLS`/`CONNECTION LIMIT`/`VALID UNTIL` changed | `ALTER ROLE name <changed-options>` | `SAFE` |
   | `PASSWORD` changed | `ALTER ROLE name PASSWORD '<resolved>'` | `CAUTION` (invalidates existing sessions/connections relying on the old password) |
   | `IN ROLE`/`ROLE`/`ADMIN` membership added | `GRANT role TO member [WITH ADMIN OPTION]` | `SAFE` |
   | `IN ROLE`/`ROLE`/`ADMIN` membership removed | `REVOKE role FROM member` | `CAUTION` (may remove access something else depends on) |
   | Role removed | `DROP ROLE name` | `DESTRUCTIVE` |

   `IN ROLE`/`ROLE`/`ADMIN` are create-time-only PostgreSQL grammar — a
   later membership change has no `ALTER ROLE` equivalent, so it's diffed
   as `GRANT`/`REVOKE` (PostgreSQL's own mechanism for changing membership
   after creation), matching how DPG already diffs object-level privilege
   grants (§11.2) rather than inventing new DDL shape for this.

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

   `REVOKE` on an already-absent privilege is a no-op in PostgreSQL —
   it succeeds without error, unlike a grant declaration's removal
   (§11.2), which is the additive model's deliberate no-op.  PostgreSQL's
   `REVOKE` grammar has no `IF EXISTS` clause at all, so the compiler
   emits plain `REVOKE ... FROM role`; there is no guard to add or omit.

   The one revocation failure mode that does error is a target role
   that does not exist — the compiler does not create roles referenced
   only in a `REVOCATIONS { }` block, so a typo'd or since-dropped role
   name surfaces as a normal PostgreSQL error at apply time.

   **However**, the compiler SHOULD check the snapshot: if the
   revocation targets a role that was never granted the privilege by
   DPG, it MAY emit a warning (lint rule: `unnecessary_revocation`) —
   useful as a "did you mean to declare this" signal, independent of
   whether re-running it would error.

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
    COMMENT 'Primary replication stream for user data';
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

   Subscriptions are database-level objects, declared and diffed as an
   opaque body (byte-for-byte source text, not per-field) — the same tier
   as Tablespace/FDW/Server/Publication/etc. `CONNECTION` is the one part
   of that body DPG treats specially, to support a secret reference
   instead of a literal credential (see below and §D.5); `COMMENT` (in the
   `{ }` block) is diffed and applied like every other Comment-bearing
   object.

   `dump`/`verify`/`plan --live` see every Subscription attribute except
   `CONNECTION` itself: `pg_subscription.subconninfo` has no default grant
   to PUBLIC (PostgreSQL revokes it from a normal caller outright), and
   even a privileged caller who *can* read it has no way to recover
   whatever `{{secret-uri}}` the original `CONNECTION` clause held, if
   any — the same inherent limitation User Mapping `OPTIONS` (§14.10, §24)
   has on recovering its original reference, though User Mapping's
   redaction works field-by-field rather than by omitting a whole column,
   since `OPTIONS` also carries non-sensitive keys `dump` must still
   reconstruct. An introspected Subscription's `CONNECTION` is rendered
   as a fixed,
   syntactically-valid-but-inert placeholder conninfo instead (with
   `connect = false`, `create_slot = false`, and `enabled = false` always
   forced in its `WITH` clause, since the placeholder can never actually
   be dialed); a `dump`'d Subscription must have its `CONNECTION`
   hand-edited back to a real value before it does anything. Because the
   reconstructed body's `BodyHash` is never stored (same as every other
   reconstructed opaque kind, §25), this placeholder never causes a
   spurious `DROP SUBSCRIPTION` + `CREATE SUBSCRIPTION` loop on
   `verify`/`plan --live` — introspecting at all is what makes an
   already-applied Subscription visible as existing, rather than
   `plan --live` proposing a spurious re-`CREATE` for one that's already
   there (which would then error on `apply`).

   **PG equivalent:**
   `CREATE SUBSCRIPTION name CONNECTION 'connstr' PUBLICATION pub [, ...] [WITH (options)]`

```abnf
subscription-decl = "SUBSCRIPTION" WSP identifier
                    WSP "CONNECTION" WSP SQUOTE conn-literal SQUOTE
                    WSP "PUBLICATION" WSP identifier-list
                    [ WSP "WITH" WSP "(" sub-options ")" ]
                    ";"
                    [ "{" comment-dir ";" "}" ]
```

   `conn-literal` is an ordinary libpq conninfo string, used exactly as
   written, optionally with one or more `{{<secret-uri>}}` placeholders
   embedded in it — each resolved independently at apply time and
   substituted in place (e.g. only the password comes from a secret, the
   rest stays literal; or the whole value is one placeholder). `{{...}}` is
   the *only* mechanism that ever triggers resolution: a real conninfo
   string may itself contain a `:` (a `postgresql://user:pass@host/db` URI
   is valid libpq syntax), so nothing else is ever read as a candidate
   reference — a literal with no `{{...}}` at all never touches the
   resolver.

   Resolution happens once, immediately before executing the `CREATE
   SUBSCRIPTION` statement — never during `plan`/`diff` (offline-first is
   unaffected: a `{{...}}` reference is compared as literal text, same as
   any other body change) and never written anywhere after that: the
   snapshot, an archived migration file, and any error message all show the
   placeholder/reference form, not the resolved value. See §D.5 for the
   underlying `SecretResolver`/`ResolveTemplate` mechanism, shared with
   Role `PASSWORD` (§11.1) and User Mapping `OPTIONS` (§14.10, §24).

   Examples:

```sql
SUBSCRIPTION replica_users
    CONNECTION 'host=primary.db.internal dbname=myapp user=replicator'
    PUBLICATION user_data
    WITH (enabled = true, copy_data = true);

SUBSCRIPTION replica_orders
    CONNECTION 'host=primary.db.internal dbname=myapp user=replicator password={{vault:secret/repl/orders#pw}}'
    PUBLICATION order_data;

SUBSCRIPTION replica_billing
    CONNECTION '{{vault:secret/repl/billing#conninfo}}'
    PUBLICATION billing_data
{
    COMMENT 'Billing replica, syncs nightly';
}
```

   **Diffing semantics:**

   The Part 1 body (`CONNECTION`, `PUBLICATION`, `WITH` options) is diffed
   as an opaque whole: any change is a full `DROP SUBSCRIPTION` +
   `CREATE SUBSCRIPTION`, not a targeted `ALTER SUBSCRIPTION`, matching
   every other opaque-tier object kind (§25). `COMMENT` is diffed
   separately at the field level — a comment-only edit emits a plain
   `COMMENT ON SUBSCRIPTION`, not a drop-and-recreate, so it never
   interrupts an already-syncing subscription.

   Both `CREATE SUBSCRIPTION` (with its default `WITH (create_slot =
   true)`) and `DROP SUBSCRIPTION` error "cannot run inside a transaction
   block" in real PostgreSQL — confirmed live — so both run outside DPG's
   normal transactional migration block, unlike most DDL. A create-time
   `COMMENT ON SUBSCRIPTION` (paired with a fresh `CREATE`) is likewise
   emitted non-transactionally, so it executes strictly after the `CREATE`
   it depends on rather than being reordered ahead of it by the
   transactional/non-transactional split — also confirmed live.

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New subscription | `CREATE SUBSCRIPTION ...` [+ `COMMENT ON SUBSCRIPTION ...` if set] | `MANUAL` (non-transactional; still auto-executed, not an operator instruction) |
   | Body change (`CONNECTION`, `PUBLICATION`, or `WITH`) | `DROP SUBSCRIPTION ...` + `CREATE SUBSCRIPTION ...` [+ `COMMENT ...`] | `DESTRUCTIVE` then `MANUAL` |
   | Comment-only change | `COMMENT ON SUBSCRIPTION ... IS ...` (or `IS NULL`) | `SAFE` |
   | Subscription removed | `DROP SUBSCRIPTION name` | `DESTRUCTIVE` (non-transactional) |

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

    OPERATOR CLASS my_ops FOR TYPE mytype USING btree FAMILY my_family AS
        OPERATOR 1 <,
        OPERATOR 2 <=,
        OPERATOR 3 =,
        OPERATOR 4 >=,
        OPERATOR 5 >,
        FUNCTION 1 mytype_cmp(mytype, mytype);
}
```

   **Diffing (operator class):** Identity is `(schema, name, access_method)`.
   The body (its `AS` member list) is diffed as normalised text
   (passthrough). Any change to the member list requires drop + recreate
   (`DESTRUCTIVE`) — PostgreSQL provides no incremental `ALTER OPERATOR
   CLASS` for its `AS` members at all, so there is no safer alternative.

   **Loose family members.** PostgreSQL separately lets an operator family
   carry members that belong to the family directly rather than to any one
   of its operator classes — most often used for cross-type comparisons
   (e.g. an `int4` family also handling `int4` vs. `int8`), added via
   `ALTER OPERATOR FAMILY name USING access_method ADD OPERATOR
   strategy_num operator_name (left_type, right_type) [FOR SEARCH | FOR
   ORDER BY sort_family_name], FUNCTION support_num [(left_type,
   right_type)] function_name (argument_types), ...`. DPG never exposes
   `ALTER` as source syntax (declarations describe desired state, not
   commands — the same reason table constraints never require `ALTER TABLE
   ADD CONSTRAINT` boilerplate), so these are declared in the family's `{ }`
   block instead, using the exact same member grammar `ALTER OPERATOR
   FAMILY ... ADD` uses, just without repeating the `ALTER`/family header:

```sql
SCHEMA public {
    OPERATOR FAMILY my_family USING btree {
        OPERATOR 1 <(int4, int8),
        OPERATOR 3 =(int4, int8),
        FUNCTION 1 (int4, int8) btint48cmp(int4, int8)
    };
}
```

   `FOR ORDER BY sort_family_name` (used for GiST/SP-GiST "KNN" support —
   `btree` itself rejects it, since it has no notion of ordering operators
   distinct from its own strategies) always names a **btree** family:
   PostgreSQL requires `sort_family_name` to reference one, so it is never
   ambiguous even though the syntax itself doesn't repeat `USING btree`.

   A member's identity is `(kind, strategy/support number, left_type,
   right_type)` — matching PostgreSQL's own `pg_amop`/`pg_amproc` unique
   indexes. Op-types are always compared in their canonical form (e.g.
   `integer`, never `int4`). A `FUNCTION` item's `(left_type, right_type)`
   may be omitted only when the function takes exactly two arguments (then
   defaulting to its own argument types, PostgreSQL's own documented
   default); any other arity requires them explicitly, since the correct
   default (the owning operator class's input type) cannot be derived from
   the function's signature alone.

   **Diffing (loose family members):** unlike an operator class's `AS`
   list, PostgreSQL genuinely supports incremental member changes at the
   family level, so these are diffed per-member rather than forcing the
   whole family to drop and recreate:

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | Member added | `ALTER OPERATOR FAMILY f USING am ADD OPERATOR n op(lt,rt) [FOR SEARCH\|FOR ORDER BY sf]` / `... ADD FUNCTION n (lt,rt) fn(args)` | `SAFE` |
   | Member changed in place (same slot, different operator/function/sort target) | `ALTER ... DROP ...;` then `ALTER ... ADD ...` | `DESTRUCTIVE` |
   | Member removed | `ALTER OPERATOR FAMILY f USING am DROP OPERATOR n (lt,rt)` / `DROP FUNCTION n (lt,rt)` | `DESTRUCTIVE` |
   | Family itself dropped and recreated (body or access method changed) | every declared member re-added in full after the `CREATE` | inherits the family's own classification |

   Removal is classified `DESTRUCTIVE` even though it destroys no data:
   dropping a cross-type member silently changes query-plan shape for any
   query that relied on it (an index scan degrading to a sequential scan,
   a merge join becoming a hash join) with no PostgreSQL error anywhere,
   and the common real-world cause of "a declared member is gone" is an
   accidental source omission, not an intentional removal —
   `--allow-destructive` is the right point to require confirmation.

   A member already owned by one of the family's operator classes (i.e.
   already present in some class's own `AS` list) must never be
   re-declared in the family's `{ }` block — PostgreSQL rejects the
   resulting `ALTER ... ADD` as a duplicate. A family that has no
   declaration of its own (relying entirely on PostgreSQL's same-name
   auto-creation from an unqualified `CREATE OPERATOR CLASS`) has nowhere
   to attach loose members; declare the family explicitly first.

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
   a foreign server. Declared and diffed as an opaque body (byte-for-byte
   source text, not per-field) — the same tier as Tablespace/FDW/Server/
   Publication/etc.

   **PG equivalent:**
   `CREATE USER MAPPING [IF NOT EXISTS] FOR user SERVER server [OPTIONS (...)]`

```sql
USER MAPPING FOR app_service
    SERVER analytics_warehouse
    OPTIONS (user 'fdw_user', password '{{vault:secret/fdw/analytics#password}}');
```

   Any `OPTIONS` value MAY hold one or more `{{<secret-uri>}}` placeholders
   (§D.5) — the same mechanism as SUBSCRIPTION `CONNECTION` (§13.2) and
   Role `PASSWORD` (§11.1), and, like both of those, the only thing that
   ever triggers resolution: a literal option value with no `{{...}}` at
   all never touches the resolver. Unlike `CONNECTION`/`PASSWORD`, DPG
   doesn't isolate one specific option key to resolve — `OPTIONS` keys are
   foreign-data-wrapper-specific, not fixed by DPG, so resolution runs over
   the entire statement text, substituting whichever placeholders it finds
   regardless of which key they're under. Resolution happens once,
   immediately before `CREATE USER MAPPING` executes — never during
   `plan`/`diff`; the snapshot, an archived migration file, and any error
   message all show the placeholder/reference form, not the resolved
   value. This supersedes an earlier draft of this section, which required
   `env:VAR_NAME` specifically — scheme-agnostic now that five backends
   exist (§D.5), matching the same correction already made to Role
   `PASSWORD`'s hardcoded-password rule.

   Hardcoded passwords (an `OPTIONS` `password` value with no `{{...}}`
   placeholder at all) are rejected by the linter when
   `forbid_hardcoded_passwords` is enabled (default `true`) — implemented
   as `hardcoded-fdw-password` in the reference linter (§19.1's own table
   still names several rules with this document's snake_case rather than
   the actual kebab-case rule identifiers in code; see Appendix D.3 for
   the corrected, authoritative rule ID table).

   **Diffing semantics:** any change to the mapping is a full
   `DROP USER MAPPING` + `CREATE USER MAPPING`, not a targeted
   `ALTER USER MAPPING`, matching every other opaque-tier object kind
   (§25) — this corrects an earlier draft of this table, which described a
   targeted `ALTER` that was never actually implemented (User Mappings
   have always been diffed via the generic opaque body-hash mechanism, the
   same one Subscription used before its own §13.2 secret-reference work).

   | Change | DDL emitted | Safety |
   |--------|-------------|--------|
   | New mapping | `CREATE USER MAPPING ...` | `SAFE` |
   | OPTIONS changed | `DROP USER MAPPING ...` + `CREATE USER MAPPING ...` | `DESTRUCTIVE` then `SAFE` |
   | Mapping removed | `DROP USER MAPPING FOR user SERVER server` | `DESTRUCTIVE` |

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

   Macro preprocessing runs in three passes across the full file set:

   **Pre-pass — Global Collection:** Before processing any individual
   file, the compiler iterates over every source file and collects all
   `MACRO` declarations into a shared global store.  This pass is
   read-only; it does not modify any file and does not expand spreads.
   If the Tokenizer implements the optional `GlobalMacroSeeder`
   interface, the compiler delegates this pre-pass to the tokenizer via
   `AddGlobalMacros(src []byte)`.

   **Pass 1 — Per-file Collection:** For each source file, scan for
   `MACRO name (body)` and `MACRO name {body}` declarations.  Merge
   the file-local definitions with the global store; file-local
   definitions take precedence on name conflicts.  Emit error DPG-E007
   if a macro declaration is found inside a block.  Emit error DPG-E011
   if a name is redeclared within the same file.  Remove all `MACRO`
   declarations from the file's output text.

   **Pass 2 — Expansion:** Scan for `...name` spread operators.
   Expand each spread inline by substituting the recorded body text
   from the merged store.  Emit error DPG-E010 if a name is not found.
   Emit error DPG-E008 or DPG-E009 if the body type does not match
   the context.

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
  --cluster <name>    REQUIRED. The cluster to introspect.
  --database <name>   REQUIRED. The database to introspect.
  --output / -o <dir> Output directory for .dpg files
                      (default: <cluster>/<database>/ within project root).
                      When set, sandboxes ALL output under this directory —
                      including cluster-level roles.dpg and the snapshot,
                      not just the per-database source files — so a dump
                      run this way never touches the real project.
  --yes / -y          Skip the interactive overwrite confirmation below
                      (for scripts/CI). The warning still always prints.
```

   Overwrite protection: if any target `.dpg` file already exists on disk,
   `dpg dump` refuses to proceed silently. It prints a warning naming
   every file that would be overwritten, then requires two separate
   confirmations before continuing — an initial `[y/N]` prompt, followed
   by typing the literal word `overwrite` — matching the type-the-word
   confirmation pattern other tools use for an action this hard to
   reverse. `--yes` skips both prompts for non-interactive use, but the
   warning listing the affected files is still always printed first,
   regardless. When nothing targeted by this dump already exists (a
   brand-new project, or a fresh `-o` directory), there is no warning and
   no prompt at all — re-running `dpg dump` is exactly as safe as before
   in that case. The generated snapshot is deliberately excluded from
   this check: it is a derived/managed artifact `dump` is always expected
   to refresh, not hand-authored source.

   `--output`/`-o` sandboxing detail: without `-o`, cluster-scoped output
   (`roles.dpg`) and the snapshot are written to their real, permanent
   project locations — the cluster's own objects directory and the
   registered snapshot store — exactly as if `dpg dump` were run with no
   flags at all. With `-o <dir>` set, both are redirected under `<dir>`
   instead: `roles.dpg` to `<dir>/<cluster-name>/cluster/roles.dpg`
   (namespaced by cluster name so a single `-o` value spanning multiple
   clusters in one invocation cannot mix their roles together), and the
   snapshot to `<dir>/.dpg/snapshots/`, mirroring the real project's own
   layout convention. Earlier versions of `dpg dump` only ever redirected
   the per-database schema/`objects.dpg` files — `roles.dpg` and the
   snapshot silently ignored `-o` and always wrote to the real project
   regardless, a real, confirmed bug (not the intended behavior of `-o`,
   whose whole purpose is to let a dump be inspected without touching the
   real project) — fixed.

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
    "rule": "hardcoded-role-password",
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
   | `pg_subscription` | Subscriptions (all attributes except `subconninfo`, §13.2) |
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

   **Minimum PostgreSQL version targeting:**
   Planned for v1.1.  The compiler's internal portability annotation
   infrastructure is already in place.  Per-object version gating will
   allow users to declare `MIN_PG_VERSION = 15` in their root
   `dpg.toml` and have the compiler emit warnings (and optionally
   errors) when a declared object uses a feature unavailable on older
   servers.  The portability analyzer already classifies each IR object
   as `Standard` or `PGSpecific`; version gating extends this with a
   per-object minimum PG release map.

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

   -   Connection strings in `dpg.toml`: if `link = "env:VAR"` is used,
       the resolved value is never written to disk (via
       `pipeline.SecretResolver`/`ChainResolver`, §D.5).  If `url =` is
       used, the connection string may contain embedded credentials and
       SHOULD NOT be committed to a public repository; this is the operator's
       responsibility.
   -   Subscription `CONNECTION` strings (§13.2): MAY hold one or more
       `{{secret-uri}}` placeholders embedded in an otherwise-literal
       conninfo (or the whole value), resolved via `pipeline.ResolveTemplate`
       (§D.5) immediately before `CREATE SUBSCRIPTION` executes — never
       during `plan`/`diff`, never written to the snapshot, an archived
       migration file, or any error message. A `CONNECTION` value with no
       `{{...}}` at all is opaque literal text, same as before this
       existed — the same operator responsibility as `url =` above.
   -   Role `PASSWORD` (§11.1): same `{{secret-uri}}` mechanism as
       Subscription `CONNECTION` above. The snapshot stores a hash of the
       declared text (never the resolved value), enabling rotation
       detection — not just a boolean `has_password`, an earlier, less
       capable design this section once specified (see §11.1's own note).
   -   User Mapping `OPTIONS` (§14.10): same `{{secret-uri}}` mechanism,
       applied to the entire opaque statement text rather than one
       isolated field — `OPTIONS` keys are foreign-data-wrapper-specific,
       not fixed by DPG, so there's no single clause to target the way
       `CONNECTION`/`PASSWORD` are. The snapshot stores a hash of the
       whole declared body (never the resolved value), same as every
       other opaque-tier kind's existing diffing.

   FDW-level `OPTIONS` (as opposed to User Mapping's) are not covered —
   FDW connection details are typically non-sensitive (host, port,
   dbname), with the credential living in the User Mapping specifically;
   no case for a secret-bearing FDW-level option has come up.

   **Introspection-side redaction:** `dump`/`verify`/`plan --live` read
   the live catalog, not source, so the concern above (a resolved value
   escaping into a persisted file) applies there too. Subscription
   `CONNECTION` is handled fully: `pg_subscription.subconninfo` is never
   selected at all (§13.2), so a resolved credential can never reach a
   dumped `.dpg` file this way. User Mapping `OPTIONS` is handled the
   same way it can be, given a structural difference from Subscription:
   PostgreSQL redacts `pg_user_mappings.umoptions` to `NULL` for a
   non-owner/non-superuser caller, but shows the real, already-resolved
   value to a privileged one (the owner or a superuser) — and unlike
   `subconninfo`, `umoptions` is a set of provider-specific key/value
   pairs DPG must read individually to reconstruct valid `OPTIONS (...)`
   syntax at all, so the column can't simply be skipped the way
   `subconninfo` is. Instead, `dump` redacts password-like keys
   (`password`, `passwd`, `pwd`, `secret`, `passphrase` — matched
   case-insensitively as a substring, same heuristic as the
   `hardcoded-fdw-password` lint rule's column-default check) to a fixed,
   clearly-fake placeholder before writing OPTIONS into source; every
   other key (`user`, `dbname`, `host`, etc.) is left untouched. The
   placeholder deliberately contains no `{{...}}` marker, so if the
   dumped file is planned or applied unmodified, the existing
   `hardcoded-fdw-password` rule (see Appendix D.3 for the corrected
   rule ID table) still hard-errors on the literal
   `password` key and forces the operator to replace it with a real
   `{{secret-uri}}` reference. What remains a genuine, inherent
   limitation — not fixable by redaction — is narrower than the above:
   PostgreSQL itself only ever stores the resolved credential, never the
   original `{{secret-uri}}` reference (if any) that produced it, so
   `dump` has no way to *recover* that reference; it can only stop the
   live value from leaking into a persisted file.

   **SQL injection in generated DDL:** All identifier names read from
   source files are validated against PostgreSQL's identifier rules
   before being interpolated into generated SQL.  The compiler MUST
   quote all identifiers using PostgreSQL's double-quote quoting
   (`"identifier"`) in generated DDL to prevent injection via crafted
   identifier names.

   **SECURITY DEFINER functions:** The linter warns on `SECURITY
   DEFINER` functions lacking explicit `SET search_path` to mitigate
   search path injection attacks (rule: `security-definer-search-path`).

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
   | Column comments | Declared, Diffed | `COLUMN c { COMMENT '...'; }` |
   | Column `DEPRECATED` | Declared, Diffed | `COLUMN c { DEPRECATED '...'; }` |
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
   | Event triggers | Declared, Passthrough | Reconstructed from catalog; hash-diffed |
   | Sequences | Declared, Diffed | |
   | Schemas | Declared, Diffed | |
   | Extensions | Declared, Diffed | |
   | Roles | Declared, Diffed | Cluster-level; `PASSWORD` (§11.1) never live-introspected (superuser-only in PG), diffed offline via a hash of the declared text |
   | Table-level grants | Declared, Diffed | Additive model |
   | Column-level grants | Declared, Diffed | Additive model |
   | Explicit revocations | Declared, Diffed | |
   | Default Privileges | Declared, Diffed | |
   | Tablespaces | Declared, Passthrough | Cluster-level; reconstructed from catalog, hash-diffed |
   | Foreign Data Wrappers | Declared, Passthrough | Cluster-level; reconstructed from catalog, hash-diffed |
   | Foreign Servers | Declared, Passthrough | Reconstructed from catalog; hash-diffed |
   | User Mappings | Declared, Passthrough | Reconstructed from catalog; hash-diffed. `OPTIONS` may hold a `{{secret-uri}}` reference (§14.10, §D.5), resolved only immediately before `CREATE USER MAPPING` executes |
   | Foreign Tables | Declared, Diffed | |
   | Partitioned Tables | Declared, Diffed | |
   | Sub-partitioning | Declared, Diffed | |
   | Publications | Declared, Passthrough | Reconstructed from catalog; hash-diffed |
   | Subscriptions | Declared, Passthrough | Reconstructed from the catalog; hash-diffed. `CONNECTION` alone is never introspected (`subconninfo` has no PUBLIC grant, and even a privileged read can't recover the original `{{secret-uri}}`) — reconstructed as a fixed placeholder instead, excluded from the drift comparison like every other reconstructed body (§13.2). `CONNECTION` may hold a `{{secret-uri}}` reference in source (§13.2, §D.5), resolved only immediately before `CREATE SUBSCRIPTION` executes |
   | Collations | Declared, Passthrough | Reconstructed from catalog, hash-diffed; any change = DESTRUCTIVE |
   | Operators | Declared, Passthrough | Reconstructed from catalog, hash-diffed; any change = DESTRUCTIVE |
   | Operator Classes | Declared, Passthrough | Reconstructed from catalog, hash-diffed; any `AS` member-list change = DESTRUCTIVE (PostgreSQL has no incremental `ALTER OPERATOR CLASS`) |
   | Operator Families | Declared, Passthrough + Diffed | Header (name/access method) reconstructed from catalog, hash-diffed; loose members (§14.4, `ALTER OPERATOR FAMILY ... ADD`) are structured and diffed incrementally per member, live-path included — not gated on `Reconstructed` the way the bare header hash is |
   | Casts | Declared, Passthrough | Reconstructed from catalog, hash-diffed; any change = DESTRUCTIVE |
   | Extended Statistics Objects | Declared, Passthrough | Reconstructed from catalog; hash-diffed |
   | Text Search Configurations | Declared, Passthrough | Reconstructed from catalog; hash-diffed |
   | Text Search Dictionaries | Declared, Passthrough | Reconstructed from catalog; hash-diffed |
   | Text Search Parsers | Declared, Passthrough | Reconstructed from catalog; hash-diffed |
   | Text Search Templates | Declared, Passthrough | Reconstructed from catalog; hash-diffed |
   | Macro preprocessor | Declared, No SQL | Compile-time text expansion |
   | Cross-file macro sharing | Declared, No SQL | Macros defined in any file in the compilation scope are available to all others |
   | Rules (REWRITE) | Out of scope | Legacy |
   | `IMPORT FOREIGN SCHEMA` | Out of scope | Runtime discovery |
   | `REFRESH MATERIALIZED VIEW` | Out of scope | Runtime DML |
   | Temporary tables | Out of scope | Session-scoped |
   | Inline data seeding | Out of scope | DPG is a schema tool; data management is outside its scope |
   | Minimum PG version targeting | Deferred | See §23; planned v1.1 |

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
comment-dir      = "COMMENT" WSP SQUOTE <text> SQUOTE
renamed-from-dir = "RENAMED FROM" WSP identifier
protected-dir    = "PROTECTED"
deprecated-dir   = "DEPRECATED" WSP SQUOTE <text> SQUOTE
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
        COMMENT 'Top-level account lifecycle states';
    }

    ENUM invoice_status ('draft', 'sent', 'paid', 'void', 'overdue');
    {
        COMMENT 'Billing lifecycle states for customer invoices';
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
        COMMENT 'Top-level tenant accounts';
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
   underscores. §19.1's table also predates two rule identifiers being
   split apart and two others being renamed (both corrected below) — the
   corrected, complete rule ID table, matching `internal/linter/linter.go`
   exactly as of this writing:

   | Rule ID (actual) | Description | Default Level |
   |---|---|---|
   | `hardcoded-password` | A table column's `DEFAULT` contains a hardcoded string, for a column whose name contains `password`, `passwd`, `pwd`, `secret`, or `passphrase` (case-insensitive). | Error |
   | `hardcoded-role-password` | A `ROLE`'s `PASSWORD` is a literal value with no `{{secret-uri}}` placeholder. A separate rule from `hardcoded-password` above — different check, different object kind — despite §19.1's table conflating both under one `hardcoded_password` entry. | Error |
   | `hardcoded-fdw-password` | A `USER MAPPING`'s `OPTIONS (password '...')` is a literal value with no `{{secret-uri}}` placeholder. | Error |
   | `deprecated` | Object or column is marked `DEPRECATED`. Applied to tables, columns, views, functions. A different check from `deprecated-reference` below (that object/column being deprecated, vs. something else referencing it) — see the note below. | Warning |
   | `missing-column-comment` | Column lacks a `COMMENT` when `require_column_comments = true`. Renamed from `require-column-comments` (§19.1 named this `missing_column_comment`; the actual code now matches that wording, kebab-cased). | Warning |
   | `column-count-exceeded` | Table exceeds `max_columns_per_table` columns. Renamed from `max-columns` (§19.1 named this `column_count_exceeded`; the actual code now matches that wording, kebab-cased). | Error |
   | `security-definer-search-path` | `SECURITY DEFINER` function body does not reference `search_path`. | Warning |
   | `serial-sequence-declared` | A hand-declared `SEQUENCE` collides with the name PostgreSQL auto-manages for a `GENERATED ... AS IDENTITY` column's sequence, or for a `SERIAL`/`BIGSERIAL`/`SMALLSERIAL` column's owned sequence (`<table>_<column>_seq` in both cases) in the same desired state. Renamed from §19.1's `serial_sequence_declared`; originally scoped to `IDENTITY` only, extended to cover `SERIAL` once `ir.Column.Serial` was added — see the note below and Appendix D.11. | Warning |
   | `unnecessary-revocation` | A `REVOCATIONS` entry names a (role, privilege) pair with no matching `GRANTS` entry in the *same object's own declaration*. Renamed from §19.1's `unnecessary_revocation`; narrower in scope than that entry's wording — see the note below. | Warning |
   | `deprecated-reference` | A non-deprecated `FOREIGN KEY` references a deprecated table/column, or a non-deprecated column/function-parameter/function-return-type references a deprecated custom `TYPE`. Renamed from §19.1's `deprecated_reference`; deliberately narrower in scope than that entry's wording — see the note below. | Warning |
   | `scalar-merge-conflict` | Two files declare the same object and provide different values for the same scalar property (e.g. two files each set `TABLE t`'s `OWNER` to a different role). Renamed from §19.1's `scalar_merge_conflict`; the winning (alphabetically-last-file) value is applied regardless — this rule only adds visibility, per §3.7. See the note below for exact per-kind scope. | Warning |

   **Implementation note on `hardcoded-password` vs. `hardcoded-role-password`:**
   the column rule checks a table column's `DEFAULT` expression: if the
   column name contains any of the substrings `password`, `passwd`, `pwd`,
   `secret`, or `passphrase` (case-insensitive), AND the default
   expression is a single-quoted string literal (starts with `'`), the
   linter emits `hardcoded-password` as an error. The role rule is
   unrelated: it checks `ROLE ... PASSWORD` directly (a structured field,
   not a text-pattern match) for the absence of any `{{...}}` placeholder.

   **Note on the remaining §19.1 rules' actual implementation status:**
   three of them — `stale_renamed_from` (DPG-E021), `unguarded_enum_removal`
   (DPG-E014), and `protected_drop_attempt` (DPG-E022) — are fully
   implemented and tested, but not as `Linter.Lint`-interface rules: each
   is a hard `error` returned directly from `Differ.Diff` during the
   desired-vs-snapshot comparison (`internal/diff/differ.go`), which aborts
   the plan/apply outright rather than surfacing as a downgradable
   diagnostic. This is a real, deliberate architectural difference from
   the built-in linter rules, not a gap: `stale_renamed_from` and
   `unguarded_enum_removal` both fundamentally need a desired-vs-snapshot
   comparison to detect what they detect (a stale rename target, a
   removed enum value), which the differ already does every run and the
   linter — see §19.1's own `Linter.Lint(objects []IRObject, cfg
   LinterConfig)` signature — does not have access to at all today.
   (A prior revision of this note incorrectly stated these two had no
   implementation anywhere; corrected after re-reading `differ.go`
   directly.)

   Of the remaining four, `serial_sequence_declared`,
   `unnecessary_revocation`, `deprecated_reference`, and
   `scalar_merge_conflict` are now all implemented — the first three as
   `Linter.Lint`-interface rules (see below for their exact, and in
   `unnecessary_revocation`'s and `deprecated_reference`'s cases
   deliberately narrowed, scope). `scalar_merge_conflict` is architecturally
   different: it is computed by `pipeline.Merger.Merge` itself (the merge
   stage runs before `Linter.Lint` ever sees the objects, and the conflict
   needs to see the pre-merge, per-file values `Linter.Lint`'s
   already-merged `[]IRObject` input has thrown away), not by
   `internal/linter`'s own `checkObject`/`checkCrossObjectRules` dispatch —
   see below for the full architecture.

   **`serial_sequence_declared`:** warns when a hand-declared `SEQUENCE`
   object's name collides with the auto-managed sequence name PostgreSQL
   generates for an identity or `SERIAL`-family column in the same desired
   state (`<table>_<column>_seq`). Originally scoped to `GENERATED ... AS
   IDENTITY` columns only, using `ir.Column.Identity`; now also triggers on
   `ir.Column.Serial` now that `SERIAL`/`BIGSERIAL`/`SMALLSERIAL` are
   modeled as a distinct IR concept — see Appendix D.11 for the full
   `Column.Serial` specification, including why this was previously
   out of scope (SERIAL passed through as a literal type named `"serial"`
   with no auto-sequence relationship recorded at all).

   **`unnecessary_revocation`:** scoped to a single object's own
   declaration — warns when a `REVOCATIONS` entry names a
   (role, privilege) pair with no matching `GRANTS` entry for that same
   pair in the *same* object's declaration. This is narrower than this
   section's literal wording ("a role that was never granted the
   privilege by DPG"), which would require snapshot/grant-history access
   the linter does not have (see above) — the within-object scope still
   catches the common real mistake (a copy-pasted or typo'd revocation
   with no corresponding grant) without requiring an interface change.

   **`deprecated_reference`:** warns when a non-deprecated object
   references a deprecated one. Deliberately narrow v1 scope, chosen
   because the alternative — a lint-rule false positive — is a visible
   user-facing warning, unlike an over-matching edge in `internal/graph`'s
   dependency-ordering heuristics (harmless there; wrong here):
     - A `FOREIGN KEY` on a non-deprecated table referencing a deprecated
       table, or referencing a specific deprecated column of the target
       table (via `ir.Constraint.RefSchema`/`RefTable`/`RefColumns`,
       structured fields populated directly from the parsed `REFERENCES`
       clause at build time — not a re-parse of the constraint's rendered
       SQL text).
     - A non-deprecated column, `FUNCTION` parameter/return type, or
       `PROCEDURE` parameter, whose declared type is a deprecated custom
       `TYPE`. An unqualified type reference resolves against the
       referencing object's own schema, the same convention
       `ir.TypeRef.Schema` uses everywhere else; a real PostgreSQL built-in
       type never accidentally matches, since matching requires an actual
       index hit against a table of currently-deprecated custom types.
     - The gating gate is per-referencing-object: an already-deprecated
       table's own `FOREIGN KEY` to another deprecated table does not also
       fire this rule (its own `deprecated` diagnostic already covers it),
       and likewise for an already-deprecated column/function referencing
       a deprecated type.

   **NOT covered in v1** — flagged as a real, explicit residual rather
   than silently out of scope: a `VIEW`'s query referencing a deprecated
   table/column, a function/procedure *body* referencing a deprecated
   object, and a column `DEFAULT` expression referencing a deprecated
   object/function. All three would need either genuine SQL-AST analysis
   (no parser exists today for any of these opaque text blobs — view
   queries, function bodies, and default expressions are all stored as
   raw text) or a regex/text-scan heuristic, which risks exactly the
   false-positive-warning problem this rule design otherwise avoids.

   The `[linter.rules]` per-rule severity-override mechanism described in
   §19.2 is now implemented, following the existing `--strict` promotion
   pattern (`LintDiagnostic.IsError`) for `"error"`, and a post-filter
   step for `"off"`.

   **`scalar_merge_conflict`:** unlike every other rule in this table,
   this one is computed inside `pipeline.Merger.Merge` (`internal/merger`),
   not `internal/linter`'s object-dispatch. `Merger.Merge` returns a second
   value, `[]LintDiagnostic`, always populated regardless of
   `warn_on_scalar_merge_conflict` — gating (the config flag, and any
   `[linter.rules]` override) happens once, centrally, in
   `internal/linter.FilterMergeDiagnostics`, mirroring how
   `ApplyRuleSeverityOverrides` is the one place `[linter.rules]` gating
   lives for `Linter.Lint`'s own diagnostics. Every CLI command that calls
   `compiler.Compile` (`plan`, `apply`, `validate`) combines
   `FilterMergeDiagnostics`'s output with `Linter.Lint`'s diagnostics into
   one slice before printing/`--strict`-promoting, so a `scalar-merge-conflict`
   warning behaves identically to any other lint warning from the user's
   perspective, including under `--strict`, despite the different
   implementation location. `pkg/dpg`'s public Go API re-exports both
   `FilterMergeDiagnostics` and `ApplyRuleSeverityOverrides` so external
   consumers of `dpg.Compile` can replicate the same gating without
   reaching into `internal/linter`, which they cannot import.

   A `conflictTracker` (`internal/merger/merger.go`) is threaded through
   every scalar field this package merges: for each property, it records
   which file most recently supplied the property's current value, and
   emits a diagnostic whenever a later file supplies a genuinely different
   value for the same property — while still always applying last-file-wins
   regardless (§3.7: "the winning value is used regardless"). Tracking is
   by *last setter*, not merely "compare to the immediately preceding
   file": if file A sets a property, file B reconfirms the same value, and
   file C then disagrees, the diagnostic correctly attributes the conflict
   to file B (the file that actually owns the current value), not file A.

   Scope, mirroring exactly which fields each `mergeX` function already
   treats as a last-file-wins scalar (§3.7's own examples — owner,
   comment, tablespace, RLS flags, `PROTECTED`, `DEPRECATED`,
   `DROP CASCADE`, `RENAMED FROM`, drop behaviour — plus the equivalent
   settable properties for kinds not in that list): `TABLE`, `VIEW`,
   `FUNCTION`, `SCHEMA`, `TYPE` (including `DOMAIN`'s `BaseType`/`Default`
   fields — `NotNull` is OR-merged like every other boolean flag, not
   last-wins, so it is never conflict-checked), `PROCEDURE`, `AGGREGATE`,
   `SEQUENCE`, `EXTENSION`, `ROLE`, `TABLESPACE`, `FOREIGN DATA WRAPPER`,
   `SERVER`, and `USER MAPPING`. A `USER MAPPING`/FDW/`SERVER`'s `OPTIONS`
   list is checked key-by-key (each key is its own scalar property,
   addressed as `options[key]` in the diagnostic) rather than as one
   opaque blob, so two files setting different, non-colliding keys never
   conflict. Set-valued properties (indexes, constraints, grants,
   `ROLE`'s `IN ROLE`/`ROLE`/`ADMIN` membership lists, etc.) are unaffected
   — they already use §3.7's union semantics, not last-wins, so there is
   nothing to conflict-check.

   **NOT covered** — the opaque/reconstruction-tier object kinds
   (`PUBLICATION`, `EVENT TRIGGER`, `COLLATION`, `CAST`, `OPERATOR` and
   `OPERATOR CLASS`/`FAMILY`, the `TEXT SEARCH` kinds, `STATISTICS`, and
   similarly-shaped kinds) still fall through to `mergeGroup`'s blind
   `grp[len(grp)-1]` last-object-wins default with no field-level merging
   or conflict detection at all — unchanged from before this rule existed,
   and a real, explicit residual rather than an oversight: extending
   `conflictTracker` to these kinds is future work, not attempted in this
   round.

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
   | `pipeline.KeySecretResolver` | `pipeline.SecretResolver` | `internal/secrets.ChainResolver` (with `env:` wired in) |

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

   `ChainResolver` (`internal/secrets.ChainResolver`) is the default
   registered resolver. It is a scheme-keyed dispatch table, not a
   fallback chain that tries multiple resolvers per URI in sequence: a
   secret URI's scheme unambiguously names the one resolver responsible
   for it, so there is never a genuine "which one applies" question for a
   chain to resolve by trial. `Register(scheme, resolver)` is the
   extension point the backends below plug into (each self-registers from
   its own subpackage's `init()`; the subpackage must be imported,
   typically via a blank import in `cmd/dpg/main.go`, for its scheme to be
   available). No other scheme is recognised; `link` URIs with an
   unrecognized scheme return an error at resolution time naming the
   scheme and listing every scheme currently registered.

   Authentication for every backend below is entirely ambient — each
   resolver defers to that provider's own standard credential chain (env
   vars, config files, instance/managed identity, CLI-cached login, ...)
   and never reimplements or wraps it. Constructing a resolver never makes
   a network call and never fails just because that backend isn't
   configured; only actually resolving a URI for that scheme can fail.

   **`env:<VAR_NAME>`** (`internal/secrets.EnvResolver`)

   Resolves to `os.Getenv(VAR_NAME)`.  If the variable is not set
   (empty string), the resolver returns an error.  The variable name
   MUST consist only of ASCII letters, digits, and underscores.

   Example: `link = "env:PRODUCTION_DB_URL"` → resolves `PRODUCTION_DB_URL`
   from the process environment (which may have been populated from the
   `.env` file per §D.2.3).

   **`vault:<mount>/<path>#<field>`** (`internal/secrets/vault.Resolver`)

   Reads a HashiCorp Vault KV version 2 secret (`VAULT_ADDR`/`VAULT_TOKEN`
   ambient auth, the same convention the `vault` CLI uses). `mount` is the
   KV v2 engine's mount path, `path` is the secret's path within it. The
   `#field` suffix is REQUIRED — a Vault secret is always a key-value map,
   never a bare string, so there is no unambiguous "whole value" reading
   to fall back to. Example: `vault:secret/myapp/db#password`.

   **`aws-sm:<secret-id>[#<json-field>]`** (`internal/secrets/awssm.Resolver`)

   Reads an AWS Secrets Manager secret via the AWS SDK's default
   credential chain. `secret-id` is the secret's ARN or name (may contain
   `/`). Without `#json-field`, resolves to the secret's raw
   `SecretString`. With it, `SecretString` is parsed as JSON and that key
   extracted. Examples: `aws-sm:myapp/prod/db-password`,
   `aws-sm:myapp/prod/db#password`.

   **`gcp-sm:<project>/<secret-id>[/<version>][#<json-field>]`**
   (`internal/secrets/gcpsm.Resolver`)

   Reads a GCP Secret Manager secret via Application Default Credentials.
   `version` defaults to `"latest"` if omitted. Without `#json-field`,
   resolves to the raw payload as UTF-8 text; with it, the payload is
   parsed as JSON and that key extracted. Example:
   `gcp-sm:my-project/db-password`.

   **`azure-kv:<vault-name>/<secret-name>[/<version>][#<json-field>]`**
   (`internal/secrets/azurekv.Resolver`)

   Reads an Azure Key Vault secret via `DefaultAzureCredential`.
   `vault-name` resolves to `https://<vault-name>.vault.azure.net/`.
   `version` omitted (or empty) means the latest version. Without
   `#json-field`, resolves to the raw secret value; with it, the value is
   parsed as JSON and that key extracted. Example:
   `azure-kv:my-vault/db-password`.

   **Embedding a reference inside a literal: `{{<secret-uri>}}`**
   (`pipeline.ResolveTemplate`)

   The five schemes above resolve a URI that occupies an *entire* field
   (`link` in `dpg.toml`). Some fields are themselves a larger literal that
   only a *part* of should come from a secret — a SUBSCRIPTION `CONNECTION`
   conninfo string being the first case (§13.2): most of it (host, dbname,
   user) is not sensitive, and forcing the whole value into a secret
   (duplicating the non-sensitive parts into the backend, or requiring a
   backend-side template) is worse than just resolving the one sensitive
   part in place. `pipeline.ResolveTemplate(s, resolver)` scans `s` for
   `{{<secret-uri>}}` placeholders — any of the six schemes above, or a
   future one — and replaces each with `resolver.Resolve(<secret-uri>)`,
   leaving everything else untouched; `s` with no `{{...}}` at all never
   touches the resolver, so a plain literal has zero behavioral change and
   zero performance cost. This is a general mechanism, not
   SUBSCRIPTION-specific — also used by Role `PASSWORD` (§11.1) and User
   Mapping `OPTIONS` (§14.10).
   `{{...}}` is deliberately the *only* trigger for resolution in such a
   field: unlike `link`, a real literal in one of these fields may itself
   contain a `:` (a conninfo string can be a `postgresql://user:pass@host/db`
   URI), so treating any colon-prefixed substring as a candidate secret
   reference would risk misreading a literal as broken. Curly braces never
   appear in legitimate conninfo/option syntax, so `{{...}}` cannot collide.

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

### D.10. Name Maps

   Name Maps provide a mechanism for attaching tool-specific naming
   conventions to DPG schema objects.  They allow downstream consumers
   — ORM generators, type-safe query builders, API code generators —
   to transform DPG identifier names into the naming convention
   appropriate for their target language or framework, without
   requiring any changes to the DPG source files.

   Name Maps operate at two orthogonal layers:

   -   **Configuration layer** (`dpg.toml`): project-wide defaults,
       settable at root, cluster, and database scope.
   -   **Block layer** (the `{ }` DPG block): per-object or per-column
       overrides declared inline in the source file.

   Resolution is most-specific-wins: block-level beats database
   config, database config beats cluster config, cluster config beats
   root config.  Resolution is applied independently per tool key per
   object type.

#### D.10.1. Rule Keywords

   DPG defines a closed set of naming-convention rule keywords.  All
   keywords are case-insensitive in TOML config but stored and
   validated in their canonical upper-case form.  A rule keyword MUST
   be written without quotation marks in the DPG block syntax.

   | Rule Keyword | Example transformation of `user_profile_id` |
   |---|---|
   | `LOWER_SNAKE_CASE` | `user_profile_id` |
   | `UPPER_SNAKE_CASE` | `USER_PROFILE_ID` |
   | `LOWER_CAMEL_CASE` | `userProfileId` |
   | `UPPER_CAMEL_CASE` | `UserProfileId` |
   | `LOWER_KEBAB_CASE` | `user-profile-id` |
   | `UPPER_KEBAB_CASE` | `USER-PROFILE-ID` |
   | `TRAIN_CASE` | `User-Profile-Id` |
   | `LOWER_CASE` | `userprofileid` |
   | `UPPER_CASE` | `USERPROFILEID` |
   | `PASCAL_SNAKE_CASE` | `User_Profile_Id` |

   A literal target name (not a rule) is written as a double-quoted
   identifier in the DPG block syntax (e.g., `"MySpecialName"`).
   Literal names are stored verbatim in the snapshot and are passed
   through to consumers unchanged.  Literal names are only supported
   in the block layer, not in `dpg.toml`.

   The reserved tool key `default` MUST NOT be used as an actual tool
   name.  It represents the catch-all rule applied by any tool for
   which no explicit rule is configured.

#### D.10.2. Configuration Layer (`dpg.toml`)

   All three configuration files (root, cluster, database) accept a
   `[namemaps]` section.  Within this section:

   -   Scalar string entries are **global rules** keyed by tool name.
   -   Subtable entries (`[namemaps.<type>]`) are **per-object-type
       rules** keyed by tool name, where `<type>` matches the DPG
       object-type identifier (e.g., `column`, `table`, `view`).

   Deeper configuration scopes override shallower ones for the same
   (tool, type) pair.

```toml
# Root dpg.toml — project-wide defaults
[namemaps]
default  = "LOWER_SNAKE_CASE"   # catch-all for all tools
prisma   = "LOWER_CAMEL_CASE"   # Prisma-specific global rule
drizzle  = "LOWER_CAMEL_CASE"

[namemaps.table]
prisma  = "UPPER_CAMEL_CASE"    # Prisma table names: PascalCase
drizzle = "LOWER_CAMEL_CASE"    # Drizzle table names: camelCase

[namemaps.column]
prisma  = "LOWER_CAMEL_CASE"
drizzle = "LOWER_CAMEL_CASE"
```

```toml
# Cluster or database dpg.toml — narrows/overrides root config
[namemaps]
sqlc = "LOWER_SNAKE_CASE"

[namemaps.function]
sqlc = "LOWER_SNAKE_CASE"
```

   Rules MUST be one of the ten keywords listed in §D.10.1.  Unknown
   keywords MUST cause a validation error (DPG-E030).  Literal names
   are NOT supported in `dpg.toml`; only rule keywords are accepted.

#### D.10.3. Block Layer — Inline Directives

   Name map directives appear inside any DPG `{ }` block (object-level
   or column-level).  Two forms are supported.

**Singular form:**

```
NAME MAP TO <rule> ;
NAME MAP <tool> TO <rule> ;
NAME MAP <tool> TO "LiteralName" ;
```

   -   When `<tool>` is omitted, the directive applies to the `default`
       tool key.
   -   `<rule>` is an unquoted rule keyword (case-insensitive).
   -   `"LiteralName"` is a double-quoted literal target identifier.
       Double quotes follow the PostgreSQL identifier quoting convention
       (identifiers, not string values).

**Grouped form:**

```
NAME MAPS {
  <tool> TO <rule> ;
  <tool> TO "LiteralName" ;
  ...
}
```

   -   The `NAME MAPS { }` form requires explicit tool names; the
       implicit `default` shorthand is NOT available inside the group.
   -   Multiple singular `NAME MAP` directives and `NAME MAPS` blocks
       MAY be mixed freely within the same `{ }` block.
   -   If the same tool key appears more than once in a single `{ }`
       block the last entry wins.

**Examples:**

```sql
TABLE users (
  id       BIGINT GENERATED ALWAYS AS IDENTITY,
  email    TEXT NOT NULL,
  username TEXT NOT NULL
) {
  -- All tools: use LOWER_SNAKE_CASE (default catch-all)
  NAME MAP TO LOWER_SNAKE_CASE;

  -- Prisma and drizzle: override to camelCase
  NAME MAPS {
    prisma  TO LOWER_CAMEL_CASE;
    drizzle TO LOWER_CAMEL_CASE;
  }

  COLUMNS {
    username {
      -- Override: literal name for the sqlc tool only
      NAME MAP sqlc TO "UserName";
    }
  }
}
```

```sql
ENUM user_status ('active', 'inactive', 'banned') {
  NAME MAP prisma TO UPPER_CAMEL_CASE;
}
```

#### D.10.4. Snapshot Representation

   Name maps are serialised to the snapshot `name_maps` array field
   present on all typed snap objects (`SnapTable`, `SnapColumn`,
   `SnapView`, `SnapFunction`, `SnapType`, `SnapSchema`, `SnapExtension`,
   `SnapSequence`, `SnapRole`, `SnapVirtualType`, `SnapOpaque`).

   Each entry is a `SnapNameMapEntry`:

```json
{
  "tool": "prisma",
  "rule": "LOWER_CAMEL_CASE"
}
```

   or, for a literal name:

```json
{
  "tool": "sqlc",
  "name": "UserName"
}
```

   Fields:

   | Field | Type | Required | Description |
   |-------|------|----------|-------------|
   | `tool` | string | YES | Tool key (e.g., `"prisma"`, `"default"`). |
   | `rule` | string | one of rule/name | One of the ten rule keywords. Present when the value is a rule. |
   | `name` | string | one of rule/name | Literal target identifier. Present when the value is a literal. |

   Exactly one of `rule` or `name` MUST be non-empty.  Both being
   populated in the same entry is invalid.  The `name_maps` field is
   omitted entirely from the JSON when the array is empty (`omitempty`).

   The differ does not generate SQL for name map changes — name maps
   are metadata annotations consumed by downstream tools, not by
   PostgreSQL.  A change to `name_maps` triggers a snapshot update
   only (no SQL emitted, no migration step added).

#### D.10.5. Error Codes

   | Error ID | Code String | Condition |
   |----------|-------------|-----------|
   | DPG-E030 | `invalid_namemap_rule` | Unknown rule keyword in `[namemaps]` config or `NAME MAP` block directive. |
   | DPG-E031 | `duplicate_namemap_tool` | Same tool key specified more than once at the same block level (warning only; last entry wins). |

---

### D.11. SERIAL / BIGSERIAL / SMALLSERIAL Column Sugar (amends §7.2)

   §7.2's `col-constraint` ABNF and surrounding prose describe
   `GENERATED ALWAYS/BY DEFAULT AS IDENTITY` in detail but say nothing
   about PostgreSQL's older `SERIAL`/`BIGSERIAL`/`SMALLSERIAL`
   pseudo-types, which are syntactically just an ordinary `type-ref`
   (§Appendix A) and so were always accepted by the parser. Until this
   revision, DPG gave them no distinct treatment anywhere past parsing:
   a column declared `SERIAL` passed through the compiler as a literal
   type named `"serial"` — not a real PostgreSQL catalog type — with no
   record of the owned-sequence relationship, and diffing a live
   `SERIAL` column against such a snapshot produced spurious
   `ALTER COLUMN TYPE` drift. This section specifies the sugar's actual
   modeled behavior.

   **Normalization:** the compiler MUST rewrite a column's declared
   type from a `SERIAL`-family spelling to the real underlying integer
   type PostgreSQL itself stores, per the following table, and record
   which spelling was used as a sibling marker rather than replacing
   `Column.Type`:

   | Declared spelling(s) | Underlying `Column.Type` | Marker (`Column.Serial`) |
   |---|---|---|
   | `SERIAL`, `SERIAL4` | `integer` | `"SERIAL"` |
   | `SMALLSERIAL`, `SERIAL2` | `smallint` | `"SMALLSERIAL"` |
   | `BIGSERIAL`, `SERIAL8` | `bigint` | `"BIGSERIAL"` |

   `Column.Serial` is a sibling marker on the IR `Column` type,
   analogous to `Column.Identity`: it augments `Column.Type` rather than
   replacing it, so every other part of the pipeline that reasons about
   a column's real storage type (index/constraint compatibility checks,
   cast validation, dump rendering of dependent objects) continues to
   see the true underlying integer type. A schema-qualified type merely
   named `serial` (e.g. `myschema.serial`) is a real user type reference
   and MUST NOT be mistaken for the pseudo-type — only an unqualified,
   case-insensitive match against the table above triggers this rule.

   **`SERIAL` implies `NOT NULL`:** real PostgreSQL makes every
   `SERIAL`-family column `NOT NULL` unconditionally, independent of
   whether it is also `PRIMARY KEY` — the same shape as the existing
   PRIMARY-KEY-implies-NOT-NULL rule in §7.2. The compiler MUST set
   `Column.NotNull = true` for any `SERIAL`-family column regardless of
   other constraints present in source.

   **Emitted DDL:** `CREATE TABLE` and `ADD COLUMN` MUST render the
   literal `SERIAL`/`BIGSERIAL`/`SMALLSERIAL` keyword for a column with
   `Column.Serial` set, in place of the normalized underlying type, and
   MUST suppress the `NOT NULL` and `DEFAULT` clauses that would
   otherwise be rendered — PostgreSQL synthesizes both itself as part of
   expanding the pseudo-type. This mirrors how `GENERATED AS IDENTITY`
   columns already suppress those same clauses. `ADD COLUMN` for a
   `SERIAL`-family column is classified `SAFE` (not `CAUTION`), the same
   exemption already given to identity columns, since no data rewrite or
   implicit cast is involved.

   **Introspection:** a column is reconstructed as `SERIAL`-family when
   its owning table has a `pg_depend` entry of deptype `'a'`
   ("auto", i.e. internally-created and owned) linking it to a sequence
   whose default expression is a `nextval(...)` call on that same
   sequence, mirroring the existing `GENERATED AS IDENTITY` detection
   shape. `Column.Default` is left `nil` in this case, exactly as it
   already is for identity columns — the owned-sequence default is not
   surfaced as an ordinary `Default` expression. A hand-rolled
   owned-sequence-plus-`nextval()`-default on a non-integer column (legal
   PostgreSQL, not `SERIAL` sugar) MUST fall through to ordinary
   `Default` handling rather than being misdetected.

   **Dump reconstruction:** `dpg dump` MUST render a reconstructed
   `SERIAL`-family column using the literal keyword form above, not the
   normalized underlying type with a hand-written
   `DEFAULT nextval('<table>_<column>_seq')` — the latter is
   non-reapplicable output, since dump does not separately declare the
   owned sequence as its own `SEQUENCE` object (the same treatment
   identity-backed sequences already receive).

   **Snapshot representation:** `SnapColumn` gains an optional `serial`
   string field (JSON key `"serial"`, `omitempty`), populated with the
   same three-value marker as `Column.Serial`. A snapshot written before
   this revision has no `serial` field on any column and, for a
   previously-declared `SERIAL` column, stores the literal type name
   `"serial"`/`"bigserial"`/`"smallserial"` instead of the normalized
   underlying type — the differ MUST recognize this legacy shape
   specifically (declared column has `Column.Serial` set and the
   snapshot's stored type name is one of those three legacy spellings)
   and treat it as already matching, rather than emitting a destructive
   `ALTER COLUMN TYPE`. This is a one-time self-healing comparison: the
   next snapshot write after any successful `plan`/`apply` stores the
   normalized type and marker going forward, and the legacy-name check
   never applies again for that column.

   **Linter interaction:** the `serial-sequence-declared` rule
   (Appendix D.3) triggers on `Column.Serial` in addition to
   `Column.Identity` — a hand-declared `SEQUENCE` colliding with a
   `SERIAL` column's auto-managed sequence name is exactly as much a
   mistake as the identity case, and both share the same
   `<table>_<column>_seq` naming collision surface.

---

## Appendix E. Revision History

   This appendix records all substantive changes to this document after
   its initial publication.

   | Revision | Date | Description |
   |----------|------|-------------|
   | E.1 | 2026-05-13 | Initial publication. Formal IETF-style RFC superseding the informal design document `rfc/v0.8.0.md`. All sections written from scratch with normative RFC 2119 language, ABNF grammars, and exhaustive per-object specifications. |
   | E.2 | 2026-05-13 | Appendix D added. Corrections to §16 (snapshot wire format: `SnapObject` discriminated union, `SnapOpaque`, corrected field names), §18 (`--format text` default, `--watch` flag, `.env` loading protocol, `planJSON` schema, target auto-selection), §19 (linter rule IDs use hyphens). Pipeline Registry key constants table and SecretResolver protocol specification added. Source revision detection algorithm formalised. |
   | E.3 | 2026-05-13 | §D.8–§D.9 added. Root `dpg.toml` `[fmt]` and `[migrations]` sections documented. CLI corrections: `dpg validate` JSON schema, `dpg portability` flag set, `dpg init` default cluster name (`"production"`), `dpg fmt` TOML key names. ToC updated to include Appendix D subsections. |
   | E.4 | 2026-05-17 | §D.10 added. Name Maps feature specified: ten rule keywords, `[namemaps]` TOML config at all three levels (global + per-object-type rules), inline `NAME MAP` and `NAME MAPS` block directives, literal target name support via double-quoted identifiers, resolution order (block > database > cluster > root), snapshot `name_maps` array field on all object types, error codes DPG-E030 and DPG-E031. |
   | E.5 | 2026-08-16 | §D.11 added. `SERIAL`/`BIGSERIAL`/`SMALLSERIAL` column sugar specified as a first-class IR concept (`Column.Serial`, sibling marker to `Column.Type`): normalization table, `SERIAL`-implies-`NOT NULL` rule, literal-keyword emission with suppressed `NOT NULL`/`DEFAULT`, `pg_depend`-based introspection detection mirroring identity columns, non-reapplicable dump output fixed, `SnapColumn.serial` field, and a legacy-snapshot self-healing comparison for pre-existing snapshots that stored the literal `"serial"` type name. §D.3's `serial-sequence-declared` entry updated: now also triggers on `Column.Serial`, not `Column.Identity` only. |

---

*End of RFC 1 — Declarative PG (DPG) v0.8.1*
