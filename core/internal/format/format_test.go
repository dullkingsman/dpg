package format_test

import (
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/format"
)

// ── Lexer ─────────────────────────────────────────────────────────────────────

func TestLex_Keywords(t *testing.T) {
	src := `TABLE VIEW FUNCTION`
	tokens := format.Lex("", []byte(src))
	var kws []string
	for _, tok := range tokens {
		if tok.Type == format.TokKeyword {
			kws = append(kws, tok.Text)
		}
	}
	if len(kws) != 3 {
		t.Errorf("expected 3 keywords, got %d: %v", len(kws), kws)
	}
}

func TestLex_LineComment(t *testing.T) {
	src := "-- this is a comment\nTABLE"
	tokens := format.Lex("", []byte(src))
	var got []format.TokType
	for _, tok := range tokens {
		if tok.Type != format.TokWhitespace && tok.Type != format.TokNewline && tok.Type != format.TokEOF {
			got = append(got, tok.Type)
		}
	}
	if len(got) < 2 || got[0] != format.TokLineComment || got[1] != format.TokKeyword {
		t.Errorf("unexpected token sequence: %v", got)
	}
}

func TestLex_BlockComment(t *testing.T) {
	src := `/* nested /* block */ comment */`
	tokens := format.Lex("", []byte(src))
	found := false
	for _, tok := range tokens {
		if tok.Type == format.TokBlockComment {
			found = true
			if tok.Text != src {
				t.Errorf("block comment text: got %q", tok.Text)
			}
		}
	}
	if !found {
		t.Error("block comment token not found")
	}
}

func TestLex_DollarQuote(t *testing.T) {
	src := `$$SELECT 1;$$`
	tokens := format.Lex("", []byte(src))
	found := false
	for _, tok := range tokens {
		if tok.Type == format.TokDollarQuote {
			found = true
			if tok.Text != src {
				t.Errorf("dollar quote text: got %q", tok.Text)
			}
		}
	}
	if !found {
		t.Error("dollar quote token not found")
	}
}

func TestLex_LineNumbers(t *testing.T) {
	src := "TABLE\nVIEW"
	tokens := format.Lex("", []byte(src))
	viewLine := -1
	for _, tok := range tokens {
		if tok.Text == "VIEW" {
			viewLine = tok.Line
		}
	}
	if viewLine != 2 {
		t.Errorf("VIEW should be on line 2, got %d", viewLine)
	}
}

// ── Format (round-trip) ────────────────────────────────────────────────────────

func formatSrc(t *testing.T, src string, opts format.Options) string {
	t.Helper()
	out, err := format.Format("test.dpg", []byte(src), opts)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	return string(out)
}

var defaultOpts = format.Options{IndentSize: 4, KeywordCase: "upper"}

func TestFormat_SimpleTable(t *testing.T) {
	src := `table users (
    id bigint not null,
    email text not null
);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "TABLE") {
		t.Errorf("keywords not uppercased in: %s", out)
	}
	if !strings.Contains(out, "users") {
		t.Error("table name 'users' missing")
	}
}

func TestFormat_KeywordCaseLower(t *testing.T) {
	opts := format.Options{IndentSize: 4, KeywordCase: "lower"}
	src := `TABLE users (id BIGINT NOT NULL);`
	out := formatSrc(t, src, opts)
	if !strings.Contains(out, "table") {
		t.Errorf("expected lowercase keywords in: %s", out)
	}
	if strings.Contains(out, "TABLE") {
		t.Errorf("unexpected uppercase keywords in: %s", out)
	}
}

func TestFormat_IndentSize2(t *testing.T) {
	opts := format.Options{IndentSize: 2, KeywordCase: "upper"}
	src := `TABLE users (id BIGINT NOT NULL, email TEXT NOT NULL);`
	out := formatSrc(t, src, opts)
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, " ") {
			if strings.HasPrefix(line, "    ") {
				t.Errorf("expected 2-space indent, found 4+ space indent in: %q", line)
			}
			break
		}
	}
}

func TestFormat_BlankLineBetweenObjects(t *testing.T) {
	src := `TABLE a (id BIGINT NOT NULL);
TABLE b (id BIGINT NOT NULL);`
	out := formatSrc(t, src, defaultOpts)
	// There should be a blank line between the two table declarations.
	if !strings.Contains(out, ";\n\nTABLE") {
		t.Errorf("expected blank line between top-level declarations, got:\n%s", out)
	}
}

func TestFormat_LeadingCommentPreserved(t *testing.T) {
	src := `-- Users table
TABLE users (id BIGINT NOT NULL);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "-- Users table") {
		t.Errorf("leading comment lost, got:\n%s", out)
	}
}

