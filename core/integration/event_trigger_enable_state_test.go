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

// TestRoundtripEventTriggerEnableState is the live regression guard for RFC
// audit item #76's remainder: Section 14.1's trigger-enable-state
// (DISABLED/ENABLE REPLICA/ENABLE ALWAYS), previously not modeled anywhere
// in core/ — the struct's own doc comment explicitly said none of
// ENABLE/DISABLE were modeled, reasoning it was blocked on the same
// mechanism as table Trigger's now-closed #56. Modeled as a block directive
// rather than the RFC's literal (but PG-invalid) inline Part-1 placement:
// confirmed live that real CREATE EVENT TRIGGER rejects an inline
// enable-state clause outright.
//
// Proves live that a declared enable-state actually takes effect (via
// pg_event_trigger.evtenabled) — including that DISABLED genuinely stops
// the event trigger from firing at all, not just a catalog flag with no
// behavioral effect — and that changing it in place (without touching any
// other property) is a targeted ALTER EVENT TRIGGER, not a
// drop-and-recreate (proven via a stable event trigger OID).
func TestRoundtripEventTriggerEnableState(t *testing.T) {
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

	if _, err := conn.Exec(ctx, `CREATE TABLE ddl_hits (n INTEGER);`); err != nil {
		t.Fatalf("create counter table: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO ddl_hits (n) VALUES (0);`); err != nil {
		t.Fatalf("seed counter: %v", err)
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	v1 := `FUNCTION evt_fn() RETURNS event_trigger
LANGUAGE plpgsql AS $$ BEGIN UPDATE ddl_hits SET n = n + 1; END; $$ {}

EVENT TRIGGER audit_ddl ON ddl_command_start
    EXECUTE FUNCTION evt_fn() {
    DISABLED;
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	evtState := func() (string, int64) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT evtenabled::text, oid::bigint FROM pg_event_trigger WHERE evtname = 'audit_ddl'`)
		if err != nil {
			t.Fatalf("query evtenabled: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("audit_ddl does not exist")
		}
		var state string
		var oid int64
		_ = rows.Scan(&state, &oid)
		return state, oid
	}
	hitCount := func() int {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT n FROM ddl_hits`)
		if err != nil {
			t.Fatalf("query ddl_hits: %v", err)
		}
		defer rows.Close()
		rows.Next()
		var n int
		_ = rows.Scan(&n)
		return n
	}

	state, oldOID := evtState()
	if state != "D" {
		t.Fatalf("expected evtenabled = 'D' (disabled) after initial apply, got %q", state)
	}

	// A disabled event trigger must not actually fire.
	before := hitCount()
	if _, err := conn.Exec(ctx, `CREATE TABLE probe1 (id INTEGER);`); err != nil {
		t.Fatalf("create probe1: %v", err)
	}
	if after := hitCount(); after != before {
		t.Fatalf("expected the disabled event trigger not to fire (count unchanged at %d), got %d", before, after)
	}

	v2 := `FUNCTION evt_fn() RETURNS event_trigger
LANGUAGE plpgsql AS $$ BEGIN UPDATE ddl_hits SET n = n + 1; END; $$ {}

EVENT TRIGGER audit_ddl ON ddl_command_start
    EXECUTE FUNCTION evt_fn();`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	state, newOID := evtState()
	if state != "O" {
		t.Fatalf("expected evtenabled = 'O' (origin/enabled) after re-enabling, got %q", state)
	}
	if newOID != oldOID {
		t.Fatalf("audit_ddl has a different OID (%d) than before (%d) — dropped and recreated instead of a targeted ALTER", newOID, oldOID)
	}

	// Now that it's enabled, it must actually fire.
	before = hitCount()
	if _, err := conn.Exec(ctx, `CREATE TABLE probe2 (id INTEGER);`); err != nil {
		t.Fatalf("create probe2: %v", err)
	}
	if after := hitCount(); after != before+1 {
		t.Fatalf("expected the re-enabled event trigger to fire (count %d -> %d), got %d", before, before+1, after)
	}

	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
