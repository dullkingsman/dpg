package ir_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/blockparser"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pgparser"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

var zeroPos = pipeline.SourcePos{File: "test.dpg", Line: 1, Col: 1}

func buildObject(t *testing.T, kind pipeline.ObjectKind, part1, part2 string) pipeline.IRObject {
	t.Helper()
	p := pgparser.New()
	pgResult, err := p.Parse(kind, part1, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	bp := blockparser.New()
	blockAST, err := bp.Parse(kind, part2, zeroPos)
	if err != nil {
		t.Fatalf("block parse error: %v", err)
	}
	builder := ir.NewBuilder()
	obj, err := builder.Build(pgResult, blockAST)
	if err != nil {
		t.Fatalf("build error: %v", err)
	}
	return obj
}

// ── Table ─────────────────────────────────────────────────────────────────────

// TestBuildUnloggedTableSetsFlag guards a real, pre-existing bug found while
// testing an unrelated FOREIGN TABLE fix: CREATE TABLE and CREATE UNLOGGED
// TABLE parse to the identical pg_query node type (Node_CreateStmt),
// distinguished only by Relation.Relpersistence == "u" — but Build()'s
// dispatch unconditionally passed unlogged=false to buildTable, so a
// declared "UNLOGGED TABLE" always silently built as a regular table. The
// scanner correctly classified it as KindUnloggedTable (see
// scanner_test.go's TestUnloggedTable), which is exactly why this went
// unnoticed: nothing tested past the scan step to the actual IR object.
func TestBuildUnloggedTableSetsFlag(t *testing.T) {
	obj := buildObject(t, pipeline.KindUnloggedTable,
		`session_cache (
			key   TEXT,
			value JSONB
		)`,
		``,
	)
	tbl, ok := obj.(*ir.Table)
	if !ok {
		t.Fatalf("expected *ir.Table, got %T", obj)
	}
	if !tbl.Unlogged {
		t.Error("expected Unlogged = true")
	}
}

// TestBuildTableAndIndexTablespace guards a real bug found live-testing a
// demo project: ir.Table.Tablespace and ir.Index.Tablespace were both parsed
// (Table's from real pg_query CreateStmt.Tablespacename, Index's from the
// blockparser's own TABLESPACE clause) but never read by the differ,
// snapshot, introspect, or dump — a declared TABLESPACE on a table or index
// was a total silent no-op at apply time; `dpg plan` reported "(no changes)"
// even after adding TABLESPACE to both a table and one of its indexes.
func TestBuildTableAndIndexTablespace(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`archive (
			id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			data JSONB
		) TABLESPACE archive_space`,
		`INDICES { idx_data (data) TABLESPACE archive_space; }`,
	)
	tbl, ok := obj.(*ir.Table)
	if !ok {
		t.Fatalf("expected *ir.Table, got %T", obj)
	}
	if tbl.Tablespace == nil || *tbl.Tablespace != "archive_space" {
		t.Errorf("Table.Tablespace: got %v, want archive_space", tbl.Tablespace)
	}
	if len(tbl.Indexes) != 1 || tbl.Indexes[0].Tablespace == nil || *tbl.Indexes[0].Tablespace != "archive_space" {
		t.Errorf("Index.Tablespace: got %+v", tbl.Indexes)
	}
}

// TestBuildGeneratedColumnStoredAndVirtual guards the builder's
// pg_query.ConstrType_CONSTR_GENERATED handling distinguishing STORED from
// VIRTUAL (PostgreSQL 18+, RFC Section 7.2) via cst.GeneratedKind — "s" vs
// "v" — populating ir.Generated.Stored accordingly.
func TestBuildGeneratedColumnStoredAndVirtual(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`orders (
			amount          NUMERIC,
			amount_stored   NUMERIC GENERATED ALWAYS AS (amount * 1.08) STORED,
			amount_virtual  NUMERIC GENERATED ALWAYS AS (amount * 1.08) VIRTUAL
		)`,
		``,
	)
	tbl, ok := obj.(*ir.Table)
	if !ok {
		t.Fatalf("expected *ir.Table, got %T", obj)
	}
	var stored, virtual *ir.Column
	for _, c := range tbl.Columns {
		switch c.Name {
		case "amount_stored":
			stored = c
		case "amount_virtual":
			virtual = c
		}
	}
	if stored == nil || stored.Generated == nil {
		t.Fatal("expected amount_stored to have a Generated clause")
	}
	if !stored.Generated.Stored {
		t.Error("expected amount_stored.Generated.Stored = true")
	}
	if virtual == nil || virtual.Generated == nil {
		t.Fatal("expected amount_virtual to have a Generated clause")
	}
	if virtual.Generated.Stored {
		t.Error("expected amount_virtual.Generated.Stored = false")
	}
}

func TestBuildSimpleTable(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`users (
			id    BIGINT GENERATED ALWAYS AS IDENTITY,
			email TEXT NOT NULL,
			CONSTRAINT pk_users PRIMARY KEY (id)
		)`,
		``,
	)
	tbl, ok := obj.(*ir.Table)
	if !ok {
		t.Fatalf("expected *ir.Table, got %T", obj)
	}
	if tbl.Name != "users" {
		t.Errorf("Name: got %q", tbl.Name)
	}
	if len(tbl.Columns) != 2 {
		t.Errorf("Columns: expected 2, got %d", len(tbl.Columns))
	}
	if tbl.Columns[0].Name != "id" {
		t.Errorf("col[0].Name: got %q", tbl.Columns[0].Name)
	}
	if tbl.Columns[1].Name != "email" {
		t.Errorf("col[1].Name: got %q", tbl.Columns[1].Name)
	}
	if !tbl.Columns[1].NotNull {
		t.Error("email.NotNull: expected true")
	}
	if len(tbl.Constraints) != 1 {
		t.Errorf("Constraints: expected 1, got %d", len(tbl.Constraints))
	}
	if tbl.Constraints[0].Type != "PRIMARY KEY" {
		t.Errorf("constraint type: got %q", tbl.Constraints[0].Type)
	}
	if tbl.QualifiedName() != "public.users" {
		t.Errorf("QualifiedName: got %q", tbl.QualifiedName())
	}
}

func TestBuildTableWithBlock(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`users (
			id    BIGINT GENERATED ALWAYS AS IDENTITY,
			email TEXT NOT NULL
		)`,
		`
			COMMENT 'Primary user store';
			OWNER   "app_role";
			COLUMN email { COMMENT 'Email address'; STATISTICS 300; }
			INDICES { idx_email (email); }
			ENABLE ROW LEVEL SECURITY;
			GRANTS { SELECT TO app_readonly; }
		`,
	)
	tbl, ok := obj.(*ir.Table)
	if !ok {
		t.Fatalf("expected *ir.Table, got %T", obj)
	}
	if tbl.Comment == nil || *tbl.Comment != "Primary user store" {
		t.Errorf("Comment: got %v", tbl.Comment)
	}
	if tbl.Owner == nil || *tbl.Owner != "app_role" {
		t.Errorf("Owner: got %v", tbl.Owner)
	}
	if !tbl.RLSEnabled {
		t.Error("expected RLSEnabled")
	}
	if len(tbl.Indexes) != 1 || tbl.Indexes[0].Name != "idx_email" {
		t.Errorf("Indexes: got %v", tbl.Indexes)
	}
	if len(tbl.Grants) != 1 {
		t.Errorf("Grants: got %d", len(tbl.Grants))
	}
	// Column block merged in.
	emailCol := findCol(tbl.Columns, "email")
	if emailCol == nil {
		t.Fatal("email column not found")
	}
	if emailCol.Comment == nil || *emailCol.Comment != "Email address" {
		t.Errorf("email.Comment: got %v", emailCol.Comment)
	}
	if emailCol.Statistics == nil || *emailCol.Statistics != 300 {
		t.Errorf("email.Statistics: got %v", emailCol.Statistics)
	}
}

// TestBuildTableSubPartitioned guards RFC §7.13 sub-partitioning: a partition
// entry may itself carry a nested PARTITION BY clause and PARTITIONS { }
// block. Before this, the blockparser swallowed the whole nested clause as
// opaque raw "Bounds" text and there was no IR field to hold structured
// sub-partition data.
func TestBuildTableSubPartitioned(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`metrics (
			id         BIGINT GENERATED ALWAYS AS IDENTITY,
			created_at TIMESTAMPTZ NOT NULL,
			channel    TEXT NOT NULL
		) PARTITION BY RANGE (created_at)`,
		`
			PARTITIONS {
				metrics_2025 FOR VALUES FROM ('2025-01-01') TO ('2026-01-01');
				metrics_2026 FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')
					PARTITION BY LIST (channel) {
						PARTITIONS {
							metrics_2026_web   FOR VALUES IN ('web');
							metrics_2026_other FOR VALUES IN ('mobile', 'api');
						}
					};
			}
		`,
	)
	tbl, ok := obj.(*ir.Table)
	if !ok {
		t.Fatalf("expected *ir.Table, got %T", obj)
	}
	if tbl.PartitionBy == nil || tbl.PartitionBy.Strategy != "RANGE" {
		t.Fatalf("expected top-level PartitionBy RANGE, got %v", tbl.PartitionBy)
	}
	if len(tbl.Partitions) != 2 {
		t.Fatalf("expected 2 partitions, got %d", len(tbl.Partitions))
	}

	leaf := tbl.Partitions[0]
	if leaf.Name != "metrics_2025" || leaf.PartitionBy != nil || len(leaf.Partitions) != 0 {
		t.Errorf("metrics_2025: got %+v", leaf)
	}

	sub := tbl.Partitions[1]
	if sub.Name != "metrics_2026" {
		t.Errorf("partitions[1] name: got %q", sub.Name)
	}
	if sub.PartitionBy == nil || sub.PartitionBy.Strategy != "LIST" {
		t.Fatalf("metrics_2026: expected nested PartitionBy LIST, got %v", sub.PartitionBy)
	}
	if len(sub.PartitionBy.Columns) != 1 || sub.PartitionBy.Columns[0] != "channel" {
		t.Errorf("metrics_2026 sub-partition columns: got %v", sub.PartitionBy.Columns)
	}
	if len(sub.Partitions) != 2 {
		t.Fatalf("expected 2 nested sub-partitions, got %d", len(sub.Partitions))
	}
	if sub.Partitions[0].Name != "metrics_2026_web" {
		t.Errorf("sub-partition[0] name: got %q", sub.Partitions[0].Name)
	}
	if sub.Partitions[1].Bounds != "FOR VALUES IN ('mobile', 'api')" {
		t.Errorf("sub-partition[1] bounds: got %q", sub.Partitions[1].Bounds)
	}
}

// TestBuildForeignTableOptions guards a real bug found live-testing a demo
// project: buildForeignTable captured Foreign/ForeignServer but silently
// dropped OPTIONS (...) entirely — no field existed to hold it, so a
// declared FOREIGN TABLE's table_name/schema_name options were lost before
// they ever reached the differ.
func TestBuildForeignTableOptions(t *testing.T) {
	obj := buildObject(t, pipeline.KindForeignTable,
		`remote_users (
			id    BIGINT,
			email TEXT
		) SERVER loopback_server OPTIONS (table_name 'users', schema_name 'public')`,
		``,
	)
	tbl, ok := obj.(*ir.Table)
	if !ok {
		t.Fatalf("expected *ir.Table, got %T", obj)
	}
	if !tbl.Foreign {
		t.Error("expected Foreign = true")
	}
	if tbl.ForeignServer == nil || *tbl.ForeignServer != "loopback_server" {
		t.Errorf("ForeignServer: got %v", tbl.ForeignServer)
	}
	want := map[string]string{"table_name": "users", "schema_name": "public"}
	if len(tbl.ForeignOptions) != len(want) {
		t.Fatalf("ForeignOptions: expected %d entries, got %d: %v", len(want), len(tbl.ForeignOptions), tbl.ForeignOptions)
	}
	for _, p := range tbl.ForeignOptions {
		if want[p.Key] != p.Value {
			t.Errorf("ForeignOptions[%q]: got %q, want %q", p.Key, p.Value, want[p.Key])
		}
	}
}

func TestBuildSchemaQualifiedTable(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`billing.invoices (id BIGINT)`,
		``,
	)
	tbl := obj.(*ir.Table)
	if tbl.Schema != "billing" {
		t.Errorf("Schema: got %q", tbl.Schema)
	}
	if tbl.Name != "invoices" {
		t.Errorf("Name: got %q", tbl.Name)
	}
	if tbl.QualifiedName() != "billing.invoices" {
		t.Errorf("QualifiedName: got %q", tbl.QualifiedName())
	}
}

func TestBuildPrimaryKeyImpliesNotNull(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`facilities (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY)`,
		``,
	)
	tbl := obj.(*ir.Table)
	col := findCol(tbl.Columns, "id")
	if col == nil {
		t.Fatal("id column not found")
	}
	if !col.NotNull {
		t.Error("expected NotNull=true for inline PRIMARY KEY column")
	}
}

func TestBuildIdentityColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`orders (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY)`,
		``,
	)
	tbl := obj.(*ir.Table)
	col := findCol(tbl.Columns, "id")
	if col == nil {
		t.Fatal("id column not found")
	}
	if col.Identity == nil {
		t.Fatal("expected Identity spec")
	}
	if !col.Identity.Always {
		t.Error("expected Always = true")
	}
}

// ── View ──────────────────────────────────────────────────────────────────────

func TestBuildView(t *testing.T) {
	obj := buildObject(t, pipeline.KindView,
		`users_summary AS SELECT id, email FROM users`,
		`COMMENT 'Summary view'; GRANTS { SELECT TO app_readonly; }`,
	)
	v, ok := obj.(*ir.View)
	if !ok {
		t.Fatalf("expected *ir.View, got %T", obj)
	}
	if v.Name != "users_summary" {
		t.Errorf("Name: got %q", v.Name)
	}
	if v.Comment == nil || *v.Comment != "Summary view" {
		t.Errorf("Comment: got %v", v.Comment)
	}
	if len(v.Grants) != 1 {
		t.Errorf("Grants: got %d", len(v.Grants))
	}
}

// TestBuildRecursiveViewSetsFlag guards RFC audit item #27: buildView's
// caller in Build() hardcoded recursive=false unconditionally, never reading
// the scanner's already-correct pipeline.KindRecursiveView detection — so
// ir.View.Recursive could never be true via real compilation even though the
// scanner correctly classified "RECURSIVE VIEW" syntax. Mirrors
// TestBuildUnloggedTableSetsFlag's shape (same "field exists, dispatch never
// wires it" bug pattern).
func TestBuildRecursiveViewSetsFlag(t *testing.T) {
	obj := buildObject(t, pipeline.KindRecursiveView,
		`t(n) AS (VALUES (1) UNION ALL SELECT n+1 FROM t WHERE n < 100)`,
		``,
	)
	v, ok := obj.(*ir.View)
	if !ok {
		t.Fatalf("expected *ir.View, got %T", obj)
	}
	if !v.Recursive {
		t.Error("expected Recursive = true")
	}
}

// TestBuildMaterializedView guards a real bug found live-testing a demo
// project: pg_query parses CREATE MATERIALIZED VIEW as CreateTableAsStmt
// (Objtype == OBJECT_MATVIEW), not ViewStmt like a plain CREATE VIEW —
// PostgreSQL's own grammar implements it as a CREATE TABLE AS variant.
// Build's switch had no case for CreateTableAsStmt at all, so every
// MATERIALIZED VIEW silently fell through to the generic default case (an
// empty, nameless OpaqueObject) — confirmed live: `dpg plan` reported
// "-- (no changes)" for a newly-declared MATERIALIZED VIEW instead of a
// CREATE, because the malformed object's QualifiedName() was "" and never
// matched anything, in either direction of the diff.
// TestBuildTablespaceGrantsAndRevocations, TestBuildFDWGrantsAndRevocations,
// and TestBuildServerGrantsAndRevocations guard RFC audit items #4/#5/#6:
// ir.Tablespace/ForeignDataWrapper/ForeignServer had no Grants/Revocations
// field at all — the same missing-field shape as the already-fixed
// Schema-grants pattern, never generalized to these three kinds.
func TestBuildTablespaceGrantsAndRevocations(t *testing.T) {
	obj := buildObject(t, pipeline.KindTablespace,
		`gl_ts LOCATION '/var/lib/postgresql/ts1'`,
		`GRANTS { CREATE TO app_service; } REVOCATIONS { CREATE FROM PUBLIC; }`,
	)
	ts, ok := obj.(*ir.Tablespace)
	if !ok {
		t.Fatalf("expected *ir.Tablespace, got %T", obj)
	}
	if len(ts.Grants) != 1 {
		t.Fatalf("Grants: got %d", len(ts.Grants))
	}
	if len(ts.Revocations) != 1 {
		t.Fatalf("Revocations: got %d", len(ts.Revocations))
	}
}

// TestBuildTablespaceOwner guards RFC audit item #80: buildTablespace never
// read the inline OWNER clause at all, discarding it at CREATE time even
// though it's valid real PostgreSQL CREATE TABLESPACE grammar.
func TestBuildTablespaceOwner(t *testing.T) {
	obj := buildObject(t, pipeline.KindTablespace,
		`gl_ts OWNER app_admin LOCATION '/var/lib/postgresql/ts1'`,
		``,
	)
	ts, ok := obj.(*ir.Tablespace)
	if !ok {
		t.Fatalf("expected *ir.Tablespace, got %T", obj)
	}
	if ts.Owner == nil || *ts.Owner != "app_admin" {
		t.Errorf("Owner: got %v, want app_admin", ts.Owner)
	}
}

// TestBuildTablespaceRenamedFrom guards RFC audit item #80's other half.
func TestBuildTablespaceRenamedFrom(t *testing.T) {
	obj := buildObject(t, pipeline.KindTablespace,
		`gl_ts_new LOCATION '/var/lib/postgresql/ts1'`,
		`RENAMED FROM gl_ts_old;`,
	)
	ts, ok := obj.(*ir.Tablespace)
	if !ok {
		t.Fatalf("expected *ir.Tablespace, got %T", obj)
	}
	if ts.RenamedFrom == nil || *ts.RenamedFrom != "gl_ts_old" {
		t.Errorf("RenamedFrom: got %v, want gl_ts_old", ts.RenamedFrom)
	}
}

// TestBuildTablespaceOwnerUnspecified proves Owner stays nil when the
// source never mentions OWNER.
func TestBuildTablespaceOwnerUnspecified(t *testing.T) {
	obj := buildObject(t, pipeline.KindTablespace,
		`gl_ts LOCATION '/var/lib/postgresql/ts1'`,
		``,
	)
	ts, ok := obj.(*ir.Tablespace)
	if !ok {
		t.Fatalf("expected *ir.Tablespace, got %T", obj)
	}
	if ts.Owner != nil {
		t.Errorf("Owner: got %v, want nil", ts.Owner)
	}
}

