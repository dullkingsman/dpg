---
title: "Language Reference"
generated: false
weight: 1
---


## The Two-Part Syntax Model

Every DPG object definition has at most two parts:

**Part 1 — The native PostgreSQL SQL definition.** Written exactly as PostgreSQL SQL dictates: same keywords, same clause ordering, same syntax, same dollar-quoting for function bodies. The leading `CREATE` (or `CREATE OR REPLACE`, `CREATE TABLE`, etc.) verb is omitted. Everything else is identical to what you would write in a raw `CREATE` statement.

**Part 2 — The DPG structural block `{ }`.** An optional trailing block that holds only things PostgreSQL expresses as *separate DDL statements*: indexes (`CREATE INDEX`), policies (`CREATE POLICY`), triggers (`CREATE TRIGGER`), grants (`GRANT`), comments (`COMMENT ON`), per-column storage attributes (`ALTER TABLE ... ALTER COLUMN`), and DPG lifecycle directives (`RENAMED FROM`, `PROTECTED`, `DEPRECATED`, `DROP CASCADE`).

**The rule of thumb:** If PostgreSQL writes it as part of `CREATE OBJECT`, it is Part 1. If PostgreSQL writes it as a separate statement, it is Part 2.

```
TABLE users (
    -- Part 1: native PG CREATE TABLE syntax, CREATE verb omitted
    id    BIGINT GENERATED ALWAYS AS IDENTITY,
    email TEXT   NOT NULL,
    CONSTRAINT pk_users       PRIMARY KEY (id),
    CONSTRAINT uq_users_email UNIQUE (email)
)
{
    -- Part 2: things PG expresses as separate statements
    COMMENT "Primary identity store";
    OWNER   "app_role";
    INDICES { idx_email (email); }
    GRANTS  { SELECT TO app_readonly; }
}
```

## The No-Verb Mandate

`CREATE`, `ALTER`, and `DROP` are **illegal** at the declaration level in DPG source files.

**Exceptions:**
- Inside dollar-quoted function bodies (`AS $$...$$`) — these are opaque text the compiler does not interpret.
- Inside `MIGRATE REMOVE { }` blocks on ENUM types — these contain passthrough DML.

```
-- Illegal:
CREATE TABLE users (...);   -- error: CREATE is forbidden
ALTER TABLE users ...;       -- error: ALTER is forbidden

-- Legal:
TABLE users (...) { ... }   -- correct DPG syntax

-- Legal (inside dollar-quote body):
FUNCTION seed_data() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    CREATE TEMP TABLE staging (...);  -- OK inside $$
END;
$$;
```

## Structural Scoping

The `{ }` block of any container provides scope. Nested object definitions inherit their context automatically — you never repeat the schema name or table name inside a nested block.

```
SCHEMA analytics {
    OWNER "analytics_role";
    COMMENT "Derived tables and event aggregations";

    TABLE events (
        id         BIGINT GENERATED ALWAYS AS IDENTITY,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
        CONSTRAINT pk_events PRIMARY KEY (id)
    )
    {
        INDICES {
            idx_ts (created_at);    -- ON analytics.events: inferred from context
        }
        GRANTS { SELECT TO app_readonly; }
    }
}
```

Schemas have no `( )` list — their `{ }` block directly contains all schema-level attributes and nested objects.

## Semicolons as Statement Terminators

Semicolons terminate statements. The rules differ by object type:

**Objects with no trailing block** — semicolon after the last PG SQL clause:

```
TABLE simple_log (
    id      BIGINT GENERATED ALWAYS AS IDENTITY,
    message TEXT NOT NULL,
    CONSTRAINT pk_simple_log PRIMARY KEY (id)
);

EXTENSION pgcrypto;

ENUM invoice_status ('draft', 'sent', 'paid', 'void');
```

**Objects with a `{ }` block but no `( )` list** — the preceding statement ends with `;`, then `{` follows immediately:

```
VIEW active_users AS
    SELECT id, email FROM users WHERE status = 'active';
{
    GRANTS { SELECT TO app_readonly; }
}

ENUM invoice_status ('draft', 'sent', 'paid', 'void');
{
    COMMENT "Billing lifecycle states";
}
```

**Objects whose Part 1 ends with `)` (tables)** — no semicolon between `)` and `{`:

```
TABLE users (
    id    BIGINT GENERATED ALWAYS AS IDENTITY,
    email TEXT   NOT NULL,
    CONSTRAINT pk_users PRIMARY KEY (id)
) WITH (fillfactor = 90)
{
    INDICES { idx_email (email); }
}
```

**Functions and procedures** — dollar-quote ends with `$$;`, then `{` follows immediately:

```
FUNCTION foo() RETURNS TEXT LANGUAGE sql STABLE
AS $$
    SELECT 'hello';
$$;
{
    COMMENT "Simple example function";
    GRANTS { EXECUTE TO app_service; }
}
```

