//go:build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thec1oud/dpg/internal/compiler"
	"github.com/thec1oud/dpg/internal/diff"
	"github.com/thec1oud/dpg/internal/emit"
	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/format"
	"github.com/thec1oud/dpg/internal/introspect"
	"github.com/thec1oud/dpg/internal/ir"
	"github.com/thec1oud/dpg/internal/pipeline"
	"github.com/thec1oud/dpg/internal/snapshot"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestDumpRoleMembershipRoleDirectionInheritCompaction is the live
// regression guard for the remaining half of RFC audit item #32's
// dump-rendering gap (`.dpg-notes/core-fix-order-2026-08-23.md`'s item #5):
// a ROLE-direction membership's WITH INHERIT clause was never compacted
// away even when it matched the target (grantee) role's own real
// rolinherit default — unlike the IN_ROLE-direction case, which already
// could (o.Inherit is the rendering role's own attribute, directly
// available). Compacting the ROLE-direction case needs a cross-role
// lookup runDump now builds (roleInheritDefaults, from every introspected
// role in the same dump run) and renderObjectDPGWithRoleInheritDefaults
// now accepts.
//
// Against a real database: role_bravo (default INHERIT) and role_alpha
// (NOINHERIT) are both granted membership in parent_role, each with an
// explicit WITH INHERIT clause — role_alpha's matches its own default,
// role_bravo's does not (a real declared override). Proves dump's output
// compacts the matching one to a bare "ROLE role_alpha;" while keeping the
// mismatched one explicit, and that the compacted form still recompiles to
// a genuine no-op against live state (boolPtrDiffers' nil-desired-never-
// differs rule, confirmed via diffRoleMembership).
func TestDumpRoleMembershipRoleDirectionInheritCompaction(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()
	ci := introspect.New()

	schema := `
ROLE role_alpha NOINHERIT;
ROLE role_bravo;
ROLE parent_role {
    ROLE role_alpha WITH INHERIT FALSE;
    ROLE role_bravo WITH INHERIT FALSE;
}
`
	dir := t.TempDir()
	schemaFile := filepath.Join(dir, "schema.dpg")
	if err := os.WriteFile(schemaFile, []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	desired, _, err := compiler.Compile([]string{schemaFile}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ops, err := differ.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatalf("diff (initial): %v", err)
	}
	migration, err := emitter.Emit(ops, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if err := applyExec.Apply(ctx, migration, conn); err != nil {
		t.Fatalf("apply: %v", err)
	}

	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	roleInheritDefaults := map[string]bool{}
	var parent *ir.Role
	for _, obj := range liveObjects {
		r, ok := obj.(*ir.Role)
		if !ok {
			continue
		}
		if r.Inherit != nil {
			roleInheritDefaults[r.Name] = *r.Inherit
		}
		if r.Name == "parent_role" {
			parent = r
		}
	}
	if parent == nil {
		t.Fatal("introspection did not return parent_role")
	}
	if !roleInheritDefaults["role_bravo"] {
		t.Fatalf("expected role_bravo's live rolinherit default to be true, got %v", roleInheritDefaults["role_bravo"])
	}
	if roleInheritDefaults["role_alpha"] {
		t.Fatalf("expected role_alpha's live rolinherit default to be false (NOINHERIT), got %v", roleInheritDefaults["role_alpha"])
	}

	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	var b strings.Builder
	renderObjectDPGWithRoleInheritDefaults(&b, parent, fmtOpts, roleInheritDefaults)
	rendered := b.String()

	for line := range strings.SplitSeq(rendered, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "role_alpha") && strings.Contains(trimmed, "WITH INHERIT") {
			t.Errorf("role_alpha: WITH INHERIT should be compacted away (matches its own NOINHERIT default), got line: %q", trimmed)
		}
	}
	var sawExplicitOverride bool
	for line := range strings.SplitSeq(rendered, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "role_bravo") && strings.Contains(trimmed, "WITH INHERIT FALSE") {
			sawExplicitOverride = true
		}
	}
	if !sawExplicitOverride {
		t.Fatalf("role_bravo: expected an explicit WITH INHERIT FALSE (a real declared override against its true default), got: %s", rendered)
	}

	dumpDir := t.TempDir()
	dumpFile := filepath.Join(dumpDir, "roles.dpg")
	if err := os.WriteFile(dumpFile, []byte(rendered), 0o644); err != nil {
		t.Fatalf("write dumped source: %v", err)
	}
	redesired, _, err := compiler.Compile([]string{dumpFile}, dumpDir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped source failed to recompile: %v\n---\n%s", err, rendered)
	}

	var managedLive []pipeline.IRObject
	for _, obj := range liveObjects {
		if obj.QualifiedName() == parent.QualifiedName() {
			managedLive = append(managedLive, obj)
		}
	}
	liveSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(liveSnap, managedLive); err != nil {
		t.Fatalf("populate live snapshot: %v", err)
	}

	driftOps, err := differ.Diff(redesired, liveSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(driftOps) != 0 {
		t.Errorf("drift between dumped-and-recompiled source and live catalog (%d ops) — the compacted bare ROLE role_alpha entry must never trigger a re-GRANT:", len(driftOps))
		for _, op := range driftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}
}
