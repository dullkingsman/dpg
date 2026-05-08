---
title: "CLI Reference"
generated: false
weight: 9
description: "All dpg CLI commands with flags, output formats, and examples."
---


## Global Flags

| Flag | Description |
|---|---|
| `-C, --dir <dir>` | Project root directory. Default: current working directory. |
| `--version` | Print version, commit, and build date. |
| `--help` | Print help. |

## `dpg plan`

Compares `.dpg` source files against the committed snapshot and prints the minimal SQL migration required to reach the declared state. **No database connection required.**

The linter runs automatically before diffing. Lint errors abort the command; lint warnings are printed to stderr and do not block.

```
dpg plan [--cluster <name>] [--database <name>] [--format text|json] [--watch] [--live] [-C <dir>]
```

| Flag | Description |
|---|---|
| `--cluster <name>` | Limit to one cluster. Default: all clusters. |
| `--database <name>` | Limit to one database. Default: all databases. |
| `--format text\|json` | Output format. Default: `text`. Use `json` for machine-readable output. |
| `--watch` | Re-run automatically whenever source files change (polls every 500 ms). |
| `--live` | Diff against the live database instead of the stored snapshot. Requires a database connection. |

**Output:**

```sql
-- DPG Migration
-- Generated:       2025-09-15T14:32:00Z
-- Source revision: a3f7c91
-- Cluster:         production
-- Database:        myapp

BEGIN;

CREATE TABLE IF NOT EXISTS public.users ( ... );
GRANT SELECT ON TABLE public.users TO app_readonly;

COMMIT;

-- Non-transactional steps (executed after COMMIT):
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_users_email ON public.users (email);
```

When no changes are required, the output is `(no changes)`. No-ops are a first-class result — a clean plan is as meaningful as a non-empty one.

**Safety labels:** Each generated statement is tagged `SAFE`, `CAUTION`, `DESTRUCTIVE`, or `MANUAL` (non-transactional). These appear as inline comments in the output.

**Exit codes:** `0` on success (including empty plan), non-zero on lint errors or compile errors.

---

## `dpg apply`

Runs the linter and diff (identical to `plan`), prints the migration SQL, prompts for approval, executes the SQL against the primary node, and updates the committed snapshot on success.

```
dpg apply [--cluster <name>] [--database <name>] [--yes] [--allow-destructive]
          [--approve-partition-rebuild] [-C <dir>]
```

| Flag | Description |
|---|---|
| `--cluster <name>` | Limit to one cluster. |
| `--database <name>` | Limit to one database. |
| `-y, --yes` | Skip the interactive approval prompt. |
| `--allow-destructive` | Allow `DESTRUCTIVE` operations. Default: blocked. |
| `--approve-partition-rebuild` | Acknowledge partition strategy changes. Required when the plan contains a partition strategy change (e.g. `PARTITION BY` method changed). These operations are shown in the plan but are never executed automatically — they require manual operator action outside DPG. |

**Approval prompt:**

```
Apply this migration? [y/N]
```

If the user answers anything other than `y` or `Y`, the migration is aborted and the snapshot is not updated.

**Snapshot update:** If execution succeeds, the snapshot file at `.dpg/snapshots/<cluster>/<database>.json` is rewritten to reflect the new state. This file should be committed to version control.

**Destructive operations:** Any operation classified `DESTRUCTIVE` causes `apply` to abort with an error unless `--allow-destructive` is passed. The error message names the first destructive statement encountered.

**Non-transactional steps:** Statements emitted after `COMMIT` (primarily concurrent index builds) are executed outside the transaction. If the transactional block succeeds but a non-transactional step fails, the snapshot is not updated and the step must be re-applied manually or by re-running `dpg apply`.

---

## `dpg verify`

Connects to the primary node, introspects the live database catalog, and compares it against the committed snapshot. Reports any drift — objects present in the snapshot but absent from the live catalog, and DPG-managed grants missing from the live catalog.

```
dpg verify [--cluster <name>] [--database <name>] [-C <dir>]
```

| Flag | Description |
|---|---|
| `--cluster <name>` | Limit to one cluster. |
| `--database <name>` | Limit to one database. |

**Under the additive grant model:** `verify` reports DPG-declared grants that are missing from the live catalog. It does not report extra grants present in the live catalog but absent from DPG source — those are intentionally not managed by DPG.

**Exit codes:** `0` if no drift found, non-zero if drift is detected. Suitable for use in health checks and monitoring scripts.

---

## `dpg dump`

Connects to the primary node, introspects the live catalog, and writes `.dpg` source files and an initial snapshot to the output directory. Use this to bootstrap a DPG project from an existing database.

```
dpg dump --cluster <name> --database <name> [--output <dir>] [-C <dir>]
```

| Flag | Description |
|---|---|
| `--cluster <name>` | Cluster to dump. Required when multiple clusters exist. |
| `--database <name>` | Database to dump. Required when multiple databases exist. |
| `-o, --output <dir>` | Output directory. Default: `<project-root>/<cluster>/<database>/`. |

**Output structure:**

```
<output-dir>/
├── schemas/
│   ├── public/
│   │   └── schema.dpg    # all objects in the public schema
│   └── analytics/
│       └── schema.dpg
```

One `.dpg` file is written per schema. Functions are output as stubs with a comment noting the body is omitted — retrieve function bodies from `pg_proc.prosrc` manually and paste them in.

The snapshot is written to `.dpg/snapshots/<cluster>/<database>.json` automatically.

---

## `dpg diff`

Compares two DPG database-scoped source directories and prints the SQL migration required to go from the `--from` state to the `--to` state. **No snapshot or database connection required.**

```
dpg diff --from <dir> --to <dir>
```

