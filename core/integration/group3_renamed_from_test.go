//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dpg/internal/diff"
	"github.com/thec1oud/dpg/internal/emit"
	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/testpg"
)

// catalogOID looks up a single row's oid column, or nil if no row matches
// — used below to prove a rename is metadata-only (same OID before and
// after) rather than a silent DROP+CREATE (which would produce a new OID).
// This is the exact class of bug found and fixed while implementing these
// five kinds: several of them embed the object's own name inside their
// opaque Body text (e.g. "CREATE SUBSCRIPTION name ..."), so hashing that
// body unmodified against a snapshot hash computed under the OLD name
// always misdetected a pure rename as a body/definition change — an
// existence-only check ("old name gone, new name present") is also true
// after a DROP+CREATE, so it would not have caught this bug; OID stability
// is the check that actually distinguishes the two.
func catalogOID(t *testing.T, ctx context.Context, conn *executor.PgxConn, table, nameCol, name string) *int64 {
	t.Helper()
	rows, err := conn.QueryRows(ctx, `SELECT oid::bigint FROM `+table+` WHERE `+nameCol+` = $1`, name)
	if err != nil {
		t.Fatalf("query %s.oid for %s = %s: %v", table, nameCol, name, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil
	}
	var oid *int64
	if err := rows.Scan(&oid); err != nil {
		t.Fatalf("scan %s.oid: %v", table, err)
	}
	return oid
}

// TestRoundtripFDWRenamedFrom is the regression guard for
// ForeignDataWrapper rename detection: before this, ir.ForeignDataWrapper
// had no RenamedFrom field at all, so a renamed FDW was indistinguishable
// from "old one dropped, new one added."
func TestRoundtripFDWRenamedFrom(t *testing.T) {
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

	v1 := `FOREIGN DATA WRAPPER gl_fdw_old;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oldOID := catalogOID(t, ctx, conn, "pg_foreign_data_wrapper", "fdwname", "gl_fdw_old")
	if oldOID == nil {
		t.Fatalf("gl_fdw_old does not exist after initial apply")
	}

	v2 := `FOREIGN DATA WRAPPER gl_fdw {
    RENAMED FROM gl_fdw_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if catalogOID(t, ctx, conn, "pg_foreign_data_wrapper", "fdwname", "gl_fdw_old") != nil {
		t.Fatalf("gl_fdw_old still exists after rename")
	}
	newOID := catalogOID(t, ctx, conn, "pg_foreign_data_wrapper", "fdwname", "gl_fdw")
	if newOID == nil {
		t.Fatalf("gl_fdw does not exist after rename")
	}
	if *newOID != *oldOID {
		t.Fatalf("gl_fdw has a different OID (%d) than gl_fdw_old had (%d) — dropped and recreated instead of renamed", *newOID, *oldOID)
	}
}

// TestRoundtripSubscriptionRenamedFrom is the regression guard for
// Subscription rename detection: before this, ir.Subscription had no
// RenamedFrom field at all — and, once added, a second bug meant the
// rename was still never actually applied for a real (non-identical-text)
// declaration: Subscription's Body embeds its own name
// ("CREATE SUBSCRIPTION name ..."), so a real compiled rename always
// changed the hashed body text, misdetecting as a definition change and
// falling back to DROP SUBSCRIPTION + CREATE SUBSCRIPTION — which can even
// error outright, since DROP SUBSCRIPTION also tries to drop the
// associated replication slot under the OLD name on the publisher.
// connect=false/create_slot=false avoids needing a real replicating
// connection — this proves the diff/apply contract for rename, not
// replication itself (already proven separately elsewhere).
func TestRoundtripSubscriptionRenamedFrom(t *testing.T) {
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

	const connInfo = "host=127.0.0.1 port=5432 dbname=dpgtest user=dpg password=dpg"
	if _, err := conn.Exec(ctx, `CREATE PUBLICATION gl_pub FOR ALL TABLES;`); err != nil {
		t.Fatalf("create publication: %v", err)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	v1 := "SUBSCRIPTION gl_sub_old\n" +
		"    CONNECTION '" + connInfo + "'\n" +
		"    PUBLICATION gl_pub\n" +
		"    WITH (connect = false, create_slot = false);\n"
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oldOID := catalogOID(t, ctx, conn, "pg_subscription", "subname", "gl_sub_old")
	if oldOID == nil {
		t.Fatalf("gl_sub_old does not exist after initial apply")
	}

	v2 := "SUBSCRIPTION gl_sub\n" +
		"    CONNECTION '" + connInfo + "'\n" +
		"    PUBLICATION gl_pub\n" +
		"    WITH (connect = false, create_slot = false) {\n" +
		"    RENAMED FROM gl_sub_old;\n" +
		"}\n"
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if catalogOID(t, ctx, conn, "pg_subscription", "subname", "gl_sub_old") != nil {
		t.Fatalf("gl_sub_old still exists after rename")
	}
	newOID := catalogOID(t, ctx, conn, "pg_subscription", "subname", "gl_sub")
	if newOID == nil {
		t.Fatalf("gl_sub does not exist after rename")
	}
	if *newOID != *oldOID {
		t.Fatalf("gl_sub has a different OID (%d) than gl_sub_old had (%d) — dropped and recreated instead of renamed", *newOID, *oldOID)
	}
}

// TestRoundtripOperatorClassRenamedFrom is the regression guard for
// OperatorClass rename detection, using PostgreSQL's own built-in
// btint4cmp support function (no C extension needed). FAMILY is bound to
// an independent, unrelated family name so the rename doesn't entangle
// with the class's own family-name comparison.
func TestRoundtripOperatorClassRenamedFrom(t *testing.T) {
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

	v1 := `OPERATOR FAMILY gl_shared_fam USING btree;
OPERATOR CLASS gl_opc_old FOR TYPE integer USING btree FAMILY gl_shared_fam AS
    OPERATOR 1 <, OPERATOR 3 =,
    FUNCTION 1 btint4cmp(integer, integer);`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oldOID := catalogOID(t, ctx, conn, "pg_opclass", "opcname", "gl_opc_old")
	if oldOID == nil {
		t.Fatalf("gl_opc_old does not exist after initial apply")
	}

	v2 := `OPERATOR FAMILY gl_shared_fam USING btree;
OPERATOR CLASS gl_opc FOR TYPE integer USING btree FAMILY gl_shared_fam AS
    OPERATOR 1 <, OPERATOR 3 =,
    FUNCTION 1 btint4cmp(integer, integer) {
    RENAMED FROM gl_opc_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if catalogOID(t, ctx, conn, "pg_opclass", "opcname", "gl_opc_old") != nil {
		t.Fatalf("gl_opc_old still exists after rename")
	}
	newOID := catalogOID(t, ctx, conn, "pg_opclass", "opcname", "gl_opc")
	if newOID == nil {
		t.Fatalf("gl_opc does not exist after rename")
	}
	if *newOID != *oldOID {
		t.Fatalf("gl_opc has a different OID (%d) than gl_opc_old had (%d) — dropped and recreated instead of renamed", *newOID, *oldOID)
	}
}

// TestRoundtripOperatorFamilyRenamedFrom is the regression guard for
// OperatorFamily rename detection — including the same Body-embeds-its-
// own-name bug fixed for Subscription/TSDict/TSParser/TSTemplate, since
// OperatorFamily also routes its body-hash comparison through
// diffOpaqueIRHash.
func TestRoundtripOperatorFamilyRenamedFrom(t *testing.T) {
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

	v1 := `OPERATOR FAMILY gl_fam_old USING btree;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oldOID := catalogOID(t, ctx, conn, "pg_opfamily", "opfname", "gl_fam_old")
	if oldOID == nil {
		t.Fatalf("gl_fam_old does not exist after initial apply")
	}

	v2 := `OPERATOR FAMILY gl_fam USING btree {
    RENAMED FROM gl_fam_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if catalogOID(t, ctx, conn, "pg_opfamily", "opfname", "gl_fam_old") != nil {
		t.Fatalf("gl_fam_old still exists after rename")
	}
	newOID := catalogOID(t, ctx, conn, "pg_opfamily", "opfname", "gl_fam")
	if newOID == nil {
		t.Fatalf("gl_fam does not exist after rename")
	}
	if *newOID != *oldOID {
		t.Fatalf("gl_fam has a different OID (%d) than gl_fam_old had (%d) — dropped and recreated instead of renamed", *newOID, *oldOID)
	}
}

// TestRoundtripStatisticsObjectRenamedFrom is the regression guard for
// StatisticsObject rename detection.
func TestRoundtripStatisticsObjectRenamedFrom(t *testing.T) {
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

	v1 := `TABLE gl_stats_t (id INTEGER, val INTEGER);
STATISTICS gl_stats_old (ndistinct) ON id, val FROM gl_stats_t;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oldOID := catalogOID(t, ctx, conn, "pg_statistic_ext", "stxname", "gl_stats_old")
	if oldOID == nil {
		t.Fatalf("gl_stats_old does not exist after initial apply")
	}

	v2 := `TABLE gl_stats_t (id INTEGER, val INTEGER);
STATISTICS gl_stats (ndistinct) ON id, val FROM gl_stats_t {
    RENAMED FROM gl_stats_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if catalogOID(t, ctx, conn, "pg_statistic_ext", "stxname", "gl_stats_old") != nil {
		t.Fatalf("gl_stats_old still exists after rename")
	}
	newOID := catalogOID(t, ctx, conn, "pg_statistic_ext", "stxname", "gl_stats")
	if newOID == nil {
		t.Fatalf("gl_stats does not exist after rename")
	}
	if *newOID != *oldOID {
		t.Fatalf("gl_stats has a different OID (%d) than gl_stats_old had (%d) — dropped and recreated instead of renamed", *newOID, *oldOID)
	}
}
