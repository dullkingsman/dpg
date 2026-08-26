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

// TestRoundtripTSDictOptions is the live regression guard for RFC audit
// item #52: diffTSDict previously relied entirely on the raw opaque
// BodyHash for option changes. Offline, this misdetected any option-only
// edit as a full body change, forcing an unnecessary DESTRUCTIVE
// drop+recreate. Live, it detected nothing at all, since a Reconstructed
// (introspected) body skips BodyHash comparison entirely — a live-catalog-
// only option change was completely invisible.
//
// Confirms live: an option change (LANGUAGE) runs a real, targeted
// ALTER TEXT SEARCH DICTIONARY (...) (stable dictionary OID, not dropped
// and recreated), and a second plan against freshly introspected live
// state is a genuine no-op.
func TestRoundtripTSDictOptions(t *testing.T) {
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

	dictState := func() (string, int64) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `SELECT dictinitoption, oid::bigint FROM pg_ts_dict WHERE dictname = 'my_dict'`)
		if err != nil {
			t.Fatalf("query pg_ts_dict: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("my_dict does not exist")
		}
		var opts string
		var oid int64
		_ = rows.Scan(&opts, &oid)
		return opts, oid
	}

	write(`TEXT SEARCH DICTIONARY my_dict (TEMPLATE = snowball, LANGUAGE = 'english', STOPWORDS = 'english');`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	opts, oldOID := dictState()
	if opts != "language = 'english', stopwords = 'english'" {
		t.Fatalf("dictinitoption after create: got %q", opts)
	}
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)

	// Change LANGUAGE, drop STOPWORDS.
	write(`TEXT SEARCH DICTIONARY my_dict (TEMPLATE = snowball, LANGUAGE = 'french');`)
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	opts, newOID := dictState()
	if opts != "language = 'french'" {
		t.Fatalf("dictinitoption after ALTER: got %q, want %q", opts, "language = 'french'")
	}
	if newOID != oldOID {
		t.Fatalf("my_dict has a different OID (%d) than before (%d) — dropped and recreated instead of a targeted ALTER", newOID, oldOID)
	}

	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
