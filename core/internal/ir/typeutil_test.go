package ir_test

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/ir"
)

// ── HashFunctionBody ─────────────────────────────────────────────────────────

func TestHashFunctionBodySQLReformattingIsNoop(t *testing.T) {
	a := "SELECT   1 + 1;"
	b := "select 1+1;"
	if ir.HashFunctionBody("sql", a, "") != ir.HashFunctionBody("sql", b, "") {
		t.Errorf("cosmetically-reformatted-but-equivalent SQL bodies hashed differently: %q vs %q", a, b)
	}

	multi := "SELECT   1;\nSELECT 2;"
	multiReformatted := "select 1; select    2;"
	if ir.HashFunctionBody("sql", multi, "") != ir.HashFunctionBody("sql", multiReformatted, "") {
		t.Errorf("multi-statement SQL bodies with only whitespace/case differences hashed differently: %q vs %q", multi, multiReformatted)
	}

	// Case-insensitive language match.
	if ir.HashFunctionBody("SQL", a, "") != ir.HashFunctionBody("sql", b, "") {
		t.Errorf("language match should be case-insensitive")
	}
}

func TestHashFunctionBodySQLGenuineChangeStillDetected(t *testing.T) {
	a := "SELECT   1 + 1;"
	b := "SELECT   1 + 2;"
	if ir.HashFunctionBody("sql", a, "") == ir.HashFunctionBody("sql", b, "") {
		t.Errorf("genuinely different SQL bodies (even both reformatted) hashed equal: %q vs %q", a, b)
	}
}

func TestHashFunctionBodySQLMalformedFallsBackToRawHash(t *testing.T) {
	malformed := "this is not valid SQL at all ((("
	got := ir.HashFunctionBody("sql", malformed, "")
	want := ir.HashBody(malformed)
	if got != want {
		t.Errorf("malformed SQL body should fall back to raw-text HashBody; got %q, want %q", got, want)
	}
}

// plpgsqlShim builds a minimal, argument-accurate "CREATE FUNCTION ...
// LANGUAGE plpgsql AS $$...$$;" statement, the shape HashFunctionBody's
// plpgsql canonicalisation needs as its fullStatement argument (a bare body
// string is not enough — see HashFunctionBody's doc comment).
func plpgsqlShim(argList, returnType, body string) string {
	return "CREATE FUNCTION f(" + argList + ") RETURNS " + returnType +
		" LANGUAGE plpgsql AS $$" + body + "$$;"
}

// TestHashFunctionBodyPlpgsqlReformattingIsNoop guards the fix itself: a
// purely cosmetic reformatting (blank lines, a comment) of a plpgsql body
// must now hash equal, given an accurate fullStatement shim.
func TestHashFunctionBodyPlpgsqlReformattingIsNoop(t *testing.T) {
	a := "BEGIN\n  -- explain the thing\n  RETURN n;\nEND;"
	b := "BEGIN RETURN n; END;"
	full := func(body string) string { return plpgsqlShim("n integer", "integer", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) != ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("cosmetically-reformatted-but-equivalent plpgsql bodies hashed differently: %q vs %q", a, b)
	}
}

// TestHashFunctionBodyPlpgsqlGenuineChangeStillDetected mirrors the SQL
// genuine-change test: canonicalisation must never mask a real logic
// change, even when both sides are also reformatted.
func TestHashFunctionBodyPlpgsqlGenuineChangeStillDetected(t *testing.T) {
	a := "BEGIN\n  RETURN n + 1;\nEND;"
	b := "BEGIN RETURN n + 2; END;"
	full := func(body string) string { return plpgsqlShim("n integer", "integer", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) == ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("genuinely different plpgsql bodies (even both reformatted) hashed equal: %q vs %q", a, b)
	}
}

func TestHashFunctionBodyPlpgsqlMalformedFallsBackToRawHash(t *testing.T) {
	body := "this is not valid plpgsql at all ((("
	full := "CREATE FUNCTION f() RETURNS integer LANGUAGE plpgsql AS $$" + body + "$$;"
	got := ir.HashFunctionBody("plpgsql", body, full)
	want := ir.HashBody(body)
	if got != want {
		t.Errorf("malformed plpgsql body should fall back to raw-text HashBody; got %q, want %q", got, want)
	}
}

func TestHashFunctionBodyPlpgsqlEmptyFullStatementFallsBackToRawHash(t *testing.T) {
	body := "BEGIN RETURN 1; END;"
	got := ir.HashFunctionBody("plpgsql", body, "")
	want := ir.HashBody(body)
	if got != want {
		t.Errorf("empty fullStatement should fall back to raw-text HashBody; got %q, want %q", got, want)
	}
}

