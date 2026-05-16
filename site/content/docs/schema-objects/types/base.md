---
title: "Base (Shell) Type"
description: "C-implemented base types declared with `TYPE name (...)`."
weight: 6
---

Base types (also called shell types) are for C-implemented custom types. They require input and output functions already installed in the database.

## Base type declaration

```sql
SCHEMA public {
    TYPE mytype (
        INPUT          = mytype_in,
        OUTPUT         = mytype_out,
        INTERNALLENGTH = 16,
        ALIGNMENT      = double
    );
}
```

```sql
-- emits
CREATE TYPE "public"."mytype" (
    INPUT          = mytype_in,
    OUTPUT         = mytype_out,
    INTERNALLENGTH = 16,
    ALIGNMENT      = double
);
```

## Full base type with all options

```sql
SCHEMA public {
    TYPE complex (
        INPUT          = complex_in,
        OUTPUT         = complex_out,
        RECEIVE        = complex_recv,
        SEND           = complex_send,
        INTERNALLENGTH = 16,
        ALIGNMENT      = double,
        STORAGE        = plain,
        PASSEDBYVALUE
    );
}
```

```sql
-- emits
CREATE TYPE "public"."complex" (
    INPUT          = complex_in,
    OUTPUT         = complex_out,
    RECEIVE        = complex_recv,
    SEND           = complex_send,
    INTERNALLENGTH = 16,
    ALIGNMENT      = double,
    STORAGE        = plain,
    PASSEDBYVALUE
);
```

Any change to a base type requires `DROP TYPE CASCADE` then `CREATE TYPE` — `DESTRUCTIVE`.
