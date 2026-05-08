---
title: "Portability Analysis"
generated: false
weight: 7
description: "PostgreSQL-specific construct reporting and standard SQL alternatives."
---


`dpg portability` scans compiled IR objects and reports all PostgreSQL-specific constructs. This is informational only — it never blocks compilation or apply.

The compiler knows internally which constructs are ISO/IEC 9075 Standard SQL and which are PG-specific. You never annotate portability yourself.

## Flagged Constructs

### Extensions

| Construct | Standard SQL alternative |
|---|---|
| `CREATE EXTENSION` | Extensions are PostgreSQL-specific; no standard SQL equivalent. |

### Types

| Construct | Standard SQL alternative |
|---|---|
| `CREATE TYPE AS ENUM` | PG ENUM is non-standard; use a lookup table with a FK constraint. |
| `CREATE TYPE AS RANGE` | PG range types have no standard SQL equivalent. |
| `CREATE TYPE (base/shell)` | Base types are PG-specific; no standard equivalent. |

### Column Types

| Construct | Standard SQL alternative |
|---|---|
| `jsonb` | Use JSON (standard) instead of JSONB for portability. |
| `uuid` | UUID is not in SQL standard; use CHAR(36) or BINARY(16). |
| `bytea` | Use BLOB / BINARY for portability. |
| `tsquery` | PG full-text search type; no standard equivalent. |
| `tsvector` | PG full-text search type; no standard equivalent. |
| `inet` | PG network type; use VARCHAR for portability. |
| `cidr` | PG network type; use VARCHAR for portability. |
| `macaddr` | PG network type; use VARCHAR for portability. |
| `point` | PG geometric type; use PostGIS or GEOMETRY for portability. |
| `hstore` | PG key-value type; use JSON/JSONB for portability. |
| `xml` | XML is in SQL standard but rarely portable across vendors. |
| `int4range` | PG range type; no standard equivalent. |
| `int8range` | PG range type; no standard equivalent. |
| `numrange` | PG range type; no standard equivalent. |
| `tsrange` | PG range type; no standard equivalent. |
| `tstzrange` | PG range type; no standard equivalent. |
| `daterange` | PG range type; no standard equivalent. |
| `COMPRESSION` | Column-level compression is PG 14+; no standard equivalent. |

### Tables

| Construct | Standard SQL alternative |
|---|---|
| `UNLOGGED TABLE` | Not in SQL standard; use regular TABLE for portability. |
| `ROW LEVEL SECURITY` | PG-specific; use application-level access control for portability. |
| `CREATE POLICY` | PG-specific row-security policy; no standard SQL equivalent. |
| `PARTITION BY` | Declarative partitioning is PG 10+; syntax differs across vendors. |
| `INDEX USING GIN` | Only BTREE index type is in the SQL standard. |
| `INDEX USING GIST` | Only BTREE index type is in the SQL standard. |
| `INDEX USING BRIN` | Only BTREE index type is in the SQL standard. |
| `INDEX USING HASH` | Only BTREE index type is in the SQL standard. |
| `INDEX USING SPGIST` | Only BTREE index type is in the SQL standard. |

### Functions

| Construct | Standard SQL alternative |
|---|---|
| `LANGUAGE plpgsql` | PL/pgSQL is PG-specific; use SQL functions for portability. |
| `SECURITY DEFINER` | PG-specific; standard SQL uses roles. |

## Output Format

```
-- production/myapp: 4 portability issue(s)

  [schemas/public/types.dpg:3] CREATE TYPE AS ENUM
    → PG ENUM is non-standard; use a lookup table with a FK constraint.

  [schemas/public/tables/events.dpg:6] UNLOGGED TABLE
    → Not in SQL standard; use regular TABLE for portability.

  [schemas/public/tables/events.dpg:9] jsonb
    → Use JSON (standard) instead of JSONB for portability.

  [schemas/public/functions.dpg:2] LANGUAGE plpgsql
    → PL/pgSQL is PG-specific; use SQL functions for portability.
```

When no issues are found:

```
-- production/myapp: no portability issues found
```
