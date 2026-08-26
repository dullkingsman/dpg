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

// TestPartitionRLSDoesNotProtectDirectAccess is executable documentation
// for Section 7.8's confirmed-live note (added alongside this test): a Row
// Level Security policy declared on a partitioned parent does not protect
// a leaf partition queried directly, and DPG does nothing — nor should it,
// this is real PostgreSQL behavior, not a DPG diffing decision — to close
// that gap automatically. This is a regression guard against ever
// accidentally believing otherwise (e.g. by adding synthetic per-partition
// policy propagation) without deliberately revisiting this documented
// PostgreSQL fact first.
//
// Confirms live: a DPG-declared partitioned table with RLS + a policy on
// the parent correctly filters queries against the parent, but a role
// granted access directly to a leaf partition sees every row when querying
// that partition by name.
func TestPartitionRLSDoesNotProtectDirectAccess(t *testing.T) {
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

	src := `TABLE orders (id INTEGER, region TEXT, amount NUMERIC) PARTITION BY LIST (region) {
    ENABLE ROW LEVEL SECURITY;

    POLICIES {
        only_us FOR SELECT USING (region = 'us');
    }

    PARTITIONS {
        orders_us FOR VALUES IN ('us');
        orders_eu FOR VALUES IN ('eu');
    }
}`
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	// Role and grants set up directly — this test is about the RLS/partition
	// interaction, not about DPG's grant-declaration syntax for partitions.
	if _, err := conn.Exec(ctx, `CREATE ROLE test_role LOGIN;`); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := conn.Exec(ctx, `GRANT SELECT ON orders, orders_us, orders_eu TO test_role;`); err != nil {
		t.Fatalf("grant select: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO orders VALUES (1, 'us', 100), (2, 'eu', 200);`); err != nil {
		t.Fatalf("insert rows: %v", err)
	}

	countAs := func(role, table string) int {
		t.Helper()
		if _, err := conn.Exec(ctx, "SET ROLE "+role+";"); err != nil {
			t.Fatalf("set role: %v", err)
		}
		rows, err := conn.QueryRows(ctx, "SELECT count(*) FROM "+table)
		if err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		var n int
		if !rows.Next() {
			rows.Close()
			t.Fatalf("no rows counting %s", table)
		}
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		rows.Close()
		if _, err := conn.Exec(ctx, "RESET ROLE;"); err != nil {
			t.Fatalf("reset role: %v", err)
		}
		return n
	}

	if n := countAs("test_role", "orders"); n != 1 {
		t.Fatalf("querying via parent: got %d rows, want 1 (RLS policy should filter to region='us')", n)
	}
	if n := countAs("test_role", "orders_eu"); n != 1 {
		t.Fatalf("querying leaf partition orders_eu directly: got %d rows, want 1 — "+
			"if this now returns 0, PostgreSQL's behavior changed and Section 7.8's "+
			"confirmed-live note needs revisiting; if DPG changed to auto-propagate "+
			"RLS onto partitions, this test's premise (and the RFC note) needs updating too", n)
	}

	// Not calling noDriftAgainstLive here: the role and grants above were
	// created directly via SQL (this test is about the RLS/partition
	// interaction, not DPG's grant management), so live introspection would
	// correctly report them as undeclared drift — that's expected, not a bug.
}
