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

// TestRoundtripPartitionForeign is the live regression guard for RFC
// Section 7.13's "FOREIGN partition-name ... SERVER server_name [OPTIONS
// (...)]" form (#14, the last item from the original RFC-completeness
// priority list): a partition made a foreign table via direct creation
// (CREATE FOREIGN TABLE ... PARTITION OF), distinct from attaching an
// already-existing standalone foreign table via ATTACHED FROM.
//
// Two real, independently-confirmed-live bugs made this genuinely new work,
// not just a field-and-wire item:
//
//  1. The partition-children introspection query (introspectPartitions'
//     childQ) filtered relkind IN ('r', 'p'), excluding 'f' entirely — a
//     foreign-table partition was completely invisible to dpg dump/
//     plan --live/verify, not merely missing its SERVER/OPTIONS.
//  2. Real PostgreSQL rejects DROP TABLE on a foreign table ("... is not a
//     table", confirmed live) — removing or bound-changing a foreign
//     partition must emit DROP FOREIGN TABLE instead of the plain DROP
//     TABLE a regular partition removal gets.
//
// Uses a genuinely connectable postgres_fdw loopback (not the catalog-only,
// non-connectable FDW stack other opaque-object tests use) so this proves
// real data actually routes through the created foreign partition, not just
// that the catalog rows look right.
func TestRoundtripPartitionForeign(t *testing.T) {
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

	relkindOf := func(name string) string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT relkind::text FROM pg_class WHERE relname = $1`, name)
		if err != nil {
			t.Fatalf("query relkind: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			return ""
		}
		var k string
		_ = rows.Scan(&k)
		return k
	}

	backingRowCount := func() int {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM events_archive_backing`)
		if err != nil {
			t.Fatalf("query backing row count: %v", err)
		}
		defer rows.Close()
		rows.Next()
		var n int
		_ = rows.Scan(&n)
		return n
	}

	// Stage 0: a genuinely connectable postgres_fdw loopback (server points
	// back at this same database/container) plus the real local table it
	// proxies to, so INSERTs against the foreign partition can be proven to
	// actually land somewhere, not just parse and apply without error.
	// EXTENSION's own COMMENT block restates postgres_fdw's control-file
	// default comment (confirmed live via obj_description) — without it,
	// introspection's real (non-nil) comment would permanently mismatch
	// this fixture's undeclared (nil) one on every live re-diff, a real
	// but unrelated pre-existing diffExtension gap this test isn't about.
	write(`
EXTENSION postgres_fdw {
    COMMENT 'foreign-data wrapper for remote PostgreSQL servers';
};
SERVER loopback_srv FOREIGN DATA WRAPPER postgres_fdw OPTIONS (host 'localhost', port '5432', dbname 'dpgtest');
USER MAPPING FOR dpg SERVER loopback_srv OPTIONS (user 'dpg', password 'dpg');
TABLE events_archive_backing (id BIGINT, region TEXT);
TABLE events (id BIGINT, region TEXT) PARTITION BY LIST (region) {
    PARTITIONS {
        events_us FOR VALUES IN ('us-east');
    }
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	// Stage 1: add the FOREIGN default partition, backed by the loopback
	// server, proxying to the real local table above.
	write(`
EXTENSION postgres_fdw {
    COMMENT 'foreign-data wrapper for remote PostgreSQL servers';
};
SERVER loopback_srv FOREIGN DATA WRAPPER postgres_fdw OPTIONS (host 'localhost', port '5432', dbname 'dpgtest');
USER MAPPING FOR dpg SERVER loopback_srv OPTIONS (user 'dpg', password 'dpg');
TABLE events_archive_backing (id BIGINT, region TEXT);
TABLE events (id BIGINT, region TEXT) PARTITION BY LIST (region) {
    PARTITIONS {
        events_us FOR VALUES IN ('us-east');
        FOREIGN events_archive DEFAULT SERVER loopback_srv OPTIONS (table_name 'events_archive_backing');
    }
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := relkindOf("events_archive"); got != "f" {
		t.Fatalf("events_archive relkind: got %q, want \"f\" (foreign table)", got)
	}

	// Real behavioral proof, not just "no error": a row that matches no
	// explicit partition routes to DEFAULT (events_archive), which — being
	// a foreign table over the loopback server — must actually forward the
	// INSERT to events_archive_backing.
	if _, err := conn.Exec(ctx, `INSERT INTO events VALUES (1, 'eu-west');`); err != nil {
		t.Fatalf("insert into events (should route through foreign DEFAULT partition): %v", err)
	}
	if n := backingRowCount(); n != 1 {
		t.Fatalf("events_archive_backing row count after insert: got %d, want 1 (foreign partition did not actually route data)", n)
	}

	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	// Stage 2: remove the foreign partition — this must emit DROP FOREIGN
	// TABLE, not DROP TABLE (a real PostgreSQL error: "... is not a
	// table"). A failing Apply here is exactly the predicted symptom of
	// reverting the dropPartitionVerb fix.
	write(`
EXTENSION postgres_fdw {
    COMMENT 'foreign-data wrapper for remote PostgreSQL servers';
};
SERVER loopback_srv FOREIGN DATA WRAPPER postgres_fdw OPTIONS (host 'localhost', port '5432', dbname 'dpgtest');
USER MAPPING FOR dpg SERVER loopback_srv OPTIONS (user 'dpg', password 'dpg');
TABLE events_archive_backing (id BIGINT, region TEXT);
TABLE events (id BIGINT, region TEXT) PARTITION BY LIST (region) {
    PARTITIONS {
        events_us FOR VALUES IN ('us-east');
    }
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := relkindOf("events_archive"); got != "" {
		t.Fatalf("events_archive: expected to be dropped, still has relkind %q", got)
	}
	// The backing table (independent of the partition) must survive —
	// dropping a foreign table never touches the remote data it pointed to.
	if n := backingRowCount(); n != 1 {
		t.Fatalf("events_archive_backing row count after dropping the foreign partition: got %d, want 1 (unchanged)", n)
	}

	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
