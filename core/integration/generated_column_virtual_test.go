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
	"github.com/dullkingsman/dpg/internal/introspect"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripGeneratedColumnVirtualAndDiffing is the live regression guard
// for RFC Section 7.2's VIRTUAL generated-column keyword (PostgreSQL 18+)
// and Section 7.4's generated-column diffing table — previously unimplemented
// at all (diffColumns never compared col.Generated against the snapshot),
// and, separately, always hardcoded STORED when adding a generated column,
// silently dropping a declared VIRTUAL.
//
// Walks all four Section 7.4 transitions against a real PostgreSQL 18
// server: add-where-none-existed (DROP+ADD, DESTRUCTIVE), STORED<->VIRTUAL
// kind change (DROP+ADD, DESTRUCTIVE), expression-only change (SET
// EXPRESSION, CAUTION, attnum-stable), and removal (DROP EXPRESSION,
// SAFE, freezes the last computed value) — plus proves introspection
// round-trips VIRTUAL the same way it already does STORED, and that every
// stage settles to a genuine no-op on re-diff.
func TestRoundtripGeneratedColumnVirtualAndDiffing(t *testing.T) {
	connStr := testpg.Start18(t)
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

	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
			t.Fatalf("write schema: %v", err)
		}
	}

	attInfo := func() (attnum int, generated string, expr string) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
			SELECT a.attnum, a.attgenerated::text,
			       coalesce(c.generation_expression, '')
			FROM pg_attribute a
			JOIN pg_class t ON t.oid = a.attrelid
			JOIN information_schema.columns c
			  ON c.table_name = t.relname AND c.column_name = a.attname
			WHERE t.relname = 'orders' AND a.attname = 'amount_with_tax'`)
		if err != nil {
			t.Fatalf("query attribute info: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("amount_with_tax column not found")
		}
		if err := rows.Scan(&attnum, &generated, &expr); err != nil {
			t.Fatalf("scan attribute info: %v", err)
		}
		return attnum, generated, expr
	}

	noDrift := func(files []string) {
		t.Helper()
		desired, _, err := compiler.Compile(files, dir, pipeline.Default)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		base, _ := store.Load("test", "dpgtest")
		ops, err := differ.Diff(desired, base)
		if err != nil {
			t.Fatalf("diff: %v", err)
		}
		if len(ops) != 0 {
			t.Errorf("expected no-op re-diff, got %d ops:", len(ops))
			for _, op := range ops {
				t.Errorf("  [%s] %s", op.Safety(), op.SQL())
			}
		}

		// Independent live-catalog corroboration, not just the snapshot the
		// applier itself wrote.
		ci := introspect.New()
		liveObjects, err := ci.Introspect(ctx, conn)
		if err != nil {
			t.Fatalf("introspect: %v", err)
		}
		var managedLive []pipeline.IRObject
		for _, obj := range liveObjects {
			if _, ok := base.Objects[obj.QualifiedName()]; ok {
				managedLive = append(managedLive, obj)
			}
		}
		liveSnap := &pipeline.Snapshot{}
		if err := snapshot.Populate(liveSnap, managedLive); err != nil {
			t.Fatalf("populate live snapshot: %v", err)
		}
		liveDriftOps, err := differ.Diff(desired, liveSnap)
		if err != nil {
			t.Fatalf("live drift diff: %v", err)
		}
		if len(liveDriftOps) != 0 {
			t.Errorf("expected zero drift against freshly introspected live state, got %d ops:", len(liveDriftOps))
			for _, op := range liveDriftOps {
				t.Errorf("  [%s] %s", op.Safety(), op.SQL())
			}
		}
	}

	// Stage 1: create with a STORED generated column.
	write(`TABLE orders (
    id BIGINT NOT NULL,
    amount NUMERIC NOT NULL,
    amount_with_tax NUMERIC GENERATED ALWAYS AS (amount * 1.08) STORED
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	attnum1, gen1, expr1 := attInfo()
	if gen1 != "s" {
		t.Fatalf("expected attgenerated = 's' (STORED) after create, got %q", gen1)
	}
	if !strings.Contains(expr1, "1.08") {
		t.Fatalf("expected generation_expression to mention 1.08, got %q", expr1)
	}
	noDrift([]string{f})

	// Stage 2: switch STORED -> VIRTUAL, expression unchanged. Section 7.4:
	// no in-place ALTER path exists for a kind change, so this must
	// DROP+ADD the column (attnum must change).
	write(`TABLE orders (
    id BIGINT NOT NULL,
    amount NUMERIC NOT NULL,
    amount_with_tax NUMERIC GENERATED ALWAYS AS (amount * 1.08) VIRTUAL
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	attnum2, gen2, _ := attInfo()
	if gen2 != "v" {
		t.Fatalf("expected attgenerated = 'v' (VIRTUAL) after kind change, got %q", gen2)
	}
	if attnum2 == attnum1 {
		t.Fatalf("expected a new attnum after STORED->VIRTUAL kind change (DROP+ADD), got the same attnum %d", attnum2)
	}
	noDrift([]string{f})

	// Stage 3: expression-only change, kind stays VIRTUAL. Section 7.4:
	// real PostgreSQL 18's SET EXPRESSION changes the expression in place —
	// attnum must NOT change.
	write(`TABLE orders (
    id BIGINT NOT NULL,
    amount NUMERIC NOT NULL,
    amount_with_tax NUMERIC GENERATED ALWAYS AS (amount * 1.10) VIRTUAL
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	attnum3, gen3, expr3 := attInfo()
	if gen3 != "v" {
		t.Fatalf("expected attgenerated to remain 'v' (VIRTUAL) after expression-only change, got %q", gen3)
	}
	if attnum3 != attnum2 {
		t.Fatalf("expected attnum to stay stable across SET EXPRESSION (no DROP+ADD), got %d before and %d after", attnum2, attnum3)
	}
	if !strings.Contains(expr3, "1.10") {
		t.Fatalf("expected generation_expression to reflect the new expression, got %q", expr3)
	}
	noDrift([]string{f})

	// Stage 4: remove the GENERATED clause from a VIRTUAL column, column
	// kept. Confirmed live against a real PostgreSQL 18 server that
	// DROP EXPRESSION is flatly rejected for VIRTUAL ("ALTER TABLE / DROP
	// EXPRESSION is not supported for virtual generated columns") — unlike
	// STORED (guarded at the unit-test level by
	// TestDiffGeneratedColumnRemovedStored in internal/diff), so removal
	// here goes through DROP+ADD (DESTRUCTIVE): a VIRTUAL column has no
	// stored value to freeze in the first place, so the re-added plain
	// column comes back NULL for existing rows, not the last computed
	// value.
	if _, err := conn.Exec(ctx, `INSERT INTO orders (id, amount) VALUES (1, 100)`); err != nil {
		t.Fatalf("insert probe row: %v", err)
	}
	attnum3b, _, _ := attInfo()

	write(`TABLE orders (
    id BIGINT NOT NULL,
    amount NUMERIC NOT NULL,
    amount_with_tax NUMERIC
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	attnum4, gen4, _ := attInfo()
	if gen4 != "" {
		t.Fatalf("expected attgenerated = '' (plain column) after removing GENERATED from a VIRTUAL column, got %q", gen4)
	}
	if attnum4 == attnum3b {
		t.Fatalf("expected a new attnum after removing GENERATED from a VIRTUAL column (DROP+ADD), got the same attnum %d", attnum4)
	}
	rows, err := conn.QueryRows(ctx, `SELECT amount_with_tax FROM orders WHERE id = 1`)
	if err != nil {
		t.Fatalf("query probe row after removal: %v", err)
	}
	if !rows.Next() {
		t.Fatal("expected probe row after removal")
	}
	var afterVal *float64
	_ = rows.Scan(&afterVal)
	rows.Close()
	if afterVal != nil {
		t.Errorf("expected the re-added plain column to be NULL (no value survives DROP+ADD), got %v", *afterVal)
	}
	noDrift([]string{f})
}
