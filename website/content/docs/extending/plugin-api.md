---
title: "Plugin API"
description: "Writing custom linters, snapshot stores, apply executors, and secret resolvers using the pkg/dpg public API."
weight: 2
---

The `pkg/dpg` package is the stable public surface for extending DPG. Import it from your plugin:

```go
import "github.com/dullkingsman/dpg/pkg/dpg"
```

No internal packages are required. Every interface, type, and helper function needed to build and register a custom pipeline stage is available through `pkg/dpg`.

---

## Custom Linter

The most common extension is a custom lint rule. Implement `dpg.Linter` and register it with `dpg.Default`.

### Implementing the Interface

```go
// tableCommentLinter warns when a table has no COMMENT directive.
type tableCommentLinter struct{}

func (l *tableCommentLinter) Lint(objects []dpg.IRObject, _ dpg.LinterConfig) ([]dpg.LintDiagnostic, error) {
    var diags []dpg.LintDiagnostic
    for _, obj := range objects {
        t, ok := obj.(*dpg.Table)
        if !ok {
            continue
        }
        if t.Comment == nil {
            diags = append(diags, dpg.LintDiagnostic{
                Pos:     t.SrcPos,
                Rule:    "require-table-comment",
                Message: fmt.Sprintf("table %s has no COMMENT directive", t.QualifiedName()),
            })
        }
    }
    return diags, nil
}
```

### Replacing the Built-in Linter

Register your linter to replace the built-in one entirely:

```go
func init() {
    dpg.Default.Register(dpg.KeyLinter, &tableCommentLinter{})
}
```

With this registration, `dpg validate` and `dpg plan` run only your linter.

### Augmenting the Built-in Linter

More commonly you want to keep the built-in rules and add your own. Use `dpg.NewChainLinter`:

```go
func init() {
    builtin, ok := dpg.ResolveLinter(dpg.Default)
    if !ok {
        panic("built-in linter not registered")
    }
    chained := dpg.NewChainLinter(builtin, &tableCommentLinter{})
    dpg.Default.Register(dpg.KeyLinter, chained)
}
```

`NewChainLinter` runs each linter in order and merges the diagnostics. The built-in linter always runs first.

### Testing Your Linter

```go
func TestTableCommentLinter(t *testing.T) {
    objects, err := dpg.Compile([]string{"testdata/schema.dpg"}, ".")
    if err != nil {
        t.Fatalf("compile: %v", err)
    }

    // Swap in our linter for this test only.
    original, _ := dpg.ResolveLinter(dpg.Default)
    dpg.Default.Register(dpg.KeyLinter, &tableCommentLinter{})
    t.Cleanup(func() { dpg.Default.Register(dpg.KeyLinter, original) })

    diags, err := dpg.Lint(objects, dpg.LinterConfig{})
    if err != nil {
        t.Fatalf("lint: %v", err)
    }

    if len(diags) == 0 {
        t.Fatal("expected diagnostics, got none")
    }
    t.Logf("found %d diagnostics", len(diags))
}
```

See `examples/plugin/plugin_test.go` for the full runnable example (`go test ./examples/plugin/... -v`).

---

## Custom Snapshot Store

By default DPG persists snapshots as JSON files under `.dpg/snapshots/<cluster>/<database>.json`. Implement `dpg.SnapshotStore` to use an alternative backend (database table, object storage, etc.).

```go
type dbSnapshotStore struct {
    db *sql.DB
}

func (s *dbSnapshotStore) Load(cluster, database string) (*dpg.Snapshot, error) {
    var data []byte
    err := s.db.QueryRow(
        `SELECT content FROM dpg_snapshots WHERE cluster=$1 AND database=$2`,
        cluster, database,
    ).Scan(&data)
    if errors.Is(err, sql.ErrNoRows) {
        return &dpg.Snapshot{}, nil // empty snapshot on first run
    }
    if err != nil {
        return nil, err
    }
    var snap dpg.Snapshot
    if err := json.Unmarshal(data, &snap); err != nil {
        return nil, err
    }
    return &snap, nil
}

func (s *dbSnapshotStore) Save(cluster, database string, snap *dpg.Snapshot) error {
    data, err := json.Marshal(snap)
    if err != nil {
        return err
    }
    _, err = s.db.Exec(
        `INSERT INTO dpg_snapshots (cluster, database, content)
         VALUES ($1, $2, $3)
         ON CONFLICT (cluster, database) DO UPDATE SET content = EXCLUDED.content`,
        cluster, database, data,
    )
    return err
}

// Register at startup:
func init() {
    dpg.Default.Register(dpg.KeySnapshotStore, &dbSnapshotStore{db: globalDB})
}
```

---

## Custom Apply Executor

Implement `dpg.ApplyExecutor` to wrap migration execution — useful for audit logging, dry-run gating, or multi-tenant routing.

```go
type auditExecutor struct {
    inner dpg.ApplyExecutor
    audit *sql.DB
}

func (e *auditExecutor) Apply(ctx context.Context, m dpg.Migration, conn dpg.Conn) error {
    start := time.Now()
    err := e.inner.Apply(ctx, m, conn)
    status := "ok"
    if err != nil {
        status = err.Error()
    }
    _, _ = e.audit.ExecContext(ctx,
        `INSERT INTO migration_log (sql, duration_ms, status) VALUES ($1, $2, $3)`,
        m.SQL, time.Since(start).Milliseconds(), status,
    )
    return err
}
```

---

## Custom Secret Resolver

Implement `dpg.SecretResolver` to add a custom secret provider (Vault, AWS Secrets Manager, etc.).

```go
type vaultResolver struct {
    client *vault.Client
}

func (r *vaultResolver) Resolve(uri string) (string, error) {
    if !strings.HasPrefix(uri, "vault://") {
        return "", fmt.Errorf("unsupported URI: %s", uri)
    }
    path := strings.TrimPrefix(uri, "vault://")
    secret, err := r.client.Logical().Read(path)
    if err != nil {
        return "", err
    }
    value, ok := secret.Data["value"].(string)
    if !ok {
        return "", fmt.Errorf("vault: no 'value' key at %s", path)
    }
    return value, nil
}
```

Chain the built-in `env:` resolver with your Vault resolver so both URI schemes work:

```go
func init() {
    existing, _ := dpg.ResolveSecretResolver(dpg.Default)
    chained := &chainResolver{first: existing, second: &vaultResolver{client: vc}}
    dpg.Default.Register(dpg.KeySecretResolver, chained)
}
```

---

## Available Public API

The full set of public functions and helpers in `pkg/dpg`:

| Function | Description |
|----------|-------------|
| `dpg.Compile(files []string, dbDir string)` | Scan → parse → IR → merge → topo sort |
| `dpg.Lint(objects, cfg)` | Run the registered Linter |
| `dpg.ResolveLinter(r)` | Retrieve the Linter from a registry |
| `dpg.ResolveDiffer(r)` | Retrieve the Differ from a registry |
| `dpg.ResolveEmitter(r)` | Retrieve the Emitter from a registry |
| `dpg.ResolveSecretResolver(r)` | Retrieve the SecretResolver from a registry |
| `dpg.NewChainLinter(linters...)` | Compose multiple linters |
| `dpg.NewRegistry()` | Create an isolated registry (for testing or embedding) |
| `dpg.Default` | The process-wide registry |

For godoc on all exported types, see the [pkg.go.dev documentation](https://pkg.go.dev/github.com/dullkingsman/dpg/pkg/dpg).
