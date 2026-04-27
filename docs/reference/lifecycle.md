# Lifecycle Directives

DPG lifecycle directives live inside `{ }` blocks and communicate intent to the compiler. They do not exist in PostgreSQL DDL — they are DPG-specific annotations that drive safe, multi-step migrations for operations that cannot be expressed as simple ALTER statements.

## `RENAMED FROM`

Declares that an object was renamed from an old name. The compiler emits the appropriate `RENAME` DDL instead of treating the old name as deleted and the new name as new.

**Applies to:** schemas, tables, columns.

### Schema rename

```
SCHEMA reporting {
    RENAMED FROM old_reporting;
}
```

Compiler emits: `ALTER SCHEMA old_reporting RENAME TO reporting;`

### Table rename

```
TABLE user_accounts ( ... )
{
    RENAMED FROM users;
    COMMENT "Renamed from users in migration 042";
}
```

Compiler emits: `ALTER TABLE users RENAME TO user_accounts;`

### Column rename

The new column name appears in both the `( )` list and the `COLUMN` block. The old name appears only inside `RENAMED FROM`:

```
TABLE users (
    email_address TEXT NOT NULL,    -- new name in ( ) list
    ...
    CONSTRAINT uq_users_email UNIQUE (email_address)   -- must use new name
)
{
    COLUMN email_address {
        RENAMED FROM email;
        COMMENT "Verified email address";
    }
}
```

Compiler emits: `ALTER TABLE users RENAME COLUMN email TO email_address;`

**Compiler resolution algorithm:**

1. Compiler sees `email_address` in the `( )` list and `COLUMN email_address { RENAMED FROM email; }` in the `{ }` block.
2. Looks up `email` in the snapshot. If `email` exists and `email_address` does not → rename.
3. Emits `ALTER TABLE users RENAME COLUMN email TO email_address`.
4. Validates that all index and constraint declarations use `email_address`, not `email`. Errors on mismatch.
5. If `email` does not exist in the snapshot → compiler error. Remove `RENAMED FROM` for genuinely new columns.

**After a rename:** Remove the `RENAMED FROM` directive in the next migration cycle. It serves only for the single migration where the rename occurs.

---

## `DEPRECATED`

Marks an object as deprecated. The linter warns when `warn_on_deprecated = true` (default). The compiler stores the deprecation message as a special `COMMENT ON` on the object.

**Applies to:** tables, columns, views, functions.

```
TABLE legacy_sessions ( ... )
{
    DEPRECATED "Use the sessions table instead. Remove after 2026-01-01.";
}

TABLE users ( ... )
{
    COLUMN old_username {
        DEPRECATED "Use email field instead.";
    }
}

FUNCTION old_get_user(p_id BIGINT) RETURNS users
LANGUAGE sql AS $$ ... $$;
{
    DEPRECATED "Use get_user(TEXT) instead";
}
```

**Linter behavior:** Generates a warning for each deprecated object encountered during compilation. The warning includes the deprecation message.

---

## `PROTECTED`

Prevents the compiler from emitting `DROP` for this object, even if it is removed from source. Use for objects managed externally or that must never be dropped automatically.

**Applies to:** tables.

```
TABLE audit_trail ( ... )
{
    PROTECTED;
}
```

If `audit_trail` is removed from `.dpg` source while `PROTECTED` is set, the compiler errors rather than emitting `DROP TABLE`. Remove `PROTECTED` explicitly before dropping a protected table.

---

## `DROP CASCADE`

Declares that when this table is dropped, it should use `DROP ... CASCADE` rather than `DROP ... RESTRICT` (the default). Overrides the global `default_drop_behavior` in `dpg.toml` for this specific object.

```
TABLE scratch_data ( ... )
{
    DROP CASCADE;
}
```

Compiler emits: `DROP TABLE scratch_data CASCADE;` (classified as `DESTRUCTIVE`).

---

## `MIGRATE REMOVE` — ENUM Value Removal

Removing a value from a PostgreSQL ENUM type is not directly supported by `ALTER TYPE ... DROP VALUE`. DPG handles this with a safe seven-step migration procedure triggered by the `MIGRATE REMOVE` block.

```
ENUM order_status ('pending', 'confirmed', 'shipped', 'delivered');
{
    COMMENT "Order lifecycle states";
    MIGRATE REMOVE ('cancelled') {
        UPDATE orders SET status = 'delivered' WHERE status = 'cancelled';
    }
}
```

The `MIGRATE REMOVE` block lists the values to remove (as a parenthesised list) and contains DML statements to migrate existing rows away from those values before removal.

**Compiler procedure (emitted as a migration):**

1. `CREATE TYPE order_status__dpg_new AS ENUM ('pending', 'confirmed', 'shipped', 'delivered')` — the reduced value set.
2. Execute the DML inside `MIGRATE REMOVE` within a transaction (migrates existing rows).
3. Verify no rows still carry the removed values — abort with an error listing affected tables and row counts if any remain.
4. For each column typed as `order_status`: `ALTER TABLE t ALTER COLUMN c TYPE order_status__dpg_new USING c::text::order_status__dpg_new`.
5. `DROP TYPE order_status`.
6. `ALTER TYPE order_status__dpg_new RENAME TO order_status`.
7. On failure: `DROP TYPE IF EXISTS order_status__dpg_new` (cleanup).

**Requirements:**
- The DML inside `MIGRATE REMOVE { }` must migrate or delete all rows that carry the removed values before step 3 runs. If any remain, the migration aborts.
- Multiple values can be removed in one `MIGRATE REMOVE` block by listing them all: `MIGRATE REMOVE ('val1', 'val2') { ... }`.

**After the migration:** Remove the `MIGRATE REMOVE` block from source. It applies only to the single migration cycle where the removal occurs. If left in source, subsequent `dpg apply` runs will error because the values no longer exist.

---

## `NOT VALID` — Deferred Constraint Validation

`NOT VALID` is a PostgreSQL constraint option, not a DPG lifecycle directive. DPG tracks it across migrations. A constraint added as `NOT VALID` is added without scanning existing rows (faster, non-blocking). Removing `NOT VALID` in a subsequent migration causes DPG to emit `ALTER TABLE VALIDATE CONSTRAINT`, which scans existing rows.

```
-- Migration 1: add constraint without validating existing rows
TABLE orders ( ... )
{
    CONSTRAINT ck_amount_positive CHECK (amount > 0) NOT VALID;
}

-- Migration 2: validate existing rows (remove NOT VALID)
TABLE orders ( ... )
{
    CONSTRAINT ck_amount_positive CHECK (amount > 0);
    -- NOT VALID removed → compiler emits ALTER TABLE VALIDATE CONSTRAINT
}
```

A validated constraint may also be moved from the `{ }` block to the `( )` list — the compiler identifies it by name and treats it as already existing.
