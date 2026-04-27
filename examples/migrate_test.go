package examples_test

// TestSchemaMigration shows DPG computing the minimal SQL needed to evolve from
// v1 to v2. DPG compares the desired v2 state against a snapshot of the already-
// applied v1 state, then emits only the delta.
//
// Changes detected (v1 → v2):
//   - SAFE:        users.phone column added
//   - SAFE:        order_items.notes column added
//   - SAFE:        coupons table added
//   - DESTRUCTIVE: products.active column dropped
//
// Run:
//
//	go test ./examples/... -v -run TestSchemaMigration

import (
	"strings"
	"testing"
)

func TestSchemaMigration(t *testing.T) {
	// Step 1: build a snapshot representing the current production state (v1).
	v1Objects := compileDPG(t, "fixtures/v1/schema.dpg")
	v1Snap := buildSnapshot(t, v1Objects)

	// Step 2: compile the desired target state (v2).
	v2Objects := compileDPG(t, "fixtures/v2/schema.dpg")

	// Step 3: compute the migration SQL (v2 desired vs v1 snapshot).
	sql := planSQL(t, v2Objects, v1Snap)

	t.Logf("\n=== Migration SQL: v1 → v2 ===\n%s", sql)

	// Safe operations go inside the transactional block.
	assertContains(t, sql, "BEGIN", "COMMIT")

	// New phone column on users (safe).
	assertContains(t, sql, "ADD COLUMN", "phone")

	// New notes column on order_items (safe).
	assertContains(t, sql, "notes")

	// New coupons table (safe).
	assertContains(t, sql, "coupons")

	// Dropped products.active column — marked DESTRUCTIVE.
	assertContains(t, sql, "DROP COLUMN")
	assertContains(t, sql, "DESTRUCTIVE")
}

// TestMigrationSafeOpsFirst verifies that SAFE ADD COLUMN statements appear
// before DESTRUCTIVE DROP COLUMN statements in the output.
func TestMigrationSafeOpsFirst(t *testing.T) {
	v1Snap := buildSnapshot(t, compileDPG(t, "fixtures/v1/schema.dpg"))
	sql := planSQL(t, compileDPG(t, "fixtures/v2/schema.dpg"), v1Snap)

	addPos := indexOf(sql, "ADD COLUMN")
	dropPos := indexOf(sql, "DROP COLUMN")

	t.Logf("ADD COLUMN at pos %d, DROP COLUMN at pos %d", addPos, dropPos)

	if addPos < 0 || dropPos < 0 {
		t.Fatalf("ADD COLUMN or DROP COLUMN not found in output:\n%s", sql)
	}
	if addPos > dropPos {
		t.Errorf("expected ADD COLUMN (safe) before DROP COLUMN (destructive)")
	}
}

// TestMigrationNoSpuriousOps verifies that unchanged objects (roles, extension,
// unchanged tables) do not appear as CREATE/DROP operations.
func TestMigrationNoSpuriousOps(t *testing.T) {
	v1Snap := buildSnapshot(t, compileDPG(t, "fixtures/v1/schema.dpg"))
	sql := planSQL(t, compileDPG(t, "fixtures/v2/schema.dpg"), v1Snap)

	t.Logf("\n=== Spurious-op check: unchanged objects should not be re-created ===\n%s", sql)

	// The migration should not recreate unchanged objects.
	if strings.Contains(sql, "CREATE ROLE") {
		t.Error("unexpected CREATE ROLE — roles did not change and should be a no-op")
	}
	if strings.Contains(sql, "CREATE EXTENSION") {
		t.Error("unexpected CREATE EXTENSION — extension did not change")
	}
}

// TestV2Idempotent verifies that running the migration a second time (v2 vs v2
// snapshot) produces no operations.
func TestV2Idempotent(t *testing.T) {
	v2Objects := compileDPG(t, "fixtures/v2/schema.dpg")
	v2Snap := buildSnapshot(t, v2Objects)

	sql := planSQL(t, v2Objects, v2Snap)

	t.Logf("\n=== Idempotency check: v2 vs v2 snapshot ===\n%s", sql)
	assertContains(t, sql, "no changes")
}
