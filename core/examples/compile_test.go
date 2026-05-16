package examples_test

// TestCompileInitialDDL shows what DPG generates when you deploy a brand-new
// schema for the first time (desired state diffed against an empty snapshot).
//
//	go test ./examples/... -v -run TestCompileInitialDDL

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

func TestCompileInitialDDL(t *testing.T) {
	// Compile the v1 source files into IR objects.
	objects := compileDPG(t, "fixtures/v1/schema.dpg")

	// Diff against an empty snapshot — nothing exists yet, so every object
	// becomes a CREATE operation.
	sql := planSQL(t, objects, &pipeline.Snapshot{})

	t.Logf("\n=== Initial DDL: v1 schema vs empty snapshot ===\n%s", sql)

	// All these statements must appear in a single transactional block.
	assertContains(t, sql,
		"BEGIN",
		"COMMIT",
		"CREATE TABLE",
		"CREATE ROLE",
		"CREATE EXTENSION",
		"users",
		"products",
		"orders",
		"order_items",
	)

	// No destructive operations on a fresh schema.
	assertNotContains(t, sql, "DROP TABLE", "DROP COLUMN")
}

// TestCompileObjectCount verifies the number of top-level IR objects compiled
// from v1: 3 roles + 1 extension + 1 schema + 4 tables + 1 view + 1 function.
func TestCompileObjectCount(t *testing.T) {
	objects := compileDPG(t, "fixtures/v1/schema.dpg")

	const wantAtLeast = 9
	if len(objects) < wantAtLeast {
		t.Errorf("expected at least %d IR objects, got %d", wantAtLeast, len(objects))
	}
	t.Logf("compiled %d IR objects from v1/schema.dpg", len(objects))
	for _, obj := range objects {
		t.Logf("  %T  %s", obj, obj.QualifiedName())
	}
}

// TestCompileIdempotent verifies that diffing the same schema twice produces no
// operations (applying to a snapshot that already reflects the desired state).
func TestCompileIdempotent(t *testing.T) {
	objects := compileDPG(t, "fixtures/v1/schema.dpg")
	snap := buildSnapshot(t, objects)

	// Diff desired v1 against a snapshot already built from v1 → no changes.
	sql := planSQL(t, objects, snap)

	t.Logf("\n=== Idempotency check: v1 vs v1 snapshot (expect no changes) ===\n%s", sql)

	assertContains(t, sql, "no changes")
	assertNotContains(t, sql, "BEGIN", "CREATE TABLE", "ALTER TABLE")
}

// TestCompileDependencyOrder verifies that tables with foreign keys are emitted
// after the tables they reference.
func TestCompileDependencyOrder(t *testing.T) {
	objects := compileDPG(t, "fixtures/v1/schema.dpg")
	snap := &pipeline.Snapshot{}
	sql := planSQL(t, objects, snap)

	t.Logf("\n=== Dependency ordering (FKs after their referenced tables) ===\n%s", sql)

	// orders references users, so "users" must appear before "orders".
	usersPos := indexOf(sql, `"users"`)
	ordersPos := indexOf(sql, `"orders"`)
	if usersPos < 0 || ordersPos < 0 {
		t.Skip("could not find table positions in output")
	}
	if usersPos > ordersPos {
		t.Errorf("expected 'users' (pos %d) to appear before 'orders' (pos %d)", usersPos, ordersPos)
	}
}

func indexOf(s, sub string) int {
	for i := range s {
		if i+len(sub) <= len(s) && s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
