---
title: "Linter"
generated: false
weight: 4
description: "Built-in static analysis rules, configuration keys, and severity classification."
---


The linter runs automatically as part of `dpg plan`, `dpg apply`, and `dpg validate`. It runs over the merged IR (compiled object graph) and produces diagnostics before any SQL is generated. Lint **errors** abort the command. Lint **warnings** are printed to stderr but do not block.

## Configuration

Linter rules are configured in `dpg.toml`:

```toml
[linter]
warn_on_deprecated            = true   # default
require_column_comments       = false  # default
forbid_hardcoded_passwords    = true   # default
max_columns_per_table         = 50     # default: 0 (disabled)
warn_on_scalar_merge_conflict = true   # default
```

## Rules

### `deprecated`

**Severity:** Warning  
**Config key:** `warn_on_deprecated`  
**Default:** enabled

Warns when a table, column, view, or function is marked `DEPRECATED`.

```
TABLE legacy_sessions ( ... )
{
    DEPRECATED "Use the jwt_tokens table instead";
}
```

Diagnostic: `table public.legacy_sessions is deprecated: Use the jwt_tokens table instead`

Also fires on deprecated columns:

```
TABLE users ( ... )
{
    COLUMN old_username {
        DEPRECATED "Use email instead";
    }
}
```

---

### `hardcoded-password`

**Severity:** Error  
**Config key:** `forbid_hardcoded_passwords`  
**Default:** enabled

Errors when a column whose name contains `password`, `passwd`, `pwd`, `secret`, or `passphrase` has a string-literal `DEFAULT` value.

```
TABLE service_accounts (
    id       BIGINT GENERATED ALWAYS AS IDENTITY,
    password TEXT   NOT NULL DEFAULT 'changeme',   -- error
    CONSTRAINT pk PRIMARY KEY (id)
);
```

Diagnostic: `column public.service_accounts.password default may contain a hardcoded password`

**Fix:** Use a parameter or runtime value instead. For role passwords, use `env:VAR_NAME` syntax.

---

### `security-definer-search-path`

**Severity:** Warning  
**Config key:** always enabled (no config key)

Warns when a `SECURITY DEFINER` function does not contain `search_path` in its body. Functions that execute with owner privileges and have an unconstrained `search_path` are vulnerable to search path injection attacks.

```
FUNCTION unsafe_auth(p_user TEXT) RETURNS BOOLEAN
LANGUAGE plpgsql SECURITY DEFINER
AS $$
BEGIN
    RETURN EXISTS (SELECT 1 FROM users WHERE email = p_user);
END;
$$;
```

Diagnostic: `SECURITY DEFINER function public.unsafe_auth should set search_path`

**Fix:** Add `SET search_path = public` (or appropriate schema) to the function signature in Part 1:

```
FUNCTION safe_auth(p_user TEXT) RETURNS BOOLEAN
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public
AS $$
BEGIN
    RETURN EXISTS (SELECT 1 FROM users WHERE email = p_user);
END;
$$;
```

The linter checks whether `search_path` appears anywhere in the function body text. Setting it on the signature line (outside `$$`) is the recommended approach.

---

### `max-columns`

**Severity:** Error  
**Config key:** `max_columns_per_table`  
**Default:** `0` (disabled)

Errors when a table has more columns than the configured limit. A value of `0` disables the rule.

```toml
[linter]
max_columns_per_table = 50
```

Diagnostic: `table public.wide_table has 67 columns (max 50)`

---

### `require-column-comments`

**Severity:** Warning  
**Config key:** `require_column_comments`  
**Default:** disabled

Warns when a column has no `COMMENT` directive in its `COLUMN` block. Enable to enforce documentation coverage on column definitions.

```toml
[linter]
require_column_comments = true
```

Diagnostic: `column public.users.created_at has no comment`

To satisfy this rule, add a `COLUMN` block with a comment:

```
TABLE users ( created_at TIMESTAMPTZ NOT NULL DEFAULT now(), ... )
{
    COLUMN created_at {
        COMMENT "UTC timestamp when the record was created";
    }
}
```

---

### `warn-on-scalar-merge-conflict`

**Severity:** Warning  
**Config key:** `warn_on_scalar_merge_conflict`  
**Default:** enabled

Warns when the same scalar property (owner, comment, tablespace, deprecated message, etc.) is declared in multiple files for the same object. Last-declaration-wins applies (alphabetical by file path), but the conflict may indicate an unintentional discrepancy.

Example: `TABLE users` declared in `tables/users.dpg` with `OWNER "app_role"` and also in `tables/users_grants.dpg` with `OWNER "admin_role"` — one of them will win silently.

## Linter Output Format

Lint output goes to stderr:

```
warn  [deprecated] table public.legacy_sessions is deprecated: Use the jwt_tokens table instead
error [hardcoded-password] column public.service_accounts.password default may contain a hardcoded password
warn  [security-definer-search-path] SECURITY DEFINER function public.unsafe_auth should set search_path
```

Errors cause `dpg plan`, `dpg apply`, and `dpg validate` to exit non-zero before any SQL is generated.
