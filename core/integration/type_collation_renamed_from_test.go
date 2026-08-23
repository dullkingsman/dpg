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

// oidByCast is a small helper shared by this file's tests: casts a
// qualified name to the given PostgreSQL OID-alias type (regtype,
// regcollation, ...) and returns its OID, or nil if it doesn't resolve.
func oidByCast(t *testing.T, ctx context.Context, conn *executor.PgxConn, castType, qualified string) *int64 {
	t.Helper()
	rows, err := conn.QueryRows(ctx, `SELECT ($1::`+castType+`)::oid::bigint`, qualified)
	if err != nil {
		t.Fatalf("query %s oid for %s: %v", castType, qualified, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil
	}
	var oid *int64
	if err := rows.Scan(&oid); err != nil {
		// A cast to a nonexistent name raises a query error in PostgreSQL
		// (not a NULL result), so this path is only reached for scan
		// failures on an otherwise-successful query; treat as "doesn't
		// resolve" the same as no rows.
		return nil
	}
	return oid
}

// TestRoundtripEnumRenamedFrom is the regression guard for Type rename
// detection: before this, ir.Type had no RenamedFrom field, so a renamed
// type was indistinguishable from "old type dropped, new one added" — a
// real DROP TYPE + CREATE TYPE pair instead of a metadata-only rename,
// which for an ENUM in use by a table column would fail outright (a type
// can't be dropped while a column depends on it) or cascade-drop the
// column with CASCADE. Uses ENUM as the representative variant since
// diffType's rename handling is shared uniformly across all 5.
func TestRoundtripEnumRenamedFrom(t *testing.T) {
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

	v1 := `TYPE mood_old AS ENUM ('sad', 'happy');`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oldOID := oidByCast(t, ctx, conn, "regtype", "public.mood_old")
	if oldOID == nil {
		t.Fatalf("public.mood_old does not exist after initial apply")
	}

	v2 := `TYPE mood AS ENUM ('sad', 'happy') {
    RENAMED FROM mood_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if oidByCast(t, ctx, conn, "regtype", "public.mood_old") != nil {
		t.Fatalf("public.mood_old still exists after rename")
	}
	renamedOID := oidByCast(t, ctx, conn, "regtype", "public.mood")
	if renamedOID == nil {
		t.Fatalf("public.mood does not exist after rename")
	}
	if *renamedOID != *oldOID {
		t.Fatalf("public.mood has a different OID (%d) than mood_old had (%d) — dropped and recreated instead of renamed", *renamedOID, *oldOID)
	}
}

// TestRoundtripDomainRenamedFrom is TestRoundtripEnumRenamedFrom's DOMAIN
// counterpart, also confirming the domain-specific ALTER DOMAIN verb is
// used live, not the generic ALTER TYPE (which real PostgreSQL rejects for
// a domain).
func TestRoundtripDomainRenamedFrom(t *testing.T) {
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

	v1 := `DOMAIN posint_old AS INTEGER;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oldOID := oidByCast(t, ctx, conn, "regtype", "public.posint_old")
	if oldOID == nil {
		t.Fatalf("public.posint_old does not exist after initial apply")
	}

	v2 := `DOMAIN posint AS INTEGER {
    RENAMED FROM posint_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if oidByCast(t, ctx, conn, "regtype", "public.posint_old") != nil {
		t.Fatalf("public.posint_old still exists after rename")
	}
	renamedOID := oidByCast(t, ctx, conn, "regtype", "public.posint")
	if renamedOID == nil {
		t.Fatalf("public.posint does not exist after rename")
	}
	if *renamedOID != *oldOID {
		t.Fatalf("public.posint has a different OID (%d) than posint_old had (%d) — dropped and recreated instead of renamed", *renamedOID, *oldOID)
	}
}

// TestRoundtripEnumRenamedFromCrossSchema is
// TestRoundtripTableRenamedFromCrossSchema's Type counterpart, live-proving
// that a cross-schema RENAMED FROM emits a real ALTER TYPE ... SET SCHEMA
// (not just a rename in place) before the ALTER TYPE ... RENAME TO.
func TestRoundtripEnumRenamedFromCrossSchema(t *testing.T) {
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

	v1 := `SCHEMA old_schema {}

TYPE old_schema.mood_old AS ENUM ('sad', 'happy');`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oldOID := oidByCast(t, ctx, conn, "regtype", "old_schema.mood_old")
	if oldOID == nil {
		t.Fatalf("old_schema.mood_old does not exist after initial apply")
	}

	v2 := `SCHEMA old_schema {}

SCHEMA new_schema {}

TYPE new_schema.mood AS ENUM ('sad', 'happy') {
    RENAMED FROM old_schema.mood_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if oidByCast(t, ctx, conn, "regtype", "old_schema.mood_old") != nil {
		t.Fatalf("old_schema.mood_old still exists — expected it to be moved/renamed away")
	}
	if oidByCast(t, ctx, conn, "regtype", "new_schema.mood_old") != nil {
		t.Fatalf("new_schema.mood_old unexpectedly exists — RENAME TO should have renamed it to mood")
	}
	movedOID := oidByCast(t, ctx, conn, "regtype", "new_schema.mood")
	if movedOID == nil {
		t.Fatalf("new_schema.mood does not exist — the cross-schema RENAMED FROM SET SCHEMA move failed")
	}
	if *movedOID != *oldOID {
		t.Fatalf("new_schema.mood has a different OID (%d) than old_schema.mood_old had (%d) — dropped and recreated instead of moved/renamed", *movedOID, *oldOID)
	}
}

// TestRoundtripCollationRenamedFrom is the Collation counterpart, live-
// verified the same way.
func TestRoundtripCollationRenamedFrom(t *testing.T) {
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

	v1 := `COLLATION case_insensitive_old (PROVIDER = icu, LOCALE = 'und-u-ks-level2', DETERMINISTIC = false);`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oldOID := oidByCast(t, ctx, conn, "regcollation", "public.case_insensitive_old")
	if oldOID == nil {
		t.Fatalf("public.case_insensitive_old does not exist after initial apply")
	}

	v2 := `COLLATION case_insensitive (PROVIDER = icu, LOCALE = 'und-u-ks-level2', DETERMINISTIC = false) {
    RENAMED FROM case_insensitive_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if oidByCast(t, ctx, conn, "regcollation", "public.case_insensitive_old") != nil {
		t.Fatalf("public.case_insensitive_old still exists after rename")
	}
	renamedOID := oidByCast(t, ctx, conn, "regcollation", "public.case_insensitive")
	if renamedOID == nil {
		t.Fatalf("public.case_insensitive does not exist after rename")
	}
	if *renamedOID != *oldOID {
		t.Fatalf("public.case_insensitive has a different OID (%d) than case_insensitive_old had (%d) — dropped and recreated instead of renamed", *renamedOID, *oldOID)
	}
}

// TestRoundtripCollationRenamedFromCrossSchema is
// TestRoundtripTableRenamedFromCrossSchema's Collation counterpart.
func TestRoundtripCollationRenamedFromCrossSchema(t *testing.T) {
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

	v1 := `SCHEMA old_schema {}

COLLATION old_schema.case_insensitive_old (PROVIDER = icu, LOCALE = 'und-u-ks-level2', DETERMINISTIC = false);`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oldOID := oidByCast(t, ctx, conn, "regcollation", "old_schema.case_insensitive_old")
	if oldOID == nil {
		t.Fatalf("old_schema.case_insensitive_old does not exist after initial apply")
	}

	v2 := `SCHEMA old_schema {}

SCHEMA new_schema {}

COLLATION new_schema.case_insensitive (PROVIDER = icu, LOCALE = 'und-u-ks-level2', DETERMINISTIC = false) {
    RENAMED FROM old_schema.case_insensitive_old;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if oidByCast(t, ctx, conn, "regcollation", "old_schema.case_insensitive_old") != nil {
		t.Fatalf("old_schema.case_insensitive_old still exists — expected it to be moved/renamed away")
	}
	if oidByCast(t, ctx, conn, "regcollation", "new_schema.case_insensitive_old") != nil {
		t.Fatalf("new_schema.case_insensitive_old unexpectedly exists — RENAME TO should have renamed it to case_insensitive")
	}
	movedOID := oidByCast(t, ctx, conn, "regcollation", "new_schema.case_insensitive")
	if movedOID == nil {
		t.Fatalf("new_schema.case_insensitive does not exist — the cross-schema RENAMED FROM SET SCHEMA move failed")
	}
	if *movedOID != *oldOID {
		t.Fatalf("new_schema.case_insensitive has a different OID (%d) than old_schema.case_insensitive_old had (%d) — dropped and recreated instead of moved/renamed", *movedOID, *oldOID)
	}
}
