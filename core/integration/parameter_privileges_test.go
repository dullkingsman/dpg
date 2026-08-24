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
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripParameterPrivileges is the live regression guard for #110
// (RFC Section 11.6, PG15+): PARAMETER PRIVILEGES was a wholly new object
// kind (a synthetic top-level block, same DEFAULT PRIVILEGES-style
// PGSQLParser bypass, but backed by a genuinely-parseable real PostgreSQL
// GRANT ... ON PARAMETER statement underneath). This proves, against
// pg_parameter_acl via has_parameter_privilege(): (a) a declared grant
// actually lands live at CREATE time, (b) removing the GRANTS declaration
// alone is a no-op — the additive model (RFC Section 11.6/11.2): the grant
// must still be present live, not revoked, and (c) an explicit
// REVOCATIONS entry actually revokes it live.
func TestRoundtripParameterPrivileges(t *testing.T) {
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
	f := filepath.Join(dir, "schema.dpg")

	checkPriv := func(role, param, priv string) bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT has_parameter_privilege($1, $2, $3)`, role, param, priv)
		if err != nil {
			t.Fatalf("query has_parameter_privilege: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("has_parameter_privilege returned no row")
		}
		var v bool
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return v
	}

	// v1: declare the grant.
	v1 := `ROLE app_admin NOLOGIN;

PARAMETER PRIVILEGES {
    GRANTS {
        SET ON PARAMETER work_mem, statement_timeout TO app_admin;
    }
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if !checkPriv("app_admin", "work_mem", "SET") {
		t.Fatal("app_admin should have SET on work_mem after initial apply — GRANT never applied")
	}
	if !checkPriv("app_admin", "statement_timeout", "SET") {
		t.Fatal("app_admin should have SET on statement_timeout after initial apply — GRANT never applied")
	}

	// v2: remove the PARAMETER PRIVILEGES declaration entirely. The
	// additive model (RFC Section 11.6) says this must emit nothing — the
	// grant must remain live.
	v2 := `ROLE app_admin NOLOGIN;`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if !checkPriv("app_admin", "work_mem", "SET") {
		t.Fatal("app_admin lost SET on work_mem after the declaration was merely removed from source — additive model violated (a spurious REVOKE was emitted)")
	}
	if !checkPriv("app_admin", "statement_timeout", "SET") {
		t.Fatal("app_admin lost SET on statement_timeout after the declaration was merely removed from source — additive model violated (a spurious REVOKE was emitted)")
	}

	// v3: an explicit REVOCATIONS entry must actually revoke it live.
	v3 := `ROLE app_admin NOLOGIN;

PARAMETER PRIVILEGES {
    REVOCATIONS {
        SET ON PARAMETER work_mem FROM app_admin;
    }
}`
	if err := os.WriteFile(f, []byte(v3), 0o644); err != nil {
		t.Fatalf("write v3: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if checkPriv("app_admin", "work_mem", "SET") {
		t.Fatal("app_admin still has SET on work_mem after an explicit REVOCATIONS entry — REVOKE never applied")
	}
	// statement_timeout was never targeted by the REVOCATIONS entry and was
	// dropped from GRANTS in v2 — it was already proven still-live above;
	// the REVOCATIONS-driven apply must not touch it either.
	if !checkPriv("app_admin", "statement_timeout", "SET") {
		t.Fatal("app_admin lost SET on statement_timeout as a side effect of an unrelated REVOCATIONS entry")
	}
}