func TestBuildFDWGrantsAndRevocations(t *testing.T) {
	obj := buildObject(t, pipeline.KindFDW,
		`gl_fdw HANDLER file_fdw_handler`,
		`GRANTS { USAGE TO app_service; } REVOCATIONS { USAGE FROM PUBLIC; }`,
	)
	f, ok := obj.(*ir.ForeignDataWrapper)
	if !ok {
		t.Fatalf("expected *ir.ForeignDataWrapper, got %T", obj)
	}
	if len(f.Grants) != 1 {
		t.Fatalf("Grants: got %d", len(f.Grants))
	}
	if len(f.Revocations) != 1 {
		t.Fatalf("Revocations: got %d", len(f.Revocations))
	}
}

func TestBuildServerGrantsAndRevocations(t *testing.T) {
	obj := buildObject(t, pipeline.KindServer,
		`gl_srv FOREIGN DATA WRAPPER gl_fdw2`,
		`GRANTS { USAGE TO app_service; } REVOCATIONS { USAGE FROM PUBLIC; }`,
	)
	s, ok := obj.(*ir.ForeignServer)
	if !ok {
		t.Fatalf("expected *ir.ForeignServer, got %T", obj)
	}
	if len(s.Grants) != 1 {
		t.Fatalf("Grants: got %d", len(s.Grants))
	}
	if len(s.Revocations) != 1 {
		t.Fatalf("Revocations: got %d", len(s.Revocations))
	}
}

// TestBuildServerOwnerAndRenamedFrom guards RFC audit item #79: ForeignServer
// had no Owner or RenamedFrom field at all.
func TestBuildServerOwnerAndRenamedFrom(t *testing.T) {
	obj := buildObject(t, pipeline.KindServer,
		`gl_srv FOREIGN DATA WRAPPER gl_fdw2`,
		`OWNER app_admin; RENAMED FROM gl_srv_old;`,
	)
	s, ok := obj.(*ir.ForeignServer)
	if !ok {
		t.Fatalf("expected *ir.ForeignServer, got %T", obj)
	}
	if s.Owner == nil || *s.Owner != "app_admin" {
		t.Errorf("Owner: got %v, want app_admin", s.Owner)
	}
	if s.RenamedFrom == nil || *s.RenamedFrom != "gl_srv_old" {
		t.Errorf("RenamedFrom: got %v, want gl_srv_old", s.RenamedFrom)
	}
}

// TestBuildPublicationOwner guards RFC audit item #7: Publication had no
// Owner field at all — real PostgreSQL supports ALTER PUBLICATION ... OWNER
// TO, but nothing in the builder ever read the block's OWNER directive for
// it.
func TestBuildPublicationOwner(t *testing.T) {
	obj := buildObject(t, pipeline.KindPublication,
		`pub_all FOR ALL TABLES`,
		`OWNER app_admin;`,
	)
	pub, ok := obj.(*ir.Publication)
	if !ok {
		t.Fatalf("expected *ir.Publication, got %T", obj)
	}
	if pub.Owner == nil || *pub.Owner != "app_admin" {
		t.Errorf("Owner: got %v, want app_admin", pub.Owner)
	}
}

// TestBuildPublicationRenamedFrom guards RFC audit item #78.
func TestBuildPublicationRenamedFrom(t *testing.T) {
	obj := buildObject(t, pipeline.KindPublication,
		`pub_new FOR ALL TABLES`,
		`RENAMED FROM pub_old;`,
	)
	pub, ok := obj.(*ir.Publication)
	if !ok {
		t.Fatalf("expected *ir.Publication, got %T", obj)
	}
	if pub.RenamedFrom == nil || *pub.RenamedFrom != "pub_old" {
		t.Errorf("RenamedFrom: got %v, want pub_old", pub.RenamedFrom)
	}
}

func TestBuildMaterializedView(t *testing.T) {
	obj := buildObject(t, pipeline.KindMaterializedView,
		`order_status_summary AS SELECT status, count(*) AS order_count FROM orders GROUP BY status`,
		`COMMENT 'Order counts by status';`,
	)
	v, ok := obj.(*ir.View)
	if !ok {
		t.Fatalf("expected *ir.View, got %T", obj)
	}
	if v.Name != "order_status_summary" {
		t.Errorf("Name: got %q, want %q", v.Name, "order_status_summary")
	}
	if !v.Materialized {
		t.Error("Materialized: got false, want true")
	}
	if v.Comment == nil || *v.Comment != "Order counts by status" {
		t.Errorf("Comment: got %v", v.Comment)
	}
	if v.Query == "" {
		t.Error("Query: got empty, want the deparsed SELECT")
	}
}

// TestBuildMaterializedViewWithNoData guards the WITH NO DATA clause, which
// has no ViewStmt counterpart at all (it's CreateTableAsStmt.Into.SkipData) —
// a distinct field from anything buildView already handled.
func TestBuildMaterializedViewWithNoData(t *testing.T) {
	obj := buildObject(t, pipeline.KindMaterializedView,
		`order_status_summary AS SELECT status, count(*) AS order_count FROM orders GROUP BY status WITH NO DATA`,
		``,
	)
	v := obj.(*ir.View)
	if !v.WithNoData {
		t.Error("WithNoData: got false, want true")
	}
}

// TestBuildMaterializedViewIndices guards RFC §8.2's matview-block: a
// materialized view's { } block MAY contain INDICES { } — previously
// entirely unimplemented (no IR field existed to hold it, and the
// blockparser's generically-parsed block.Indices was just never read here).
func TestBuildMaterializedViewIndices(t *testing.T) {
	obj := buildObject(t, pipeline.KindMaterializedView,
		`order_status_summary AS SELECT status, count(*) AS order_count FROM orders GROUP BY status`,
		`INDICES { UNIQUE order_status_summary_status_uq (status); }`,
	)
	v := obj.(*ir.View)
	if len(v.Indexes) != 1 {
		t.Fatalf("Indexes: got %d, want 1", len(v.Indexes))
	}
	idx := v.Indexes[0]
	if idx.Name != "order_status_summary_status_uq" || !idx.Unique {
		t.Errorf("Indexes[0]: got %+v", idx)
	}
}

// TestBuildViewIndicesRejected guards the RFC's own restriction: INDICES is
// only valid in a MATERIALIZED VIEW's block (matview-directive), never a
// plain or recursive VIEW's (view-directive omits indices-block) — real
// PostgreSQL doesn't support indexes on a non-materialized view at all, so
// silently accepting (and then dropping) this would be worse than erroring.
func TestBuildViewIndicesRejected(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindView,
		`active_users AS SELECT id FROM users WHERE active`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	bp := blockparser.New()
	blockAST, err := bp.Parse(pipeline.KindView,
		`INDICES { idx_active (id); }`, zeroPos)
	if err != nil {
		t.Fatalf("block parse error: %v", err)
	}
	builder := ir.NewBuilder()
	_, err = builder.Build(pgResult, blockAST)
	if err == nil {
		t.Fatal("expected an error declaring INDICES on a plain VIEW, got nil")
	}
	if !strings.Contains(err.Error(), "INDICES") || !strings.Contains(err.Error(), "MATERIALIZED VIEW") {
		t.Errorf("error should mention INDICES and MATERIALIZED VIEW, got: %v", err)
	}
}

// ── Enum ──────────────────────────────────────────────────────────────────────

func TestBuildEnum(t *testing.T) {
	obj := buildObject(t, pipeline.KindEnum,
		`status AS ENUM ('active', 'pending', 'inactive')`,
		`COMMENT 'User lifecycle states';`,
	)
	tp, ok := obj.(*ir.Type)
	if !ok {
		t.Fatalf("expected *ir.Type, got %T", obj)
	}
	if tp.Variant != "ENUM" {
		t.Errorf("Variant: got %q", tp.Variant)
	}
	if tp.Name != "status" {
		t.Errorf("Name: got %q", tp.Name)
	}
	if len(tp.EnumValues) != 3 {
		t.Errorf("EnumValues: got %d", len(tp.EnumValues))
	}
	if tp.Comment == nil || *tp.Comment != "User lifecycle states" {
		t.Errorf("Comment: got %v", tp.Comment)
	}
}

// TestBuildEnumGrantsAndRevocations guards RFC audit item #3: ir.Type had
// no Grants/Revocations field at all, for any of the 5 variants — a
// declared GRANT/REVOCATION in a TYPE block was silently discarded.
func TestBuildEnumGrantsAndRevocations(t *testing.T) {
	obj := buildObject(t, pipeline.KindEnum,
		`status AS ENUM ('active', 'pending', 'inactive')`,
		`GRANTS { USAGE TO app_service; } REVOCATIONS { USAGE FROM PUBLIC; }`,
	)
	tp := obj.(*ir.Type)
	if len(tp.Grants) != 1 {
		t.Fatalf("Grants: got %d", len(tp.Grants))
	}
	if len(tp.Revocations) != 1 {
		t.Fatalf("Revocations: got %d", len(tp.Revocations))
	}
	if len(tp.Revocations[0].Roles) != 1 || tp.Revocations[0].Roles[0] != "PUBLIC" {
		t.Errorf("Revocations[0].Roles: got %v", tp.Revocations[0].Roles)
	}
}

// TestBuildCompositeType guards a real bug found live-testing a demo
// project, the same shape as TestBuildMaterializedView above: pg_query
// parses CREATE TYPE name AS (attr type, ...) as its own distinct node,
// CompositeTypeStmt — unlike CREATE TYPE name AS ENUM (...), which parses as
// CreateEnumStmt. Build's switch had no case for CompositeTypeStmt at all,
// so every composite type declaration silently fell through to the generic
// default case (an empty, nameless OpaqueObject), even though ir.Type
// already had a "COMPOSITE" Variant and CompositeAttrs field fully wired
// through differ.go and snapshot/convert.go — just never reachable from
// source. Confirmed live: `dpg plan` reported "-- (no changes)" for a
// newly-declared composite TYPE instead of a CREATE.
func TestBuildCompositeType(t *testing.T) {
	obj := buildObject(t, pipeline.KindCompositeType,
		`address AS (street text, city text, zip text)`,
		`COMMENT 'A postal address';`,
	)
	tp, ok := obj.(*ir.Type)
	if !ok {
		t.Fatalf("expected *ir.Type, got %T", obj)
	}
	if tp.Variant != "COMPOSITE" {
		t.Errorf("Variant: got %q, want %q", tp.Variant, "COMPOSITE")
	}
	if tp.Name != "address" {
		t.Errorf("Name: got %q, want %q", tp.Name, "address")
	}
	if len(tp.CompositeAttrs) != 3 {
		t.Fatalf("CompositeAttrs: got %d, want 3", len(tp.CompositeAttrs))
	}
	wantNames := []string{"street", "city", "zip"}
	for i, want := range wantNames {
		if tp.CompositeAttrs[i].Name != want {
			t.Errorf("CompositeAttrs[%d].Name: got %q, want %q", i, tp.CompositeAttrs[i].Name, want)
		}
		if tp.CompositeAttrs[i].Type.Name != "text" {
			t.Errorf("CompositeAttrs[%d].Type: got %q, want %q", i, tp.CompositeAttrs[i].Type.Name, "text")
		}
	}
	if tp.Comment == nil || *tp.Comment != "A postal address" {
		t.Errorf("Comment: got %v", tp.Comment)
	}
}

// TestBuildCompositeTypeColumnBlockSetsRenamedFrom is the regression guard
// for RFC audit item #12: buildCompositeType built CompositeAttrs purely
// from the native CREATE TYPE ... AS ( ) coldeflist and never read
// block.Columns at all — a COLUMN attr { RENAMED FROM old; } sub-block (RFC
// §5.2: "the same [COLUMN] mechanism applies to composite type attributes")
// was silently discarded before it ever reached the differ, which then saw
// an unrelated drop+add of two differently-named attributes instead of a
// rename.
func TestBuildCompositeTypeColumnBlockSetsRenamedFrom(t *testing.T) {
	obj := buildObject(t, pipeline.KindCompositeType,
		`address AS (new_name text, city text)`,
		`COLUMN new_name { RENAMED FROM old_name; }`,
	)
	tp := obj.(*ir.Type)
	if len(tp.CompositeAttrs) != 2 {
		t.Fatalf("CompositeAttrs: got %d, want 2", len(tp.CompositeAttrs))
	}
	attr := tp.CompositeAttrs[0]
	if attr.Name != "new_name" {
		t.Fatalf("CompositeAttrs[0].Name: got %q, want %q", attr.Name, "new_name")
	}
	if attr.RenamedFrom == nil || *attr.RenamedFrom != "old_name" {
		t.Errorf("CompositeAttrs[0].RenamedFrom: got %v, want %q", attr.RenamedFrom, "old_name")
	}
	if tp.CompositeAttrs[1].RenamedFrom != nil {
		t.Errorf("CompositeAttrs[1] (city) should have no RenamedFrom, got %v", tp.CompositeAttrs[1].RenamedFrom)
	}
}

// TestBuildCompositeTypeRejectsUnknownColumnBlock mirrors
// TestBuildTableRejectsUnknownColumnBlock for composite types: a COLUMN
// block naming an attribute absent from the type's ( ) list must be a build
// error, not a silently-invented phantom attribute.
func TestBuildCompositeTypeRejectsUnknownColumnBlock(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindCompositeType, `address AS (street text, city text)`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse: %v", err)
	}
	bp := blockparser.New()
	blockAST, err := bp.Parse(pipeline.KindCompositeType,
		`COLUMN streets { RENAMED FROM street; }`, zeroPos)
	if err != nil {
		t.Fatalf("block parse: %v", err)
	}
	_, err = ir.NewBuilder().Build(pgResult, blockAST)
	if err == nil {
		t.Fatal("expected build error for unknown COLUMN block target, got nil")
	}
	msg := err.Error()
	for _, want := range []string{`"streets"`, "street"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error to mention %s, got: %s", want, msg)
		}
	}
}

// TestBuildRangeType guards a real bug found live-testing a demo project,
// the same shape as TestBuildMaterializedView/TestBuildCompositeType above:
// CREATE TYPE name AS RANGE (options) parses as its own distinct node,
// CreateRangeStmt — the third of three CREATE TYPE variants pg_query splits
// into dedicated node types (ENUM -> CreateEnumStmt, composite ->
// CompositeTypeStmt, range -> CreateRangeStmt), none of which is DefineStmt.
// Build's switch had no case for CreateRangeStmt at all, so every range type
// declaration silently fell through to the generic default case (an empty,
// nameless OpaqueObject) — confirmed live: `dpg plan` reported
// "-- (no changes)" for a newly-declared range TYPE instead of a CREATE.
// Notably, buildDefineStmt already has dead "isRange" detection logic for a
// DefineStmt shape pg_query v6 simply never produces for this syntax.
func TestBuildRangeType(t *testing.T) {
	obj := buildObject(t, pipeline.KindRangeType,
		`price_range AS RANGE (SUBTYPE = numeric)`,
		`COMMENT 'A price range';`,
	)
	tp, ok := obj.(*ir.Type)
	if !ok {
		t.Fatalf("expected *ir.Type, got %T", obj)
	}
	if tp.Variant != "RANGE" {
		t.Errorf("Variant: got %q, want %q", tp.Variant, "RANGE")
	}
	if tp.Name != "price_range" {
		t.Errorf("Name: got %q, want %q", tp.Name, "price_range")
	}
	if tp.Body == "" {
		t.Error("Body: got empty, want the deparsed CREATE TYPE statement")
	}
	if tp.Comment == nil || *tp.Comment != "A price range" {
		t.Errorf("Comment: got %v", tp.Comment)
	}
}

// TestBuildBaseType guards a real bug found live-testing a demo project:
// unlike buildRangeType/buildDomain (both of which set Body: rawBody),
// buildDefineStmt's BASE branch set Variant = "BASE" but never assigned
// Body at all — a declared base (shell) type parsed successfully but
// carried an empty Body forever, which createType's default branch (and
// sourceBodyHash) both treat as "nothing to create/hash."
func TestBuildBaseType(t *testing.T) {
	obj := buildObject(t, pipeline.KindBaseType,
		`mytype (INPUT = mytype_in, OUTPUT = mytype_out, INTERNALLENGTH = 16)`,
		`COMMENT 'A custom base type';`,
	)
	tp, ok := obj.(*ir.Type)
	if !ok {
		t.Fatalf("expected *ir.Type, got %T", obj)
	}
	if tp.Variant != "BASE" {
		t.Errorf("Variant: got %q, want %q", tp.Variant, "BASE")
	}
	if tp.Body == "" {
		t.Error("Body: got empty, want the deparsed CREATE TYPE statement")
	}
	if tp.Comment == nil || *tp.Comment != "A custom base type" {
		t.Errorf("Comment: got %v", tp.Comment)
	}
}

// TestBuildDomainInlineSyntax guards RFC §5.4's structured domain diffing
// inputs (base type, DEFAULT, NOT NULL, CHECK) using real PostgreSQL's own
// inline CREATE DOMAIN syntax — the form this codebase's demo project
// already used before this fix (e.g. email_address), which buildDomain
// never extracted at all: only block.Comment was ever read, so diffType had
// nothing to compare a changed DEFAULT/constraint/NOT NULL against.
func TestBuildDomainInlineSyntax(t *testing.T) {
	obj := buildObject(t, pipeline.KindDomainType,
		`positive_integer AS integer DEFAULT 1 CONSTRAINT positive_only CHECK (VALUE > 0) NOT NULL`,
		``,
	)
	tp, ok := obj.(*ir.Type)
	if !ok {
		t.Fatalf("expected *ir.Type, got %T", obj)
	}
	if tp.Variant != "DOMAIN" {
		t.Errorf("Variant: got %q, want %q", tp.Variant, "DOMAIN")
	}
	if tp.DomainBaseType.Name != "integer" && tp.DomainBaseType.Name != "int4" {
		t.Errorf("DomainBaseType: got %q", tp.DomainBaseType.Name)
	}
	if tp.DomainDefault == nil || *tp.DomainDefault != "1" {
		t.Errorf("DomainDefault: got %v", tp.DomainDefault)
	}
	if !tp.DomainNotNull {
		t.Error("DomainNotNull: got false, want true")
	}
	if len(tp.DomainConstraints) != 1 || tp.DomainConstraints[0].Name != "positive_only" {
		t.Fatalf("DomainConstraints: got %+v", tp.DomainConstraints)
	}
	// pg_query's deparser lowercases the VALUE keyword; real PG treats it
	// case-insensitively, so this is cosmetic, not a bug.
	if !strings.Contains(strings.ToLower(tp.DomainConstraints[0].Expr), "value > 0") {
		t.Errorf("DomainConstraints[0].Expr: got %q", tp.DomainConstraints[0].Expr)
	}
}

