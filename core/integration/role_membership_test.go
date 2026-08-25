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

// TestRoundtripRoleMembership guards RFC audit item #32 (Section 11.1):
// the unified ir.Role.Memberships (replacing the old separate InRole/
// RoleMembers/AdminRoles lists) and the new block-directive form that
// carries WITH ADMIN/INHERIT/SET modifiers real CREATE ROLE's inline lists
// cannot express at all (confirmed live before implementing). Exercises
// both the bare CREATE-time inline form and the block-directive form
// together in one declaration. Proves: (a) both forms actually apply live
// and land in pg_auth_members with the correct admin_option/inherit_option/
// set_option, (b) a fresh live-introspection round trip is a genuine no-op
// — the critical proof that introspectRoleMemberships' default-suppression
// logic (comparing inherit_option against the member's own rolinherit, and
// set_option against its constant default) doesn't itself create spurious
// drift, (c) changing a declared WITH INHERIT value emits a real targeted
// re-GRANT that actually changes the live row, and (d) removing a
// membership declaration emits a real REVOKE that actually removes the
// live row.
func TestRoundtripRoleMembership(t *testing.T) {
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
	f := filepath.Join(dir, "roles.dpg")

	type memberRow struct {
		admin, inherit, set bool
	}
	rowFor := func(parent, member string) (memberRow, bool) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
SELECT am.admin_option, am.inherit_option, am.set_option
FROM   pg_auth_members am
JOIN   pg_roles p ON p.oid = am.roleid
JOIN   pg_roles m ON m.oid = am.member
WHERE  p.rolname = $1 AND m.rolname = $2`, parent, member)
		if err != nil {
			t.Fatalf("query pg_auth_members: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			return memberRow{}, false
		}
		var r memberRow
		if err := rows.Scan(&r.admin, &r.inherit, &r.set); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return r, true
	}

	v1 := `ROLE parent1 NOLOGIN;
ROLE parent2 NOLOGIN;
ROLE child1 NOLOGIN;
ROLE child2 NOLOGIN;

ROLE app_role NOLOGIN IN ROLE parent1
{
    IN ROLE parent2 WITH ADMIN OPTION WITH INHERIT FALSE;
    ROLE child1 WITH SET FALSE;
    ROLE child2;
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if r, ok := rowFor("parent1", "app_role"); !ok || r.admin {
		t.Fatalf("bare IN ROLE parent1: got %+v, ok=%v, want present with admin=false", r, ok)
	}
	if r, ok := rowFor("parent2", "app_role"); !ok || !r.admin || r.inherit {
		t.Fatalf("IN ROLE parent2 WITH ADMIN OPTION WITH INHERIT FALSE: got %+v, ok=%v, want admin=true inherit=false", r, ok)
	}
	if r, ok := rowFor("app_role", "child1"); !ok || r.set {
		t.Fatalf("ROLE child1 WITH SET FALSE: got %+v, ok=%v, want set=false", r, ok)
	}
	if _, ok := rowFor("app_role", "child2"); !ok {
		t.Fatal("bare ROLE child2: expected a live membership row")
	}

	// The critical proof: a fresh live-introspection round trip must be a
	// genuine no-op — proves introspectRoleMemberships' recorded live
	// Inherit/Set values correctly match what desired declares (parent1's
	// bare membership relies on the "not declared, don't compare"
	// convention; parent2/child1's explicit values must match exactly what
	// was just applied), not spurious drift.
	assertNoLiveDrift(t, ctx, conn, []string{f}, dir, differ, store)

	// v2: change a declared WITH INHERIT value — must re-GRANT and actually
	// change the live row.
	v2 := `ROLE parent1 NOLOGIN;
ROLE parent2 NOLOGIN;
ROLE child1 NOLOGIN;
ROLE child2 NOLOGIN;

ROLE app_role NOLOGIN IN ROLE parent1
{
    IN ROLE parent2 WITH ADMIN OPTION WITH INHERIT TRUE;
    ROLE child1 WITH SET FALSE;
    ROLE child2;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if r, ok := rowFor("parent2", "app_role"); !ok || !r.inherit {
		t.Fatalf("after WITH INHERIT TRUE change: got %+v, ok=%v, want inherit=true", r, ok)
	}
	assertNoLiveDrift(t, ctx, conn, []string{f}, dir, differ, store)

	// v3: remove the child1 membership declaration while keeping child2 —
	// the ROLE-direction bucket stays managed (child2 still declares it),
	// so this must REVOKE specifically child1's row and leave child2's
	// untouched (RFC audit item #32's own "declared, so managed" per-
	// direction scoping: a direction is only managed at all when at least
	// one entry for it is declared, matching this codebase's pre-
	// unification three-bucket behavior exactly).
	v3 := `ROLE parent1 NOLOGIN;
ROLE parent2 NOLOGIN;
ROLE child1 NOLOGIN;
ROLE child2 NOLOGIN;

ROLE app_role NOLOGIN IN ROLE parent1
{
    IN ROLE parent2 WITH ADMIN OPTION WITH INHERIT TRUE;
    ROLE child2;
}`
	if err := os.WriteFile(f, []byte(v3), 0o644); err != nil {
		t.Fatalf("write v3: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, ok := rowFor("app_role", "child1"); ok {
		t.Fatal("expected the app_role -> child1 membership to be revoked live after removing its declaration")
	}
	if _, ok := rowFor("app_role", "child2"); !ok {
		t.Fatal("child2's still-declared membership must survive child1's removal")
	}
	assertNoLiveDrift(t, ctx, conn, []string{f}, dir, differ, store)
}
