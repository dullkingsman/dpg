//go:build integration

package integration

import (
	"context"
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
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoleIntrospectionZeroDrift proves introspectRoles itself (not just
// hand-written verification queries against pg_roles/pg_auth_members, which
// TestRoleAttributesAndPasswordSecretRoundtrip already uses) reads every
// attribute correctly: apply a role with a representative set of
// attributes + membership, introspect it back through the real
// CatalogIntrospector, and diff the introspected state against the original
// desired state — zero drift expected (PASSWORD aside, which neither side
// sets here, so it's not exercised by this test at all).
func TestRoleIntrospectionZeroDrift(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.Start(t)
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	dir := t.TempDir()
	f := filepath.Join(dir, "roles.dpg")
	fixture := "ROLE reader_group NOLOGIN;\n\n" +
		"ROLE svc_role\n" +
		"    LOGIN\n" +
		"    NOSUPERUSER\n" +
		"    NOCREATEDB\n" +
		"    NOCREATEROLE\n" +
		"    INHERIT\n" +
		"    NOREPLICATION\n" +
		"    NOBYPASSRLS\n" +
		"    CONNECTION LIMIT 7\n" +
		"    VALID UNTIL '2099-01-01 00:00:00+00'\n" +
		"    IN ROLE reader_group\n" +
		"{\n" +
		"    COMMENT 'introspection fidelity check';\n" +
		"}\n"
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
	emitter := emit.New()
	migration, err := emitter.Emit(ops, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := executor.New().Apply(ctx, migration, conn); err != nil {
		t.Fatalf("apply: %v", err)
	}

	ci := introspect.New()
	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	desiredSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(desiredSnap, desired); err != nil {
		t.Fatalf("populate desired snapshot: %v", err)
	}
	var managedLive []pipeline.IRObject
	for _, obj := range liveObjects {
		if _, ok := desiredSnap.Objects[obj.QualifiedName()]; ok {
			managedLive = append(managedLive, obj)
		}
	}
	liveSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(liveSnap, managedLive); err != nil {
		t.Fatalf("populate live snapshot: %v", err)
	}

	driftOps, err := differ.Diff(desired, liveSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(driftOps) != 0 {
		t.Errorf("drift after introspection (%d ops):", len(driftOps))
		for _, op := range driftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}
}

// TestRoleAttributesAndPasswordSecretRoundtrip proves Role's new structured
// attribute support (RFC §11.1) end-to-end against a real server: a ROLE
// declaration with LOGIN/CONNECTION LIMIT/membership and a PASSWORD held as
// a {{vault:...}} reference (a) is accepted by, and actually works against,
// a real PostgreSQL instance — a role created with a broken/unresolved
// password would fail to actually log in, not just fail to parse — (b) the
// archived migration text never contains the resolved plaintext password,
// and (c) a second apply cycle correctly diffs an attribute + password
// change into ALTER ROLE, not a spurious CREATE.
func TestRoleAttributesAndPasswordSecretRoundtrip(t *testing.T) {
	ctx := context.Background()

	vaultAddr, vaultToken := startVaultDev(t)
	t.Setenv("VAULT_ADDR", vaultAddr)
	t.Setenv("VAULT_TOKEN", vaultToken)

	vc, err := vaultapi.NewClient(&vaultapi.Config{Address: vaultAddr})
	if err != nil {
		t.Fatalf("vault client: %v", err)
	}
	vc.SetToken(vaultToken)
	if _, err := vc.KVv2("secret").Put(ctx, "roles/app_service", map[string]any{"pw": "s3cr3t-v1"}); err != nil {
		t.Fatalf("seed vault secret: %v", err)
	}

	connStr := testpg.Start(t)
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	store := newMemStore()
	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()

	dir := t.TempDir()
	f := filepath.Join(dir, "roles.dpg")
	fixture1 := "ROLE reader_group NOLOGIN;\n\n" +
		"ROLE app_service\n" +
		"    LOGIN\n" +
		"    PASSWORD '{{vault:secret/roles/app_service#pw}}'\n" +
		"    CONNECTION LIMIT 5\n" +
		"    IN ROLE reader_group\n" +
		"{\n" +
		"    COMMENT 'application service account';\n" +
		"}\n"
	if err := os.WriteFile(f, []byte(fixture1), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	desired1, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ops1, err := differ.Diff(desired1, &pipeline.Snapshot{})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}

	// ── Redaction guard: none of the archived/displayed SQL for this
	// migration may contain the resolved secret.
	for _, op := range ops1 {
		if strings.Contains(op.SQL(), "s3cr3t-v1") {
			t.Fatalf("archived/displayed SQL leaked the resolved password: %s", op.SQL())
		}
	}
	foundPlaceholder := false
	for _, op := range ops1 {
		if strings.Contains(op.SQL(), "{{vault:secret/roles/app_service#pw}}") {
			foundPlaceholder = true
		}
	}
	if !foundPlaceholder {
		t.Fatal("expected the CREATE ROLE op's SQL() to keep the {{...}} placeholder")
	}

	migration1, err := emitter.Emit(ops1, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := applyExec.Apply(ctx, migration1, conn); err != nil {
		t.Fatalf("apply (real password resolution + CREATE ROLE): %v", err)
	}
	snap1 := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap1, desired1); err != nil {
		t.Fatalf("populate snapshot: %v", err)
	}
	if err := store.Save("test", "dpgtest", snap1); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	// ── Proof the real resolved password actually works, not just that
	// CREATE ROLE parsed: connect AS app_service using the real secret
	// value (never DPG's placeholder), against the same database.
	appConnStr := strings.Replace(connStr, "dpg:dpg@", "app_service:s3cr3t-v1@", 1)
	appConn, err := executor.Connect(ctx, appConnStr)
	if err != nil {
		t.Fatalf("connect as app_service with the real resolved password: %v", err)
	}
	appConn.Close(ctx)

	// ── Attributes + membership actually took effect.
	rows, err := conn.QueryRows(ctx,
		"SELECT rolconnlimit, shobj_description(oid, 'pg_authid') FROM pg_roles WHERE rolname = 'app_service'")
	if err != nil {
		t.Fatalf("query pg_roles: %v", err)
	}
	if !rows.Next() {
		t.Fatal("pg_roles has no row for app_service")
	}
	var connLimit int
	var comment *string
	if err := rows.Scan(&connLimit, &comment); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows.Close()
	if connLimit != 5 {
		t.Errorf("rolconnlimit: got %d, want 5", connLimit)
	}
	if comment == nil || *comment != "application service account" {
		t.Errorf("comment: got %v", comment)
	}

	memRows, err := conn.QueryRows(ctx, `
SELECT pr.rolname FROM pg_auth_members am
JOIN pg_roles pr ON pr.oid = am.roleid
JOIN pg_roles member ON member.oid = am.member
WHERE member.rolname = 'app_service'`)
	if err != nil {
		t.Fatalf("query membership: %v", err)
	}
	defer memRows.Close()
	foundMembership := false
	for memRows.Next() {
		var name string
		if err := memRows.Scan(&name); err != nil {
			t.Fatalf("scan membership: %v", err)
		}
		if name == "reader_group" {
			foundMembership = true
		}
	}
	if !foundMembership {
		t.Error("expected app_service to be IN ROLE reader_group")
	}

	// ── Second apply cycle: rotate the password reference + change
	// CONNECTION LIMIT, confirming diffRole emits ALTER ROLE (not a
	// spurious CREATE/DROP) and that the rotated password actually takes
	// effect live too. The declared {{...}} text itself must change, not
	// just the value it happens to resolve to right now — PASSWORD drift
	// detection hashes the declared reference (RFC §11.1), by design, the
	// same as Subscription CONNECTION; if only the underlying Vault value
	// changed under an unchanged reference string, DPG has no way to know
	// (and isn't supposed to — that's a live-secret-backend concern, not a
	// DPG diff concern). So this seeds a *new* field, pw_v2, and points the
	// updated fixture at it, exercising real hash-based drift detection.
	if _, err := vc.KVv2("secret").Put(ctx, "roles/app_service", map[string]any{"pw": "s3cr3t-v1", "pw_v2": "s3cr3t-v2"}); err != nil {
		t.Fatalf("seed rotated vault secret field: %v", err)
	}
	fixture2 := "ROLE reader_group NOLOGIN;\n\n" +
		"ROLE app_service\n" +
		"    LOGIN\n" +
		"    PASSWORD '{{vault:secret/roles/app_service#pw_v2}}'\n" +
		"    CONNECTION LIMIT 10\n" +
		"    IN ROLE reader_group\n" +
		"{\n" +
		"    COMMENT 'application service account';\n" +
		"}\n"
	if err := os.WriteFile(f, []byte(fixture2), 0o644); err != nil {
		t.Fatalf("write fixture2: %v", err)
	}
	desired2, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile2: %v", err)
	}
	base, err := store.Load("test", "dpgtest")
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	ops2, err := differ.Diff(desired2, base)
	if err != nil {
		t.Fatalf("diff2: %v", err)
	}
	for _, op := range ops2 {
		if strings.Contains(op.SQL(), "CREATE ROLE") || strings.Contains(op.SQL(), "DROP ROLE") {
			t.Errorf("expected only ALTER ROLE ops for an attribute+password change, got: %s", op.SQL())
		}
		if strings.Contains(op.SQL(), "s3cr3t-v2") {
			t.Fatalf("archived/displayed SQL leaked the rotated resolved password: %s", op.SQL())
		}
	}
	migration2, err := emitter.Emit(ops2, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit2: %v", err)
	}
	if err := applyExec.Apply(ctx, migration2, conn); err != nil {
		t.Fatalf("apply2 (password rotation): %v", err)
	}

	rows2, err := conn.QueryRows(ctx, "SELECT rolconnlimit FROM pg_roles WHERE rolname = 'app_service'")
	if err != nil {
		t.Fatalf("query pg_roles after alter: %v", err)
	}
	if !rows2.Next() {
		t.Fatal("pg_roles has no row for app_service after alter")
	}
	var connLimit2 int
	if err := rows2.Scan(&connLimit2); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows2.Close()
	if connLimit2 != 10 {
		t.Errorf("rolconnlimit after ALTER: got %d, want 10", connLimit2)
	}

	rotatedConnStr := strings.Replace(connStr, "dpg:dpg@", "app_service:s3cr3t-v2@", 1)
	rotatedConn, err := executor.Connect(ctx, rotatedConnStr)
	if err != nil {
		t.Fatalf("connect as app_service with the ROTATED resolved password: %v", err)
	}
	rotatedConn.Close(ctx)

	// The OLD password must no longer work — proves this was a real
	// ALTER ROLE PASSWORD, not a no-op.
	if _, err := executor.Connect(ctx, appConnStr); err == nil {
		t.Error("expected the old password to be rejected after rotation")
	}
}