// TestBuildDomainBlockSyntax guards the same extraction via RFC §5.4's own
// literal ABNF: DEFAULT/CONSTRAINT/NOT NULL declared in the { } block
// instead of inline after AS. Real PG's CreateDomainStmt parser only sees
// the bare "name AS basetype" in Part 1 here — this form is additive to,
// not a replacement for, the inline syntax TestBuildDomainInlineSyntax
// covers.
func TestBuildDomainBlockSyntax(t *testing.T) {
	obj := buildObject(t, pipeline.KindDomainType,
		`positive_integer AS integer`,
		`DEFAULT 1; CONSTRAINT positive_only CHECK (VALUE > 0); NOT NULL;`,
	)
	tp, ok := obj.(*ir.Type)
	if !ok {
		t.Fatalf("expected *ir.Type, got %T", obj)
	}
	if tp.DomainDefault == nil || *tp.DomainDefault != "1" {
		t.Errorf("DomainDefault: got %v", tp.DomainDefault)
	}
	if !tp.DomainNotNull {
		t.Error("DomainNotNull: got false, want true")
	}
	if len(tp.DomainConstraints) != 1 || tp.DomainConstraints[0].Name != "positive_only" {
		t.Fatalf("DomainConstraints: got %+v", tp.DomainConstraints)
	}
	if !strings.Contains(tp.DomainConstraints[0].Expr, "VALUE > 0") {
		t.Errorf("DomainConstraints[0].Expr: got %q", tp.DomainConstraints[0].Expr)
	}
}

// TestBuildDomainGrantsAndRevocations is TestBuildEnumGrantsAndRevocations's
// DOMAIN counterpart (RFC audit item #3) — worth its own test since DOMAIN
// has a distinct builder function (buildDomain) from ENUM's (buildEnum).
func TestBuildDomainGrantsAndRevocations(t *testing.T) {
	obj := buildObject(t, pipeline.KindDomainType,
		`positive_integer AS integer`,
		`GRANTS { USAGE TO app_service; } REVOCATIONS { USAGE FROM PUBLIC; }`,
	)
	tp := obj.(*ir.Type)
	if len(tp.Grants) != 1 {
		t.Fatalf("Grants: got %d", len(tp.Grants))
	}
	if len(tp.Revocations) != 1 {
		t.Fatalf("Revocations: got %d", len(tp.Revocations))
	}
}

// ── Schema ────────────────────────────────────────────────────────────────────

func TestBuildSchema(t *testing.T) {
	obj := buildObject(t, pipeline.KindSchema,
		`billing`,
		`OWNER "finance_role"; COMMENT 'Billing schema';`,
	)
	s, ok := obj.(*ir.Schema)
	if !ok {
		t.Fatalf("expected *ir.Schema, got %T", obj)
	}
	if s.Name != "billing" {
		t.Errorf("Name: got %q", s.Name)
	}
	if s.Owner == nil || *s.Owner != "finance_role" {
		t.Errorf("Owner: got %v", s.Owner)
	}
}

// ── Function ─────────────────────────────────────────────────────────────────

func TestBuildFunction(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`add(a INT, b INT) RETURNS INT LANGUAGE sql AS $$ SELECT a + b $$;`,
		`COMMENT 'Adds two integers';`,
	)
	fn, ok := obj.(*ir.Function)
	if !ok {
		t.Fatalf("expected *ir.Function, got %T", obj)
	}
	if fn.Name != "add" {
		t.Errorf("Name: got %q", fn.Name)
	}
	if len(fn.Args) != 2 {
		t.Errorf("Args: got %d", len(fn.Args))
	}
	if fn.Attrs.Language != "sql" {
		t.Errorf("Language: got %q", fn.Attrs.Language)
	}
	if fn.BodyHash == "" {
		t.Error("expected non-empty BodyHash")
	}
	if fn.Comment == nil || *fn.Comment != "Adds two integers" {
		t.Errorf("Comment: got %v", fn.Comment)
	}
}

// TestBuildFunctionGrantsAndRevocations guards a real bug found live-testing
// REVOCATIONS against a demo project: ir.Function had no Revocations field at
// all, so a declared REVOCATIONS block was parsed generically by the
// blockparser but silently dropped by buildFunction — only the GRANT half of
// the block ever reached the IR.
func TestBuildFunctionGrantsAndRevocations(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`add(a INT, b INT) RETURNS INT LANGUAGE sql AS $$ SELECT a + b $$;`,
		`GRANTS { EXECUTE TO app_service; } REVOCATIONS { EXECUTE FROM PUBLIC; }`,
	)
	fn, ok := obj.(*ir.Function)
	if !ok {
		t.Fatalf("expected *ir.Function, got %T", obj)
	}
	if len(fn.Grants) != 1 {
		t.Fatalf("Grants: got %d", len(fn.Grants))
	}
	if len(fn.Revocations) != 1 {
		t.Fatalf("Revocations: got %d", len(fn.Revocations))
	}
	if len(fn.Revocations[0].Roles) != 1 || fn.Revocations[0].Roles[0] != "PUBLIC" {
		t.Errorf("Revocations[0].Roles: got %v", fn.Revocations[0].Roles)
	}
}

// TestBuildProcedureGrantsAndRevocations is TestBuildFunctionGrantsAndRevocations's
// PROCEDURE counterpart — ir.Procedure had the identical missing-field bug.
func TestBuildProcedureGrantsAndRevocations(t *testing.T) {
	obj := buildObject(t, pipeline.KindProcedure,
		`mark_order_paid(bigint) LANGUAGE plpgsql AS $$ BEGIN NULL; END; $$;`,
		`GRANTS { EXECUTE TO app_service; } REVOCATIONS { EXECUTE FROM PUBLIC; }`,
	)
	proc, ok := obj.(*ir.Procedure)
	if !ok {
		t.Fatalf("expected *ir.Procedure, got %T", obj)
	}
	if len(proc.Grants) != 1 {
		t.Fatalf("Grants: got %d", len(proc.Grants))
	}
	if len(proc.Revocations) != 1 {
		t.Fatalf("Revocations: got %d", len(proc.Revocations))
	}
	if len(proc.Revocations[0].Roles) != 1 || proc.Revocations[0].Roles[0] != "PUBLIC" {
		t.Errorf("Revocations[0].Roles: got %v", proc.Revocations[0].Roles)
	}
}

// TestBuildAggregateGrantsAndRevocations is TestBuildFunctionGrantsAndRevocations's
// AGGREGATE counterpart — ir.Aggregate had the identical missing-field bug
// (REVOCATIONS parsed generically by the blockparser but silently dropped by
// the AGGREGATE case in buildObject).
func TestBuildAggregateGrantsAndRevocations(t *testing.T) {
	obj := buildObject(t, pipeline.KindAggregate,
		`amount_product (numeric) (SFUNC = numeric_mul, STYPE = numeric, INITCOND = '1')`,
		`GRANTS { EXECUTE TO app_service; } REVOCATIONS { EXECUTE FROM PUBLIC; }`,
	)
	agg, ok := obj.(*ir.Aggregate)
	if !ok {
		t.Fatalf("expected *ir.Aggregate, got %T", obj)
	}
	if len(agg.Grants) != 1 {
		t.Fatalf("Grants: got %d", len(agg.Grants))
	}
	if len(agg.Revocations) != 1 {
		t.Fatalf("Revocations: got %d", len(agg.Revocations))
	}
	if len(agg.Revocations[0].Roles) != 1 || agg.Revocations[0].Roles[0] != "PUBLIC" {
		t.Errorf("Revocations[0].Roles: got %v", agg.Revocations[0].Roles)
	}
}

// TestBuildFunctionOwner guards RFC audit item #70: Function had no Owner
// field at all.
func TestBuildFunctionOwner(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`add(a INT, b INT) RETURNS INT LANGUAGE sql AS $$ SELECT a + b $$;`,
		`OWNER app_admin;`,
	)
	fn, ok := obj.(*ir.Function)
	if !ok {
		t.Fatalf("expected *ir.Function, got %T", obj)
	}
	if fn.Owner == nil || *fn.Owner != "app_admin" {
		t.Errorf("Owner: got %v, want app_admin", fn.Owner)
	}
}

// TestBuildProcedureOwner is TestBuildFunctionOwner's PROCEDURE counterpart.
func TestBuildProcedureOwner(t *testing.T) {
	obj := buildObject(t, pipeline.KindProcedure,
		`mark_order_paid(bigint) LANGUAGE plpgsql AS $$ BEGIN NULL; END; $$;`,
		`OWNER app_admin;`,
	)
	proc, ok := obj.(*ir.Procedure)
	if !ok {
		t.Fatalf("expected *ir.Procedure, got %T", obj)
	}
	if proc.Owner == nil || *proc.Owner != "app_admin" {
		t.Errorf("Owner: got %v, want app_admin", proc.Owner)
	}
}

// TestBuildAggregateBareFlagOptions guards RFC audit item #29:
// FINALFUNC_EXTRA/MFINALFUNC_EXTRA/HYPOTHETICAL are bare presence flags with
// no "= value" part — buildAggregateOptions previously required a non-nil
// DefElem.Arg for every option, silently dropping all three (a real
// data-loss bug on both diffing and dpg dump, not just an omission).
func TestBuildAggregateBareFlagOptions(t *testing.T) {
	obj := buildObject(t, pipeline.KindAggregate,
		`myrank (numeric) (SFUNC = numeric_add, STYPE = numeric, FINALFUNC_EXTRA, HYPOTHETICAL, PARALLEL = SAFE)`,
		``,
	)
	agg := obj.(*ir.Aggregate)
	keys := make(map[string]string, len(agg.Options))
	for _, p := range agg.Options {
		keys[p.Key] = p.Value
	}
	if v, ok := keys["finalfunc_extra"]; !ok || v != "" {
		t.Errorf("expected finalfunc_extra present with empty value, got ok=%v value=%q, options=%v", ok, v, agg.Options)
	}
	if v, ok := keys["hypothetical"]; !ok || v != "" {
		t.Errorf("expected hypothetical present with empty value, got ok=%v value=%q, options=%v", ok, v, agg.Options)
	}
	if v, ok := keys["parallel"]; !ok || v != "safe" {
		t.Errorf("expected parallel = safe, got ok=%v value=%q, options=%v", ok, v, agg.Options)
	}
}

// TestBuildAggregateOwner is TestBuildFunctionOwner's AGGREGATE counterpart.
func TestBuildAggregateOwner(t *testing.T) {
	obj := buildObject(t, pipeline.KindAggregate,
		`amount_product (numeric) (SFUNC = numeric_mul, STYPE = numeric, INITCOND = '1')`,
		`OWNER app_admin;`,
	)
	agg, ok := obj.(*ir.Aggregate)
	if !ok {
		t.Fatalf("expected *ir.Aggregate, got %T", obj)
	}
	if agg.Owner == nil || *agg.Owner != "app_admin" {
		t.Errorf("Owner: got %v, want app_admin", agg.Owner)
	}
}

// TestBuildProcedureDeprecatedAndRenamedFrom guards RFC audit items #8/#10:
// PROCEDURE never got Function's identical DEPRECATED/RENAMED FROM
// func-block directive support despite sharing the same grammar (RFC
// §9.3/9.4) — both were silently discarded (no field on ir.Procedure at
// all).
func TestBuildProcedureDeprecatedAndRenamedFrom(t *testing.T) {
	obj := buildObject(t, pipeline.KindProcedure,
		`mark_order_paid(bigint) LANGUAGE plpgsql AS $$ BEGIN NULL; END; $$;`,
		`DEPRECATED 'use mark_order_settled instead'; RENAMED FROM mark_order_complete;`,
	)
	proc, ok := obj.(*ir.Procedure)
	if !ok {
		t.Fatalf("expected *ir.Procedure, got %T", obj)
	}
	if proc.Deprecated == nil || *proc.Deprecated != "use mark_order_settled instead" {
		t.Errorf("Deprecated: got %v", proc.Deprecated)
	}
	if proc.RenamedFrom == nil || *proc.RenamedFrom != "mark_order_complete" {
		t.Errorf("RenamedFrom: got %v", proc.RenamedFrom)
	}
}

// TestBuildAggregateDeprecatedAndRenamedFrom is
// TestBuildProcedureDeprecatedAndRenamedFrom's AGGREGATE counterpart (RFC
// audit items #9/#11).
func TestBuildAggregateDeprecatedAndRenamedFrom(t *testing.T) {
	obj := buildObject(t, pipeline.KindAggregate,
		`amount_product (numeric) (SFUNC = numeric_mul, STYPE = numeric, INITCOND = '1')`,
		`DEPRECATED 'use amount_sum instead'; RENAMED FROM amount_mul;`,
	)
	agg, ok := obj.(*ir.Aggregate)
	if !ok {
		t.Fatalf("expected *ir.Aggregate, got %T", obj)
	}
	if agg.Deprecated == nil || *agg.Deprecated != "use amount_sum instead" {
		t.Errorf("Deprecated: got %v", agg.Deprecated)
	}
	if agg.RenamedFrom == nil || *agg.RenamedFrom != "amount_mul" {
		t.Errorf("RenamedFrom: got %v", agg.RenamedFrom)
	}
}

// TestBuildFunctionParallelCostRows proves PARALLEL/COST/ROWS all parse
// correctly. COST/ROWS use pg_query's NumericOnly grammar production
// (Integer or Float node, never String — confirmed via a live probe before
// writing this test), unlike LANGUAGE/VOLATILITY/PARALLEL's plain
// identifier arguments.
func TestBuildFunctionParallelCostRows(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f(x INT) RETURNS INT LANGUAGE sql STABLE PARALLEL SAFE COST 500 ROWS 50 AS $$ SELECT x $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.Attrs.Parallel != "SAFE" {
		t.Errorf("Parallel: got %q, want SAFE", fn.Attrs.Parallel)
	}
	if fn.Attrs.Cost == nil || *fn.Attrs.Cost != 500 {
		t.Errorf("Cost: got %v, want 500", fn.Attrs.Cost)
	}
	if fn.Attrs.Rows == nil || *fn.Attrs.Rows != 50 {
		t.Errorf("Rows: got %v, want 50", fn.Attrs.Rows)
	}
}

// TestBuildFunctionFractionalCost proves a fractional COST value (a real,
// documented PostgreSQL grammar form — NumericOnly reduces to a Float node
// for FCONST, confirmed via a live probe) parses correctly, not just the
// integer-looking common case.
func TestBuildFunctionFractionalCost(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f() RETURNS INT LANGUAGE sql COST 500.5 AS $$ SELECT 1 $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.Attrs.Cost == nil || *fn.Attrs.Cost != 500.5 {
		t.Errorf("Cost: got %v, want 500.5", fn.Attrs.Cost)
	}
}

// TestBuildFunctionParallelCostRowsUnspecified proves Parallel defaults to
// PostgreSQL's own real default ("UNSAFE", matching what a live-introspected
// function with no explicit PARALLEL clause also reports), while Cost/Rows
// stay nil rather than defaulting to a guessed number — nil is what lets
// diffFunction distinguish "source doesn't mention it" from "source
// explicitly set it to a value that happens to match the default."
func TestBuildFunctionParallelCostRowsUnspecified(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f() RETURNS INT LANGUAGE sql AS $$ SELECT 1 $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.Attrs.Parallel != "UNSAFE" {
		t.Errorf("Parallel: got %q, want UNSAFE (PostgreSQL's own default)", fn.Attrs.Parallel)
	}
	if fn.Attrs.Cost != nil {
		t.Errorf("Cost: got %v, want nil (unspecified)", fn.Attrs.Cost)
	}
	if fn.Attrs.Rows != nil {
		t.Errorf("Rows: got %v, want nil (unspecified)", fn.Attrs.Rows)
	}
}

// TestBuildFunctionReturnsSetOf proves RETURNS SETOF <type> parses with
// ReturnType.SetOf true — pg_query's TypeName.Setof field was previously
// never read anywhere in the codebase (confirmed via a repo-wide grep), so
// DPG silently dropped SETOF from every function it compiled.
func TestBuildFunctionReturnsSetOf(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f(n INT) RETURNS SETOF INT LANGUAGE sql AS $$ SELECT n $$;`, ``)
	fn := obj.(*ir.Function)
	if !fn.ReturnType.SetOf {
		t.Error("ReturnType.SetOf: got false, want true")
	}
	if fn.ReturnType.Name != "integer" {
		t.Errorf("ReturnType.Name: got %q, want integer", fn.ReturnType.Name)
	}
}

// TestBuildFunctionReturnsPlainNotSetOf is the negative control: an ordinary
// scalar RETURNS clause must leave SetOf false, not true by some fluke of
// the shared typeNameToRef conversion.
func TestBuildFunctionReturnsPlainNotSetOf(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f(n INT) RETURNS INT LANGUAGE sql AS $$ SELECT n $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.ReturnType.SetOf {
		t.Error("ReturnType.SetOf: got true, want false for a plain scalar RETURNS")
	}
}

// TestBuildFunctionArgTypeNeverSetOf guards typeNameToRef's shared-conversion
// design: SetOf is a field on every parsed TypeName regardless of syntactic
// context, but PostgreSQL's grammar only ever sets it true when parsing a
// function's RETURNS clause — an argument's type must never pick it up.
func TestBuildFunctionArgTypeNeverSetOf(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f(n INT) RETURNS INT LANGUAGE sql AS $$ SELECT n $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.Args[0].Type.SetOf {
		t.Error("Args[0].Type.SetOf: got true, want false (SETOF is only valid on RETURNS)")
	}
}

// TestBuildColumnTypeModifiers guards three stacked bugs found live-testing
// a throwaway demo project (numeric(10,2)/varchar(50)/timestamptz(3) columns
// all silently lost their modifier in generated DDL):
//  1. typmodString read a typmod literal via Node.GetInteger(), but pg_query
//     wraps it in an A_Const node instead (confirmed via a direct pg_query.Parse
//     probe) — GetInteger() always returned nil, so the whole function always
//     returned "" regardless of type.
//  2. Once (1) was fixed, character/varchar's case incorrectly subtracted 4
//     from the value (a live-catalog atttypmod encoding quirk that does NOT
//     apply to the parse tree's plain literal typmod — this function is only
//     ever fed from source-parsed TypeName nodes, never a live atttypmod).
//  3. Once (1) and (2) were fixed, timestamptz/timetz's switch case only
//     matched their short internal name, but typeNameToRef always runs the
//     name through PGCatalogName first, which renames them to the long form
//     ("timestamp with time zone") before typmodString ever sees it — so the
//     case never matched and the modifier was dropped a third way.
//
// Also guards TypeRef.String() splicing the modifier in the right position
// for the "with time zone" family specifically: PostgreSQL requires
// "timestamp(3) with time zone", and errors on "timestamp with time
// zone(3)" — confirmed live via format_type() against a real column.
func TestBuildColumnTypeModifiers(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (
			a NUMERIC(10,2),
			b VARCHAR(50),
			c TIMESTAMPTZ(3),
			d TIME(2) WITH TIME ZONE
		)`,
		``,
	)
	tbl := obj.(*ir.Table)
	want := map[string]string{
		"a": "numeric(10,2)",
		"b": "character varying(50)",
		"c": "timestamp(3) with time zone",
		"d": "time(2) with time zone",
	}
	for _, col := range tbl.Columns {
		if w, ok := want[col.Name]; ok && col.Type.String() != w {
			t.Errorf("column %s: got %q, want %q", col.Name, col.Type.String(), w)
		}
	}
}

// ── SERIAL/BIGSERIAL/SMALLSERIAL ────────────────────────────────────────────

