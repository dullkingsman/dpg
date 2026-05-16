---
title: "Composite"
description: "Row-shaped composite type declared with `TYPE name AS (...)`."
weight: 2
---

## Basic composite type

```sql
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

```sql
-- emits
CREATE TYPE "public"."address" AS (
    "street"      text,
    "city"        text,
    "state"       char(2),
    "postal_code" text,
    "country"     char(2)
);
```

## Adding or removing an attribute

Composite type changes that add or remove attributes require `DROP TYPE CASCADE` then `CREATE TYPE` — this is classified as `DESTRUCTIVE`. Columns of this type in any table must be retyped.

```sql
-- before
TYPE contact AS (name TEXT, email TEXT);

-- after (add phone)
TYPE contact AS (name TEXT, email TEXT, phone TEXT);
```

```sql
-- emits (DESTRUCTIVE)
DROP TYPE "public"."contact" CASCADE;
CREATE TYPE "public"."contact" AS (
    "name"  text,
    "email" text,
    "phone" text
);
```
