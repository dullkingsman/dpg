# DPG — Declarative PG

DPG is a declarative, state-based superset of PostgreSQL SQL that compiles to idiomatic PG DDL. You describe what your database *should be*; DPG figures out what needs to change.

```
SCHEMA public {
    TABLE users (
        id    BIGINT GENERATED ALWAYS AS IDENTITY,
        email TEXT   NOT NULL,
        CONSTRAINT pk_users       PRIMARY KEY (id),
        CONSTRAINT uq_users_email UNIQUE (email)
    )
    {
        INDICES { idx_users_email (email); }
        GRANTS  { SELECT TO app_readonly; SELECT, INSERT, UPDATE TO app_service; }
        ENABLE ROW LEVEL SECURITY;
    }
}
```

Run `dpg plan` — DPG computes the minimal SQL to reach that state:

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
ALTER TABLE "public"."users" ENABLE ROW LEVEL SECURITY;

COMMIT;

-- Non-transactional steps (executed after COMMIT):
CREATE INDEX CONCURRENTLY "idx_users_email" ON "public"."users" ("email");
```

If the database already matches, the output is `(no changes)`. No-ops are a first-class result.

## Why DPG

PostgreSQL DDL is imperative. Migration files describe *actions taken at a point in time* — not the current intended state. The result: drift, no single source of truth, and non-idempotent history. DPG flips the model. Source files describe *desired state*. The compiler computes the delta.

DPG source files are valid PostgreSQL SQL with verbs (`CREATE`, `ALTER`, `DROP`) removed and a `{ }` block appended for sub-objects (indexes, policies, grants, comments). Existing PG tooling, syntax highlighters, and institutional knowledge apply directly.

## Quick Start

```bash
# Build and install
git clone https://github.com/dullkingsman/dpg
cd dpg
make install

# Start from an existing database
dpg dump --cluster prod --database myapp

# Or create project structure manually and run
dpg plan                        # diff source vs snapshot (no DB required)
dpg apply                       # run the migration, update snapshot
dpg verify                      # check live DB for drift
dpg portability                 # report PG-specific constructs
```

## Documentation

| Document | Contents |
|---|---|
| [Installation](installation.md) | Build requirements, `make` targets, installing binaries |
| [Project Structure](reference/project.md) | Directory layout, `dpg.toml`, cluster and database config files |
| [Language Reference](reference/language.md) | Two-part syntax model, no-verb mandate, semicolons, dollar-quoting, schema scoping, merge rules, dependency ordering |
| [Object Reference](reference/objects.md) | Every object type: tables, views, functions, types, sequences, roles, grants, indexes, RLS, triggers, FTS, replication, advanced PG objects |
| [CLI Reference](reference/commands.md) | All six commands (`plan`, `apply`, `verify`, `dump`, `diff`, `portability`) with every flag |
| [Linter](reference/linter.md) | All lint rules, configuration keys, error vs warning classification |
| [Portability Analysis](reference/portability.md) | All flagged constructs, standard SQL alternatives |
| [Lifecycle Directives](reference/lifecycle.md) | `RENAMED FROM`, `DEPRECATED`, `PROTECTED`, `DROP CASCADE`, `MIGRATE REMOVE` |
| [Secrets](reference/secrets.md) | `env:`, `link:`, plain-value passthrough |
| [Snapshot Format](reference/snapshot.md) | JSON structure, VCS commit strategy, how `apply` updates it |

## Core Concepts in 90 Seconds

**Offline-first.** `dpg plan` and `dpg diff` never touch a database. They compare `.dpg` source files against a committed snapshot. CI pipelines review migrations before any database is reached.

**Idempotent.** Applying the same plan twice produces zero SQL on the second run.

**Safety-classified.** Every generated statement is tagged `SAFE`, `CAUTION`, `DESTRUCTIVE`, or `MANUAL`. Destructive operations are blocked by default; they require `--allow-destructive` to proceed.

**Additive grants.** DPG only emits `GRANT`. It never auto-revokes. Removing a `GRANTS` entry emits nothing — write an explicit `REVOCATIONS { }` block to revoke.

**No new DSL.** Part 1 of every declaration is valid PostgreSQL DDL syntax with the leading `CREATE` verb stripped. `FUNCTION` bodies, `VIEW` queries, `TABLE` column lists — all are written exactly as PostgreSQL requires.
