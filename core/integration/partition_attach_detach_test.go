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

// TestRoundtripPartitionAttachDetach is the live regression guard for RFC
// Section 7.13's ATTACHED FROM/DETACHED FROM directives: previously
// completely unimplemented (zero grammar, zero IR fields, zero diff
// handling) — a genuinely new cross-object diffing mechanism, not just a
// field-and-wire-through item: a standalone table declaring DETACHED FROM
// is matched against a NESTED entry in its parent's snapshot (a different
// key space than every top-level rename mechanism), which independently
// risked a destructive DROP TABLE (from the parent's own removal
// detection) racing a CREATE TABLE (from the child's own not-found
// handling) instead of a single metadata-only ALTER TABLE ... DETACH
// PARTITION.
//
// Confirms live: attaching an existing standalone table (with data already
// in it) as a partition via ATTACHED FROM actually runs ALTER TABLE ...
// ATTACH PARTITION (not a destructive drop+recreate that would lose the
// data), the table's rows survive, a second plan against freshly
// introspected live state is a genuine no-op, and the symmetric reverse —
// detaching it back into a standalone table via DETACHED FROM — runs a
// real ALTER TABLE ... DETACH PARTITION, again with the data intact and a
// clean no-op re-diff.
func TestRoundtripPartitionAttachDetach(t *testing.T) {
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

	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
			t.Fatalf("write schema: %v", err)
		}
	}

	isPartition := func() bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT relispartition FROM pg_class WHERE relname = 'legacy_events'`)
		if err != nil {
			t.Fatalf("query relispartition: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("legacy_events not found")
		}
		var v bool
		_ = rows.Scan(&v)
		return v
	}

	rowCount := func() int {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM legacy_events`)
		if err != nil {
			t.Fatalf("query row count: %v", err)
		}
		defer rows.Close()
		rows.Next()
		var n int
		_ = rows.Scan(&n)
		return n
	}

	// Stage 1: a partitioned parent with no children yet, and a genuinely
	// standalone table holding data that predates any partitioning.
	write(`
TABLE events (id BIGINT, created_at DATE NOT NULL) PARTITION BY RANGE (created_at);
TABLE legacy_events (id BIGINT, created_at DATE NOT NULL);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if _, err := conn.Exec(ctx, `INSERT INTO legacy_events VALUES (1, '2023-06-01');`); err != nil {
		t.Fatalf("insert row: %v", err)
	}
	if isPartition() {
		t.Fatalf("legacy_events should not be a partition yet")
	}

	// Stage 2: attach it — the standalone TABLE declaration is replaced by
	// an ATTACHED FROM entry in the parent's PARTITIONS block.
	write(`
TABLE events (id BIGINT, created_at DATE NOT NULL) PARTITION BY RANGE (created_at) {
    PARTITIONS {
        ATTACHED FROM legacy_events FOR VALUES FROM ('2023-01-01') TO ('2024-01-01');
    }
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if !isPartition() {
		t.Fatalf("legacy_events should be a partition after ATTACHED FROM")
	}
	if n := rowCount(); n != 1 {
		t.Fatalf("row count after attach: got %d, want 1 (attach must not lose data)", n)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	// Stage 3: detach it back — the ATTACHED FROM entry is removed from
	// the parent's PARTITIONS block, and a standalone TABLE declaration
	// carrying DETACHED FROM replaces it.
	write(`
TABLE events (id BIGINT, created_at DATE NOT NULL) PARTITION BY RANGE (created_at);
TABLE legacy_events (id BIGINT, created_at DATE NOT NULL) {
    DETACHED FROM events;
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if isPartition() {
		t.Fatalf("legacy_events should not be a partition after DETACHED FROM")
	}
	if n := rowCount(); n != 1 {
		t.Fatalf("row count after detach: got %d, want 1 (detach must not lose data)", n)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
