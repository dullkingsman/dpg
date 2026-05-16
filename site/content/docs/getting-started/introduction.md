---
title: "Introduction"
description: "What DPG is, why it exists, and the five core tenets that guide every design decision."
weight: 1
---

DPG is a declarative, state-based superset of PostgreSQL SQL. You describe what your database *should be*; the compiler figures out what needs to change and generates the minimal, safe set of DDL statements to reach that state.

## The problem with imperative migrations

PostgreSQL DDL is fundamentally imperative. Migration files describe *actions taken at a point in time*, not the *current intended state* of the database:

- Drift accumulates as production databases are patched and hotfixed outside the migration history.
- To understand the current schema, you must mentally replay every migration file from the beginning.
- Running a migration file twice will fail or corrupt state — idempotency is never guaranteed.
- `ALTER TABLE public.users ADD CONSTRAINT ...` re-states the schema and table name in every alteration, even though the context is already known.

## The DPG answer

Instead of writing commands, you write a description of the desired state. The compiler compares that description against a committed snapshot and generates the precise SQL delta. If the database already matches the description, the output is empty.

## Five core tenets

**Tenet 1 — Full PostgreSQL feature parity.** DPG must be capable of expressing anything raw PostgreSQL DDL can express. If a PG feature cannot be declared in DPG, that is a bug in DPG, not an out-of-scope request.

**Tenet 2 — Prefer PG syntax exactly.** DPG removes imperative verbs (`CREATE`, `ALTER`, `DROP`) and adds structural scoping, but does not invent new keywords for concepts PostgreSQL already names well.

**Tenet 3 — SQL/PG-extension boundary tracked internally.** The compiler knows which constructs are ISO SQL and which are PG-specific. Users never annotate portability; the compiler surfaces this via `dpg portability`.

**Tenet 4 — Offline-first diffing.** `dpg plan` and `dpg diff` never require a live database connection. The primary workflow compares `.dpg` source files against a committed snapshot. Live catalog introspection is available for verification and bootstrap, not required for day-to-day operation.

**Tenet 5 — The `{ }` block holds only what PG SQL cannot.** The native PG SQL definition of an object is written exactly as PG SQL dictates. The trailing `{ }` block exists exclusively for things PG SQL expresses as separate DDL statements and for DPG lifecycle directives. Nothing that has a natural place in PG SQL's own syntax is moved into the `{ }` block.

## What makes DPG different

| Tool | Approach |
|------|----------|
| **Flyway / Liquibase** | Migration-based — manage history of changes, not desired state |
| **Atlas** | HCL schema language — foreign DSL to a PG developer |
| **Prisma** | Invents parallel concepts (`@id`, `@relation`) — PG-specific features inaccessible |
| **DPG** | PG SQL is already a nearly complete declarative schema language — DPG adds structural scaffolding and a diff engine and nothing else |

## Next steps

- [Installation](../installation/) — build from source and verify the binary
- [Quick Start](../quick-start/) — create your first project and run a migration in five minutes
- [Two-Part Syntax](../../fundamentals/two-part-syntax/) — the core language model