func TestFormat_IntraTableCommentPreserved(t *testing.T) {
	src := `TABLE users (
    id    BIGINT NOT NULL,
    -- email column
    email TEXT NOT NULL
);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "-- email column") {
		t.Errorf("intra-table comment lost, got:\n%s", out)
	}
}

func TestFormat_BlankLineInsideTablePreserved(t *testing.T) {
	src := `TABLE users (
    id    BIGINT NOT NULL,

    -- lifecycle
    created_at TIMESTAMPTZ NOT NULL
);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "\n\n") {
		t.Errorf("blank line inside table lost, got:\n%s", out)
	}
	if !strings.Contains(out, "-- lifecycle") {
		t.Errorf("section comment lost, got:\n%s", out)
	}
}

func TestFormat_TrailingCommentPreserved(t *testing.T) {
	src := `TABLE users (
    id    BIGINT NOT NULL, -- primary key
    email TEXT NOT NULL
);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "-- primary key") {
		t.Errorf("trailing comment lost, got:\n%s", out)
	}
}

func TestFormat_OpaqueObjectPreserved(t *testing.T) {
	src := `EXTENSION "uuid-ossp";`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "EXTENSION") {
		t.Errorf("EXTENSION keyword missing, got:\n%s", out)
	}
	if !strings.Contains(out, "uuid-ossp") {
		t.Errorf("extension name missing, got:\n%s", out)
	}
}

func TestFormat_UnloggedTable(t *testing.T) {
	src := `UNLOGGED TABLE cache (key TEXT NOT NULL, value TEXT NOT NULL);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "UNLOGGED") {
		t.Errorf("UNLOGGED keyword missing, got:\n%s", out)
	}
	if !strings.Contains(out, "TABLE") {
		t.Errorf("TABLE keyword missing, got:\n%s", out)
	}
}

func TestFormat_ParseError_ReturnsOriginal(t *testing.T) {
	// Intentionally broken SQL; Format should return original unchanged.
	src := `NOT VALID SQL @@@`
	out, err := format.Format("bad.dpg", []byte(src), defaultOpts)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if string(out) != src {
		t.Errorf("on error, original should be returned unchanged")
	}
}

func TestFormat_EmptyFile(t *testing.T) {
	out := formatSrc(t, "", defaultOpts)
	if out != "" {
		t.Errorf("empty file: expected empty output, got %q", out)
	}
}

// ── Idempotence ───────────────────────────────────────────────────────────────

func TestFormat_Idempotent(t *testing.T) {
	src := `-- Users table
TABLE users (
    id    BIGINT NOT NULL,

    -- lifecycle
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

TABLE orders (
    id      BIGINT NOT NULL,
    user_id BIGINT NOT NULL
);`
	first := formatSrc(t, src, defaultOpts)
	second := formatSrc(t, first, defaultOpts)
	if first != second {
		t.Errorf("format is not idempotent.\nFirst:\n%s\nSecond:\n%s", first, second)
	}
}

// ── Options ───────────────────────────────────────────────────────────────────

func TestOptions_IndentDefault(t *testing.T) {
	opts := format.Options{}
	ind := opts.Indent()
	if ind != "    " {
		t.Errorf("default indent: got %q", ind)
	}
}

func TestOptions_IndentCustom(t *testing.T) {
	opts := format.Options{IndentSize: 2}
	if opts.Indent() != "  " {
		t.Errorf("2-space indent: got %q", opts.Indent())
	}
}

func TestOptions_KeywordUpper(t *testing.T) {
	opts := format.Options{KeywordCase: "upper"}
	if opts.Keyword("table") != "TABLE" {
		t.Errorf("Keyword upper: got %q", opts.Keyword("table"))
	}
}

