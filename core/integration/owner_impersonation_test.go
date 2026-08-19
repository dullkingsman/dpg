//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestApplyOwnerImpersonationFiresDefaultPrivileges is the live-database
// regression guard for RFC audit item #28: PostgreSQL attributes
// default-privilege eligibility (ALTER DEFAULT PRIVILEGES FOR ROLE) to
// whichever role actually executed CREATE, not to an object's final OWNER.
// Before this fix, DPG always created objects as its connecting role and
// reassigned ownership afterward via a trailing ALTER ... OWNER TO — so a
// DEFAULT PRIVILEGES FOR ROLE block never fired for anything DPG created,
// silently. This proves the opposite against a real database: a table
// declared with OWNER app_admin, created *after* a DEFAULT PRIVILEGES FOR
// ROLE app_admin rule already exists, automatically grants SELECT to
// app_readonly with zero explicit GRANTS block on the table itself — only
// possible if the table's CREATE TABLE genuinely ran as app_admin (RFC
// §11.5's SET ROLE wrapping), not as the connecting superuser.
func TestApplyOwnerImpersonationFiresDefaultPrivileges(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.Start(t)

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()
	store := newMemStore()

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	// Step 1: establish the roles and the default-privilege rule, with no
	// objects yet — DEFAULT PRIVILEGES only ever applies prospectively to
	// objects created after the rule exists.
	v1 := `ROLE app_admin NOLOGIN;
ROLE app_readonly NOLOGIN;

DEFAULT PRIVILEGES FOR ROLE app_admin IN SCHEMA public {
    GRANTS {
        SELECT ON TABLES TO app_readonly;
    }
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	// postgres:17 revokes CREATE ON SCHEMA public FROM PUBLIC by default
	// (unlike pre-15 defaults) — app_admin needs it explicitly to create
	// the table in step 2 once CREATE TABLE actually runs as app_admin.
	if _, err := conn.Exec(ctx, `GRANT CREATE ON SCHEMA public TO app_admin`); err != nil {
		t.Fatalf("grant create on schema public to app_admin: %v", err)
	}

	// Step 2: create a table owned by app_admin, with NO explicit GRANTS
	// block of its own — the only way app_readonly can end up with SELECT
	// is via the default-privilege rule actually firing for app_admin.
	v2 := v1 + `

TABLE widgets (
    id bigint PRIMARY KEY
) {
    OWNER app_admin;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}

	desired2, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile v2: %v", err)
	}
	prevSnap, _ := store.Load("test", "dpgtest")
	ops, err := differ.Diff(desired2, prevSnap)
	if err != nil {
		t.Fatalf("diff v2: %v", err)
	}
	var sawSetRole, sawOwnerTo bool
	for _, op := range ops {
		if strings.Contains(op.SQL(), `SET ROLE "app_admin";`) {
			sawSetRole = true
		}
		if strings.Contains(op.SQL(), "OWNER TO") {
			sawOwnerTo = true
		}
	}
	if !sawSetRole {
		t.Fatalf("expected SET ROLE \"app_admin\"; wrapping the CREATE TABLE, got: %v", opsSQL(ops))
	}
	if sawOwnerTo {
		t.Fatalf("expected no ALTER TABLE ... OWNER TO for a brand-new table, got: %v", opsSQL(ops))
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	// The table must actually be owned by app_admin.
	rows, err := conn.QueryRows(ctx, `SELECT tableowner FROM pg_tables WHERE schemaname = 'public' AND tablename = 'widgets'`)
	if err != nil {
		t.Fatalf("query tableowner: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("widgets table not found")
	}
	var tableOwner string
	if err := rows.Scan(&tableOwner); err != nil {
		rows.Close()
		t.Fatalf("scan tableowner: %v", err)
	}
	rows.Close()
	if tableOwner != "app_admin" {
		t.Fatalf("table owner = %q, want app_admin", tableOwner)
	}

	// The real proof: app_readonly must have SELECT on widgets purely via
	// the default-privilege rule — zero explicit GRANT was ever issued.
	rows, err = conn.QueryRows(ctx, `SELECT has_table_privilege('app_readonly', 'public.widgets', 'SELECT')`)
	if err != nil {
		t.Fatalf("query has_table_privilege: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("has_table_privilege returned no row")
	}
	var hasPriv bool
	if err := rows.Scan(&hasPriv); err != nil {
		rows.Close()
		t.Fatalf("scan has_table_privilege: %v", err)
	}
	rows.Close()
	if !hasPriv {
		t.Fatal("app_readonly does not have SELECT on widgets — DEFAULT PRIVILEGES FOR ROLE app_admin never fired; bug #28 regressed")
	}
}

// TestApplyOwnerMembershipPreflight is the live-database regression guard for
// RFC §11.5's pre-flight membership validation (DPG-E036): before any DDL in
// a migration runs, the connecting role must be a member of every role a
// SET ROLE/OWNER TO statement will target. Uses a real non-superuser
// connecting role throughout (unlike the sibling default-privileges test,
// which — deliberately — uses the superuser connection testpg provides) so
// SET ROLE's own membership requirement is genuinely exercised, not bypassed
// by superuser privilege escalation.
func TestApplyOwnerMembershipPreflight(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.Start(t)

	superConn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect (superuser): %v", err)
	}
	defer superConn.Close(ctx)

	if _, err := superConn.Exec(ctx, `CREATE ROLE limited_conn LOGIN PASSWORD 'limitedpw'`); err != nil {
		t.Fatalf("create limited_conn: %v", err)
	}
	if _, err := superConn.Exec(ctx, `CREATE ROLE target_owner NOLOGIN`); err != nil {
		t.Fatalf("create target_owner: %v", err)
	}
	if _, err := superConn.Exec(ctx, `GRANT CREATE ON SCHEMA public TO target_owner`); err != nil {
		t.Fatalf("grant create on schema public: %v", err)
	}

	limitedConnStr := strings.Replace(connStr, "dpg:dpg@", "limited_conn:limitedpw@", 1)
	limitedConn, err := executor.Connect(ctx, limitedConnStr)
	if err != nil {
		t.Fatalf("connect (limited_conn): %v", err)
	}
	defer limitedConn.Close(ctx)

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	src := `TABLE gadgets (
    id bigint PRIMARY KEY
) {
    OWNER target_owner;
}`
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
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
	applyExec := executor.New()

	// applyWithPreflight mirrors cmd/dpg/apply.go's actual control flow:
	// validate BEFORE calling Apply, never afterward.
	applyWithPreflight := func(conn *executor.PgxConn) error {
		if err := executor.ValidateOwnerMembership(ctx, conn, ops); err != nil {
			return err
		}
		return applyExec.Apply(ctx, migration, conn)
	}

	// limited_conn is not yet a member of target_owner: must be rejected
	// before any DDL runs.
	err = applyWithPreflight(limitedConn)
	if err == nil {
		t.Fatal("expected DPG-E036 for missing membership, got nil")
	}
	if !strings.Contains(err.Error(), "DPG-E036") || !strings.Contains(err.Error(), "target_owner") {
		t.Fatalf("expected DPG-E036 naming target_owner, got: %v", err)
	}

	rows, err := superConn.QueryRows(ctx, `SELECT EXISTS (SELECT 1 FROM pg_tables WHERE schemaname = 'public' AND tablename = 'gadgets')`)
	if err != nil {
		t.Fatalf("query pg_tables: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("existence check returned no row")
	}
	var exists bool
	if err := rows.Scan(&exists); err != nil {
		rows.Close()
		t.Fatalf("scan exists: %v", err)
	}
	rows.Close()
	if exists {
		t.Fatal("gadgets table exists despite the pre-flight membership check failing — DDL ran before validation")
	}

	// Grant the real membership and retry: the exact same migration must
	// now succeed end-to-end, with the table genuinely owned by
	// target_owner (proving SET ROLE worked over a real non-superuser
	// connection, not just a superuser one).
	if _, err := superConn.Exec(ctx, `GRANT target_owner TO limited_conn`); err != nil {
		t.Fatalf("grant target_owner to limited_conn: %v", err)
	}
	if err := applyWithPreflight(limitedConn); err != nil {
		t.Fatalf("apply after granting membership: %v", err)
	}

	rows, err = superConn.QueryRows(ctx, `SELECT tableowner FROM pg_tables WHERE schemaname = 'public' AND tablename = 'gadgets'`)
	if err != nil {
		t.Fatalf("query tableowner: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("gadgets table not found after successful apply")
	}
	var tableOwner string
	if err := rows.Scan(&tableOwner); err != nil {
		rows.Close()
		t.Fatalf("scan tableowner: %v", err)
	}
	rows.Close()
	if tableOwner != "target_owner" {
		t.Fatalf("table owner = %q, want target_owner", tableOwner)
	}
}
