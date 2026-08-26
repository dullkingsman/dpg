//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dpg/internal/compiler"
	"github.com/thec1oud/dpg/internal/diff"
	"github.com/thec1oud/dpg/internal/emit"
	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/introspect"
	"github.com/thec1oud/dpg/internal/pipeline"
	"github.com/thec1oud/dpg/internal/snapshot"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestRoundtripTemporalKeys is the live regression guard for RFC Section
// 7.3's WITHOUT OVERLAPS/PERIOD temporal keys (PostgreSQL 18+, SQL:2011,
// Phase 5 #39 — the last of the 6 items unblocked by the PG18 parser
// upgrade) — a temporal PRIMARY KEY referencing an existing range column,
// and a temporal FOREIGN KEY referencing it via PERIOD on both sides.
//
// Confirms live: a temporal key stays catalogued as an ordinary PRIMARY
// KEY/FOREIGN KEY (contype 'p'/'f'), never reclassified EXCLUDE (matching
// RFC changelog E.21's correction); non-range key columns need
// btree_gist for the underlying exclusion search to have an applicable
// operator class; dropping a temporal FOREIGN KEY's PERIOD column cascades
// the whole constraint away, closing the anyDropped bug found while
// building this test (previously required ALL referenced columns dropped,
// not just one, to skip a redundant conflicting DROP CONSTRAINT) — plus an
// introspection no-drift check at every stage.
func TestRoundtripTemporalKeys(t *testing.T) {
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

	if _, err := conn.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS btree_gist`); err != nil {
		t.Fatalf("create btree_gist: %v", err)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
			t.Fatalf("write schema: %v", err)
		}
	}

	queryConstraint := func(table, name string) (contype string, found bool) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
			SELECT con.contype::text
			FROM pg_constraint con
			JOIN pg_class c ON c.oid = con.conrelid
			WHERE c.relname = $1 AND con.conname = $2`, table, name)
		if err != nil {
			t.Fatalf("query constraint: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			return "", false
		}
		_ = rows.Scan(&contype)
		return contype, true
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

	// Stage 1: temporal PRIMARY KEY referencing an existing range column,
	// plus a temporal FOREIGN KEY referencing it via PERIOD on both sides.
	write(`TABLE room_bookings (
    room_id  BIGINT NOT NULL,
    valid_at DATERANGE NOT NULL,
    CONSTRAINT no_double_booking PRIMARY KEY (room_id, valid_at WITHOUT OVERLAPS)
) {
}
TABLE room_events (
    room_id  BIGINT NOT NULL,
    valid_at DATERANGE NOT NULL,
    note     TEXT,
    CONSTRAINT fk_room FOREIGN KEY (room_id, PERIOD valid_at) REFERENCES room_bookings (room_id, PERIOD valid_at)
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	pkType, pkFound := queryConstraint("room_bookings", "no_double_booking")
	if !pkFound || pkType != "p" {
		t.Fatalf("expected no_double_booking catalogued as an ordinary PRIMARY KEY ('p'), got contype=%q found=%v", pkType, pkFound)
	}
	fkType, fkFound := queryConstraint("room_events", "fk_room")
	if !fkFound || fkType != "f" {
		t.Fatalf("expected fk_room catalogued as an ordinary FOREIGN KEY ('f'), got contype=%q found=%v", fkType, fkFound)
	}

	// Overlap enforcement actually works.
	if _, err := conn.Exec(ctx, `INSERT INTO room_bookings (room_id, valid_at) VALUES (1, daterange('2026-01-01','2026-01-10'))`); err != nil {
		t.Fatalf("insert first booking: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO room_bookings (room_id, valid_at) VALUES (1, daterange('2026-01-05','2026-01-15'))`); err == nil {
		t.Error("expected an overlapping booking to be rejected by the temporal PRIMARY KEY, but it succeeded")
	}
	noDrift([]string{f})

	// Stage 2: drop the FOREIGN KEY's PERIOD column entirely — must cascade
	// the whole constraint away with no separate, conflicting DROP
	// CONSTRAINT (the anyDropped fix this test's own first run surfaced).
	write(`TABLE room_bookings (
    room_id  BIGINT NOT NULL,
    valid_at DATERANGE NOT NULL,
    CONSTRAINT no_double_booking PRIMARY KEY (room_id, valid_at WITHOUT OVERLAPS)
) {
}
TABLE room_events (
    room_id BIGINT NOT NULL,
    note    TEXT
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, found := queryConstraint("room_events", "fk_room"); found {
		t.Error("expected fk_room to be gone after dropping its PERIOD column")
	}
	noDrift([]string{f})
}
