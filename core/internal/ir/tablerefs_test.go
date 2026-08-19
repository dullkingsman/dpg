package ir

import (
	"sort"
	"testing"
)

// refNames sorts and stringifies refs for order-independent comparison —
// ExtractTableRefs's doc comment explicitly makes no ordering guarantee.
func refNames(refs []TableRef) []string {
	names := make([]string, len(refs))
	for i, r := range refs {
		if r.Schema != "" {
			names[i] = r.Schema + "." + r.Name
		} else {
			names[i] = r.Name
		}
	}
	sort.Strings(names)
	return names
}

func assertRefs(t *testing.T, got []TableRef, want ...string) {
	t.Helper()
	gotNames := refNames(got)
	sort.Strings(want)
	if len(gotNames) != len(want) {
		t.Fatalf("got %v, want %v", gotNames, want)
	}
	for i := range want {
		if gotNames[i] != want[i] {
			t.Fatalf("got %v, want %v", gotNames, want)
		}
	}
}

func TestExtractTableRefs_SQLFromJoin(t *testing.T) {
	refs := ExtractTableRefs("sql", `SELECT o.id, c.name FROM orders o JOIN customers c ON c.id = o.customer_id`)
	assertRefs(t, refs, "orders", "customers")
}

func TestExtractTableRefs_SQLInsertTarget(t *testing.T) {
	refs := ExtractTableRefs("sql", `INSERT INTO audit_log (msg) SELECT 'x' FROM widgets`)
	assertRefs(t, refs, "audit_log", "widgets")
}

func TestExtractTableRefs_SQLUpdateAndDeleteTargets(t *testing.T) {
	refs := ExtractTableRefs("sql", `UPDATE accounts SET balance = balance - 1`)
	assertRefs(t, refs, "accounts")

	refs = ExtractTableRefs("sql", `DELETE FROM sessions WHERE expired`)
	assertRefs(t, refs, "sessions")
}

func TestExtractTableRefs_SQLSchemaQualified(t *testing.T) {
	refs := ExtractTableRefs("sql", `SELECT * FROM app.orders`)
	assertRefs(t, refs, "app.orders")
}

func TestExtractTableRefs_SQLSubqueryAndCTE(t *testing.T) {
	refs := ExtractTableRefs("sql", `SELECT * FROM (SELECT * FROM inner_tbl) t`)
	assertRefs(t, refs, "inner_tbl")

	// A CTE's own name resolving as a false-positive "table" reference
	// (recent, below) is a known, accepted imprecision — see
	// ExtractTableRefs's doc comment; harmless (an extra graph edge to a
	// name matching no real object simply never resolves at the call site).
	refs = ExtractTableRefs("sql", `WITH recent AS (SELECT * FROM orders) SELECT * FROM recent JOIN customers ON true`)
	assertRefs(t, refs, "recent", "orders", "customers")
}

func TestExtractTableRefs_SQLNoReference(t *testing.T) {
	refs := ExtractTableRefs("sql", `SELECT 1`)
	if len(refs) != 0 {
		t.Fatalf("expected no refs, got %v", refs)
	}
}

func TestExtractTableRefs_EmptyBody(t *testing.T) {
	if refs := ExtractTableRefs("sql", ""); refs != nil {
		t.Fatalf("expected nil for empty body, got %v", refs)
	}
	if refs := ExtractTableRefs("sql", "   "); refs != nil {
		t.Fatalf("expected nil for whitespace-only body, got %v", refs)
	}
}

func TestExtractTableRefs_UnparseableBodyIsNoop(t *testing.T) {
	if refs := ExtractTableRefs("sql", `this is not valid SQL at all ((`); refs != nil {
		t.Fatalf("expected nil for unparseable body, got %v", refs)
	}
}

func TestExtractTableRefs_NonSQLLanguageIsNoop(t *testing.T) {
	if refs := ExtractTableRefs("c", `$libdir/myext, my_c_func`); refs != nil {
		t.Fatalf("expected nil for a non-SQL language body, got %v", refs)
	}
	if refs := ExtractTableRefs("internal", `int4pl`); refs != nil {
		t.Fatalf("expected nil for LANGUAGE internal, got %v", refs)
	}
}

// TestExtractTableRefs_Plpgsql is the plpgsql-specific coverage this fix is
// mainly about: a FOR-loop query, an INSERT, an EXISTS subquery inside an IF
// condition, and a plain UPDATE — every plpgsql statement shape the fragment
// walk needs to reach, all in one function body.
func TestExtractTableRefs_Plpgsql(t *testing.T) {
	body := `CREATE FUNCTION f() RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    r record;
BEGIN
    FOR r IN SELECT * FROM widgets LOOP
        INSERT INTO widget_log (widget_id) VALUES (r.id);
    END LOOP;
    IF EXISTS (SELECT 1 FROM flags WHERE active) THEN
        UPDATE settings SET dirty = true;
    END IF;
END;
$$`
	refs := ExtractTableRefs("plpgsql", body)
	assertRefs(t, refs, "widgets", "widget_log", "flags", "settings")
}

// TestExtractTableRefs_PlpgsqlFunctionCallNotATableRef proves the walk
// doesn't over-match: a plain function call inside a plpgsql body (PERFORM,
// or an expression call) must not be reported as a table reference.
func TestExtractTableRefs_PlpgsqlFunctionCallNotATableRef(t *testing.T) {
	body := `CREATE FUNCTION f() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    PERFORM cleanup_orphans();
END;
$$`
	if refs := ExtractTableRefs("plpgsql", body); len(refs) != 0 {
		t.Fatalf("expected no table refs from a bare function call, got %v", refs)
	}
}

// TestExtractTableRefs_PlpgsqlDynamicSQLInvisible is the documented
// limitation, proven: EXECUTE with a dynamically-built string is invisible
// to a static walk, matching real PostgreSQL's own inability to validate it
// either.
func TestExtractTableRefs_PlpgsqlDynamicSQLInvisible(t *testing.T) {
	body := `CREATE FUNCTION f() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    EXECUTE 'DELETE FROM ' || quote_ident('dynamic_tbl');
END;
$$`
	refs := ExtractTableRefs("plpgsql", body)
	for _, r := range refs {
		if r.Name == "dynamic_tbl" {
			t.Fatalf("dynamic_tbl must not be detected (dynamic SQL is a documented blind spot), got %v", refs)
		}
	}
}
