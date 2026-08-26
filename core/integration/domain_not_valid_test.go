//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dpg/internal/compiler"
	"github.com/thec1oud/dpg/internal/diff"
	"github.com/thec1oud/dpg/internal/emit"
	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/pipeline"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestRoundtripDomainConstraintNotValidLifecycle is the live regression
// guard for RFC Section 5.4's NOT VALID lifecycle on domain constraints:
// Constraint.NotValid already existed from the table-level feature, but
// diffType's domain ADD CONSTRAINT branch never read it, so a domain
// constraint declared NOT VALID silently validated against every existing
// column value using the domain anyway — defeating the entire point of
// NOT VALID (skip validation now, validate later once ready).
//
// Proves live that: (1) adding a constraint NOT VALID against a column
// that already has a violating value succeeds (would fail without NOT
// VALID), (2) the constraint is genuinely marked not-valid in the catalog,
// and (3) removing NOT VALID from source emits and runs a real
// VALIDATE CONSTRAINT that fails against the still-violating existing
// value — proving it's a real validation pass, not just a no-op flag flip.
func TestRoundtripDomainConstraintNotValidLifecycle(t *testing.T) {
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

	v1 := `DOMAIN posint AS INTEGER;
TABLE t (n posint);`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, err := conn.Exec(ctx, `INSERT INTO t (n) VALUES (-5);`); err != nil {
		t.Fatalf("insert violating value: %v", err)
	}

	v2 := `DOMAIN posint AS INTEGER {
    CONSTRAINT positive_only CHECK (VALUE > 0) NOT VALID;
}
TABLE t (n posint);`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	convalidated := func() bool {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT convalidated FROM pg_constraint WHERE conname = 'positive_only'`)
		if err != nil {
			t.Fatalf("query convalidated: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("positive_only constraint does not exist")
		}
		var v bool
		_ = rows.Scan(&v)
		return v
	}
	if convalidated() {
		t.Fatalf("expected constraint to be NOT VALID (unvalidated) right after ADD, since an existing violating value is present")
	}

	v3 := `DOMAIN posint AS INTEGER {
    CONSTRAINT positive_only CHECK (VALUE > 0);
}
TABLE t (n posint);`
	if err := os.WriteFile(f, []byte(v3), 0o644); err != nil {
		t.Fatalf("write v3: %v", err)
	}

	desired, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile v3: %v", err)
	}
	base, _ := store.Load("test", "dpgtest")
	ops, err := differ.Diff(desired, base)
	if err != nil {
		t.Fatalf("diff v3: %v", err)
	}
	migration, err := emitter.Emit(ops, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit v3: %v", err)
	}
	if err := applyExec.Apply(ctx, migration, conn); err == nil {
		t.Fatalf("expected VALIDATE CONSTRAINT to fail against the still-violating existing value, but apply succeeded")
	}
	// The failed VALIDATE CONSTRAINT must not have silently marked the
	// constraint valid.
	if convalidated() {
		t.Fatalf("constraint should still be NOT VALID after a failed VALIDATE CONSTRAINT")
	}

	// Now remove the violating row and retry — VALIDATE CONSTRAINT should
	// succeed this time, proving it's a real, working validation pass.
	if _, err := conn.Exec(ctx, `DELETE FROM t WHERE n = -5;`); err != nil {
		t.Fatalf("delete violating row: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if !convalidated() {
		t.Fatalf("expected constraint to be validated after VALIDATE CONSTRAINT succeeds")
	}
}
