---
title: "Drift Detection"
description: "How `dpg verify` compares the live database catalog against the committed snapshot to detect unmanaged changes."
weight: 3
---

`dpg verify` introspects the live PostgreSQL catalog and compares it against the committed snapshot. Exit code `0` means the live database matches the declared state. Exit code `1` means drift was detected.

## Running verify

```bash
dpg verify
# ✓ No drift detected

dpg verify --cluster production --database myapp
# ✗ Drift detected:
#   table public.orders: column "priority" present in live DB, absent from snapshot
#   grant: SELECT on public.users to rogue_role present in live DB, absent from snapshot
```

## What counts as drift

| Condition | Reported |
|-----------|----------|
| Object in live DB but absent from snapshot | Yes |
| Object in snapshot but absent from live DB | Yes |
| DPG-declared grant absent from live catalog | Yes |
| Extra grant in live catalog not in snapshot | No — additive model |

The additive grants model means `dpg verify` never flags extra grants that exist in the live DB but were not declared in DPG source. This matches the behaviour of [grants](../../access-control/grants/).

## Catalog tables read

`dpg verify` reads: `pg_class`, `pg_attribute`, `pg_constraint`, `pg_index`, `pg_proc`, `pg_trigger`, `pg_policy`, `pg_type`, `pg_enum`, `pg_namespace`, `pg_extension`, `pg_publication`, `pg_subscription`, `pg_foreign_table`, `pg_foreign_server`, `pg_user_mapping`, `pg_foreign_data_wrapper`, `pg_statistic_ext`, `pg_event_trigger`, `pg_collation`, `pg_operator`, `pg_opclass`, `pg_opfamily`, `pg_cast`, `pg_rewrite`, `pg_partitioned_table`, `pg_inherits`, `pg_sequence`, `pg_ts_config`, `pg_ts_dict`, `pg_ts_parser`, `pg_ts_template`. Column-level grants via `information_schema.column_privileges`.

## Resolving drift

Three options:

1. **Apply a migration** — if drift is unintended, run `dpg apply` to reconcile the live DB to the declared state.
2. **Update source** — if the live change is intentional, update `.dpg` source to match the live state, then run `dpg plan` to verify the diff is empty.
3. **Re-snapshot** — use `dpg dump` to re-bootstrap source from the live catalog if the live state is the desired state.

## Relation to `dpg plan --live`

`dpg plan --live` also connects to the live catalog but computes a migration rather than reporting drift. Use `dpg verify` for alerting in a monitoring context; use `dpg plan --live` when you want the corrective SQL.
