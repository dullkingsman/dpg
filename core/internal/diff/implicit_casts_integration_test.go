//go:build integration

package diff

import (
	"context"
	"sort"
	"testing"

	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestImplicitCastsMatchesLiveCatalog re-runs the exact query
// implicit_casts.go's doc comment documents as this table's extraction
// method against a real, freshly-started PostgreSQL container, and asserts
// an EXACT match (nothing extra on either side) against implicitCastPairs.
// This is the canary this whole approach depends on: implicitCastPairs is a
// hardcoded table, but unlike a table hardcoded from memory or
// documentation, this one can never silently drift out of sync with real
// PostgreSQL without this test failing first — a future PG version adding,
// removing, or changing an implicit cast breaks this test immediately, the
// same day it would otherwise start giving a wrong CAUTION/DESTRUCTIVE
// answer, not months later when someone hits it live.
func TestImplicitCastsMatchesLiveCatalog(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.Start(t)
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	rows, err := conn.QueryRows(ctx, `
SELECT s.typname, t.typname
FROM pg_cast c
JOIN pg_type s ON s.oid = c.castsource
JOIN pg_type t ON t.oid = c.casttarget
WHERE c.castcontext = 'i' AND c.castsource != c.casttarget
ORDER BY 1, 2;
`)
	if err != nil {
		t.Fatalf("query pg_cast: %v", err)
	}
	defer rows.Close()

	var live [][2]string
	for rows.Next() {
		var from, to string
		if scanErr := rows.Scan(&from, &to); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		live = append(live, [2]string{from, to})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	sortPairs := func(pairs [][2]string) {
		sort.Slice(pairs, func(i, j int) bool {
			if pairs[i][0] != pairs[j][0] {
				return pairs[i][0] < pairs[j][0]
			}
			return pairs[i][1] < pairs[j][1]
		})
	}
	hardcoded := append([][2]string(nil), implicitCastPairs...)
	sortPairs(hardcoded)
	sortPairs(live)

	liveSet := make(map[[2]string]bool, len(live))
	for _, p := range live {
		liveSet[p] = true
	}
	hardcodedSet := make(map[[2]string]bool, len(hardcoded))
	for _, p := range hardcoded {
		hardcodedSet[p] = true
	}

	var missingFromTable, extraInTable []string
	for _, p := range live {
		if !hardcodedSet[p] {
			missingFromTable = append(missingFromTable, p[0]+" -> "+p[1])
		}
	}
	for _, p := range hardcoded {
		if !liveSet[p] {
			extraInTable = append(extraInTable, p[0]+" -> "+p[1])
		}
	}

	if len(missingFromTable) > 0 {
		t.Errorf("live PostgreSQL has %d implicit cast(s) NOT in implicitCastPairs (table is stale, re-extract from a live container): %v",
			len(missingFromTable), missingFromTable)
	}
	if len(extraInTable) > 0 {
		t.Errorf("implicitCastPairs has %d entry/entries NOT present in the live catalog (table is stale or was hand-edited incorrectly): %v",
			len(extraInTable), extraInTable)
	}
}
