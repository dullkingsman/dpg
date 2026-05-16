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

## Adding an attribute

Adding an attribute emits `ALTER TYPE ... ADD ATTRIBUTE` — `SAFE`.

```sql
-- before
TYPE contact AS (name TEXT, email TEXT);

-- after (add phone)
TYPE contact AS (name TEXT, email TEXT, phone TEXT);
```

```sql
-- emits (SAFE)
ALTER TYPE "public"."contact" ADD ATTRIBUTE "phone" text;
```

## Removing or changing an attribute

Removing an attribute or changing an attribute's type emits `DROP TYPE CASCADE` then `CREATE TYPE` — `DESTRUCTIVE`. All columns of this type in dependent tables must be retyped.

| Change | DDL emitted | Safety |
|--------|-------------|--------|
| Attribute added | `ALTER TYPE name ADD ATTRIBUTE col type` | `SAFE` |
| Attribute removed | `DROP TYPE CASCADE` + `CREATE TYPE` | `DESTRUCTIVE` |
| Attribute type changed | `DROP TYPE CASCADE` + `CREATE TYPE` | `DESTRUCTIVE` |
