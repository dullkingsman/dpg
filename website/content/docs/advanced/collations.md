---
title: "Collations"
description: "COLLATION declarations with provider, locale, and determinism."
weight: 4
---

## ICU collation (non-deterministic)

Non-deterministic collations allow case-insensitive and accent-insensitive comparisons.

```sql
SCHEMA public {
    COLLATION case_insensitive (
        PROVIDER      = icu,
        LOCALE        = 'und-u-ks-level2',
        DETERMINISTIC = false
    );
}
```

```sql
-- emits
CREATE COLLATION "public"."case_insensitive" (
    PROVIDER      = icu,
    LOCALE        = 'und-u-ks-level2',
    DETERMINISTIC = false
);
```

## Standard libc collation

```sql
SCHEMA public {
    COLLATION custom_en (
        PROVIDER = libc,
        LOCALE   = 'en_US.UTF-8'
    );
}
```

```sql
-- emits
CREATE COLLATION "public"."custom_en" (
    PROVIDER = libc,
    LOCALE   = 'en_US.UTF-8'
);
```

## Collation copy

```sql
SCHEMA public {
    COLLATION my_collation (COPY = pg_catalog."default");
}
```

```sql
-- emits
CREATE COLLATION "public"."my_collation" (COPY = pg_catalog."default");
```

## Diffing behaviour

Any change to a collation's properties requires `DROP COLLATION` + `CREATE COLLATION` — `DESTRUCTIVE`. Columns using the collation must be re-indexed after recreation.
