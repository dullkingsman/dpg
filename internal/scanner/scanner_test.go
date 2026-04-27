package scanner_test

import (
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/scanner"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func scan(t *testing.T, src string) []pipeline.RawObject {
	t.Helper()
	sc := scanner.New()
	objs, err := sc.Scan("test.dpg", []byte(src))
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	return objs
}

func scanErr(t *testing.T, src string) error {
	t.Helper()
	sc := scanner.New()
	_, err := sc.Scan("test.dpg", []byte(src))
	return err
}

func assertOne(t *testing.T, objs []pipeline.RawObject) pipeline.RawObject {
	t.Helper()
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	return objs[0]
}

func assertKind(t *testing.T, obj pipeline.RawObject, want pipeline.ObjectKind) {
	t.Helper()
	if obj.Kind != want {
		t.Errorf("kind: got %s, want %s", obj.Kind, want)
	}
}

func assertPart1Contains(t *testing.T, obj pipeline.RawObject, want string) {
	t.Helper()
	if !strings.Contains(obj.Part1, want) {
		t.Errorf("Part1 %q does not contain %q", obj.Part1, want)
	}
}

func assertPart2Contains(t *testing.T, obj pipeline.RawObject, want string) {
	t.Helper()
	if !strings.Contains(obj.Part2, want) {
		t.Errorf("Part2 %q does not contain %q", obj.Part2, want)
	}
}

func assertPart2Empty(t *testing.T, obj pipeline.RawObject) {
	t.Helper()
	if obj.Part2 != "" {
		t.Errorf("expected empty Part2, got %q", obj.Part2)
	}
}

func assertSchema(t *testing.T, obj pipeline.RawObject, want string) {
	t.Helper()
	if obj.Schema != want {
		t.Errorf("Schema: got %q, want %q", obj.Schema, want)
	}
}

// ── single-kind detection ─────────────────────────────────────────────────────

func TestSimpleTable(t *testing.T) {
	src := `TABLE users (
    id    BIGINT GENERATED ALWAYS AS IDENTITY,
    email TEXT   NOT NULL,
    CONSTRAINT pk_users PRIMARY KEY (id)
);`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindTable)
	assertPart1Contains(t, obj, "users")
	assertPart1Contains(t, obj, "pk_users")
	assertPart2Empty(t, obj)
}

func TestTableWithBlock(t *testing.T) {
	src := `TABLE users (
    id    BIGINT GENERATED ALWAYS AS IDENTITY,
    email TEXT   NOT NULL
)
{
    INDICES { idx_email (email); }
    GRANTS  { SELECT TO app_readonly; }
}`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindTable)
	assertPart1Contains(t, obj, "users")
	assertPart2Contains(t, obj, "idx_email")
	assertPart2Contains(t, obj, "app_readonly")
}

func TestTableWithStorageOptions(t *testing.T) {
	src := `TABLE large_events (
    id      BIGINT GENERATED ALWAYS AS IDENTITY,
    payload JSONB
) WITH (fillfactor = 70)
{
    INDICES { idx_large_events_ts (created_at); }
}`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindTable)
	assertPart1Contains(t, obj, "fillfactor")
	assertPart2Contains(t, obj, "idx_large_events_ts")
}

func TestUnloggedTable(t *testing.T) {
	obj := assertOne(t, scan(t, `UNLOGGED TABLE session_cache (
    key   TEXT,
    value JSONB
);`))
	assertKind(t, obj, pipeline.KindUnloggedTable)
	assertPart1Contains(t, obj, "session_cache")
}

func TestForeignTable(t *testing.T) {
	obj := assertOne(t, scan(t, `FOREIGN TABLE remote_events (
    id      BIGINT,
    payload JSONB
) SERVER log_server OPTIONS (table_name 'events');`))
	assertKind(t, obj, pipeline.KindForeignTable)
	assertPart1Contains(t, obj, "log_server")
}

func TestView(t *testing.T) {
	src := `VIEW active_users AS
    SELECT id, email FROM users WHERE status = 'active';`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindView)
	assertPart1Contains(t, obj, "active_users")
	assertPart1Contains(t, obj, "SELECT")
	assertPart2Empty(t, obj)
}