// TestHashFunctionBodyPlpgsqlOwnParameterReference is the case the whole
// design hinges on: the PL/pgSQL compiler resolves a body's own parameter
// references (assignment target "a := ...") against the shim's declared
// argument list at compile time, so canonicalisation only works when that
// list is accurate. Also proves the fallback path itself degrades
// gracefully (no panic/error) when the shim's argument list doesn't match.
func TestHashFunctionBodyPlpgsqlOwnParameterReference(t *testing.T) {
	a := "BEGIN\n  a := a + 1;\n  RETURN a;\nEND;"
	b := "BEGIN a := a + 1; RETURN a; END;"

	correctShim := func(body string) string { return plpgsqlShim("a integer", "integer", body) }
	if ir.HashFunctionBody("plpgsql", a, correctShim(a)) != ir.HashFunctionBody("plpgsql", b, correctShim(b)) {
		t.Errorf("reformatted bodies with a correct-arg-list shim should hash equal: %q vs %q", a, b)
	}

	// A shim whose argument list doesn't match the body's own reference to
	// "a" fails to compile through the real PL/pgSQL compiler; canonicalizePlpgsqlBody
	// must fall back to raw HashBody(body) rather than error or panic.
	wrongShim := plpgsqlShim("", "integer", a) // no "a" declared at all
	got := ir.HashFunctionBody("plpgsql", a, wrongShim)
	want := ir.HashBody(a)
	if got != want {
		t.Errorf("a mismatched shim should fall back to raw-text HashBody; got %q, want %q", got, want)
	}
}

// TestHashFunctionBodyPlpgsqlTriggerNewOld verifies (not just assumes) that
// a trigger function shim — zero SQL-level parameters, RETURNS trigger —
// still compiles through ParsePlPgSqlToJSON with NEW/OLD resolved, so
// reformatting a trigger body's whitespace/comments is absorbed same as any
// other plpgsql function.
func TestHashFunctionBodyPlpgsqlTriggerNewOld(t *testing.T) {
	a := "BEGIN\n  -- bump x\n  NEW.x := OLD.x + 1;\n  RETURN NEW;\nEND;"
	b := "BEGIN NEW.x := OLD.x + 1; RETURN NEW; END;"
	full := func(body string) string {
		return "CREATE FUNCTION t() RETURNS trigger LANGUAGE plpgsql AS $$" + body + "$$;"
	}
	if ir.HashFunctionBody("plpgsql", a, full(a)) != ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("reformatted trigger-function bodies (NEW/OLD) should hash equal: %q vs %q", a, b)
	}
}

// TestHashFunctionBodyPlpgsqlEmbeddedExpressionWhitespaceIsNoop is the
// motivating case for embedded-fragment canonicalisation: a whitespace-only
// change *inside* a RETURN expression (not just the outer control-flow
// shape) must now be absorbed too.
func TestHashFunctionBodyPlpgsqlEmbeddedExpressionWhitespaceIsNoop(t *testing.T) {
	a := "BEGIN RETURN v_name || '/' || v_version; END;"
	b := "BEGIN RETURN v_name||'/'||v_version; END;"
	full := func(body string) string { return plpgsqlShim("v_name text, v_version text", "text", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) != ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("whitespace-only change inside an embedded expression should hash equal: %q vs %q", a, b)
	}
}

// TestHashFunctionBodyPlpgsqlEmbeddedConditionWhitespaceIsNoop covers a
// bare condition fragment (parseMode 2), the other common embedded shape.
func TestHashFunctionBodyPlpgsqlEmbeddedConditionWhitespaceIsNoop(t *testing.T) {
	a := "BEGIN IF v_version IS NULL THEN RETURN v_name; END IF; RETURN v_name; END;"
	b := "BEGIN IF v_version   IS   NULL THEN RETURN v_name; END IF; RETURN v_name; END;"
	full := func(body string) string { return plpgsqlShim("v_name text, v_version text", "text", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) != ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("whitespace-only change inside an embedded condition should hash equal: %q vs %q", a, b)
	}
}

// TestHashFunctionBodyPlpgsqlAssignmentWhitespaceIsNoop covers an
// assignment statement (parseMode 3) — the query text libpg_query captures
// for these includes the target ("n := ..."), not just the right-hand
// side, which canonicalizePlpgsqlAssign has to split back apart.
func TestHashFunctionBodyPlpgsqlAssignmentWhitespaceIsNoop(t *testing.T) {
	a := "DECLARE n text; BEGIN n := v_name || '/' || v_version; RETURN n; END;"
	b := "DECLARE n text; BEGIN n  :=  v_name||'/'||v_version; RETURN n; END;"
	full := func(body string) string { return plpgsqlShim("v_name text, v_version text", "text", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) != ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("whitespace-only change in an assignment should hash equal: %q vs %q", a, b)
	}
}

