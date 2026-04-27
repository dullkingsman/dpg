# Object Reference

Complete reference for every object type DPG can declare. All objects follow the [two-part syntax model](language.md): Part 1 is native PostgreSQL SQL with the `CREATE` verb removed; Part 2 is an optional `{ }` block for sub-objects and lifecycle directives.

## Table of Contents

- [SCHEMA](#schema)
- [EXTENSION](#extension)
- [ENUM](#enum)
- [Composite TYPE](#composite-type)
- [Range TYPE](#range-type)
- [Domain TYPE](#domain-type)
- [Base TYPE](#base-type)
- [TABLE](#table)
  - [Column definitions](#column-definitions)
  - [COLUMN block](#column-block)
  - [Column-level grants](#column-level-grants)
  - [Column renaming](#column-renaming)
  - [CONSTRAINT block](#constraint-block)
  - [Indexes](#indexes)
  - [Row Level Security](#row-level-security)
  - [Triggers](#triggers)
  - [Grants and Revocations](#grants-and-revocations)
  - [UNLOGGED TABLE](#unlogged-table)
  - [FOREIGN TABLE](#foreign-table)
  - [Partitioned tables](#partitioned-tables)
- [SEQUENCE](#sequence)
- [VIEW](#view)
- [MATERIALIZED VIEW](#materialized-view)
- [FUNCTION](#function)
- [PROCEDURE](#procedure)
- [AGGREGATE](#aggregate)
- [ROLE](#role)
- [Default Privileges](#default-privileges)
- [TABLESPACE](#tablespace)
- [FOREIGN DATA WRAPPER](#foreign-data-wrapper)
- [SERVER](#server)
- [USER MAPPING](#user-mapping)
- [PUBLICATION](#publication)
- [SUBSCRIPTION](#subscription)
- [EVENT TRIGGER](#event-trigger)
- [COLLATION](#collation)
- [OPERATOR](#operator)
- [OPERATOR CLASS and OPERATOR FAMILY](#operator-class-and-operator-family)
- [CAST](#cast)
- [STATISTICS](#statistics-object)
- [Full-Text Search Objects](#full-text-search-objects)

---

## SCHEMA

Schemas have no `( )` list. Their `{ }` block holds all attributes and nested objects.

**PG equivalent:** `CREATE SCHEMA [IF NOT EXISTS] name [AUTHORIZATION owner]`

```
SCHEMA public {
    -- objects in the public schema go here
}

SCHEMA analytics {
    OWNER "analytics_role";
    COMMENT "Derived tables and event aggregations";

    TABLE events ( ... ) { ... }
    VIEW  daily_revenue AS SELECT ...;
}
```

**Rename a schema:**

```
SCHEMA reporting {
    RENAMED FROM old_reporting;
}
```

Compiler emits: `ALTER SCHEMA old_reporting RENAME TO reporting;`

**Diffing:** Schema rename uses `RENAMED FROM`. Dropping a schema requires removing it from source (classified `DESTRUCTIVE`).

---

## EXTENSION

**PG equivalent:** `CREATE EXTENSION [IF NOT EXISTS] name [SCHEMA schema] [VERSION version] [CASCADE]`

```
EXTENSION pgcrypto;
EXTENSION postgis SCHEMA public VERSION '3.3';
EXTENSION pg_trgm CASCADE;
```

Extensions are declared at the database level (not inside a `SCHEMA` block). The compiler emits `CREATE EXTENSION IF NOT EXISTS`. Removing an extension from source emits `DROP EXTENSION` (classified `DESTRUCTIVE`).

---

## ENUM

ENUM types use PostgreSQL's parenthesised list syntax, terminated with `;`. An optional trailing `{ }` block holds comments and value-removal directives.

**PG equivalent:** `CREATE TYPE name AS ENUM ('val1', 'val2', ...)`

```
ENUM user_status ('active', 'suspended', 'deleted');

ENUM invoice_status ('draft', 'sent', 'paid', 'void', 'overdue');
{
    COMMENT "Billing lifecycle states for customer invoices";
}
```

**Adding values** emits `ALTER TYPE ... ADD VALUE` (safe; PG 9.1+).

**Removing values** requires a `MIGRATE REMOVE` block — see [Lifecycle Directives](lifecycle.md#migrate-remove--enum-value-removal) for the complete seven-step procedure.

---

## Composite TYPE

**PG equivalent:** `CREATE TYPE name AS (col1 type1, col2 type2, ...)`

```
SCHEMA public {
    TYPE address AS (
        street      TEXT,
        city        TEXT,
        state       CHAR(2),
        postal_code TEXT,
        country     CHAR(2)
    );
}
```

**Diffing:** Column additions and type changes require DROP + recreate (`DESTRUCTIVE`).

---

## Range TYPE

**PG equivalent:** `CREATE TYPE name AS RANGE (SUBTYPE = ..., [options])`

```
SCHEMA public {
    TYPE float8range AS RANGE (
        SUBTYPE      = float8,
        SUBTYPE_DIFF = float8mi
    );
}
```

---

## Domain TYPE

**PG equivalent:** `CREATE DOMAIN name AS base_type [DEFAULT ...] [CONSTRAINT ...]`

Constraints and defaults live in the `{ }` block:

```
SCHEMA public {
    DOMAIN positive_integer AS INTEGER {
        DEFAULT 1;
        CONSTRAINT positive_only  CHECK (VALUE > 0);
        CONSTRAINT reasonable_max CHECK (VALUE < 1000000);
    }

    DOMAIN positive_money AS NUMERIC(12, 2) {
        CONSTRAINT must_be_positive CHECK (VALUE >= 0);
    }
}
```

---

## Base TYPE

**PG equivalent:** `CREATE TYPE name (INPUT = ..., OUTPUT = ..., [options])`

```
SCHEMA public {
    TYPE mytype (
        INPUT          = mytype_in,
        OUTPUT         = mytype_out,
        INTERNALLENGTH = 16,
        ALIGNMENT      = double
    );
}
```

Base types are passthrough — the compiler does not perform structural diffing. Any change triggers a DROP + recreate.

---

## TABLE

The most feature-rich object in DPG. Part 1 is a full PostgreSQL `CREATE TABLE` column list; Part 2 is the `{ }` block holding indexes, policies, triggers, grants, comments, and column-level attributes.

**PG equivalent:** `CREATE [UNLOGGED] TABLE name (columns, constraints) [options]`

### Minimal table

```
TABLE simple_log (
    id      BIGINT GENERATED ALWAYS AS IDENTITY,
    message TEXT NOT NULL,
    CONSTRAINT pk_simple_log PRIMARY KEY (id)
);
```

### Full table with all features

```
TABLE users (
    id         BIGINT GENERATED ALWAYS AS IDENTITY,
    email      TEXT          NOT NULL,
    status     user_status   NOT NULL DEFAULT 'active',
    org_id     UUID          NOT NULL,
    created_at TIMESTAMPTZ   NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT pk_users       PRIMARY KEY (id),
    CONSTRAINT uq_users_email UNIQUE (email)
) WITH (fillfactor = 90)
{
    COMMENT "Primary identity store for the application";
    OWNER   "app_role";

    COLUMN email {
        COMMENT    "Verified email address. Unique across all users.";
        STATISTICS 300;
        GRANTS     { SELECT TO reporting_role; }
    }

    COLUMN status     { STATISTICS 500; }
    COLUMN created_at { STATISTICS 200; }

    INDICES {
        idx_email  (email);
        idx_status (status) WHERE (status != 'deleted');
        idx_org    (org_id, created_at DESC);
    }

    ENABLE ROW LEVEL SECURITY;
    FORCE  ROW LEVEL SECURITY;

    POLICIES {
        view_self FOR SELECT USING (id = auth.uid());
        insert_own FOR INSERT WITH CHECK (id = auth.uid());
    }

    TRIGGERS {
        after_email_change AFTER UPDATE OF email
            FOR EACH ROW
            WHEN (OLD.email IS DISTINCT FROM NEW.email)
            EXECUTE FUNCTION notify_email_change();
    }

    GRANTS {
        SELECT, INSERT, UPDATE TO app_service;
        SELECT                 TO app_readonly;
    }

    REVOCATIONS {
        ALL PRIVILEGES FROM PUBLIC;
    }
}
```

### Column definitions

Column definitions live in the `( )` list and use standard PostgreSQL syntax:

```
TABLE products (
    -- integer types
    id          BIGINT          GENERATED ALWAYS AS IDENTITY,
    quantity    INTEGER         NOT NULL DEFAULT 0,
    -- text types
    name        TEXT            NOT NULL,
    slug        VARCHAR(255)    NOT NULL,
    -- numeric
    price       NUMERIC(10, 2)  NOT NULL,
    -- boolean
    active      BOOLEAN         NOT NULL DEFAULT true,
    -- timestamps
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ,
    -- uuid
    external_id UUID            NOT NULL DEFAULT gen_random_uuid(),
    -- json
    metadata    JSONB,
    -- generated column
    name_lower  TEXT            GENERATED ALWAYS AS (lower(name)) STORED,
    -- constraints inline
    CONSTRAINT pk_products      PRIMARY KEY (id),
    CONSTRAINT uq_products_slug UNIQUE (slug),
    CONSTRAINT ck_price_pos     CHECK (price >= 0)
);
```

All PostgreSQL built-in types are valid. Custom types (ENUM, composite, domain) defined in the same database are also valid.

### COLUMN block

The `COLUMN name { }` block inside a table's `{ }` block references an existing column and holds attributes that PostgreSQL expresses as separate `ALTER TABLE ... ALTER COLUMN` statements.

| Attribute | PostgreSQL equivalent |
|---|---|
| `COMMENT "text"` | `COMMENT ON COLUMN t.c IS '...'` |
| `STATISTICS n` | `ALTER TABLE t ALTER COLUMN c SET STATISTICS n` |
| `COMPRESSION method` | `ALTER TABLE t ALTER COLUMN c SET COMPRESSION m` |
| `STORAGE type` | `ALTER TABLE t ALTER COLUMN c SET STORAGE s` |
| `DEPRECATED "msg"` | Linter warning; stores as comment |
| `USING expr` | `ALTER TABLE t ALTER COLUMN c TYPE ... USING expr` |
| `RENAMED FROM old_name` | `ALTER TABLE t RENAME COLUMN old_name TO new_name` |
| `GRANTS { }` | `GRANT privilege (col) ON TABLE t TO role` |
| `REVOCATIONS { }` | `REVOKE privilege (col) ON TABLE t FROM role` |

`COLUMN` blocks do not declare new columns — they annotate existing columns declared in the `( )` list.

**Storage types:** `plain`, `external`, `extended` (default), `main`.

**Compression methods:** `pglz`, `lz4` (PG 14+).

**Statistics targets:** `-1` (reset to default), `0` (disable), `1–10000` (explicit target). Values above 500 rarely improve query planning.

```
TABLE orders ( amount NUMERIC(10,2), status order_status, created_at TIMESTAMPTZ, ... )
{
    COLUMN status     { STATISTICS 500; COMPRESSION lz4; }
    COLUMN created_at { STATISTICS 200; }
    COLUMN metadata   { STORAGE external; }
}
```

Plural form `COLUMNS { name1 { ... } name2 { ... } }` is also valid.

### Column-level grants

Column grants use the same `GRANTS { }` syntax inside `COLUMN name { }`. The column scope is inferred — the compiler emits `GRANT privilege (col) ON TABLE t TO role`.

```
TABLE users ( id BIGINT ..., email TEXT, ssn TEXT, ... )
{
    COLUMN email {
        GRANTS {
            SELECT TO reporting_role;
            SELECT TO analytics_role;
        }
    }

    COLUMN ssn {
        GRANTS     { SELECT TO compliance_role; }
        REVOCATIONS { ALL PRIVILEGES FROM PUBLIC; }
    }

    GRANTS { SELECT, INSERT, UPDATE TO app_service; }  -- table-level grant
}
```

### Column renaming

```
TABLE users (
    email_address TEXT NOT NULL,    -- new name in ( ) list
    CONSTRAINT uq_email UNIQUE (email_address)   -- must use new name
)
{
    COLUMN email_address {
        RENAMED FROM email;   -- old name
        COMMENT "Verified email address";
    }
}
```

See [Lifecycle Directives — RENAMED FROM](lifecycle.md#renamed-from) for the full resolution algorithm.

### CONSTRAINT block

Constraints in the `{ }` block handle cases that cannot appear in `CREATE TABLE` (specifically `NOT VALID`) and provide alternative placement for any constraint.

Constraints are identified by name across both `( )` and `{ }` — merged into a single logical set. Same name + same definition = deduplicated. Same name + conflicting definition = compiler error.

```
TABLE orders (
    id     BIGINT GENERATED ALWAYS AS IDENTITY,
    amount NUMERIC(10,2) NOT NULL,
    CONSTRAINT pk_orders PRIMARY KEY (id)
)
{
    CONSTRAINT ck_amount_positive CHECK (amount > 0) NOT VALID;

    CONSTRAINT fk_account FOREIGN KEY (account_id)
        REFERENCES accounts (id)
        ON DELETE CASCADE;
}
```

**`NOT VALID` lifecycle:**

1. Add constraint with `NOT VALID` → compiler emits `ALTER TABLE ADD CONSTRAINT ... NOT VALID` (non-blocking).
2. Remove `NOT VALID` in a subsequent migration → compiler emits `ALTER TABLE VALIDATE CONSTRAINT name` (validates existing rows).
3. Optionally move the validated constraint from `{ }` to the `( )` list — compiler identifies by name and treats as already existing.

### Indexes

Declared in an `INDICES { }` block or as individual `INDEX` directives:

```
TABLE users ( ... )
{
    INDICES {
        idx_email    (email);
        idx_status   (status) WHERE (status != 'deleted');
        idx_org      (org_id ASC, created_at DESC);
        idx_lower_email (lower(email));
        idx_covering (user_id) INCLUDE (email, created_at);
    }
}
```

**Index options:**

| Syntax | Description |
|---|---|
| `idx_name (col)` | Basic btree index |
| `idx_name UNIQUE (col)` | Unique index |
| `idx_name (col) WHERE (predicate)` | Partial index |
| `idx_name (expr)` | Expression index |
| `idx_name USING gist (col)` | GiST index |
| `idx_name USING gin (col)` | GIN index (arrays, JSONB) |
| `idx_name USING brin (col)` | BRIN index (large time-ordered tables) |
| `idx_name USING hash (col)` | Hash index |
| `idx_name (col) INCLUDE (col2)` | Covering index |
| `idx_name USING brin (col) WITH (pages_per_range = 128)` | Index with storage options |
| `idx_name (col) TABLESPACE fast_ssd` | Index in specific tablespace |
| `idx_name (col) CONCURRENTLY false` | Disable concurrent creation for this index |

**Concurrent creation:** By default, DPG emits `CREATE INDEX CONCURRENTLY` for indexes added to existing tables. Concurrent index creation runs outside a transaction and is emitted as a `MANUAL` step after `COMMIT`. New tables (first migration) use `CREATE INDEX` (non-concurrent, inside the transaction). Override with `CONCURRENTLY false` per index.

### Row Level Security

```
TABLE orders ( ... )
{
    ENABLE ROW LEVEL SECURITY;   -- enable RLS (superusers bypass by default)
    FORCE  ROW LEVEL SECURITY;   -- superusers also subject to policies

    POLICIES {
        view_own FOR SELECT
            USING (user_id = auth.uid());

        insert_own FOR INSERT
            WITH CHECK (user_id = auth.uid());

        update_own FOR UPDATE
            USING     (user_id = auth.uid())
            WITH CHECK (user_id = auth.uid() AND status != 'locked');

        restrict_deleted AS RESTRICTIVE FOR ALL
            USING (deleted_at IS NULL);

        admin_all FOR ALL
            TO admin_role
            USING (true);

        service_read FOR SELECT
            TO service_role, readonly_role
            USING (true);
    }
}
```

**Policy options:**
- `FOR SELECT | INSERT | UPDATE | DELETE | ALL`
- `AS PERMISSIVE` (default) or `AS RESTRICTIVE`
- `TO role [, role ...]` — restrict policy to specific roles (default: all roles)
- `USING (expr)` — visibility condition (SELECT, UPDATE, DELETE)
- `WITH CHECK (expr)` — write condition (INSERT, UPDATE)

### Triggers

```
TABLE users ( ... )
{
    TRIGGERS {
        before_insert BEFORE INSERT
            FOR EACH ROW
            EXECUTE FUNCTION set_defaults();

        after_email_change AFTER UPDATE OF email
            FOR EACH ROW
            WHEN (OLD.email IS DISTINCT FROM NEW.email)
            EXECUTE FUNCTION notify_email_change();

        audit_changes AFTER INSERT OR UPDATE OR DELETE
            REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
            FOR EACH STATEMENT
            EXECUTE FUNCTION audit_table_changes();

        check_ref CONSTRAINT AFTER INSERT OR UPDATE
            FROM orders
            DEFERRABLE INITIALLY DEFERRED
            FOR EACH ROW
            EXECUTE FUNCTION check_ref_integrity();
    }
}
```

Trigger functions are declared separately as `FUNCTION name() RETURNS TRIGGER`.

### Grants and Revocations

```
TABLE orders ( ... )
{
    GRANTS {
        SELECT                 TO app_readonly;
        SELECT, INSERT, UPDATE TO app_service;
        ALL PRIVILEGES         TO app_admin;
    }

    REVOCATIONS {
        ALL PRIVILEGES FROM PUBLIC;
    }
}
```

**Additive model:** DPG only emits `GRANT`. Removing a `GRANTS` entry emits nothing — add an explicit `REVOCATIONS` entry to revoke. See [CLI Reference — Additive grants](commands.md) for implications on `dpg verify`.

**Valid privileges:** `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `TRUNCATE`, `REFERENCES`, `TRIGGER`, `ALL PRIVILEGES`

### UNLOGGED TABLE

```
UNLOGGED TABLE session_cache (
    key   TEXT,
    value JSONB,
    CONSTRAINT pk_session_cache PRIMARY KEY (key)
);
```

UNLOGGED tables do not write to the WAL. Writes are faster but data is lost on crash or unclean shutdown. Not replicated to replicas. PG-specific — flagged by `dpg portability`.

### FOREIGN TABLE

`SERVER` and `OPTIONS` are part of PostgreSQL's `CREATE FOREIGN TABLE` syntax and appear after `)` in Part 1:

```
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

### Partitioned tables

`PARTITION BY` is part of the `CREATE TABLE` signature and appears after `)`:

```
SCHEMA public {
    TABLE events (
        id         BIGINT GENERATED ALWAYS AS IDENTITY,
        tenant_id  UUID NOT NULL,
        event_type TEXT NOT NULL,
        payload    JSONB,
        created_at TIMESTAMPTZ NOT NULL DEFAULT now()
    ) PARTITION BY RANGE (created_at)
    {
        INDICES {
            idx_events_tenant (tenant_id);
            idx_events_type   (event_type);
        }

        PARTITIONS {
            events_2024_q1 FOR VALUES FROM ('2024-01-01') TO ('2024-04-01');
            events_2024_q2 FOR VALUES FROM ('2024-04-01') TO ('2024-07-01');
            events_2024_q3 FOR VALUES FROM ('2024-07-01') TO ('2024-10-01');
            events_2024_q4 FOR VALUES FROM ('2024-10-01') TO ('2025-01-01');
            events_default DEFAULT;
        }
    }

    -- LIST partitioning
    TABLE orders_by_region (
        id     BIGINT GENERATED ALWAYS AS IDENTITY,
        region TEXT NOT NULL
    ) PARTITION BY LIST (region)
    {
        PARTITIONS {
            orders_north FOR VALUES IN ('NYC', 'BOS', 'PHI');
            orders_south FOR VALUES IN ('MIA', 'ATL', 'DAL');
        }
    }

    -- HASH partitioning
    TABLE logs (
        id      BIGINT GENERATED ALWAYS AS IDENTITY,
        message TEXT
    ) PARTITION BY HASH (id)
    {
        PARTITIONS {
            logs_0 FOR VALUES WITH (MODULUS 4, REMAINDER 0);
            logs_1 FOR VALUES WITH (MODULUS 4, REMAINDER 1);
            logs_2 FOR VALUES WITH (MODULUS 4, REMAINDER 2);
            logs_3 FOR VALUES WITH (MODULUS 4, REMAINDER 3);
        }
    }
}
```

**Sub-partitioning:**

```
TABLE events ( ... ) PARTITION BY RANGE (created_at)
{
    PARTITIONS {
        events_2024 FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')
            PARTITION BY LIST (region) {
                PARTITIONS {
                    events_2024_us FOR VALUES IN ('us-east', 'us-west');
                    events_2024_eu FOR VALUES IN ('eu-west', 'eu-central');
                }
            };
    }
}
```

**Safety:** Adding partitions is `SAFE`. Removing partitions is `DESTRUCTIVE`. Changing the partition strategy requires `dpg apply --approve-partition-rebuild`.

**Temporary tables** are session-scoped and not managed by DPG. A `TEMPORARY TABLE` declaration is a compiler error.

---

## SEQUENCE

**PG equivalent:** `CREATE SEQUENCE name [AS type] [options]`

```
SCHEMA public {
    SEQUENCE order_number_seq
        AS BIGINT
        START WITH  10000
        INCREMENT BY 1
        MINVALUE     10000
        MAXVALUE     99999999
        CACHE        50
        NO CYCLE
        OWNED BY orders.order_number;
}
```

Sequences backing `GENERATED AS IDENTITY` or `SERIAL` columns are managed by PostgreSQL automatically — do not declare them in DPG.

**Diffing:** `START WITH` and `OWNED BY` changes are `CAUTION` (may require `ALTER SEQUENCE RESTART`). Most options can be altered in place.

---

## VIEW

The `AS query` clause is Part 1, terminated with `;`. PG SQL options appear on the signature line. An optional trailing `{ }` block holds grants, comments, and lifecycle directives.

**PG equivalent:** `CREATE [OR REPLACE] VIEW name [(columns)] [WITH (options)] AS query [WITH CHECK OPTION]`

```
SCHEMA public {
    -- Simple view
    VIEW active_users AS
        SELECT id, email, created_at
        FROM users
        WHERE status = 'active' AND deleted_at IS NULL;

    -- Column alias list
    VIEW user_summary (user_id, email, order_count) AS
        SELECT u.id, u.email, COUNT(o.id)
        FROM users u
        LEFT JOIN orders o ON o.user_id = u.id
        GROUP BY u.id, u.email;

    -- Security barrier
    VIEW secure_user_view WITH (security_barrier = true) AS
        SELECT id, email FROM users WHERE tenant_id = current_tenant();

    -- With check option
    VIEW active_orders AS
        SELECT * FROM orders WHERE status != 'cancelled'
        WITH LOCAL CHECK OPTION;

    -- Recursive view
    RECURSIVE VIEW org_tree (id, parent_id, depth, path) AS
        SELECT id, parent_id, 0, ARRAY[id]
        FROM departments WHERE parent_id IS NULL
        UNION ALL
        SELECT d.id, d.parent_id, t.depth + 1, t.path || d.id
        FROM departments d JOIN org_tree t ON d.parent_id = t.id;

    -- View with grants and comment
    VIEW admin_summary AS
        SELECT id, email, created_at FROM users WHERE role = 'admin';
    {
        COMMENT "Admin user summary for operations dashboard";
        GRANTS { SELECT TO app_readonly; }
    }
}
```

**Diffing:**
- Query changes with **identical output column list** → `CREATE OR REPLACE VIEW` (safe in-place replacement).
- Output column list changes in any way (added, removed, reordered, renamed) → `DROP VIEW CASCADE` then `CREATE VIEW` (classified `DESTRUCTIVE`).

---

## MATERIALIZED VIEW

**PG equivalent:** `CREATE MATERIALIZED VIEW name [WITH (options)] [TABLESPACE name] AS query [WITH [NO] DATA]`

```
SCHEMA analytics {
    MATERIALIZED VIEW daily_revenue AS
        SELECT
            date_trunc('day', created_at) AS day,
            SUM(total_amount)             AS revenue,
            COUNT(*)                      AS order_count
        FROM orders
        WHERE status = 'completed'
        GROUP BY 1;

    MATERIALIZED VIEW product_stats
    WITH (fillfactor = 90)
    TABLESPACE analytics_space AS
        SELECT product_id, COUNT(*) AS purchases, AVG(price) AS avg_price
        FROM order_items
        GROUP BY product_id
    WITH NO DATA;
    {
        INDICES { idx_product_stats_id (product_id); }
        GRANTS  { SELECT TO app_readonly; }
    }
}
```

**Diffing:** Any query change = DROP + recreate (always `DESTRUCTIVE`). `REFRESH MATERIALIZED VIEW` is out of scope — it is runtime DML, not schema management.

---

## FUNCTION

Functions are written in complete, unmodified PostgreSQL SQL syntax — the same syntax as `CREATE FUNCTION` with only the `CREATE OR REPLACE` verb removed. The dollar-quoted body is Part 1; the `{ }` block is Part 2.

**PG equivalent:** `CREATE [OR REPLACE] FUNCTION name(args) RETURNS type [attributes] AS $$...$$`

```
SCHEMA public {

    -- SQL function, no sub-objects
    FUNCTION active_user_count() RETURNS BIGINT
    LANGUAGE sql STABLE PARALLEL SAFE
    AS $$
        SELECT COUNT(*) FROM users WHERE status = 'active';
    $$;

    -- PL/pgSQL function with grants and comment
    FUNCTION get_user(p_email TEXT) RETURNS users
    LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public
    AS $$
    DECLARE
        v_user users;
    BEGIN
        SELECT * INTO STRICT v_user FROM users WHERE email = p_email;
        RETURN v_user;
    EXCEPTION
        WHEN NO_DATA_FOUND THEN
            RAISE EXCEPTION 'User not found: %', p_email;
    END;
    $$;
    {
        COMMENT "Fetch a user record by verified email address";
        GRANTS { EXECUTE TO app_service; }
    }

    -- Set-returning function
    FUNCTION users_in_org(p_org_id UUID) RETURNS SETOF users
    LANGUAGE sql STABLE
    AS $$
        SELECT * FROM users WHERE org_id = p_org_id ORDER BY email;
    $$;

    -- Returns TABLE inline
    FUNCTION monthly_summary(p_year INT)
    RETURNS TABLE (month INT, revenue NUMERIC, count BIGINT)
    LANGUAGE sql STABLE
    AS $$
        SELECT
            EXTRACT(MONTH FROM created_at)::INT,
            SUM(total),
            COUNT(*)
        FROM orders
        WHERE EXTRACT(YEAR FROM created_at) = p_year
        GROUP BY 1 ORDER BY 1;
    $$;

    -- Variadic arguments
    FUNCTION log_event(p_type TEXT, VARIADIC p_tags TEXT[]) RETURNS VOID
    LANGUAGE plpgsql
    AS $$
    BEGIN
        INSERT INTO event_log (event_type, tags) VALUES (p_type, p_tags);
    END;
    $$;

    -- Default parameters
    FUNCTION paginate_users(p_limit INT DEFAULT 20, p_offset INT DEFAULT 0)
    RETURNS SETOF users
    LANGUAGE sql STABLE
    AS $$
        SELECT * FROM users LIMIT p_limit OFFSET p_offset;
    $$;

    -- Trigger function
    FUNCTION notify_email_change() RETURNS TRIGGER
    LANGUAGE plpgsql SECURITY DEFINER SET search_path = public
    AS $$
    BEGIN
        PERFORM pg_notify('email_changed', NEW.id::TEXT);
        RETURN NEW;
    END;
    $$;

    -- Named dollar-quoting (useful when body contains $$ literals)
    FUNCTION format_price(p_amount NUMERIC) RETURNS TEXT
    LANGUAGE plpgsql IMMUTABLE STRICT
    AS $func$
    BEGIN
        RETURN '$' || TO_CHAR(p_amount, 'FM999,999,990.00');
    END;
    $func$;
    {
        COMMENT "Format a numeric amount as a dollar price string";
        GRANTS { EXECUTE TO app_readonly, app_service; }
    }

}
```

### Function attributes

All attributes appear on the signature line in PostgreSQL's own order:

| Attribute | Meaning |
|---|---|
| `VOLATILE` | Default. May modify DB; result may differ per call |
| `STABLE` | Constant within a transaction for given inputs |
| `IMMUTABLE` | Constant forever for given inputs; index-eligible |
| `STRICT` | Returns NULL if any argument is NULL |
| `SECURITY DEFINER` | Executes with owner's privileges |
| `SECURITY INVOKER` | Default. Executes with caller's privileges |
| `PARALLEL SAFE` | Safe for parallel workers |
| `PARALLEL RESTRICTED` | Parallel, leader process only |
| `PARALLEL UNSAFE` | Default. Cannot run in parallel |
| `COST n` | Estimated execution cost in cpu_operator_cost units |
| `ROWS n` | Estimated rows returned (set-returning functions) |
| `SUPPORT func` | Planner support function (PG 12+) |
| `SET param = value` | Set a GUC for the duration of the call |
| `WINDOW` | Marks as a window function |

**Function body diffing:** Any change to the body text — even whitespace — changes the stored hash and causes the compiler to emit `CREATE OR REPLACE FUNCTION`. No semantic diff of procedural code is performed.

**Function identity:** `(schema, name, argument_types)`. Overloaded functions (same name, different argument types) are separate objects.

---

## PROCEDURE

Procedures follow the same model as functions. Procedures may `COMMIT` mid-execution.

**PG equivalent:** `CREATE [OR REPLACE] PROCEDURE name(args) [attributes] AS $$...$$`

```
SCHEMA public {
    PROCEDURE process_settlements()
    LANGUAGE plpgsql SECURITY DEFINER
    AS $$
    DECLARE
        v_id settlements.id%TYPE;
    BEGIN
        FOR v_id IN SELECT id FROM settlements WHERE processed = false LOOP
            PERFORM settle_order(v_id);
            COMMIT;
        END LOOP;
    END;
    $$;
    {
        GRANTS { EXECUTE TO scheduler_role; }
    }
}
```

---

## AGGREGATE

Aggregates use two `( )` groups per PostgreSQL's `CREATE AGGREGATE` syntax — both are Part 1. The trailing `{ }` block holds grants, comments.

**PG equivalent:** `CREATE AGGREGATE name(args) (SFUNC = ..., STYPE = ..., [options])`

```
SCHEMA public {
    AGGREGATE product (DOUBLE PRECISION) (
        SFUNC    = float8mul,
        STYPE    = DOUBLE PRECISION,
        INITCOND = '1'
    )
    {
        COMMENT "Multiplicative aggregate over DOUBLE PRECISION values";
        GRANTS { EXECUTE TO app_service; }
    }

    AGGREGATE percentile_disc (DOUBLE PRECISION ORDER BY anyelement) (
        SFUNC     = ordered_set_transition,
        STYPE     = internal,
        FINALFUNC = percentile_disc_final,
        FINALFUNC_EXTRA
    );
}
```

**Diffing:** Identity is `(schema, name, input_types)`. Changes to `SFUNC`, `STYPE`, `INITCOND`, `FINALFUNC`, `COMBINEFUNC`, or `SERIALFUNC` require DROP + recreate (`DESTRUCTIVE`).

---

## ROLE

Roles are cluster-level objects declared in the cluster objects directory (`cluster/` by default).

**PG equivalent:** `CREATE ROLE name [options]`

```
-- production/cluster/roles.dpg

ROLE app_readonly {
    NOLOGIN;
    COMMENT "Read-only access for reporting tools";
}

ROLE app_service {
    LOGIN;
    PASSWORD 'env:APP_SERVICE_PW';
    CONNECTION LIMIT 20;
    VALID UNTIL '2030-01-01';
}

ROLE app_admin {
    LOGIN;
    SUPERUSER  false;
    CREATEDB   false;
    CREATEROLE false;
    INHERIT;
    IN ROLE pg_read_all_stats;
}
```

**Role options (inside `{ }`):**

| Option | Description |
|---|---|
| `LOGIN` / `NOLOGIN` | Whether the role can authenticate |
| `SUPERUSER` / `NOSUPERUSER` | Superuser privileges |
| `CREATEDB` / `NOCREATEDB` | Can create databases |
| `CREATEROLE` / `NOCREATEROLE` | Can create roles |
| `INHERIT` / `NOINHERIT` | Inherits privileges from member roles |
| `REPLICATION` / `NOREPLICATION` | Replication role |
| `BYPASSRLS` / `NOBYPASSRLS` | Bypass row-level security |
| `CONNECTION LIMIT n` | Max concurrent connections (-1 = no limit) |
| `PASSWORD 'env:VAR'` | Role password (must use `env:` — see [Secrets](secrets.md)) |
| `VALID UNTIL 'timestamp'` | Password expiry |
| `IN ROLE role [, ...]` | Member of specified roles |

**Hardcoded passwords are rejected** by the `forbid_hardcoded_passwords` linter rule. Always use `env:VAR_NAME` for passwords.

---

## Default Privileges

Default privileges apply to objects created by a specific role in the future.

```
SCHEMA public {
    DEFAULT PRIVILEGES FOR ROLE app_admin {
        GRANTS {
            SELECT   ON TABLES    TO app_readonly;
            EXECUTE  ON FUNCTIONS TO app_service;
            USAGE    ON SEQUENCES TO app_service;
        }
    }
}
```

**PG equivalent:** `ALTER DEFAULT PRIVILEGES FOR ROLE ... GRANT ...`

---

## TABLESPACE

Tablespaces are cluster-level objects declared in the cluster objects directory.

**PG equivalent:** `CREATE TABLESPACE name LOCATION 'path'`

```
-- production/cluster/tablespaces.dpg

TABLESPACE fast_ssd LOCATION '/mnt/nvme/pg_data';
TABLESPACE archive  LOCATION '/mnt/hdd/pg_archive';
```

---

## FOREIGN DATA WRAPPER

In the common case, FDWs are installed via `EXTENSION` (e.g., `EXTENSION postgres_fdw;`). The explicit `FOREIGN DATA WRAPPER` declaration is reserved for custom C-implemented FDWs and is a cluster-level object.

```
-- production/cluster/fdw.dpg

FOREIGN DATA WRAPPER myfdw
    HANDLER   myfdw_handler
    VALIDATOR myfdw_validator;
```

---

## SERVER

Foreign servers are database-level objects.

**PG equivalent:** `CREATE SERVER name FOREIGN DATA WRAPPER fdw [OPTIONS (...)]`

```
SCHEMA public {
    SERVER analytics_warehouse
        FOREIGN DATA WRAPPER postgres_fdw
        OPTIONS (host 'warehouse.internal', dbname 'analytics', port '5432');
}
```

**Diffing:** `OPTIONS` changes are applied with `ALTER SERVER OPTIONS (SET/ADD/DROP key ...)`. `FOREIGN DATA WRAPPER` changes are `DESTRUCTIVE`.

---

## USER MAPPING

**PG equivalent:** `CREATE USER MAPPING FOR user SERVER server [OPTIONS (...)]`

```
USER MAPPING FOR app_service
    SERVER analytics_warehouse
    OPTIONS (user 'fdw_user', password 'env:FDW_PASSWORD');
```

User mappings are scoped to a database. The `password` option should use `env:VAR_NAME` to avoid hardcoded credentials.

---

## PUBLICATION

**PG equivalent:** `CREATE PUBLICATION name [FOR TABLE ... | FOR ALL TABLES] [WITH (options)]`

```
PUBLICATION user_data
    FOR TABLE users, profiles
    WITH (publish = 'insert, update, delete');
{
    COMMENT "Primary replication stream for user data";
}

PUBLICATION all_tables FOR ALL TABLES;

-- Column-list and row filter (PG 15+)
PUBLICATION filtered_orders
    FOR TABLE orders (id, customer_id, status, total)
    WHERE (status != 'draft');
```

---

## SUBSCRIPTION

**PG equivalent:** `CREATE SUBSCRIPTION name CONNECTION '...' PUBLICATION name [WITH (options)]`

```
SUBSCRIPTION replica_users
    CONNECTION 'host=primary.db.internal dbname=myapp user=replicator'
    PUBLICATION user_data
    WITH (enabled = true, copy_data = true);
```

**Diffing:** `CONNECTION` string changes are `DESTRUCTIVE` (DROP + recreate).

---

## EVENT TRIGGER

**PG equivalent:** `CREATE EVENT TRIGGER name ON event [WHEN TAG IN (...)] EXECUTE FUNCTION func()`

```
EVENT TRIGGER prevent_drop_table
    ON sql_drop
    WHEN TAG IN ('DROP TABLE', 'DROP SCHEMA')
    EXECUTE FUNCTION abort_drop();
```

Event triggers are database-level objects, not schema-scoped.

---

## COLLATION

**PG equivalent:** `CREATE COLLATION name (PROVIDER = ..., LOCALE = ..., [options])`

```
SCHEMA public {
    COLLATION case_insensitive (
        PROVIDER      = icu,
        LOCALE        = 'und-u-ks-level2',
        DETERMINISTIC = false
    );
}
```

**Diffing:** Any property change is `DESTRUCTIVE` (DROP + recreate; dependents checked).

---

## OPERATOR

**PG equivalent:** `CREATE OPERATOR symbol (LEFTARG = ..., RIGHTARG = ..., PROCEDURE = ..., [options])`

```
SCHEMA public {
    OPERATOR === (
        LEFTARG    = complex,
        RIGHTARG   = complex,
        PROCEDURE  = complex_eq,
        COMMUTATOR = ===,
        NEGATOR    = !==,
        RESTRICT   = eqsel,
        JOIN       = eqjoinsel,
        HASHES,
        MERGES
    );
}
```

**Identity:** `(schema, symbol, leftarg_type, rightarg_type)`. `PROCEDURE` changes are `DESTRUCTIVE`. Optimizer hint changes (`COMMUTATOR`, `NEGATOR`, `RESTRICT`, `JOIN`) emit `ALTER OPERATOR`.

---

## OPERATOR CLASS and OPERATOR FAMILY

**PG equivalent:** `CREATE OPERATOR CLASS name USING method FOR TYPE type (operators, functions)`

```
SCHEMA public {
    OPERATOR FAMILY my_family USING btree;

    OPERATOR CLASS my_ops USING btree FOR TYPE mytype (
        OPERATOR 1 <  ,
        OPERATOR 2 <= ,
        OPERATOR 3 =  ,
        OPERATOR 4 >= ,
        OPERATOR 5 >  ,
        FUNCTION 1 mytype_cmp(mytype, mytype)
    );
}
```

Operator classes and families are passthrough — diffed by text equality only.

---

## CAST

**PG equivalent:** `CREATE CAST (source AS target) WITH FUNCTION func(args) [AS IMPLICIT | AS ASSIGNMENT]`

```
SCHEMA public {
    CAST (mytype AS TEXT)
        WITH FUNCTION mytype_to_text(mytype)
        AS IMPLICIT;

    CAST (TEXT AS mytype)
        WITH FUNCTION text_to_mytype(TEXT);
}
```

**Identity:** `(source_type, target_type)`. No `ALTER CAST` exists — any change is `DESTRUCTIVE`. Dependents are checked before DROP.

---

## STATISTICS Object

Extended statistics on multiple columns for better query planning.

**PG equivalent:** `CREATE STATISTICS name [(kinds)] ON col1, col2 FROM table`

```
SCHEMA public {
    STATISTICS orders_stats (dependencies, ndistinct, mcv)
        ON customer_id, created_at
        FROM orders;
}
```

**Kinds:** `dependencies` (functional dependencies), `ndistinct` (distinct value counts), `mcv` (most common values).

**Diffing:** Column list or kinds changes are `DESTRUCTIVE`. Only `statistics_target` can be altered in place.

---

## Full-Text Search Objects

### TEXT SEARCH CONFIGURATION

```
SCHEMA public {
    TEXT SEARCH CONFIGURATION english_unaccented (COPY = pg_catalog.english) {
        MAPPING FOR hword, hword_part, word
            WITH unaccent, english_stem;
    }
}
```

The `MAPPING FOR` directive inside `{ }` compiles to `ALTER TEXT SEARCH CONFIGURATION ... ALTER MAPPING`.

### TEXT SEARCH DICTIONARY

```
SCHEMA public {
    TEXT SEARCH DICTIONARY english_ispell (
        TEMPLATE  = ispell,
        DictFile  = english,
        AffFile   = english,
        StopWords = english
    );
}
```

### TEXT SEARCH PARSER

```
SCHEMA public {
    TEXT SEARCH PARSER my_parser (
        START    = prsd_start,
        GETTOKEN = prsd_nexttoken,
        END      = prsd_end,
        LEXTYPES = prsd_lextype,
        HEADLINE = prsd_headline
    );
}
```

### TEXT SEARCH TEMPLATE

```
SCHEMA public {
    TEXT SEARCH TEMPLATE ispell_template (
        LEXIZE = dispell_lexize,
        INIT   = dispell_init
    );
}
```

---

## Feature Coverage Summary

For the complete coverage matrix with diff strategy details, see Appendix A of the [RFC](../../rfc/v0.8.0.md).

| Feature | Diff strategy |
|---|---|
| Regular tables | Structured (column-level diff) |
| Unlogged tables | Structured |
| Foreign tables | Structured |
| Temporary tables | Out of scope (session-scoped) |
| Views | `CREATE OR REPLACE` if columns unchanged; DROP+CREATE if changed |
| Materialized views | DROP + CREATE on any query change |
| Recursive views | Structured |
| Functions | Body hash diff → `CREATE OR REPLACE`; signature change → DROP+CREATE |
| Procedures | Same as functions |
| Aggregates | Structured; component change → DROP+CREATE |
| ENUM types | `ADD VALUE` for additions; `MIGRATE REMOVE` for removals |
| Composite types | DROP+CREATE on any change |
| Range types | DROP+CREATE on any change |
| Domain types | Structured |
| Base types | Passthrough (text equality) |
| Schemas | `ALTER SCHEMA RENAME` via `RENAMED FROM` |
| Extensions | `CREATE EXTENSION IF NOT EXISTS` |
| Sequences | Most options in-place; ownership change is CAUTION |
| Indexes | `CREATE INDEX [CONCURRENTLY]`; DROP+CREATE on definition change |
| Roles | Cluster-level; structured |
| Tablespaces | Cluster-level |
| Foreign data wrappers | Cluster-level; custom C FDWs only |
| Foreign servers | Structured; FDW change is DESTRUCTIVE |
| User mappings | Structured |
| Publications | Structured |
| Subscriptions | CONNECTION change is DESTRUCTIVE |
| Event triggers | Structured |
| Collations | Any change is DESTRUCTIVE |
| Operators | PROCEDURE change is DESTRUCTIVE; hint changes in-place |
| Operator classes/families | Passthrough |
| Casts | Any change is DESTRUCTIVE |
| Statistics objects | Column/kinds change is DESTRUCTIVE |
| Text search config | MAPPING FOR compiles to ALTER MAPPING |
| Text search dict/parser/template | Structured |