func TestViewWithBlock(t *testing.T) {
	src := `VIEW active_users AS
    SELECT id, email FROM users WHERE status = 'active';
{
    GRANTS { SELECT TO app_readonly; }
}`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindView)
	assertPart2Contains(t, obj, "app_readonly")
}

func TestMaterializedView(t *testing.T) {
	obj := assertOne(t, scan(t, `MATERIALIZED VIEW daily_revenue AS
    SELECT date_trunc('day', created_at) AS day, SUM(total_amount) AS revenue
    FROM orders WHERE status = 'completed' GROUP BY 1
WITH NO DATA;`))
	assertKind(t, obj, pipeline.KindMaterializedView)
	assertPart1Contains(t, obj, "daily_revenue")
	assertPart1Contains(t, obj, "WITH NO DATA")
}

func TestRecursiveView(t *testing.T) {
	obj := assertOne(t, scan(t, `RECURSIVE VIEW org_tree (id, parent_id, depth) AS
    SELECT id, parent_id, 0 FROM departments WHERE parent_id IS NULL
    UNION ALL
    SELECT d.id, d.parent_id, t.depth + 1 FROM departments d JOIN org_tree t ON d.parent_id = t.id;`))
	assertKind(t, obj, pipeline.KindRecursiveView)
}

func TestSimpleFunction(t *testing.T) {
	src := `FUNCTION active_user_count() RETURNS BIGINT
LANGUAGE sql STABLE PARALLEL SAFE
AS $$
    SELECT COUNT(*) FROM users WHERE status = 'active';
$$;`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindFunction)
	assertPart1Contains(t, obj, "active_user_count")
	assertPart1Contains(t, obj, "SELECT COUNT(*)")
	assertPart2Empty(t, obj)
}

func TestFunctionWithBlock(t *testing.T) {
	src := `FUNCTION get_user(p_email TEXT) RETURNS users
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public
AS $$
BEGIN
    RETURN (SELECT * FROM users WHERE email = p_email);
END;
$$;
{
    COMMENT "Fetch user by email";
    GRANTS { EXECUTE TO app_service; }
}`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindFunction)
	assertPart1Contains(t, obj, "get_user")
	assertPart1Contains(t, obj, "$$")
	assertPart2Contains(t, obj, "app_service")
}

func TestNamedDollarQuote(t *testing.T) {
	src := `FUNCTION format_price(p NUMERIC) RETURNS TEXT
LANGUAGE plpgsql IMMUTABLE STRICT
AS $func$
BEGIN
    RETURN '$' || TO_CHAR(p, 'FM999,999,990.00');
END;
$func$;`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindFunction)
	assertPart1Contains(t, obj, "$func$")
	assertPart1Contains(t, obj, "RETURN '$'")
}

func TestProcedure(t *testing.T) {
	obj := assertOne(t, scan(t, `PROCEDURE process_settlements()
LANGUAGE plpgsql SECURITY DEFINER
AS $$
BEGIN
    PERFORM settle_order(id) FROM settlements WHERE processed = false;
END;
$$;`))
	assertKind(t, obj, pipeline.KindProcedure)
}

func TestEnum(t *testing.T) {
	obj := assertOne(t, scan(t, `ENUM user_status ('active', 'suspended', 'deleted');`))
	assertKind(t, obj, pipeline.KindEnum)
	assertPart1Contains(t, obj, "user_status")
	assertPart1Contains(t, obj, "'active'")
}

func TestEnumWithBlock(t *testing.T) {
	obj := assertOne(t, scan(t, `ENUM invoice_status ('draft', 'sent', 'paid', 'void');
{
    COMMENT "Billing lifecycle states";
}`))
	assertKind(t, obj, pipeline.KindEnum)
	assertPart2Contains(t, obj, "Billing lifecycle states")
}

func TestCompositeType(t *testing.T) {
	obj := assertOne(t, scan(t, `TYPE address AS (
    street TEXT,
    city   TEXT,
    state  CHAR(2)
);`))
	assertKind(t, obj, pipeline.KindCompositeType)
	assertPart1Contains(t, obj, "address")
}

