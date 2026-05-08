---
title: "Project Structure and Configuration"
generated: false
weight: 8
---


## Directory Layout

A DPG project maps onto the physical topology of a PostgreSQL deployment: one or more **clusters**, each containing one or more **databases**.

```
myproject/
├── dpg.toml                          # Root: tool configuration and linter rules
│
├── production/                        # Cluster directory
│   ├── dpg.toml                       # Cluster config (connection, options)
│   │
│   ├── cluster/                       # Cluster-level objects (roles, tablespaces, FDWs)
│   │   ├── roles.dpg
│   │   └── tablespaces.dpg
│   │
│   ├── myapp/                         # Database directory
│   │   ├── dpg.toml                   # Database config
│   │   ├── extensions.dpg
│   │   └── schemas/
│   │       ├── public/
│   │       │   ├── types.dpg
│   │       │   ├── tables/
│   │       │   │   ├── users.dpg
│   │       │   │   └── orders.dpg
│   │       │   ├── views.dpg
│   │       │   └── functions.dpg
│   │       └── analytics/
│   │           └── tables.dpg
│   │
│   └── analytics_db/
│       ├── dpg.toml
│       └── ...
│
├── staging/
│   ├── dpg.toml
│   └── ...
│
└── .dpg/
    └── snapshots/
        └── production/
            ├── myapp.json             # Committed snapshot: production/myapp
            └── analytics_db.json
```

**Rules:**
- Each cluster is a subdirectory of the project root containing a `dpg.toml`.
- Each database is a subdirectory of its cluster directory containing a `dpg.toml`.
- The cluster name is declared inside `<cluster>/dpg.toml`, not derived from the directory name.
- The database name is declared inside `<cluster>/<db>/dpg.toml`.
- The `cluster/` subdirectory (default name, configurable) is reserved for cluster-level objects. No database may share this name, and it must not contain a `dpg.toml`.
- All `.dpg` files in a database directory and its subdirectories are compiled together as a single unit.
- File ordering within a database does not matter — the compiler resolves dependencies before emitting SQL.
- Snapshot files are committed to version control. They represent the last successfully applied state.

## `dpg.toml` — Root Configuration

Place `dpg.toml` in the project root directory. All fields are optional; defaults are shown.

```toml
[compiler]
# How DROPs are qualified: "restrict" or "cascade".
default_drop_behavior = "restrict"

# Whether new indexes on existing tables use CREATE INDEX CONCURRENTLY by default.
concurrent_indexes = true

[linter]
# Warn when a table, column, view, or function is marked DEPRECATED.
warn_on_deprecated = true

# Require every column to have a COMMENT directive in its COLUMN block.
require_column_comments = false

# Error when a column whose name contains "password", "passwd", "pwd", "secret",
# or "passphrase" has a string-literal DEFAULT value.
forbid_hardcoded_passwords = true

# Error when a table has more columns than this limit. 0 = no limit.
max_columns_per_table = 50

# Warn when the same scalar property is declared in multiple files for the same
# object (last-declaration-wins applies, but may indicate a conflict).
warn_on_scalar_merge_conflict = true

[snapshots]
# Directory (relative to project root) where snapshot JSON files are stored.
directory = ".dpg/snapshots"
```

## `<cluster>/dpg.toml` — Cluster Configuration

One file per cluster, placed inside the cluster directory.

```toml
[cluster]
# Cluster name. Used in snapshot filenames and log output.
name = "production"

# Subdirectory within the cluster directory holding cluster-level objects.
# Default: "cluster". Reserved — no database may use this name.
cluster_objects_dir = "cluster"

# Inline PostgreSQL connection string for the primary node.
# Mutually exclusive with link.
url = "postgresql://pguser@primary.prod.internal:5432/postgres"

# Alternatively, a secrets-provider URI resolved at runtime:
# link = "env:PRIMARY_DB_URL"

[cluster.options]
# Write an updated snapshot after every successful dpg apply.
snapshot_on_apply = true
```

**Connection fields:**

| Field | Type | Description |
|---|---|---|
| `url` | string | Inline PostgreSQL connection string (DSN or URL). Mutually exclusive with `link`. |
| `link` | string | Secrets-provider URI resolved at connection time. See [Secrets](secrets.md). Mutually exclusive with `url`. |

Both `url` and `link` are optional for offline-only use cases (`dpg plan`, `dpg diff`). Commands that connect to the database (`dpg apply`, `dpg verify`, `dpg dump`) require one to be set.

**Validation errors:**
- `url` and `link` are mutually exclusive.

## `<cluster>/<db>/dpg.toml` — Database Configuration

One file per database, placed inside the database directory.

```toml
[database]
# Must match the intended PostgreSQL database name.
name = "myapp"

# The default schema for unqualified object references.
default_schema = "public"
```

## Discovery Algorithm

When any `dpg` command runs, it:

1. Walks up from the working directory (or `--dir`) until it finds a `dpg.toml` containing root-level configuration. That directory is the project root.
2. Scans immediate subdirectories of the project root. Any subdirectory containing a `dpg.toml` is a cluster.
3. For each cluster, scans its immediate subdirectories. Any subdirectory containing a `dpg.toml` that is not the cluster objects directory is a database.
4. Collects all `.dpg` files recursively within each database directory as that database's source files.

Hidden directories (names beginning with `.`) are skipped at every level.

## Cluster-Level vs. Database-Level Objects

**Cluster-level objects** belong to the PostgreSQL cluster (server), not to a single database. They are declared in `.dpg` files inside the cluster objects directory (`cluster/` by default). This directory does not contain a `dpg.toml` and is never treated as a database.

Cluster-level objects:
- Roles (`ROLE`)
- Tablespaces (`TABLESPACE`)
- Custom foreign data wrappers (`FOREIGN DATA WRAPPER`)

**Database-level objects** are scoped to a specific database and declared in that database's source directory:
- Everything else: schemas, tables, views, functions, types, sequences, extensions, publications, subscriptions, foreign servers, user mappings, etc.

## Source File Conventions

DPG does not require any specific file names or directory structure within a database directory. The following conventions are recommended:

```
myapp/
├── dpg.toml               # Database config
├── extensions.dpg         # EXTENSION declarations
├── schemas/
│   ├── public/
│   │   ├── types.dpg      # ENUM, composite, range, domain types
│   │   ├── tables/        # One file per table, or all tables together
│   │   │   ├── users.dpg
│   │   │   └── orders.dpg
│   │   ├── views.dpg      # VIEW and MATERIALIZED VIEW
│   │   └── functions.dpg  # FUNCTION and PROCEDURE
│   └── analytics/
│       └── tables.dpg
```

All `.dpg` files within a database directory tree are compiled as a single unit regardless of directory depth or file name.
