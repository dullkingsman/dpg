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

// TestRoundtripBaseTypeAlterableProperty is the live regression guard for
// RFC Section 5.5's 7 in-place-alterable BASE type properties: previously
// ANY change to a BASE type (including these) forced an unconditional
// DROP+CREATE. Proves a STORAGE-only change instead runs a real
// ALTER TYPE ... SET (STORAGE = ...) and takes effect, with the type's OID
// unchanged (metadata-only, not dropped and recreated). Built via
// PostgreSQL's own documented "reuse an existing internal function"
// bootstrapping trick (same as TestRoundtripBaseType), using textin/textout
// since STORAGE other than 'plain' requires a variable-length
// (INTERNALLENGTH = VARIABLE) type.
func TestRoundtripBaseTypeAlterableProperty(t *testing.T) {
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

	v1 := `FUNCTION rt_vbase_in(cstring) RETURNS rt_vbase LANGUAGE internal AS $$textin$$;
FUNCTION rt_vbase_out(rt_vbase) RETURNS cstring LANGUAGE internal AS $$textout$$;
TYPE rt_vbase (INPUT = rt_vbase_in, OUTPUT = rt_vbase_out, INTERNALLENGTH = VARIABLE, STORAGE = plain);`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	typeOID := func() int64 {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT ('public.rt_vbase'::regtype)::oid::bigint`)
		if err != nil {
			t.Fatalf("query oid: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("rt_vbase does not exist")
		}
		var oid int64
		_ = rows.Scan(&oid)
		return oid
	}
	storageMode := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT typstorage::text FROM pg_type WHERE typname = 'rt_vbase'`)
		if err != nil {
			t.Fatalf("query typstorage: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("rt_vbase does not exist")
		}
		var s string
		_ = rows.Scan(&s)
		return s
	}

	oldOID := typeOID()
	if got := storageMode(); got != "p" {
		t.Fatalf("expected initial STORAGE plain ('p'), got %q", got)
	}

	v2 := `FUNCTION rt_vbase_in(cstring) RETURNS rt_vbase LANGUAGE internal AS $$textin$$;
FUNCTION rt_vbase_out(rt_vbase) RETURNS cstring LANGUAGE internal AS $$textout$$;
TYPE rt_vbase (INPUT = rt_vbase_in, OUTPUT = rt_vbase_out, INTERNALLENGTH = VARIABLE, STORAGE = extended);`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := storageMode(); got != "x" {
		t.Fatalf("expected STORAGE extended ('x') after the ALTER, got %q", got)
	}
	if newOID := typeOID(); newOID != oldOID {
		t.Fatalf("rt_vbase has a different OID (%d) than before (%d) — dropped and recreated instead of altered in place", newOID, oldOID)
	}
}
