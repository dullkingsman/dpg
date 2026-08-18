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
	"github.com/dullkingsman/dpg/internal/introspect"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripPolicyRoleListChanged is the regression guard for RFC audit
// item #19: RLS Policy TO role-list changes were a silent no-op end-to-end.
// SnapPolicy had no Roles field, diffPolicies never compared it, and
// introspectPolicies didn't even SELECT p.polroles — so an edited policy
// silently kept its old role list on plan/apply, AND --live verify couldn't
// see the drift either (introspection never read the live roles at all).
// This proves: (a) editing a policy's TO clause is detected and actually
// reapplied against a real database, (b) the new role list is genuinely
// live on pg_policy, and (c) a fresh introspect pass afterward sees zero
// drift — the live-verify blindness half of the bug.
func TestRoundtripPolicyRoleListChanged(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()
	ci := introspect.New()
	store := newMemStore()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	base := `ROLE app_readonly NOLOGIN;
ROLE app_admin NOLOGIN;

TABLE t (id INTEGER, owner_id INTEGER) {
    POLICY p_owner FOR SELECT TO %s USING (true);
}`

	v1 := strings.Replace(base, "%s", "app_readonly", 1)
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	livePolicyRoles := func() []string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
SELECT array_agg(COALESCE(pr.rolname, 'PUBLIC') ORDER BY COALESCE(pr.rolname, 'PUBLIC'))
FROM pg_policy p
JOIN pg_class c ON c.oid = p.polrelid
LEFT JOIN LATERAL unnest(p.polroles) AS role_oid ON true
LEFT JOIN pg_roles pr ON pr.oid = role_oid
WHERE c.relname = 't' AND p.polname = 'p_owner'`)
		if err != nil {
			t.Fatalf("query pg_policy roles: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("pg_policy has no row for p_owner")
		}
		var roles []string
		if err := rows.Scan(&roles); err != nil {
			t.Fatalf("scan roles: %v", err)
		}
		return roles
	}

	if got := livePolicyRoles(); len(got) != 1 || got[0] != "app_readonly" {
		t.Fatalf("p_owner: live roles = %v after initial apply, want [app_readonly] — test setup is broken", got)
	}

	// Edit the TO clause to a different role.
	v2 := strings.Replace(base, "%s", "app_admin", 1)
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
		t.Fatalf("diff (role change): %v", err)
	}
	var sawDrop, sawCreateWithNewRole bool
	for _, op := range ops {
		sql := op.SQL()
		if strings.Contains(sql, `DROP POLICY IF EXISTS "p_owner"`) {
			sawDrop = true
		}
		if strings.Contains(sql, "CREATE POLICY") && strings.Contains(sql, "app_admin") {
			sawCreateWithNewRole = true
		}
	}
	if !sawDrop || !sawCreateWithNewRole {
		t.Fatalf("expected DROP+CREATE reflecting the new role list, got: %v", opsSQL(ops))
	}

	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := livePolicyRoles(); len(got) != 1 || got[0] != "app_admin" {
		t.Fatalf("p_owner: live roles = %v after role-list edit — bug #19 regressed (drift invisible)", got)
	}

	// The live-verify blindness half of the bug: a fresh introspect pass
	// must see the new role list too, not just the applier's own snapshot.
	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	newSnap, _ := store.Load("test", "dpgtest")
	var managedLive []pipeline.IRObject
	for _, obj := range liveObjects {
		if _, ok := newSnap.Objects[obj.QualifiedName()]; ok {
			managedLive = append(managedLive, obj)
		}
	}
	liveSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(liveSnap, managedLive); err != nil {
		t.Fatalf("populate live snapshot: %v", err)
	}
	liveDriftOps, err := differ.Diff(desired2, liveSnap)
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
