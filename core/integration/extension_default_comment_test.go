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

// TestRoundtripExtensionDefaultCommentNoDrift is the live regression guard
// for a real bug found while testing #14 (foreign table as partition):
// CREATE EXTENSION auto-populates obj_description from the extension's own
// .control file `comment` field with zero explicit COMMENT ON EXTENSION
// ever run — confirmed live, postgres_fdw's real obj_description right
// after a bare CREATE EXTENSION is already "foreign-data wrapper for
// remote PostgreSQL servers". diffExtension's `!ptrEq(o.Comment,
// snap.Comment)` check previously had no way to know this, so an
// undeclared (nil) source Comment permanently mismatched that live default,
// proposing a spurious `COMMENT ON EXTENSION ... IS NULL` on every single
// plan against a live database.
//
// Also proves the fix doesn't break genuine comment management: an
// explicitly declared comment (matching or not matching the control-file
// default) still applies via a real COMMENT ON EXTENSION, and removing a
// previously-declared non-default comment from source still clears it back
// to NULL — only the true, never-explicitly-set default is suppressed.
func TestRoundtripExtensionDefaultCommentNoDrift(t *testing.T) {
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

	liveComment := func() *string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT obj_description(oid, 'pg_extension') FROM pg_extension WHERE extname = 'postgres_fdw'`)
		if err != nil {
			t.Fatalf("query obj_description: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("postgres_fdw extension not found")
		}
		var c *string
		_ = rows.Scan(&c)
		return c
	}

	// Stage 1: no COMMENT declared at all — the real-world common case.
	// Before the fix, this alone produced permanent live drift.
	write(`EXTENSION postgres_fdw;`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if c := liveComment(); c == nil || *c != "foreign-data wrapper for remote PostgreSQL servers" {
		t.Fatalf("expected the control-file default comment to be set automatically, got %v", c)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	// Stage 2: declare an explicit, non-default comment — must still apply
	// via a real COMMENT ON EXTENSION (the fix must not suppress genuine
	// comment management, only the untouched control-file default).
	write(`
EXTENSION postgres_fdw {
    COMMENT 'our own note about this extension';
};`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if c := liveComment(); c == nil || *c != "our own note about this extension" {
		t.Fatalf("expected the declared custom comment to be set, got %v", c)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	// Stage 3: remove the declaration — since the live comment is now the
	// custom text (not the control-file default), this must still clear it
	// back to NULL via a real COMMENT ON EXTENSION ... IS NULL, proving the
	// suppression in stage 1 doesn't also swallow a genuine removal.
	write(`EXTENSION postgres_fdw;`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if c := liveComment(); c != nil {
		t.Fatalf("expected the custom comment to be cleared, got %v", c)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}

// TestDiffExtensionExplicitCommentMatchingDefaultStillApplies guards a
// narrow edge case at the offline-diff level: an explicitly declared
// comment that happens to be byte-for-byte identical to the control-file
// default is indistinguishable, via PostgreSQL's own catalog, from the
// untouched default — pg_description carries no "who set this" metadata.
// This documents the accepted, unavoidable ambiguity (same class as the
// ROLE-direction WITH INHERIT compaction gap) rather than silently
// asserting behavior that can't actually be guaranteed: applying such a
// declaration is indistinguishable from a no-op, so this only proves the
// SQL emitted at DIFF time (against an empty snapshot, offline) is still a
// real, correct COMMENT ON EXTENSION statement.
func TestDiffExtensionExplicitCommentMatchingDefaultStillApplies(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")
	src := "EXTENSION postgres_fdw {\n    COMMENT 'foreign-data wrapper for remote PostgreSQL servers';\n};"
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	desired, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	d := diff.New()
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	var sawComment bool
	for _, op := range ops {
		if strings.Contains(op.SQL(), "COMMENT ON EXTENSION") {
			sawComment = true
		}
	}
	if !sawComment {
		t.Fatalf("expected a COMMENT ON EXTENSION statement, got: %v", opsSQL(ops))
	}
}