func TestOptions_KeywordLower(t *testing.T) {
	opts := format.Options{KeywordCase: "lower"}
	if opts.Keyword("TABLE") != "table" {
		t.Errorf("Keyword lower: got %q", opts.Keyword("TABLE"))
	}
}

func TestOptions_KeywordDefaultUpper(t *testing.T) {
	opts := format.Options{}
	if opts.Keyword("table") != "TABLE" {
		t.Errorf("Keyword default (upper): got %q", opts.Keyword("table"))
	}
}

// ── Column ordering ───────────────────────────────────────────────────────────

func TestFormat_ColumnsBeforeReferences(t *testing.T) {
	// FK constraints (references section) must appear after column defs and
	// before other constraints.
	src := `TABLE orders (
    CONSTRAINT pk_orders      PRIMARY KEY (id),
    CONSTRAINT fk_orders_user FOREIGN KEY (user_id) REFERENCES users(id),
    id      BIGINT NOT NULL,
    user_id BIGINT NOT NULL
);`
	out := formatSrc(t, src, defaultOpts)
	idPos := strings.Index(out, "id ")
	fkPos := strings.Index(out, "fk_orders_user")
	pkPos := strings.Index(out, "pk_orders")
	if idPos < 0 || fkPos < 0 || pkPos < 0 {
		t.Fatalf("missing expected tokens in output:\n%s", out)
	}
	if !(idPos < fkPos && fkPos < pkPos) {
		t.Errorf("expected column < FK < PK, got positions id=%d fk=%d pk=%d\n%s",
			idPos, fkPos, pkPos, out)
	}
}

func TestFormat_MultipleReferencesAlphabetical(t *testing.T) {
	src := `TABLE order_items (
    id         BIGINT NOT NULL,
    CONSTRAINT fk_items_product FOREIGN KEY (product_id) REFERENCES products(id),
    CONSTRAINT fk_items_order   FOREIGN KEY (order_id)   REFERENCES orders(id),
    CONSTRAINT pk_order_items   PRIMARY KEY (id)
);`
	out := formatSrc(t, src, defaultOpts)
	orderPos := strings.Index(out, "fk_items_order")
	productPos := strings.Index(out, "fk_items_product")
	if orderPos < 0 || productPos < 0 {
		t.Fatalf("missing FK constraints in output:\n%s", out)
	}
	// fk_items_order < fk_items_product alphabetically
	if orderPos > productPos {
		t.Errorf("FKs not alphabetically ordered: order=%d product=%d\n%s",
			orderPos, productPos, out)
	}
}

func TestFormat_GeneratedIdentityColumnPreservesSourceOrder(t *testing.T) {
	// Generated/identity columns stay in their source position among column defs.
	src := `TABLE users (
    id         BIGINT GENERATED ALWAYS AS IDENTITY,
    email      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT pk_users PRIMARY KEY (id)
);`
	out := formatSrc(t, src, defaultOpts)
	idPos := strings.Index(out, "id ")
	emailPos := strings.Index(out, "email")
	pkPos := strings.Index(out, "pk_users")
	if idPos < 0 || emailPos < 0 || pkPos < 0 {
		t.Fatalf("missing tokens in output:\n%s", out)
	}
	if !(idPos < emailPos && emailPos < pkPos) {
		t.Errorf("source order not preserved: id=%d email=%d pk=%d\n%s",
			idPos, emailPos, pkPos, out)
	}
}

func TestFormat_ColumnOrderingIdempotent(t *testing.T) {
	src := `TABLE order_items (
    CONSTRAINT pk_order_items   PRIMARY KEY (id),
    CONSTRAINT fk_items_product FOREIGN KEY (product_id) REFERENCES products(id),
    CONSTRAINT fk_items_order   FOREIGN KEY (order_id)   REFERENCES orders(id),
    id         BIGINT NOT NULL,
    order_id   BIGINT NOT NULL,
    product_id BIGINT NOT NULL
);`
	first := formatSrc(t, src, defaultOpts)
	second := formatSrc(t, first, defaultOpts)
	if first != second {
		t.Errorf("column ordering is not idempotent.\nFirst:\n%s\nSecond:\n%s", first, second)
	}
}

// ── Block directive ordering ──────────────────────────────────────────────────