| Flag | Description |
|---|---|
| `--from <dir>` | Directory representing the base state. **Required.** |
| `--to <dir>` | Directory representing the desired state. **Required.** |

Both directories must contain `.dpg` files for the same logical database. All `.dpg` files are collected recursively.

**Use cases:**
- Reviewing migrations between feature branches without a live database.
- Comparing schema versions stored as directory snapshots in CI.
- Generating a migration script for offline review before deployment.

```bash
dpg diff --from schemas/v1/ --to schemas/v2/
dpg diff --from releases/2024-q1/ --to releases/2024-q2/
```

---

## `dpg validate`

Compiles all `.dpg` source files and runs the linter. **No database connection or snapshot required.** Use this as a fast offline check before running `dpg plan`.

```
dpg validate [--cluster <name>] [--database <name>] [--format text|json] [-C <dir>]
```

| Flag | Description |
|---|---|
| `--cluster <name>` | Limit to one cluster. Default: all clusters. |
| `--database <name>` | Limit to one database. Default: all databases. |
| `--format text\|json` | Output format. Default: `text`. Use `json` for machine-readable output. |

**Text output:**
```
production/(cluster): 3 object(s) — OK
production/myapp: 12 object(s) — OK
```

Cluster-level objects (roles, tablespaces, FDWs) are reported under the `(cluster)` scope before per-database results.

**JSON output (`--format json`):**
```json
{
  "cluster": "production",
  "database": "myapp",
  "objects": 12,
  "errors": [],
  "warnings": [
    {
      "rule": "deprecated",
      "message": "table legacy_sessions is deprecated",
      "file": "schemas/public/tables.dpg",
      "line": 42,
      "col": 1
    }
  ]
}
```

**Exit codes:** `0` if no errors, non-zero on compile or lint errors. Warnings do not cause a non-zero exit.

---

## `dpg init`

Scaffold a new DPG project with the standard directory layout and minimal configuration files.

```
dpg init [dir] [--cluster <name>] [--database <name>] [--schema <name>] [--url <conn>]
```

| Flag | Description |
|---|---|
| `dir` | Target directory. Default: current directory. |
| `--cluster <name>` | Cluster directory name. Default: `production`. |
| `--database <name>` | Database directory name. Default: `myapp`. |
| `--schema <name>` | Default schema directory. Default: `public`. |
| `--url <conn>` | PostgreSQL connection URL. Can be set later in `dpg.toml`. |

**Created layout:**
```
dpg.toml
<cluster>/
  dpg.toml
  cluster/
  <database>/
    dpg.toml
    schemas/<schema>/
.dpg/
  snapshots/
```

Existing files are skipped rather than overwritten.

---

## `dpg portability`

Parses the `.dpg` source files and reports all PostgreSQL-specific constructs in use, along with standard SQL alternatives where available. This command never blocks compilation or apply — it is purely informational.

```
dpg portability [--cluster <name>] [--database <name>] [-C <dir>]
```

| Flag | Description |
|---|---|
| `--cluster <name>` | Limit to one cluster. |
| `--database <name>` | Limit to one database. |

**Output example:**

```
-- production/myapp: 4 portability issue(s)

  [schemas/public/types.dpg:3] CREATE TYPE AS ENUM
    → PG ENUM is non-standard; use a lookup table with a FK constraint.

  [schemas/public/tables/events.dpg:6] UNLOGGED TABLE
    → Not in SQL standard; use regular TABLE for portability.

  [schemas/public/tables/events.dpg:9] jsonb
    → Use JSON (standard) instead of JSONB for portability.

  [schemas/public/functions.dpg:2] LANGUAGE plpgsql
    → PL/pgSQL is PG-specific; use SQL functions for portability.
```

See [Portability Analysis](portability.md) for the complete list of flagged constructs.

---

## Safety Classification

Every generated SQL statement is assigned one of four safety classes:

| Class | Description | Default behavior |
|---|---|---|
| `SAFE` | No data loss possible | Applied automatically |
| `CAUTION` | Locks acquired; performance impact possible | Applied with warning logged |
| `DESTRUCTIVE` | Data loss possible (DROP TABLE, DROP COLUMN, etc.) | Blocked unless `--allow-destructive` |
| `MANUAL` | Cannot run inside a transaction (concurrent index creation), or describes a manual operator step (partition strategy change) | Executable `MANUAL` ops are emitted after `COMMIT` as non-transactional steps. Instruction-only `MANUAL` ops (shown with a `--` comment, e.g. partition strategy changes) are displayed in the plan but **never executed** — the operator must perform them manually outside DPG. `--approve-partition-rebuild` is required to acknowledge these. |

Examples:
- `CREATE TABLE` → SAFE
- `ALTER TABLE ADD COLUMN` → SAFE
- `ALTER TABLE DROP COLUMN` → DESTRUCTIVE
- `DROP TABLE` → DESTRUCTIVE
- `CREATE INDEX CONCURRENTLY` → MANUAL
- `ALTER TABLE ALTER COLUMN TYPE` → CAUTION (may require a table rewrite)

---

## `dpg completion`

Generate a shell completion script for Bash, Zsh, Fish, or PowerShell. The script is generated by Cobra and printed to stdout for sourcing into your shell profile.

```
dpg completion bash
dpg completion zsh
dpg completion fish
dpg completion powershell
```

**Bash:**
```bash
echo 'source <(dpg completion bash)' >> ~/.bashrc
```

**Zsh:**
```zsh
echo 'source <(dpg completion zsh)' >> ~/.zshrc
```

**Fish:**
```fish
dpg completion fish | source
```

See `dpg completion <shell> --help` for shell-specific installation instructions.