func TestRangeType(t *testing.T) {
	obj := assertOne(t, scan(t, `TYPE float8range AS RANGE (
    SUBTYPE      = float8,
    SUBTYPE_DIFF = float8mi
);`))
	assertKind(t, obj, pipeline.KindRangeType)
}

func TestDomainType(t *testing.T) {
	obj := assertOne(t, scan(t, `DOMAIN positive_integer AS INTEGER
{
    CONSTRAINT positive_only CHECK (VALUE > 0);
}`))
	assertKind(t, obj, pipeline.KindDomainType)
	assertPart1Contains(t, obj, "positive_integer")
	assertPart2Contains(t, obj, "positive_only")
}

func TestSchema(t *testing.T) {
	src := `SCHEMA analytics {
    OWNER "analytics_role";
    COMMENT "Derived tables and event aggregations";
}`
	objs := scan(t, src)
	if len(objs) != 1 {
		t.Fatalf("expected 1 object (schema only), got %d", len(objs))
	}
	assertKind(t, objs[0], pipeline.KindSchema)
	if objs[0].Part1 != "analytics" {
		t.Errorf("schema Part1: got %q, want %q", objs[0].Part1, "analytics")
	}
	assertPart2Contains(t, objs[0], "OWNER")
	assertPart2Contains(t, objs[0], "COMMENT")
}

func TestSchemaWithNestedObjects(t *testing.T) {
	src := `SCHEMA public {
    OWNER "postgres";

    TABLE users (
        id    BIGINT GENERATED ALWAYS AS IDENTITY,
        email TEXT NOT NULL,
        CONSTRAINT pk_users PRIMARY KEY (id)
    )
    {
        INDICES { idx_email (email); }
    }

    FUNCTION active_count() RETURNS BIGINT
    LANGUAGE sql STABLE
    AS $$
        SELECT COUNT(*) FROM users WHERE active;
    $$;
}`
	objs := scan(t, src)

	// Expect: schema + table + function = 3 objects.
	if len(objs) != 3 {
		t.Fatalf("expected 3 objects, got %d: %v", len(objs), kindList(objs))
	}

	var schemaObj, tableObj, funcObj pipeline.RawObject
	for _, o := range objs {
		switch o.Kind {
		case pipeline.KindSchema:
			schemaObj = o
		case pipeline.KindTable:
			tableObj = o
		case pipeline.KindFunction:
			funcObj = o
		}
	}

	// Schema itself has attributes in Part2.
	assertKind(t, schemaObj, pipeline.KindSchema)
	if schemaObj.Part1 != "public" {
		t.Errorf("schema name: got %q, want %q", schemaObj.Part1, "public")
	}
	assertPart2Contains(t, schemaObj, "OWNER")

	// Nested table has Schema = "public".
	assertKind(t, tableObj, pipeline.KindTable)
	assertSchema(t, tableObj, "public")
	assertPart1Contains(t, tableObj, "users")
	assertPart2Contains(t, tableObj, "idx_email")

	// Nested function has Schema = "public".
	assertKind(t, funcObj, pipeline.KindFunction)
	assertSchema(t, funcObj, "public")
	assertPart1Contains(t, funcObj, "active_count")
}

func TestExtension(t *testing.T) {
	obj := assertOne(t, scan(t, `EXTENSION pgcrypto;`))
	assertKind(t, obj, pipeline.KindExtension)
	assertPart1Contains(t, obj, "pgcrypto")
}

func TestSequence(t *testing.T) {
	obj := assertOne(t, scan(t, `SEQUENCE order_number_seq
    AS BIGINT
    START WITH  10000
    INCREMENT BY 1
    MAXVALUE     99999999
    CACHE        50
    NO CYCLE
    OWNED BY orders.order_number;`))
	assertKind(t, obj, pipeline.KindSequence)
	assertPart1Contains(t, obj, "order_number_seq")
}

