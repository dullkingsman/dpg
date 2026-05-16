---
title: "Secrets"
generated: false
weight: 6
description: "Secret URI resolution (env:, link:) and where secrets appear in dpg.toml."
---


DPG resolves secret values at connection time, never at parse time or in compiled output. Secrets appear in two places: node connection strings (`url` / `link` fields in cluster config) and role password declarations.

## Secret URI Schemes

### `env:VAR_NAME`

Reads the value from an environment variable at runtime.

```toml
[cluster]
link = "env:PRIMARY_DB_URL"
```

```
ROLE app_service {
    LOGIN;
    PASSWORD 'env:APP_SERVICE_PW';
    CONNECTION LIMIT 20;
}
```

`env:PRIMARY_DB_URL` resolves to the value of the `PRIMARY_DB_URL` environment variable at the time `dpg apply`, `dpg verify`, or `dpg dump` runs. If the variable is not set, the command errors with a clear message.

### `link:...`

A secrets-provider URI resolved by an external secrets backend. The `link:` scheme is reserved for integration with secrets managers (Vault, AWS Secrets Manager, etc.). Implementation is provider-specific and not included in the base DPG binary.

### Plain values

A value that does not begin with `env:` or `link:` is returned as-is. This is valid for `url` fields (inline connection strings):

```toml
[cluster]
url = "postgresql://pguser@primary.prod.internal:5432/postgres"
```

Plain-value passwords in `ROLE` declarations are rejected by the `forbid_hardcoded_passwords` linter rule when the column/role name matches a password-like pattern.

## Where Secrets Appear

| Location | Field | Description |
|---|---|---|
| `[cluster]` in `<cluster>/dpg.toml` | `link` | Full connection string URI resolved at runtime |
| `[cluster]` in `<cluster>/dpg.toml` | `url` | Plain connection string (not a secret resolver) |
| Role declaration | `PASSWORD 'env:VAR'` | Role password |
| User mapping | `OPTIONS (password 'env:VAR')` | FDW user mapping credential |

## Security Model

- Secret values are never written to snapshot files.
- Secret values are never written to generated SQL files.
- Secret values are never logged by DPG.
- Resolution happens immediately before establishing a database connection.

## Example: Full Cluster Config with Secrets

```toml
# production/dpg.toml

[cluster]
name        = "production"
link        = "env:PROD_PRIMARY_URL"
```

```
-- production/cluster/roles.dpg

ROLE app_service {
    LOGIN;
    PASSWORD 'env:APP_SERVICE_PW';
    CONNECTION LIMIT 20;
    VALID UNTIL '2030-01-01';
}
```

```
-- production/myapp/schemas/public/fdw.dpg

SERVER analytics_warehouse
    FOREIGN DATA WRAPPER postgres_fdw
    OPTIONS (host 'warehouse.internal', dbname 'analytics', port '5432');

USER MAPPING FOR app_service
    SERVER analytics_warehouse
    OPTIONS (user 'fdw_user', password 'env:FDW_PASSWORD');
```
