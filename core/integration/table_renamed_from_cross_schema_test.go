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

// TestRoundtripTableRenamedFromCrossSchema is the regression guard for the
// generic cross-schema RENAMED FROM mechanism (RenamedFromSchema): before
// this fix, renamedFromKey assumed a RENAMED FROM name lived in the object's
// own (new) schema, so `RENAMED FROM old_schema.old_name` from a different
// schema never matched the snapshot entry at all — the differ treated the
// table as brand new (CREATE) and dropped the "orphaned" original as a
// separate op, instead of recognizing it as the same table and renaming it
// in place.
//
// This proves, against a real database, that the table is (a) matched (no
// DROP+CREATE — same OID before and after) and (b) actually renamed live via
// ALTER TABLE ... RENAME TO, referencing the table by the schema it actually
// lives in (old_schema), not the desired new one.
//
// It also documents a known, intentional limit of this fix: RENAMED FROM
// only triggers a RENAME TO (a name change); actually moving the table to
// the new schema requires a dedicated ALTER TABLE ... SET SCHEMA op, which
// this fix does not add (tracked separately in
// .dpg-notes/core-fix-order-2026-08-23.md's Phase 4, item #54). So after
// applying, the table is expected to end up at old_schema.accounts — renamed
// but not yet moved — not at new_schema.accounts.
func TestRoundtripTableRenamedFromCrossSchema(t *testing.T) {
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

TABLE old_schema.users (id INTEGER);`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	regclassOID := func(qualified string) *int64 {
		t.Helper()
		// Cast to bigint (not left as the native oid/regclass type) purely to
		// keep scanning simple and driver-agnostic.
		rows, err := conn.QueryRows(ctx, `SELECT to_regclass($1)::oid::bigint`, qualified)
		if err != nil {
			t.Fatalf("query oid for %s: %v", qualified, err)
		}
		defer rows.Close()
		if !rows.Next() {
			return nil
		}
		var oid *int64
		if err := rows.Scan(&oid); err != nil {
			t.Fatalf("scan oid for %s: %v", qualified, err)
		}
		return oid
	}

	oldOID := regclassOID("old_schema.users")
	if oldOID == nil {
		t.Fatalf("old_schema.users does not exist after initial apply")
	}

	v2 := `SCHEMA old_schema {}

SCHEMA new_schema {}

TABLE new_schema.accounts (id INTEGER) {
    RENAMED FROM old_schema.users;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if regclassOID("old_schema.users") != nil {
		t.Fatalf("old_schema.users still exists after rename — expected it to be renamed away")
	}

	// Not yet moved to new_schema (SET SCHEMA is separate, not-yet-implemented
	// work) — it should have landed at old_schema.accounts: same schema,
	// renamed in place.
	renamedOID := regclassOID("old_schema.accounts")
	if renamedOID == nil {
		t.Fatalf("old_schema.accounts does not exist after rename — the cross-schema RENAMED FROM match failed, likely a DROP+CREATE regression")
	}
	if *renamedOID != *oldOID {
		t.Fatalf("old_schema.accounts has a different OID (%d) than old_schema.users had (%d) — the table was dropped and recreated instead of renamed", *renamedOID, *oldOID)
	}

	if regclassOID("new_schema.accounts") != nil {
		t.Fatalf("new_schema.accounts unexpectedly exists — SET SCHEMA is not implemented yet; if this now passes, update this test's expectations (Phase 4, item #54 has landed)")
	}
}