func TestRole(t *testing.T) {
	obj := assertOne(t, scan(t, `ROLE app_readonly {
    NOLOGIN;
    COMMENT "Read-only access";
}`))
	assertKind(t, obj, pipeline.KindRole)
	assertPart1Contains(t, obj, "app_readonly")
	assertPart2Contains(t, obj, "NOLOGIN")
}

func TestFDW(t *testing.T) {
	obj := assertOne(t, scan(t, `FOREIGN DATA WRAPPER myfdw
    HANDLER   myfdw_handler
    VALIDATOR myfdw_validator;`))
	assertKind(t, obj, pipeline.KindFDW)
}

func TestServer(t *testing.T) {
	obj := assertOne(t, scan(t, `SERVER analytics_warehouse
    FOREIGN DATA WRAPPER postgres_fdw
    OPTIONS (host 'warehouse.internal', dbname 'analytics');`))
	assertKind(t, obj, pipeline.KindServer)
}

func TestUserMapping(t *testing.T) {
	obj := assertOne(t, scan(t, `USER MAPPING FOR app_service
    SERVER analytics_warehouse
    OPTIONS (user 'fdw_user', password 'env:FDW_PASSWORD');`))
	assertKind(t, obj, pipeline.KindUserMapping)
}

func TestPublication(t *testing.T) {
	obj := assertOne(t, scan(t, `PUBLICATION user_data
    FOR TABLE users, profiles
    WITH (publish = 'insert, update, delete');`))
	assertKind(t, obj, pipeline.KindPublication)
}

func TestSubscription(t *testing.T) {
	obj := assertOne(t, scan(t, `SUBSCRIPTION replica_users
    CONNECTION 'host=primary.db.internal dbname=myapp user=replicator'
    PUBLICATION user_data
    WITH (enabled = true, copy_data = true);`))
	assertKind(t, obj, pipeline.KindSubscription)
}

func TestEventTrigger(t *testing.T) {
	obj := assertOne(t, scan(t, `EVENT TRIGGER prevent_drop_table
    ON sql_drop
    WHEN TAG IN ('DROP TABLE', 'DROP SCHEMA')
    EXECUTE FUNCTION abort_drop();`))
	assertKind(t, obj, pipeline.KindEventTrigger)
}

func TestCollation(t *testing.T) {
	obj := assertOne(t, scan(t, `COLLATION case_insensitive (
    PROVIDER      = icu,
    LOCALE        = 'und-u-ks-level2',
    DETERMINISTIC = false
);`))
	assertKind(t, obj, pipeline.KindCollation)
}

func TestOperator(t *testing.T) {
	obj := assertOne(t, scan(t, `OPERATOR === (
    LEFTARG   = complex,
    RIGHTARG  = complex,
    PROCEDURE = complex_eq
);`))
	assertKind(t, obj, pipeline.KindOperator)
}

func TestOperatorClass(t *testing.T) {
	obj := assertOne(t, scan(t, `OPERATOR CLASS my_ops USING btree FOR TYPE mytype (
    OPERATOR 1 < ,
    OPERATOR 3 =
);`))
	assertKind(t, obj, pipeline.KindOperatorClass)
}

func TestOperatorFamily(t *testing.T) {
	obj := assertOne(t, scan(t, `OPERATOR FAMILY my_family USING btree;`))
	assertKind(t, obj, pipeline.KindOperatorFamily)
}

func TestCast(t *testing.T) {
	obj := assertOne(t, scan(t, `CAST (mytype AS TEXT)
    WITH FUNCTION mytype_to_text(mytype)
    AS IMPLICIT;`))
	assertKind(t, obj, pipeline.KindCast)
}

func TestStatistics(t *testing.T) {
	obj := assertOne(t, scan(t, `STATISTICS orders_stats (dependencies, ndistinct, mcv)
    ON customer_id, created_at
    FROM orders;`))
	assertKind(t, obj, pipeline.KindStatisticsObject)
}

func TestTextSearchConfiguration(t *testing.T) {
	obj := assertOne(t, scan(t, `TEXT SEARCH CONFIGURATION english_unaccented (COPY = pg_catalog.english)
{
    MAPPING FOR hword, hword_part WITH unaccent, english_stem;
}`))
	assertKind(t, obj, pipeline.KindTSConfig)
}

