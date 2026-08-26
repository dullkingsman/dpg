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

// TestDropConstraintOnlyRejectedOnPG17 pins down the PostgreSQL 17-vs-18
// behavior change RFC Section 7.3's "DROP CONSTRAINT ... ONLY" feature relies
// on, independent of any DPG code: "ALTER TABLE ONLY parent DROP CONSTRAINT"
// on a partitioned table with an existing partition and an inherited CHECK
// constraint is rejected outright on PostgreSQL 17 ("cannot remove
// constraint from only the partitioned table when partitions exist").
// TestRoundtripPartitionDropConstraintOnly below is the PG18 positive case.
func TestDropConstraintOnlyRejectedOnPG17(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `
		CREATE TABLE orders (
			id BIGINT NOT NULL,
			amount NUMERIC NOT NULL,
			CONSTRAINT ck_amount CHECK (amount > 0)
		) PARTITION BY RANGE (id);
		CREATE TABLE orders_1 PARTITION OF orders FOR VALUES FROM ('1') TO ('1000');
	`); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err = conn.Exec(ctx, `ALTER TABLE ONLY orders DROP CONSTRAINT ck_amount;`)
	if err == nil {
		t.Fatal("expected PostgreSQL 17 to reject ALTER TABLE ONLY ... DROP CONSTRAINT on a partitioned table with an existing partition")
	}
}

// TestRoundtripPartitionDropConstraintOnly is the live regression guard for
// RFC Section 7.3's "DROP CONSTRAINT ... ONLY" gap (PostgreSQL 18+): removing
// a constraint from a partitioned parent's own declaration while a direct
// partition independently declares a local constraint of the same name must
// emit "ALTER TABLE ONLY parent DROP CONSTRAINT" (confirmed live: succeeds
// on PostgreSQL 18, unlike 17 — see the sibling test above), leaving the
// constraint in place on the partition as a local (no-longer-inherited)
// constraint, with the parent losing it entirely. Walks: the ordinary
// inherited starting state; the ONLY-drop transition (parent loses the
// constraint, partition's copy flips from inherited to local); and a
// no-drift check at every stage, including against fresh live introspection.
func TestRoundtripPartitionDropConstraintOnly(t *testing.T) {
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

	// constraintRow returns conislocal/coninhcount for a named constraint on
	// relname, or found=false if no such row exists at all.
	constraintRow := func(relname, conname string) (local bool, inhcount int, found bool) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
			SELECT con.conislocal, con.coninhcount
			FROM pg_constraint con
			JOIN pg_class c ON c.oid = con.conrelid
			WHERE c.relname = $1 AND con.conname = $2`, relname, conname)
		if err != nil {
			t.Fatalf("query constraint row: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			return false, 0, false
		}
		if err := rows.Scan(&local, &inhcount); err != nil {
			t.Fatalf("scan constraint row: %v", err)
		}
		return local, inhcount, true
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

	// Stage 1: ordinary partitioned table with a parent-level CHECK
	// constraint that propagates to the partition as an inherited
	// (conislocal=false, coninhcount=1) row — the pre-existing, unaffected
	// case.
	write(`TABLE orders (
    id     BIGINT NOT NULL,
    amount NUMERIC NOT NULL,
    CONSTRAINT ck_amount CHECK (amount > 0)
) PARTITION BY RANGE (id) {
    PARTITIONS {
        orders_1 FOR VALUES FROM ('1') TO ('1000');
    }
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if local, inh, found := constraintRow("orders", "ck_amount"); !found || local != true || inh != 0 {
		t.Fatalf("parent ck_amount: got local=%v inhcount=%v found=%v, want local=true inhcount=0", local, inh, found)
	}
	if local, inh, found := constraintRow("orders_1", "ck_amount"); !found || local != false || inh != 1 {
		t.Fatalf("orders_1 ck_amount: got local=%v inhcount=%v found=%v, want local=false inhcount=1 (ordinary inheritance)", local, inh, found)
	}
	noDrift([]string{f})

	// Stage 2: drop ck_amount from the parent's own declaration while
	// orders_1 independently declares it locally — must emit
	// "ALTER TABLE ONLY orders DROP CONSTRAINT ck_amount" (SAFE), leaving it
	// on orders_1 as a local, no-longer-inherited constraint, and removing
	// it from the parent entirely.
	write(`TABLE orders (
    id     BIGINT NOT NULL,
    amount NUMERIC NOT NULL
) PARTITION BY RANGE (id) {
    PARTITIONS {
        orders_1 FOR VALUES FROM ('1') TO ('1000') {
            CONSTRAINT ck_amount CHECK (amount > 0);
        };
    }
}`)
	desired, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile stage 2: %v", err)
	}
	base, _ := store.Load("test", "dpgtest")
	ops, err := differ.Diff(desired, base)
	if err != nil {
		t.Fatalf("diff stage 2: %v", err)
	}
	var sawOnlyDrop bool
	for _, op := range ops {
		if op.SQL() == `ALTER TABLE ONLY "public"."orders" DROP CONSTRAINT "ck_amount";` {
			sawOnlyDrop = true
			if op.Safety() != pipeline.Safe {
				t.Errorf("ONLY drop safety = %v, want Safe", op.Safety())
			}
		}
		if op.SQL() != "" && op.Safety() == pipeline.Destructive {
			t.Errorf("expected no DESTRUCTIVE ops in this transition, got: [%s] %s", op.Safety(), op.SQL())
		}
	}
	if !sawOnlyDrop {
		t.Fatalf("expected an ALTER TABLE ONLY ... DROP CONSTRAINT op, got:")
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, _, found := constraintRow("orders", "ck_amount"); found {
		t.Error("expected ck_amount to be fully gone from the parent after ONLY drop")
	}
	if local, inh, found := constraintRow("orders_1", "ck_amount"); !found || !local || inh != 0 {
		t.Fatalf("orders_1 ck_amount after ONLY drop: got local=%v inhcount=%v found=%v, want local=true inhcount=0", local, inh, found)
	}
	noDrift([]string{f})
}
