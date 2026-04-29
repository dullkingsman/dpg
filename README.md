# DPG — Declarative PG

> Describe the database you want. DPG figures out how to get there.

DPG is a declarative, state-based schema compiler for PostgreSQL. Instead of writing migration files, you describe the desired state of your database in `.dpg` source files. DPG computes the minimal, safe SQL required to reach that state from wherever things currently are.

```sql
TABLE users (
    id    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email TEXT   NOT NULL UNIQUE
) {
    INDEX idx_users_email (email);
    GRANTS { SELECT TO app_readonly; SELECT, INSERT, UPDATE TO app_service; }
};
```

Run `dpg plan`:

```sql
BEGIN;

CREATE TABLE "public"."users" (
    "id"    bigint GENERATED ALWAYS AS IDENTITY,
    "email" text NOT NULL,
    CONSTRAINT "pk_users" PRIMARY KEY ("id"),
    CONSTRAINT "uq_users_email" UNIQUE ("email")
);

GRANT SELECT ON TABLE "public"."users" TO "app_readonly";
GRANT SELECT, INSERT, UPDATE ON TABLE "public"."users" TO "app_service";

COMMIT;

-- Non-transactional (executed after COMMIT):
CREATE INDEX CONCURRENTLY "idx_users_email" ON "public"."users" ("email");
```

If the database already matches, the output is `(no changes)`. No-ops are a first-class result.

## Why DPG

PostgreSQL DDL is imperative. Migration files describe *actions taken at a point in time* — not the current intended state. The result is drift, no single source of truth, and a history that is non-idempotent by construction.

DPG flips the model:

- **Source files describe desired state.** The compiler derives the delta.
- **Offline-first.** `dpg plan` and `dpg diff` never touch a database. CI can review migrations before any server is reached.
- **Safety-classified output.** Every generated statement is tagged `SAFE`, `CAUTION`, `DESTRUCTIVE`, or `MANUAL`. Destructive operations are blocked by default.
- **No new language.** DPG syntax is valid PostgreSQL DDL with the leading verb removed and a `{ }` block appended for sub-objects (indexes, grants, RLS policies, comments). Existing tooling, syntax highlighting, and institutional knowledge apply directly.
- **Idempotent.** Applying the same plan twice produces zero SQL on the second run.

## Installation

**Requirements:** Go 1.25+, a C compiler (GCC or Clang) — `pg_query_go` links against the real PostgreSQL parser via CGo.

```bash
git clone https://github.com/dullkingsman/dpg
cd dpg
make install       # installs dpg to $GOPATH/bin
```

See [docs/installation.md](docs/installation.md) for cross-compilation, pre-built binaries, and all `make` targets.

## Quick Start

```bash
# Bootstrap from an existing database
dpg dump --cluster prod --database myapp

# Declare schema changes in .dpg source files, then:
dpg plan           # preview the SQL (no database connection required)
dpg apply          # execute the migration and update the snapshot
dpg verify         # detect live drift against the committed snapshot
```

## Commands

| Command | Description |
|---|---|
| `dpg plan` | Diff source files vs committed snapshot; print SQL. No DB required. |
| `dpg apply` | Lint, diff, prompt for approval, execute SQL, update snapshot. |
| `dpg verify` | Connect to live DB and report any drift from the snapshot. |
| `dpg dump` | Introspect a live DB and generate initial `.dpg` source files. |
| `dpg diff` | Diff two `.dpg` source directories and print the SQL between them. |
| `dpg portability` | Report PostgreSQL-specific constructs that reduce portability. |

All commands accept `--cluster` and `--database` flags when a project has multiple targets. See [docs/reference/commands.md](docs/reference/commands.md) for the full flag reference.

## Project Layout

```
myproject/
├── dpg.toml                         # Root config
├── prod/                            # Cluster directory
│   ├── dpg.toml                     # Cluster config (connection url or secret link)
│   ├── cluster/                     # Cluster-level objects (roles, tablespaces)
│   └── myapp/                       # Database directory
│       ├── dpg.toml                 # Database config
│       └── schemas/
│           └── public/
│               ├── tables.dpg
│               ├── views.dpg
│               └── functions.dpg
└── .dpg/
    └── snapshots/                   # Committed snapshot (source of truth)
```

See [docs/reference/project.md](docs/reference/project.md) for the full project structure reference.

## Documentation

| Document | Contents |
|---|---|
| [Installation](docs/installation.md) | Build requirements, make targets, cross-compilation |
| [Project Structure](docs/reference/project.md) | Directory layout, dpg.toml, cluster and database config |
| [Language Reference](docs/reference/language.md) | Two-part syntax, schema scoping, merge rules, dependency ordering |
| [Object Reference](docs/reference/objects.md) | Tables, views, functions, types, sequences, roles, indexes, RLS, grants |
| [CLI Reference](docs/reference/commands.md) | All commands and flags |
| [Linter](docs/reference/linter.md) | Lint rules and configuration |
| [Lifecycle Directives](docs/reference/lifecycle.md) | RENAMED FROM, DEPRECATED, PROTECTED, DROP CASCADE |
| [Secrets](docs/reference/secrets.md) | env:, link:, plain-value passthrough |
| [Snapshot Format](docs/reference/snapshot.md) | JSON structure and VCS commit strategy |
| [Portability](docs/reference/portability.md) | PostgreSQL-specific constructs and standard SQL alternatives |
| [RFC v0.8.0](rfc/v0.8.0.md) | Full language specification |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE) — Copyright (c) 2026 Daniel Tsegaw