// TestHashFunctionBodyPlpgsqlDottedAssignmentTargetWhitespaceIsNoop covers
// a two-part dotted assignment target (parseMode 4) — a record field
// assignment, the shape whose target is *not* just a plain name.
func TestHashFunctionBodyPlpgsqlDottedAssignmentTargetWhitespaceIsNoop(t *testing.T) {
	a := "DECLARE rec record; BEGIN rec.field := rec.field + 1; RETURN rec; END;"
	b := "DECLARE rec record; BEGIN rec.field  :=  rec.field+1; RETURN rec; END;"
	full := func(body string) string { return plpgsqlShim("", "record", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) != ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("whitespace-only change in a dotted assignment target should hash equal: %q vs %q", a, b)
	}
}

// TestHashFunctionBodyPlpgsqlForLoopQueryWhitespaceIsNoop covers a
// full-statement embedded fragment (parseMode 0, a FOR-loop query) — the
// path that reuses canonicalizeSQLBody, the same mechanism already used
// for LANGUAGE SQL bodies.
func TestHashFunctionBodyPlpgsqlForLoopQueryWhitespaceIsNoop(t *testing.T) {
	a := "DECLARE r record; total integer := 0; BEGIN FOR r IN SELECT * FROM t WHERE x = 1 LOOP total := total + 1; END LOOP; RETURN total; END;"
	b := "DECLARE r record; total integer := 0; BEGIN FOR r IN select   *   from   t   where   x=1 LOOP total := total + 1; END LOOP; RETURN total; END;"
	full := func(body string) string { return plpgsqlShim("", "integer", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) != ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("whitespace-only change in an embedded FOR-loop query should hash equal: %q vs %q", a, b)
	}
}

// TestHashFunctionBodyPlpgsqlEmbeddedExpressionGenuineChangeStillDetected
// is the false-negative guard for the fix above: canonicalising embedded
// fragments must never mask a real logic change inside one.
func TestHashFunctionBodyPlpgsqlEmbeddedExpressionGenuineChangeStillDetected(t *testing.T) {
	a := "BEGIN RETURN v_name || '/' || v_version; END;"
	b := "BEGIN RETURN v_name || '-' || v_version; END;"
	full := func(body string) string { return plpgsqlShim("v_name text, v_version text", "text", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) == ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("a genuine change inside an embedded expression must still be detected: %q vs %q", a, b)
	}
}

// ── Embedded-fragment coverage beyond the core cases above ─────────────────────
//
// canonicalizePlpgsqlExprFragments doesn't dispatch on statement type (IF,
// RAISE, assignment, ...) — it structurally matches any JSON node carrying
// both a "query" and a "parseMode" key, wherever it occurs. Confirmed by
// reading libpg_query's parser/pg_query_json_plpgsql.c directly: dump_expr
// (the function that writes those two keys together) is the ONLY place in
// the entire emitter that does so — grepped the whole file, one match. So
// coverage here isn't an enumeration of every plpgsql construct; it's
// spot-checking that constructs that reach the JSON output through paths
// other than the core cases above (assignment, condition, RETURN, FOR-loop
// query) still land on that same single emission shape and get
// canonicalised the same way.

func TestHashFunctionBodyPlpgsqlArraySubscriptAssignmentTargetWhitespaceIsNoop(t *testing.T) {
	a := "DECLARE arr integer[] := ARRAY[1,2,3]; BEGIN arr[1] := arr[1] + 1; RETURN arr; END;"
	b := "DECLARE arr integer[] := ARRAY[1,2,3]; BEGIN arr[1]  :=  arr[1]+1; RETURN arr; END;"
	full := func(body string) string { return plpgsqlShim("", "integer[]", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) != ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("whitespace-only change in an array-subscript assignment target should hash equal: %q vs %q", a, b)
	}
}

