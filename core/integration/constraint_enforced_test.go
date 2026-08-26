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
	"github.com/thec1oud/dpg/internal/introspect"
	"github.com/thec1oud/dpg/internal/pipeline"
	"github.com/thec1oud/dpg/internal/snapshot"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestRoundtripConstraintEnforced is the live regression guard for RFC
// Section 7.2/7.3's ENFORCED/NOT ENFORCED (PostgreSQL 18+, Phase 5 #38) on
// CHECK and FOREIGN KEY, plus the FOREIGN KEY deferrability-only ALTER
// CONSTRAINT path (RFC Section 7.3's "Deferrability-only changes" row,
// documented but never actually implemented anywhere in core/ before this).
//
// Walks: creating both kinds NOT ENFORCED from scratch; toggling CHECK back
// to ENFORCED (must drop-and-recreate — constraint OID changes, real
// PostgreSQL has no ALTER path for CHECK's enforceability at all); toggling
// FOREIGN KEY back to ENFORCED (must use the new targeted ALTER CONSTRAINT
// — OID stable, proving no drop-and-recreate); a FOREIGN KEY
// deferrability-only change (also ALTER CONSTRAINT, OID stable); and a
// simultaneous deferrability + ENFORCED change (still ALTER CONSTRAINT, OID
// stable) — plus an introspection no-drift check at every stage.
func TestRoundtripConstraintEnforced(t *testing.T) {
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

	type constraintState struct {
		oid                 int
		enforced, validated bool
		deferrable, deferred bool
		found               bool
	}
	queryConstraint := func(table, name string) constraintState {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
			SELECT con.oid::int, con.conenforced, con.convalidated, con.condeferrable, con.condeferred
			FROM pg_constraint con
			JOIN pg_class c ON c.oid = con.conrelid
			WHERE c.relname = $1 AND con.conname = $2`, table, name)
		if err != nil {
			t.Fatalf("query constraint state: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			return constraintState{}
		}
		var s constraintState
		if err := rows.Scan(&s.oid, &s.enforced, &s.validated, &s.deferrable, &s.deferred); err != nil {
			t.Fatalf("scan constraint state: %v", err)
		}
		s.found = true
		return s
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

	// Stage 1: create both kinds NOT ENFORCED from scratch.
	write(`TABLE orders (
    id        BIGINT NOT NULL PRIMARY KEY,
    parent_id BIGINT,
    amount    NUMERIC,
    CONSTRAINT ck_amount CHECK (amount > 0) NOT ENFORCED,
    CONSTRAINT fk_parent FOREIGN KEY (parent_id) REFERENCES orders (id) NOT ENFORCED
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	ck1 := queryConstraint("orders", "ck_amount")
	fk1 := queryConstraint("orders", "fk_parent")
	if !ck1.found || ck1.enforced || ck1.validated {
		t.Fatalf("expected ck_amount NOT ENFORCED and unvalidated, got %+v", ck1)
	}
	if !fk1.found || fk1.enforced || fk1.validated {
		t.Fatalf("expected fk_parent NOT ENFORCED and unvalidated, got %+v", fk1)
	}
	noDrift([]string{f})

	// Stage 2: CHECK back to ENFORCED — must drop-and-recreate (OID
	// changes), real PostgreSQL has no ALTER path for CHECK's
	// enforceability at all.
	write(`TABLE orders (
    id        BIGINT NOT NULL PRIMARY KEY,
    parent_id BIGINT,
    amount    NUMERIC,
    CONSTRAINT ck_amount CHECK (amount > 0),
    CONSTRAINT fk_parent FOREIGN KEY (parent_id) REFERENCES orders (id) NOT ENFORCED
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	ck2 := queryConstraint("orders", "ck_amount")
	if !ck2.found || !ck2.enforced || !ck2.validated {
		t.Fatalf("expected ck_amount ENFORCED and validated, got %+v", ck2)
	}
	if ck2.oid == ck1.oid {
		t.Errorf("expected a new OID for ck_amount after ENFORCED change (proves DROP+ADD, not an in-place ALTER), got the same OID %d", ck2.oid)
	}
	noDrift([]string{f})

	// Stage 3: FK back to ENFORCED — must use the new targeted ALTER
	// CONSTRAINT (OID stable).
	write(`TABLE orders (
    id        BIGINT NOT NULL PRIMARY KEY,
    parent_id BIGINT,
    amount    NUMERIC,
    CONSTRAINT ck_amount CHECK (amount > 0),
    CONSTRAINT fk_parent FOREIGN KEY (parent_id) REFERENCES orders (id)
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	fk3 := queryConstraint("orders", "fk_parent")
	if !fk3.found || !fk3.enforced || !fk3.validated {
		t.Fatalf("expected fk_parent ENFORCED and validated, got %+v", fk3)
	}
	if fk3.oid != fk1.oid {
		t.Errorf("expected a stable OID for fk_parent across an ENFORCED-only change (proves ALTER CONSTRAINT, not DROP+ADD), got %d before and %d after", fk1.oid, fk3.oid)
	}
	noDrift([]string{f})

	// Stage 4: FK deferrability-only change.
	write(`TABLE orders (
    id        BIGINT NOT NULL PRIMARY KEY,
    parent_id BIGINT,
    amount    NUMERIC,
    CONSTRAINT ck_amount CHECK (amount > 0),
    CONSTRAINT fk_parent FOREIGN KEY (parent_id) REFERENCES orders (id) DEFERRABLE INITIALLY DEFERRED
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	fk4 := queryConstraint("orders", "fk_parent")
	if !fk4.deferrable || !fk4.deferred {
		t.Fatalf("expected fk_parent DEFERRABLE INITIALLY DEFERRED, got %+v", fk4)
	}
	if fk4.oid != fk1.oid {
		t.Errorf("expected a stable OID for fk_parent across a deferrability-only change, got %d before and %d after", fk1.oid, fk4.oid)
	}
	noDrift([]string{f})

	// Stage 5: simultaneous deferrability + ENFORCED change — still one
	// ALTER CONSTRAINT (OID stable), not a drop-and-recreate.
	write(`TABLE orders (
    id        BIGINT NOT NULL PRIMARY KEY,
    parent_id BIGINT,
    amount    NUMERIC,
    CONSTRAINT ck_amount CHECK (amount > 0),
    CONSTRAINT fk_parent FOREIGN KEY (parent_id) REFERENCES orders (id) NOT ENFORCED
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	fk5 := queryConstraint("orders", "fk_parent")
	if fk5.enforced {
		t.Error("expected fk_parent NOT ENFORCED")
	}
	if fk5.deferrable || fk5.deferred {
		t.Errorf("expected DEFERRABLE cleared (back to NOT DEFERRABLE) alongside the ENFORCED change, got %+v", fk5)
	}
	if fk5.oid != fk1.oid {
		t.Errorf("expected a stable OID for fk_parent across a combined deferrability+ENFORCED change, got %d before and %d after", fk1.oid, fk5.oid)
	}
	noDrift([]string{f})
}
