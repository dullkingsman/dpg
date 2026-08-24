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

// TestRoundtripFunctionProcedureAggregateOwner is the live regression guard
// for RFC audit item #70: Function/Procedure/Aggregate had no Owner field at
// all across all three structs — real PostgreSQL supports
// ALTER FUNCTION/PROCEDURE/AGGREGATE ... OWNER TO, but none of them ever
// reached the differ. Each object is first created without OWNER (as the
// connecting superuser), so this only exercises the ALTER OWNER TO path —
// same reasoning as TestRoundtripCollationOwner: declaring OWNER at CREATE
// time would additionally require the new owner role to have CREATE on the
// target schema (revoked from PUBLIC by default since PG15), an unrelated
// prerequisite this test isn't about.
func TestRoundtripFunctionProcedureAggregateOwner(t *testing.T) {
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

	v1 := `ROLE code_admin NOLOGIN;

FUNCTION calc_total(n INTEGER) RETURNS INTEGER LANGUAGE plpgsql AS $$
BEGIN
    RETURN n * 2;
END;
$$ {}

PROCEDURE recalc_totals(n INTEGER) LANGUAGE plpgsql AS $$
BEGIN
    NULL;
END;
$$ {}

AGGREGATE amount_sum (numeric) (SFUNC = numeric_add, STYPE = numeric) {}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	ownerOf := func(query string, args ...any) string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, query, args...)
		if err != nil {
			t.Fatalf("query owner: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("no matching row")
		}
		var owner string
		if err := rows.Scan(&owner); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return owner
	}

	funcOwnerQ := `SELECT r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid = p.proowner WHERE p.proname = $1 AND p.prokind = $2`

	if got := ownerOf(funcOwnerQ, "calc_total", "f"); got == "code_admin" {
		t.Fatal("calc_total already owned by code_admin before OWNER was ever declared — test setup is broken")
	}
	if got := ownerOf(funcOwnerQ, "recalc_totals", "p"); got == "code_admin" {
		t.Fatal("recalc_totals already owned by code_admin before OWNER was ever declared — test setup is broken")
	}
	if got := ownerOf(funcOwnerQ, "amount_sum", "a"); got == "code_admin" {
		t.Fatal("amount_sum already owned by code_admin before OWNER was ever declared — test setup is broken")
	}

	v2 := `ROLE code_admin NOLOGIN;

FUNCTION calc_total(n INTEGER) RETURNS INTEGER LANGUAGE plpgsql AS $$
BEGIN
    RETURN n * 2;
END;
$$ {
    OWNER code_admin;
}

PROCEDURE recalc_totals(n INTEGER) LANGUAGE plpgsql AS $$
BEGIN
    NULL;
END;
$$ {
    OWNER code_admin;
}

AGGREGATE amount_sum (numeric) (SFUNC = numeric_add, STYPE = numeric) {
    OWNER code_admin;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := ownerOf(funcOwnerQ, "calc_total", "f"); got != "code_admin" {
		t.Fatalf("calc_total: owner = %q after OWNER change, want code_admin — bug #70 regressed", got)
	}
	if got := ownerOf(funcOwnerQ, "recalc_totals", "p"); got != "code_admin" {
		t.Fatalf("recalc_totals: owner = %q after OWNER change, want code_admin — bug #70 regressed", got)
	}
	if got := ownerOf(funcOwnerQ, "amount_sum", "a"); got != "code_admin" {
		t.Fatalf("amount_sum: owner = %q after OWNER change, want code_admin — bug #70 regressed", got)
	}
}
