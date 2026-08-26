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

// TestRoundtripIdentityColumnOptionsAndDiffing is the live regression guard
// for two related bugs found in the same audit pass: (1) the builder
// discarded a declared identity column's sequence options (START WITH/
// INCREMENT BY/MINVALUE/MAXVALUE/CACHE/CYCLE) entirely, silently downgrading
// every one to PostgreSQL's bare default; (2) diffColumns' "alter existing
// column" branch never read col.Identity at all, so RFC Section 7.4's whole
// 4-row identity-change table (clause added, ALWAYS<->BY DEFAULT toggle,
// identity-opts changed, clause removed) produced zero DiffOps.
//
// Walks all four transitions against a real PostgreSQL server, confirming
// via information_schema.columns' identity_* fields (not just the DPG
// snapshot) and attnum stability (proving each transition is a genuine
// in-place ALTER, not a hidden drop-and-recreate) that every one of real
// PostgreSQL's documented identity ALTER forms — ADD GENERATED, SET
// GENERATED, SET <option>, DROP IDENTITY — actually fires.
func TestRoundtripIdentityColumnOptionsAndDiffing(t *testing.T) {
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

	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
			t.Fatalf("write schema: %v", err)
		}
	}

	type idInfo struct {
		attnum     int
		generation string
		start, inc int64
		min, max   int64
		cache      int64
		cycle      bool
	}
	colInfo := func(col string) idInfo {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
			SELECT a.attnum,
			       CASE a.attidentity WHEN 'a' THEN 'ALWAYS' WHEN 'd' THEN 'BY DEFAULT' ELSE '' END,
			       coalesce(sq.seqstart, 0), coalesce(sq.seqincrement, 0),
			       coalesce(sq.seqmin, 0), coalesce(sq.seqmax, 0),
			       coalesce(sq.seqcache, 0), coalesce(sq.seqcycle, false)
			FROM pg_attribute a
			JOIN pg_class t ON t.oid = a.attrelid
			LEFT JOIN LATERAL (
			    SELECT s.seqstart, s.seqincrement, s.seqmin, s.seqmax, s.seqcache, s.seqcycle
			    FROM pg_depend dep
			    JOIN pg_sequence s ON s.seqrelid = dep.objid
			    WHERE dep.deptype = 'i'
			      AND dep.classid = 'pg_class'::regclass
			      AND dep.refclassid = 'pg_class'::regclass
			      AND dep.refobjid = a.attrelid
			      AND dep.refobjsubid = a.attnum
			) sq ON a.attidentity <> ''
			WHERE t.relname = 'orders' AND a.attname = $1`, col)
		if err != nil {
			t.Fatalf("query attribute info: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("%s column not found", col)
		}
		var info idInfo
		if err := rows.Scan(&info.attnum, &info.generation, &info.start, &info.inc, &info.min, &info.max, &info.cache, &info.cycle); err != nil {
			t.Fatalf("scan attribute info: %v", err)
		}
		return info
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

	// Stage 1: create with a fully-optioned ALWAYS identity column and a
	// plain column that will later gain identity in Stage 3.
	write(`TABLE orders (
    id BIGINT GENERATED ALWAYS AS IDENTITY (START WITH 1000 INCREMENT BY 10 MINVALUE 100 MAXVALUE 999999 CACHE 20 CYCLE),
    seq_col BIGINT NOT NULL,
    amount NUMERIC NOT NULL
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	info1 := colInfo("id")
	if info1.generation != "ALWAYS" {
		t.Fatalf("expected identity_generation = ALWAYS after create, got %q", info1.generation)
	}
	if info1.start != 1000 || info1.inc != 10 || info1.min != 100 || info1.max != 999999 || info1.cache != 20 || !info1.cycle {
		t.Fatalf("unexpected identity options after create: %+v", info1)
	}
	noDrift([]string{f})

	// Stage 2: ALWAYS -> BY DEFAULT toggle plus an options change
	// (INCREMENT BY). attnum must NOT change — real PostgreSQL's SET
	// GENERATED/SET <option> are in-place ALTERs, not a drop-and-recreate.
	write(`TABLE orders (
    id BIGINT GENERATED BY DEFAULT AS IDENTITY (START WITH 1000 INCREMENT BY 5 MINVALUE 100 MAXVALUE 999999 CACHE 20 CYCLE),
    seq_col BIGINT NOT NULL,
    amount NUMERIC NOT NULL
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	info2 := colInfo("id")
	if info2.attnum != info1.attnum {
		t.Fatalf("expected attnum to stay stable across SET GENERATED/SET <option> (no drop+recreate), got %d before and %d after", info1.attnum, info2.attnum)
	}
	if info2.generation != "BY DEFAULT" {
		t.Fatalf("expected identity_generation = BY DEFAULT after toggle, got %q", info2.generation)
	}
	if info2.inc != 5 {
		t.Fatalf("expected identity_increment = 5 after options change, got %d", info2.inc)
	}
	if info2.start != 1000 || info2.min != 100 || info2.max != 999999 || info2.cache != 20 || !info2.cycle {
		t.Fatalf("unchanged identity options must not drift: %+v", info2)
	}
	noDrift([]string{f})

	// Stage 3: "clause added where none existed" on a plain column already
	// declared NOT NULL — real PostgreSQL's ADD GENERATED requires this.
	// attnum must NOT change (genuine in-place ALTER, unlike the generated-
	// column "added" case which has no such path at all).
	seqColAttnumBefore := colInfo("seq_col").attnum
	write(`TABLE orders (
    id BIGINT GENERATED BY DEFAULT AS IDENTITY (START WITH 1000 INCREMENT BY 5 MINVALUE 100 MAXVALUE 999999 CACHE 20 CYCLE),
    seq_col BIGINT GENERATED ALWAYS AS IDENTITY (START WITH 5 INCREMENT BY 2),
    amount NUMERIC NOT NULL
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	info3 := colInfo("seq_col")
	if info3.attnum != seqColAttnumBefore {
		t.Fatalf("expected attnum to stay stable across ADD GENERATED ... AS IDENTITY, got %d before and %d after", seqColAttnumBefore, info3.attnum)
	}
	if info3.generation != "ALWAYS" || info3.start != 5 || info3.inc != 2 {
		t.Fatalf("unexpected identity state after ADD GENERATED: %+v", info3)
	}
	noDrift([]string{f})

	// Stage 4: "clause removed, column kept" — DROP IDENTITY must not
	// rewrite the column. Insert a probe row first (id is BY DEFAULT and
	// seq_col is ALWAYS, so a plain insert naming neither works) and
	// confirm its value survives.
	if _, err := conn.Exec(ctx, `INSERT INTO orders (amount) VALUES (42)`); err != nil {
		t.Fatalf("insert probe row: %v", err)
	}
	rows, err := conn.QueryRows(ctx, `SELECT seq_col FROM orders WHERE amount = 42`)
	if err != nil {
		t.Fatalf("query probe row before removal: %v", err)
	}
	if !rows.Next() {
		t.Fatal("expected probe row")
	}
	var probeVal int64
	if err := rows.Scan(&probeVal); err != nil {
		t.Fatalf("scan probe row: %v", err)
	}
	rows.Close()

	write(`TABLE orders (
    id BIGINT GENERATED BY DEFAULT AS IDENTITY (START WITH 1000 INCREMENT BY 5 MINVALUE 100 MAXVALUE 999999 CACHE 20 CYCLE),
    seq_col BIGINT NOT NULL,
    amount NUMERIC NOT NULL
) {
}`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	info4 := colInfo("seq_col")
	if info4.attnum != info3.attnum {
		t.Fatalf("expected attnum to stay stable across DROP IDENTITY, got %d before and %d after", info3.attnum, info4.attnum)
	}
	if info4.generation != "" {
		t.Fatalf("expected identity_generation = '' (plain column) after DROP IDENTITY, got %q", info4.generation)
	}
	rows2, err := conn.QueryRows(ctx, `SELECT seq_col FROM orders WHERE amount = 42`)
	if err != nil {
		t.Fatalf("query probe row after removal: %v", err)
	}
	if !rows2.Next() {
		t.Fatal("expected probe row after removal")
	}
	var afterVal int64
	if err := rows2.Scan(&afterVal); err != nil {
		t.Fatalf("scan probe row after removal: %v", err)
	}
	rows2.Close()
	if afterVal != probeVal {
		t.Errorf("DROP IDENTITY must not rewrite existing values: got %d, want %d (unchanged)", afterVal, probeVal)
	}
	noDrift([]string{f})
}
