package ir_test

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/ir"
)

// ── HashFunctionBody ─────────────────────────────────────────────────────────

func TestHashFunctionBodySQLReformattingIsNoop(t *testing.T) {
	a := "SELECT   1 + 1;"
	b := "select 1+1;"
	if ir.HashFunctionBody("sql", a) != ir.HashFunctionBody("sql", b) {
		t.Errorf("cosmetically-reformatted-but-equivalent SQL bodies hashed differently: %q vs %q", a, b)
	}

	multi := "SELECT   1;\nSELECT 2;"
	multiReformatted := "select 1; select    2;"
	if ir.HashFunctionBody("sql", multi) != ir.HashFunctionBody("sql", multiReformatted) {
		t.Errorf("multi-statement SQL bodies with only whitespace/case differences hashed differently: %q vs %q", multi, multiReformatted)
	}

	// Case-insensitive language match.
	if ir.HashFunctionBody("SQL", a) != ir.HashFunctionBody("sql", b) {
		t.Errorf("language match should be case-insensitive")
	}
}

func TestHashFunctionBodySQLGenuineChangeStillDetected(t *testing.T) {
	a := "SELECT   1 + 1;"
	b := "SELECT   1 + 2;"
	if ir.HashFunctionBody("sql", a) == ir.HashFunctionBody("sql", b) {
		t.Errorf("genuinely different SQL bodies (even both reformatted) hashed equal: %q vs %q", a, b)
	}
}

func TestHashFunctionBodySQLMalformedFallsBackToRawHash(t *testing.T) {
	malformed := "this is not valid SQL at all ((("
	got := ir.HashFunctionBody("sql", malformed)
	want := ir.HashBody(malformed)
	if got != want {
		t.Errorf("malformed SQL body should fall back to raw-text HashBody; got %q, want %q", got, want)
	}
}

// TestHashFunctionBodyPlpgsqlReformattingStillDetected is a deliberate
// regression guard for the documented, retained scope boundary: plpgsql is
// NOT canonicalized (see HashFunctionBody's doc comment and RFC §9.5), so a
// purely cosmetic reformatting of a plpgsql body must still hash
// differently. This test should FAIL if plpgsql canonicalization is added
// later without updating this test to match the new, intentionally
// narrower scope.
func TestHashFunctionBodyPlpgsqlReformattingStillDetected(t *testing.T) {
	// A comment-only difference: HashBody's whitespace-collapse alone can't
	// normalize this away (unlike pure whitespace/indentation, which it
	// already handles), so this specifically proves plpgsql gets no
	// pg_query-level canonicalization the way LANGUAGE SQL does.
	a := "BEGIN\n  -- explain the thing\n  RETURN 1;\nEND;"
	b := "BEGIN RETURN 1; END;"
	if ir.HashFunctionBody("plpgsql", a) == ir.HashFunctionBody("plpgsql", b) {
		t.Errorf("plpgsql bodies should NOT be canonicalized (documented scope boundary), but %q and %q hashed equal", a, b)
	}
}

func TestHashFunctionBodyNonSQLLanguageMatchesHashBody(t *testing.T) {
	body := "BEGIN\n  RETURN 1;\nEND;"
	if got, want := ir.HashFunctionBody("plpgsql", body), ir.HashBody(body); got != want {
		t.Errorf("HashFunctionBody(plpgsql, ...) = %q, want HashBody(...) = %q", got, want)
	}
	if got, want := ir.HashFunctionBody("c", body), ir.HashBody(body); got != want {
		t.Errorf("HashFunctionBody(c, ...) = %q, want HashBody(...) = %q", got, want)
	}
}
