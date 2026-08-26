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

// TestRoundtripTablespaceStorageParams is the live regression guard for RFC
// Section 14.7's WITH (...) storage-params clause (#15): before this,
// ir.Tablespace had no StorageParams field at all, and — unlike Table's
// pre-#60 gap, which at least fell back to opaque body-hash comparison —
// Tablespace bodies are always Reconstructed on introspection, which skips
// hash comparison entirely (see sourceBodyHash), so a live-catalog storage-
// param change was completely invisible at every layer, not merely
// undiffed. Introspection also never included spcoptions in the
// reconstructed CREATE TABLESPACE body text, so a `dpg dump` of a
// tablespace with declared storage params silently dropped them too.
//
// Confirms live: a declared WITH (...) clause lands in pg_tablespace's
// reloptions, a second plan against freshly introspected live state is a
// genuine no-op, changing/adding a key runs a real ALTER TABLESPACE ... SET
// (...), and removing one runs ALTER TABLESPACE ... RESET (...).
func TestRoundtripTablespaceStorageParams(t *testing.T) {
	connStr, container := testpg.StartWithContainer(t)
	ctx := context.Background()

	if code, _, err := container.Exec(ctx, []string{"mkdir", "-p", "/var/lib/postgresql/ts_storage"}); err != nil || code != 0 {
		t.Fatalf("mkdir in container: code=%d err=%v", code, err)
	}
	if code, _, err := container.Exec(ctx, []string{"chown", "postgres:postgres", "/var/lib/postgresql/ts_storage"}); err != nil || code != 0 {
		t.Fatalf("chown in container: code=%d err=%v", code, err)
	}

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
		rows, err := conn.QueryRows(ctx, `SELECT unnest(spcoptions) FROM pg_tablespace WHERE spcname = 'ts_storage'`)
		if err != nil {
			t.Fatalf("query spcoptions: %v", err)
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

	write(`TABLESPACE ts_storage LOCATION '/var/lib/postgresql/ts_storage' WITH (seq_page_cost=1.5, random_page_cost=2.0);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	opts := reloptions()
	if opts["seq_page_cost"] != "1.5" || opts["random_page_cost"] != "2.0" {
		t.Fatalf("reloptions after create: got %v", opts)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	// Change seq_page_cost, drop random_page_cost, add a new key.
	write(`TABLESPACE ts_storage LOCATION '/var/lib/postgresql/ts_storage' WITH (seq_page_cost=1.0, effective_io_concurrency=200);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	opts = reloptions()
	if opts["seq_page_cost"] != "1.0" || opts["effective_io_concurrency"] != "200" {
		t.Fatalf("reloptions after SET: got %v", opts)
	}
	if _, stillSet := opts["random_page_cost"]; stillSet {
		t.Fatalf("expected random_page_cost reset, got %v", opts)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	// Clearing WITH (...) entirely resets every remaining key.
	write(`TABLESPACE ts_storage LOCATION '/var/lib/postgresql/ts_storage';`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if opts = reloptions(); len(opts) != 0 {
		t.Fatalf("reloptions after clearing WITH: got %v, want empty", opts)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
