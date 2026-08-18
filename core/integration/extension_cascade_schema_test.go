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

// TestRoundtripExtensionCascade is the regression guard for RFC audit item
// #15: buildExtension never read pg_query's "cascade" DefElem, so a
// declared CASCADE was silently discarded — breaking the RFC's own
// canonical example ("EXTENSION pg_trgm CASCADE;") verbatim. This proves,
// via the real compiler pipeline (not hand-built IR), that CASCADE reaches
// the generated SQL and applies successfully against a real database.
func TestRoundtripExtensionCascade(t *testing.T) {
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
	if err := os.WriteFile(f, []byte(`EXTENSION pg_trgm CASCADE;`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	desired, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ops, err := differ.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	var sawCascade bool
	for _, op := range ops {
		if strings.Contains(op.SQL(), "CREATE EXTENSION") && strings.Contains(op.SQL(), "CASCADE") {
			sawCascade = true
		}
	}
	if !sawCascade {
		t.Fatalf("expected CREATE EXTENSION ... CASCADE in the diff, got: %v", opsSQL(ops))
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	rows, err := conn.QueryRows(ctx, `SELECT count(*) FROM pg_extension WHERE extname = 'pg_trgm'`)
	if err != nil {
		t.Fatalf("query pg_extension: %v", err)
	}
	defer rows.Close()
	rows.Next()
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 1 {
		t.Fatalf("pg_trgm not installed after CREATE EXTENSION ... CASCADE — bug #15 regressed")
	}
}

// TestRoundtripExtensionSchemaChange is the regression guard for RFC audit
// item #16: SnapExtension.Schema existed but diffExtension never compared
// it, a spurious no-op on a genuine SCHEMA change that the RFC requires to
// be a DESTRUCTIVE drop + recreate. This proves the extension actually
// moves to the new schema live.
func TestRoundtripExtensionSchemaChange(t *testing.T) {
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

	liveSchema := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid = e.extnamespace WHERE e.extname = 'pg_trgm'`)
		if err != nil {
			t.Fatalf("query pg_extension schema: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("pg_extension has no row for pg_trgm")
		}
		var schema string
		if err := rows.Scan(&schema); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return schema
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	v1 := `SCHEMA ext_schema {}

EXTENSION pg_trgm SCHEMA public;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := liveSchema(); got != "public" {
		t.Fatalf("pg_trgm: live schema = %q after initial apply, want public — test setup is broken", got)
	}

	v2 := `SCHEMA ext_schema {}

EXTENSION pg_trgm SCHEMA ext_schema;`
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
		t.Fatalf("diff (schema change): %v", err)
	}
	var sawDrop, sawRecreate bool
	for _, op := range ops {
		if strings.Contains(op.SQL(), "DROP EXTENSION") {
			sawDrop = true
			if op.Safety() != pipeline.Destructive {
				t.Errorf("expected DROP EXTENSION for a schema change to be Destructive, got %s", op.Safety())
			}
		}
		if strings.Contains(op.SQL(), "CREATE EXTENSION") && strings.Contains(op.SQL(), `SCHEMA "ext_schema"`) {
			sawRecreate = true
		}
	}
	if !sawDrop || !sawRecreate {
		t.Fatalf("expected DROP+CREATE reflecting the new schema, got: %v", opsSQL(ops))
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := liveSchema(); got != "ext_schema" {
		t.Fatalf("pg_trgm: live schema = %q after SCHEMA change — bug #16 regressed", got)
	}

	newSnap, _ := store.Load("test", "dpgtest")
	noDriftOps, err := differ.Diff(desired2, newSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(noDriftOps) != 0 {
		t.Errorf("expected zero drift after the schema change, got %d ops:", len(noDriftOps))
		for _, op := range noDriftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}
}
