---
title: "Domain"
description: "Constrained alias over a base type with `DOMAIN name AS type`."
weight: 4
---

## Basic domain

```sql
SCHEMA public {
    DOMAIN positive_integer AS INTEGER {
        DEFAULT 1;
        CONSTRAINT positive_only  CHECK (VALUE > 0);
        CONSTRAINT reasonable_max CHECK (VALUE < 1000000);
    }
}
```

```sql
-- emits
CREATE DOMAIN "public"."positive_integer" AS integer
    DEFAULT 1
    CONSTRAINT "positive_only"  CHECK (VALUE > 0)
    CONSTRAINT "reasonable_max" CHECK (VALUE < 1000000);
```

## Domain with NOT NULL

```sql
SCHEMA public {
    DOMAIN email_address AS TEXT {
        NOT NULL;
        CONSTRAINT valid_email CHECK (VALUE ~ '^[^@]+@[^@]+\.[^@]+$');
    }
}
```

```sql
-- emits
CREATE DOMAIN "public"."email_address" AS text
    NOT NULL
    CONSTRAINT "valid_email" CHECK (VALUE ~ '^[^@]+@[^@]+\.[^@]+$');
```

## Altering a domain constraint

Adding a new constraint emits `ALTER DOMAIN ADD CONSTRAINT`. Removing one emits `ALTER DOMAIN DROP CONSTRAINT`. Changing the base type or a `NOT NULL` constraint is `DESTRUCTIVE`.

```sql
-- add a new constraint (SAFE)
DOMAIN positive_integer AS INTEGER {
    DEFAULT 1;
    CONSTRAINT positive_only  CHECK (VALUE > 0);
    CONSTRAINT reasonable_max CHECK (VALUE < 1000000);
    CONSTRAINT even_only      CHECK (VALUE % 2 = 0);
}
```

```sql
-- emits
ALTER DOMAIN "public"."positive_integer" ADD CONSTRAINT "even_only" CHECK (VALUE % 2 = 0);
```
