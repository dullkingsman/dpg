---
title: "Two-Part Syntax"
description: "Every DPG object has a PostgreSQL SQL definition (Part 1) and an optional structural block (Part 2)."
weight: 1
---

Every DPG object has at most two parts: native PostgreSQL DDL with the `CREATE` verb removed (Part 1), and an optional `{ }` block for things PostgreSQL expresses as separate statements (Part 2).

**Rule of thumb:** if PostgreSQL writes it as part of `CREATE OBJECT`, it is Part 1. If PostgreSQL writes it as a separate statement (`CREATE INDEX`, `GRANT`, `COMMENT ON`), it is Part 2.

## Basic object — Part 1 only

```sql
TABLE simple_log (
    id      BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message TEXT NOT NULL
);
```

```sql
-- emits
CREATE TABLE "public"."simple_log" (
    "id"      bigint GENERATED ALWAYS AS IDENTITY CONSTRAINT "simple_log_pkey" PRIMARY KEY,
    "message" text NOT NULL
);
```

## Object with a `{ }` block — Parts 1 and 2

```sql
TABLE users (
    id    BIGINT GENERATED ALWAYS AS IDENTITY,
    email TEXT NOT NULL,
    CONSTRAINT pk_users       PRIMARY KEY (id),
    CONSTRAINT uq_users_email UNIQUE (email)
)
{
    COMMENT "Primary identity store";
    OWNER   "app_role";
    INDICES { idx_email (email); }
    GRANTS  { SELECT TO app_readonly; }
}
```

```sql
-- emits
CREATE TABLE "public"."users" (
    "id"    bigint GENERATED ALWAYS AS IDENTITY,
    "email" text NOT NULL,
    CONSTRAINT "pk_users"       PRIMARY KEY ("id"),
    CONSTRAINT "uq_users_email" UNIQUE ("email")
);

COMMENT ON TABLE "public"."users" IS 'Primary identity store';
ALTER TABLE "public"."users" OWNER TO "app_role";
GRANT SELECT ON TABLE "public"."users" TO "app_readonly";

-- non-transactional (after COMMIT):
CREATE INDEX CONCURRENTLY "idx_email" ON "public"."users" ("email");
```

## No-verb mandate

`CREATE`, `ALTER`, and `DROP` are illegal at the declaration level.

```sql
-- illegal
CREATE TABLE users (...);
ALTER TABLE users ADD COLUMN ...;

-- legal
TABLE users (...) { ... }
```

Exceptions: inside dollar-quoted function bodies (`AS $$...$$`) and inside `MIGRATE REMOVE { }` blocks on ENUMs — both are opaque text the compiler does not interpret.

## Structural scoping

Nested objects inherit context from their enclosing `{ }` block.

```sql
SCHEMA analytics {
    OWNER "analytics_role";

    TABLE events (
        id         BIGINT GENERATED ALWAYS AS IDENTITY,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        CONSTRAINT pk_events PRIMARY KEY (id)
    )
    {
        INDICES { idx_ts (created_at); }
    }
}
```

```sql
-- emits
CREATE SCHEMA IF NOT EXISTS "analytics";
ALTER SCHEMA "analytics" OWNER TO "analytics_role";

CREATE TABLE "analytics"."events" (
    "id"         bigint GENERATED ALWAYS AS IDENTITY,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT "pk_events" PRIMARY KEY ("id")
);

-- non-transactional:
CREATE INDEX CONCURRENTLY "idx_ts" ON "analytics"."events" ("created_at");
```

Schemas have no `( )` list — their `{ }` block directly contains attributes and nested objects.

## Semicolon rules

| Object kind | Terminator before `{ }` |
|-------------|------------------------|
| Objects with a `( )` list (tables) | None — `)` or `) WITH (...)` ends Part 1 |
| Functions and procedures | `$$;` ends the dollar-quoted body |
| All others (views, enums, extensions) | `;` terminates Part 1 |

```sql
-- table: no semicolon before {
TABLE users ( id BIGINT ) WITH (fillfactor = 90)
{ INDICES { idx_email (email); } }

-- function: $$; before {
FUNCTION foo() RETURNS TEXT LANGUAGE sql AS $$
    SELECT 'hello';
$$;
{ GRANTS { EXECUTE TO app_service; } }

-- view: ; before {
VIEW active_users AS SELECT id FROM users WHERE status = 'active';
{ GRANTS { SELECT TO app_readonly; } }
```

## Dual definition modes

Every plural keyword has a singular equivalent. Both are identical to the compiler.

```sql
-- Mode A: plural block
INDICES {
    idx_email (email);
    idx_name  (last_name, first_name);
}

-- Mode B: singular keyword
INDEX idx_email (email);
INDEX idx_name  (last_name, first_name);
```

Applies to: `INDICES`/`INDEX`, `POLICIES`/`POLICY`, `TRIGGERS`/`TRIGGER`, `GRANTS`/`GRANT`, `REVOCATIONS`/`REVOCATION`, `COLUMNS`/`COLUMN`, `CONSTRAINTS`/`CONSTRAINT`, `PARTITIONS`/`PARTITION`, `STATISTICS`.

## Block merging

The same object may be declared across multiple `.dpg` files. DPG merges all declarations before compiling.

- **Set-valued properties** (columns, indexes, policies, triggers, grants): union of all declarations. Identical duplicates are silently dropped. Conflicting entries with the same name are a compiler error.
- **Scalar properties** (owner, comment, tablespace, protected, deprecated): last-declaration-wins, ordered alphabetically by full file path.

```sql
-- tables/users.dpg
TABLE users ( id BIGINT GENERATED ALWAYS AS IDENTITY, ... )
{ OWNER "app_role"; INDICES { idx_email (email); } }

-- grants.dpg
TABLE users ( id BIGINT GENERATED ALWAYS AS IDENTITY, ... )
{ GRANTS { SELECT TO app_readonly; } }

-- compiler merges both into one TABLE users object
```

## Dependency ordering

The compiler builds a dependency graph before emitting any SQL. Objects are emitted in topological order — you never need to declare objects in order or think about file ordering.

Circular foreign keys require at least one FK to be `DEFERRABLE`. The compiler emits both tables first, then the circular FK as `ALTER TABLE ADD CONSTRAINT ... DEFERRABLE INITIALLY DEFERRED`.
