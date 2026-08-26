//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dpg/internal/diff"
	"github.com/thec1oud/dpg/internal/emit"
	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestRoundtripMaterializedViewRenamedFromCrossSchema is diffTable's
// TestRoundtripTableRenamedFromCrossSchema counterpart for a materialized
// view, live-proving both that ALTER MATERIALIZED VIEW (not the ALTER VIEW
// real PostgreSQL rejects for a matview relkind) is used, and that SET
// SCHEMA actually moves the object to the new schema rather than just
// renaming it in place.
func TestRoundtripMaterializedViewRenamedFromCrossSchema(t *testing.T) {
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

	regclassOID := func(qualified string) *int64 {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT to_regclass($1)::oid::bigint`, qualified)
		if err != nil {
			t.Fatalf("query oid for %s: %v", qualified, err)
		}
		defer rows.Close()
		if !rows.Next() {
			return nil
		}
		var oid *int64
		if err := rows.Scan(&oid); err != nil {
			t.Fatalf("scan oid for %s: %v", qualified, err)
		}
		return oid
	}

	v1 := `SCHEMA old_schema {}

MATERIALIZED VIEW old_schema.orders_summary AS SELECT 1 AS total;`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	oldOID := regclassOID("old_schema.orders_summary")
	if oldOID == nil {
		t.Fatalf("old_schema.orders_summary does not exist after initial apply")
	}

	v2 := `SCHEMA old_schema {}

SCHEMA new_schema {}

MATERIALIZED VIEW new_schema.order_summary AS SELECT 1 AS total {
    RENAMED FROM old_schema.orders_summary;
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if regclassOID("old_schema.orders_summary") != nil {
		t.Fatalf("old_schema.orders_summary still exists — expected it to be moved/renamed away")
	}
	if regclassOID("new_schema.orders_summary") != nil {
		t.Fatalf("new_schema.orders_summary unexpectedly exists — RENAME TO should have renamed it to order_summary")
	}

	movedOID := regclassOID("new_schema.order_summary")
	if movedOID == nil {
		t.Fatalf("new_schema.order_summary does not exist — the cross-schema RENAMED FROM SET SCHEMA move failed")
	}
	if *movedOID != *oldOID {
		t.Fatalf("new_schema.order_summary has a different OID (%d) than old_schema.orders_summary had (%d) — dropped and recreated instead of moved/renamed", *movedOID, *oldOID)
	}
}