Without a trailing block, `$$;` alone completes the declaration:

```
FUNCTION foo() RETURNS TEXT LANGUAGE sql STABLE
AS $$
    SELECT 'hello';
$$;
```

## Dollar-Quote Parsing

The parser handles `$$...$$` and `$tag$...$tag$` as opaque string delimiters. When the parser encounters `AS $$`, it scans forward for the matching `$$` token. Everything between the delimiters is treated as opaque text — no brace counting, no keyword interpretation, no SQL parsing. Named dollar-quoting (`$body$`, `$func$`, `$sql$`, any `$identifier$`) is fully supported and the parser matches opening and closing tags exactly.

Function bodies may freely contain any content — SQL DML, PL/pgSQL blocks with `BEGIN`/`END`, nested dollar-quoted strings, even `CREATE`/`ALTER`/`DROP` statements — without any escaping.

```
-- Named dollar-quoting (useful when body contains $$ literals)
FUNCTION format_price(p_amount NUMERIC) RETURNS TEXT
LANGUAGE plpgsql IMMUTABLE STRICT
AS $func$
BEGIN
    RETURN '$' || TO_CHAR(p_amount, 'FM999,999,990.00');
END;
$func$;
```

## Dual Definition Modes

For every object type that admits multiples, DPG supports two equivalent modes that can be freely mixed within the same file or across files:

**Plural block (Mode A):** The object keyword is omitted inside the enclosing block.

```
INDICES {
    idx_email (email);
    idx_name  (last_name, first_name);
}
```

**Singular keyword (Mode B):** The singular object keyword precedes each definition.

```
INDEX idx_email (email);
INDEX idx_name  (last_name, first_name);
```

Both produce identical compiler output and are merged before compilation.

This applies to: `INDICES`/`INDEX`, `POLICIES`/`POLICY`, `TRIGGERS`/`TRIGGER`, `GRANTS`/`GRANT`, `REVOCATIONS`/`REVOCATION`, `COLUMNS`/`COLUMN`, `CONSTRAINTS`/`CONSTRAINT`, `PARTITIONS`/`PARTITION`, `STATISTICS`.

## Block Merging

The same object may be declared across multiple `.dpg` files. DPG merges all declarations before compiling:

**Set-valued properties** (columns, constraints, indexes, policies, triggers, grants, revocations, column blocks) — the union of all declarations. Identical duplicate entries are silently deduplicated. Conflicting entries with the same name but different definitions are a compiler error.

**Scalar properties** (owner, comment, tablespace, RLS flags, protected, deprecated, drop behavior, renamed-from) — last-declaration-wins, ordered alphabetically by full file path. This makes merge resolution deterministic and machine-consistent. The linter warns on scalar conflicts when `warn_on_scalar_merge_conflict = true`.

```
-- file: schemas/public/tables/users.dpg
TABLE users ( id BIGINT GENERATED ALWAYS AS IDENTITY, ... ) {
    OWNER "app_role";
    INDICES { idx_email (email); }
}

-- file: schemas/public/grants.dpg
TABLE users ( id BIGINT GENERATED ALWAYS AS IDENTITY, ... ) {
    GRANTS { SELECT TO app_readonly; }
}
-- compiler merges both declarations into one logical TABLE users object
```

## Dependency Ordering — Topological Sort

The compiler builds a dependency graph of all declared objects before emitting any SQL. Every reference from one object to another (a column typed as a custom ENUM, a foreign key to another table, a view referencing a function) creates a directed edge. The compiler performs a topological sort and emits DDL in dependency order.

You never need to declare objects in order or think about ordering within a file.

**Circular foreign keys:** Two tables with mutual FKs require at least one FK to be `DEFERRABLE`. The compiler emits both tables first, then the circular FK as a subsequent `ALTER TABLE ADD CONSTRAINT ... DEFERRABLE`. If a cycle exists with no `DEFERRABLE` FK, the compiler errors with a message identifying the cycle.

**Cross-file references:** Objects may freely reference objects declared in other files within the same database directory tree. All references are resolved across the full merged object graph before sorting.

## Schema Scoping Within a Database

All `.dpg` files within a database directory are compiled as a single unit. Schema declarations at any depth provide scoping for nested objects. The database itself is never declared in source — it is determined by the directory's position in the project structure.

Within a single file, you may open and close `SCHEMA` blocks freely. All `SCHEMA public { ... }` blocks across all files in the same database are merged.

```
-- file A: declares users in public schema
SCHEMA public {
    TABLE users ( ... ) { ... }
}

-- file B: also declares in public schema (merged)
SCHEMA public {
    TABLE orders ( ... ) { ... }
    VIEW active_orders AS SELECT ...;
}
```

The compiled output treats both files as a single `public` schema declaration.
