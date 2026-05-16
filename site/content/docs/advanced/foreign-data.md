---
title: "Foreign Data"
description: "FOREIGN DATA WRAPPER, SERVER, USER MAPPING, and FOREIGN TABLE declarations."
weight: 8
---

## FOREIGN DATA WRAPPER

In the common case, FDWs are installed via extension (`EXTENSION postgres_fdw;`). The explicit declaration is reserved for custom C-implemented FDWs. Place in the cluster objects directory.

```sql
FOREIGN DATA WRAPPER myfdw
    HANDLER   myfdw_handler
    VALIDATOR myfdw_validator;
```

```sql
-- emits
CREATE FOREIGN DATA WRAPPER myfdw
    HANDLER   myfdw_handler
    VALIDATOR myfdw_validator;
```

---

## SERVER

Servers are database-level objects that reference a foreign data wrapper.

```sql
SERVER analytics_warehouse
    FOREIGN DATA WRAPPER postgres_fdw
    OPTIONS (host 'warehouse.internal', dbname 'analytics', port '5432');
```

```sql
-- emits
CREATE SERVER "analytics_warehouse"
    FOREIGN DATA WRAPPER postgres_fdw
    OPTIONS (host 'warehouse.internal', dbname 'analytics', port '5432');
```

---

## USER MAPPING

```sql
USER MAPPING FOR app_service
    SERVER analytics_warehouse
    OPTIONS (user 'fdw_user', password 'env:FDW_PASSWORD');
```

```sql
-- emits
CREATE USER MAPPING FOR "app_service"
    SERVER "analytics_warehouse"
    OPTIONS (user 'fdw_user', password 'env:FDW_PASSWORD');
```

---

## FOREIGN TABLE

`SERVER` and `OPTIONS` are PG SQL clauses on the `CREATE FOREIGN TABLE` signature — they stay in Part 1 per the [two-part syntax](../../fundamentals/two-part-syntax/) rules.

```sql
FOREIGN TABLE remote_events (
    id         BIGINT,
    payload    JSONB,
    created_at TIMESTAMPTZ
) SERVER log_server OPTIONS (table_name 'events', schema_name 'public')
{
    COLUMN id { COMMENT "Remote event primary key"; }
    GRANTS { SELECT TO app_readonly; }
}
```

```sql
-- emits
CREATE FOREIGN TABLE "public"."remote_events" (
    "id"         bigint,
    "payload"    jsonb,
    "created_at" timestamptz
) SERVER "log_server" OPTIONS (table_name 'events', schema_name 'public');

COMMENT ON COLUMN "public"."remote_events"."id" IS 'Remote event primary key';
GRANT SELECT ON "public"."remote_events" TO "app_readonly";
```

## Diffing behaviour

- `SERVER OPTIONS` changes: `ALTER SERVER OPTIONS`.
- `USER MAPPING OPTIONS` changes: `ALTER USER MAPPING OPTIONS`.
- Foreign table column changes: `ALTER FOREIGN TABLE`.
- All foreign data objects removed: `DROP` statements — `SAFE` (no local data stored).
