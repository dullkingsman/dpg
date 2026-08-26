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

// TestRoundtripEventTriggerOwner is the live regression guard for Section
// 14.1's OWNER TO capability: previously ir.EventTrigger had no Owner field
// at all, so a declared owner had zero effect on the live catalog.
func TestRoundtripEventTriggerOwner(t *testing.T) {
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

	// Real PostgreSQL restricts event trigger ownership to superusers only
	// (both to own one and to become the new owner via OWNER TO) —
	// confirmed live ("permission denied to change owner of event trigger").
	if _, err := conn.Exec(ctx, `CREATE ROLE evt_owner NOLOGIN SUPERUSER;`); err != nil {
		t.Fatalf("create role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, `DROP ROLE IF EXISTS evt_owner;`)
	})

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	v1 := `FUNCTION evt_fn() RETURNS event_trigger
LANGUAGE plpgsql AS $$ BEGIN END; $$ {}

EVENT TRIGGER evt ON sql_drop
    WHEN TAG IN ('DROP TABLE')
    EXECUTE FUNCTION evt_fn();`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	ownerOf := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT r.rolname FROM pg_event_trigger e JOIN pg_roles r ON r.oid = e.evtowner WHERE e.evtname = 'evt'`)
		if err != nil {
			t.Fatalf("query owner: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("evt does not exist")
		}
		var owner string
		_ = rows.Scan(&owner)
		return owner
	}

	initialOwner := ownerOf()
	if initialOwner == "evt_owner" {
		t.Fatalf("evt_owner should not already own evt before OWNER TO is applied")
	}

	v2 := `FUNCTION evt_fn() RETURNS event_trigger
LANGUAGE plpgsql AS $$ BEGIN END; $$ {}

EVENT TRIGGER evt ON sql_drop
    WHEN TAG IN ('DROP TABLE')
    EXECUTE FUNCTION evt_fn() {
    OWNER "evt_owner";
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := ownerOf(); got != "evt_owner" {
		t.Fatalf("expected evt_owner to own evt after OWNER TO, got %q", got)
	}
}
