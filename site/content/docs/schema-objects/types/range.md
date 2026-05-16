---
title: "Range"
description: "Custom range type over a subtype using `TYPE name AS RANGE (...)`."
weight: 3
---

## Basic range type

```sql
SCHEMA public {
    TYPE float8range AS RANGE (
        SUBTYPE      = float8,
        SUBTYPE_DIFF = float8mi
    );
}
```

```sql
-- emits
CREATE TYPE "public"."float8range" AS RANGE (
    SUBTYPE      = float8,
    SUBTYPE_DIFF = float8mi
);
```

## Range type with operator class and collation

```sql
SCHEMA public {
    TYPE ci_text_range AS RANGE (
        SUBTYPE           = text,
        SUBTYPE_OPCLASS   = text_pattern_ops,
        COLLATION         = "und-u-ks-level2"
    );
}
```

```sql
-- emits
CREATE TYPE "public"."ci_text_range" AS RANGE (
    SUBTYPE         = text,
    SUBTYPE_OPCLASS = text_pattern_ops,
    COLLATION       = "und-u-ks-level2"
);
```

Any change to a range type's parameters requires `DROP TYPE CASCADE` then `CREATE TYPE` — `DESTRUCTIVE`.
