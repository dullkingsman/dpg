//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
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

// TestRoundtripTableLevelNotNullConstraint is the live regression guard for
// RFC Section 7.3's table-level named NOT NULL constraint (PostgreSQL 18+,
// Phase 5 #37/#113/#114): CONSTRAINT name NOT NULL col [NO INHERIT] [NOT
// VALID], its NOT VALID/VALIDATE CONSTRAINT lifecycle, and its own ALTER
// CONSTRAINT [NO] INHERIT targeted change.
//
// Walks: an ordinary bare inline NOT NULL collapsing back to Column.NotNull
// on introspection (no spurious promoted constraint for the common case);
// promoting to a named NO INHERIT constraint (ADD CONSTRAINT performs the
// "make not null" transition itself, no separate SET NOT NULL); an
// INHERIT-only change via the new targeted ALTER CONSTRAINT (constraint OID
// stable, proving no drop-and-recreate); a NOT VALID constraint added from
// scratch and later validated (VALIDATE CONSTRAINT, reusing CHECK/FK's
// existing lifecycle); and removing NOT NULL entirely via DROP NOT NULL
// alone (no separate, conflicting DROP CONSTRAINT) — plus an introspection
// no-drift check at every stage.
func TestRoundtripTableLevelNotNullConstraint(t *testing.T) {
	connStr := testpg.Start18(t)
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

	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
			t.Fatalf("write schema: %v", err)
		}
	}

	constraintInfo := func() (name string, noInherit, validated bool, found bool) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
			SELECT con.conname, con.connoinherit, con.convalidated
			FROM pg_constraint con
			JOIN pg_class c ON c.oid = con.conrelid
			JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = con.conkey[1]
			WHERE c.relname = 'widgets' AND con.contype = 'n' AND a.attname = 'sku'`)
		if err != nil {
			t.Fatalf("query constraint info: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			return "", false, false, false
		}
		if err := rows.Scan(&name, &noInherit, &validated); err != nil {
			t.Fatalf("scan constraint info: %v", err)
		}
		return name, noInherit, validated, true
	}

	constraintOID := func() (oid int, found bool) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
			SELECT con.oid
			FROM pg_constraint con
			JOIN pg_class c ON c.oid = con.conrelid
			JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = con.conkey[1]
			WHERE c.relname = 'widgets' AND con.contype = 'n' AND a.attname = 'sku'`)
		if err != nil {
			t.Fatalf("query constraint oid: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			return 0, false
		}
		_ = rows.Scan(&oid)
		return oid, true
	}

	noDrift := func(files []string) {
		t.Helper()
		desired, _, err := compiler.Compile(files, dir, pipeline.Default)
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		base, _ := store.Load("test", "dpgtest")
		ops, err := differ.Diff(desired, base)
		if err != nil {
			t.Fatalf("diff: %v", err)
		}
		if len(ops) != 0 {
			t.Errorf("expected no-op re-diff, got %d ops:", len(ops))
			for _, op := range ops {
				t.Errorf("  [%s] %s", op.Safety(), op.SQL())
			}
		}

		ci := introspect.New()
		liveObjects, err := ci.Introspect(ctx, conn)
		if err != nil {
			t.Fatalf("introspect: %v", err)
		}
		var managedLive []pipeline.IRObject
		for _, obj := range liveObjects {
			if _, ok := base.Objects[obj.QualifiedName()]; ok {
				managedLive = append(managedLive, obj)
			}
		}
		liveSnap := &pipeline.Snapshot{}
		if err := snapshot.Populate(liveSnap, managedLive); err != nil {
			t.Fatalf("populate live snapshot: %v", err)
		}
		liveDriftOps, err := differ.Diff(desired, liveSnap)
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

	// Stage 1: ordinary bare inline NOT NULL — no CONSTRAINT keyword, no
	// NO INHERIT. PostgreSQL 18 still catalogues a real contype='n' row
	// (auto-named "widgets_sku_not_null") but introspection must collapse
	// it back into Column.NotNull, not surface a spurious promoted
	// Constraint the source never declared.
	write(`TABLE widgets (
    id  BIGINT NOT NULL,
    sku TEXT NOT NULL
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	name1, noInherit1, validated1, found1 := constraintInfo()
	if !found1 {
		t.Fatal("expected a contype='n' row for sku")
	}
	if name1 != "widgets_sku_not_null" {
		t.Errorf("expected PostgreSQL's own auto-generated name, got %q", name1)
	}
	if noInherit1 || !validated1 {
		t.Errorf("expected ordinary state (not NO INHERIT, validated), got noInherit=%v validated=%v", noInherit1, validated1)
	}
	noDrift([]string{f})

	// Stage 2: promote to a named, NO INHERIT constraint. Confirmed live
	// (see internal/diff's own doc comments) that ADD CONSTRAINT performs
	// the "make not null" transition itself — diffColumns must not also
	// emit a bare SET NOT NULL for the same column in the same run.
	write(`TABLE widgets (
    id  BIGINT NOT NULL,
    sku TEXT,
    CONSTRAINT named_nn NOT NULL sku NO INHERIT
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	name2, noInherit2, validated2, found2 := constraintInfo()
	if !found2 {
		t.Fatal("expected a contype='n' row for sku after promotion")
	}
	if name2 != "named_nn" {
		t.Errorf("expected the explicit name to survive, got %q", name2)
	}
	if !noInherit2 || !validated2 {
		t.Errorf("expected NO INHERIT + validated, got noInherit=%v validated=%v", noInherit2, validated2)
	}
	noDrift([]string{f})

	// Stage 3: INHERIT-only change (drop NO INHERIT, keep everything else).
	// Must use the new targeted ALTER CONSTRAINT — the constraint's OID
	// staying stable proves it wasn't dropped and recreated.
	oidBefore, _ := constraintOID()
	write(`TABLE widgets (
    id  BIGINT NOT NULL,
    sku TEXT,
    CONSTRAINT named_nn NOT NULL sku
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	name3, noInherit3, _, found3 := constraintInfo()
	oidAfter, _ := constraintOID()
	if !found3 || name3 != "named_nn" {
		t.Fatalf("expected named_nn to still exist under the same name, got name=%q found=%v", name3, found3)
	}
	if noInherit3 {
		t.Error("expected NO INHERIT to be cleared")
	}
	if oidBefore != oidAfter {
		t.Errorf("expected stable constraint OID across an INHERIT-only change (proves ALTER CONSTRAINT, not DROP+ADD), got %d before and %d after", oidBefore, oidAfter)
	}
	noDrift([]string{f})

	// Stage 4: drop the constraint entirely (NOT NULL removed from source,
	// column kept) — must use DROP NOT NULL alone, never also a separate
	// DROP CONSTRAINT for the same now-gone name.
	write(`TABLE widgets (
    id  BIGINT NOT NULL,
    sku TEXT
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, _, _, found4 := constraintInfo(); found4 {
		t.Error("expected no contype='n' row for sku after NOT NULL was removed")
	}
	noDrift([]string{f})

	// Stage 5: a NOT VALID constraint added from scratch on an
	// already-nullable column, then validated — #114's core scenario,
	// reusing CHECK/FK's existing NOT VALID/VALIDATE CONSTRAINT lifecycle
	// (validateConstraintOp is type-agnostic; the only new code for this
	// stage is Constraint promotion/plumbing, not the lifecycle itself).
	if _, err := conn.Exec(ctx, `UPDATE widgets SET sku = 'PLACEHOLDER' WHERE sku IS NULL`); err != nil {
		t.Fatalf("backfill probe data: %v", err)
	}
	write(`TABLE widgets (
    id  BIGINT NOT NULL,
    sku TEXT
) {
    CONSTRAINT named_nn NOT NULL sku NOT VALID;
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	name5, _, validated5, found5 := constraintInfo()
	if !found5 || name5 != "named_nn" {
		t.Fatalf("expected named_nn to exist as NOT VALID, got name=%q found=%v", name5, found5)
	}
	if validated5 {
		t.Error("expected convalidated = false immediately after ADD CONSTRAINT ... NOT VALID")
	}
	noDrift([]string{f})

	write(`TABLE widgets (
    id  BIGINT NOT NULL,
    sku TEXT,
    CONSTRAINT named_nn NOT NULL sku
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	_, _, validated6, found6 := constraintInfo()
	if !found6 {
		t.Fatal("expected named_nn to still exist after validation")
	}
	if !validated6 {
		t.Error("expected convalidated = true after removing NOT VALID from source (VALIDATE CONSTRAINT)")
	}
	noDrift([]string{f})
}
