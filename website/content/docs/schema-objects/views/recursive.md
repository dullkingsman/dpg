---
title: "Recursive Views"
description: "`RECURSIVE VIEW` declarations with a named working table."
weight: 3
---

## Recursive view

```sql
SCHEMA public {
    RECURSIVE VIEW org_tree (id, parent_id, depth, path) AS
        SELECT id, parent_id, 0, ARRAY[id]
        FROM departments WHERE parent_id IS NULL
        UNION ALL
        SELECT d.id, d.parent_id, t.depth + 1, t.path || d.id
        FROM departments d JOIN org_tree t ON d.parent_id = t.id;
}
```

```sql
-- emits
CREATE OR REPLACE RECURSIVE VIEW "public"."org_tree"
    ("id", "parent_id", "depth", "path") AS
    SELECT id, parent_id, 0, ARRAY[id]
    FROM departments WHERE parent_id IS NULL
    UNION ALL
    SELECT d.id, d.parent_id, t.depth + 1, t.path || d.id
    FROM departments d JOIN org_tree t ON d.parent_id = t.id;
```

Recursive views follow the same [diffing rules as regular views](../regular/): query body changes emit `CREATE OR REPLACE`; output column list changes emit `DROP` + `CREATE` (`DESTRUCTIVE`).
