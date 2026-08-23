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

// TestRoundtripTriggerEnableState is the live regression guard for RFC
// audit item #56: Section 7.9's trigger-enable-state, previously not
// modeled anywhere in core/. Proves live that a declared enable-state
// actually takes effect (via pg_trigger.tgenabled) — including that
// DISABLED genuinely stops the trigger from firing, not just a catalog
// flag with no behavioral effect — and that changing it in place (without
// touching any other trigger property) is a targeted ALTER, not a
// drop-and-recreate (proven via a stable trigger OID).
func TestRoundtripTriggerEnableState(t *testing.T) {
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

	v1 := `FUNCTION trg_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.n := NEW.n + 1; RETURN NEW; END; $$ {}

TABLE t (id INTEGER, n INTEGER DEFAULT 0) {
    TRIGGER trg_a BEFORE INSERT
        FOR EACH ROW
        EXECUTE FUNCTION trg_fn()
        DISABLED;
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	triggerState := func() (string, int64) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT tgenabled::text, oid::bigint FROM pg_trigger WHERE tgname = 'trg_a'`)
		if err != nil {
			t.Fatalf("query tgenabled: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("trg_a does not exist")
		}
		var state string
		var oid int64
		_ = rows.Scan(&state, &oid)
		return state, oid
	}

	state, oldOID := triggerState()
	if state != "D" {
		t.Fatalf("expected tgenabled = 'D' (disabled) after initial apply, got %q", state)
	}

	// A disabled trigger must not actually fire.
	if _, err := conn.Exec(ctx, `INSERT INTO t (id, n) VALUES (1, 5);`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := conn.QueryRows(ctx, `SELECT n FROM t WHERE id = 1`)
	if err != nil {
		t.Fatalf("query n: %v", err)
	}
	var n int
	rows.Next()
	_ = rows.Scan(&n)
	rows.Close()
	if n != 5 {
		t.Fatalf("expected trigger to be genuinely disabled (n unchanged at 5), got n=%d", n)
	}

	v2 := `FUNCTION trg_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.n := NEW.n + 1; RETURN NEW; END; $$ {}

TABLE t (id INTEGER, n INTEGER DEFAULT 0) {
    TRIGGER trg_a BEFORE INSERT
        FOR EACH ROW
        EXECUTE FUNCTION trg_fn();
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	state, newOID := triggerState()
	if state != "O" {
		t.Fatalf("expected tgenabled = 'O' (origin/enabled) after re-enabling, got %q", state)
	}
	if newOID != oldOID {
		t.Fatalf("trg_a has a different OID (%d) than before (%d) — dropped and recreated instead of a targeted ALTER", newOID, oldOID)
	}

	// Now that it's enabled, it must actually fire.
	if _, err := conn.Exec(ctx, `INSERT INTO t (id, n) VALUES (2, 5);`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows2, err := conn.QueryRows(ctx, `SELECT n FROM t WHERE id = 2`)
	if err != nil {
		t.Fatalf("query n: %v", err)
	}
	defer rows2.Close()
	rows2.Next()
	_ = rows2.Scan(&n)
	if n != 6 {
		t.Fatalf("expected the re-enabled trigger to fire (n=6), got n=%d", n)
	}
}
