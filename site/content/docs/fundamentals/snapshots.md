---
title: "Snapshots & Diffing"
description: "How DPG computes migrations offline by diffing source against a committed snapshot, and how safety classification works."
weight: 4
---

DPG never requires a live database connection to generate a migration. The primary workflow diffs `.dpg` source files against a committed JSON snapshot.

## Workflow

```
.dpg source files  ──compile──▶  desired state
                                       │
                                       ▼  diff
.dpg/snapshots/prod/myapp.json ───────▶│
                                       ▼
                                 migration SQL
```

`dpg apply` executes the SQL and updates the snapshot atomically. The snapshot is always the post-apply state.

## Migration modes

```bash
dpg plan                                     # source vs. committed snapshot (no DB)
dpg plan --live                              # source vs. live catalog
dpg diff --from schemas/v1/ --to schemas/v2/ # two source directories, no snapshot
```

## Snapshot format

`.dpg/snapshots/<cluster>/<database>.json` — commit this file.

The `objects` field is a flat map keyed by each object's qualified name (e.g. `"public.users"`, `"public.get_user(text)"`). Every value is a discriminated union: a `kind` string selects which sibling field holds the object's data.

```json
{
  "dpg_version": "0.5.2",
  "cluster": "production",
  "database": "myapp",
  "applied_at": "2025-09-15T14:32:00Z",
  "source_revision": "a3f7c91",
  "objects": {
    "public": {
      "kind": "schema",
      "schema": { "name": "public" }
    },
    "public.users": {
      "kind": "table",
      "table": {
        "schema": "public",
        "name": "users",
        "owner": "app_role",
        "comment": "Primary identity store",
        "rls_enabled": true,
        "columns": [
          {
            "name": "id",
            "type": "bigint",
            "not_null": true,
            "identity": "ALWAYS"
          },
          {
            "name": "email",
            "type": "text",
            "not_null": true,
            "statistics": 300,
            "grants": [
              { "privileges": ["SELECT"], "roles": ["reporting_role"] }
            ]
          }
        ],
        "constraints": [
          { "name": "pk_users",       "type": "primary_key" },
          { "name": "uq_users_email", "type": "unique" }
        ],
        "indexes": [
          { "name": "idx_users_email", "method": "btree", "columns": "email" }
        ],
        "grants": [
          { "privileges": ["SELECT"], "roles": ["app_readonly"] }
        ]
      }
    },
    "public.get_user(text)": {
      "kind": "function",
      "function": {
        "schema": "public",
        "name": "get_user",
        "args": "text",
        "return_type": "users",
        "language": "plpgsql",
        "volatility": "volatile",
        "body_hash": "a3f7c91d8e2b...",
        "grants": [
          { "privileges": ["EXECUTE"], "roles": ["app_service"] }
        ]
      }
    }
  }
}
```

Function and procedure bodies are stored as a SHA-256 hex digest (`body_hash`). Any change to the body text causes `CREATE OR REPLACE FUNCTION` to be emitted.

### Key field notes

| Field | Type | Notes |
|-------|------|-------|
| `not_null` | bool | Omitted when false (Go zero-value omitempty) |
| `identity` | string | `"ALWAYS"` or `"BY DEFAULT"` |
| `statistics` | int | Per-column `ALTER COLUMN … SET STATISTICS` target |
| `indexes[].columns` | string | Comma-separated column list |
| `grants[].roles` | string array | One or more grantees |
| `grants[].privileges` | string array | Omitted means `ALL` |

Objects whose state is entirely captured by their body text (procedures, aggregates, tablespaces, FDW, foreign servers, event triggers, collations, operators, text search objects, etc.) are stored as `"kind": "<type>"` with an `opaque` field containing only `kind`, `name`, `schema`, and `body_hash`.

## Migration output format

```sql
-- DPG Migration
-- Generated:       2025-09-15T14:32:00Z
-- Source revision: a3f7c91
-- Cluster:         production
-- Database:        myapp

BEGIN;

-- [source: schemas/public/tables/users.dpg:4]
CREATE TABLE "public"."users" (
    "id"    bigint GENERATED ALWAYS AS IDENTITY,
    "email" text NOT NULL,
    CONSTRAINT "pk_users"       PRIMARY KEY ("id"),
    CONSTRAINT "uq_users_email" UNIQUE ("email")
);

COMMIT;

-- Non-transactional steps (executed after COMMIT):
-- [source: schemas/public/tables/users.dpg:22]
CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_users_email" ON "public"."users" ("email");
```

Source file references (`[source: ...]`) appear before every statement group.

## Safety classification

| Class | Meaning | Default behavior |
|-------|---------|-----------------|
| `SAFE` | No data loss possible | Applied automatically |
| `CAUTION` | Locks acquired; performance impact possible | Applied with warning logged |
| `DESTRUCTIVE` | Data loss possible | Blocked unless `--allow-destructive` |
| `MANUAL` | Cannot run inside a transaction, or requires manual operator action | Executable `MANUAL` ops emitted after `COMMIT`; instruction-only `MANUAL` ops printed but never executed |

Concurrent index creation is always `MANUAL` — emitted as a non-transactional step after `COMMIT`. Partition strategy changes are also `MANUAL` and require `--approve-partition-rebuild`.

## Idempotency guarantee

Running `dpg apply` on a database that already matches the declared state produces zero SQL statements. Any violation is a compiler bug.

## Grants model

DPG only emits `GRANT`. It never auto-revokes. Removing a `GRANTS` entry from source emits nothing. Add an explicit `REVOCATIONS { }` block to revoke. See [Grants](../../access-control/grants/).

`dpg verify` reports as drift any DPG-declared grant absent from the live catalog. It does not report extra grants present in the live catalog but absent from source.
