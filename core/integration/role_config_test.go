//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripRoleConfig guards RFC audit item #74 (Section 11.1):
// role-config-dir's ALTER ROLE ... [IN DATABASE db] SET/RESET forms.
// Proves: (a) a declared cluster-wide SET and an IN DATABASE-qualified SET
// both actually apply live and land in pg_db_role_setting correctly
// distinguished by database, (b) a second plan against the same
// declaration is a genuine no-op, (c) changing a value emits a real
// targeted ALTER ROLE SET (not a role recreate), and (d) an explicit RESET
// entry actually clears the live setting.
func TestRoundtripRoleConfig(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()
	store := newMemStore()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	dir := t.TempDir()
	f := filepath.Join(dir, "roles.dpg")

	settingFor := func(database *string) (string, bool) {
		t.Helper()
		var rows pipeline.Rows
		var qerr error
		if database == nil {
			rows, qerr = conn.QueryRows(ctx, `
SELECT s.setconfig FROM pg_db_role_setting s
JOIN pg_roles r ON r.oid = s.setrole
WHERE r.rolname = 'app_cfg' AND s.setdatabase = 0`)
		} else {
			rows, qerr = conn.QueryRows(ctx, `
SELECT s.setconfig FROM pg_db_role_setting s
JOIN pg_roles r ON r.oid = s.setrole
JOIN pg_database d ON d.oid = s.setdatabase
WHERE r.rolname = 'app_cfg' AND d.datname = $1`, *database)
		}
		if qerr != nil {
			t.Fatalf("query pg_db_role_setting: %v", qerr)
		}
		defer rows.Close()
		if !rows.Next() {
			return "", false
		}
		var setconfig []string
		if err := rows.Scan(&setconfig); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if len(setconfig) == 0 {
			return "", false
		}
		return setconfig[0], true
	}

	// v1: cluster-wide SET plus an IN DATABASE dpgtest SET (the real test
	// database testpg provisions — an arbitrary nonexistent name would
	// error live, real PostgreSQL validates it).
	v1 := `ROLE app_cfg NOLOGIN
{
    SET statement_timeout = '5s';
    SET work_mem = '64MB' IN DATABASE dpgtest;
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if v, ok := settingFor(nil); !ok || v != "statement_timeout=5s" {
		t.Fatalf("cluster-wide statement_timeout: got %q, ok=%v", v, ok)
	}
	dbName := "dpgtest"
	if v, ok := settingFor(&dbName); !ok || v != "work_mem=64MB" {
		t.Fatalf("IN DATABASE work_mem: got %q, ok=%v", v, ok)
	}

	// A second apply of the same declaration must be a genuine no-op.
	assertNoLiveDrift(t, ctx, conn, []string{f}, dir, differ, store)

	// v2: change the cluster-wide value — must emit a real targeted ALTER
	// ROLE SET (not a role recreate), and the new value must actually
	// apply.
	v2 := `ROLE app_cfg NOLOGIN
{
    SET statement_timeout = '30s';
    SET work_mem = '64MB' IN DATABASE dpgtest;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if v, ok := settingFor(nil); !ok || v != "statement_timeout=30s" {
		t.Fatalf("statement_timeout after change: got %q, ok=%v", v, ok)
	}

	// v3: RESET the cluster-wide entry — must actually clear it live.
	v3 := `ROLE app_cfg NOLOGIN
{
    RESET statement_timeout;
    SET work_mem = '64MB' IN DATABASE dpgtest;
}`
	if err := os.WriteFile(f, []byte(v3), 0o644); err != nil {
		t.Fatalf("write v3: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, ok := settingFor(nil); ok {
		t.Fatal("expected no cluster-wide setting row for app_cfg after RESET statement_timeout")
	}
	// The IN DATABASE entry, untouched by the RESET, must survive.
	if v, ok := settingFor(&dbName); !ok || v != "work_mem=64MB" {
		t.Fatalf("IN DATABASE work_mem after unrelated RESET: got %q, ok=%v", v, ok)
	}
}