// TestHashFunctionBodyPlpgsqlArrayOfCompositeAssignmentTargetWhitespaceIsNoop
// covers a target with two indirection levels (subscript + field,
// "arr[1].field") — a shape distinct from the plain dotted-name case
// already covered elsewhere. Uses pg_class as the composite element type —
// a real, always-resolvable system catalog row type, available even to
// this offline parser with no live catalog connection — rather than the
// fictional "t_composite" this test used before: PostgreSQL 18 tightened
// plpgsql's type resolution to reject "array of an unresolvable composite"
// altogether ("PL/pgSQL functions cannot return type _record" / "variable
// ... has pseudo-type _record", confirmed empirically against both the pre-
// and post-upgrade parser directly), where PG17 had silently tolerated it.
func TestHashFunctionBodyPlpgsqlArrayOfCompositeAssignmentTargetWhitespaceIsNoop(t *testing.T) {
	a := "DECLARE arr pg_class[]; BEGIN arr[1].relpages := arr[1].relpages + 1; RETURN arr; END;"
	b := "DECLARE arr pg_class[]; BEGIN arr[1].relpages  :=  arr[1].relpages+1; RETURN arr; END;"
	full := func(body string) string { return plpgsqlShim("", "pg_class[]", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) != ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("whitespace-only change in an array-of-composite assignment target should hash equal: %q vs %q", a, b)
	}
}

func TestHashFunctionBodyPlpgsqlReturnQueryWhitespaceIsNoop(t *testing.T) {
	a := "BEGIN RETURN QUERY SELECT * FROM t WHERE x = 1; END;"
	b := "BEGIN RETURN QUERY select   *   from   t   where   x=1; END;"
	full := func(body string) string { return plpgsqlShim("", "SETOF t", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) != ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("whitespace-only change in a RETURN QUERY fragment should hash equal: %q vs %q", a, b)
	}
}

func TestHashFunctionBodyPlpgsqlCaseStatementWhitespaceIsNoop(t *testing.T) {
	a := "DECLARE n integer := 1; BEGIN CASE n WHEN 1 THEN RETURN 'a'; ELSE RETURN 'b'; END CASE; END;"
	b := "DECLARE n integer := 1; BEGIN CASE n WHEN 1 THEN RETURN   'a'; ELSE RETURN 'b'; END CASE; END;"
	full := func(body string) string { return plpgsqlShim("", "text", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) != ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("whitespace-only change in a CASE statement should hash equal: %q vs %q", a, b)
	}
}

func TestHashFunctionBodyPlpgsqlExceptionHandlerWhitespaceIsNoop(t *testing.T) {
	a := "BEGIN RETURN 1/0; EXCEPTION WHEN division_by_zero THEN RETURN -1; END;"
	b := "BEGIN RETURN 1 / 0; EXCEPTION WHEN division_by_zero THEN RETURN   -1; END;"
	full := func(body string) string { return plpgsqlShim("", "integer", body) }
	if ir.HashFunctionBody("plpgsql", a, full(a)) != ir.HashFunctionBody("plpgsql", b, full(b)) {
		t.Errorf("whitespace-only change in an exception handler should hash equal: %q vs %q", a, b)
	}
}

func TestHashFunctionBodyNonSQLLanguageMatchesHashBody(t *testing.T) {
	body := "BEGIN\n  RETURN 1;\nEND;"
	if got, want := ir.HashFunctionBody("c", body, ""), ir.HashBody(body); got != want {
		t.Errorf("HashFunctionBody(c, ...) = %q, want HashBody(...) = %q", got, want)
	}
}

// TestParseTypeText guards the one thing that makes OPERATOR FAMILY member
// op_types (RFC Section 14.4) comparable across source and introspection: a
// hand-written alias must normalize to the exact string format_type()/
// ::regtype::text produces, not just round-trip through pg_query
// unchanged (which is what canonicalDDL-style Parse+Deparse alone would
// give — pg_query only rewrites an explicit "pg_catalog.int4", never a
// bare "int4", since it has no catalog access to know they're the same
// type).
func TestParseTypeText(t *testing.T) {
	cases := map[string]string{
		"int4":            "integer",
		"integer":         "integer",
		"int8":            "bigint",
		"varchar(20)":     "character varying(20)",
		"numeric(10,2)":   "numeric(10,2)",
		"myschema.mytype": "myschema.mytype",
		"text":            "text",
	}
	for in, want := range cases {
		if got := ir.ParseTypeText(in).String(); got != want {
			t.Errorf("ParseTypeText(%q).String() = %q, want %q", in, got, want)
		}
	}
}

// TestParseTypeTextFallsBackOnParseFailure guards the documented
// fail-open behavior: an input ParseTypeText can't parse must never be a
// hard error (it only affects op_type comparison, showing as ordinary
// drift at worst), so it falls back to the raw, trimmed input.
func TestParseTypeTextFallsBackOnParseFailure(t *testing.T) {
	got := ir.ParseTypeText("  not a valid type ((( ").String()
	if got == "" {
		t.Error("expected a non-empty fallback, got empty string")
	}
}
