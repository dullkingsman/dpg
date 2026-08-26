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

// TestRoundtripPartitionRenamedFrom is the regression guard for the
// partition rename-detection gap: before this fix, ir.Partition had no
// RenamedFrom field at all, so a renamed partition was indistinguishable
// from "old partition removed, new partition added" — diffPartitionList
// matched purely by current name, meaning a rename silently produced a real
// DROP TABLE (destroying the partition's own data, indexes, and
// constraints) followed by re-attaching a "new" partition under the new
// name via CREATE TABLE ... PARTITION OF. This is the highest-priority
// finding from the 2026-08-23 rename-capability audit precisely because the
// failure mode is silent data loss, not just an extra SQL statement.
//
// This proves, against a real database with actual rows in the partition,
// that renaming is (a) matched (no DROP+CREATE — same OID and same row
// count before and after) and (b) actually renamed live via
// ALTER TABLE ... RENAME TO.
func TestRoundtripPartitionRenamedFrom(t *testing.T) {
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

	v1 := `TABLE events (id BIGINT, created_at DATE NOT NULL) PARTITION BY RANGE (created_at) {
    PARTITIONS {
        events_2024_old FOR VALUES FROM ('2024-01-01') TO ('2025-01-01');
    }
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, err := conn.Exec(ctx, `INSERT INTO events (id, created_at) VALUES (1, '2024-06-15'), (2, '2024-07-01')`); err != nil {
		t.Fatalf("insert rows: %v", err)
	}

	regclassOID := func(qualified string) *int64 {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT to_regclass($1)::oid::bigint`, qualified)
		if err != nil {
			t.Fatalf("query oid for %s: %v", qualified, err)
		}
		defer rows.Close()
		if !rows.Next() {
			return nil
		}
		var oid *int64
		if err := rows.Scan(&oid); err != nil {
			t.Fatalf("scan oid for %s: %v", qualified, err)
		}
		return oid
	}
	rowCount := func(qualified string) int {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM `+qualified)
		if err != nil {
			t.Fatalf("count rows in %s: %v", qualified, err)
		}
		defer rows.Close()
		var n int
		rows.Next()
		_ = rows.Scan(&n)
		return n
	}

	oldOID := regclassOID("public.events_2024_old")
	if oldOID == nil {
		t.Fatalf("public.events_2024_old does not exist after initial apply")
	}
	if got := rowCount("public.events_2024_old"); got != 2 {
		t.Fatalf("expected 2 rows in the partition before rename, got %d", got)
	}

	v2 := `TABLE events (id BIGINT, created_at DATE NOT NULL) PARTITION BY RANGE (created_at) {
    PARTITIONS {
        events_2024 FOR VALUES FROM ('2024-01-01') TO ('2025-01-01') RENAMED FROM events_2024_old;
    }
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if regclassOID("public.events_2024_old") != nil {
		t.Fatalf("public.events_2024_old still exists after rename — expected it to be renamed away")
	}
	renamedOID := regclassOID("public.events_2024")
	if renamedOID == nil {
		t.Fatalf("public.events_2024 does not exist after rename — RENAMED FROM match failed")
	}
	if *renamedOID != *oldOID {
		t.Fatalf("public.events_2024 has a different OID (%d) than events_2024_old had (%d) — the partition was dropped and recreated (data loss), not renamed", *renamedOID, *oldOID)
	}
	if got := rowCount("public.events_2024"); got != 2 {
		t.Fatalf("expected the same 2 rows to survive the rename, got %d — data was lost", got)
	}

	// The partition must still be attached and query correctly through the
	// parent, not just exist as a standalone table with a matching OID.
	if got := rowCount("public.events"); got != 2 {
		t.Fatalf("expected 2 rows visible through the parent table after rename, got %d — partition attachment may have broken", got)
	}
}

// TestRoundtripSubPartitionRenamedFrom confirms rename detection also works
// on a nested sub-partition, exercising the same recursive matching logic
// one level deeper.
func TestRoundtripSubPartitionRenamedFrom(t *testing.T) {
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

	v1 := `TABLE events (id BIGINT, region TEXT, created_at DATE NOT NULL) PARTITION BY RANGE (created_at) {
    PARTITIONS {
        events_2024 FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')
            PARTITION BY LIST (region) {
                PARTITIONS {
                    events_2024_us_old FOR VALUES IN ('us-east');
                }
            };
    }
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, err := conn.Exec(ctx, `INSERT INTO events (id, region, created_at) VALUES (1, 'us-east', '2024-06-15')`); err != nil {
		t.Fatalf("insert rows: %v", err)
	}

	regclassOID := func(qualified string) *int64 {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT to_regclass($1)::oid::bigint`, qualified)
		if err != nil {
			t.Fatalf("query oid for %s: %v", qualified, err)
		}
		defer rows.Close()
		if !rows.Next() {
			return nil
		}
		var oid *int64
		if err := rows.Scan(&oid); err != nil {
			t.Fatalf("scan oid for %s: %v", qualified, err)
		}
		return oid
	}

	oldOID := regclassOID("public.events_2024_us_old")
	if oldOID == nil {
		t.Fatalf("public.events_2024_us_old does not exist after initial apply")
	}

	v2 := `TABLE events (id BIGINT, region TEXT, created_at DATE NOT NULL) PARTITION BY RANGE (created_at) {
    PARTITIONS {
        events_2024 FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')
            PARTITION BY LIST (region) {
                PARTITIONS {
                    events_2024_us FOR VALUES IN ('us-east') RENAMED FROM events_2024_us_old;
                }
            };
    }
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if regclassOID("public.events_2024_us_old") != nil {
		t.Fatalf("public.events_2024_us_old still exists after rename")
	}
	renamedOID := regclassOID("public.events_2024_us")
	if renamedOID == nil {
		t.Fatalf("public.events_2024_us does not exist after rename")
	}
	if *renamedOID != *oldOID {
		t.Fatalf("public.events_2024_us has a different OID (%d) than events_2024_us_old had (%d) — dropped and recreated instead of renamed", *renamedOID, *oldOID)
	}
}
