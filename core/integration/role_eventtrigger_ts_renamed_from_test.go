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

// TestRoundtripRoleRenamedFrom is the regression guard for Role rename
// detection: before this, ir.Role had no RenamedFrom field at all, so a
// renamed role was indistinguishable from "old role dropped, new one
// added" — a real DROP ROLE, which real PostgreSQL can outright refuse if
// the role still owns any object (the RFC's own documented severity for
// this gap: drop-and-recreate isn't always a viable fallback at all).
func TestRoundtripRoleRenamedFrom(t *testing.T) {
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

	v1 := `ROLE app_role_old NOLOGIN;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	roleExists := func(name string) bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_roles WHERE rolname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_roles for %s: %v", name, err)
		}
		defer rows.Close()
		var n int
		rows.Next()
		_ = rows.Scan(&n)
		return n == 1
	}
	if !roleExists("app_role_old") {
		t.Fatalf("app_role_old does not exist after initial apply")
	}

	// Have the role own an object, so a DROP ROLE fallback would genuinely
	// fail — proving the fix is load-bearing, not just avoiding an extra
	// statement.
	if _, err := conn.Exec(ctx, `ALTER SCHEMA public OWNER TO app_role_old;`); err != nil {
		t.Fatalf("make app_role_old own the public schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, `ALTER SCHEMA public OWNER TO postgres;`)
	})

	v2 := `ROLE app_role NOLOGIN {
    RENAMED FROM app_role_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if roleExists("app_role_old") {
		t.Fatalf("app_role_old still exists after rename")
	}
	if !roleExists("app_role") {
		t.Fatalf("app_role does not exist after rename")
	}
}

// TestRoundtripEventTriggerRenamedFrom is the regression guard for
// EventTrigger rename detection: before this, ir.EventTrigger had no
// RenamedFrom field at all — the struct's own doc comment explicitly said
// none of ENABLE/DISABLE/OWNER TO/RENAME TO were modeled.
func TestRoundtripEventTriggerRenamedFrom(t *testing.T) {
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

	v1 := `FUNCTION evt_fn() RETURNS event_trigger
LANGUAGE plpgsql AS $$ BEGIN END; $$ {}

EVENT TRIGGER evt_old ON sql_drop
    WHEN TAG IN ('DROP TABLE')
    EXECUTE FUNCTION evt_fn();`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	eventTriggerExists := func(name string) bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_event_trigger WHERE evtname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_event_trigger for %s: %v", name, err)
		}
		defer rows.Close()
		var n int
		rows.Next()
		_ = rows.Scan(&n)
		return n == 1
	}
	if !eventTriggerExists("evt_old") {
		t.Fatalf("evt_old does not exist after initial apply")
	}

	v2 := `FUNCTION evt_fn() RETURNS event_trigger
LANGUAGE plpgsql AS $$ BEGIN END; $$ {}

EVENT TRIGGER evt ON sql_drop
    WHEN TAG IN ('DROP TABLE')
    EXECUTE FUNCTION evt_fn() {
    RENAMED FROM evt_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if eventTriggerExists("evt_old") {
		t.Fatalf("evt_old still exists after rename")
	}
	if !eventTriggerExists("evt") {
		t.Fatalf("evt does not exist after rename")
	}
}

// TestRoundtripTSDictRenamedFrom is the regression guard for TSDict rename
// detection.
func TestRoundtripTSDictRenamedFrom(t *testing.T) {
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

	v1 := `TEXT SEARCH DICTIONARY simple_dict_old (TEMPLATE = pg_catalog.simple);`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	dictExists := func(name string) bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_ts_dict WHERE dictname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_ts_dict for %s: %v", name, err)
		}
		defer rows.Close()
		var n int
		rows.Next()
		_ = rows.Scan(&n)
		return n == 1
	}
	if !dictExists("simple_dict_old") {
		t.Fatalf("simple_dict_old does not exist after initial apply")
	}

	v2 := `TEXT SEARCH DICTIONARY simple_dict (TEMPLATE = pg_catalog.simple) {
    RENAMED FROM simple_dict_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if dictExists("simple_dict_old") {
		t.Fatalf("simple_dict_old still exists after rename")
	}
	if !dictExists("simple_dict") {
		t.Fatalf("simple_dict does not exist after rename")
	}
}

// TestRoundtripTSParserAndTemplateRenamedFrom covers TSParser and TSTemplate
// rename together, using PostgreSQL's own built-in support functions
// (prsd_*/dsimple_*, the same ones its default parser/template use
// internally) so both can be created and renamed from pure SQL without a C
// extension.
func TestRoundtripTSParserAndTemplateRenamedFrom(t *testing.T) {
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

	v1 := `TEXT SEARCH PARSER prs_old (START = prsd_start, GETTOKEN = prsd_nexttoken, END = prsd_end, LEXTYPES = prsd_lextype);
TEXT SEARCH TEMPLATE tmpl_old (LEXIZE = dsimple_lexize);`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	parserExists := func(name string) bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_ts_parser WHERE prsname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_ts_parser for %s: %v", name, err)
		}
		defer rows.Close()
		var n int
		rows.Next()
		_ = rows.Scan(&n)
		return n == 1
	}
	templateExists := func(name string) bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_ts_template WHERE tmplname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_ts_template for %s: %v", name, err)
		}
		defer rows.Close()
		var n int
		rows.Next()
		_ = rows.Scan(&n)
		return n == 1
	}
	if !parserExists("prs_old") {
		t.Fatalf("prs_old does not exist after initial apply")
	}
	if !templateExists("tmpl_old") {
		t.Fatalf("tmpl_old does not exist after initial apply")
	}

	v2 := `TEXT SEARCH PARSER prs (START = prsd_start, GETTOKEN = prsd_nexttoken, END = prsd_end, LEXTYPES = prsd_lextype) {
    RENAMED FROM prs_old;
}
TEXT SEARCH TEMPLATE tmpl (LEXIZE = dsimple_lexize) {
    RENAMED FROM tmpl_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if parserExists("prs_old") {
		t.Fatalf("prs_old still exists after rename")
	}
	if !parserExists("prs") {
		t.Fatalf("prs does not exist after rename")
	}
	if templateExists("tmpl_old") {
		t.Fatalf("tmpl_old still exists after rename")
	}
	if !templateExists("tmpl") {
		t.Fatalf("tmpl does not exist after rename")
	}
}