// TestBuildColumnSerial proves every SERIAL-family spelling normalizes
// Column.Type to the real underlying integer type while recording the
// original sugar kind on Column.Serial — mirrors how Identity is a sibling
// marker to Type rather than a replacement for it.
func TestBuildColumnSerial(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (
			a SERIAL,
			b SERIAL4,
			c BIGSERIAL,
			d SERIAL8,
			e SMALLSERIAL,
			f SERIAL2
		)`,
		``,
	)
	tbl := obj.(*ir.Table)
	want := map[string]struct {
		typeName string
		marker   string
	}{
		"a": {"integer", "SERIAL"},
		"b": {"integer", "SERIAL"},
		"c": {"bigint", "BIGSERIAL"},
		"d": {"bigint", "BIGSERIAL"},
		"e": {"smallint", "SMALLSERIAL"},
		"f": {"smallint", "SMALLSERIAL"},
	}
	if len(tbl.Columns) != len(want) {
		t.Fatalf("got %d columns, want %d", len(tbl.Columns), len(want))
	}
	for _, col := range tbl.Columns {
		w, ok := want[col.Name]
		if !ok {
			t.Fatalf("unexpected column %s", col.Name)
		}
		if col.Type.Name != w.typeName {
			t.Errorf("column %s: Type.Name got %q, want %q", col.Name, col.Type.Name, w.typeName)
		}
		if col.Serial == nil || *col.Serial != w.marker {
			t.Errorf("column %s: Serial got %v, want %q", col.Name, col.Serial, w.marker)
		}
		if !col.NotNull {
			t.Errorf("column %s: NotNull got false, want true (SERIAL implies NOT NULL)", col.Name)
		}
		if col.Default != nil {
			t.Errorf("column %s: Default got %q, want nil", col.Name, *col.Default)
		}
	}
}

// TestBuildColumnSerialImpliesNotNullWithoutPK is a regression guard for a
// real live bug: a bare SERIAL column with no PRIMARY KEY and no explicit
// NOT NULL still must be NotNull==true in the IR, since PostgreSQL's real
// SERIAL macro-expansion implies NOT NULL independent of any PK. Before this
// fix, only the CONSTR_PRIMARY branch ever set NotNull=true, so a bare
// `qty SERIAL` column silently carried NotNull=false, causing permanent
// phantom `ALTER COLUMN SET NOT NULL` drift against a live catalog (where
// the column really is NOT NULL).
func TestBuildColumnSerialImpliesNotNullWithoutPK(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable, `t (qty SERIAL)`, ``)
	tbl := obj.(*ir.Table)
	if len(tbl.Columns) != 1 {
		t.Fatalf("got %d columns, want 1", len(tbl.Columns))
	}
	col := tbl.Columns[0]
	if !col.NotNull {
		t.Error("bare SERIAL column: NotNull got false, want true")
	}
	if col.Serial == nil || *col.Serial != "SERIAL" {
		t.Errorf("bare SERIAL column: Serial got %v, want \"SERIAL\"", col.Serial)
	}
}

// TestBuildColumnSerialPrimaryKey proves the common `id SERIAL PRIMARY KEY`
// shape still works end to end: Serial set, NotNull true, and the inline
// PRIMARY KEY still promotes to a table-level constraint as normal.
func TestBuildColumnSerialPrimaryKey(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable, `t (id SERIAL PRIMARY KEY)`, ``)
	tbl := obj.(*ir.Table)
	if len(tbl.Columns) != 1 {
		t.Fatalf("got %d columns, want 1", len(tbl.Columns))
	}
	col := tbl.Columns[0]
	if col.Type.Name != "integer" {
		t.Errorf("Type.Name got %q, want integer", col.Type.Name)
	}
	if col.Serial == nil || *col.Serial != "SERIAL" {
		t.Errorf("Serial got %v, want \"SERIAL\"", col.Serial)
	}
	if !col.NotNull {
		t.Error("NotNull got false, want true")
	}
	found := false
	for _, cst := range tbl.Constraints {
		if cst.Type == "PRIMARY KEY" {
			found = true
		}
	}
	if !found {
		t.Error("expected a promoted PRIMARY KEY constraint")
	}
}

// TestBuildColumnCustomTypeNamedSerialNotMistaken proves a schema-qualified
// reference to a real user type that happens to be named "serial" is never
// mistaken for the SERIAL pseudo-type — SERIAL is only ever written bare in
// real PostgreSQL DDL.
func TestBuildColumnCustomTypeNamedSerialNotMistaken(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable, `t (a myschema.serial)`, ``)
	tbl := obj.(*ir.Table)
	col := tbl.Columns[0]
	if col.Serial != nil {
		t.Errorf("Serial got %v, want nil for a schema-qualified custom type", col.Serial)
	}
	if col.Type.Name != "serial" || col.Type.Schema != "myschema" {
		t.Errorf("Type got {Schema:%q Name:%q}, want {Schema:myschema Name:serial}", col.Type.Schema, col.Type.Name)
	}
}

// TestBuildFunctionImplicitReturnsSingleOut proves that omitting RETURNS
// entirely (valid PostgreSQL when at least one OUT/INOUT parameter is
// present) infers the correct return type from that single OUT parameter —
// confirmed live against postgres:17 (pg_get_functiondef always reconstructs
// this as an explicit "RETURNS integer"), matching what a live-introspected
// version of the same function would report. Before this fix, cfs.ReturnType
// was nil (pg_query performs no semantic analysis) and fn.ReturnType stayed
// the zero TypeRef, producing a broken "RETURNS  LANGUAGE" (empty type) on
// buildFunctionSQL/dump.
func TestBuildFunctionImplicitReturnsSingleOut(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f_single_out(IN n INT, OUT a INT) LANGUAGE sql AS $$ SELECT n + 1 $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.ReturnType.Name != "integer" {
		t.Errorf("ReturnType.Name: got %q, want integer", fn.ReturnType.Name)
	}
	if fn.ReturnType.SetOf {
		t.Error("ReturnType.SetOf: got true, want false")
	}
}

// TestBuildFunctionImplicitReturnsMultiOut proves more than one OUT/INOUT
// parameter with no RETURNS clause infers "record" — confirmed live against
// postgres:17 (a 2-OUT-param function with no RETURNS reports
// pg_get_function_result = "record", proretset = false).
func TestBuildFunctionImplicitReturnsMultiOut(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f_multi_out(IN n INT, OUT a INT, OUT b TEXT) LANGUAGE sql AS $$ SELECT n, 'x' $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.ReturnType.Name != "record" {
		t.Errorf("ReturnType.Name: got %q, want record", fn.ReturnType.Name)
	}
	if fn.ReturnType.SetOf {
		t.Error("ReturnType.SetOf: got true, want false")
	}
}

// TestBuildFunctionImplicitReturnsInout proves a single INOUT parameter (not
// just OUT) also infers its own type as the return type — confirmed live
// against postgres:17.
func TestBuildFunctionImplicitReturnsInout(t *testing.T) {
	obj := buildObject(t, pipeline.KindFunction,
		`f_inout(INOUT n INT) LANGUAGE sql AS $$ SELECT n + 1 $$;`, ``)
	fn := obj.(*ir.Function)
	if fn.ReturnType.Name != "integer" {
		t.Errorf("ReturnType.Name: got %q, want integer", fn.ReturnType.Name)
	}
}

// ── Aggregate ─────────────────────────────────────────────────────────────────

// TestBuildAggregateArgs guards a real bug found live-testing a demo
// project: for CREATE AGGREGATE, pg_query's DefineStmt.Args is NOT a flat
// list of FunctionParameter nodes like a regular function's — Args[0] is
// itself a List node wrapping the actual input-type parameter(s), and
// Args[1] is an unrelated integer sentinel (-1 for a normal aggregate).
// Calling GetFunctionParameter() directly on the top-level Args elements (as
// the code previously did) always returned nil, so agg.Args silently stayed
// empty — the RFC's own worked example round-tripped with an empty "()"
// signature everywhere (CREATE AGGREGATE, COMMENT ON AGGREGATE, GRANT ON
// FUNCTION), referencing a different, nonexistent zero-arg aggregate.
func TestBuildAggregateArgs(t *testing.T) {
	obj := buildObject(t, pipeline.KindAggregate,
		`amount_product (numeric) (SFUNC = numeric_mul, STYPE = numeric, INITCOND = '1')`,
		`COMMENT 'multiplicative aggregate';`,
	)
	agg, ok := obj.(*ir.Aggregate)
	if !ok {
		t.Fatalf("expected *ir.Aggregate, got %T", obj)
	}
	if len(agg.Args) != 1 || agg.Args[0].Type.Name != "numeric" {
		t.Fatalf("Args: got %+v, want 1 arg of type numeric", agg.Args)
	}
	if agg.QualifiedName() != "public.amount_product(numeric)" {
		t.Errorf("QualifiedName: got %q", agg.QualifiedName())
	}
	if !strings.Contains(agg.Body, "(numeric)") {
		t.Errorf("Body should carry the (numeric) signature, got %q", agg.Body)
	}
}

// TestBuildAggregateOptions guards agg.Options, the structured (ordered)
// SFUNC/STYPE/INITCOND/... list dump needs to reconstruct DPG source syntax
// without re-parsing Body's raw "CREATE AGGREGATE ..." SQL text.
func TestBuildAggregateOptions(t *testing.T) {
	obj := buildObject(t, pipeline.KindAggregate,
		`amount_product (numeric) (SFUNC = numeric_mul, STYPE = numeric, INITCOND = '1')`,
		``,
	)
	agg := obj.(*ir.Aggregate)
	if len(agg.Options) != 3 {
		t.Fatalf("Options: got %d, want 3: %+v", len(agg.Options), agg.Options)
	}
	want := []struct{ key, val string }{
		{"sfunc", "numeric_mul"}, {"stype", "numeric"}, {"initcond", "'1'"},
	}
	for i, w := range want {
		if agg.Options[i].Key != w.key || agg.Options[i].Value != w.val {
			t.Errorf("Options[%d]: got %+v, want {%s %s}", i, agg.Options[i], w.key, w.val)
		}
	}
}

// ── Extension ─────────────────────────────────────────────────────────────────

func TestBuildExtension(t *testing.T) {
	obj := buildObject(t, pipeline.KindExtension, `pgcrypto`, ``)
	e, ok := obj.(*ir.Extension)
	if !ok {
		t.Fatalf("expected *ir.Extension, got %T", obj)
	}
	if e.Name != "pgcrypto" {
		t.Errorf("Name: got %q", e.Name)
	}
}

// TestBuildExtensionCascade guards RFC audit item #15: buildExtension never
// read pg_query's "cascade" DefElem, so a declared CASCADE was silently
// discarded — breaking the RFC's own canonical example
// ("EXTENSION pg_trgm CASCADE;") verbatim.
func TestBuildExtensionCascade(t *testing.T) {
	obj := buildObject(t, pipeline.KindExtension, `pg_trgm CASCADE`, ``)
	e, ok := obj.(*ir.Extension)
	if !ok {
		t.Fatalf("expected *ir.Extension, got %T", obj)
	}
	if !e.Cascade {
		t.Error("Cascade: got false, want true")
	}
}

// TestBuildExtensionCascadeUnspecified proves Cascade stays false when the
// source doesn't mention CASCADE at all.
func TestBuildExtensionCascadeUnspecified(t *testing.T) {
	obj := buildObject(t, pipeline.KindExtension, `pg_trgm`, ``)
	e := obj.(*ir.Extension)
	if e.Cascade {
		t.Error("Cascade: got true, want false")
	}
}

// ── Sequence ──────────────────────────────────────────────────────────────────

// TestBuildSequenceCycle is the regression guard for a bug found during a
// diff-package coverage push: pg_query represents CYCLE/NO CYCLE as a
// DefElem whose Arg is a Boolean node, not an Integer/A_Const like every
// other sequence option — buildSequence routed all options through
// seqOptionInt (which only handles Integer/A_Const), so CYCLE always
// silently evaluated to nil/unset regardless of what was written, no matter
// how the differ compared it.
func TestBuildSequenceCycle(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id CYCLE`, ``)
	s, ok := obj.(*ir.Sequence)
	if !ok {
		t.Fatalf("expected *ir.Sequence, got %T", obj)
	}
	if s.Cycle == nil {
		t.Fatal("Cycle: got nil, want non-nil (true)")
	}
	if !*s.Cycle {
		t.Error("Cycle: got false, want true")
	}
}

func TestBuildSequenceNoCycle(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id NO CYCLE`, ``)
	s := obj.(*ir.Sequence)
	if s.Cycle == nil {
		t.Fatal("Cycle: got nil, want non-nil (false)")
	}
	if *s.Cycle {
		t.Error("Cycle: got true, want false")
	}
}

// TestBuildSequenceCycleUnspecified proves Cycle stays nil (not false) when
// the source doesn't mention CYCLE/NO CYCLE at all — the nil/false
// distinction is what lets the differ tell "don't touch cycling" apart from
// "explicitly set to no cycle".
func TestBuildSequenceCycleUnspecified(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id INCREMENT BY 1`, ``)
	s := obj.(*ir.Sequence)
	if s.Cycle != nil {
		t.Errorf("Cycle: got %v, want nil", *s.Cycle)
	}
}

// TestBuildSequenceNoMinMaxValue guards RFC audit item #20: pg_query
// represents both "NO MINVALUE"/"NO MAXVALUE" and the option being omitted
// entirely as the identical nil-Arg DefElem shape, so MinValue/MaxValue
// alone (nil either way) can't tell them apart — the builder must capture
// the explicit-NO case in its own field.
func TestBuildSequenceNoMinMaxValue(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id NO MINVALUE NO MAXVALUE`, ``)
	s := obj.(*ir.Sequence)
	if s.MinValue != nil {
		t.Errorf("MinValue: got %v, want nil", *s.MinValue)
	}
	if !s.NoMinValue {
		t.Error("NoMinValue: got false, want true")
	}
	if s.MaxValue != nil {
		t.Errorf("MaxValue: got %v, want nil", *s.MaxValue)
	}
	if !s.NoMaxValue {
		t.Error("NoMaxValue: got false, want true")
	}
}

// TestBuildSequenceMinMaxValueUnspecified proves NoMinValue/NoMaxValue stay
// false (not true) when the source doesn't mention MINVALUE/MAXVALUE at
// all — mirrors TestBuildSequenceCycleUnspecified's reasoning.
func TestBuildSequenceMinMaxValueUnspecified(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id INCREMENT BY 1`, ``)
	s := obj.(*ir.Sequence)
	if s.NoMinValue {
		t.Error("NoMinValue: got true, want false")
	}
	if s.NoMaxValue {
		t.Error("NoMaxValue: got true, want false")
	}
}

func TestBuildSequenceAllOptions(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence,
		`seq_id INCREMENT BY 2 MINVALUE 1 MAXVALUE 100 START WITH 5 CACHE 10 CYCLE`, ``)
	s := obj.(*ir.Sequence)
	if s.IncrementBy == nil || *s.IncrementBy != 2 {
		t.Errorf("IncrementBy: got %v, want 2", s.IncrementBy)
	}
	if s.MinValue == nil || *s.MinValue != 1 {
		t.Errorf("MinValue: got %v, want 1", s.MinValue)
	}
	if s.MaxValue == nil || *s.MaxValue != 100 {
		t.Errorf("MaxValue: got %v, want 100", s.MaxValue)
	}
	if s.StartValue == nil || *s.StartValue != 5 {
		t.Errorf("StartValue: got %v, want 5", s.StartValue)
	}
	if s.Cache == nil || *s.Cache != 10 {
		t.Errorf("Cache: got %v, want 10", s.Cache)
	}
	if s.Cycle == nil || !*s.Cycle {
		t.Errorf("Cycle: got %v, want true", s.Cycle)
	}
}

// TestBuildSequenceAsTypeAndOwnedBy guards RFC audit item #14: sequence
// "AS type" and "OWNED BY" were completely unimplemented (no IR field, no
// builder handling) — breaking the RFC's own canonical example
// ("SEQUENCE ... AS BIGINT ... OWNED BY orders.order_number;") verbatim.
func TestBuildSequenceAsTypeAndOwnedBy(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence,
		`order_number_seq AS BIGINT OWNED BY orders.order_number`, ``)
	s := obj.(*ir.Sequence)
	if s.AsType == nil || s.AsType.Name != "bigint" {
		t.Errorf("AsType: got %v, want bigint", s.AsType)
	}
	if s.OwnedBy == nil || *s.OwnedBy != "orders.order_number" {
		t.Errorf("OwnedBy: got %v, want orders.order_number", s.OwnedBy)
	}
}

// TestBuildSequenceOwnedByNone proves the explicit "OWNED BY NONE" form
// parses to the "NONE" sentinel, distinct from OwnedBy being nil
// (unspecified).
func TestBuildSequenceOwnedByNone(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id OWNED BY NONE`, ``)
	s := obj.(*ir.Sequence)
	if s.OwnedBy == nil || *s.OwnedBy != "NONE" {
		t.Errorf("OwnedBy: got %v, want NONE", s.OwnedBy)
	}
}

// TestBuildSequenceRestartWith proves RESTART WITH n parses into Restart
// (true) and RestartWith (RFC audit item #68).
func TestBuildSequenceRestartWith(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id RESTART WITH 1000`, ``)
	s := obj.(*ir.Sequence)
	if !s.Restart {
		t.Error("Restart: got false, want true")
	}
	if s.RestartWith == nil || *s.RestartWith != 1000 {
		t.Errorf("RestartWith: got %v, want 1000", s.RestartWith)
	}
}

// TestBuildSequenceRestartBare proves a bare RESTART (no WITH n) sets
// Restart true with RestartWith left nil.
func TestBuildSequenceRestartBare(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id RESTART`, ``)
	s := obj.(*ir.Sequence)
	if !s.Restart {
		t.Error("Restart: got false, want true")
	}
	if s.RestartWith != nil {
		t.Errorf("RestartWith: got %v, want nil", s.RestartWith)
	}
}

// TestBuildSequenceRestartUnspecified proves Restart stays false when the
// source never mentions RESTART.
func TestBuildSequenceRestartUnspecified(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id INCREMENT BY 1`, ``)
	s := obj.(*ir.Sequence)
	if s.Restart {
		t.Error("Restart: got true, want false")
	}
}

// TestBuildSequenceAsTypeOwnedByUnspecified proves both fields stay nil
// when the source doesn't mention AS/OWNED BY at all.
func TestBuildSequenceAsTypeOwnedByUnspecified(t *testing.T) {
	obj := buildObject(t, pipeline.KindSequence, `seq_id INCREMENT BY 1`, ``)
	s := obj.(*ir.Sequence)
	if s.AsType != nil {
		t.Errorf("AsType: got %v, want nil", s.AsType)
	}
	if s.OwnedBy != nil {
		t.Errorf("OwnedBy: got %v, want nil", s.OwnedBy)
	}
}

// ── CHECK constraint column extraction ──────────────────────────────────────────

func findCheck(cs []*ir.Constraint, expr string) *ir.Constraint {
	for _, c := range cs {
		if c.Type == "CHECK" && strings.Contains(c.Expr, expr) {
			return c
		}
	}
	return nil
}

