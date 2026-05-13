---
title: "Indexes"
description: "All index methods, partial indexes, expression indexes, covering indexes, and CONCURRENTLY behaviour."
weight: 4
---

Indexes are declared in the `INDICES { }` block inside a table's `{ }`. By default, DPG emits `CREATE INDEX CONCURRENTLY` for additions on existing tables — emitted as a non-transactional step after `COMMIT`. To disable concurrent creation for a specific index, use `CONCURRENTLY false`.

## Standard btree index

```sql
TABLE users ( email TEXT NOT NULL, ... )
{
    INDICES { idx_users_email (email); }
}
```

```sql
-- emits (non-transactional, after COMMIT)
CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_users_email"
    ON "public"."users" ("email");
```

## Unique index

```sql
{ INDICES { idx_unique_slug UNIQUE (slug); } }
```

```sql
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS "idx_unique_slug"
    ON "public"."users" ("slug");
```

## Composite index with sort order

```sql
{ INDICES { idx_tenant_created (tenant_id ASC, created_at DESC); } }
```

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_tenant_created"
    ON "public"."events" ("tenant_id" ASC, "created_at" DESC);
```

## Partial index

```sql
{ INDICES { idx_active_users (email) WHERE (status = 'active'); } }
```

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_active_users"
    ON "public"."users" ("email") WHERE (status = 'active');
```

## Expression index

```sql
{ INDICES { idx_lower_email (lower(email)); } }
```

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_lower_email"
    ON "public"."users" (lower("email"));
```

## Covering index (INCLUDE)

```sql
{ INDICES { idx_covering (user_id) INCLUDE (email, created_at); } }
```

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_covering"
    ON "public"."users" ("user_id") INCLUDE ("email", "created_at");
```

## GIN index

```sql
{ INDICES {
    idx_tags (tags)       USING gin;
    idx_fts  (search_vec) USING gin;
} }
```

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_tags"
    ON "public"."posts" USING gin ("tags");

CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_fts"
    ON "public"."posts" USING gin ("search_vec");
```

## GiST index

```sql
{ INDICES { idx_location (location) USING gist; } }
```

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_location"
    ON "public"."places" USING gist ("location");
```

## BRIN index with storage parameter

```sql
{ INDICES { idx_brin (created_at) USING brin WITH (pages_per_range = 128); } }
```

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_brin"
    ON "public"."events" USING brin ("created_at") WITH (pages_per_range = 128);
```

## Index with tablespace

```sql
{ INDICES { idx_archived (archived_at) TABLESPACE archive_space; } }
```

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_archived"
    ON "public"."records" ("archived_at") TABLESPACE "archive_space";
```

## Disable CONCURRENTLY for a specific index

```sql
{ INDICES { idx_email (email) CONCURRENTLY false; } }
```

```sql
-- emits inside the transaction block (not after COMMIT)
CREATE INDEX IF NOT EXISTS "idx_email" ON "public"."users" ("email");
```

## Index removal

Removing an index from the `INDICES` block emits a `DROP INDEX` — classified as `SAFE` (no data loss; the underlying data remains).

```sql
-- emits
DROP INDEX IF EXISTS "public"."idx_old_index";
```
