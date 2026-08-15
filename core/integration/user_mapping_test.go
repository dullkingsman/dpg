//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/introspect"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestUserMappingPasswordSecretRoundtrip proves USER MAPPING's OPTIONS may
// embed a {{secret-uri}} reference (Secret resolution, Phase 5) end-to-end
// against a real server: applying it with a broken/unresolved password
// would still create the mapping (PostgreSQL doesn't validate FDW
// connectivity at CREATE time) but a query through the resulting foreign
// table would fail to authenticate — this proves the real resolved
// password actually works, not just that the statement parsed. Also proves
// the archived/displayed SQL never contains the resolved password.
func TestUserMappingPasswordSecretRoundtrip(t *testing.T) {
	ctx := context.Background()

	vaultAddr, vaultToken := startVaultDev(t)
	t.Setenv("VAULT_ADDR", vaultAddr)
	t.Setenv("VAULT_TOKEN", vaultToken)

	vc, err := vaultapi.NewClient(&vaultapi.Config{Address: vaultAddr})
	if err != nil {
		t.Fatalf("vault client: %v", err)
	}
	vc.SetToken(vaultToken)
	if _, err := vc.KVv2("secret").Put(ctx, "fdw/selfdb", map[string]any{"pw": "dpg"}); err != nil {
		t.Fatalf("seed vault secret: %v", err)
	}

	connStr := testpg.Start(t)
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// A local table + a self-referencing foreign table wrapping it: the
	// only way SELECTing through the foreign table succeeds is if the
	// USER MAPPING's password actually authenticates a real connection.
	if _, err := conn.Exec(ctx, "CREATE TABLE local_source (id int, val text);"); err != nil {
		t.Fatalf("create local_source: %v", err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO local_source VALUES (1, 'hello');"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := conn.Exec(ctx, "CREATE EXTENSION postgres_fdw;"); err != nil {
		t.Fatalf("create extension: %v", err)
	}
	if _, err := conn.Exec(ctx, "CREATE SERVER selfdb FOREIGN DATA WRAPPER postgres_fdw OPTIONS (host 'localhost', port '5432', dbname 'dpgtest');"); err != nil {
		t.Fatalf("create server: %v", err)
	}

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()

	dir := t.TempDir()
	f := filepath.Join(dir, "usermapping.dpg")
	fixture := "USER MAPPING FOR dpg SERVER selfdb OPTIONS (user 'dpg', password '{{vault:secret/fdw/selfdb#pw}}');\n"
	if err := os.WriteFile(f, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	desired, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ops, err := differ.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op (CREATE USER MAPPING), got %d: %v", len(ops), ops)
	}

	// ── Redaction guard.
	sql := ops[0].SQL()
	if !strings.Contains(sql, "{{vault:secret/fdw/selfdb#pw}}") {
		t.Errorf("expected the archived/displayed SQL to keep the {{...}} placeholder, got: %s", sql)
	}
	if strings.Contains(sql, "password 'dpg'") {
		t.Fatalf("archived/displayed SQL leaked the resolved password: %s", sql)
	}

	migration, err := emitter.Emit(ops, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := applyExec.Apply(ctx, migration, conn); err != nil {
		t.Fatalf("apply (real password resolution + CREATE USER MAPPING): %v", err)
	}

	// ── Proof the real resolved password actually works: create a foreign
	// table wrapping local_source through the "selfdb" server and query it
	// — this only succeeds if the USER MAPPING authenticates for real.
	if _, err := conn.Exec(ctx, "CREATE FOREIGN TABLE remote_source (id int, val text) SERVER selfdb OPTIONS (schema_name 'public', table_name 'local_source');"); err != nil {
		t.Fatalf("create foreign table: %v", err)
	}
	rows, err := conn.QueryRows(ctx, "SELECT val FROM remote_source WHERE id = 1;")
	if err != nil {
		t.Fatalf("query foreign table (real connection through USER MAPPING): %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected 1 row from the foreign table")
	}
	var val string
	if err := rows.Scan(&val); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if val != "hello" {
		t.Errorf("got %q, want %q", val, "hello")
	}
}

// TestUserMappingPasswordRedactedOnDump proves `dpg dump`'s introspection
// side (internal/introspect.introspectUserMappings /
// formatUserMappingOptions) never writes a live, resolved password into the
// reconstructed .dpg source it produces — the counterpart to
// TestUserMappingPasswordSecretRoundtrip above, which proves the apply side
// never leaks it. Also proves the redacted mapping still shows zero drift
// when diffed against any declared USER MAPPING for the same server (the
// pre-existing Reconstructed-excludes-Body mechanism, confirmed undisturbed
// by the redaction change).
func TestUserMappingPasswordRedactedOnDump(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.Start(t)
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	const realPassword = "hunter2-supersecret-live-value"

	// Applied directly via raw SQL (not through DPG) to simulate a mapping
	// that already exists live — exactly the scenario `dpg dump` bootstraps
	// from — with a real, literal password an owner/superuser caller can
	// read back out of pg_user_mappings.umoptions.
	if _, err := conn.Exec(ctx, "CREATE EXTENSION postgres_fdw;"); err != nil {
		t.Fatalf("create extension: %v", err)
	}
	if _, err := conn.Exec(ctx, "CREATE SERVER selfdb FOREIGN DATA WRAPPER postgres_fdw OPTIONS (host 'localhost', port '5432', dbname 'dpgtest');"); err != nil {
		t.Fatalf("create server: %v", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf(
		"CREATE USER MAPPING FOR PUBLIC SERVER selfdb OPTIONS (user 'dpg', password '%s');", realPassword,
	)); err != nil {
		t.Fatalf("create user mapping: %v", err)
	}

	ci := introspect.New()
	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var um *ir.UserMapping
	for _, obj := range liveObjects {
		if m, ok := obj.(*ir.UserMapping); ok && m.Server == "selfdb" {
			um = m
		}
	}
	if um == nil {
		t.Fatal("introspection did not return the selfdb USER MAPPING")
	}

	if strings.Contains(um.Body, realPassword) {
		t.Fatalf("introspected USER MAPPING body leaked the real live password: %s", um.Body)
	}
	if !strings.Contains(um.Body, introspect.UserMappingRedactedPlaceholder) {
		t.Fatalf("introspected USER MAPPING body missing the redaction placeholder: %s", um.Body)
	}
	if !um.Reconstructed {
		t.Fatal("expected Reconstructed=true so the redacted body is excluded from drift comparison")
	}

	// Zero-drift proof: any declared USER MAPPING for the same server must
	// diff as unchanged against the redacted live snapshot, since
	// Reconstructed already excludes UserMapping's Body from comparison
	// entirely (pre-existing mechanism — this proves the redaction swap
	// didn't disturb it).
	dir := t.TempDir()
	f := filepath.Join(dir, "usermapping.dpg")
	fixture := "USER MAPPING FOR PUBLIC SERVER selfdb OPTIONS (user 'dpg', password '{{vault:secret/fdw/selfdb#pw}}');\n"
	if err := os.WriteFile(f, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	desired, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	liveSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(liveSnap, []pipeline.IRObject{um}); err != nil {
		t.Fatalf("populate live snapshot: %v", err)
	}
	differ := diff.New()
	driftOps, err := differ.Diff(desired, liveSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(driftOps) != 0 {
		t.Errorf("expected zero drift against the redacted live snapshot, got %d ops:", len(driftOps))
		for _, op := range driftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}
}