// TestBuildCheckConstraintSingleColumn mirrors PostgreSQL's own default-name
// selection for CHECK constraints (heap.c's AddRelationNewConstraints):
// when the expression references exactly one distinct column, CheckColumn
// must carry it (used only for reconstructing PG's auto-generated name).
func TestBuildCheckConstraintSingleColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a INTEGER, b INTEGER, CONSTRAINT chk CHECK (a > 0))`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "a > 0")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if c.CheckColumn == nil || *c.CheckColumn != "a" {
		t.Errorf("CheckColumn: got %v, want \"a\"", c.CheckColumn)
	}
}

func TestBuildCheckConstraintMultiColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a INTEGER, b INTEGER, CONSTRAINT chk CHECK (a > 0 AND b > 0))`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "a > 0")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if c.CheckColumn != nil {
		t.Errorf("CheckColumn: got %q, want nil (2 distinct columns referenced)", *c.CheckColumn)
	}
}

// TestBuildCheckConstraintDedupSameColumn proves a repeated reference to the
// SAME column still counts as exactly one distinct column, matching PG's
// list_union dedup in heap.c.
func TestBuildCheckConstraintDedupSameColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a INTEGER, CONSTRAINT chk CHECK (a > 0 AND a < 100))`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "a > 0")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if c.CheckColumn == nil || *c.CheckColumn != "a" {
		t.Errorf("CheckColumn: got %v, want \"a\" (deduped)", c.CheckColumn)
	}
}

func TestBuildCheckConstraintNoColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a INTEGER, CONSTRAINT chk CHECK (1 = 1))`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "1 = 1")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if c.CheckColumn != nil {
		t.Errorf("CheckColumn: got %q, want nil (no column referenced)", *c.CheckColumn)
	}
}

// TestBuildCheckConstraintNestedExpression proves the extraction walks
// arbitrarily nested expression nodes (CASE, function calls), not just a
// flat A_Expr — needed since a generic protoreflect walk, not a hand-picked
// set of node types, is what makes this robust.
func TestBuildCheckConstraintNestedExpression(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a INTEGER, CONSTRAINT chk CHECK (CASE WHEN a > 0 THEN true ELSE false END))`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "CASE")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if c.CheckColumn == nil || *c.CheckColumn != "a" {
		t.Errorf("CheckColumn: got %v, want \"a\"", c.CheckColumn)
	}
}

// TestBuildCheckConstraintPromotedColumnLevelSingleColumn proves an inline
// column-level CHECK gets a CheckColumn consistent with its (only) column,
// same as the existing syntactic-position-based Columns marker.
func TestBuildCheckConstraintPromotedColumnLevelSingleColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable, `t (a INTEGER CHECK (a > 0))`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "a > 0")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if len(c.Columns) != 1 || c.Columns[0] != "a" {
		t.Errorf("Columns: got %v, want [a]", c.Columns)
	}
	if c.CheckColumn == nil || *c.CheckColumn != "a" {
		t.Errorf("CheckColumn: got %v, want \"a\"", c.CheckColumn)
	}
}

// TestBuildCheckConstraintPromotedColumnLevelReferencesOtherColumn is the
// key divergence case: a column-level CHECK is free to reference OTHER
// columns too (valid SQL), and PG's real naming rule is expression-based,
// not position-based. Columns must stay the syntactic-position marker
// ([a], used only by createTable's inline-rendering decision) while
// CheckColumn must correctly reflect that 2 distinct columns are referenced
// (nil), not silently inherit the wrong single-column assumption.
func TestBuildCheckConstraintPromotedColumnLevelReferencesOtherColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a INTEGER CHECK (a > b), b INTEGER)`, ``)
	tbl := obj.(*ir.Table)
	c := findCheck(tbl.Constraints, "a > b")
	if c == nil {
		t.Fatal("CHECK constraint not found")
	}
	if len(c.Columns) != 1 || c.Columns[0] != "a" {
		t.Errorf("Columns: got %v, want [a] (syntactic-position marker, unaffected)", c.Columns)
	}
	if c.CheckColumn != nil {
		t.Errorf("CheckColumn: got %q, want nil (references 2 distinct columns)", *c.CheckColumn)
	}
}

// ── FOREIGN KEY structured ref fields ────────────────────────────────────────────

func findFK(cs []*ir.Constraint) *ir.Constraint {
	for _, c := range cs {
		if c.Type == "FOREIGN KEY" {
			return c
		}
	}
	return nil
}

// TestBuildConstraintForeignKeyRefColumns proves an inline column-level
// REFERENCES clause (promoted to a table-level constraint by buildColumn)
// populates the structured RefSchema/RefTable/RefColumns fields, not just
// the rendered Expr text — these exist specifically so a consumer like the
// deprecated-reference lint rule doesn't need to re-parse Expr.
func TestBuildConstraintForeignKeyRefColumns(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (id INTEGER, user_id INTEGER REFERENCES app.users (id))`, ``)
	tbl := obj.(*ir.Table)
	fk := findFK(tbl.Constraints)
	if fk == nil {
		t.Fatal("FK constraint not found")
	}
	if fk.RefSchema != "app" || fk.RefTable != "users" {
		t.Errorf("RefSchema/RefTable: got %q/%q, want app/users", fk.RefSchema, fk.RefTable)
	}
	if len(fk.RefColumns) != 1 || fk.RefColumns[0] != "id" {
		t.Errorf("RefColumns: got %v, want [id]", fk.RefColumns)
	}
}

// TestBuildConstraintForeignKeyRefColumnsTableLevelUnqualified covers the
// table-level CONSTRAINT ... FOREIGN KEY form (the other of the two FK
// build sites) and the unqualified-reference case: RefSchema must stay ""
// (never guessed) so a consumer resolves it against the referencing
// table's own schema itself, matching ir.TypeRef.Schema's convention.
func TestBuildConstraintForeignKeyRefColumnsTableLevelUnqualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (id INTEGER, org_id INTEGER, CONSTRAINT fk_org FOREIGN KEY (org_id) REFERENCES orgs (id))`, ``)
	tbl := obj.(*ir.Table)
	fk := findFK(tbl.Constraints)
	if fk == nil {
		t.Fatal("FK constraint not found")
	}
	if fk.RefSchema != "" {
		t.Errorf("RefSchema: got %q, want empty (unqualified reference)", fk.RefSchema)
	}
	if fk.RefTable != "orgs" {
		t.Errorf("RefTable: got %q, want orgs", fk.RefTable)
	}
	if len(fk.RefColumns) != 1 || fk.RefColumns[0] != "id" {
		t.Errorf("RefColumns: got %v, want [id]", fk.RefColumns)
	}
}

// ── EXCLUDE constraint round-tripping ────────────────────────────────────────────

func findExclude(cs []*ir.Constraint) *ir.Constraint {
	for _, c := range cs {
		if c.Type == "EXCLUDE" {
			return c
		}
	}
	return nil
}

