//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/introspect"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripCompositeAttributeCollation is the live regression guard for
// RFC Section 5.2's composite attribute COLLATE clause: real PostgreSQL
// grammar accepts it directly on ColumnDef (CollClause), no DPG-specific
// parsing needed, but ir.Column had no Collation field at all before this,
// so it was silently dropped between parse and diff regardless.
//
// Confirms live: a declared COLLATE lands in pg_attribute.attcollation, a
// second plan against the freshly introspected live state is a genuine
// no-op, and a COLLATE-only change on an existing attribute (RFC's "type or
// COLLATE" DESTRUCTIVE row) actually runs ALTER TYPE ... ALTER ATTRIBUTE
// ... TYPE ... COLLATE against a real server, not just diff-only text.
func TestRoundtripCompositeAttributeCollation(t *testing.T) {
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

	attrCollation := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
			SELECT co.collname
			FROM pg_type t
			JOIN pg_class c ON c.oid = t.typrelid
			JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'street'
			JOIN pg_collation co ON co.oid = a.attcollation
			WHERE t.typname = 'addr'`)
		if err != nil {
			t.Fatalf("query attcollation: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("addr.street not found or has no non-default collation")
		}
		var name string
		_ = rows.Scan(&name)
		return name
	}

	noDrift := func(files []string) {
		t.Helper()
		desired, _, err := compiler.Compile(files, dir, pipeline.Default)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		base, _ := store.Load("test", "dpgtest")
		ops, err := differ.Diff(desired, base)
		if err != nil {
			t.Fatalf("diff: %v", err)
		}
		if len(ops) != 0 {
			t.Errorf("expected no-op re-diff, got %d ops:", len(ops))
			for _, op := range ops {
				t.Errorf("  [%s] %s", op.Safety(), op.SQL())
			}
		}

		ci := introspect.New()
		liveObjects, err := ci.Introspect(ctx, conn)
		if err != nil {
			t.Fatalf("introspect: %v", err)
		}
		var managedLive []pipeline.IRObject
		for _, obj := range liveObjects {
			if _, ok := base.Objects[obj.QualifiedName()]; ok {
				managedLive = append(managedLive, obj)
			}
		}
		liveSnap := &pipeline.Snapshot{}
		if err := snapshot.Populate(liveSnap, managedLive); err != nil {
			t.Fatalf("populate live snapshot: %v", err)
		}
		liveDriftOps, err := differ.Diff(desired, liveSnap)
		if err != nil {
			t.Fatalf("live drift diff: %v", err)
		}
		if len(liveDriftOps) != 0 {
			t.Errorf("expected zero drift against freshly introspected live state, got %d ops:", len(liveDriftOps))
			for _, op := range liveDriftOps {
				t.Errorf("  [%s] %s", op.Safety(), op.SQL())
			}
		}
	}

	// Stage 1: create with an explicit non-default COLLATE ("C" is always
	// present regardless of the server's configured locale).
	write(`TYPE addr AS (street text COLLATE "C", city text);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := attrCollation(); got != "C" {
		t.Fatalf("attcollation after create: got %q, want %q", got, "C")
	}
	noDrift([]string{f})

	// Stage 2: COLLATE-only change (same type) must run a real ALTER
	// ATTRIBUTE TYPE ... COLLATE, not a no-op.
	write(`TYPE addr AS (street text COLLATE "POSIX", city text);`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := attrCollation(); got != "POSIX" {
		t.Fatalf("attcollation after COLLATE change: got %q, want %q", got, "POSIX")
	}
	noDrift([]string{f})
}

// TestRoundtripDomainCollation is the live regression guard for RFC Section
// 5.4's domain COLLATE clause: real PostgreSQL grammar accepts it directly
// on CreateDomainStmt (CollClause), but ir.Type had no DomainCollation field
// before this, so it was silently dropped.
//
// Confirms live: a declared COLLATE lands in pg_type.typcollation, a second
// plan against the freshly introspected live state is a genuine no-op, and
// a COLLATE-only change (RFC's "Changing the base type or COLLATE" row —
// PostgreSQL has no ALTER DOMAIN for either) actually runs a real DROP
// DOMAIN CASCADE + CREATE DOMAIN against a live server.
func TestRoundtripDomainCollation(t *testing.T) {
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

	domainCollation := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
			SELECT co.collname
			FROM pg_type t
			JOIN pg_collation co ON co.oid = t.typcollation
			WHERE t.typname = 'd'`)
		if err != nil {
			t.Fatalf("query typcollation: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("domain d not found or has no non-default collation")
		}
		var name string
		_ = rows.Scan(&name)
		return name
	}

	noDrift := func(files []string) {
		t.Helper()
		desired, _, err := compiler.Compile(files, dir, pipeline.Default)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		base, _ := store.Load("test", "dpgtest")
		ops, err := differ.Diff(desired, base)
		if err != nil {
			t.Fatalf("diff: %v", err)
		}
		if len(ops) != 0 {
			t.Errorf("expected no-op re-diff, got %d ops:", len(ops))
			for _, op := range ops {
				t.Errorf("  [%s] %s", op.Safety(), op.SQL())
			}
		}

		ci := introspect.New()
		liveObjects, err := ci.Introspect(ctx, conn)
		if err != nil {
			t.Fatalf("introspect: %v", err)
		}
		var managedLive []pipeline.IRObject
		for _, obj := range liveObjects {
			if _, ok := base.Objects[obj.QualifiedName()]; ok {
				managedLive = append(managedLive, obj)
			}
		}
		liveSnap := &pipeline.Snapshot{}
		if err := snapshot.Populate(liveSnap, managedLive); err != nil {
			t.Fatalf("populate live snapshot: %v", err)
		}
		liveDriftOps, err := differ.Diff(desired, liveSnap)
		if err != nil {
			t.Fatalf("live drift diff: %v", err)
		}
		if len(liveDriftOps) != 0 {
			t.Errorf("expected zero drift against freshly introspected live state, got %d ops:", len(liveDriftOps))
			for _, op := range liveDriftOps {
				t.Errorf("  [%s] %s", op.Safety(), op.SQL())
			}
		}
	}

	write(`DOMAIN d AS text COLLATE "C";`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := domainCollation(); got != "C" {
		t.Fatalf("typcollation after create: got %q, want %q", got, "C")
	}
	noDrift([]string{f})

	write(`DOMAIN d AS text COLLATE "POSIX";`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := domainCollation(); got != "POSIX" {
		t.Fatalf("typcollation after COLLATE change: got %q, want %q", got, "POSIX")
	}
	noDrift([]string{f})
}
