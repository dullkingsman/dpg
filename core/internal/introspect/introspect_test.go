package introspect

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

// ── stripStringLiteralCasts ───────────────────────────────────────────────────

func TestStripStringLiteralCasts(t *testing.T) {
	cases := []struct{ in, want string }{
		{"'active'::status", "'active'"},
		{"'bar'::text", "'bar'"},
		{"'x'::varchar", "'x'"},
		// No cast — unchanged.
		{"'hello'", "'hello'"},
		// Escaped single-quote inside literal.
		{"'it''s'::text", "'it''s'"},
		// Non-string cast (no surrounding quotes) is not touched.
		{"42::bigint", "42::bigint"},
		// Multiple casts in one expression.
		{"'a'::text AND 'b'::text", "'a' AND 'b'"},
		// Two-word type names are NOT stripped (regex matches single identifier only).
		{"'foo'::character varying", "'foo' varying"},
	}
	for _, tc := range cases {
		got := stripStringLiteralCasts(tc.in)
		if got != tc.want {
			t.Errorf("stripStringLiteralCasts(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ── parseIndexDef ─────────────────────────────────────────────────────────────

func TestParseIndexDef(t *testing.T) {
	cases := []struct {
		def  string
		want []pipeline.IndexColumn
	}{
		{
			"CREATE INDEX idx ON public.t USING btree (id)",
			[]pipeline.IndexColumn{{Name: "id"}},
		},
		{
			"CREATE UNIQUE INDEX idx ON public.t USING btree (email ASC NULLS LAST)",
			[]pipeline.IndexColumn{{Name: "email", SortOrder: "ASC", Nulls: "LAST"}},
		},
		{
			"CREATE INDEX idx ON public.t USING btree (a, b DESC)",
			[]pipeline.IndexColumn{{Name: "a"}, {Name: "b", SortOrder: "DESC"}},
		},
		{
			// Expression index
			"CREATE INDEX idx ON public.t USING btree (lower(email))",
			[]pipeline.IndexColumn{{Expr: &pipeline.RawExpr{Text: "lower(email)"}}},
		},
	}
	for _, tc := range cases {
		got := parseIndexDef(tc.def)
		if len(got) != len(tc.want) {
			t.Errorf("parseIndexDef(%q): got %d cols, want %d", tc.def, len(got), len(tc.want))
			continue
		}
		for i, w := range tc.want {
			g := got[i]
			if g.Name != w.Name || g.SortOrder != w.SortOrder || g.Nulls != w.Nulls {
				t.Errorf("parseIndexDef col[%d]: got {Name:%q SortOrder:%q Nulls:%q}, want {Name:%q SortOrder:%q Nulls:%q}",
					i, g.Name, g.SortOrder, g.Nulls, w.Name, w.SortOrder, w.Nulls)
			}
			if w.Expr != nil {
				if g.Expr == nil || g.Expr.Text != w.Expr.Text {
					t.Errorf("parseIndexDef col[%d]: expr got %v, want %q", i, g.Expr, w.Expr.Text)
				}
			}
		}
	}
}

func TestParseIndexDefInvalid(t *testing.T) {
	// No USING clause → nil.
	if got := parseIndexDef("CREATE INDEX idx ON t (id)"); got != nil {
		t.Errorf("expected nil for def with no USING, got %v", got)
	}
	// Empty string → nil.
	if got := parseIndexDef(""); got != nil {
		t.Errorf("expected nil for empty def, got %v", got)
	}
}

// ── parseIndexColumn ──────────────────────────────────────────────────────────

func TestParseIndexColumn(t *testing.T) {
	cases := []struct {
		in   string
		want pipeline.IndexColumn
	}{
		{"id", pipeline.IndexColumn{Name: "id"}},
		{"email DESC", pipeline.IndexColumn{Name: "email", SortOrder: "DESC"}},
		{"created_at ASC NULLS FIRST", pipeline.IndexColumn{Name: "created_at", SortOrder: "ASC", Nulls: "FIRST"}},
		{"score DESC NULLS LAST", pipeline.IndexColumn{Name: "score", SortOrder: "DESC", Nulls: "LAST"}},
	}
	for _, tc := range cases {
		got := parseIndexColumn(tc.in)
		if got.Name != tc.want.Name || got.SortOrder != tc.want.SortOrder || got.Nulls != tc.want.Nulls {
			t.Errorf("parseIndexColumn(%q) = {%q %q %q}, want {%q %q %q}",
				tc.in, got.Name, got.SortOrder, got.Nulls,
				tc.want.Name, tc.want.SortOrder, tc.want.Nulls)
		}
	}
}

// ── parsePartitionKey ─────────────────────────────────────────────────────────

func TestParsePartitionKey(t *testing.T) {
	cases := []struct {
		keyDef   string
		strategy string
		cols     []string
	}{
		{"RANGE (created_at)", "RANGE", []string{"created_at"}},
		{"LIST (region)", "LIST", []string{"region"}},
		{"HASH (user_id)", "HASH", []string{"user_id"}},
		{"RANGE (year, month)", "RANGE", []string{"year", "month"}},
	}
	for _, tc := range cases {
		got := parsePartitionKey(tc.keyDef)
		if got.Strategy != tc.strategy {
			t.Errorf("parsePartitionKey(%q).Strategy = %q, want %q", tc.keyDef, got.Strategy, tc.strategy)
		}
		if len(got.Columns) != len(tc.cols) {
			t.Errorf("parsePartitionKey(%q).Columns = %v, want %v", tc.keyDef, got.Columns, tc.cols)
			continue
		}
		for i, c := range tc.cols {
			if got.Columns[i] != c {
				t.Errorf("parsePartitionKey(%q).Columns[%d] = %q, want %q", tc.keyDef, i, got.Columns[i], c)
			}
		}
	}
}

// ── normalizeViewQuery ────────────────────────────────────────────────────────

func TestNormalizeViewQuery(t *testing.T) {
	// Casts on string literals should be stripped.
	q := "SELECT 'active'::text AS status, id FROM users"
	got := normalizeViewQuery(q)
	if got == "" {
		t.Fatal("normalizeViewQuery returned empty string")
	}
	// The cast should be gone.
	for _, bad := range []string{"::text", "::character"} {
		if containsSubstr(got, bad) {
			t.Errorf("normalizeViewQuery left cast %q in output: %q", bad, got)
		}
	}
}

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstrHelper(s, sub))
}

func containsSubstrHelper(s, sub string) bool {
	for i := range s {
		if i+len(sub) <= len(s) && s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── splitIndexColumns ─────────────────────────────────────────────────────────

func TestSplitIndexColumns(t *testing.T) {
	// Nested parens (expression with function call) must not be split on the
	// comma inside the function call.
	in := "lower(a, b), c DESC"
	cols := splitIndexColumns(in)
	if len(cols) != 2 {
		t.Fatalf("splitIndexColumns(%q): got %d cols, want 2: %v", in, len(cols), cols)
	}
	if cols[0].Expr == nil || cols[0].Expr.Text != "lower(a, b)" {
		t.Errorf("col[0]: want expr 'lower(a, b)', got %+v", cols[0])
	}
	if cols[1].Name != "c" || cols[1].SortOrder != "DESC" {
		t.Errorf("col[1]: want {Name:c SortOrder:DESC}, got %+v", cols[1])
	}
}