// TestBuildExcludeBinaryGistWithWhere is the core regression guard: a
// realistic multi-element EXCLUDE (the canonical "no overlapping bookings"
// shape) must capture its access method, both elements' operators, and its
// WHERE predicate — not the old "EXCLUDE" placeholder, which would already
// fail to apply as invalid SQL.
func TestBuildExcludeBinaryGistWithWhere(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (room integer, during tsrange, CONSTRAINT no_overlap EXCLUDE USING gist (room WITH =, during WITH &&) WHERE (room > 0))`,
		``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude == nil {
		t.Fatal("Exclude spec not populated")
	}
	if c.Exclude.AccessMethod != "gist" {
		t.Errorf("AccessMethod: got %q, want gist", c.Exclude.AccessMethod)
	}
	if c.Exclude.Where != "room > 0" {
		t.Errorf("Where: got %q, want %q", c.Exclude.Where, "room > 0")
	}
	if len(c.Exclude.Elements) != 2 {
		t.Fatalf("Elements: got %d, want 2", len(c.Exclude.Elements))
	}
	if c.Exclude.Elements[0].Column != "room" || c.Exclude.Elements[0].Operator != "=" {
		t.Errorf("Elements[0]: got %+v, want Column=room Operator= =", c.Exclude.Elements[0])
	}
	if c.Exclude.Elements[1].Column != "during" || c.Exclude.Elements[1].Operator != "&&" {
		t.Errorf("Elements[1]: got %+v, want Column=during Operator=&&", c.Exclude.Elements[1])
	}
	if len(c.Columns) != 2 || c.Columns[0] != "room" || c.Columns[1] != "during" {
		t.Errorf("Columns: got %v, want [room during]", c.Columns)
	}
	wantExpr := `EXCLUDE USING gist ("room" WITH =, "during" WITH &&) WHERE (room > 0)`
	if c.Expr != wantExpr {
		t.Errorf("Expr: got %q, want %q", c.Expr, wantExpr)
	}
}

// TestBuildExcludeDefaultsToBtreeAccessMethod proves an EXCLUDE with no
// USING clause still gets a real access method ("btree", PostgreSQL's
// grammar-level default — confirmed live: PG itself both accepts
// "EXCLUDE (a WITH =)" with no USING and reports
// "EXCLUDE USING btree (a WITH =)" back via pg_get_constraintdef), not an
// empty one that would render invalid/ambiguous SQL.
func TestBuildExcludeDefaultsToBtreeAccessMethod(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE (a WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.AccessMethod != "btree" {
		t.Errorf("AccessMethod: got %q, want btree", c.Exclude.AccessMethod)
	}
	wantExpr := `EXCLUDE USING btree ("a" WITH =)`
	if c.Expr != wantExpr {
		t.Errorf("Expr: got %q, want %q", c.Expr, wantExpr)
	}
}

// TestBuildExcludeExpressionElement proves an element that's an expression
// (parenthesized, not a bare column) rather than a plain column reference
// round-trips too — EXCLUDE elements follow the same IndexElem grammar as
// CREATE INDEX, which allows either.
func TestBuildExcludeExpressionElement(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a text, CONSTRAINT c EXCLUDE ((lower(a)) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if len(c.Exclude.Elements) != 1 {
		t.Fatalf("Elements: got %d, want 1", len(c.Exclude.Elements))
	}
	el := c.Exclude.Elements[0]
	if el.Column != "" {
		t.Errorf("Column: got %q, want empty (expression element)", el.Column)
	}
	if el.Expr != "lower(a)" {
		t.Errorf("Expr: got %q, want %q", el.Expr, "lower(a)")
	}
	if len(c.Columns) != 0 {
		t.Errorf("Columns: got %v, want empty (no plain-column elements)", c.Columns)
	}
	wantExpr := `EXCLUDE USING btree ((lower(a)) WITH =)`
	if c.Expr != wantExpr {
		t.Errorf("Expr: got %q, want %q", c.Expr, wantExpr)
	}
}

// TestBuildExcludeCollationOpclassSortNulls proves the full IndexElem
// attribute set — COLLATE, opclass, sort direction, NULLS placement — is
// captured, in PostgreSQL's own rendering order.
func TestBuildExcludeCollationOpclassSortNulls(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a text, CONSTRAINT c EXCLUDE (a COLLATE "C" text_ops DESC NULLS LAST WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	el := c.Exclude.Elements[0]
	if el.Collation != "C" {
		t.Errorf("Collation: got %q, want C", el.Collation)
	}
	if el.OpClass != "text_ops" {
		t.Errorf("OpClass: got %q, want text_ops", el.OpClass)
	}
	if el.SortOrder != "DESC" {
		t.Errorf("SortOrder: got %q, want DESC", el.SortOrder)
	}
	if el.Nulls != "LAST" {
		t.Errorf("Nulls: got %q, want LAST", el.Nulls)
	}
	wantExpr := `EXCLUDE USING btree ("a" COLLATE C text_ops DESC NULLS LAST WITH =)`
	if c.Expr != wantExpr {
		t.Errorf("Expr: got %q, want %q", c.Expr, wantExpr)
	}
}

// TestBuildExcludeOperatorSchemaQualificationStripped proves an explicitly
// schema-qualified built-in operator (OPERATOR(pg_catalog.=)) renders
// without the redundant "pg_catalog." prefix, mirroring PGCatalogName's
// identical treatment of built-in type names.
func TestBuildExcludeOperatorSchemaQualificationStripped(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE (a WITH OPERATOR(pg_catalog.=)))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].Operator != "=" {
		t.Errorf("Operator: got %q, want = (pg_catalog. prefix stripped)", c.Exclude.Elements[0].Operator)
	}
}

// TestBuildExcludeFuncCallElementPredictedName proves a bare, top-level
// function-call element captures its function name — needed to predict
// PostgreSQL's real auto-generated constraint name for such an element
// (verified live: PG's own algorithm uses only the bare function name for
// this shape, never descending into the call's arguments).
func TestBuildExcludeFuncCallElementPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a text, CONSTRAINT c EXCLUDE ((lower(a)) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "lower" {
		t.Errorf("PredictedName: got %q, want lower", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeFuncCallElementMultiArgPredictedName proves the function
// name is captured regardless of how many arguments the call has (verified
// live: PostgreSQL's algorithm never appends argument-derived text for a
// function-call element).
func TestBuildExcludeFuncCallElementMultiArgPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (ts timestamp, CONSTRAINT c EXCLUDE ((date_trunc('day', ts)) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "date_trunc" {
		t.Errorf("PredictedName: got %q, want date_trunc", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeFuncCallElementSchemaQualifiedPredictedName proves a
// schema-qualified function call's PredictedName is the bare function name
// only — matching PostgreSQL's get_func_name, which never includes the
// schema (verified live for both pg_catalog and a user-defined schema).
func TestBuildExcludeFuncCallElementSchemaQualifiedPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((myschema.myfunc(a)) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "myfunc" {
		t.Errorf("PredictedName: got %q, want myfunc (schema stripped)", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeBareOperatorElementPredictedNameEmpty proves PredictedName
// stays empty for a bare, uncast operator expression — PostgreSQL's real
// generated name for this shape is the literal "expr" (verified live), a
// constant this tool does not attempt to reproduce (it carries no
// information about the actual expression, so hardcoding it would be pure
// guesswork dressed up as a prediction, not meaningfully better than not
// predicting at all).
func TestBuildExcludeBareOperatorElementPredictedNameEmpty(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, b integer, CONSTRAINT c EXCLUDE ((a + b) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "" {
		t.Errorf("PredictedName: got %q, want empty (bare operator, no cast)", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeParenthesizedColumnPredictedName proves a bare column
// wrapped in redundant parens — "(a)" rather than "a" — still predicts
// correctly, matching PostgreSQL's own ColumnRef handling (verified live:
// EXCLUDE ((a) WITH =) generates the same name as EXCLUDE (a WITH =) would).
func TestBuildExcludeParenthesizedColumnPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((a) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "a" {
		t.Errorf("PredictedName: got %q, want a", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeNullifElementPredictedName proves NULLIF(a, b) predicts
// the literal "nullif" — PostgreSQL's own FigureColnameInternal special-cases
// NULLIF to act like a regular function call for naming purposes (verified
// live: EXCLUDE ((nullif(a,b)) WITH =) generates "..._nullif_excl").
func TestBuildExcludeNullifElementPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, b integer, CONSTRAINT c EXCLUDE ((nullif(a, b)) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "nullif" {
		t.Errorf("PredictedName: got %q, want nullif", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCastOverColumnPredictedName proves a cast wrapping a
// plain column predicts the COLUMN's name, not the cast's target type —
// PostgreSQL's real algorithm prefers a "strong" name (a column or function
// call) from the cast's argument over its own type name (verified live:
// EXCLUDE ((a::text) WITH =) generates "..._a_excl", not "..._text_excl").
func TestBuildExcludeCastOverColumnPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((a::text) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "a" {
		t.Errorf("PredictedName: got %q, want a (the column, not the cast type)", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCastOverFuncCallPredictedName proves a cast wrapping a
// function call ALSO prefers the function's name over the cast's target
// type (verified live: EXCLUDE ((lower(a)::text) WITH =) generates
// "..._lower_excl", not "..._text_excl").
func TestBuildExcludeCastOverFuncCallPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a text, CONSTRAINT c EXCLUDE ((lower(a)::text) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "lower" {
		t.Errorf("PredictedName: got %q, want lower (the function, not the cast type)", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCastOverOperatorPredictedNameFallsBackToType is the key
// divergence case: a cast wrapping a BARE OPERATOR expression (which alone
// predicts nothing at all — see
// TestBuildExcludeBareOperatorElementPredictedNameEmpty) falls back to the
// cast's OWN target type name, since the operator gave nothing "strong" to
// prefer instead (verified live: EXCLUDE (((a + b)::text) WITH =) generates
// "..._text_excl").
func TestBuildExcludeCastOverOperatorPredictedNameFallsBackToType(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, b integer, CONSTRAINT c EXCLUDE (((a + b)::text) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "text" {
		t.Errorf("PredictedName: got %q, want text (cast's own target type, operator gave nothing strong)", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeNestedCastPredictedName proves a cast-of-a-cast still
// resolves through to the innermost strong name (verified live:
// EXCLUDE (((a::text)::varchar) WITH =) generates "..._a_excl").
func TestBuildExcludeNestedCastPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE (((a::text)::varchar) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "a" {
		t.Errorf("PredictedName: got %q, want a", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCoalesceElementPredictedName proves COALESCE(...) predicts
// the literal "coalesce" — PostgreSQL's own FigureColnameInternal treats it
// like a regular function call for naming, but unlike a real FuncCall it
// never even inspects which function-like node it is (verified live:
// EXCLUDE ((coalesce(a,0)) WITH =) generates "..._coalesce_excl", the same
// literal regardless of COALESCE's arguments).
func TestBuildExcludeCoalesceElementPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((coalesce(a, 0)) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "coalesce" {
		t.Errorf("PredictedName: got %q, want coalesce", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCaseElementWithElsePredictedName proves a CASE expression
// falls back to the literal "case" when its ELSE clause (Defresult) isn't
// itself a strong name — PostgreSQL's real algorithm deliberately only ever
// consults the ELSE clause, never the WHEN branches, for naming purposes
// (verified live: EXCLUDE ((case when a>0 then 1 else 2 end) WITH =)
// generates "..._case_excl", regardless of what the WHEN branch contains).
func TestBuildExcludeCaseElementWithElsePredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((case when a > 0 then 1 else 2 end) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "case" {
		t.Errorf("PredictedName: got %q, want case", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCaseElementNoElsePredictedName proves a CASE expression
// with NO ELSE clause at all (Defresult absent) also falls back to "case",
// not empty/unpredictable (verified live: EXCLUDE
// ((case when a>0 then 1 end) WITH =) generates "..._case_excl" too).
func TestBuildExcludeCaseElementNoElsePredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((case when a > 0 then 1 end) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "case" {
		t.Errorf("PredictedName: got %q, want case", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeArraySubscriptElementPredictedName proves an array
// subscript expression (with no field-access component) recurses through
// to the underlying column — PostgreSQL's A_Indirection handling only
// takes a name directly from a genuine field-access suffix, never from a
// subscript, falling back to the base expression otherwise (verified live:
// EXCLUDE (((array[a,b])[1]) WITH =) generates "..._a_excl", the base
// column's name, not something subscript-derived).
func TestBuildExcludeArraySubscriptElementPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a integer, CONSTRAINT c EXCLUDE ((a[1]) WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "a" {
		t.Errorf("PredictedName: got %q, want a", c.Exclude.Elements[0].PredictedName)
	}
}

// TestBuildExcludeCollateElementPredictedName proves a COLLATE clause is
// transparent for naming purposes — PostgreSQL's CollateClause handling is
// a pure pass-through to its argument, no naming contribution of its own
// (verified live: EXCLUDE ((a COLLATE "C") WITH =) generates "..._a_excl").
func TestBuildExcludeCollateElementPredictedName(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`t (a text, CONSTRAINT c EXCLUDE ((a COLLATE "C") WITH =))`, ``)
	tbl := obj.(*ir.Table)
	c := findExclude(tbl.Constraints)
	if c == nil {
		t.Fatal("EXCLUDE constraint not found")
	}
	if c.Exclude.Elements[0].PredictedName != "a" {
		t.Errorf("PredictedName: got %q, want a", c.Exclude.Elements[0].PredictedName)
	}
}

// ── TypeRef ───────────────────────────────────────────────────────────────────

func TestTypeRefBuiltIn(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable, `t (n BIGINT, s TEXT)`, ``)
	tbl := obj.(*ir.Table)
	n := findCol(tbl.Columns, "n")
	if n == nil {
		t.Fatal("column n not found")
	}
	if n.Type.Name != "bigint" {
		t.Errorf("type name: got %q", n.Type.Name)
	}
}

// TestBuildTableRejectsUnknownColumnBlock guards the RFC §7.2 contract: a
// COLUMN block must reference a column that exists in the DDL. Silently
// inventing one (the prior behaviour) leads to malformed migrations like an
// `ALTER COLUMN ... TYPE ` with an empty type when the phantom flows into diff.
func TestBuildTableRejectsUnknownColumnBlock(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindTable,
		`groups (
			id          BIGINT,
			locality_id BIGINT
		)`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse: %v", err)
	}
	bp := blockparser.New()
	// "locality_ids" — note the trailing s — does not match any DDL column.
	blockAST, err := bp.Parse(pipeline.KindTable,
		`COLUMN locality_ids { RENAMED FROM locale_id; }`, zeroPos)
	if err != nil {
		t.Fatalf("block parse: %v", err)
	}
	_, err = ir.NewBuilder().Build(pgResult, blockAST)
	if err == nil {
		t.Fatal("expected build error for unknown COLUMN block target, got nil")
	}
	msg := err.Error()
	for _, want := range []string{`"locality_ids"`, "locality_id"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error to mention %s, got: %s", want, msg)
		}
	}
}

// TestBuildTableAcceptsKnownColumnBlock is the positive case: when the COLUMN
// block names a real DDL column, the build succeeds and merges the attributes.
func TestBuildTableAcceptsKnownColumnBlock(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable,
		`groups (
			id          BIGINT,
			locality_id BIGINT
		)`,
		`COLUMN locality_id { COMMENT 'geo locality'; }`,
	)
	tbl := obj.(*ir.Table)
	col := findCol(tbl.Columns, "locality_id")
	if col == nil || col.Comment == nil || *col.Comment != "geo locality" {
		t.Fatalf("expected locality_id with comment, got %+v", col)
	}
}

// ── Registry ──────────────────────────────────────────────────────────────────

func TestRegistration(t *testing.T) {
	impl, ok := pipeline.Resolve[pipeline.IRBuilder](pipeline.Default, pipeline.KeyIRBuilder)
	if !ok {
		t.Fatal("IRBuilder not registered")
	}
	if impl == nil {
		t.Fatal("registered IRBuilder is nil")
	}
}

// ── ArgsKey ───────────────────────────────────────────────────────────────────

func TestArgsKey(t *testing.T) {
	cases := []struct {
		args []ir.FuncArg
		want string
	}{
		{nil, ""},
		{[]ir.FuncArg{{Mode: "IN", Type: ir.TypeRef{Name: "integer"}}}, "integer"},
		{[]ir.FuncArg{
			{Mode: "IN", Type: ir.TypeRef{Name: "integer"}},
			{Mode: "IN", Type: ir.TypeRef{Name: "text"}},
		}, "integer, text"},
		// OUT params are excluded from the identity key.
		{[]ir.FuncArg{
			{Mode: "IN", Type: ir.TypeRef{Name: "integer"}},
			{Mode: "OUT", Type: ir.TypeRef{Name: "text"}},
		}, "integer"},
		// TABLE params are also excluded.
		{[]ir.FuncArg{
			{Mode: "TABLE", Type: ir.TypeRef{Name: "bigint"}},
		}, ""},
		// INOUT params are included.
		{[]ir.FuncArg{
			{Mode: "INOUT", Type: ir.TypeRef{Name: "integer"}},
		}, "integer"},
		// Default mode (empty string treated as IN) is included.
		{[]ir.FuncArg{
			{Type: ir.TypeRef{Name: "boolean"}},
		}, "boolean"},
	}
	for _, tc := range cases {
		got := ir.ArgsKey(tc.args)
		if got != tc.want {
			t.Errorf("ArgsKey(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

// ── VirtualType ───────────────────────────────────────────────────────────────

func TestBuildVirtualTypeTypeRef(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType, `label AS text`, ``)
	vt, ok := obj.(*ir.VirtualType)
	if !ok {
		t.Fatalf("expected *ir.VirtualType, got %T", obj)
	}
	if vt.Name != "label" {
		t.Errorf("Name: got %q, want %q", vt.Name, "label")
	}
	if vt.QualifiedName() != "public.label" {
		t.Errorf("QualifiedName: got %q", vt.QualifiedName())
	}
	ref, ok := vt.Body.(ir.VtypeTypeRef)
	if !ok {
		t.Fatalf("Body: expected VtypeTypeRef, got %T", vt.Body)
	}
	if ref.Name != "text" {
		t.Errorf("Body.Name: got %q, want %q", ref.Name, "text")
	}
	if ref.IsArray {
		t.Errorf("Body.IsArray: want false")
	}
}

func TestBuildVirtualTypeTypeRefArray(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType, `tags AS text[]`, ``)
	vt := obj.(*ir.VirtualType)
	ref, ok := vt.Body.(ir.VtypeTypeRef)
	if !ok {
		t.Fatalf("Body: expected VtypeTypeRef, got %T", vt.Body)
	}
	if ref.Name != "text" || !ref.IsArray {
		t.Errorf("Body: got name=%q array=%v, want name=text array=true", ref.Name, ref.IsArray)
	}
}

func TestBuildVirtualTypeSchemaQualifiedRef(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType, `status AS billing.payment_method`, ``)
	vt := obj.(*ir.VirtualType)
	ref, ok := vt.Body.(ir.VtypeTypeRef)
	if !ok {
		t.Fatalf("Body: expected VtypeTypeRef, got %T", vt.Body)
	}
	if ref.Schema != "billing" || ref.Name != "payment_method" {
		t.Errorf("Body: got schema=%q name=%q, want billing/payment_method", ref.Schema, ref.Name)
	}
}

func TestBuildVirtualTypeComposite(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType, `point AS (x float8, y float8)`, ``)
	vt := obj.(*ir.VirtualType)
	comp, ok := vt.Body.(ir.VtypeComposite)
	if !ok {
		t.Fatalf("Body: expected VtypeComposite, got %T", vt.Body)
	}
	if len(comp.Fields) != 2 {
		t.Fatalf("Fields: got %d, want 2", len(comp.Fields))
	}
	if comp.Fields[0].Name != "x" || comp.Fields[0].Type.Name != "float8" {
		t.Errorf("Fields[0]: got %+v", comp.Fields[0])
	}
	if comp.Fields[1].Name != "y" || comp.Fields[1].Type.Name != "float8" {
		t.Errorf("Fields[1]: got %+v", comp.Fields[1])
	}
}

func TestBuildVirtualTypeCompositeWithArrayField(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType, `order_summary AS (id bigint, items line_item[])`, ``)
	vt := obj.(*ir.VirtualType)
	comp, ok := vt.Body.(ir.VtypeComposite)
	if !ok {
		t.Fatalf("Body: expected VtypeComposite, got %T", vt.Body)
	}
	if len(comp.Fields) != 2 {
		t.Fatalf("Fields: got %d, want 2", len(comp.Fields))
	}
	itemsField := comp.Fields[1]
	if itemsField.Name != "items" || itemsField.Type.Name != "line_item" || !itemsField.Type.IsArray {
		t.Errorf("Fields[1]: got name=%q type=%q array=%v", itemsField.Name, itemsField.Type.Name, itemsField.Type.IsArray)
	}
}

func TestBuildVirtualTypeUnion(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType,
		`shape AS (x float8, y float8) | (width float8, height float8) | text`, ``)
	vt := obj.(*ir.VirtualType)
	union, ok := vt.Body.(ir.VtypeUnion)
	if !ok {
		t.Fatalf("Body: expected VtypeUnion, got %T", vt.Body)
	}
	if len(union.Members) != 3 {
		t.Fatalf("Members: got %d, want 3", len(union.Members))
	}
	// First two should be composites, last a type ref.
	if _, ok := union.Members[0].(ir.VtypeComposite); !ok {
		t.Errorf("Members[0]: expected VtypeComposite, got %T", union.Members[0])
	}
	if _, ok := union.Members[1].(ir.VtypeComposite); !ok {
		t.Errorf("Members[1]: expected VtypeComposite, got %T", union.Members[1])
	}
	ref, ok := union.Members[2].(ir.VtypeTypeRef)
	if !ok {
		t.Errorf("Members[2]: expected VtypeTypeRef, got %T", union.Members[2])
	}
	if ref.Name != "text" {
		t.Errorf("Members[2].Name: got %q, want %q", ref.Name, "text")
	}
}

func TestBuildVirtualTypeUnionTypeRefs(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType, `metric AS integer | numeric | text`, ``)
	vt := obj.(*ir.VirtualType)
	union, ok := vt.Body.(ir.VtypeUnion)
	if !ok {
		t.Fatalf("Body: expected VtypeUnion, got %T", vt.Body)
	}
	if len(union.Members) != 3 {
		t.Fatalf("Members: got %d, want 3", len(union.Members))
	}
	names := []string{"integer", "numeric", "text"}
	for i, m := range union.Members {
		ref, ok := m.(ir.VtypeTypeRef)
		if !ok {
			t.Errorf("Members[%d]: expected VtypeTypeRef, got %T", i, m)
			continue
		}
		if ref.Name != names[i] {
			t.Errorf("Members[%d].Name: got %q, want %q", i, ref.Name, names[i])
		}
	}
}

func TestBuildVirtualTypeWithComment(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType,
		`user_state AS text`,
		`COMMENT 'User lifecycle state';`)
	vt := obj.(*ir.VirtualType)
	if vt.Comment == nil || *vt.Comment != "User lifecycle state" {
		t.Errorf("Comment: got %v", vt.Comment)
	}
}

func TestBuildVirtualTypePreferredJsonFormatJsonb(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType,
		`payload AS (kind text, data text)`,
		`PREFERRED JSON FORMAT jsonb;`)
	vt := obj.(*ir.VirtualType)
	if vt.JsonFormat != "jsonb" {
		t.Errorf("JsonFormat: got %q, want %q", vt.JsonFormat, "jsonb")
	}
}

func TestBuildVirtualTypePreferredJsonFormatJson(t *testing.T) {
	obj := buildObject(t, pipeline.KindVirtualType,
		`payload AS (kind text, data text)`,
		`PREFERRED JSON FORMAT json;`)
	vt := obj.(*ir.VirtualType)
	if vt.JsonFormat != "json" {
		t.Errorf("JsonFormat: got %q, want %q", vt.JsonFormat, "json")
	}
}

func TestBuildVirtualTypeDefaultJsonFormat(t *testing.T) {
	// No PREFERRED JSON FORMAT → JsonFormat is empty (caller defaults to jsonb).
	obj := buildObject(t, pipeline.KindVirtualType, `tag AS text`, ``)
	vt := obj.(*ir.VirtualType)
	if vt.JsonFormat != "" {
		t.Errorf("JsonFormat: got %q, want empty (default)", vt.JsonFormat)
	}
}

func TestBuildVirtualTypeCommentAndFormat(t *testing.T) {
	// Both COMMENT and PREFERRED JSON FORMAT can coexist in the {} block.
	obj := buildObject(t, pipeline.KindVirtualType,
		`event AS (type text, ts bigint)`,
		`COMMENT 'App event'; PREFERRED JSON FORMAT json;`)
	vt := obj.(*ir.VirtualType)
	if vt.Comment == nil || *vt.Comment != "App event" {
		t.Errorf("Comment: got %v", vt.Comment)
	}
	if vt.JsonFormat != "json" {
		t.Errorf("JsonFormat: got %q, want json", vt.JsonFormat)
	}
}

func TestBuildVirtualTypeSchemaQualifiedName(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindVirtualType, `billing.status AS text`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	// Explicit schema context must NOT override the qualified name.
	pgResult.SchemaContext = "public"
	bp := blockparser.New()
	blockAST, _ := bp.Parse(pipeline.KindVirtualType, ``, zeroPos)
	obj, buildErr := ir.NewBuilder().Build(pgResult, blockAST)
	if buildErr != nil {
		t.Fatalf("build error: %v", buildErr)
	}
	vt := obj.(*ir.VirtualType)
	if vt.Schema != "billing" {
		t.Errorf("Schema: got %q, want %q", vt.Schema, "billing")
	}
	if vt.Name != "status" {
		t.Errorf("Name: got %q, want %q", vt.Name, "status")
	}
}

func TestBuildVirtualTypeSchemaContext(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindVirtualType, `status AS text`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	pgResult.SchemaContext = "myschema"
	bp := blockparser.New()
	blockAST, _ := bp.Parse(pipeline.KindVirtualType, ``, zeroPos)
	obj, buildErr := ir.NewBuilder().Build(pgResult, blockAST)
	if buildErr != nil {
		t.Fatalf("build error: %v", buildErr)
	}
	vt := obj.(*ir.VirtualType)
	if vt.Schema != "myschema" {
		t.Errorf("Schema: got %q, want %q", vt.Schema, "myschema")
	}
}

func TestBuildVirtualTypeEmptyBodyError(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindVirtualType, `bad AS`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	bp := blockparser.New()
	blockAST, _ := bp.Parse(pipeline.KindVirtualType, ``, zeroPos)
	_, buildErr := ir.NewBuilder().Build(pgResult, blockAST)
	if buildErr == nil {
		t.Error("expected error for empty body, got nil")
	}
}

func TestBuildVirtualTypeMissingASError(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindVirtualType, `noashere`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	bp := blockparser.New()
	blockAST, _ := bp.Parse(pipeline.KindVirtualType, ``, zeroPos)
	_, buildErr := ir.NewBuilder().Build(pgResult, blockAST)
	if buildErr == nil {
		t.Error("expected error for missing AS keyword, got nil")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func findCol(cols []*ir.Column, name string) *ir.Column {
	for _, c := range cols {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ── Operator class / family access method ─────────────────────────────────────

func TestBuildOperatorClassAccessMethod(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorClass,
		`my_ops FOR TYPE int4 USING gin AS STORAGE int4`, ``)
	oc, ok := obj.(*ir.OperatorClass)
	if !ok {
		t.Fatalf("expected *ir.OperatorClass, got %T", obj)
	}
	if oc.AccessMethod != "gin" {
		t.Errorf("AccessMethod: got %q, want %q", oc.AccessMethod, "gin")
	}
}

func TestBuildOperatorFamilyAccessMethod(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorFamily, `my_family USING gist`, ``)
	of, ok := obj.(*ir.OperatorFamily)
	if !ok {
		t.Fatalf("expected *ir.OperatorFamily, got %T", obj)
	}
	if of.AccessMethod != "gist" {
		t.Errorf("AccessMethod: got %q, want %q", of.AccessMethod, "gist")
	}
}

// ── Operator class FAMILY / TS config PARSER / TS dict TEMPLATE extraction ─────
//
// These three back the class→family/config→parser/dict→template dependency
// edges added to graph.go (the operator-family general fix): pg_query already
// parses each of these structurally (CreateOpClassStmt.Opfamilyname, and a
// "parser"/"template" DefElem whose Arg is a TypeName, confirmed live via a
// throwaway probe before writing this code), the builder just previously
// discarded them.

func TestBuildOperatorClassFamilyExplicitUnqualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorClass,
		`my_ops FOR TYPE int4 USING gin FAMILY my_family AS STORAGE int4`, ``)
	oc := obj.(*ir.OperatorClass)
	if oc.FamilySchema != "" || oc.FamilyName != "my_family" {
		t.Errorf("Family: got schema=%q name=%q, want schema=\"\" name=\"my_family\"", oc.FamilySchema, oc.FamilyName)
	}
}

func TestBuildOperatorClassFamilyExplicitQualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorClass,
		`my_ops FOR TYPE int4 USING gin FAMILY myschema.my_family AS STORAGE int4`, ``)
	oc := obj.(*ir.OperatorClass)
	if oc.FamilySchema != "myschema" || oc.FamilyName != "my_family" {
		t.Errorf("Family: got schema=%q name=%q, want schema=\"myschema\" name=\"my_family\"", oc.FamilySchema, oc.FamilyName)
	}
}

// TestBuildOperatorClassFamilyOmitted is the negative control: hand-written
// source that omits FAMILY (relying on PostgreSQL's own same-name
// auto-creation) must leave FamilyName empty, not default it to the class's
// own name — that defaulting is a graph.go dependency-edge-lookup concern
// (see defaultSchema there), not something the builder should fabricate,
// since introspection is what always supplies an explicit FAMILY going
// forward (see introspectOperatorClasses); hand-written source keeps
// whatever the user actually wrote.
func TestBuildOperatorClassFamilyOmitted(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorClass,
		`my_ops FOR TYPE int4 USING gin AS STORAGE int4`, ``)
	oc := obj.(*ir.OperatorClass)
	if oc.FamilyName != "" {
		t.Errorf("FamilyName: got %q, want empty (FAMILY omitted from source)", oc.FamilyName)
	}
}

// ── Operator class structured member capture ────────────────────────────────
// Regression guard for the builder/introspect member-capture asymmetry: the
// builder previously only captured FUNCTION items (as a bare-name Functions
// list, dependency-ordering only) and silently skipped OPERATOR and
// STORAGETYPE items entirely — the same class introspection produced was
// represented completely differently depending on which path built it.

func TestBuildOperatorClassMembersDefaultToClassType(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorClass,
		`my_ops FOR TYPE int4 USING btree AS
			OPERATOR 1 <,
			OPERATOR 3 =,
			FUNCTION 1 btint4cmp(int4, int4),
			STORAGE int4`, ``)
	oc := obj.(*ir.OperatorClass)
	if oc.StorageType != "integer" {
		t.Errorf("StorageType: got %q, want %q", oc.StorageType, "integer")
	}
	if len(oc.Members) != 3 {
		t.Fatalf("Members: got %d, want 3: %+v", len(oc.Members), oc.Members)
	}
	for _, m := range oc.Members {
		if m.LeftType != "integer" || m.RightType != "integer" {
			t.Errorf("member %+v: op_type should default to the class's own type (integer)", m)
		}
	}
	if oc.Members[0].IsFunction || oc.Members[0].Number != 1 || oc.Members[0].Name.String() != "<" {
		t.Errorf("Members[0]: got %+v, want OPERATOR 1 <", oc.Members[0])
	}
	fn := oc.Members[2]
	if !fn.IsFunction || fn.Number != 1 || fn.Name.String() != "btint4cmp" {
		t.Errorf("Members[2]: got %+v, want FUNCTION 1 btint4cmp", fn)
	}
	if !slices.Equal(fn.FuncArgs, []string{"integer", "integer"}) {
		t.Errorf("Members[2].FuncArgs: got %v, want [integer integer]", fn.FuncArgs)
	}
	// Functions (dependency-edge ordering only) must still work unchanged.
	if len(oc.Functions) != 1 || oc.Functions[0] != "btint4cmp" {
		t.Errorf("Functions: got %v, want [btint4cmp]", oc.Functions)
	}
}

// TestBuildOperatorClassMembersExplicitTypesAndOrderBy covers the parts of
// the grammar that don't default to the class's own type: an OPERATOR/
// FUNCTION item's explicit "(op_type, op_type)", and OPERATOR's FOR ORDER BY.
func TestBuildOperatorClassMembersExplicitTypesAndOrderBy(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorClass,
		`my_ops FOR TYPE box USING gist AS
			OPERATOR 1 << (box, box) FOR ORDER BY float_ops,
			FUNCTION 1 (box, box) my_cmp(box, box)`, ``)
	oc := obj.(*ir.OperatorClass)
	if len(oc.Members) != 2 {
		t.Fatalf("Members: got %d, want 2: %+v", len(oc.Members), oc.Members)
	}
	op := oc.Members[0]
	if !op.OrderBy || op.SortFamily.String() != "float_ops" {
		t.Errorf("Members[0]: got OrderBy=%v SortFamily=%q, want OrderBy=true SortFamily=float_ops", op.OrderBy, op.SortFamily.String())
	}
	fn := oc.Members[1]
	if fn.LeftType != "box" || fn.RightType != "box" {
		t.Errorf("Members[1] explicit op_type: got left=%q right=%q, want box/box", fn.LeftType, fn.RightType)
	}
}

func TestBuildTSConfigParserUnqualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindTSConfig, `my_cfg (PARSER = my_parser)`, ``)
	tc := obj.(*ir.TSConfig)
	if tc.ParserSchema != "" || tc.ParserName != "my_parser" {
		t.Errorf("Parser: got schema=%q name=%q, want schema=\"\" name=\"my_parser\"", tc.ParserSchema, tc.ParserName)
	}
}

func TestBuildTSConfigParserQualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindTSConfig, `my_cfg (PARSER = pg_catalog."default")`, ``)
	tc := obj.(*ir.TSConfig)
	if tc.ParserSchema != "pg_catalog" || tc.ParserName != "default" {
		t.Errorf("Parser: got schema=%q name=%q, want schema=\"pg_catalog\" name=\"default\"", tc.ParserSchema, tc.ParserName)
	}
}

func TestBuildTSDictTemplateUnqualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindTSDict, `my_dict (TEMPLATE = simple)`, ``)
	td := obj.(*ir.TSDict)
	if td.TemplateSchema != "" || td.TemplateName != "simple" {
		t.Errorf("Template: got schema=%q name=%q, want schema=\"\" name=\"simple\"", td.TemplateSchema, td.TemplateName)
	}
}

func TestBuildTSDictTemplateQualified(t *testing.T) {
	obj := buildObject(t, pipeline.KindTSDict, `my_dict (TEMPLATE = pg_catalog.simple)`, ``)
	td := obj.(*ir.TSDict)
	if td.TemplateSchema != "pg_catalog" || td.TemplateName != "simple" {
		t.Errorf("Template: got schema=%q name=%q, want schema=\"pg_catalog\" name=\"simple\"", td.TemplateSchema, td.TemplateName)
	}
}

// ── Operator LEFTARG/RIGHTARG extraction ────────────────────────────────────────

// TestBuildOperatorBinaryOperandTypes proves a binary operator's LEFTARG/
// RIGHTARG are captured, needed so QualifiedName can disambiguate overloaded
// operator symbols (same name, different operand types) instead of colliding
// in the flat, name-keyed snapshot/diff maps.
func TestBuildOperatorBinaryOperandTypes(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperator,
		`public.## (LEFTARG = integer, RIGHTARG = integer, FUNCTION = int4eq)`, ``)
	op, ok := obj.(*ir.Operator)
	if !ok {
		t.Fatalf("expected *ir.Operator, got %T", obj)
	}
	if op.LeftType == nil || op.LeftType.String() != "integer" {
		t.Errorf("LeftType: got %v, want integer", op.LeftType)
	}
	if op.RightType == nil || op.RightType.String() != "integer" {
		t.Errorf("RightType: got %v, want integer", op.RightType)
	}
	if want := `public.##(integer, integer)`; op.QualifiedName() != want {
		t.Errorf("QualifiedName: got %q, want %q", op.QualifiedName(), want)
	}
}

