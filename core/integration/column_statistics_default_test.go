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

// TestRoundtripColumnStatisticsDefault is the live regression guard for RFC
// audit item #112: parseStatisticsValue only accepted an integer, so
// "STATISTICS DEFAULT;" on a column block — real PostgreSQL's own
// ALTER ... SET STATISTICS DEFAULT spelling — was a hard parse error, the
// only way to reset a customized per-column statistics target back to
// default was deleting the directive line entirely. Proves live that
// writing DEFAULT explicitly resets attstattarget back to default (NULL in
// pg_attribute — PostgreSQL stores "use default_statistics_target" as SQL
// NULL, not the literal integer -1, confirmed live; COALESCE normalizes it
// for a simple Go int scan).
//
// Also exercises a second, adjacent bug found while writing this test:
// createTable never emitted SET STATISTICS for a brand-new table's column
// at all (no inline CREATE TABLE form exists for it, unlike STORAGE/
// COMPRESSION), so a customized target declared on a table's first-ever
// apply was silently never applied live — while the snapshot (populated
// from the desired IR, not a live re-introspection) recorded it anyway,
// permanently masking the drift on every subsequent plan.
func TestRoundtripColumnStatisticsDefault(t *testing.T) {
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

	v1 := `TABLE t (id INTEGER, val TEXT) {
    COLUMN val {
        STATISTICS 300;
    }
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	statTarget := func() int {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT coalesce(attstattarget, -1) FROM pg_attribute WHERE attrelid = 't'::regclass AND attname = 'val'`)
		if err != nil {
			t.Fatalf("query attstattarget: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("t.val does not exist")
		}
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan attstattarget: %v", err)
		}
		return n
	}

	if got := statTarget(); got != 300 {
		t.Fatalf("expected attstattarget = 300 after initial apply (was this ever actually applied live for a brand-new table's column?), got %d", got)
	}

	v2 := `TABLE t (id INTEGER, val TEXT) {
    COLUMN val {
        STATISTICS DEFAULT;
    }
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := statTarget(); got != -1 {
		t.Fatalf("expected attstattarget reset to default (-1) after STATISTICS DEFAULT, got %d", got)
	}
}
