---
title: "Linting"
description: "Built-in static analysis rules, severity levels, and dpg.toml configuration."
weight: 2
---

The linter runs automatically as part of `dpg plan`, `dpg apply`, and `dpg validate`. It runs over the merged IR before any SQL is generated. Lint **errors** abort the command. Lint **warnings** are printed to stderr but do not block.

## Configuration

```toml
# dpg.toml
[linter]
warn_on_deprecated            = true   # default
require_column_comments       = false  # default
forbid_hardcoded_passwords    = true   # default
max_columns_per_table         = 50     # default: 50 (0 = disabled)
warn_on_scalar_merge_conflict = true   # default
```

---

## `deprecated`

**Severity:** Warning — **Config:** `warn_on_deprecated` — **Default:** enabled

Warns when a table, column, view, or function is marked [`DEPRECATED`](lifecycle/).

```sql
TABLE legacy_sessions ( ... )
{ DEPRECATED "Use the jwt_tokens table instead"; }
```

```
warn  [deprecated] table public.legacy_sessions is deprecated: Use the jwt_tokens table instead
```

---

## `hardcoded-password`

**Severity:** Error — **Config:** `forbid_hardcoded_passwords` — **Default:** enabled

Errors when a column whose name contains `password`, `passwd`, `pwd`, `secret`, or `passphrase` has a string-literal `DEFAULT`.

```sql
TABLE service_accounts (
    password TEXT NOT NULL DEFAULT 'changeme',   -- error
    ...
);
```

```
error [hardcoded-password] column public.service_accounts.password default may contain a hardcoded password
```

Use a parameter or runtime value instead. For role passwords, use `env:VAR_NAME`. See [Roles](../../access-control/roles/).

---

## `security-definer-search-path`

**Severity:** Warning — **Config:** always enabled

Warns when a `SECURITY DEFINER` function does not set `search_path`.

```sql
-- triggers warning
FUNCTION unsafe_auth(p_user TEXT) RETURNS BOOLEAN
LANGUAGE plpgsql SECURITY DEFINER
AS $$ ... $$;
```

```
warn  [security-definer-search-path] SECURITY DEFINER function public.unsafe_auth should set search_path
```

Fix: add `SET search_path = public` to the signature line:

```sql
-- clean
FUNCTION safe_auth(p_user TEXT) RETURNS BOOLEAN
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public
AS $$ ... $$;
```

---

## `max-columns`

**Severity:** Error — **Config:** `max_columns_per_table` — **Default:** `50` (set to `0` to disable)

```toml
[linter]
max_columns_per_table = 50  # default; 0 = disabled
```

```
error [max-columns] table public.wide_table has 67 columns (max 50)
```

---

## `require-column-comments`

**Severity:** Warning — **Config:** `require_column_comments` — **Default:** disabled

```toml
[linter]
require_column_comments = true
```

```
warn  [require-column-comments] column public.users.created_at has no comment
```

Fix by adding a `COLUMN` block with a comment:

```sql
TABLE users ( created_at TIMESTAMPTZ NOT NULL DEFAULT now(), ... )
{
    COLUMN created_at { COMMENT "UTC timestamp when the record was created"; }
}
```

---

## `warn-on-scalar-merge-conflict`

**Severity:** Warning — **Config:** `warn_on_scalar_merge_conflict` — **Default:** enabled

Warns when the same scalar property (owner, comment, tablespace, etc.) is declared in multiple files for the same object. Last-declaration-wins applies (alphabetical by file path), but the conflict may indicate an unintentional discrepancy.

---

## Linter output format

```
warn  [deprecated] table public.legacy_sessions is deprecated: Use the jwt_tokens table instead
error [hardcoded-password] column public.service_accounts.password default may contain a hardcoded password
warn  [security-definer-search-path] SECURITY DEFINER function public.unsafe_auth should set search_path
```

Errors cause all commands to exit non-zero before any SQL is generated.