func TestTextSearchDictionary(t *testing.T) {
	obj := assertOne(t, scan(t, `TEXT SEARCH DICTIONARY english_ispell (
    TEMPLATE  = ispell,
    DictFile  = english,
    AffFile   = english
);`))
	assertKind(t, obj, pipeline.KindTSDict)
}

func TestTextSearchParser(t *testing.T) {
	obj := assertOne(t, scan(t, `TEXT SEARCH PARSER my_parser (
    START    = prsd_start,
    GETTOKEN = prsd_nexttoken,
    END      = prsd_end,
    LEXTYPES = prsd_lextype
);`))
	assertKind(t, obj, pipeline.KindTSParser)
}

func TestTextSearchTemplate(t *testing.T) {
	obj := assertOne(t, scan(t, `TEXT SEARCH TEMPLATE ispell_template (
    LEXIZE = dispell_lexize,
    INIT   = dispell_init
);`))
	assertKind(t, obj, pipeline.KindTSTemplate)
}

func TestDefaultPrivileges(t *testing.T) {
	obj := assertOne(t, scan(t, `DEFAULT PRIVILEGES FOR ROLE app_admin {
    GRANTS {
        SELECT  ON TABLES    TO app_readonly;
        EXECUTE ON FUNCTIONS TO app_service;
    }
}`))
	assertKind(t, obj, pipeline.KindDefaultPrivileges)
}

func TestAggregate(t *testing.T) {
	obj := assertOne(t, scan(t, `AGGREGATE product (DOUBLE PRECISION) (
    SFUNC    = float8mul,
    STYPE    = DOUBLE PRECISION,
    INITCOND = '1'
);`))
	assertKind(t, obj, pipeline.KindAggregate)
}

func TestTablespace(t *testing.T) {
	obj := assertOne(t, scan(t, `TABLESPACE fast_ssd LOCATION '/mnt/nvme/pg_data';`))
	assertKind(t, obj, pipeline.KindTablespace)
}

// ── multi-declaration files ───────────────────────────────────────────────────

func TestMultipleTopLevelDeclarations(t *testing.T) {
	src := `
EXTENSION pgcrypto;

ROLE app_service { LOGIN; CONNECTION LIMIT 10; }

SEQUENCE order_seq AS BIGINT START WITH 1 INCREMENT BY 1;
`
	objs := scan(t, src)
	if len(objs) != 3 {
		t.Fatalf("expected 3 objects, got %d", len(objs))
	}
	assertKind(t, objs[0], pipeline.KindExtension)
	assertKind(t, objs[1], pipeline.KindRole)
	assertKind(t, objs[2], pipeline.KindSequence)
}

func TestMixedTopLevelAndSchema(t *testing.T) {
	src := `
EXTENSION pgcrypto;

SCHEMA public {
    OWNER "postgres";
    ENUM status ('a', 'b');
}

ROLE admin { LOGIN; }
`
	objs := scan(t, src)
	// extension + schema + enum + role = 4
	if len(objs) != 4 {
		t.Fatalf("expected 4 objects, got %d: %v", len(objs), kindList(objs))
	}

	kinds := kindList(objs)
	if kinds[0] != "EXTENSION" {
		t.Errorf("expected EXTENSION first, got %s", kinds[0])
	}
	if kinds[3] != "ROLE" {
		t.Errorf("expected ROLE last, got %s", kinds[3])
	}

	// Check enum has schema context.
	for _, o := range objs {
		if o.Kind == pipeline.KindEnum {
			assertSchema(t, o, "public")
		}
	}
}

// ── source positions ──────────────────────────────────────────────────────────

func TestSourcePosition(t *testing.T) {
	src := "\n\nTABLE users (id BIGINT);\n"
	obj := assertOne(t, scan(t, src))
	if obj.Pos.Line != 3 {
		t.Errorf("expected line 3, got %d", obj.Pos.Line)
	}
}

// ── dollar-quote edge cases ───────────────────────────────────────────────────

