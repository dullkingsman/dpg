---
title: "Macros"
description: "Named reusable fragments spread into column lists or block bodies with the `...name` operator."
weight: 3
---

Macros are source-level text fragments defined once and spread inline at any number of call sites. The preprocessor expands them before parsing — the compiler sees only the expanded result.

`MACRO` declarations generate no SQL. They must appear at the top level of a `.dpg` file, not inside blocks.

## Paren-body macro — column fragments

A paren-body macro spreads into a `( )` column list.

```sql
MACRO common_timestamps (
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ
)

TABLE accounts (
    id   UUID NOT NULL DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    ...common_timestamps,
    CONSTRAINT pk_accounts PRIMARY KEY (id)
);
```

```sql
-- emits (macro expanded inline)
CREATE TABLE "public"."accounts" (
    "id"         uuid NOT NULL DEFAULT gen_random_uuid(),
    "name"       text NOT NULL,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "updated_at" timestamptz,
    CONSTRAINT "pk_accounts" PRIMARY KEY ("id")
);
```

## Brace-body macro — block fragments

A brace-body macro spreads into a `{ }` block.

```sql
MACRO audit_block {
    OWNER "app_admin";
    ENABLE ROW LEVEL SECURITY;
}

TABLE orders (
    id     BIGINT GENERATED ALWAYS AS IDENTITY,
    amount NUMERIC(10,2) NOT NULL,
    CONSTRAINT pk_orders PRIMARY KEY (id)
)
{
    ...audit_block
    GRANTS { SELECT TO app_readonly; }
}
```

```sql
-- emits (macro expanded inline)
CREATE TABLE "public"."orders" (
    "id"     bigint GENERATED ALWAYS AS IDENTITY,
    "amount" numeric(10,2) NOT NULL,
    CONSTRAINT "pk_orders" PRIMARY KEY ("id")
);

ALTER TABLE "public"."orders" OWNER TO "app_admin";
ALTER TABLE "public"."orders" ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON TABLE "public"."orders" TO "app_readonly";
```

## Cross-file macros

Macros are project-scoped. You can define shared macros in a dedicated file and spread them anywhere else in the same database compilation scope.

```sql
-- macros.dpg
MACRO common_timestamps (
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ
)

MACRO audit_block {
    OWNER "app_admin";
    ENABLE ROW LEVEL SECURITY;
}
```

```sql
-- tables/accounts.dpg  (no MACRO declaration needed)
TABLE accounts (
    id   UUID NOT NULL DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    ...common_timestamps,
    CONSTRAINT pk_accounts PRIMARY KEY (id)
) { ...audit_block }
```

```sql
-- tables/orders.dpg  (same macros, different file)
TABLE orders (
    id     BIGINT GENERATED ALWAYS AS IDENTITY,
    amount NUMERIC(10,2) NOT NULL,
    ...common_timestamps,
    CONSTRAINT pk_orders PRIMARY KEY (id)
) { ...audit_block }
```

A file-local `MACRO` declaration with the same name as a cross-file macro takes precedence, so individual files can specialise a shared definition without affecting others.

## Rules

- A paren-body macro may only be spread inside a `( )` list.
- A brace-body macro may only be spread inside a `{ }` block.
- Spreading an undefined macro name is a compile-time error.
- Macros are **project-scoped**: a macro defined in any `.dpg` file is available in every other file within the same compilation scope (all files for a given database). Declaration order across files does not matter.
- A file-local `MACRO` declaration overrides a same-named macro from another file, letting individual files specialise a shared definition.
- `MACRO` does not violate the no-verb mandate; it is a DPG preprocessor keyword, not a PostgreSQL keyword.
