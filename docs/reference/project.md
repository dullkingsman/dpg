# Project Structure and Configuration

## Directory Layout

A DPG project maps onto the physical topology of a PostgreSQL deployment: one or more **clusters**, each containing one or more **databases**.

```
myproject/
├── dpg.toml                          # Root: tool configuration and linter rules
│
├── production.dpg.toml               # Cluster config for the "production" cluster
├── production/                        # Cluster directory
│   ├── cluster/                       # Cluster-level objects (roles, tablespaces, FDWs)
│   │   ├── roles.dpg
│   │   └── tablespaces.dpg
│   │
│   ├── myapp.dpg.toml                 # Database config for "myapp" on "production"
│   ├── myapp/                         # Database source files
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
│   ├── analytics_db.dpg.toml
│   └── analytics_db/
│       └── ...
│
├── staging.dpg.toml
├── staging/
│   └── ...
│
└── .dpg/
    └── snapshots/
        ├── production.myapp.json      # Committed snapshot: production/myapp
        └── production.analytics_db.json
```

**Rules:**
- The cluster directory name must match the cluster `name` in its `.dpg.toml`.
- The database directory name must match the database `name` in its `.dpg.toml`.
- The `cluster/` subdirectory (default name, configurable) is reserved for cluster-level objects. No database may share this name.
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

## `<cluster-name>.dpg.toml` — Cluster Configuration

One file per cluster, placed in the project root alongside `dpg.toml`.

```toml
[cluster]
# Must match the cluster directory name.
name = "production"

# Subdirectory within the cluster directory holding cluster-level objects.
# Default: "cluster". Reserved name — no database may use this name.
cluster_objects_dir = "cluster"

[[cluster.nodes]]
name = "primary-1"
# Inline connection string. Mutually exclusive with "link".
url  = "postgresql://pguser@primary.prod.internal:5432/postgres"
# "primary": writable; target of dpg apply. Exactly one node must be "primary".
role = "primary"

[[cluster.nodes]]
name = "replica-1"
url  = "postgresql://pguser@replica-1.prod.internal:5432/postgres"
# "replica": read-only; used by dpg verify for catalog introspection.
role = "replica"

[[cluster.nodes]]
name = "replica-2"
# Secrets-provider URI. Resolved at connection time by the configured SecretResolver.
# Mutually exclusive with "url". Supported schemes: "env:", "link:".
link = "env:REPLICA_2_URL"
role = "replica"

[cluster.options]
# Write an updated snapshot after every successful dpg apply.
snapshot_on_apply = true
```

**Node fields:**

| Field | Type | Description |
|---|---|---|
| `name` | string | Human-readable node label. Used in log output. |
| `url` | string | PostgreSQL connection string (DSN or URL format). Mutually exclusive with `link`. |
| `link` | string | Secrets-provider URI. See [Secrets](secrets.md) for supported schemes. Mutually exclusive with `url`. |
| `role` | string | `"primary"` or `"replica"`. Exactly one node must be `"primary"`. |

**Validation errors:**
- Exactly one node must have `role = "primary"`.
- `url` and `link` are mutually exclusive; one is required.
- Unknown role values are rejected at startup.

## `<db-name>.dpg.toml` — Database Configuration

One file per database, placed inside the cluster directory.

```toml
[database]
# Must match the database directory name.
name = "myapp"

# The default schema for unqualified object references.
default_schema = "public"
```

## Cluster-Level vs. Database-Level Objects

**Cluster-level objects** belong to the PostgreSQL cluster (server), not to a single database. They are declared in `.dpg` files inside the cluster objects directory (`cluster/` by default).

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
