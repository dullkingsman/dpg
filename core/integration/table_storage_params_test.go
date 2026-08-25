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
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripTableStorageParams is the live regression guard for RFC
// Section 7.11's WITH (...) storage-params clause: diffTable had zero
// handling for a changed WITH (...) at all before this (a diff-only gap),
// and WITH (...) itself wasn't even rendered at CREATE time for a plain
// table (a separate, deeper gap found alongside it — createTable silently
// dropped any declared storage param, live or not).
//
// Confirms live: a declared WITH (fillfactor=...) clause lands in
// pg_class.reloptions, a second plan against freshly introspected live state
// is a genuine no-op, changing/adding a key runs a real ALTER TABLE ... SET
// (...), removing one runs ALTER TABLE ... RESET (...) (neither a
// drop+recreate), and the table's rows survive both (proving they're
// metadata-only rewrites in place).
func TestRoundtripTableStorageParams(t *testing.T) {
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

	reloptions := func() map[string]string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT unnest(reloptions) FROM pg_class WHERE relname = 't'`)
		if err != nil {
			t.Fatalf("query reloptions: %v", err)
		}
		defer rows.Close()
		m := map[string]string{}
		for rows.Next() {
			var kv string
			if err := rows.Scan(&kv); err != nil {
				t.Fatalf("scan reloption: %v", err)
			}
			for i := 0; i < len(kv); i++ {
				if kv[i] == '=' {
					m[kv[:i]] = kv[i+1:]
					break
				}
			}
		}
		return m
	}

	rowCount := func() int {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM t`)
		if err != nil {
			t.Fatalf("query row count: %v", err)
		}
		defer rows.Close()
		rows.Next()
		var n int
		_ = rows.Scan(&n)
		return n
	}

	write(`TABLE t (id INTEGER NOT NULL) WITH (fillfactor=70, autovacuum_enabled=false);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	opts := reloptions()
	if opts["fillfactor"] != "70" || opts["autovacuum_enabled"] != "false" {
		t.Fatalf("reloptions after create: got %v", opts)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	if _, err := conn.Exec(ctx, `INSERT INTO t (id) VALUES (1), (2);`); err != nil {
		t.Fatalf("insert rows: %v", err)
	}

	// Change fillfactor, drop autovacuum_enabled, add a new key — exercises
	// SET (changed+new) and RESET (removed) in the same migration.
	write(`TABLE t (id INTEGER NOT NULL) WITH (fillfactor=90, toast_tuple_target=512);`)
	desired, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	base, _ := store.Load("test", "dpgtest")
	planOps, err := differ.Diff(desired, base)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	var planSQL []string
	for _, op := range planOps {
		planSQL = append(planSQL, op.SQL())
	}
	joined := strings.Join(planSQL, "\n")
	if !strings.Contains(joined, `SET (fillfactor=90, toast_tuple_target=512)`) {
		t.Fatalf("expected a real ALTER TABLE ... SET (...), got ops:\n%s", joined)
	}
	if !strings.Contains(joined, `RESET (autovacuum_enabled)`) {
		t.Fatalf("expected a real ALTER TABLE ... RESET (...), got ops:\n%s", joined)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	opts = reloptions()
	if opts["fillfactor"] != "90" || opts["toast_tuple_target"] != "512" {
		t.Fatalf("reloptions after SET: got %v", opts)
	}
	if _, stillSet := opts["autovacuum_enabled"]; stillSet {
		t.Fatalf("expected autovacuum_enabled reset, got %v", opts)
	}
	if n := rowCount(); n != 2 {
		t.Fatalf("row count after SET/RESET: got %d, want 2 (must not lose data)", n)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	// Clearing WITH (...) entirely resets every remaining key.
	write(`TABLE t (id INTEGER NOT NULL);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if opts = reloptions(); len(opts) != 0 {
		t.Fatalf("reloptions after clearing WITH: got %v, want empty", opts)
	}
	if n := rowCount(); n != 2 {
		t.Fatalf("row count after clearing WITH: got %d, want 2 (must not lose data)", n)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}

// TestRoundtripTableAccessMethodPartitionByOrdering is the live regression
// guard for the CREATE TABLE clause-ordering bug found while implementing
// WITH (...): createTable previously rendered USING before calling
// finishCreateTable (whose own body renders INHERITS/PARTITION BY
// afterward), producing an outright syntax error whenever a table declared
// USING together with PARTITION BY ("... USING heap2 PARTITION BY RANGE
// (...)" is rejected by real PostgreSQL; "... PARTITION BY RANGE (...)
// USING heap2" is not). WITH (...) is deliberately not combined into this
// same table — a real, separate PostgreSQL restriction (confirmed live:
// "cannot specify storage parameters for a partitioned table") rejects WITH
// on any partitioned table regardless of clause order, unrelated to the
// ordering bug this test targets. Confirms a table declaring both PARTITION
// BY and a non-default USING method applies as valid SQL, with the
// requested access method actually taking effect, and the resulting plan is
// a genuine no-op against live state.
func TestRoundtripTableAccessMethodPartitionByOrdering(t *testing.T) {
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

	// Register a second table access method backed by the same built-in heap
	// handler (there is no other stock AM to name), same trick
	// TestRoundtripTableAccessMethod already uses to get a value
	// distinguishable from the "heap" default.
	if _, err := conn.Exec(ctx, `CREATE ACCESS METHOD heap2 TYPE TABLE HANDLER heap_tableam_handler;`); err != nil {
		t.Fatalf("create access method: %v", err)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(f, []byte(
		`TABLE t (id INTEGER NOT NULL, created_at DATE NOT NULL) PARTITION BY RANGE (created_at) USING heap2;`,
	), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	rows, err := conn.QueryRows(ctx, `SELECT am.amname FROM pg_class c JOIN pg_am am ON am.oid = c.relam WHERE c.relname = 't'`)
	if err != nil {
		t.Fatalf("query access method: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("t not found")
	}
	var amname string
	if err := rows.Scan(&amname); err != nil {
		t.Fatalf("scan access method: %v", err)
	}
	rows.Close()
	if amname != "heap2" {
		t.Fatalf("access method after create: got %q, want %q", amname, "heap2")
	}

	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
