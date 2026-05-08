---
title: "Snapshot Format"
generated: false
weight: 5
description: "Snapshot format, storage location, VCS commit strategy, and how dpg apply updates it."
---


The snapshot is the source of truth for offline diffing. It represents the last successfully applied state of a database as known to DPG. Snapshots are committed to version control alongside source files.

## Location

```
.dpg/snapshots/<cluster>/<database>.json
```

Configured via `dpg.toml`:

```toml
[snapshots]
directory = ".dpg/snapshots"   # default
```

## When Snapshots Are Created and Updated

| Event | Snapshot behavior |
|---|---|
| `dpg apply` succeeds | Snapshot is rewritten to reflect the new state |
| `dpg dump` runs | Snapshot is written from the introspected live catalog |
| `dpg plan` | Snapshot is read, never written |
| `dpg diff` | No snapshot involved |
| `dpg verify` | Snapshot is read, never written |
| `cluster.options.snapshot_on_apply = true` | Same as above — this is the default |

## JSON Structure

```json
{
  "dpg_version": "0.1.0",
  "cluster": "production",
  "database": "myapp",
  "applied_at": "2025-09-15T14:32:00Z",
  "source_revision": "a3f7c91",
  "schemas": {
    "public": {
      "tables": {
        "users": {
          "owner": "app_role",
          "comment": "Primary identity store",
          "rls_enabled": true,
          "columns": {
            "id": {
              "type": "bigint",
              "identity": { "generation": "always", "start": 1, "increment": 1 },
              "nullable": false
            },
            "email": {
              "type": "text",
              "nullable": false,
              "comment": "Verified email address",
              "statistics_target": 300,
              "grants": [
                { "grantee": "reporting_role", "privileges": ["SELECT"] }
              ]
            }
          },
          "constraints": {
            "pk_users":       { "type": "primary_key", "columns": ["id"] },
            "uq_users_email": { "type": "unique",      "columns": ["email"] }
          },
          "indexes": {
            "idx_users_email": {
              "method":    "btree",
              "columns":   [{ "name": "email", "direction": "asc" }],
              "unique":    false,
              "predicate": null
            }
          },
          "grants": [
            { "grantee": "app_readonly", "privileges": ["SELECT"] },
            { "grantee": "app_service",  "privileges": ["SELECT", "INSERT", "UPDATE"] }
          ]
        }
      },
      "functions": {
        "get_user(text)": {
          "return_type": "users",
          "language":    "plpgsql",
          "attributes":  ["stable", "security_definer"],
          "set_options": { "search_path": "public" },
          "body_hash":   "sha256:a3f7c91...",
          "comment":     "Fetch a user by verified email address",
          "grants": [{ "grantee": "app_service", "privileges": ["EXECUTE"] }]
        }
      }
    }
  }
}
```

## Key Design Decisions

**Function bodies are hashed, not stored.** The snapshot stores a SHA-256 hash of the normalised function body (`body_hash`), not the full text. This keeps snapshots compact and prevents the snapshot from being an alternative source of function definitions. Change detection compares hashes — any body change triggers `CREATE OR REPLACE FUNCTION`.

**Grants are additive.** The `grants` arrays in the snapshot reflect only DPG-declared grants. They are not a complete representation of the live database's privilege state. Extra grants applied manually outside DPG are not tracked and are not reported as drift by `dpg verify`.

**Column-level grants are nested.** Column grants appear inside the column's object, not alongside table-level grants.

## VCS Commit Strategy

The recommended workflow:

1. Edit `.dpg` source files.
2. Run `dpg plan` to review the generated migration.
3. If the plan looks correct, run `dpg apply` — the snapshot is updated automatically.
4. Commit both the `.dpg` source changes and the updated snapshot together in the same commit.

This means every commit that changes schema simultaneously updates the snapshot. Pull requests include both the source change and the snapshot change, making migration review part of code review.

**Never edit snapshot files manually.** They are compiler output. Manual edits will cause `dpg plan` to generate incorrect or spurious migrations on the next run.

## Multi-Database Projects

Each database gets its own snapshot file, organized under a per-cluster subdirectory:

```
.dpg/snapshots/
├── production/
│   ├── myapp.json
│   └── analytics_db.json
└── staging/
    └── myapp.json
```

Running `dpg apply` updates only the snapshot for the cluster/database being applied.