func TestFormat_RenamedFromFirstInBlock(t *testing.T) {
	src := `TABLE users (
    id BIGINT NOT NULL
) {
    COMMENT 'The users table';
    GRANTS { SELECT TO reader; }
    RENAMED FROM old_users;
}`
	out := formatSrc(t, src, defaultOpts)
	renamedPos := strings.Index(out, "RENAMED")
	commentPos := strings.Index(out, "COMMENT")
	grantPos := strings.Index(out, "GRANTS")
	if renamedPos < 0 || commentPos < 0 || grantPos < 0 {
		t.Fatalf("missing directives in output:\n%s", out)
	}
	if !(renamedPos < commentPos && commentPos < grantPos) {
		t.Errorf("expected RENAMED < COMMENT < GRANTS, got %d %d %d\n%s",
			renamedPos, commentPos, grantPos, out)
	}
}

func TestFormat_BlockDirectiveCanonicalOrder(t *testing.T) {
	// Verify full canonical order: RENAMED, COMMENT, OWNER, GRANTS.
	src := `TABLE t (id BIGINT NOT NULL) {
    GRANTS { SELECT TO r; }
    OWNER TO dba;
    COMMENT 't';
    RENAMED FROM old_t;
}`
	out := formatSrc(t, src, defaultOpts)
	rPos := strings.Index(out, "RENAMED")
	cPos := strings.Index(out, "COMMENT")
	oPos := strings.Index(out, "OWNER")
	gPos := strings.Index(out, "GRANTS")
	if rPos < 0 || cPos < 0 || oPos < 0 || gPos < 0 {
		t.Fatalf("missing directives:\n%s", out)
	}
	if !(rPos < cPos && cPos < oPos && oPos < gPos) {
		t.Errorf("unexpected block order r=%d c=%d o=%d g=%d\n%s",
			rPos, cPos, oPos, gPos, out)
	}
}

func TestFormat_BlockDirectiveOrderIdempotent(t *testing.T) {
	src := `TABLE t (id BIGINT NOT NULL) {
    GRANTS { SELECT TO r; }
    OWNER TO dba;
    COMMENT 't';
    RENAMED FROM old_t;
}`
	first := formatSrc(t, src, defaultOpts)
	second := formatSrc(t, first, defaultOpts)
	if first != second {
		t.Errorf("block directive ordering is not idempotent.\nFirst:\n%s\nSecond:\n%s", first, second)
	}
}

func TestFormat_BlockDirectiveAlreadySortedIsNoop(t *testing.T) {
	// When directives are already in canonical order, output should be identical.
	src := `TABLE t (id BIGINT NOT NULL) {
    RENAMED FROM old_t;
    COMMENT 't';
    GRANTS { SELECT TO r; }
}`
	first := formatSrc(t, src, defaultOpts)
	second := formatSrc(t, first, defaultOpts)
	if first != second {
		t.Errorf("not idempotent when already sorted.\nFirst:\n%s\nSecond:\n%s", first, second)
	}
	if !strings.Contains(first, "RENAMED") || !strings.Contains(first, "COMMENT") {
		t.Errorf("directives missing:\n%s", first)
	}
}

// ── MACRO preservation ────────────────────────────────────────────────────────

func TestFormat_MacroDeclarationPreserved(t *testing.T) {
	src := `MACRO common_cols (
    id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
)

TABLE users (
    email TEXT NOT NULL,
    id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "MACRO") {
		t.Errorf("MACRO declaration was removed, got:\n%s", out)
	}
	if !strings.Contains(out, "common_cols") {
		t.Errorf("MACRO name 'common_cols' missing, got:\n%s", out)
	}
}

func TestFormat_MacroBraceStylePreserved(t *testing.T) {
	src := `MACRO block_attrs {
    COMMENT 'standard comment';
    DEPRECATED 'old';
}

EXTENSION pgcrypto;`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "MACRO") {
		t.Errorf("MACRO declaration was removed, got:\n%s", out)
	}
	if !strings.Contains(out, "block_attrs") {
		t.Errorf("MACRO name 'block_attrs' missing, got:\n%s", out)
	}
}

func TestFormat_MacroKeywordCased(t *testing.T) {
	src := `macro items (id bigint not null);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "MACRO") {
		t.Errorf("MACRO keyword not uppercased, got:\n%s", out)
	}
}