func TestSemicolonInsideDollarQuoteNotTerminator(t *testing.T) {
	src := `FUNCTION many_stmts() RETURNS VOID LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO t VALUES (1);
    INSERT INTO t VALUES (2);
    INSERT INTO t VALUES (3);
END;
$$;`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindFunction)
	// All semicolons inside $$ should be part of Part1, not terminators.
	assertPart1Contains(t, obj, "INSERT INTO t VALUES (1)")
	assertPart1Contains(t, obj, "INSERT INTO t VALUES (3)")
}

func TestBraceInsideDollarQuoteNotPart2(t *testing.T) {
	src := `FUNCTION returns_json() RETURNS JSONB LANGUAGE sql STABLE AS $$
    SELECT '{"key": "value"}'::jsonb;
$$;`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindFunction)
	assertPart2Empty(t, obj)
}

func TestSingleQuoteInsideDollarQuote(t *testing.T) {
	src := `FUNCTION greet(name TEXT) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $$
    SELECT 'Hello, ' || name || '!';
$$;`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindFunction)
	assertPart1Contains(t, obj, "'Hello, '")
}

// ── comment handling ──────────────────────────────────────────────────────────

func TestLineCommentBeforeDeclaration(t *testing.T) {
	src := `-- This is a comment
TABLE users (id BIGINT);`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindTable)
}

func TestBlockCommentBetweenDeclarations(t *testing.T) {
	src := `EXTENSION pgcrypto; /* a comment */ EXTENSION pg_trgm;`
	objs := scan(t, src)
	if len(objs) != 2 {
		t.Fatalf("expected 2, got %d", len(objs))
	}
}

// ── no-verb mandate ───────────────────────────────────────────────────────────

func TestCreateVerbForbidden(t *testing.T) {
	if err := scanErr(t, `CREATE TABLE users (id BIGINT);`); err == nil {
		t.Error("expected error for CREATE at declaration level, got nil")
	}
}

func TestAlterVerbForbidden(t *testing.T) {
	if err := scanErr(t, `ALTER TABLE users ADD COLUMN name TEXT;`); err == nil {
		t.Error("expected error for ALTER at declaration level, got nil")
	}
}

func TestDropVerbForbidden(t *testing.T) {
	if err := scanErr(t, `DROP TABLE users;`); err == nil {
		t.Error("expected error for DROP at declaration level, got nil")
	}
}

func TestTemporaryTableForbidden(t *testing.T) {
	if err := scanErr(t, `TEMPORARY TABLE tmp (id INT);`); err == nil {
		t.Error("expected error for TEMPORARY TABLE, got nil")
	}
}

// ── string literal edge cases ─────────────────────────────────────────────────

func TestSemicolonInsideStringNotTerminator(t *testing.T) {
	src := `VIEW odd_alias AS
    SELECT id, ';' AS separator FROM users;`
	obj := assertOne(t, scan(t, src))
	assertKind(t, obj, pipeline.KindView)
	assertPart1Contains(t, obj, "';'")
}

// ── empty / whitespace-only source ───────────────────────────────────────────

func TestEmptySource(t *testing.T) {
	objs := scan(t, "")
	if len(objs) != 0 {
		t.Errorf("expected 0 objects for empty source, got %d", len(objs))
	}
}

func TestWhitespaceOnlySource(t *testing.T) {
	objs := scan(t, "   \n\t\n  -- comment only\n  ")
	if len(objs) != 0 {
		t.Errorf("expected 0 objects, got %d", len(objs))
	}
}

// ── registry ──────────────────────────────────────────────────────────────────

func TestRegistered(t *testing.T) {
	impl, ok := pipeline.Resolve[pipeline.Tokenizer](pipeline.Default, pipeline.KeyTokenizer)
	if !ok {
		t.Fatal("scanner not registered in pipeline.Default")
	}
	if impl == nil {
		t.Fatal("registered Tokenizer is nil")
	}
}

// ── helper ───────────────────────────────────────────────────────────────────

func kindList(objs []pipeline.RawObject) []string {
	out := make([]string, len(objs))
	for i, o := range objs {
		out[i] = o.Kind.String()
	}
	return out
}
