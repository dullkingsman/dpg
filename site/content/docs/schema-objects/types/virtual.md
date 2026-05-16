---
title: "Virtual"
description: "`VIRTUAL TYPE` — DPG-only annotation that generates no SQL, used for code generation."
weight: 5
---

Virtual types are DPG-native annotations. They generate no SQL whatsoever — no `CREATE TYPE`, `ALTER TYPE`, or `DROP TYPE` is ever emitted. They exist to carry type information for code-generation tools that read the DPG snapshot or IR.

## String union

```sql
VIRTUAL TYPE user_state AS "active" | "suspended" | "deleted";
```

```sql
-- emits nothing
```

## Discriminated union with comment

```sql
VIRTUAL TYPE billing.payment_method AS
    { kind: "card", last4: string, brand: string }
    | { kind: "bank_ach", routing: string }
    | { kind: "wallet" };
{
    COMMENT "Payment method discriminated union for type generation";
}
```

```sql
-- emits nothing
-- (COMMENT is stored in the snapshot for tooling consumers, not emitted as SQL)
```

## Rules

- The body after `AS` is arbitrary text stored verbatim. The compiler does not interpret it.
- Virtual types appear in the snapshot under `"kind": "virtual_type"` so code-generation tools can read them.
- `dpg plan` produces no SQL for additions, modifications, or removals of virtual types.
- The `{ }` block accepts only `COMMENT`.
- Code-generation tools consume virtual types via the snapshot JSON or the `pkg/dpg` Go API. See [Plugin API](../../../extending/plugin-api/).