// TestBuildOperatorPrefixOperandTypes proves a unary (prefix) operator, which
// omits LEFTARG entirely, leaves LeftType nil rather than defaulting to a
// zero value that could be mistaken for a real type.
func TestBuildOperatorPrefixOperandTypes(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperator,
		`public.!! (RIGHTARG = integer, FUNCTION = fact)`, ``)
	op, ok := obj.(*ir.Operator)
	if !ok {
		t.Fatalf("expected *ir.Operator, got %T", obj)
	}
	if op.LeftType != nil {
		t.Errorf("LeftType: got %v, want nil (prefix operator has no left operand)", op.LeftType)
	}
	if op.RightType == nil || op.RightType.String() != "integer" {
		t.Errorf("RightType: got %v, want integer", op.RightType)
	}
	if want := `public.!!(NONE, integer)`; op.QualifiedName() != want {
		t.Errorf("QualifiedName: got %q, want %q", op.QualifiedName(), want)
	}
}

// TestBuildOperatorOverloadDistinctQualifiedNames is the core regression
// guard: two operators sharing the same symbol but different operand types
// (the common overload shape, e.g. + for integer vs numeric) must produce
// different QualifiedName values, or one would silently overwrite the other
// in the flat snapshot/diff maps — the same collision class already fixed
// for OperatorClass/OperatorFamily.
func TestBuildOperatorOverloadDistinctQualifiedNames(t *testing.T) {
	intOp := buildObject(t, pipeline.KindOperator,
		`public.+ (LEFTARG = integer, RIGHTARG = integer, FUNCTION = int4pl)`, ``).(*ir.Operator)
	numOp := buildObject(t, pipeline.KindOperator,
		`public.+ (LEFTARG = numeric, RIGHTARG = numeric, FUNCTION = numeric_add)`, ``).(*ir.Operator)
	if intOp.QualifiedName() == numOp.QualifiedName() {
		t.Errorf("expected distinct QualifiedName for overloaded operators, both got %q", intOp.QualifiedName())
	}
}

// ── Subscription CONNECTION (RFC §13.2) ─────────────────────────────────────────

func TestBuildSubscriptionPlainLiteral(t *testing.T) {
	obj := buildObject(t, pipeline.KindSubscription,
		`sub CONNECTION 'host=primary.internal dbname=myapp user=replicator' PUBLICATION pub`, ``)
	sub, ok := obj.(*ir.Subscription)
	if !ok {
		t.Fatalf("expected *ir.Subscription, got %T", obj)
	}
	if sub.ConnInfo != "host=primary.internal dbname=myapp user=replicator" {
		t.Errorf("ConnInfo: got %q", sub.ConnInfo)
	}
}

func TestBuildSubscriptionTemplatedLiteral(t *testing.T) {
	obj := buildObject(t, pipeline.KindSubscription,
		`sub CONNECTION 'host=x user=y password={{vault:secret/db#pw}}' PUBLICATION pub`, ``)
	sub := obj.(*ir.Subscription)
	if sub.ConnInfo != "host=x user=y password={{vault:secret/db#pw}}" {
		t.Errorf("ConnInfo: got %q, want the raw literal with {{...}} left unresolved at build time", sub.ConnInfo)
	}
}

func TestBuildSubscriptionWholeValueTemplatedLiteral(t *testing.T) {
	obj := buildObject(t, pipeline.KindSubscription,
		`sub CONNECTION '{{vault:secret/repl/db#conninfo}}' PUBLICATION pub`, ``)
	sub := obj.(*ir.Subscription)
	if sub.ConnInfo != "{{vault:secret/repl/db#conninfo}}" {
		t.Errorf("ConnInfo: got %q", sub.ConnInfo)
	}
}

func TestBuildSubscriptionComment(t *testing.T) {
	obj := buildObject(t, pipeline.KindSubscription,
		`sub CONNECTION 'host=x user=y' PUBLICATION pub`,
		`COMMENT 'replication for orders';`,
	)
	sub, ok := obj.(*ir.Subscription)
	if !ok {
		t.Fatalf("expected *ir.Subscription, got %T", obj)
	}
	if sub.Comment == nil || *sub.Comment != "replication for orders" {
		t.Errorf("Comment: got %v", sub.Comment)
	}
}

// ── Role attributes (RFC §11.1) ──────────────────────────────────────────────

func TestBuildRoleAllAttributes(t *testing.T) {
	obj := buildObject(t, pipeline.KindRole,
		`app_service LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 20 PASSWORD '{{vault:secret/roles/app_service#pw}}' VALID UNTIL '2030-01-01' IN ROLE role_a, role_b ROLE role_c ADMIN role_d`,
		``,
	)
	r, ok := obj.(*ir.Role)
	if !ok {
		t.Fatalf("expected *ir.Role, got %T", obj)
	}
	if r.CanLogin == nil || !*r.CanLogin {
		t.Errorf("CanLogin: got %v, want true", r.CanLogin)
	}
	if r.Superuser == nil || *r.Superuser {
		t.Errorf("Superuser: got %v, want false", r.Superuser)
	}
	if r.CreateDB == nil || *r.CreateDB {
		t.Errorf("CreateDB: got %v, want false", r.CreateDB)
	}
	if r.CreateRole == nil || *r.CreateRole {
		t.Errorf("CreateRole: got %v, want false", r.CreateRole)
	}
	if r.Inherit == nil || !*r.Inherit {
		t.Errorf("Inherit: got %v, want true", r.Inherit)
	}
	if r.IsReplication == nil || *r.IsReplication {
		t.Errorf("IsReplication: got %v, want false", r.IsReplication)
	}
	if r.BypassRLS == nil || *r.BypassRLS {
		t.Errorf("BypassRLS: got %v, want false", r.BypassRLS)
	}
	if r.ConnectionLimit == nil || *r.ConnectionLimit != 20 {
		t.Errorf("ConnectionLimit: got %v, want 20", r.ConnectionLimit)
	}
	if r.Password == nil || *r.Password != "{{vault:secret/roles/app_service#pw}}" {
		t.Errorf("Password: got %v", r.Password)
	}
	if r.ValidUntil == nil || *r.ValidUntil != "2030-01-01" {
		t.Errorf("ValidUntil: got %v", r.ValidUntil)
	}
	if len(r.InRole) != 2 || r.InRole[0] != "role_a" || r.InRole[1] != "role_b" {
		t.Errorf("InRole: got %v", r.InRole)
	}
	if len(r.RoleMembers) != 1 || r.RoleMembers[0] != "role_c" {
		t.Errorf("RoleMembers: got %v", r.RoleMembers)
	}
	if len(r.AdminRoles) != 1 || r.AdminRoles[0] != "role_d" {
		t.Errorf("AdminRoles: got %v", r.AdminRoles)
	}
}

func TestBuildRoleUnsetAttributesAreNil(t *testing.T) {
	obj := buildObject(t, pipeline.KindRole, `plain_role`, ``)
	r := obj.(*ir.Role)
	if r.CanLogin != nil || r.Superuser != nil || r.CreateDB != nil || r.CreateRole != nil ||
		r.Inherit != nil || r.IsReplication != nil || r.BypassRLS != nil ||
		r.ConnectionLimit != nil || r.Password != nil || r.ValidUntil != nil {
		t.Errorf("expected all optional attributes nil for a bare ROLE decl, got: %+v", r)
	}
	if r.InRole != nil || r.RoleMembers != nil || r.AdminRoles != nil {
		t.Errorf("expected nil membership lists, got InRole=%v RoleMembers=%v AdminRoles=%v", r.InRole, r.RoleMembers, r.AdminRoles)
	}
}

func TestBuildRolePlainLiteralPassword(t *testing.T) {
	obj := buildObject(t, pipeline.KindRole, `svc PASSWORD 'hunter2'`, ``)
	r := obj.(*ir.Role)
	if r.Password == nil || *r.Password != "hunter2" {
		t.Errorf("Password: got %v", r.Password)
	}
}

func TestBuildRoleComment(t *testing.T) {
	obj := buildObject(t, pipeline.KindRole, `app_readonly NOLOGIN`, `COMMENT 'Read-only access';`)
	r := obj.(*ir.Role)
	if r.Comment == nil || *r.Comment != "Read-only access" {
		t.Errorf("Comment: got %v", r.Comment)
	}
	if r.CanLogin == nil || *r.CanLogin {
		t.Errorf("CanLogin: got %v, want false (NOLOGIN)", r.CanLogin)
	}
}

// TestBuildOpaqueCommentSupport guards a real bug found live-testing a demo
// project: PostgreSQL genuinely supports COMMENT ON for all 14 of these
// opaque kinds, but 9 had no Comment field at all (the blockparser's
// generic { COMMENT '...'; } was silently discarded, no error, no effect)
// and a 10th (TSDict) had the field but the builder never populated it —
// found auditing every kind with a Comment field for whether it's actually
// wired, not just declared. Table-driven across a representative sample
// covering both routing paths (buildDefineStmt for Collation/Operator/
// TSDict, buildOpaque for Cast/EventTrigger/OperatorClass).
func TestBuildOpaqueCommentSupport(t *testing.T) {
	const comment = "a comment"
	cases := []struct {
		name       string
		kind       pipeline.ObjectKind
		part1      string
		getComment func(pipeline.IRObject) *string
	}{
		{
			"Collation", pipeline.KindCollation,
			`case_insensitive (provider = icu, locale = 'und-u-ks-level2')`,
			func(o pipeline.IRObject) *string { return o.(*ir.Collation).Comment },
		},
		{
			"Cast", pipeline.KindCast,
			`(order_status AS integer) WITH FUNCTION order_status_to_int(order_status)`,
			func(o pipeline.IRObject) *string { return o.(*ir.Cast).Comment },
		},
		{
			"Operator", pipeline.KindOperator,
			`<<< (LEFTARG = order_status, RIGHTARG = order_status, PROCEDURE = order_status_precedes)`,
			func(o pipeline.IRObject) *string { return o.(*ir.Operator).Comment },
		},
		{
			"EventTrigger", pipeline.KindEventTrigger,
			`log_ddl ON ddl_command_end EXECUTE FUNCTION log_ddl_command()`,
			func(o pipeline.IRObject) *string { return o.(*ir.EventTrigger).Comment },
		},
		{
			// TSDict specifically guards the "field existed but was never
			// populated" half of the bug — distinct from the 9 kinds that
			// gained the field entirely in this fix.
			"TSDict", pipeline.KindTSDict,
			`my_dict (TEMPLATE = simple)`,
			func(o pipeline.IRObject) *string { return o.(*ir.TSDict).Comment },
		},
		{
			"OperatorClass", pipeline.KindOperatorClass,
			`text_ci_ops FOR TYPE text USING btree AS OPERATOR 1 <, FUNCTION 1 text_ci_cmp(text, text)`,
			func(o pipeline.IRObject) *string { return o.(*ir.OperatorClass).Comment },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obj := buildObject(t, tc.kind, tc.part1, `COMMENT 'a comment';`)
			got := tc.getComment(obj)
			if got == nil || *got != comment {
				t.Errorf("Comment: got %v, want %q", got, comment)
			}
		})
	}
}

// ── Collation structured diffing inputs (RFC §14.2) ────────────────────────────
// Regression guards for diffCollation's actual risk (see its doc comment):
// LOCALE and LC_COLLATE/LC_CTYPE are different DPG source spellings that
// must resolve to the SAME Collate/Ctype fields — proven here at the
// builder level (not just with hand-set differ_test.go fixtures), matching
// what real PostgreSQL does (confirmed live against a real server).

func TestBuildCollationLocaleShorthandResolvesCollateAndCtype(t *testing.T) {
	obj := buildObject(t, pipeline.KindCollation, `c1 (LOCALE = 'en_US.utf8')`, ``)
	col := obj.(*ir.Collation)
	if col.Provider != "c" {
		t.Errorf("Provider: got %q, want \"c\" (libc default)", col.Provider)
	}
	if col.Collate == nil || *col.Collate != "en_US.utf8" {
		t.Errorf("Collate: got %v, want \"en_US.utf8\"", col.Collate)
	}
	if col.Ctype == nil || *col.Ctype != "en_US.utf8" {
		t.Errorf("Ctype: got %v, want \"en_US.utf8\"", col.Ctype)
	}
	if !col.Deterministic {
		t.Error("Deterministic: got false, want true (PostgreSQL's own default)")
	}
}

// TestBuildCollationRules guards RFC audit item #111: PG16+ ICU RULES was
// entirely unparsed — buildCollation's DefElem switch had no "rules" case
// at all.
func TestBuildCollationRules(t *testing.T) {
	obj := buildObject(t, pipeline.KindCollation, `c1 (PROVIDER = icu, LOCALE = 'und', RULES = '&a < b')`, ``)
	col := obj.(*ir.Collation)
	if col.Rules == nil || *col.Rules != "&a < b" {
		t.Errorf("Rules: got %v, want \"&a < b\"", col.Rules)
	}
}

// TestBuildCollationRulesUnspecified proves Rules stays nil when the source
// never mentions RULES.
func TestBuildCollationRulesUnspecified(t *testing.T) {
	obj := buildObject(t, pipeline.KindCollation, `c1 (PROVIDER = icu, LOCALE = 'und')`, ``)
	col := obj.(*ir.Collation)
	if col.Rules != nil {
		t.Errorf("Rules: got %v, want nil", col.Rules)
	}
}

// TestBuildCollationOwner guards RFC audit item #81: Collation had no Owner
// field at all.
func TestBuildCollationOwner(t *testing.T) {
	obj := buildObject(t, pipeline.KindCollation, `c1 (LOCALE = 'en_US.utf8')`, `OWNER app_admin;`)
	col := obj.(*ir.Collation)
	if col.Owner == nil || *col.Owner != "app_admin" {
		t.Errorf("Owner: got %v, want app_admin", col.Owner)
	}
}

