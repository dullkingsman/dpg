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

// TestRoundtripCollationFrom is the live regression guard for RFC Section
// 14.2's "FROM existing_collation" copy-from shorthand: real PostgreSQL
// grammar (opaque passthrough, no DPG-specific parsing), but a FROM-
// declared ir.Collation's structured Provider/Collate/Ctype/ICULocale/
// Deterministic/Rules fields were all just hardcoded Go zero-value
// defaults (Provider "c", Deterministic true) — diffCollation compared
// those defaults against introspection's real resolved values every single
// plan, which would falsely differ for any base collation not already
// using libc/deterministic defaults, producing a permanent spurious
// DROP+CREATE on an already-applied FROM-declared collation.
//
// Uses an ICU-provider, non-deterministic base collation specifically —
// the pre-fix bug would have fired immediately against it (Provider "i" !=
// the hardcoded "c" default, Deterministic false != the hardcoded true
// default). Confirms live: the alias resolves the SAME properties as its
// base (proving FROM's own CREATE-time emission, already correct before
// this fix since Body is the deparser's own verbatim reconstruction), and
// a second plan against freshly introspected live state is a genuine
// no-op — the actual bug this item fixes.
func TestRoundtripCollationFrom(t *testing.T) {
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

	src := `COLLATION case_insensitive (
    PROVIDER      = icu,
    LOCALE        = 'und-u-ks-level2',
    DETERMINISTIC = false
);
COLLATION case_insensitive_alias FROM case_insensitive;`
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	collationProps := func(name string) (provider string, locale *string, deterministic bool) {
		t.Helper()
		rows, err := conn.QueryRows(ctx, `
			SELECT collprovider::text, colllocale, collisdeterministic
			FROM pg_collation WHERE collname = $1`, name)
		if err != nil {
			t.Fatalf("query pg_collation: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatalf("pg_collation has no row for %s", name)
		}
		if err := rows.Scan(&provider, &locale, &deterministic); err != nil {
			t.Fatalf("scan: %v", err)
		}
		return provider, locale, deterministic
	}

	baseProvider, baseLocale, baseDet := collationProps("case_insensitive")
	aliasProvider, aliasLocale, aliasDet := collationProps("case_insensitive_alias")
	if aliasProvider != baseProvider {
		t.Errorf("alias provider: got %q, want %q (copied from base)", aliasProvider, baseProvider)
	}
	if aliasDet != baseDet {
		t.Errorf("alias deterministic: got %v, want %v (copied from base)", aliasDet, baseDet)
	}
	if baseLocale == nil || aliasLocale == nil || *baseLocale != *aliasLocale {
		t.Errorf("alias locale: got %v, want %v (copied from base)", aliasLocale, baseLocale)
	}
	if baseProvider != "i" || baseDet {
		t.Fatalf("test setup broken: base collation isn't ICU/non-deterministic (provider=%q det=%v) — the regression this test guards wouldn't be exercised", baseProvider, baseDet)
	}

	// The actual bug: without the fix, this second plan would propose
	// dropping and recreating case_insensitive_alias, since its desired-
	// side Provider/Deterministic defaults ("c"/true) never match the
	// ICU/non-deterministic values just confirmed above.
	noDriftAgainstLive(t, ctx, conn, []string{f}, dir, differ, store)
}
