---
title: "Tablespaces"
description: "TABLESPACE declarations — cluster-level objects that map a name to a filesystem location."
weight: 9
---

Tablespaces are cluster-level objects. Declare them in the cluster objects directory (e.g. `production/cluster/tablespaces.dpg`). See [Project Structure](../../fundamentals/project-structure/).

## Basic tablespace

```sql
TABLESPACE fast_ssd LOCATION '/mnt/nvme/pg_data';
TABLESPACE archive  LOCATION '/mnt/hdd/pg_archive';
```

```sql
-- emits
CREATE TABLESPACE "fast_ssd" LOCATION '/mnt/nvme/pg_data';
CREATE TABLESPACE "archive"  LOCATION '/mnt/hdd/pg_archive';
```

## Tablespace with owner

```sql
TABLESPACE fast_ssd LOCATION '/mnt/nvme/pg_data'
{
    OWNER "pg_admin";
}
```

```sql
-- emits
CREATE TABLESPACE "fast_ssd" LOCATION '/mnt/nvme/pg_data';
ALTER TABLESPACE "fast_ssd" OWNER TO "pg_admin";
```

## Using a tablespace

Reference the tablespace name in a table, index, or materialized view declaration:

```sql
TABLE events ( ... ) TABLESPACE fast_ssd { ... }
```

```sql
{ INDICES { idx_events_ts (created_at) TABLESPACE archive; } }
```

See [Tables Overview](../../schema-objects/tables/overview/) and [Indexes](../../schema-objects/tables/indexes/).

## Diffing behaviour

- New tablespace: `CREATE TABLESPACE`.
- Owner change: `ALTER TABLESPACE OWNER TO`.
- Removed tablespace: `DROP TABLESPACE` — `DESTRUCTIVE`. The compiler additionally emits a warning comment that the drop will fail at the PostgreSQL level if any objects still reside in the tablespace.