func TestBuildCollationExplicitLcCollateCtypeMatchesLocaleShorthand(t *testing.T) {
	shorthand := buildObject(t, pipeline.KindCollation, `c1 (LOCALE = 'en_US.utf8')`, ``).(*ir.Collation)
	explicit := buildObject(t, pipeline.KindCollation, `c2 (LC_COLLATE = 'en_US.utf8', LC_CTYPE = 'en_US.utf8')`, ``).(*ir.Collation)
	if shorthand.Provider != explicit.Provider {
		t.Errorf("Provider mismatch: LOCALE=%q vs LC_COLLATE/LC_CTYPE=%q", shorthand.Provider, explicit.Provider)
	}
	if *shorthand.Collate != *explicit.Collate {
		t.Errorf("Collate mismatch: LOCALE=%q vs LC_COLLATE/LC_CTYPE=%q", *shorthand.Collate, *explicit.Collate)
	}
	if *shorthand.Ctype != *explicit.Ctype {
		t.Errorf("Ctype mismatch: LOCALE=%q vs LC_COLLATE/LC_CTYPE=%q", *shorthand.Ctype, *explicit.Ctype)
	}
}

func TestBuildCollationICUProviderLocaleSetsICULocaleOnly(t *testing.T) {
	obj := buildObject(t, pipeline.KindCollation, `c1 (PROVIDER = icu, LOCALE = 'en-US-u-ks-level2', DETERMINISTIC = false)`, ``)
	col := obj.(*ir.Collation)
	if col.Provider != "i" {
		t.Errorf("Provider: got %q, want \"i\" (icu)", col.Provider)
	}
	if col.ICULocale == nil || *col.ICULocale != "en-US-u-ks-level2" {
		t.Errorf("ICULocale: got %v, want \"en-US-u-ks-level2\"", col.ICULocale)
	}
	if col.Collate != nil {
		t.Errorf("Collate: got %v, want nil (LOCALE with PROVIDER=icu sets ICULocale, not Collate/Ctype)", col.Collate)
	}
	if col.Ctype != nil {
		t.Errorf("Ctype: got %v, want nil (LOCALE with PROVIDER=icu sets ICULocale, not Collate/Ctype)", col.Ctype)
	}
	if col.Deterministic {
		t.Error("Deterministic: got true, want false (explicitly declared)")
	}
}

// ── StatisticsObject structured diffing inputs (RFC §14.6) ─────────────────────

func TestBuildStatisticsObjectFields(t *testing.T) {
	obj := buildObject(t, pipeline.KindStatisticsObject,
		`orders_stats (dependencies, ndistinct) ON customer_id, created_at FROM orders`, ``)
	st := obj.(*ir.StatisticsObject)
	if st.Table != "orders" {
		t.Errorf("Table: got %q, want \"orders\"", st.Table)
	}
	wantKinds := map[string]bool{"dependencies": true, "ndistinct": true}
	if len(st.Kinds) != len(wantKinds) {
		t.Fatalf("Kinds: got %v, want %v", st.Kinds, wantKinds)
	}
	for _, k := range st.Kinds {
		if !wantKinds[k] {
			t.Errorf("Kinds: unexpected kind %q", k)
		}
	}
	wantCols := map[string]bool{"customer_id": true, "created_at": true}
	if len(st.Columns) != len(wantCols) {
		t.Fatalf("Columns: got %v, want %v", st.Columns, wantCols)
	}
	for _, c := range st.Columns {
		if !wantCols[c] {
			t.Errorf("Columns: unexpected column %q", c)
		}
	}
}

// TestBuildStatisticsObjectExpressionColumn proves an expression element
// (not just a plain column) is captured, canonicalized the same way
// PostgreSQL's own pg_get_statisticsobjdef_expressions renders it
// (confirmed live) — see ir.StatisticsObject.Table's doc comment.
func TestBuildStatisticsObjectExpressionColumn(t *testing.T) {
	obj := buildObject(t, pipeline.KindStatisticsObject,
		`s1 (ndistinct) ON a, (lower(b)) FROM t`, ``)
	st := obj.(*ir.StatisticsObject)
	foundPlain, foundExpr := false, false
	for _, c := range st.Columns {
		if c == "a" {
			foundPlain = true
		}
		if c == "lower(b)" {
			foundExpr = true
		}
	}
	if !foundPlain {
		t.Errorf("Columns: missing plain column \"a\", got %v", st.Columns)
	}
	if !foundExpr {
		t.Errorf("Columns: missing expression \"lower(b)\", got %v", st.Columns)
	}
}

func TestBuildStatisticsObjectUnqualifiedTable(t *testing.T) {
	obj := buildObject(t, pipeline.KindStatisticsObject, `s1 (ndistinct) ON a, b FROM myschema.t`, ``)
	st := obj.(*ir.StatisticsObject)
	if st.Table != "myschema.t" {
		t.Errorf("Table: got %q, want \"myschema.t\"", st.Table)
	}
}

// TestBuildStatisticsObjectNoKindListDefaultsToAllThree is a real-bug
// regression guard, found live-testing against the demo project's own
// orders_user_status_stats (declared with no "(kind, ...)" clause at all):
// PostgreSQL's own default for a bare "ON col1, col2 FROM t" with no kind
// list is to enable all three supported kinds (confirmed live:
// pg_statistic_ext.stxkind = {d,f,m}), not an empty list — an empty Kinds
// here would have spuriously diffed against a live introspected object
// that has all three, DROP+CREATE-ing the demo's own real statistics
// object on every plan --live.
func TestBuildStatisticsObjectNoKindListDefaultsToAllThree(t *testing.T) {
	obj := buildObject(t, pipeline.KindStatisticsObject, `s1 ON a, b FROM t`, ``)
	st := obj.(*ir.StatisticsObject)
	want := map[string]bool{"ndistinct": true, "dependencies": true, "mcv": true}
	if len(st.Kinds) != len(want) {
		t.Fatalf("Kinds: got %v, want all three (ndistinct, dependencies, mcv)", st.Kinds)
	}
	for _, k := range st.Kinds {
		if !want[k] {
			t.Errorf("Kinds: unexpected kind %q", k)
		}
	}
}

// ── OPERATOR FAMILY loose members (RFC §14.4) ─────────────────────────────────

func TestBuildOperatorFamilyMembers(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorFamily, `my_family USING btree`, `
		OPERATOR 1 <(int4, int8),
		OPERATOR 3 =(int4, int8) FOR ORDER BY public.my_family,
		FUNCTION 1 (int4, int8) btint48cmp(int4, int8)
	`)
	of := obj.(*ir.OperatorFamily)
	if len(of.Members) != 3 {
		t.Fatalf("expected 3 members, got %d: %+v", len(of.Members), of.Members)
	}
	m1 := of.Members[1]
	if !m1.OrderBy || m1.SortFamily.Schema != "public" || m1.SortFamily.Name != "my_family" {
		t.Errorf("member 1 (FOR ORDER BY): got %+v", m1)
	}
}

// TestBuildOperatorFamilyMemberTypesCanonicalized guards the whole reason
// ir.ParseTypeText exists: a hand-written "int4"/"int8" must normalize to
// the same canonical form ("integer"/"bigint") introspection's
// ::regtype::text produces, or every source-declared member with a
// non-canonical type name would show permanent spurious drift.
func TestBuildOperatorFamilyMemberTypesCanonicalized(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorFamily, `my_family USING btree`,
		`OPERATOR 1 <(int4, int8)`)
	m := obj.(*ir.OperatorFamily).Members[0]
	if m.LeftType != "integer" || m.RightType != "bigint" {
		t.Errorf("got left=%q right=%q, want integer/bigint", m.LeftType, m.RightType)
	}
}

// TestBuildOperatorFamilyFunctionDefaultsFromTwoArgs guards PostgreSQL's own
// documented default for an omitted FUNCTION op_type list: the function's
// own argument types, but ONLY when it takes exactly two arguments.
func TestBuildOperatorFamilyFunctionDefaultsFromTwoArgs(t *testing.T) {
	obj := buildObject(t, pipeline.KindOperatorFamily, `my_family USING btree`,
		`FUNCTION 1 btint48cmp(int4, int8)`)
	m := obj.(*ir.OperatorFamily).Members[0]
	if m.LeftType != "integer" || m.RightType != "bigint" {
		t.Errorf("got left=%q right=%q, want integer/bigint (defaulted from args)", m.LeftType, m.RightType)
	}
}

// TestBuildOperatorFamilyFunctionOtherArityRequiresOpTypes guards the
// documented refusal to guess: for GiST/GIN support functions like
// consistent(internal, text, smallint, oid, internal), the true
// amproclefttype is the opclass's own input type, not derivable from the
// function's argument list at all.
func TestBuildOperatorFamilyFunctionOtherArityRequiresOpTypes(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindOperatorFamily, `my_family USING gist`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	bp := blockparser.New()
	blockAST, err := bp.Parse(pipeline.KindOperatorFamily,
		`FUNCTION 1 consistent(internal, text, smallint, oid, internal)`, zeroPos)
	if err != nil {
		t.Fatalf("block parse error: %v", err)
	}
	builder := ir.NewBuilder()
	if _, err := builder.Build(pgResult, blockAST); err == nil {
		t.Fatal("expected a build error requiring explicit op_types for a non-2-arg function")
	}
}

func TestBuildOperatorFamilyDuplicateMemberErrors(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindOperatorFamily, `my_family USING btree`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	bp := blockparser.New()
	blockAST, err := bp.Parse(pipeline.KindOperatorFamily, `
		OPERATOR 1 <(int4, int8),
		OPERATOR 1 <=(int4, int8)
	`, zeroPos)
	if err != nil {
		t.Fatalf("block parse error: %v", err)
	}
	builder := ir.NewBuilder()
	if _, err := builder.Build(pgResult, blockAST); err == nil {
		t.Fatal("expected a build error for two members at the same catalog slot")
	}
}

// TestBuildOpFamilyMembersOnWrongKindErrors guards the compile-time gate
// (Build's top-level check, not buildOpaque's) that catches OPERATOR/
// FUNCTION block directives written on any object other than OPERATOR
// FAMILY — the block parser itself has no per-kind gating, so this is the
// one place both halves (Part 1's real kind, Part 2's parsed directives)
// are visible together.
func TestBuildOpFamilyMembersOnWrongKindErrors(t *testing.T) {
	p := pgparser.New()
	pgResult, err := p.Parse(pipeline.KindCast, `(int4 AS int8) WITHOUT FUNCTION`, zeroPos)
	if err != nil {
		t.Fatalf("pg parse error: %v", err)
	}
	bp := blockparser.New()
	blockAST, err := bp.Parse(pipeline.KindCast, `OPERATOR 1 <(int4, int8)`, zeroPos)
	if err != nil {
		t.Fatalf("block parse error: %v", err)
	}
	builder := ir.NewBuilder()
	if _, err := builder.Build(pgResult, blockAST); err == nil {
		t.Fatal("expected a build error for OPERATOR/FUNCTION members on a non-family object")
	}
}

// ── Bare-word type rendering fidelity (bit/varbit, time/timestamp) ─────────────

// TestBuildColumnTimestampPrecisionPreserved guards a real, live-verified
// bug: TIMESTAMP(2) compiled to a rendered type string with the precision
// silently dropped entirely ("timestamp" instead of "timestamp(2) without
// time zone"), because PGCatalogName's "timestamp" -> "timestamp without
// time zone" mapping (added to fix a separate spurious-drift bug — see
// TestBuildColumnBareTimestampMatchesFormatType below) moved typmodString's
// switch case out from under it; both had to be fixed together. Confirmed
// live via a real apply + `plan --live` round-trip against a PostgreSQL 17
// container before landing (zero drift after the fix).
func TestBuildColumnTimestampPrecisionPreserved(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable, `t (a TIMESTAMP(2), b TIME(3), c TIMESTAMPTZ(4))`, ``)
	tbl := obj.(*ir.Table)
	cases := map[string]string{
		"a": "timestamp(2) without time zone",
		"b": "time(3) without time zone",
		"c": "timestamp(4) with time zone",
	}
	for _, col := range tbl.Columns {
		want, ok := cases[col.Name]
		if !ok {
			continue
		}
		if got := col.Type.String(); got != want {
			t.Errorf("column %s: got %q, want %q", col.Name, got, want)
		}
	}
}

// TestBuildColumnBareTimestampMatchesFormatType guards the actual bug found
// live: a bare TIMESTAMP/TIME column (no precision) rendered as literal
// "timestamp"/"time", permanently mismatching PostgreSQL's own
// format_type() output ("timestamp without time zone"/"time without time
// zone") that live introspection uses — a real, high-impact bug (these are
// extremely common column types) causing permanent spurious DESTRUCTIVE
// drift on every `plan --live`/`verify` for any table with a plain
// TIMESTAMP or TIME column. Confirmed live via apply + `plan --live`
// (zero drift after the fix) before landing.
func TestBuildColumnBareTimestampMatchesFormatType(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable, `t (a TIMESTAMP, b TIME)`, ``)
	tbl := obj.(*ir.Table)
	cases := map[string]string{
		"a": "timestamp without time zone",
		"b": "time without time zone",
	}
	for _, col := range tbl.Columns {
		want, ok := cases[col.Name]
		if !ok {
			continue
		}
		if got := col.Type.String(); got != want {
			t.Errorf("column %s: got %q, want %q", col.Name, got, want)
		}
	}
}

// TestBuildColumnBitVaryingLengthPreserved guards a real, live-verified bug:
// BIT VARYING(8) and BIT(4) both compiled with their length modifier
// completely dropped ("varbit"/"bit", no "(n)" at all) — typmodString had
// no case for either type name at all. This wasn't just cosmetic: applying
// the resulting DDL created a column with PostgreSQL's own bare-word
// default width instead of the declared one — confirmed live that bare
// `bit` silently means `bit(1)`, an 8x-narrower column than BIT(4)
// declared, with no error anywhere in the pipeline. Also guards the
// "bit varying"/"varbit" rendering-fidelity half separately (PGCatalogName
// now maps "varbit" -> "bit varying" to match format_type(), same
// reasoning as the timestamp/time fix above).
func TestBuildColumnBitVaryingLengthPreserved(t *testing.T) {
	obj := buildObject(t, pipeline.KindTable, `t (a BIT VARYING(8), b BIT(4))`, ``)
	tbl := obj.(*ir.Table)
	cases := map[string]string{
		"a": "bit varying(8)",
		"b": "bit(4)",
	}
	for _, col := range tbl.Columns {
		want, ok := cases[col.Name]
		if !ok {
			continue
		}
		if got := col.Type.String(); got != want {
			t.Errorf("column %s: got %q, want %q", col.Name, got, want)
		}
	}
}

func TestPGCatalogNameNewMappings(t *testing.T) {
	cases := map[string]string{
		"time":      "time without time zone",
		"timestamp": "timestamp without time zone",
		"varbit":    "bit varying",
	}
	for internal, want := range cases {
		if got := ir.PGCatalogName(internal); got != want {
			t.Errorf("PGCatalogName(%q): got %q, want %q", internal, got, want)
		}
	}
}

// TestTypeRefStringWithoutTimeZoneModPosition proves TypeRef.String()
// inserts a typmod before " without time zone" the same way it already did
// for " with time zone" — the mid-string special case previously only
// checked for " with time zone" as a substring, which "without time zone"
// does NOT contain (the word is "without", not "with" immediately followed
// by "out"), so a typmod on a "timestamp without time zone"/"time without
// time zone" TypeRef was silently dropped entirely rather than merely
// misplaced, until this was fixed alongside PGCatalogName's new mappings.
func TestTypeRefStringWithoutTimeZoneModPosition(t *testing.T) {
	ref := ir.TypeRef{Name: "timestamp without time zone", Mods: "(3)"}
	if got := ref.String(); got != "timestamp(3) without time zone" {
		t.Errorf("got %q, want %q", got, "timestamp(3) without time zone")
	}
}

// ── ParameterPrivileges (RFC Section 11.6, PG15+) ────────────────────────────

// buildParameterPrivileges mirrors buildObject but for the compiler's
// PARAMETER PRIVILEGES bypass path (KindParameterPrivileges never goes
// through pgparser — see compiler.Compile's identical DEFAULT PRIVILEGES
// bypass), matching exactly what compiler.Compile itself does: parse via
// blockparser.ParseParameterPrivileges, then build via
// Builder.BuildParameterPrivileges.
func buildParameterPrivileges(t *testing.T, header, body string) *ir.ParameterPrivileges {
	t.Helper()
	block, err := blockparser.ParseParameterPrivileges(header, body, zeroPos)
	if err != nil {
		t.Fatalf("ParseParameterPrivileges: %v", err)
	}
	builder := ir.NewBuilder()
	objs, err := builder.BuildParameterPrivileges(block)
	if err != nil {
		t.Fatalf("BuildParameterPrivileges: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected exactly 1 IRObject, got %d", len(objs))
	}
	pp, ok := objs[0].(*ir.ParameterPrivileges)
	if !ok {
		t.Fatalf("expected *ir.ParameterPrivileges, got %T", objs[0])
	}
	return pp
}

// TestBuildParameterPrivileges guards the RFC's own worked example (Section
// 11.6): a GRANTS-only block with two parameters granted together to one role.
func TestBuildParameterPrivileges(t *testing.T) {
	pp := buildParameterPrivileges(t, "", `
		GRANTS {
			SET ON PARAMETER work_mem, statement_timeout TO app_admin;
		}
	`)
	if len(pp.Grants) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(pp.Grants))
	}
	g := pp.Grants[0]
	if !slices.Equal(g.Privileges, []string{"SET"}) {
		t.Errorf("Privileges: got %v", g.Privileges)
	}
	if !slices.Equal(g.Parameters, []string{"work_mem", "statement_timeout"}) {
		t.Errorf("Parameters: got %v", g.Parameters)
	}
	if !slices.Equal(g.Roles, []string{"app_admin"}) {
		t.Errorf("Roles: got %v", g.Roles)
	}
	if g.WithGrant {
		t.Error("WithGrant should be false")
	}
}

// TestBuildParameterPrivilegesQualifiedNameIsConstant guards that
// ParameterPrivileges.QualifiedName is a fixed singleton key — unlike
// DefaultPrivileges, which splits per (role, schema, object type), a DPG
// project declares at most one PARAMETER PRIVILEGES block and
// pg_parameter_acl has no role/schema/type dimension to key on.
func TestBuildParameterPrivilegesQualifiedNameIsConstant(t *testing.T) {
	pp := buildParameterPrivileges(t, "", `GRANTS { SET ON PARAMETER work_mem TO app_admin; }`)
	if got := pp.QualifiedName(); got != "PARAMETER PRIVILEGES" {
		t.Errorf("QualifiedName: got %q, want %q", got, "PARAMETER PRIVILEGES")
	}
}

// TestBuildParameterPrivilegesRevocation guards Revocations conversion,
// including Cascade.
func TestBuildParameterPrivilegesRevocation(t *testing.T) {
	pp := buildParameterPrivileges(t, "", `
		REVOCATIONS {
			ALTER SYSTEM ON PARAMETER shared_preload_libraries FROM app_readonly CASCADE;
		}
	`)
	if len(pp.Revocations) != 1 {
		t.Fatalf("expected 1 revocation, got %d", len(pp.Revocations))
	}
	r := pp.Revocations[0]
	if !slices.Equal(r.Privileges, []string{"ALTER SYSTEM"}) {
		t.Errorf("Privileges: got %v", r.Privileges)
	}
	if !slices.Equal(r.Parameters, []string{"shared_preload_libraries"}) {
		t.Errorf("Parameters: got %v", r.Parameters)
	}
	if !r.Cascade {
		t.Error("Cascade did not propagate")
	}
}
