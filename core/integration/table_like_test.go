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

// TestRoundtripTableLikeSourceTable is the live regression guard for the
// audit's headline finding: CREATE TABLE (LIKE source_table) previously
// built with zero columns and no error, since buildTable's TableElts switch
// had no case for a LIKE clause at all. This proves, against a real
// database, that the copied table actually has the source's columns, that
// a bare LIKE excludes DEFAULT (PostgreSQL's own documented default), and
// that INCLUDING DEFAULTS/CONSTRAINTS/INDEXES actually take effect live —
// not just in the resolved IR before it's ever executed.
func TestRoundtripTableLikeSourceTable(t *testing.T) {
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

	src := `TABLE order_items (
    id BIGINT NOT NULL,
    qty INTEGER NOT NULL DEFAULT 1,
    email TEXT UNIQUE,
    CHECK (qty > 0)
);
TABLE order_items_bare (LIKE order_items);
TABLE order_items_archive (
    LIKE order_items INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES,
    archived_at TIMESTAMPTZ NOT NULL
);`
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	columnCount := func(table string) int {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1`, table)
		if err != nil {
			t.Fatalf("count columns for %s: %v", table, err)
		}
		defer rows.Close()
		var n int
		rows.Next()
		_ = rows.Scan(&n)
		return n
	}
	columnHasDefault := func(table, column string) bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT column_default IS NOT NULL FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`, table, column)
		if err != nil {
			t.Fatalf("check default for %s.%s: %v", table, column, err)
		}
		defer rows.Close()
		var has bool
		if !rows.Next() {
			t.Fatalf("%s.%s does not exist", table, column)
		}
		_ = rows.Scan(&has)
		return has
	}
	checkConstraintCount := func(table string) int {
		t.Helper()
		// pg_constraint directly, not information_schema.table_constraints:
		// on PostgreSQL 17 the latter synthesizes a pseudo "CHECK" row per
		// NOT NULL column (confirmed live: "..._not_null" named rows with
		// no corresponding pg_constraint entry, since NOT NULL isn't a real
		// constraint row until PG 18's contype='n'), which would otherwise
		// swamp the count of genuine, user-declared CHECK constraints.
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_constraint c JOIN pg_class t ON t.oid = c.conrelid WHERE t.relname = $1 AND c.contype = 'c'`, table)
		if err != nil {
			t.Fatalf("count check constraints for %s: %v", table, err)
		}
		defer rows.Close()
		var n int
		rows.Next()
		_ = rows.Scan(&n)
		return n
	}
	uniqueConstraintCount := func(table string) int {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM information_schema.table_constraints WHERE table_schema = 'public' AND table_name = $1 AND constraint_type = 'UNIQUE'`, table)
		if err != nil {
			t.Fatalf("count unique constraints for %s: %v", table, err)
		}
		defer rows.Close()
		var n int
		rows.Next()
		_ = rows.Scan(&n)
		return n
	}

	if got := columnCount("order_items_bare"); got != 3 {
		t.Fatalf("order_items_bare: expected 3 columns (id, qty, email) copied via bare LIKE, got %d — LIKE may have been silently discarded", got)
	}
	if columnHasDefault("order_items_bare", "qty") {
		t.Errorf("order_items_bare.qty: bare LIKE should not copy DEFAULT")
	}
	if checkConstraintCount("order_items_bare") != 0 {
		t.Errorf("order_items_bare: bare LIKE should not copy CHECK constraints")
	}

	if got := columnCount("order_items_archive"); got != 4 {
		t.Fatalf("order_items_archive: expected 4 columns (3 copied + archived_at), got %d", got)
	}
	if !columnHasDefault("order_items_archive", "qty") {
		t.Errorf("order_items_archive.qty: expected DEFAULT copied (INCLUDING DEFAULTS)")
	}
	if checkConstraintCount("order_items_archive") != 1 {
		t.Errorf("order_items_archive: expected the CHECK constraint copied (INCLUDING CONSTRAINTS)")
	}
	if uniqueConstraintCount("order_items_archive") != 1 {
		t.Errorf("order_items_archive: expected the UNIQUE constraint copied (INCLUDING INDEXES)")
	}

	// The CHECK constraint must actually be enforced live, not just present
	// in the catalog as inert metadata.
	if _, err := conn.Exec(ctx, `INSERT INTO order_items_archive (id, qty, archived_at) VALUES (1, 0, now());`); err == nil {
		t.Fatalf("expected the copied CHECK (qty > 0) constraint to reject qty = 0")
	}
	if _, err := conn.Exec(ctx, `INSERT INTO order_items_archive (id, qty, archived_at) VALUES (1, 1, now());`); err != nil {
		t.Fatalf("valid insert into order_items_archive failed: %v", err)
	}
}
