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
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripSequenceAsTypeAndOwnedBy is the regression guard for RFC
// audit item #14: sequence "AS type" and "OWNED BY" were completely
// unimplemented (no IR field, no builder handling, silently vanishing on
// every create/diff) — breaking the RFC's own canonical example
// ("SEQUENCE ... AS BIGINT ... OWNED BY orders.order_number;") verbatim.
// This proves, against a real database: the initial CREATE SEQUENCE ... AS
// ... OWNED BY ... applies correctly; an AS type change is detected and
// applies as DROP+CREATE; an OWNED BY change is detected and applies as a
// targeted ALTER SEQUENCE; and a fresh introspect pass sees both fields
// (which required widening introspectSequences' SERIAL-exclusion filter —
// a hand-declared OWNED BY sequence produces the identical pg_depend
// deptype='a' row a SERIAL column's auto-generated sequence does, so the
// prior blanket filter made every hand-declared OWNED BY sequence
// invisible to introspection too, found while implementing this fix).
func TestRoundtripSequenceAsTypeAndOwnedBy(t *testing.T) {
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

	liveAsType := func() string {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT format_type(seqtypid, NULL) FROM pg_sequence s JOIN pg_class c ON c.oid = s.seqrelid WHERE c.relname = 'order_number_seq'`)
		if err != nil {
			t.Fatalf("query pg_sequence: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("pg_sequence has no row for order_number_seq")
		}
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return v
	}
	liveOwnedBy := func() (table, col string, ok bool) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
SELECT t.relname, a.attname
FROM   pg_depend d
JOIN   pg_class s     ON s.oid = d.objid AND s.relkind = 'S'
JOIN   pg_class t     ON t.oid = d.refobjid
JOIN   pg_attribute a ON a.attrelid = t.oid AND a.attnum = d.refobjsubid
WHERE  s.relname = 'order_number_seq' AND d.deptype = 'a'`)
		if err != nil {
			t.Fatalf("query pg_depend: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			return "", "", false
		}
		if err := rows.Scan(&table, &col); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return table, col, true
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	v1 := `TABLE orders (
    order_number BIGINT NOT NULL
);

SEQUENCE order_number_seq
    AS BIGINT
    START WITH 10000
    INCREMENT BY 1
    OWNED BY orders.order_number;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if got := liveAsType(); got != "bigint" {
		t.Fatalf("order_number_seq: live AS type = %q after initial apply, want bigint — bug #14 regressed (AS type never applied)", got)
	}
	if tbl, col, ok := liveOwnedBy(); !ok || tbl != "orders" || col != "order_number" {
		t.Fatalf("order_number_seq: live OWNED BY = (%q, %q, ok=%v), want (orders, order_number, true) — bug #14 regressed (OWNED BY never applied)", tbl, col, ok)
	}

	// A fresh introspect pass must see the sequence as a real, manageable
	// object (not silently excluded as if it were SERIAL sugar).
	ci := introspect.New()
	liveObjects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var liveSeq *ir.Sequence
	for _, obj := range liveObjects {
		if seq, ok := obj.(*ir.Sequence); ok && seq.Name == "order_number_seq" {
			liveSeq = seq
		}
	}
	if liveSeq == nil {
		t.Fatal("introspect did not return order_number_seq — the OWNED BY dependency filter still excludes it")
	}
	if liveSeq.AsType == nil || liveSeq.AsType.Name != "bigint" {
		t.Fatalf("introspected order_number_seq.AsType = %v, want bigint", liveSeq.AsType)
	}
	if liveSeq.OwnedBy == nil || *liveSeq.OwnedBy != "public.orders.order_number" {
		t.Fatalf("introspected order_number_seq.OwnedBy = %v, want public.orders.order_number", liveSeq.OwnedBy)
	}

	// Change AS type: must be detected as DROP+CREATE (DESTRUCTIVE) and
	// actually apply.
	v2 := `TABLE orders (
    order_number BIGINT NOT NULL
);

SEQUENCE order_number_seq
    AS INTEGER
    START WITH 10000
    INCREMENT BY 1
    OWNED BY orders.order_number;`
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
		t.Fatalf("diff (AS type change): %v", err)
	}
	var sawDrop bool
	for _, op := range ops {
		if strings.Contains(op.SQL(), "DROP SEQUENCE") {
			sawDrop = true
			if op.Safety() != pipeline.Destructive {
				t.Errorf("expected DROP SEQUENCE for an AS type change to be Destructive, got %s", op.Safety())
			}
		}
	}
	if !sawDrop {
		t.Fatalf("expected DROP SEQUENCE for the AS type change, got: %v", opsSQL(ops))
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if got := liveAsType(); got != "integer" {
		t.Fatalf("order_number_seq: live AS type = %q after AS type change — bug #14 regressed", got)
	}

	// Change OWNED BY: must be detected as a targeted SAFE ALTER SEQUENCE
	// (no DROP), and actually apply.
	v3 := `TABLE orders (
    order_number BIGINT NOT NULL,
    legacy_number BIGINT NOT NULL
);

SEQUENCE order_number_seq
    AS INTEGER
    START WITH 10000
    INCREMENT BY 1
    OWNED BY orders.legacy_number;`
	if err := os.WriteFile(f, []byte(v3), 0o644); err != nil {
		t.Fatalf("write v3: %v", err)
	}
	desired3, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile v3: %v", err)
	}
	prevSnap3, _ := store.Load("test", "dpgtest")
	ops3, err := differ.Diff(desired3, prevSnap3)
	if err != nil {
		t.Fatalf("diff (OWNED BY change): %v", err)
	}
	var sawOwnedByAlter bool
	for _, op := range ops3 {
		if strings.Contains(op.SQL(), "DROP SEQUENCE") {
			t.Errorf("OWNED BY change should not DROP+CREATE, got: %s", op.SQL())
		}
		if strings.Contains(op.SQL(), "ALTER SEQUENCE") && strings.Contains(op.SQL(), "OWNED BY") && strings.Contains(op.SQL(), "legacy_number") {
			sawOwnedByAlter = true
			if op.Safety() != pipeline.Safe {
				t.Errorf("expected OWNED BY change to be Safe, got %s", op.Safety())
			}
		}
	}
	if !sawOwnedByAlter {
		t.Fatalf("expected ALTER SEQUENCE ... OWNED BY ... legacy_number, got: %v", opsSQL(ops3))
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)
	if tbl, col, ok := liveOwnedBy(); !ok || tbl != "orders" || col != "legacy_number" {
		t.Fatalf("order_number_seq: live OWNED BY = (%q, %q, ok=%v) after OWNED BY change, want (orders, legacy_number, true) — bug #14 regressed", tbl, col, ok)
	}

	newSnap, _ := store.Load("test", "dpgtest")
	noDriftOps, err := differ.Diff(desired3, newSnap)
	if err != nil {
		t.Fatalf("drift diff: %v", err)
	}
	if len(noDriftOps) != 0 {
		t.Errorf("expected zero drift after the OWNED BY change, got %d ops:", len(noDriftOps))
		for _, op := range noDriftOps {
			t.Errorf("  [%s] %s", op.Safety(), op.SQL())
		}
	}
}
