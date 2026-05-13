---
title: "Partitioning"
description: "RANGE, LIST, and HASH partitioned tables with sub-partitions and partition management."
weight: 7
---

## RANGE partitioned table

```sql
TABLE events (
    id         BIGINT GENERATED ALWAYS AS IDENTITY,
    tenant_id  UUID NOT NULL,
    event_type TEXT NOT NULL,
    payload    JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
) PARTITION BY RANGE (created_at)
{
    PARTITIONS {
        events_2024_q1 FOR VALUES FROM ('2024-01-01') TO ('2024-04-01');
        events_2024_q2 FOR VALUES FROM ('2024-04-01') TO ('2024-07-01');
        events_2024_q3 FOR VALUES FROM ('2024-07-01') TO ('2024-10-01');
        events_2024_q4 FOR VALUES FROM ('2024-10-01') TO ('2025-01-01');
        events_default DEFAULT;
    }
}
```

```sql
-- emits
CREATE TABLE "public"."events" (
    "id"         bigint GENERATED ALWAYS AS IDENTITY,
    "tenant_id"  uuid NOT NULL,
    "event_type" text NOT NULL,
    "payload"    jsonb,
    "created_at" timestamptz NOT NULL DEFAULT now()
) PARTITION BY RANGE ("created_at");

CREATE TABLE "public"."events_2024_q1"
    PARTITION OF "public"."events"
    FOR VALUES FROM ('2024-01-01') TO ('2024-04-01');

CREATE TABLE "public"."events_2024_q2"
    PARTITION OF "public"."events"
    FOR VALUES FROM ('2024-04-01') TO ('2024-07-01');

CREATE TABLE "public"."events_2024_q3"
    PARTITION OF "public"."events"
    FOR VALUES FROM ('2024-07-01') TO ('2024-10-01');

CREATE TABLE "public"."events_2024_q4"
    PARTITION OF "public"."events"
    FOR VALUES FROM ('2024-10-01') TO ('2025-01-01');

CREATE TABLE "public"."events_default"
    PARTITION OF "public"."events" DEFAULT;
```

## LIST partitioned table

```sql
TABLE orders_by_region (
    id     BIGINT GENERATED ALWAYS AS IDENTITY,
    region TEXT NOT NULL
) PARTITION BY LIST (region)
{
    PARTITIONS {
        orders_north FOR VALUES IN ('NYC', 'BOS', 'PHI');
        orders_south FOR VALUES IN ('MIA', 'ATL', 'DAL');
    }
}
```

```sql
-- emits
CREATE TABLE "public"."orders_by_region" (
    "id"     bigint GENERATED ALWAYS AS IDENTITY,
    "region" text NOT NULL
) PARTITION BY LIST ("region");

CREATE TABLE "public"."orders_north"
    PARTITION OF "public"."orders_by_region"
    FOR VALUES IN ('NYC', 'BOS', 'PHI');

CREATE TABLE "public"."orders_south"
    PARTITION OF "public"."orders_by_region"
    FOR VALUES IN ('MIA', 'ATL', 'DAL');
```

## HASH partitioned table

```sql
TABLE sessions (
    id      UUID NOT NULL DEFAULT gen_random_uuid(),
    user_id BIGINT NOT NULL
) PARTITION BY HASH (user_id)
{
    PARTITIONS {
        sessions_0 FOR VALUES WITH (MODULUS 4, REMAINDER 0);
        sessions_1 FOR VALUES WITH (MODULUS 4, REMAINDER 1);
        sessions_2 FOR VALUES WITH (MODULUS 4, REMAINDER 2);
        sessions_3 FOR VALUES WITH (MODULUS 4, REMAINDER 3);
    }
}
```

```sql
-- emits
CREATE TABLE "public"."sessions" (
    "id"      uuid NOT NULL DEFAULT gen_random_uuid(),
    "user_id" bigint NOT NULL
) PARTITION BY HASH ("user_id");

CREATE TABLE "public"."sessions_0"
    PARTITION OF "public"."sessions"
    FOR VALUES WITH (MODULUS 4, REMAINDER 0);

-- ... (sessions_1, sessions_2, sessions_3 follow same pattern)
```

## Sub-partitioning

```sql
TABLE events ( ... ) PARTITION BY RANGE (created_at)
{
    PARTITIONS {
        events_2024 FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')
            PARTITION BY LIST (region) {
                PARTITIONS {
                    events_2024_us FOR VALUES IN ('us-east', 'us-west');
                    events_2024_eu FOR VALUES IN ('eu-west', 'eu-central');
                }
            };
    }
}
```

```sql
-- emits
CREATE TABLE "public"."events" ( ... ) PARTITION BY RANGE ("created_at");

CREATE TABLE "public"."events_2024"
    PARTITION OF "public"."events"
    FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')
    PARTITION BY LIST ("region");

CREATE TABLE "public"."events_2024_us"
    PARTITION OF "public"."events_2024"
    FOR VALUES IN ('us-east', 'us-west');

CREATE TABLE "public"."events_2024_eu"
    PARTITION OF "public"."events_2024"
    FOR VALUES IN ('eu-west', 'eu-central');
```

## Partition management

| Operation | Safety | Notes |
|-----------|--------|-------|
| Add a partition | `SAFE` | `CREATE TABLE ... PARTITION OF` |
| Remove a partition | `DESTRUCTIVE` | `DROP TABLE partition_name` |
| Change partition strategy | `MANUAL` | Requires `--approve-partition-rebuild`; full table rebuild |

Indexes and grants declared in the parent table's `{ }` block apply to all partitions.
