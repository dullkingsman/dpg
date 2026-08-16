//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vaultapi "github.com/hashicorp/vault/api"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/pipeline"
	_ "github.com/dullkingsman/dpg/internal/secrets"       // registers the default ChainResolver
	_ "github.com/dullkingsman/dpg/internal/secrets/vault" // registers the "vault" scheme
	"github.com/dullkingsman/dpg/internal/testpg"
)

// startVaultDev launches a HashiCorp Vault dev-mode container and returns
// its host-reachable address and root token. Unlike internal/secrets/vault's
// own TestResolveLiveVault (which only runs when VAULT_ADDR is already set,
// since this repo has no existing testcontainers wiring for Vault), this
// test spins up its own — it needs a specific secret seeded, not just any
// reachable server.
func startVaultDev(t *testing.T) (addr, token string) {
	t.Helper()
	ctx := context.Background()
	const rootToken = "root-token"

	req := testcontainers.ContainerRequest{
		Image:        "hashicorp/vault:1.15",
		ExposedPorts: []string{"8200/tcp"},
		Env:          map[string]string{"VAULT_DEV_ROOT_TOKEN_ID": rootToken},
		Cmd:          []string{"server", "-dev", "-dev-listen-address=0.0.0.0:8200"},
		WaitingFor:   wait.ForLog("Development mode should NOT be used in production installations"),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("startVaultDev: start container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Errorf("startVaultDev: terminate container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("startVaultDev: get host: %v", err)
	}
	port, err := container.MappedPort(ctx, "8200")
	if err != nil {
		t.Fatalf("startVaultDev: get port: %v", err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port()), rootToken
}

// mustPortFromConnStr extracts the mapped host port from a
// "postgres://user:pass@host:port/db?..." connection string, as returned by
// testpg.Start/StartLogical.
func mustPortFromConnStr(t *testing.T, connStr string) string {
	t.Helper()
	u, err := url.Parse(connStr)
	if err != nil {
		t.Fatalf("mustPortFromConnStr: %v", err)
	}
	return u.Port()
}

// TestSubscriptionConnectionSecretRoundtrip proves Phase 3's actual new
// behavior end-to-end against real servers, not fakes: a SUBSCRIPTION whose
// CONNECTION is a {{vault:...}} reference (a) is accepted by, and actually
// works against, a real PostgreSQL publisher/subscriber pair — a bad
// substitution (wrong quoting, wrong resolved value) would show up as a
// real connection failure here, not a mocked assertion — and (b) the
// archived migration text never contains the resolved plaintext connection
// string, confirmed by inspecting the same DiffOp.SQL() that cmd/dpg's
// apply.go archives to disk and shows the operator for approval.
func TestSubscriptionConnectionSecretRoundtrip(t *testing.T) {
	ctx := context.Background()

	// ── Vault: seed the publisher's real conninfo, as reachable from
	// inside the subscriber container (host.docker.internal + the
	// publisher's host-mapped port — the two containers aren't on a shared
	// docker network here, only individually port-mapped to the host).
	vaultAddr, vaultToken := startVaultDev(t)
	t.Setenv("VAULT_ADDR", vaultAddr)
	t.Setenv("VAULT_TOKEN", vaultToken)

	pubConnStr := testpg.StartLogical(t)
	pubPort := mustPortFromConnStr(t, pubConnStr)
	// As seen from inside another container, not from this test process.
	realConnInfo := fmt.Sprintf("host=host.docker.internal port=%s dbname=dpgtest user=dpg password=dpg", pubPort)

	vc, err := vaultapi.NewClient(&vaultapi.Config{Address: vaultAddr})
	if err != nil {
		t.Fatalf("vault client: %v", err)
	}
	vc.SetToken(vaultToken)
	if _, err := vc.KVv2("secret").Put(ctx, "repl/pub", map[string]any{"conninfo": realConnInfo}); err != nil {
		t.Fatalf("seed vault secret: %v", err)
	}

	// ── Publisher: a real publication to subscribe to.
	pubConn, err := executor.Connect(ctx, pubConnStr)
	if err != nil {
		t.Fatalf("connect to publisher: %v", err)
	}
	defer pubConn.Close(ctx)
	if _, err := pubConn.Exec(ctx, "CREATE PUBLICATION my_pub FOR ALL TABLES;"); err != nil {
		t.Fatalf("create publication: %v", err)
	}

	// ── Subscriber: needs host.docker.internal to resolve back to this
	// Docker host, so it can reach the publisher's host-mapped port.
	subReq := testcontainers.ContainerRequest{
		Image:        "postgres:17",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "dpg",
			"POSTGRES_PASSWORD": "dpg",
			"POSTGRES_DB":       "dpgtest",
		},
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
		WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
	}
	subContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: subReq,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start subscriber container: %v", err)
	}
	t.Cleanup(func() {
		if err := subContainer.Terminate(context.Background()); err != nil {
			t.Errorf("terminate subscriber container: %v", err)
		}
	})
	subHost, err := subContainer.Host(ctx)
	if err != nil {
		t.Fatalf("subscriber host: %v", err)
	}
	subPort, err := subContainer.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("subscriber port: %v", err)
	}
	subConnStr := fmt.Sprintf("postgres://dpg:dpg@%s:%s/dpgtest?sslmode=disable", subHost, subPort.Port())
	subConn, err := executor.Connect(ctx, subConnStr)
	if err != nil {
		t.Fatalf("connect to subscriber: %v", err)
	}
	defer subConn.Close(ctx)

	// ── DPG source: the secret reference embedded directly in the native
	// CONNECTION literal (RFC §13.2) — no separate block form.
	fixture := "SUBSCRIPTION my_sub\n" +
		"    CONNECTION '{{vault:secret/repl/pub#conninfo}}'\n" +
		"    PUBLICATION my_pub\n" +
		"    WITH (enabled = true, copy_data = false)\n" +
		"{\n" +
		"    COMMENT 'replication for orders';\n" +
		"}\n"
	dir := t.TempDir()
	f := filepath.Join(dir, "sub.dpg")
	if err := os.WriteFile(f, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	desired, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	differ := diff.New()
	ops, err := differ.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (CREATE SUBSCRIPTION + COMMENT ON SUBSCRIPTION), got %d: %v", len(ops), ops)
	}

	// ── Redaction guard: the op's displayed/archived SQL — exactly what
	// cmd/dpg's apply.go writes to the migration file and shows the
	// operator before executing — must hold the placeholder, never the
	// resolved secret.
	sql := ops[0].SQL()
	if !strings.Contains(sql, "{{vault:secret/repl/pub#conninfo}}") {
		t.Errorf("expected the archived/displayed SQL to keep the {{...}} placeholder, got: %s", sql)
	}
	if strings.Contains(sql, "host.docker.internal") || strings.Contains(sql, "password=dpg") {
		t.Fatalf("archived/displayed SQL leaked the resolved connection info: %s", sql)
	}

	emitter := emit.New()
	migration, err := emitter.Emit(ops, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	applyExec := executor.New()
	if err := applyExec.Apply(ctx, migration, subConn); err != nil {
		t.Fatalf("apply (real connection resolution + CREATE SUBSCRIPTION): %v", err)
	}

	// ── Proof the real connection actually works, not just that CREATE
	// SUBSCRIPTION parsed: a subscription with a broken/unresolved
	// CONNECTION would still be *created* (Postgres doesn't validate
	// connectivity synchronously in all cases) but would never reach
	// subenabled — confirm the row exists and is enabled.
	rows, err := subConn.QueryRows(ctx, "SELECT subenabled FROM pg_subscription WHERE subname = 'my_sub'")
	if err != nil {
		t.Fatalf("query pg_subscription: %v", err)
	}
	if !rows.Next() {
		t.Fatal("pg_subscription has no row for my_sub — CREATE SUBSCRIPTION did not take effect")
	}
	var enabled bool
	if err := rows.Scan(&enabled); err != nil {
		t.Fatalf("scan subenabled: %v", err)
	}
	if !enabled {
		t.Error("expected subenabled = true (WITH (enabled = true))")
	}
	rows.Close() // must close before issuing another query on the same conn

	// ── Confirms the { } block still works for genuinely DPG-only things
	// (COMMENT) after the CONNECTION-block-form removal — Comment isn't
	// part of Body, so this exercises a code path the redaction/CREATE
	// assertions above don't.
	crows, err := subConn.QueryRows(ctx, "SELECT obj_description(oid, 'pg_subscription') FROM pg_subscription WHERE subname = 'my_sub'")
	if err != nil {
		t.Fatalf("query subscription comment: %v", err)
	}
	defer crows.Close()
	if !crows.Next() {
		t.Fatal("pg_subscription has no row for my_sub")
	}
	var comment *string
	if err := crows.Scan(&comment); err != nil {
		t.Fatalf("scan comment: %v", err)
	}
	if comment == nil || *comment != "replication for orders" {
		t.Errorf("expected subscription comment %q, got %v", "replication for orders", comment)
	}
}
