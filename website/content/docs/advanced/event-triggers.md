---
title: "Event Triggers"
description: "EVENT TRIGGER declarations that fire on DDL events."
weight: 3
---

Event triggers are cluster-level and fire on DDL events. They are declared at the top level of a `.dpg` file (not inside a schema block).

## Basic event trigger

```sql
EVENT TRIGGER prevent_drop_table
    ON sql_drop
    WHEN TAG IN ('DROP TABLE', 'DROP SCHEMA')
    EXECUTE FUNCTION abort_drop();
```

```sql
-- emits
CREATE EVENT TRIGGER "prevent_drop_table"
    ON sql_drop
    WHEN TAG IN ('DROP TABLE', 'DROP SCHEMA')
    EXECUTE FUNCTION abort_drop();
```

## ddl_command_start trigger

```sql
EVENT TRIGGER audit_ddl
    ON ddl_command_start
    EXECUTE FUNCTION log_ddl_start();
```

```sql
-- emits
CREATE EVENT TRIGGER "audit_ddl"
    ON ddl_command_start
    EXECUTE FUNCTION log_ddl_start();
```

## Available events

| Event | Fires when |
|-------|-----------|
| `ddl_command_start` | Before any DDL command executes |
| `ddl_command_end` | After any DDL command completes |
| `sql_drop` | Before objects are dropped |
| `table_rewrite` | Before a table is rewritten |

## Diffing behaviour

- New event trigger: `CREATE EVENT TRIGGER`.
- Changed function or event: `DROP EVENT TRIGGER` + `CREATE EVENT TRIGGER` — `CAUTION`.
- Removed event trigger: `DROP EVENT TRIGGER` — `SAFE`.