func TestFormat_MacroIdempotent(t *testing.T) {
	src := `MACRO items (
    id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
)

TABLE t (id BIGINT NOT NULL);`
	first := formatSrc(t, src, defaultOpts)
	second := formatSrc(t, first, defaultOpts)
	if first != second {
		t.Errorf("format with MACRO is not idempotent.\nFirst:\n%s\nSecond:\n%s", first, second)
	}
}

// ── VIRTUAL TYPE ──────────────────────────────────────────────────────────────

func TestFormat_VirtualTypeKeywordPreserved(t *testing.T) {
	src := `VIRTUAL TYPE money_amount (
    INPUT = money_in,
    OUTPUT = money_out
);`
	out := formatSrc(t, src, defaultOpts)
	if !strings.Contains(out, "VIRTUAL") {
		t.Errorf("VIRTUAL keyword missing, got:\n%s", out)
	}
	if !strings.Contains(out, "TYPE") {
		t.Errorf("TYPE keyword missing, got:\n%s", out)
	}
	if !strings.Contains(out, "money_amount") {
		t.Errorf("type name 'money_amount' missing, got:\n%s", out)
	}
}

// ── SCHEMA block nesting ──────────────────────────────────────────────────────

func TestFormat_SchemaBlockPreservesNesting(t *testing.T) {
	src := `SCHEMA public {
    OWNER "postgres";

    TABLE users (
        id BIGINT NOT NULL,
        CONSTRAINT pk_users PRIMARY KEY (id)
    );
}`
	out := formatSrc(t, src, defaultOpts)
	// TABLE should appear inside SCHEMA, not at the top level.
	schemaPos := strings.Index(out, "SCHEMA")
	tablePos := strings.Index(out, "TABLE")
	closingBrace := strings.LastIndex(out, "}")
	if schemaPos < 0 || tablePos < 0 || closingBrace < 0 {
		t.Fatalf("missing expected tokens in:\n%s", out)
	}
	if tablePos < schemaPos {
		t.Errorf("TABLE appears before SCHEMA — nesting is broken:\n%s", out)
	}
	if tablePos > closingBrace {
		t.Errorf("TABLE appears after the closing '}' — nesting is broken:\n%s", out)
	}
}

func TestFormat_SchemaOwnerIndented(t *testing.T) {
	src := `SCHEMA public {
    OWNER "postgres";

    TABLE t (id BIGINT NOT NULL);
}`
	out := formatSrc(t, src, defaultOpts)
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "OWNER") {
			if !strings.HasPrefix(line, "    ") {
				t.Errorf("OWNER directive not indented inside SCHEMA:\n%s", out)
			}
			break
		}
	}
}

func TestFormat_SchemaBlockIdempotent(t *testing.T) {
	src := `SCHEMA public {
    OWNER "postgres";

    TABLE users (
        id         BIGINT NOT NULL,
        CONSTRAINT pk_users PRIMARY KEY (id)
    );
}`
	first := formatSrc(t, src, defaultOpts)
	second := formatSrc(t, first, defaultOpts)
	if first != second {
		t.Errorf("schema block format is not idempotent.\nFirst:\n%s\nSecond:\n%s", first, second)
	}
}

func TestFormat_OpaqueBlockRenamedFromFirst(t *testing.T) {
	// Block sorting also applies to opaque (non-table) objects.
	src := `ROLE analyst NOLOGIN {
    GRANTS { USAGE ON SCHEMA public TO analyst; }
    RENAMED FROM old_analyst;
    COMMENT 'Analytics read role';
}`
	out := formatSrc(t, src, defaultOpts)
	renamedPos := strings.Index(out, "RENAMED")
	commentPos := strings.Index(out, "COMMENT")
	grantPos := strings.Index(out, "GRANTS")
	if renamedPos < 0 || commentPos < 0 || grantPos < 0 {
		t.Fatalf("missing directives:\n%s", out)
	}
	if !(renamedPos < commentPos && commentPos < grantPos) {
		t.Errorf("expected RENAMED < COMMENT < GRANTS in opaque block, got %d %d %d\n%s",
			renamedPos, commentPos, grantPos, out)
	}
}
