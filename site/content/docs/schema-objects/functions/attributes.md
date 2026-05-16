---
title: "Function Attributes"
description: "Volatility, strictness, security, parallelism, cost, rows, support, SET, and WINDOW attributes."
weight: 2
---

All attributes appear on the signature line in PostgreSQL's own order — exactly as `CREATE FUNCTION` writes them.

## Volatility

```sql
FUNCTION rand_label() RETURNS TEXT LANGUAGE sql VOLATILE AS $$ ... $$;
FUNCTION now_tz()     RETURNS TEXT LANGUAGE sql STABLE   AS $$ ... $$;
FUNCTION pi_val()     RETURNS NUMERIC LANGUAGE sql IMMUTABLE AS $$ ... $$;
```

```sql
CREATE OR REPLACE FUNCTION "public"."rand_label"() RETURNS text LANGUAGE sql VOLATILE AS $$ ... $$;
CREATE OR REPLACE FUNCTION "public"."now_tz"()     RETURNS text LANGUAGE sql STABLE   AS $$ ... $$;
CREATE OR REPLACE FUNCTION "public"."pi_val"()     RETURNS numeric LANGUAGE sql IMMUTABLE AS $$ ... $$;
```

| Attribute | Meaning |
|-----------|---------|
| `VOLATILE` | Default. May modify DB; result may differ per call |
| `STABLE` | Constant within a transaction for given inputs |
| `IMMUTABLE` | Constant forever for given inputs; index-eligible |

## STRICT

```sql
FUNCTION safe_div(a NUMERIC, b NUMERIC) RETURNS NUMERIC
LANGUAGE sql IMMUTABLE STRICT
AS $$ SELECT a / b; $$;
```

```sql
CREATE OR REPLACE FUNCTION "public"."safe_div"(a numeric, b numeric)
RETURNS numeric LANGUAGE sql IMMUTABLE STRICT
AS $$ SELECT a / b; $$;
```

Returns `NULL` immediately if any argument is `NULL`.

## SECURITY DEFINER

```sql
FUNCTION admin_action(p_id UUID) RETURNS VOID
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public
AS $$ BEGIN ... END; $$;
```

```sql
CREATE OR REPLACE FUNCTION "public"."admin_action"(p_id uuid)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = public
AS $$ BEGIN ... END; $$;
```

`SECURITY DEFINER` functions execute with the owner's privileges. The linter warns if `search_path` is not set. See [Linting](../../../migrations/linting/).

## PARALLEL

```sql
FUNCTION count_active() RETURNS BIGINT LANGUAGE sql STABLE PARALLEL SAFE AS $$ ... $$;
FUNCTION with_lock()    RETURNS VOID   LANGUAGE sql PARALLEL RESTRICTED  AS $$ ... $$;
FUNCTION mutate()       RETURNS VOID   LANGUAGE plpgsql PARALLEL UNSAFE  AS $$ ... $$;
```

```sql
CREATE OR REPLACE FUNCTION "public"."count_active"() RETURNS bigint LANGUAGE sql STABLE PARALLEL SAFE AS $$ ... $$;
CREATE OR REPLACE FUNCTION "public"."with_lock"()    RETURNS void   LANGUAGE sql PARALLEL RESTRICTED  AS $$ ... $$;
CREATE OR REPLACE FUNCTION "public"."mutate"()       RETURNS void   LANGUAGE plpgsql PARALLEL UNSAFE  AS $$ ... $$;
```

## COST and ROWS

```sql
FUNCTION expensive_calc(p_id BIGINT) RETURNS NUMERIC
LANGUAGE plpgsql STABLE COST 500
AS $$ ... $$;

FUNCTION yield_rows(p_n INT) RETURNS SETOF INTEGER
LANGUAGE sql STABLE ROWS 100
AS $$ SELECT generate_series(1, p_n); $$;
```

```sql
CREATE OR REPLACE FUNCTION "public"."expensive_calc"(p_id bigint)
RETURNS numeric LANGUAGE plpgsql STABLE COST 500 AS $$ ... $$;

CREATE OR REPLACE FUNCTION "public"."yield_rows"(p_n integer)
RETURNS SETOF integer LANGUAGE sql STABLE ROWS 100
AS $$ SELECT generate_series(1, p_n); $$;
```

## SET (GUC per call)

```sql
FUNCTION tenant_fn(p_tenant_id UUID) RETURNS VOID
LANGUAGE plpgsql
SET search_path = public
SET app.current_tenant = 'default'
AS $$ ... $$;
```

```sql
CREATE OR REPLACE FUNCTION "public"."tenant_fn"(p_tenant_id uuid)
RETURNS void LANGUAGE plpgsql
SET search_path = public
SET app.current_tenant = 'default'
AS $$ ... $$;
```

## WINDOW

```sql
FUNCTION row_num() RETURNS BIGINT LANGUAGE internal WINDOW IMMUTABLE STRICT
AS $$ ... $$;
```

```sql
CREATE OR REPLACE FUNCTION "public"."row_num"()
RETURNS bigint LANGUAGE internal WINDOW IMMUTABLE STRICT AS $$ ... $$;
```

## SUPPORT

```sql
FUNCTION fast_fn(p_x NUMERIC) RETURNS NUMERIC
LANGUAGE c IMMUTABLE STRICT SUPPORT fast_fn_support
AS '$libdir/mylib', 'fast_fn';
```

```sql
CREATE OR REPLACE FUNCTION "public"."fast_fn"(p_x numeric)
RETURNS numeric LANGUAGE c IMMUTABLE STRICT SUPPORT fast_fn_support
AS '$libdir/mylib', 'fast_fn';
```
