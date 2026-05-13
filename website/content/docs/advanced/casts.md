---
title: "Casts"
description: "CAST declarations with function, implicit, assignment, and explicit cast contexts."
weight: 6
---

## Implicit cast

```sql
SCHEMA public {
    CAST (mytype AS TEXT)
        WITH FUNCTION mytype_to_text(mytype)
        AS IMPLICIT;
}
```

```sql
-- emits
CREATE CAST (mytype AS text)
    WITH FUNCTION mytype_to_text(mytype)
    AS IMPLICIT;
```

## Assignment cast

```sql
SCHEMA public {
    CAST (TEXT AS mytype)
        WITH FUNCTION text_to_mytype(TEXT)
        AS ASSIGNMENT;
}
```

```sql
-- emits
CREATE CAST (text AS mytype)
    WITH FUNCTION text_to_mytype(text)
    AS ASSIGNMENT;
```

## Explicit cast (default)

```sql
SCHEMA public {
    CAST (mytype AS INTEGER)
        WITH FUNCTION mytype_to_int(mytype);
}
```

```sql
-- emits
CREATE CAST (mytype AS integer)
    WITH FUNCTION mytype_to_int(mytype);
```

## INOUT cast

```sql
SCHEMA public {
    CAST (mytype AS TEXT) WITH INOUT;
}
```

```sql
-- emits
CREATE CAST (mytype AS text) WITH INOUT;
```

## Diffing behaviour

Cast identity is `(source_type, target_type)`. There is no `ALTER CAST` in PostgreSQL. Any property change emits `DROP CAST` + `CREATE CAST` — `DESTRUCTIVE`. Dependents are checked before the drop.
