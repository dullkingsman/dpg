//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thec1oud/dpg/internal/compiler"
	"github.com/thec1oud/dpg/internal/diff"
	"github.com/thec1oud/dpg/internal/emit"
	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/introspect"
	"github.com/thec1oud/dpg/internal/ir"
	"github.com/thec1oud/dpg/internal/pipeline"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestRoundtripTriggerUpdateOfColumns is the regression guard for RFC audit
// item #1: the "UPDATE OF col1, col2, ..." column list was tokenized and
// explicitly discarded by the blockparser (comment admitted it) — a
// trigger declared to fire only on specific columns actually fired on
// every column update instead, a real semantics divergence. This proves
// against a real database: the trigger only fires when a declared column
// changes (and correctly does NOT fire for an unrelated column update),
// and that a fresh introspect pass sees the same column-scoping via
// pg_trigger.tgattr.
func TestRoundtripTriggerUpdateOfColumns(t *testing.T) {
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

	v1 := `FUNCTION bump_counter() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.change_count := COALESCE(OLD.change_count, 0) + 1;
    RETURN NEW;
END;
$$ {}

TABLE widgets (
    id            bigint PRIMARY KEY,
    email         text NOT NULL,
    other_field   text,
    change_count  integer NOT NULL DEFAULT 0
) {
    TRIGGER trg_email_changed BEFORE UPDATE OF email FOR EACH ROW EXECUTE FUNCTION bump_counter();
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, err := conn.Exec(ctx, `INSERT INTO widgets (id, email, other_field) VALUES (1, 'a@example.com', 'x')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	countFor := func(id int) int {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT change_count FROM widgets WHERE id = $1`, id)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("no row")
		}
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return n
	}

	// Updating a column NOT in the OF list must NOT fire the trigger.
	if _, err := conn.Exec(ctx, `UPDATE widgets SET other_field = 'y' WHERE id = 1`); err != nil {
		t.Fatalf("update other_field: %v", err)
	}
	if got := countFor(1); got != 0 {
		t.Fatalf("change_count = %d after updating other_field (not in OF list), want 0 — bug #1 regressed (trigger fires on every column update)", got)
	}

	// Updating the declared column MUST fire the trigger.
	if _, err := conn.Exec(ctx, `UPDATE widgets SET email = 'b@example.com' WHERE id = 1`); err != nil {
		t.Fatalf("update email: %v", err)
	}
	if got := countFor(1); got != 1 {
		t.Fatalf("change_count = %d after updating email (in OF list), want 1 — trigger didn't fire at all", got)
	}

	// A fresh introspect pass must see the same column scoping.
	ci := introspect.New()
	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var liveTrigger *ir.Trigger
	for _, obj := range liveObjects {
		tbl, ok := obj.(*ir.Table)
		if !ok || tbl.Name != "widgets" {
			continue
		}
		for _, trg := range tbl.Triggers {
			if trg.Name == "trg_email_changed" {
				liveTrigger = trg
			}
		}
	}
	if liveTrigger == nil {
		t.Fatal("introspect did not return trg_email_changed")
	}
	if len(liveTrigger.UpdateOfColumns) != 1 || liveTrigger.UpdateOfColumns[0] != "email" {
		t.Fatalf("introspected UpdateOfColumns = %v, want [email]", liveTrigger.UpdateOfColumns)
	}

	// Changing the column list must be detected and re-applied.
	v2 := `FUNCTION bump_counter() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.change_count := COALESCE(OLD.change_count, 0) + 1;
    RETURN NEW;
END;
$$ {}

TABLE widgets (
    id            bigint PRIMARY KEY,
    email         text NOT NULL,
    other_field   text,
    change_count  integer NOT NULL DEFAULT 0
) {
    TRIGGER trg_email_changed BEFORE UPDATE OF email, other_field FOR EACH ROW EXECUTE FUNCTION bump_counter();
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	desired2, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile v2: %v", err)
	}
	prevSnap, _ := store.Load("test", "dpgtest")
	ops, err := differ.Diff(desired2, prevSnap)
	if err != nil {
		t.Fatalf("diff (OF list changed): %v", err)
	}
	var sawDrop, sawRecreate bool
	for _, op := range ops {
		if strings.Contains(op.SQL(), `DROP TRIGGER IF EXISTS "trg_email_changed"`) {
			sawDrop = true
		}
		if strings.Contains(op.SQL(), "CREATE TRIGGER") && strings.Contains(op.SQL(), `UPDATE OF "email", "other_field"`) {
			sawRecreate = true
		}
	}
	if !sawDrop || !sawRecreate {
		t.Fatalf("expected DROP+CREATE reflecting the new OF list, got: %v", opsSQL(ops))
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	// Now updating other_field must ALSO fire the trigger.
	if _, err := conn.Exec(ctx, `UPDATE widgets SET other_field = 'z' WHERE id = 1`); err != nil {
		t.Fatalf("update other_field after OF list change: %v", err)
	}
	if got := countFor(1); got != 2 {
		t.Fatalf("change_count = %d after updating other_field (now in OF list), want 2 — bug #1 regressed (OF list change not applied live)", got)
	}
}
