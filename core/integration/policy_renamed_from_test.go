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

// TestRoundtripPolicyRenamedFrom is the live regression guard for RFC
// Section 7.8's policy RENAMED FROM clause: ir.Policy had no such field at
// all before this, despite RFC E.19 documenting it as already closed, so a
// renamed policy misdiffed as a drop of the old name plus a create of the
// new one instead of real PostgreSQL's atomic, metadata-only ALTER POLICY
// ... RENAME TO — losing the "no window with zero active policy" safety
// property #77 established for other policy edits.
//
// Confirms live: renaming a policy runs a real ALTER POLICY ... RENAME TO
// (stable policy OID across the rename, not dropped and recreated), and a
// second plan against freshly introspected live state is a genuine no-op.
func TestRoundtripPolicyRenamedFrom(t *testing.T) {
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

	policyOID := func(name string) (bool, int64) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
SELECT p.oid::bigint FROM pg_policy p
JOIN pg_class c ON c.oid = p.polrelid
WHERE c.relname = 't' AND p.polname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_policy for %s: %v", name, err)
		}
		defer rows.Close()
		if !rows.Next() {
			return false, 0
		}
		var oid int64
		if err := rows.Scan(&oid); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return true, oid
	}

	v1 := `TABLE t (id INTEGER) {
    POLICY view_own FOR SELECT USING (true);
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	exists, oldOID := policyOID("view_own")
	if !exists {
		t.Fatal("view_own does not exist after initial apply")
	}

	v2 := `TABLE t (id INTEGER) {
    POLICY view_self FOR SELECT USING (true) RENAMED FROM view_own;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if exists, _ := policyOID("view_own"); exists {
		t.Fatal("view_own still exists after rename")
	}
	exists, newOID := policyOID("view_self")
	if !exists {
		t.Fatal("view_self does not exist after rename")
	}
	if newOID != oldOID {
		t.Fatalf("view_self has a different OID (%d) than view_own had (%d) — dropped and recreated instead of a targeted ALTER POLICY RENAME TO", newOID, oldOID)
	}

	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
