---
title: "Constraints"
description: "Inline and table-level constraints, NOT VALID deferred validation, and foreign keys."
weight: 3
---

Constraints may appear inline in the `( )` list or inside the `{ }` block via a `CONSTRAINT` entry. Both placements are merged by name into one logical set before compilation.

## Inline constraints

```sql
TABLE accounts (
    id   UUID    NOT NULL DEFAULT gen_random_uuid() CONSTRAINT pk_accounts PRIMARY KEY,
    slug TEXT    NOT NULL CONSTRAINT uq_accounts_slug UNIQUE,
    org  UUID    NOT NULL CONSTRAINT fk_accounts_org REFERENCES organisations (id) ON DELETE CASCADE,
    CONSTRAINT ck_slug_len CHECK (length(slug) >= 3)
);
```

```sql
-- emits
CREATE TABLE "public"."accounts" (
    "id"   uuid NOT NULL DEFAULT gen_random_uuid() CONSTRAINT "pk_accounts" PRIMARY KEY,
    "slug" text NOT NULL CONSTRAINT "uq_accounts_slug" UNIQUE,
    "org"  uuid NOT NULL CONSTRAINT "fk_accounts_org"
               REFERENCES "public"."organisations" ("id") ON DELETE CASCADE,
    CONSTRAINT "ck_slug_len" CHECK (length(slug) >= 3)
);
```

## Multi-column constraints (table-level only)

```sql
TABLE order_items (
    order_id   BIGINT NOT NULL,
    product_id BIGINT NOT NULL,
    quantity   INTEGER NOT NULL DEFAULT 1,
    CONSTRAINT pk_order_items PRIMARY KEY (order_id, product_id),
    CONSTRAINT uq_order_product UNIQUE (order_id, product_id)
);
```

```sql
-- emits
CREATE TABLE "public"."order_items" (
    "order_id"   bigint NOT NULL,
    "product_id" bigint NOT NULL,
    "quantity"   integer NOT NULL DEFAULT 1,
    CONSTRAINT "pk_order_items"   PRIMARY KEY ("order_id", "product_id"),
    CONSTRAINT "uq_order_product" UNIQUE ("order_id", "product_id")
);
```

## Constraint in `{ }` block

Constraints in the `{ }` block are merged with those in `( )` by name.

```sql
TABLE orders (
    id     BIGINT GENERATED ALWAYS AS IDENTITY,
    amount NUMERIC(10,2) NOT NULL,
    CONSTRAINT pk_orders PRIMARY KEY (id)
)
{
    CONSTRAINT fk_account FOREIGN KEY (account_id)
        REFERENCES accounts (id)
        ON DELETE CASCADE;
}
```

```sql
-- emits
CREATE TABLE "public"."orders" (
    "id"     bigint GENERATED ALWAYS AS IDENTITY,
    "amount" numeric(10,2) NOT NULL,
    CONSTRAINT "pk_orders" PRIMARY KEY ("id")
);

ALTER TABLE "public"."orders"
    ADD CONSTRAINT "fk_account"
    FOREIGN KEY ("account_id") REFERENCES "public"."accounts" ("id")
    ON DELETE CASCADE;
```

## NOT VALID — deferred constraint validation

Adding a constraint with `NOT VALID` skips scanning existing rows. Removing `NOT VALID` in a subsequent migration emits `VALIDATE CONSTRAINT`, which scans existing rows (non-blocking with `ShareUpdateExclusiveLock`).

```sql
-- Migration 1: add without validating existing rows
TABLE orders ( ... )
{
    CONSTRAINT ck_amount_positive CHECK (amount > 0) NOT VALID;
}
```

```sql
-- emits
ALTER TABLE "public"."orders"
    ADD CONSTRAINT "ck_amount_positive" CHECK (amount > 0) NOT VALID;
```

```sql
-- Migration 2: validate (remove NOT VALID from source)
TABLE orders ( ... )
{
    CONSTRAINT ck_amount_positive CHECK (amount > 0);
}
```

```sql
-- emits
ALTER TABLE "public"."orders" VALIDATE CONSTRAINT "ck_amount_positive";
```

After validation, the constraint may be moved from `{ }` to the `( )` list — the compiler identifies it by name and treats it as already existing.

## DEFERRABLE foreign keys

Used to resolve circular foreign key dependencies. The compiler emits the circular FK as a separate `ALTER TABLE ADD CONSTRAINT` after both tables are created.

```sql
TABLE users (
    id          BIGINT GENERATED ALWAYS AS IDENTITY,
    active_post BIGINT,
    CONSTRAINT pk_users PRIMARY KEY (id)
)
{
    CONSTRAINT fk_active_post FOREIGN KEY (active_post)
        REFERENCES posts (id)
        DEFERRABLE INITIALLY DEFERRED;
}

TABLE posts (
    id      BIGINT GENERATED ALWAYS AS IDENTITY,
    user_id BIGINT NOT NULL REFERENCES users (id),
    CONSTRAINT pk_posts PRIMARY KEY (id)
);
```

```sql
-- emits
CREATE TABLE "public"."users" (
    "id"          bigint GENERATED ALWAYS AS IDENTITY,
    "active_post" bigint,
    CONSTRAINT "pk_users" PRIMARY KEY ("id")
);

CREATE TABLE "public"."posts" (
    "id"      bigint GENERATED ALWAYS AS IDENTITY,
    "user_id" bigint NOT NULL REFERENCES "public"."users" ("id"),
    CONSTRAINT "pk_posts" PRIMARY KEY ("id")
);

-- circular FK emitted after both tables exist:
ALTER TABLE "public"."users"
    ADD CONSTRAINT "fk_active_post"
    FOREIGN KEY ("active_post") REFERENCES "public"."posts" ("id")
    DEFERRABLE INITIALLY DEFERRED;
```
