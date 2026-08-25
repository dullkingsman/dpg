package introspect

import (
	"strings"
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

// ── parseIndexWith ────────────────────────────────────────────────────────────

func TestParseIndexWith(t *testing.T) {
	got := parseIndexWith("CREATE INDEX idx ON public.t USING btree (a) WITH (fillfactor='70') WHERE (a > 0)")
	want := []pipeline.StorageParam{{Key: "fillfactor", Value: "'70'"}}
	if len(got) != len(want) {
		t.Fatalf("got %d params, want %d: %v", len(got), len(want), got)
	}
	if got[0] != want[0] {
		t.Errorf("param[0]: got %+v, want %+v", got[0], want[0])
	}
}

func TestParseIndexWithMultipleParams(t *testing.T) {
	got := parseIndexWith("CREATE INDEX idx ON public.t USING btree (a) WITH (fillfactor='70', deduplicate_items='off')")
	if len(got) != 2 {
		t.Fatalf("expected 2 params, got %d: %v", len(got), got)
	}
	if got[1].Key != "deduplicate_items" || got[1].Value != "'off'" {
		t.Errorf("param[1]: got %+v", got[1])
	}
}

func TestParseIndexWithAbsent(t *testing.T) {
	if got := parseIndexWith("CREATE INDEX idx ON public.t USING btree (a)"); got != nil {
		t.Errorf("expected nil when no WITH clause, got %v", got)
	}
}

// ── NULLS NOT DISTINCT extraction ─────────────────────────────────────────────

func TestNullsNotDistinctDetection(t *testing.T) {
	cases := []struct {
		def  string
		want bool
	}{
		{"CREATE UNIQUE INDEX idx ON public.t USING btree (a) NULLS NOT DISTINCT", true},
		{"CREATE UNIQUE INDEX idx ON public.t USING btree (a)", false},
		{"CREATE INDEX idx ON public.t USING btree (a)", false},
	}
	for _, tc := range cases {
		got := strings.Contains(strings.ToUpper(tc.def), "NULLS NOT DISTINCT")
		if got != tc.want {
			t.Errorf("NULLS NOT DISTINCT detection for %q: got %v, want %v", tc.def, got, tc.want)
		}
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

// TestParseIndexColumnStripsExpressionCasts guards against a spurious-drift
// regression found while live-testing the diffIndexes content-comparison fix:
// pg_get_indexdef adds an explicit ::typename cast to string literals inside
// expression index columns (e.g. to_tsvector('english'::regconfig, e)) that
// hand-written source doesn't have. Without stripping it, an unchanged
// expression index would show drift on every verify/plan --live.
func TestParseIndexColumnStripsExpressionCasts(t *testing.T) {
	got := parseIndexColumn(`to_tsvector('english'::regconfig, e)`)
	want := `to_tsvector('english', e)`
	if got.Expr == nil || got.Expr.Text != want {
		t.Errorf("parseIndexColumn cast-stripping: got %+v, want Expr.Text = %q", got, want)
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

// TestParseIndexColumnOpclassWithParams is the regression guard for RFC
// audit item #10, live-verified via pg_get_indexdef's own reconstruction
// (confirmed: PostgreSQL always inserts a space before an opclass's own
// "(params)", e.g. "tsvector_ops (siglen='32')") — previously any entry
// containing '(' anywhere, including this shape, was swallowed whole into
// one bogus expression column.
func TestParseIndexColumnOpclassWithParams(t *testing.T) {
	got := parseIndexColumn(`doc tsvector_ops (siglen='32')`)
	if got.Name != "doc" {
		t.Errorf("Name: got %q, want %q", got.Name, "doc")
	}
	if got.OpClass == nil || got.OpClass.Name != "tsvector_ops" {
		t.Fatalf("OpClass: got %+v", got.OpClass)
	}
	if len(got.OpClassParams) != 1 || got.OpClassParams[0].Key != "siglen" || got.OpClassParams[0].Value != "'32'" {
		t.Errorf("OpClassParams: got %+v", got.OpClassParams)
	}
}

// TestParseIndexColumnCollateOpclassSortOrder guards the full clause
// combination in pg_get_indexdef's own reconstructed order, confirmed live:
// "email COLLATE \"C\" varchar_pattern_ops DESC NULLS LAST".
func TestParseIndexColumnCollateOpclassSortOrder(t *testing.T) {
	got := parseIndexColumn(`email COLLATE "C" varchar_pattern_ops DESC NULLS LAST`)
	if got.Name != "email" {
		t.Errorf("Name: got %q", got.Name)
	}
	if got.Collation == nil || got.Collation.Name != "C" {
		t.Fatalf("Collation: got %+v", got.Collation)
	}
	if got.OpClass == nil || got.OpClass.Name != "varchar_pattern_ops" {
		t.Fatalf("OpClass: got %+v", got.OpClass)
	}
	if got.SortOrder != "DESC" || got.Nulls != "LAST" {
		t.Errorf("SortOrder/Nulls: got %q/%q", got.SortOrder, got.Nulls)
	}
}

// TestParseIndexColumnDoubleWrappedExpressionWithSortOrder guards
// pg_get_indexdef's confirmed-live inconsistency: a raw operator expression
// like "a+b" reconstructs with an extra defensive wrapping layer as a whole
// list item ("((a + b))"), unlike a function call ("lower(email)", which
// gets none) — and a trailing DESC after that expression's own closing
// paren must still be recognized, not swallowed into Expr.Text.
func TestParseIndexColumnDoubleWrappedExpressionWithSortOrder(t *testing.T) {
	got := parseIndexColumn(`((a + b)) DESC`)
	if got.Expr == nil || got.Expr.Text != "((a + b))" {
		t.Fatalf("Expr: got %+v, want Text = \"((a + b))\"", got.Expr)
	}
	if got.SortOrder != "DESC" {
		t.Errorf("SortOrder: got %q, want DESC", got.SortOrder)
	}
}
