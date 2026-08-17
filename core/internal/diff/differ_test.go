package diff

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	pg_query "github.com/pganalyze/pg_query_go/v6"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/snapshot"
)

func sha256Sum(s string) [32]byte {
	return sha256.Sum256([]byte(strings.TrimSpace(s)))
}

func TestDiffEmptyDesiredEmptySnap(t *testing.T) {
	d := New()
	ops, err := d.Diff(nil, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("want 0 ops, got %d", len(ops))
	}
}

func TestDiffCreateSchema(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Schema{Name: "myschema"},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected at least one op")
	}
	sql := ops[0].SQL()
	if !strings.Contains(sql, "CREATE SCHEMA") {
		t.Errorf("expected CREATE SCHEMA, got: %s", sql)
	}
	if !strings.Contains(sql, `"myschema"`) {
		t.Errorf("expected quoted schema name, got: %s", sql)
	}
	if ops[0].Safety() != pipeline.Safe {
		t.Errorf("expected Safe, got %s", ops[0].Safety())
	}
}

func TestDiffDropSchema(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("myschema", &snapshot.SnapObject{
		Kind:   "schema",
		Schema: &snapshot.SnapSchema{Name: "myschema"},
	})
	ops, err := d.Diff(nil, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected drop op")
	}
	sql := ops[0].SQL()
	if !strings.Contains(sql, "DROP SCHEMA") {
		t.Errorf("expected DROP SCHEMA, got: %s", sql)
	}
	if ops[0].Safety() != pipeline.Destructive {
		t.Errorf("expected Destructive, got %s", ops[0].Safety())
	}
}

func TestDiffSchemaCommentAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("myschema", &snapshot.SnapObject{
		Kind:   "schema",
		Schema: &snapshot.SnapSchema{Name: "myschema"},
	})
	comment := "reporting tables"
	desired := []pipeline.IRObject{
		&ir.Schema{Name: "myschema", Comment: &comment},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "COMMENT ON SCHEMA") || !containsSQL(ops, "'reporting tables'") {
		t.Errorf("expected COMMENT ON SCHEMA, got: %v", sqlList(ops))
	}
}

func TestDiffSchemaOwnerChanged(t *testing.T) {
	d := New()
	oldOwner := "alice"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("myschema", &snapshot.SnapObject{
		Kind:   "schema",
		Schema: &snapshot.SnapSchema{Name: "myschema", Owner: &oldOwner},
	})
	newOwner := "bob"
	desired := []pipeline.IRObject{
		&ir.Schema{Name: "myschema", Owner: &newOwner},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "ALTER SCHEMA") || !containsSQL(ops, "OWNER TO") || !containsSQL(ops, `"bob"`) {
		t.Errorf("expected ALTER SCHEMA ... OWNER TO, got: %v", sqlList(ops))
	}
}

func TestDiffSchemaUnchangedIsNoop(t *testing.T) {
	d := New()
	comment := "reporting tables"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("myschema", &snapshot.SnapObject{
		Kind:   "schema",
		Schema: &snapshot.SnapSchema{Name: "myschema", Comment: &comment},
	})
	desired := []pipeline.IRObject{
		&ir.Schema{Name: "myschema", Comment: &comment},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for unchanged schema, got: %v", sqlList(ops))
	}
}

func TestDiffCreateTable(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "users",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "integer"}, NotNull: true},
				{Name: "email", Type: ir.TypeRef{Name: "text"}, NotNull: true},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected ops")
	}
	sql := ops[0].SQL()
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Errorf("expected CREATE TABLE, got: %s", sql)
	}
	if !strings.Contains(sql, `"public"."users"`) {
		t.Errorf("expected qualified table name, got: %s", sql)
	}
	if !strings.Contains(sql, `"id"`) {
		t.Errorf("expected id column, got: %s", sql)
	}
}

// TestDiffCreateTableWithExcludeConstraint proves a fresh CREATE TABLE
// carrying a named EXCLUDE constraint emits the constraint's real SQL body
// (via createTable's generic table-level rendering path — EXCLUDE isn't in
// isInlineable/the inline-classification switch, so it must fall through to
// "CONSTRAINT name <Expr>" unchanged), not the old "EXCLUDE" placeholder
// that would already fail to apply.
func TestDiffCreateTableWithExcludeConstraint(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "bookings",
			Columns: []*ir.Column{
				{Name: "room", Type: ir.TypeRef{Name: "integer"}},
				{Name: "during", Type: ir.TypeRef{Name: "tsrange"}},
			},
			Constraints: []*ir.Constraint{
				{Name: "no_overlap", Type: "EXCLUDE", Columns: []string{"room", "during"},
					Exclude: &ir.ExcludeSpec{
						AccessMethod: "gist",
						Elements: []ir.ExcludeElement{
							{Column: "room", Operator: "="},
							{Column: "during", Operator: "&&"},
						},
					},
					Expr: `EXCLUDE USING gist ("room" WITH =, "during" WITH &&)`,
				},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected ops")
	}
	sql := ops[0].SQL()
	if !strings.Contains(sql, `CONSTRAINT "no_overlap" EXCLUDE USING gist ("room" WITH =, "during" WITH &&)`) {
		t.Errorf("expected the real EXCLUDE body inline in CREATE TABLE, got: %s", sql)
	}
	if strings.Contains(sql, `EXCLUDE;`) || strings.Contains(sql, `EXCLUDE,`) || strings.Contains(sql, `EXCLUDE\n`) {
		t.Errorf("must not regress to the old bare-EXCLUDE placeholder, got: %s", sql)
	}
}

func TestDiffDropTable(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.users", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "users",
		},
	})
	ops, err := d.Diff(nil, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected drop op")
	}
	if !strings.Contains(ops[0].SQL(), "DROP TABLE") {
		t.Errorf("expected DROP TABLE, got: %s", ops[0].SQL())
	}
	if ops[0].Safety() != pipeline.Destructive {
		t.Errorf("expected Destructive")
	}
}

func TestDiffAddColumn(t *testing.T) {
	d := New()

	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.users", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "users",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "integer"}},
		},
	})

	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "users",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "integer"}},
				{Name: "email", Type: ir.TypeRef{Name: "text"}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var addOp pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "ADD COLUMN") {
			addOp = o
			break
		}
	}
	if addOp == nil {
		t.Fatal("expected ADD COLUMN op")
	}
	if !strings.Contains(addOp.SQL(), `"email"`) {
		t.Errorf("expected email column, got: %s", addOp.SQL())
	}
}

// TestDiffAddGeneratedColumn guards a real bug found live-testing a demo
// project: createTable's column-rendering loop has always handled
// col.Generated (GENERATED ALWAYS AS (expr) STORED), but the separate
// ALTER TABLE ADD COLUMN branch — used when a generated column is added to
// an already-existing table, not declared with the table from the start —
// never checked col.Generated at all, silently emitting a plain, ungenerated
// column instead. Confirmed live: `dpg plan` for a table that already had an
// `amount` column produced `ADD COLUMN "amount_with_tax" numeric(10,2);`
// with no GENERATED clause whatsoever, for a column declared as
// `GENERATED ALWAYS AS (amount * 1.08) STORED`.
func TestDiffAddGeneratedColumn(t *testing.T) {
	d := New()

	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "amount", Type: "numeric"}},
		},
	})

	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "orders",
			Columns: []*ir.Column{
				{Name: "amount", Type: ir.TypeRef{Name: "numeric"}},
				{
					Name: "amount_with_tax", Type: ir.TypeRef{Name: "numeric"},
					Generated: &ir.Generated{Expr: "amount * 1.08", Stored: true},
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var addOp pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "ADD COLUMN") {
			addOp = o
			break
		}
	}
	if addOp == nil {
		t.Fatal("expected ADD COLUMN op")
	}
	if !strings.Contains(addOp.SQL(), "GENERATED ALWAYS AS (amount * 1.08) STORED") {
		t.Errorf("expected GENERATED ALWAYS AS (...) STORED clause, got: %s", addOp.SQL())
	}
}

func TestDiffDropColumn(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.users", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "users",
			Columns: []snapshot.SnapColumn{
				{Name: "id", Type: "integer"},
				{Name: "old_col", Type: "text"},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "users",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "integer"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var dropOp pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "DROP COLUMN") {
			dropOp = o
			break
		}
	}
	if dropOp == nil {
		t.Fatal("expected DROP COLUMN op")
	}
	if !strings.Contains(dropOp.SQL(), `"old_col"`) {
		t.Errorf("expected old_col, got: %s", dropOp.SQL())
	}
	if dropOp.Safety() != pipeline.Destructive {
		t.Errorf("expected Destructive")
	}
}

func TestDiffRenameTable(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.users", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "users",
		},
	})

	old := "users"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:      "public",
			Name:        "accounts",
			RenamedFrom: &old,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var renameOp pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "RENAME TO") {
			renameOp = o
			break
		}
	}
	if renameOp == nil {
		t.Fatalf("expected RENAME TO op, got ops: %v", sqlList(ops))
	}
	if !strings.Contains(renameOp.SQL(), `"accounts"`) {
		t.Errorf("expected new name in rename, got: %s", renameOp.SQL())
	}
}

func TestDiffNoChanges(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.users", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "users",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "integer", NotNull: true}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "users",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "integer"}, NotNull: true}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for identical state, got: %v", sqlList(ops))
	}
}

func TestDiffCreateIndex(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "users",
			Columns: []*ir.Column{{Name: "email", Type: ir.TypeRef{Name: "text"}}},
			Indexes: []*ir.Index{
				{Name: "users_email_idx", Method: "btree", Columns: []pipeline.IndexColumn{{Name: "email"}}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	var idxOp pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "CREATE") && strings.Contains(o.SQL(), "INDEX") {
			idxOp = o
			break
		}
	}
	if idxOp == nil {
		t.Fatalf("expected CREATE INDEX op, got: %v", sqlList(ops))
	}
	if idxOp.Safety() != pipeline.Caution {
		t.Errorf("expected Caution safety for index")
	}
}

// TestDiffConcurrentIndexOnNewTableIsSuppressed guards a real correctness
// rule, not just a preference: PostgreSQL rejects CREATE INDEX CONCURRENTLY
// inside a transaction block, and an index on a brand-new table is always
// emitted in the SAME transactional migration as its CREATE TABLE — so an
// explicit CONCURRENTLY on a new table's index must be silently suppressed,
// never honored. See createIndex's doc comment and createTable's index loop.
func TestDiffConcurrentIndexOnNewTableIsSuppressed(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "users",
			Columns: []*ir.Column{{Name: "email", Type: ir.TypeRef{Name: "text"}}},
			Indexes: []*ir.Index{
				{Name: "users_email_idx", Method: "btree", Concurrently: true,
					Columns: []pipeline.IndexColumn{{Name: "email"}}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "CONCURRENTLY") {
			t.Fatalf("new-table index must never be CONCURRENTLY (same-transaction PG restriction), got: %v", sqlList(ops))
		}
	}
}

// TestDiffConcurrentIndexOnExistingTableExplicit guards the case CONCURRENTLY
// actually applies: an EXISTING table (already in the snapshot) gaining a new
// index with an explicit CONCURRENTLY in source. There is no project-wide
// default — CONCURRENTLY only ever fires when the source writes it, exactly
// like real PostgreSQL's own bare CONCURRENTLY keyword.
func TestDiffConcurrentIndexOnExistingTableExplicit(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.users", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Columns: []snapshot.SnapColumn{{Name: "email", Type: "text"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "users",
			Columns: []*ir.Column{{Name: "email", Type: ir.TypeRef{Name: "text"}}},
			Indexes: []*ir.Index{
				{Name: "users_email_idx", Method: "btree", Concurrently: true,
					Columns: []pipeline.IndexColumn{{Name: "email"}}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var idxOp pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "CONCURRENTLY") {
			idxOp = o
			break
		}
	}
	if idxOp == nil {
		t.Fatalf("expected CONCURRENTLY index op, got: %v", sqlList(ops))
	}
	if idxOp.Safety() != pipeline.Manual {
		t.Errorf("expected Manual safety for concurrent index")
	}
	if idxOp.Transactional() {
		t.Errorf("concurrent index must not be transactional")
	}
}

// TestDiffIndexWithoutConcurrentlyIsNeverConcurrent guards the absence of any
// implicit default: a new index on an existing table with no explicit
// CONCURRENTLY in source must never emit CONCURRENTLY — there is nothing
// (no project config, no other signal) that can turn it on.
func TestDiffIndexWithoutConcurrentlyIsNeverConcurrent(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.users", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Columns: []snapshot.SnapColumn{{Name: "email", Type: "text"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "users",
			Columns: []*ir.Column{{Name: "email", Type: ir.TypeRef{Name: "text"}}},
			Indexes: []*ir.Index{
				{Name: "users_email_idx", Method: "btree",
					Columns: []pipeline.IndexColumn{{Name: "email"}}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(sqlList(ops), "\n"), "CONCURRENTLY") {
		t.Errorf("expected no CONCURRENTLY without an explicit source keyword, got: %v", sqlList(ops))
	}
}

func TestDiffEnumAddValue(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.status", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{
			Schema:  "public",
			Name:    "status",
			Variant: "ENUM",
			Values:  []string{"active", "inactive"},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema:     "public",
			Name:       "status",
			Variant:    "ENUM",
			EnumValues: []string{"active", "inactive", "pending"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected ADD VALUE op")
	}
	sql := ops[0].SQL()
	if !strings.Contains(sql, "ADD VALUE") {
		t.Errorf("expected ADD VALUE, got: %s", sql)
	}
	if !strings.Contains(sql, "'pending'") {
		t.Errorf("expected pending value, got: %s", sql)
	}
	if ops[0].Safety() != pipeline.Manual {
		t.Errorf("expected Manual safety for ADD VALUE")
	}
}

func TestDiffViewChanged(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.active_users", &snapshot.SnapObject{
		Kind: "view",
		View: &snapshot.SnapView{
			Schema: "public",
			Name:   "active_users",
			Query:  "SELECT * FROM users WHERE active = true",
		},
	})
	desired := []pipeline.IRObject{
		&ir.View{
			Schema: "public",
			Name:   "active_users",
			Query:  "SELECT id, email FROM users WHERE active = true",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected CREATE OR REPLACE VIEW op")
	}
	if !strings.Contains(ops[0].SQL(), "CREATE OR REPLACE VIEW") {
		t.Errorf("expected CREATE OR REPLACE VIEW, got: %s", ops[0].SQL())
	}
}

func TestDiffFunctionChanged(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.my_func()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema:     "public",
			Name:       "my_func",
			Args:       "",
			ReturnType: "void",
			Language:   "plpgsql",
			Volatility: "VOLATILE",
			BodyHash:   "oldhash",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema:     "public",
			Name:       "my_func",
			ReturnType: ir.TypeRef{Name: "void"},
			BodyHash:   "newhash",
			Attrs: ir.FuncAttrs{
				Language:   "plpgsql",
				Volatility: "VOLATILE",
				Body:       "BEGIN END;",
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected CREATE OR REPLACE FUNCTION op")
	}
	if !strings.Contains(ops[0].SQL(), "CREATE OR REPLACE FUNCTION") {
		t.Errorf("expected CREATE OR REPLACE FUNCTION, got: %s", ops[0].SQL())
	}
}

// TestDiffCreateFunctionEmitsParallelCostRows proves a fresh CREATE FUNCTION
// emits explicit PARALLEL/COST/ROWS clauses when the desired side declares
// them, in PostgreSQL's own documented attribute ordering (LANGUAGE ->
// volatility -> STRICT -> SECURITY DEFINER -> PARALLEL -> COST -> ROWS -> AS).
func TestDiffCreateFunctionEmitsParallelCostRows(t *testing.T) {
	d := New()
	cost := 500.0
	rows := 50.0
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f",
			ReturnType: ir.TypeRef{Name: "integer"},
			Attrs: ir.FuncAttrs{
				Language: "sql", Volatility: "STABLE", Parallel: "SAFE",
				Cost: &cost, Rows: &rows, Body: "SELECT 1",
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected a CREATE FUNCTION op")
	}
	sql := ops[0].SQL()
	if !strings.Contains(sql, "PARALLEL SAFE") {
		t.Errorf("expected PARALLEL SAFE, got: %s", sql)
	}
	if !strings.Contains(sql, "COST 500") {
		t.Errorf("expected COST 500, got: %s", sql)
	}
	if !strings.Contains(sql, "ROWS 50") {
		t.Errorf("expected ROWS 50, got: %s", sql)
	}
}

// TestDiffCreateFunctionOmitsDefaultParallelCostRows proves the common case
// (no explicit PARALLEL/COST/ROWS in source) renders none of those clauses
// — PARALLEL UNSAFE is PostgreSQL's own default and would be pure noise if
// always emitted, and unset Cost/Rows have nothing meaningful to render.
func TestDiffCreateFunctionOmitsDefaultParallelCostRows(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f",
			ReturnType: ir.TypeRef{Name: "integer"},
			Attrs:      ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT 1"},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	sql := ops[0].SQL()
	if strings.Contains(sql, "PARALLEL") || strings.Contains(sql, "COST") || strings.Contains(sql, "ROWS") {
		t.Errorf("expected no PARALLEL/COST/ROWS clause for the default case, got: %s", sql)
	}
}

// TestDiffFunctionParallelChanged proves an explicit PARALLEL change alone
// (no body/language/volatility change) still triggers CREATE OR REPLACE, when
// the snapshot already carries a real (non-empty) Parallel value — the common
// case for any snapshot written by this feature's own introspection/apply
// path. See TestDiffFunctionLegacySnapshotParallelIsNoop for the other case
// (an empty snapshot value from a pre-upgrade snapshot.json), which must NOT
// be treated as a genuine difference.
func TestDiffFunctionParallelChanged(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.f()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema: "public", Name: "f", ReturnType: "integer",
			Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", BodyHash: "h",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f",
			ReturnType: ir.TypeRef{Name: "integer"},
			BodyHash:   "h",
			Attrs:      ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "SAFE", Body: "SELECT 1"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 || !strings.Contains(ops[0].SQL(), "CREATE OR REPLACE FUNCTION") {
		t.Errorf("expected CREATE OR REPLACE FUNCTION for the PARALLEL change, got: %v", sqlList(ops))
	}
}

// TestDiffFunctionExplicitCostChanged proves an explicit COST change
// against a snapshot with a different value triggers CREATE OR REPLACE.
func TestDiffFunctionExplicitCostChanged(t *testing.T) {
	d := New()
	oldCost := 100.0
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.f()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema: "public", Name: "f", ReturnType: "integer",
			Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Cost: &oldCost, BodyHash: "h",
		},
	})
	newCost := 500.0
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f",
			ReturnType: ir.TypeRef{Name: "integer"},
			BodyHash:   "h",
			Attrs:      ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Cost: &newCost, Body: "SELECT 1"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 || !strings.Contains(ops[0].SQL(), "COST 500") {
		t.Errorf("expected a recreate with COST 500, got: %v", sqlList(ops))
	}
}

// TestDiffFunctionUnspecifiedCostRowsIsNoop is the core regression guard
// for the suppress-when-unspecified rule: a snapshot carrying a concrete
// Cost/Rows (e.g. from a prior live introspection, which always records
// PostgreSQL's own real catalog value) must NOT be treated as drift when
// the desired side simply never mentions COST/ROWS at all — the same
// "only act when the desired side actually declares it" rule already
// established for column STORAGE.
func TestDiffFunctionUnspecifiedCostRowsIsNoop(t *testing.T) {
	d := New()
	introspectedCost := 250.0
	introspectedRows := 75.0
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.f()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema: "public", Name: "f", ReturnType: "integer",
			Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE",
			Cost: &introspectedCost, Rows: &introspectedRows, BodyHash: "h",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f",
			ReturnType: ir.TypeRef{Name: "integer"},
			BodyHash:   "h",
			// Cost/Rows intentionally nil — source never mentions COST/ROWS.
			Attrs: ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT 1"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unspecified COST/ROWS must not flag drift against a live-introspected default), got: %v", sqlList(ops))
	}
}

// TestDiffFunctionLegacySnapshotParallelIsNoop guards a real asymmetry an
// independent verification pass caught: unlike Cost/Rows (a *float64, nil on
// the DESIRED side means "unspecified"), Parallel is a plain string that the
// IR builder ALWAYS defaults to a concrete "UNSAFE" on the desired side even
// when source never mentions PARALLEL. A snapshot.json written before this
// field existed has no "parallel" JSON key at all, so snap.Parallel comes
// back as the Go zero value "" — comparing that bare-string against the
// desired side's always-concrete "UNSAFE" would spuriously flag every
// function in every pre-existing project as changed on the very first
// plan/apply after upgrading, which is exactly the primary offline workflow
// this project's CLAUDE.md calls out as must-work-without-a-DB. Confirmed
// this reproduces before the fix (parallelChanged treating snap=="" as
// "unknown, don't diff yet") and disappears after.
func TestDiffFunctionLegacySnapshotParallelIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.f()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			// Parallel deliberately omitted, simulating a pre-upgrade
			// snapshot.json with no "parallel" key at all.
			Schema: "public", Name: "f", ReturnType: "integer",
			Language: "sql", Volatility: "VOLATILE", BodyHash: "h",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f",
			ReturnType: ir.TypeRef{Name: "integer"},
			BodyHash:   "h",
			// source never mentions PARALLEL -> builder defaults to "UNSAFE"
			Attrs: ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT 1"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops against a legacy (pre-feature) snapshot with no recorded Parallel value, got: %v", sqlList(ops))
	}
}

// TestDiffCreateFunctionEmitsSetOf proves a fresh CREATE for a SETOF function
// renders the SETOF keyword. ReturnType.SetOf was previously never read
// anywhere (typeNameToRef never looked at pg_query's TypeName.Setof field),
// so DPG silently dropped SETOF from every function it compiled.
func TestDiffCreateFunctionEmitsSetOf(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f",
			ReturnType: ir.TypeRef{Name: "integer", SetOf: true},
			Attrs:      ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT n"},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 || !strings.Contains(ops[0].SQL(), "RETURNS SETOF integer") {
		t.Errorf("expected RETURNS SETOF integer, got: %v", sqlList(ops))
	}
}

// TestDiffCreateFunctionEmitsReturnsTable proves a RETURNS TABLE(...) function
// renders correctly: the TABLE-mode columns must appear ONLY inside the
// RETURNS TABLE(...) clause, never inline in the main parameter list (that
// was the actual bug — buildFunctionSQL previously wrote "TABLE a integer"
// as a literal, invalid parameter-mode prefix into the main parens).
func TestDiffCreateFunctionEmitsReturnsTable(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f",
			ReturnType: ir.TypeRef{Name: "record", SetOf: true},
			Args: []ir.FuncArg{
				{Name: "n", Mode: "IN", Type: ir.TypeRef{Name: "integer"}},
				{Name: "a", Mode: "TABLE", Type: ir.TypeRef{Name: "integer"}},
				{Name: "b", Mode: "TABLE", Type: ir.TypeRef{Name: "text"}},
			},
			Attrs: ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT 1, 'x'"},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected a CREATE FUNCTION op")
	}
	sql := ops[0].SQL()
	if !strings.Contains(sql, "(n integer) RETURNS TABLE(a integer, b text)") {
		t.Errorf("expected the main parens to hold only n, and RETURNS TABLE(a integer, b text), got: %s", sql)
	}
	if strings.Contains(sql, "TABLE a integer") || strings.Contains(sql, "TABLE b text") {
		t.Errorf("TABLE-mode columns must not be rendered inline in the parameter list, got: %s", sql)
	}
}

// TestDiffFunctionReturnTypeChangeRequiresDropCreate proves a return-type
// change (here: adding SETOF) is NOT rendered as CREATE OR REPLACE FUNCTION —
// confirmed live against postgres:17 that PostgreSQL rejects that outright
// ("cannot change return type of existing function", hinting to DROP
// FUNCTION first). It must instead be a DROP FUNCTION followed by a fresh
// CREATE FUNCTION, the same DROP-required pattern diffView already uses for
// a materialized view's query change.
func TestDiffFunctionReturnTypeChangeRequiresDropCreate(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.f()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema: "public", Name: "f", ReturnType: "integer", ReturnsSet: false,
			Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", BodyHash: "h",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f",
			ReturnType: ir.TypeRef{Name: "integer", SetOf: true},
			BodyHash:   "h",
			Attrs:      ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT n"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) < 2 {
		t.Fatalf("expected a DROP FUNCTION + CREATE FUNCTION pair, got: %v", sqlList(ops))
	}
	if !strings.Contains(ops[0].SQL(), "DROP FUNCTION") {
		t.Errorf("expected the first op to be DROP FUNCTION, got: %s", ops[0].SQL())
	}
	if ops[0].Safety() != pipeline.Destructive {
		t.Errorf("expected DROP FUNCTION to be classified DESTRUCTIVE, got: %v", ops[0].Safety())
	}
	if !strings.Contains(ops[1].SQL(), "CREATE OR REPLACE FUNCTION") || !strings.Contains(ops[1].SQL(), "RETURNS SETOF integer") {
		t.Errorf("expected the second op to (re)create with RETURNS SETOF integer, got: %s", ops[1].SQL())
	}
}

// TestDiffFunctionSetOfUnchangedIsNoop is the zero-drift control: matching
// SetOf on both sides (here: both false, the common case) must not itself
// trigger the new DROP+CREATE path.
func TestDiffFunctionSetOfUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.f()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema: "public", Name: "f", ReturnType: "integer", ReturnsSet: false,
			Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", BodyHash: "h",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f",
			ReturnType: ir.TypeRef{Name: "integer", SetOf: false},
			BodyHash:   "h",
			Attrs:      ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT n"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for an unchanged return type, got: %v", sqlList(ops))
	}
}

// TestDiffFunctionReturnTableColumnListChanged proves a RETURNS TABLE(...)
// column-list edit (here: adding a column) is detected — ReturnType/SetOf
// alone can't tell two different TABLE column lists apart, since PostgreSQL's
// own catalog reports "record"/true for a TABLE-mode function regardless of
// its actual columns; this exercises the ReturnTable comparison specifically.
func TestDiffFunctionReturnTableColumnListChanged(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.f()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema: "public", Name: "f", ReturnType: "record", ReturnsSet: true, ReturnTable: "a integer",
			Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", BodyHash: "h",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f",
			ReturnType: ir.TypeRef{Name: "record", SetOf: true},
			Args: []ir.FuncArg{
				{Name: "a", Mode: "TABLE", Type: ir.TypeRef{Name: "integer"}},
				{Name: "b", Mode: "TABLE", Type: ir.TypeRef{Name: "text"}},
			},
			BodyHash: "h",
			Attrs:    ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT 1, 'x'"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) < 2 {
		t.Fatalf("expected a DROP FUNCTION + CREATE FUNCTION pair for the column-list change, got: %v", sqlList(ops))
	}
	if !strings.Contains(ops[0].SQL(), "DROP FUNCTION") {
		t.Errorf("expected the first op to be DROP FUNCTION, got: %s", ops[0].SQL())
	}
	if !strings.Contains(ops[1].SQL(), "RETURNS TABLE(a integer, b text)") {
		t.Errorf("expected the recreate to declare RETURNS TABLE(a integer, b text), got: %s", ops[1].SQL())
	}
}

// TestDiffFunctionReturnTableUnchangedIsNoop is the zero-drift control for
// ReturnTable: an identical TABLE column list on both sides must not trigger
// the DROP+CREATE path.
func TestDiffFunctionReturnTableUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.f()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema: "public", Name: "f", ReturnType: "record", ReturnsSet: true, ReturnTable: "a integer, b text",
			Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", BodyHash: "h",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f",
			ReturnType: ir.TypeRef{Name: "record", SetOf: true},
			Args: []ir.FuncArg{
				{Name: "a", Mode: "TABLE", Type: ir.TypeRef{Name: "integer"}},
				{Name: "b", Mode: "TABLE", Type: ir.TypeRef{Name: "text"}},
			},
			BodyHash: "h",
			Attrs:    ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT 1, 'x'"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for an unchanged TABLE column list, got: %v", sqlList(ops))
	}
}

// TestBuildFunctionSQLImplicitReturnsSingleOut guards the fix for a function
// whose source omitted RETURNS entirely (valid PostgreSQL for a signature
// with at least one OUT/INOUT parameter — confirmed live against postgres:17
// that PG itself computes and stores a concrete return type in this case).
// Before the ir.Builder fix (internal/ir/builder.go's impliedReturnType),
// ir.Function.ReturnType stayed the zero TypeRef for this input, and
// buildFunctionSQL rendered a bare "RETURNS  LANGUAGE ..." (double space,
// empty type) — a real syntax error on apply. This test operates purely at
// the differ layer (constructing the ir.Function as the builder now would,
// rather than re-parsing source) since buildFunctionSQL is what actually
// turns ReturnType into SQL text.
func TestBuildFunctionSQLImplicitReturnsSingleOut(t *testing.T) {
	fn := &ir.Function{
		Schema: "public", Name: "f_single_out",
		Args: []ir.FuncArg{
			{Name: "n", Mode: "IN", Type: ir.TypeRef{Name: "integer"}},
			{Name: "a", Mode: "OUT", Type: ir.TypeRef{Name: "integer"}},
		},
		ReturnType: ir.TypeRef{Name: "integer"},
		Attrs:      ir.FuncAttrs{Language: "sql", Body: "SELECT n + 1"},
	}
	sql := buildFunctionSQL(fn)
	if strings.Contains(sql, "RETURNS  LANGUAGE") || strings.Contains(sql, "RETURNS  ") {
		t.Fatalf("expected no bare/empty RETURNS clause, got: %s", sql)
	}
	if !strings.Contains(sql, "RETURNS integer LANGUAGE") {
		t.Errorf("expected RETURNS integer LANGUAGE, got: %s", sql)
	}
	if !strings.Contains(sql, "OUT a integer") {
		t.Errorf("expected the OUT parameter to render inline, got: %s", sql)
	}
}

// TestBuildFunctionSQLImplicitReturnsMultiOut is the multi-OUT-parameter
// sibling: PostgreSQL implies "record" (not the type of any one column) when
// more than one OUT/INOUT parameter is present and RETURNS is omitted.
func TestBuildFunctionSQLImplicitReturnsMultiOut(t *testing.T) {
	fn := &ir.Function{
		Schema: "public", Name: "f_multi_out",
		Args: []ir.FuncArg{
			{Name: "n", Mode: "IN", Type: ir.TypeRef{Name: "integer"}},
			{Name: "a", Mode: "OUT", Type: ir.TypeRef{Name: "integer"}},
			{Name: "b", Mode: "OUT", Type: ir.TypeRef{Name: "text"}},
		},
		ReturnType: ir.TypeRef{Name: "record"},
		Attrs:      ir.FuncAttrs{Language: "sql", Body: "SELECT n, 'x'"},
	}
	sql := buildFunctionSQL(fn)
	if !strings.Contains(sql, "RETURNS record LANGUAGE") {
		t.Errorf("expected RETURNS record LANGUAGE, got: %s", sql)
	}
}

// TestBuildFunctionSQLDollarQuoteCollision guards a real bug fixed alongside
// the plpgsql body-hash canonicalization work: buildFunctionSQL used to
// hardcode a literal "$$" delimiter with no collision handling, so a body
// that itself contains "$$" (e.g. dynamic SQL using a dollar-quoted string
// literal) would produce broken, unparseable migration SQL. It now delegates
// to ir.RenderCreateFunctionSQL, which picks a colliding-free tag.
func TestBuildFunctionSQLDollarQuoteCollision(t *testing.T) {
	fn := &ir.Function{
		Schema:     "public",
		Name:       "f",
		ReturnType: ir.TypeRef{Name: "text"},
		Attrs:      ir.FuncAttrs{Language: "sql", Body: "SELECT $$literal$$"},
	}
	sql := buildFunctionSQL(fn)
	if _, err := pg_query.Parse(sql); err != nil {
		t.Fatalf("rendered SQL with a body containing a literal $$ failed to parse: %v\nSQL: %s", err, sql)
	}
}

// TestDiffFunctionImplicitReturnsNoLiveDrift proves that the implied return
// type computed for an omitted-RETURNS function (ir.Builder's
// impliedReturnType) matches what live introspection reports for the same
// function — i.e. this doesn't just fix the syntax-error half of the bug,
// it also avoids a permanent self-inconsistent DROP+CREATE loop on every
// verify/plan --live (which an offline-only fix, e.g. simply omitting the
// RETURNS clause from the rendered SQL, would NOT have avoided: the snapshot
// side's ReturnType is always concrete since pg_proc.prorettype is never
// null).
func TestDiffFunctionImplicitReturnsNoLiveDrift(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.f_single_out(integer)", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema: "public", Name: "f_single_out", ReturnType: "integer", ReturnsSet: false,
			Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", BodyHash: "h",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema: "public", Name: "f_single_out",
			Args: []ir.FuncArg{
				{Name: "n", Mode: "IN", Type: ir.TypeRef{Name: "integer"}},
				{Name: "a", Mode: "OUT", Type: ir.TypeRef{Name: "integer"}},
			},
			ReturnType: ir.TypeRef{Name: "integer"}, // as impliedReturnType now computes it
			BodyHash:   "h",
			Attrs:      ir.FuncAttrs{Language: "sql", Volatility: "VOLATILE", Parallel: "UNSAFE", Body: "SELECT n + 1"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops once the implied return type matches the live-introspected one, got: %v", sqlList(ops))
	}
}

func TestDiffRegistration(t *testing.T) {
	d, ok := pipeline.Resolve[pipeline.Differ](pipeline.Default, pipeline.KeyDiffer)
	if !ok {
		t.Fatal("Differ not registered")
	}
	if d == nil {
		t.Fatal("registered Differ is nil")
	}
}

// TestDiffColumnRenameMissingSnapshotErrors verifies RFC §7.4 step 5: a
// RENAMED FROM that names a column the snapshot doesn't contain is a compiler
// error rather than a silent fall-through to ADD COLUMN.
func TestDiffColumnRenameMissingSnapshotErrors(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.users", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "users",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	stale := "ghost_col" // not in snapshot
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "users",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{Name: "email", Type: ir.TypeRef{Name: "text"}, RenamedFrom: &stale},
			},
		},
	}
	_, err := d.Diff(desired, snap)
	if err == nil {
		t.Fatal("expected diff error for stale RENAMED FROM, got nil")
	}
	for _, want := range []string{"RENAMED FROM", `"ghost_col"`, `"email"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q: %s", want, err.Error())
		}
	}
}

// TestDiffTableRenameMissingSnapshotErrors verifies the same rule for table-
// level RENAMED FROM directives. Without the guard, a stale rename silently
// degrades to a CREATE TABLE that loses the link to the original.
func TestDiffTableRenameMissingSnapshotErrors(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	stale := "ghost_table"
	desired := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "accounts", RenamedFrom: &stale},
	}
	_, err := d.Diff(desired, snap)
	if err == nil {
		t.Fatal("expected diff error for stale table RENAMED FROM, got nil")
	}
	if !strings.Contains(err.Error(), "RENAMED FROM") || !strings.Contains(err.Error(), "ghost_table") {
		t.Errorf("error missing expected substrings: %s", err.Error())
	}
}

// TestDiffTableRenamePostApplyIsNoop verifies the post-apply state: once the
// rename has run and the snapshot has been rewritten to the new name, leaving
// RENAMED FROM in the source must not error and must not regenerate the
// rename. Without this, every directive would become a one-shot that the user
// has to remove after applying — defeating the point of declarative state.
func TestDiffTableRenamePostApplyIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.accounts", &snapshot.SnapObject{
		Kind:  "table",
		Table: &snapshot.SnapTable{Schema: "public", Name: "accounts"},
	})
	stale := "users" // already-applied rename: not in snapshot
	desired := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "accounts", RenamedFrom: &stale},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatalf("expected no error in post-apply state, got: %v", err)
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "RENAME TO") {
			t.Errorf("did not expect a RENAME TO op in post-apply state, got: %s", o.SQL())
		}
	}
}

// TestDiffTableRenameStateDDropsOrphan verifies the symmetric case: snapshot
// has both the old and new names (a partial apply or hand-edited snapshot).
// Old behaviour erred. New behaviour treats it as cleanup — Pass 2 drops the
// orphaned old name, Pass 3 diffs the new one.
func TestDiffTableRenameStateDDropsOrphan(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.users", &snapshot.SnapObject{
		Kind:  "table",
		Table: &snapshot.SnapTable{Schema: "public", Name: "users"},
	})
	_ = snap.SetObject("public.accounts", &snapshot.SnapObject{
		Kind:  "table",
		Table: &snapshot.SnapTable{Schema: "public", Name: "accounts"},
	})
	stale := "users"
	desired := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "accounts", RenamedFrom: &stale},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatalf("expected no error for State D, got: %v", err)
	}
	var sawDropUsers bool
	for _, o := range ops {
		sql := o.SQL()
		if strings.Contains(sql, "DROP TABLE") && strings.Contains(sql, `"users"`) {
			sawDropUsers = true
		}
		if strings.Contains(sql, "RENAME TO") {
			t.Errorf("did not expect RENAME TO in State D, got: %s", sql)
		}
	}
	if !sawDropUsers {
		t.Errorf("expected DROP TABLE for orphaned users, got: %v", sqlList(ops))
	}
}

// TestDiffColumnRenamePostApplyIsNoop is the column-level analogue: the rename
// has been applied, the snapshot has the new column name, and RENAMED FROM is
// still in the source. Must not error and must not emit a redundant
// ALTER TABLE RENAME COLUMN.
func TestDiffColumnRenamePostApplyIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.users", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "users",
			Columns: []snapshot.SnapColumn{
				{Name: "id", Type: "bigint"},
				{Name: "email_address", Type: "text"},
			},
		},
	})
	stale := "email"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "users",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{Name: "email_address", Type: ir.TypeRef{Name: "text"}, RenamedFrom: &stale},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatalf("expected no error in column post-apply state, got: %v", err)
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "RENAME COLUMN") {
			t.Errorf("did not expect RENAME COLUMN in post-apply state, got: %s", o.SQL())
		}
	}
}

// TestDiffColumnRenameKeepsConstraints verifies that a RENAMED FROM directive
// doesn't manufacture a spurious drop+recreate of constraints whose snapshot
// expression still references the pre-rename column name.
// TestDiffValidateConstraintOnNotValidRemoval guards a real gap found live-
// testing a demo project: the RFC's NOT VALID lifecycle (§7.3) documents
// that removing NOT VALID from source must emit
// ALTER TABLE ... VALIDATE CONSTRAINT ..., but diffConstraints' identity key
// (name/type/expr only) never compared NotValid at all — a constraint
// transitioning NotValid: true -> false was invisible to the differ and
// silently produced "no changes" instead.
func TestDiffValidateConstraintOnNotValidRemoval(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "amount", Type: "numeric"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "ck_amount_positive", Type: "CHECK", Expr: "CHECK (amount > 0)", NotValid: true},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "orders",
			Columns: []*ir.Column{{Name: "amount", Type: ir.TypeRef{Name: "numeric"}}},
			Constraints: []*ir.Constraint{
				{Name: "ck_amount_positive", Type: "CHECK", Expr: "CHECK (amount > 0)"},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, o := range ops {
		if strings.Contains(o.SQL(), "VALIDATE CONSTRAINT") {
			found = true
			if !strings.Contains(o.SQL(), `"ck_amount_positive"`) {
				t.Errorf("VALIDATE CONSTRAINT does not name the constraint: %s", o.SQL())
			}
			if o.Safety() != pipeline.Caution {
				t.Errorf("expected CAUTION safety for VALIDATE CONSTRAINT, got %v", o.Safety())
			}
		}
		if strings.Contains(o.SQL(), "ADD CONSTRAINT") {
			t.Errorf("did not expect a re-ADD of an already-existing constraint, got: %s", o.SQL())
		}
	}
	if !found {
		t.Fatalf("expected ALTER TABLE ... VALIDATE CONSTRAINT ..., got: %v", sqlList(ops))
	}
}

// TestDiffNoValidateConstraintWhenAlreadyValid guards against a spurious
// VALIDATE CONSTRAINT when the snapshot's constraint was never NOT VALID to
// begin with — nothing to validate, so no-op.
func TestDiffNoValidateConstraintWhenAlreadyValid(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "amount", Type: "numeric"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "ck_amount_positive", Type: "CHECK", Expr: "CHECK (amount > 0)", NotValid: false},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "orders",
			Columns: []*ir.Column{{Name: "amount", Type: ir.TypeRef{Name: "numeric"}}},
			Constraints: []*ir.Constraint{
				{Name: "ck_amount_positive", Type: "CHECK", Expr: "CHECK (amount > 0)"},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for an already-valid constraint, got: %v", sqlList(ops))
	}
}

// TestCreateForeignTableEmitsServerOptions guards a real bug found live-
// testing a demo project: createTable wrote the "CREATE FOREIGN TABLE"
// keyword but never appended SERVER/OPTIONS — not just incomplete, an
// outright PostgreSQL syntax error, since SERVER is mandatory for a foreign
// table. Confirmed live: applying the old output failed immediately.
func TestCreateForeignTableEmitsServerOptions(t *testing.T) {
	d := New()
	server := "loopback_server"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:        "public",
			Name:          "remote_users",
			Foreign:       true,
			ForeignServer: &server,
			ForeignOptions: []pipeline.StorageParam{
				{Key: "table_name", Value: "users"},
				{Key: "schema_name", Value: "public"},
			},
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	var createOp pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "CREATE FOREIGN TABLE") {
			createOp = o
			break
		}
	}
	if createOp == nil {
		t.Fatalf("expected a CREATE FOREIGN TABLE op, got: %v", sqlList(ops))
	}
	sql := createOp.SQL()
	if !strings.Contains(sql, `SERVER "loopback_server"`) {
		t.Errorf("CREATE FOREIGN TABLE missing SERVER clause: %s", sql)
	}
	if !strings.Contains(sql, "OPTIONS (table_name 'users', schema_name 'public')") {
		t.Errorf("CREATE FOREIGN TABLE missing OPTIONS clause: %s", sql)
	}
}

// TestDiffForeignTableServerChangeDropRecreate guards that a SERVER change
// on an existing foreign table is DROP + CREATE — real PostgreSQL has no
// ALTER FOREIGN TABLE clause to change the server.
func TestDiffForeignTableServerChangeDropRecreate(t *testing.T) {
	d := New()
	oldServer := "server_a"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.remote_users", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public", Name: "remote_users",
			Foreign: true, ForeignServer: &oldServer,
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	newServer := "server_b"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "remote_users",
			Foreign: true, ForeignServer: &newServer,
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var sawDrop, sawCreate bool
	for _, o := range ops {
		if strings.Contains(o.SQL(), "DROP FOREIGN TABLE") {
			sawDrop = true
			if o.Safety() != pipeline.Destructive {
				t.Errorf("expected DESTRUCTIVE safety for DROP FOREIGN TABLE, got %v", o.Safety())
			}
		}
		if strings.Contains(o.SQL(), "CREATE FOREIGN TABLE") && strings.Contains(o.SQL(), `SERVER "server_b"`) {
			sawCreate = true
		}
	}
	if !sawDrop || !sawCreate {
		t.Fatalf("expected DROP FOREIGN TABLE + CREATE FOREIGN TABLE with new server, got: %v", sqlList(ops))
	}
}

// TestDiffForeignTableOptionsChange guards that an OPTIONS-only change uses
// real PostgreSQL's in-place ALTER FOREIGN TABLE ... OPTIONS (ADD/SET/DROP
// ...) rather than a needless drop+recreate.
func TestDiffForeignTableOptionsChange(t *testing.T) {
	d := New()
	server := "loopback_server"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.remote_users", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public", Name: "remote_users",
			Foreign: true, ForeignServer: &server,
			ForeignOptions: "table_name=users, stale_opt=x",
			Columns:        []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "remote_users",
			Foreign: true, ForeignServer: &server,
			ForeignOptions: []pipeline.StorageParam{
				{Key: "table_name", Value: "users_v2"}, // changed -> SET
				{Key: "schema_name", Value: "public"},  // new -> ADD
				// stale_opt absent -> DROP
			},
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var alterOp pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "ALTER FOREIGN TABLE") {
			alterOp = o
			break
		}
	}
	if alterOp == nil {
		t.Fatalf("expected ALTER FOREIGN TABLE ... OPTIONS, got: %v", sqlList(ops))
	}
	sql := alterOp.SQL()
	for _, want := range []string{"SET table_name 'users_v2'", "ADD schema_name 'public'", "DROP stale_opt"} {
		if !strings.Contains(sql, want) {
			t.Errorf("ALTER FOREIGN TABLE missing %q: %s", want, sql)
		}
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "DROP FOREIGN TABLE") {
			t.Errorf("did not expect drop+recreate for an OPTIONS-only change, got: %v", sqlList(ops))
		}
	}
}

func TestDiffColumnRenameKeepsConstraints(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("iam.groups", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "iam",
			Name:   "groups",
			Columns: []snapshot.SnapColumn{
				{Name: "id", Type: "bigint"},
				{Name: "locality_id", Type: "bigint"},
			},
			Constraints: []snapshot.SnapConstraint{
				{Name: "", Type: "FOREIGN KEY",
					Expr: `FOREIGN KEY ("locality_id") REFERENCES "iam"."localities" ("id")`},
			},
		},
	})

	old := "locality_id"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "iam",
			Name:   "groups",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{Name: "locale_id", Type: ir.TypeRef{Name: "bigint"}, RenamedFrom: &old},
			},
			Constraints: []*ir.Constraint{
				{Type: "FOREIGN KEY",
					Expr: `FOREIGN KEY ("locale_id") REFERENCES "iam"."localities" ("id")`},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		sql := o.SQL()
		if strings.Contains(sql, "WARNING") {
			t.Errorf("did not expect a constraint WARNING after RENAMED FROM, got: %s", sql)
		}
		if strings.Contains(sql, "ADD") && strings.Contains(sql, "FOREIGN KEY") {
			t.Errorf("did not expect FK to be re-added after RENAMED FROM, got: %s", sql)
		}
		if strings.Contains(sql, "DROP CONSTRAINT") {
			t.Errorf("did not expect DROP CONSTRAINT after RENAMED FROM, got: %s", sql)
		}
	}
}

// TestDiffColumnRenameKeepsIndex guards a real bug found live-testing a
// demo project: diffIndexes compared the desired index against the
// snapshot's SnapIndex verbatim, with no rename translation at all (unlike
// diffConstraints, which already translates constraint expressions via
// translateConstraintExpr) — so a same-named index whose column was renamed
// always compared unequal (old name in the snapshot vs. new name in
// desired) and got spuriously DROP + recreated, even though real
// PostgreSQL's ALTER TABLE RENAME COLUMN keeps every dependent index
// transparently, no rebuild needed at all.
// TestDiffColumnDeprecatedEmitsComment guards a real, functionally-total
// gap found live-testing a demo project: ir.Column.Deprecated was parsed,
// stored in the snapshot, and used by the linter (which is why `dpg plan`
// already printed a "deprecated" warning), but the differ never referenced
// it at all anywhere — a DEPRECATED directive had zero effect on generated
// SQL, despite the RFC documenting
// `COMMENT ON COLUMN t.c IS '[DEPRECATED] msg'` as the expected output.
func TestDiffColumnDeprecatedEmitsComment(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.room_bookings", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public", Name: "room_bookings",
			Columns: []snapshot.SnapColumn{
				{Name: "id", Type: "bigint"},
				{Name: "room", Type: "text"},
			},
		},
	})
	msg := "will be replaced by room_id"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "room_bookings",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{Name: "room", Type: ir.TypeRef{Name: "text"}, Deprecated: &msg},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, o := range ops {
		if strings.Contains(o.SQL(), "COMMENT ON COLUMN") {
			found = true
			want := `IS '[DEPRECATED] will be replaced by room_id';`
			if !strings.Contains(o.SQL(), want) {
				t.Errorf("COMMENT ON COLUMN text: got %q, want substring %q", o.SQL(), want)
			}
		}
	}
	if !found {
		t.Fatalf("expected a COMMENT ON COLUMN op for DEPRECATED, got: %v", sqlList(ops))
	}
}

// TestDiffColumnDeprecatedNoOpWhenUnchanged guards against re-emitting the
// COMMENT ON COLUMN every plan once DEPRECATED has already been applied —
// the snapshot's own Deprecated field (not just Comment) must be compared.
func TestDiffColumnDeprecatedNoOpWhenUnchanged(t *testing.T) {
	d := New()
	msg := "will be replaced by room_id"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.room_bookings", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public", Name: "room_bookings",
			Columns: []snapshot.SnapColumn{
				{Name: "id", Type: "bigint"},
				{Name: "room", Type: "text", Deprecated: &msg},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "room_bookings",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{Name: "room", Type: ir.TypeRef{Name: "text"}, Deprecated: &msg},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "COMMENT ON COLUMN") {
			t.Errorf("expected no COMMENT ON COLUMN op when DEPRECATED is unchanged, got: %s", o.SQL())
		}
	}
}

// TestDiffTableDeprecatedEmitsComment, TestDiffViewDeprecatedEmitsComment,
// and TestDiffFunctionDeprecatedEmitsComment guard the same gap as
// TestDiffColumnDeprecatedEmitsComment, for the RFC's other three
// DEPRECATED-bearing kinds (§19.1: "Applied to tables, columns, views,
// functions"). ir.Table/View/Function.Deprecated was parsed, snapshotted
// (for Table only — SnapView/SnapFunction didn't even have the field, a
// deeper gap than Column's), and used by the linter, but never referenced
// by the differ.
func TestDiffTableDeprecatedEmitsComment(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.legacy_orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public", Name: "legacy_orders",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	msg := "use orders instead"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "legacy_orders",
			Columns:    []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Deprecated: &msg,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, o := range ops {
		if strings.Contains(o.SQL(), "COMMENT ON TABLE") {
			found = true
			want := `IS '[DEPRECATED] use orders instead';`
			if !strings.Contains(o.SQL(), want) {
				t.Errorf("COMMENT ON TABLE text: got %q, want substring %q", o.SQL(), want)
			}
		}
	}
	if !found {
		t.Fatalf("expected a COMMENT ON TABLE op for DEPRECATED, got: %v", sqlList(ops))
	}
}

func TestDiffViewDeprecatedEmitsComment(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.legacy_summary", &snapshot.SnapObject{
		Kind: "view",
		View: &snapshot.SnapView{
			Schema: "public", Name: "legacy_summary",
			Query: "SELECT 1",
		},
	})
	msg := "use order_summary instead"
	desired := []pipeline.IRObject{
		&ir.View{
			Schema: "public", Name: "legacy_summary",
			Query:      "SELECT 1",
			Deprecated: &msg,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, o := range ops {
		if strings.Contains(o.SQL(), "COMMENT ON VIEW") {
			found = true
			want := `IS '[DEPRECATED] use order_summary instead';`
			if !strings.Contains(o.SQL(), want) {
				t.Errorf("COMMENT ON VIEW text: got %q, want substring %q", o.SQL(), want)
			}
		}
	}
	if !found {
		t.Fatalf("expected a COMMENT ON VIEW op for DEPRECATED, got: %v", sqlList(ops))
	}
}

func TestDiffFunctionDeprecatedEmitsComment(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.legacy_calc()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema: "public", Name: "legacy_calc",
			ReturnType: "void",
			Language:   "plpgsql",
			Volatility: "VOLATILE",
			BodyHash:   "hash1",
		},
	})
	msg := "use calc_v2 instead"
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema:     "public",
			Name:       "legacy_calc",
			ReturnType: ir.TypeRef{Name: "void"},
			BodyHash:   "hash1",
			Deprecated: &msg,
			Attrs: ir.FuncAttrs{
				Language:   "plpgsql",
				Volatility: "VOLATILE",
				Body:       "BEGIN END;",
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, o := range ops {
		if strings.Contains(o.SQL(), "COMMENT ON FUNCTION") {
			found = true
			want := `IS '[DEPRECATED] use calc_v2 instead';`
			if !strings.Contains(o.SQL(), want) {
				t.Errorf("COMMENT ON FUNCTION text: got %q, want substring %q", o.SQL(), want)
			}
		}
	}
	if !found {
		t.Fatalf("expected a COMMENT ON FUNCTION op for DEPRECATED, got: %v", sqlList(ops))
	}
}

func TestDiffColumnRenameKeepsIndex(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public", Name: "orders",
			Columns: []snapshot.SnapColumn{
				{Name: "id", Type: "bigint"},
				{Name: "metadata", Type: "jsonb"},
			},
			Indexes: []snapshot.SnapIndex{
				{Name: "orders_metadata_gin_idx", Method: "gin", Columns: "metadata"},
			},
		},
	})

	old := "metadata"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{Name: "extra_data", Type: ir.TypeRef{Name: "jsonb"}, RenamedFrom: &old},
			},
			Indexes: []*ir.Index{
				{Name: "orders_metadata_gin_idx", Method: "gin", Columns: []pipeline.IndexColumn{{Name: "extra_data"}}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		sql := o.SQL()
		if strings.Contains(sql, "DROP INDEX") || strings.Contains(sql, "CREATE INDEX") {
			t.Errorf("did not expect the index to be dropped/recreated after a plain column rename, got: %s", sql)
		}
	}
}

// TestDiffColumnRenameKeepsCheckConstraintUnquotedReference is the
// regression guard for a PRE-EXISTING bug an independent verification pass
// found (while checking a related EXCLUDE fix, but reproducible here too,
// unrelated to EXCLUDE specifically): replaceQuotedIdents originally matched
// only the quoted "old" form, but nodeToText/pg_query.Deparse — which
// renders a CHECK expression — leaves a plain lowercase identifier unquoted
// (confirmed live: `SELECT room > 0` deparses to `room > 0`, not
// `"room" > 0`). A CHECK whose expression is a bare comparison like
// `room > 0` (the overwhelmingly common shape) never got its renamed column
// translated at all.
func TestDiffColumnRenameKeepsCheckConstraintUnquotedReference(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.bookings", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "bookings",
			Columns: []snapshot.SnapColumn{{Name: "room", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "", Type: "CHECK", Expr: `CHECK (room > 0)`},
			},
		},
	})

	old := "room"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "bookings",
			Columns: []*ir.Column{
				{Name: "chamber", Type: ir.TypeRef{Name: "integer"}, RenamedFrom: &old},
			},
			Constraints: []*ir.Constraint{
				{Type: "CHECK", Expr: `CHECK (chamber > 0)`},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		sql := o.SQL()
		if strings.Contains(sql, "WARNING") {
			t.Errorf("did not expect a constraint WARNING after RENAMED FROM, got: %s", sql)
		}
		if strings.Contains(sql, "DROP CONSTRAINT") || strings.Contains(sql, "ADD") {
			t.Errorf("did not expect the CHECK constraint to be dropped/re-added after RENAMED FROM, got: %s", sql)
		}
	}
}

// TestDiffColumnRenameKeepsExcludeConstraint guards translateConstraintExpr's
// EXCLUDE case: an unnamed EXCLUDE constraint's structural-signature match
// (the offline path for unnamed constraints) must translate a renamed
// column's quoted element-list reference the same way CHECK's whole-string
// translation already does. The WHERE clause here deliberately references a
// column NOT being renamed — the WHERE-clause-specific case (an unquoted
// reference, which is what the real pipeline actually renders) is covered
// separately by TestDiffColumnRenameKeepsExcludeConstraintReferencedOnlyInWhere,
// since the two exercise different text — quoted (element list, built via
// quoteIdent) vs unquoted (WHERE/expression elements, built via
// nodeToText/pg_query.Deparse, which only quotes an identifier that actually
// needs it).
func TestDiffColumnRenameKeepsExcludeConstraint(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.bookings", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "bookings",
			Columns: []snapshot.SnapColumn{
				{Name: "room", Type: "integer"},
				{Name: "span", Type: "tsrange"},
			},
			Constraints: []snapshot.SnapConstraint{
				{Name: "", Type: "EXCLUDE",
					Expr: `EXCLUDE USING gist ("room" WITH =, "span" WITH &&) WHERE (room > 0)`},
			},
		},
	})

	old := "span"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "bookings",
			Columns: []*ir.Column{
				{Name: "room", Type: ir.TypeRef{Name: "integer"}},
				{Name: "during", Type: ir.TypeRef{Name: "tsrange"}, RenamedFrom: &old},
			},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE",
					Expr: `EXCLUDE USING gist ("room" WITH =, "during" WITH &&) WHERE (room > 0)`},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		sql := o.SQL()
		if strings.Contains(sql, "WARNING") {
			t.Errorf("did not expect a constraint WARNING after RENAMED FROM, got: %s", sql)
		}
		if strings.Contains(sql, "DROP CONSTRAINT") || strings.Contains(sql, "ADD") {
			t.Errorf("did not expect the EXCLUDE constraint to be dropped/re-added after RENAMED FROM, got: %s", sql)
		}
	}
}

// TestDiffColumnRenameKeepsExcludeConstraintReferencedOnlyInWhere is the
// regression guard for a bug an independent verification pass found: a
// renamed column referenced ONLY in an EXCLUDE's WHERE clause wasn't
// translated, because replaceQuotedIdents originally matched only the
// quoted "old" form — but nodeToText/pg_query.Deparse (which renders the
// WHERE clause) leaves a plain lowercase identifier unquoted (confirmed
// live: `SELECT room > 0` deparses to `room > 0`, not `"room" > 0`). The
// element list's own reference to "room" (elsewhere in this same
// constraint) IS quoted (built via quoteIdent) and would already have been
// translated correctly — this test's fixture avoids that column entirely
// (uses "during" for the element) so it isolates and proves the WHERE-only
// case specifically.
func TestDiffColumnRenameKeepsExcludeConstraintReferencedOnlyInWhere(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.bookings", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "bookings",
			Columns: []snapshot.SnapColumn{
				{Name: "room", Type: "integer"},
				{Name: "during", Type: "tsrange"},
			},
			Constraints: []snapshot.SnapConstraint{
				{Name: "", Type: "EXCLUDE",
					Expr: `EXCLUDE USING gist ("during" WITH &&) WHERE (room > 0)`},
			},
		},
	})

	old := "room"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "bookings",
			Columns: []*ir.Column{
				{Name: "chamber", Type: ir.TypeRef{Name: "integer"}, RenamedFrom: &old},
				{Name: "during", Type: ir.TypeRef{Name: "tsrange"}},
			},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE",
					Expr: `EXCLUDE USING gist ("during" WITH &&) WHERE (chamber > 0)`},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		sql := o.SQL()
		if strings.Contains(sql, "WARNING") {
			t.Errorf("did not expect a constraint WARNING after RENAMED FROM, got: %s", sql)
		}
		if strings.Contains(sql, "DROP CONSTRAINT") || strings.Contains(sql, "ADD") {
			t.Errorf("did not expect the EXCLUDE constraint to be dropped/re-added after RENAMED FROM, got: %s", sql)
		}
	}
}

// TestDiffColumnRenameDoesNotMangleStringLiteral is the regression guard for
// a bug an independent verification pass found in the bare-word substitution
// added above: a string literal is delimited by a single quote, a
// non-identifier character, so an unqualified \bold\b regex treats 'room'
// (the STRING VALUE) exactly like the bare identifier room. Renaming an
// unrelated column also named "room" elsewhere on the table must not mangle
// this CHECK's literal 'room' into 'chamber' — that would make an
// UNCHANGED constraint's snapshot-side key stop matching its (correctly
// unchanged) desired-side key, producing a spurious destructive
// drop+recreate for a constraint that never actually changed.
func TestDiffColumnRenameDoesNotMangleStringLiteral(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "t",
			Columns: []snapshot.SnapColumn{
				{Name: "status", Type: "text"},
				{Name: "room", Type: "integer"},
			},
			Constraints: []snapshot.SnapConstraint{
				{Name: "", Type: "CHECK", Expr: `CHECK (status <> 'room')`},
			},
		},
	})

	old := "room"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "t",
			Columns: []*ir.Column{
				{Name: "status", Type: ir.TypeRef{Name: "text"}},
				{Name: "chamber", Type: ir.TypeRef{Name: "integer"}, RenamedFrom: &old},
			},
			Constraints: []*ir.Constraint{
				// Genuinely unchanged: the literal 'room' is a string value,
				// unrelated to the renamed "room" column.
				{Type: "CHECK", Expr: `CHECK (status <> 'room')`},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		sql := o.SQL()
		if strings.Contains(sql, "WARNING") {
			t.Errorf("did not expect a constraint WARNING — the string literal 'room' must not be mistaken for the renamed column, got: %s", sql)
		}
		if strings.Contains(sql, "DROP CONSTRAINT") || (strings.Contains(sql, "ADD") && strings.Contains(sql, "CHECK")) {
			t.Errorf("did not expect the unchanged CHECK constraint to be dropped/re-added, got: %s", sql)
		}
	}
}

// TestDiffColumnDropSuppressesCascadedConstraint verifies that when a column
// is dropped (no RENAMED FROM), an unnamed constraint on that column doesn't
// surface as a manual-drop warning — DROP COLUMN cascades to it in PG.
func TestDiffColumnDropSuppressesCascadedConstraint(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("iam.groups", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "iam",
			Name:   "groups",
			Columns: []snapshot.SnapColumn{
				{Name: "id", Type: "bigint"},
				{Name: "locality_id", Type: "bigint"},
			},
			Constraints: []snapshot.SnapConstraint{
				{Name: "", Type: "FOREIGN KEY",
					Expr: `FOREIGN KEY ("locality_id") REFERENCES "iam"."localities" ("id")`},
			},
			Indexes: []snapshot.SnapIndex{
				{Name: "groups_locality_id_idx", Method: "btree", Columns: "locality_id"},
			},
		},
	})

	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "iam",
			Name:   "groups",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var sawDropCol bool
	for _, o := range ops {
		sql := o.SQL()
		if strings.Contains(sql, "DROP COLUMN") && strings.Contains(sql, `"locality_id"`) {
			sawDropCol = true
		}
		if strings.Contains(sql, "WARNING") {
			t.Errorf("expected no WARNING for cascaded constraint, got: %s", sql)
		}
		if strings.Contains(sql, "DROP INDEX") && strings.Contains(sql, "groups_locality_id_idx") {
			t.Errorf("expected no DROP INDEX for index whose only column is dropped, got: %s", sql)
		}
	}
	if !sawDropCol {
		t.Fatalf("expected DROP COLUMN locality_id, got: %v", sqlList(ops))
	}
}

// ── SQL string-literal-aware rename translation ─────────────────────────────────

// TestSplitSQLStringLiteralsBasic proves a plain literal is correctly
// isolated from the surrounding code text.
func TestSplitSQLStringLiteralsBasic(t *testing.T) {
	segs := splitSQLStringLiterals(`status <> 'room'`)
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2: %+v", len(segs), segs)
	}
	if segs[0].isLiteral || segs[0].text != "status <> " {
		t.Errorf("segs[0] = %+v, want {\"status <> \", false}", segs[0])
	}
	if !segs[1].isLiteral || segs[1].text != "'room'" {
		t.Errorf("segs[1] = %+v, want {\"'room'\", true}", segs[1])
	}
}

// TestSplitSQLStringLiteralsEscapedQuote proves SQL's doubled-quote escape
// (” inside a literal means a literal single quote, not end-of-string) is
// handled — confirmed this is the real PostgreSQL rule, and that
// pg_query.Deparse's own output uses it (e.g. rendering "it's" as 'it”s').
// A naive split on every apostrophe would end the literal early here.
func TestSplitSQLStringLiteralsEscapedQuote(t *testing.T) {
	segs := splitSQLStringLiterals(`name = 'it''s room'`)
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2: %+v", len(segs), segs)
	}
	if !segs[1].isLiteral || segs[1].text != `'it''s room'` {
		t.Errorf("segs[1] = %+v, want the whole escaped literal as one segment", segs[1])
	}
}

// TestSplitSQLStringLiteralsMultipleLiterals proves code text between two
// literals is correctly re-isolated as its own non-literal segment.
func TestSplitSQLStringLiteralsMultipleLiterals(t *testing.T) {
	segs := splitSQLStringLiterals(`a = 'x' AND b = 'y'`)
	var gotLiterals, gotCode []string
	for _, seg := range segs {
		if seg.isLiteral {
			gotLiterals = append(gotLiterals, seg.text)
		} else {
			gotCode = append(gotCode, seg.text)
		}
	}
	if len(gotLiterals) != 2 || gotLiterals[0] != "'x'" || gotLiterals[1] != "'y'" {
		t.Errorf("literals: got %v, want ['x'] ['y']", gotLiterals)
	}
	if len(gotCode) != 2 || gotCode[0] != "a = " || gotCode[1] != " AND b = " {
		t.Errorf("code segments: got %v", gotCode)
	}
}

// TestSplitSQLStringLiteralsNoLiteral proves text with no literal at all
// round-trips as a single non-literal segment.
func TestSplitSQLStringLiteralsNoLiteral(t *testing.T) {
	segs := splitSQLStringLiterals(`room > 0`)
	if len(segs) != 1 || segs[0].isLiteral || segs[0].text != "room > 0" {
		t.Errorf("got %+v, want a single non-literal segment", segs)
	}
}

// ── Unnamed-constraint / live-generated-name matching ─────────────────────────
//
// Regression guard for a bug found reviewing the diff-coverage-push session:
// PostgreSQL's pg_constraint.conname is NEVER empty (it auto-generates a name
// like "t_pkey" even when the user wrote none), so a live-introspected
// unnamed inline PK/UNIQUE/FK always keyed as "n:<real name>", while the
// desired IR (still unnamed in source) keyed as "s:<type>|<expr>" — the two
// never matched, so verify/plan --live produced a self-inconsistent
// DROP+ADD pair on every single run for any inline unnamed PK/UNIQUE/FK.
// pgAutoConstraintName reconstructs PostgreSQL's actual auto-naming
// algorithm (ChooseConstraintName/makeObjectName, ported from PG's own C
// source) so an unnamed desired constraint can be matched against the real
// generated name directly.

func TestMakeObjectNamePrimaryKey(t *testing.T) {
	if got := makeObjectName("orders", "", "pkey"); got != "orders_pkey" {
		t.Errorf("makeObjectName(orders, \"\", pkey) = %q, want orders_pkey", got)
	}
}

func TestMakeObjectNameUnique(t *testing.T) {
	if got := makeObjectName("orders", "user_id", "key"); got != "orders_user_id_key" {
		t.Errorf("makeObjectName(orders, user_id, key) = %q, want orders_user_id_key", got)
	}
}

// TestMakeObjectNameTruncation guards PG's actual truncation rule: when
// name1_name2_label would exceed NAMEDATALEN-1 (63) bytes, the longer of
// name1/name2 is shortened first, one byte at a time, alternating — the
// label is never touched. Verified against PostgreSQL's own C source
// (src/backend/commands/indexcmds.c makeObjectName), not from memory.
func TestMakeObjectNameTruncation(t *testing.T) {
	name1 := strings.Repeat("a", 40)
	name2 := strings.Repeat("b", 40)
	got := makeObjectName(name1, name2, "key")
	if len(got) > 63 {
		t.Fatalf("generated name exceeds NAMEDATALEN-1 (63 bytes): %d bytes: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "_key") {
		t.Errorf("label must never be truncated, got: %q", got)
	}
	// availchars = 63 - 1(sep) - 3(key) - 1(sep) = 58; split evenly since
	// both names start equal length: 29 + 29.
	wantName1 := strings.Repeat("a", 29)
	wantName2 := strings.Repeat("b", 29)
	want := wantName1 + "_" + wantName2 + "_key"
	if got != want {
		t.Errorf("makeObjectName truncation = %q, want %q", got, want)
	}
}

func TestMakeObjectNameMultiByteSafeTruncation(t *testing.T) {
	// A multi-byte (3-byte UTF-8) rune landing exactly on the truncation
	// boundary must not be split — mbClipLen must round the cut down to the
	// nearest complete rune, mirroring PG's pg_mbcliplen.
	name1 := strings.Repeat("a", 55) + "日本語" // 55 ASCII + 3 three-byte runes = 64 bytes
	got := makeObjectName(name1, "", "pkey")
	if !utf8.ValidString(got) {
		t.Fatalf("truncation produced invalid UTF-8: %q", got)
	}
}

func TestPgAutoConstraintNameCollisionSuffix(t *testing.T) {
	existing := map[string]bool{"orders_pkey": true}
	got := pgAutoConstraintName("orders", nil, "pkey", existing)
	if got != "orders_pkey1" {
		t.Errorf("expected collision suffix orders_pkey1, got: %q", got)
	}
	if !existing["orders_pkey1"] {
		t.Error("expected pgAutoConstraintName to record the chosen name in existingNames")
	}
}

// TestDiffUnnamedPrimaryKeyMatchesLiveGeneratedName is the core regression
// guard: an inline `id BIGINT PRIMARY KEY` (unnamed in source) must produce
// zero ops against a snapshot shaped like live introspection — real
// generated name "orders_pkey", never empty.
func TestDiffUnnamedPrimaryKeyMatchesLiveGeneratedName(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint", NotNull: true}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "orders_pkey", Type: "PRIMARY KEY", Expr: `PRIMARY KEY ("id")`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}, NotNull: true}},
			Constraints: []*ir.Constraint{
				{Type: "PRIMARY KEY", Columns: []string{"id"}, Expr: `PRIMARY KEY ("id")`},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unnamed PK must match live-generated orders_pkey), got: %v", sqlList(ops))
	}
}

func TestDiffUnnamedUniqueMatchesLiveGeneratedName(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "external_id", Type: "text"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "orders_external_id_key", Type: "UNIQUE", Expr: `UNIQUE ("external_id")`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns: []*ir.Column{{Name: "external_id", Type: ir.TypeRef{Name: "text"}}},
			Constraints: []*ir.Constraint{
				{Type: "UNIQUE", Columns: []string{"external_id"}, Expr: `UNIQUE ("external_id")`},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unnamed UNIQUE must match live-generated orders_external_id_key), got: %v", sqlList(ops))
	}
}

func TestDiffUnnamedForeignKeyMatchesLiveGeneratedName(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "user_id", Type: "bigint"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "orders_user_id_fkey", Type: "FOREIGN KEY",
					Expr: `FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id")`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns: []*ir.Column{{Name: "user_id", Type: ir.TypeRef{Name: "bigint"}}},
			Constraints: []*ir.Constraint{
				{Type: "FOREIGN KEY", Columns: []string{"user_id"},
					Expr: `FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id")`},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unnamed FK must match live-generated orders_user_id_fkey), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedCheckMatchesLiveGeneratedName proves an unnamed CHECK whose
// expression references exactly one distinct column matches PG's real
// generated name ("orders_amount_check" — heap.c's single-Var name2 rule).
func TestDiffUnnamedCheckMatchesLiveGeneratedName(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "amount", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "orders_amount_check", Type: "CHECK", Expr: `CHECK ((amount > 0))`},
			},
		},
	})
	col := "amount"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns: []*ir.Column{{Name: "amount", Type: ir.TypeRef{Name: "integer"}}},
			Constraints: []*ir.Constraint{
				{Type: "CHECK", CheckColumn: &col, Expr: `CHECK (amount > 0)`},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unnamed CHECK must match live-generated orders_amount_check), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedCheckMultiColumnMatchesLiveGeneratedName covers the other
// branch of heap.c's rule: when more than one distinct column is
// referenced, name2 is omitted entirely ("orders_check", not
// "orders_a_b_check").
func TestDiffUnnamedCheckMultiColumnMatchesLiveGeneratedName(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}, {Name: "b", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "orders_check", Type: "CHECK", Expr: `CHECK (((a > 0) AND (b > 0)))`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "integer"}}, {Name: "b", Type: ir.TypeRef{Name: "integer"}}},
			Constraints: []*ir.Constraint{
				{Type: "CHECK", Expr: `CHECK (a > 0 AND b > 0)`}, // CheckColumn nil: 2 distinct columns
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unnamed multi-column CHECK must match live-generated orders_check), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedCheckRemovalStillDetected proves the fix doesn't paper over
// a genuinely removed CHECK constraint.
func TestDiffUnnamedCheckRemovalStillDetected(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "amount", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "orders_amount_check", Type: "CHECK", Expr: `CHECK ((amount > 0))`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns: []*ir.Column{{Name: "amount", Type: ir.TypeRef{Name: "integer"}}},
			// No CHECK constraint at all — genuinely removed.
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "DROP CONSTRAINT") || !containsSQL(ops, `orders_amount_check`) {
		t.Errorf("expected DROP CONSTRAINT orders_amount_check for the removed CHECK, got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedCheckDifferentColumnDetectedAsChange proves a single-column
// unnamed CHECK is NOT subject to the same identity-only blind spot as an
// unnamed PRIMARY KEY (TestDiffUnnamedPrimaryKeyRemovalStillDetected's doc
// comment): unlike a PK's name, which never encodes columns at all, a
// single-column CHECK's predicted name DOES encode its column — so moving
// the check to a different column produces a genuinely different predicted
// name, correctly surfacing as DROP old + ADD new rather than a silent
// no-op.
func TestDiffUnnamedCheckDifferentColumnDetectedAsChange(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}, {Name: "b", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "orders_a_check", Type: "CHECK", Expr: `CHECK ((a > 0))`},
			},
		},
	})
	colB := "b"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "integer"}}, {Name: "b", Type: ir.TypeRef{Name: "integer"}}},
			Constraints: []*ir.Constraint{
				{Type: "CHECK", CheckColumn: &colB, Expr: `CHECK (b > 0)`},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "DROP CONSTRAINT") || !containsSQL(ops, "orders_a_check") {
		t.Errorf("expected DROP CONSTRAINT orders_a_check, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "ADD") || !containsSQL(ops, "b > 0") {
		t.Errorf("expected ADD for the new b-based CHECK, got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedPrimaryKeyRemovalStillDetected proves the fix doesn't paper
// over a genuinely removed PK: when desired drops the PRIMARY KEY
// constraint entirely, a live-style "orders_pkey" in the snapshot must still
// surface as a real DROP CONSTRAINT.
//
// Note a known, pre-existing limitation this fix inherits rather than
// introduces: matching here is identity-only (by name, or — for an unnamed
// constraint — by predicted name), never full-definition equality. This
// already applied to any hand-named constraint before this fix (redefining
// a CHECK's expression while keeping its explicit name goes undetected the
// same way); it now also applies to an unnamed PK specifically, since a
// PRIMARY KEY's PG-generated name never encodes its columns at all (a table
// has only one PK), so an unnamed PK moved to a different column on both
// sides is not something the tool can distinguish from unchanged. Genuine
// removal (this test) or a change of TYPE (a different test could add
// UNIQUE where a PK used to be) are still caught correctly, since those
// change which map entry — if any — the snap constraint matches.
func TestDiffUnnamedPrimaryKeyRemovalStillDetected(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "orders_pkey", Type: "PRIMARY KEY", Expr: `PRIMARY KEY ("id")`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			// No PRIMARY KEY constraint at all — genuinely removed.
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "DROP CONSTRAINT") || !containsSQL(ops, `orders_pkey`) {
		t.Errorf("expected DROP CONSTRAINT orders_pkey for the removed PK, got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedConstraintSchemaWideCollision proves the collision universe
// is genuinely schema-wide, not just same-table: a second table in the same
// schema already using the name "widgets_pkey" would force PostgreSQL's own
// algorithm to fall back to "orders_pkey1" ONLY if the two tables' own
// generated names actually collided — this test instead checks the more
// realistic case (two DIFFERENT unnamed PKs on two different tables produce
// two DIFFERENT names, since name1 embeds the table name) to confirm
// schemaConstraintNames doesn't cause false cross-table collisions.
func TestDiffUnnamedConstraintNoFalseCrossTableCollision(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.widgets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "widgets",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "widgets_pkey", Type: "PRIMARY KEY", Expr: `PRIMARY KEY ("id")`},
			},
		},
	})
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "orders_pkey", Type: "PRIMARY KEY", Expr: `PRIMARY KEY ("id")`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "widgets",
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}, NotNull: true}},
			Constraints: []*ir.Constraint{{Type: "PRIMARY KEY", Columns: []string{"id"}, Expr: `PRIMARY KEY ("id")`}},
		},
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}, NotNull: true}},
			Constraints: []*ir.Constraint{{Type: "PRIMARY KEY", Columns: []string{"id"}, Expr: `PRIMARY KEY ("id")`}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for both tables' unnamed PKs matching their own distinct generated names, got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedPrimaryKeyRelationNameCollision is the regression guard for
// a gap an independent verification pass found live: PostgreSQL's
// ChooseRelationName (used for PRIMARY KEY/UNIQUE, since both are
// index-backed) checks for a naming collision against ANY relation in the
// schema via pg_class — not just other constraint names. A plain table
// literally named "bar_pkey" forces PG to fall back to "bar_pkey1" for an
// unnamed PK on table "bar", even though no OTHER CONSTRAINT is named
// "bar_pkey". schemaConstraintNames alone (constraint names only) would
// miss this and predict "bar_pkey", silently reintroducing the exact
// self-inconsistent DROP+ADD bug this feature exists to eliminate.
// schemaRelationNames closes this by also collecting table/view/sequence/
// index names in the schema.
func TestDiffUnnamedPrimaryKeyRelationNameCollision(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	// A plain, unrelated table that happens to be named exactly what
	// table "bar"'s auto-generated PK name would otherwise be.
	_ = snap.SetObject("public.bar_pkey", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "bar_pkey",
			Columns: []snapshot.SnapColumn{{Name: "x", Type: "bigint"}},
		},
	})
	_ = snap.SetObject("public.bar", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "bar",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Constraints: []snapshot.SnapConstraint{
				// The name PG actually assigns once "bar_pkey" the relation
				// is taken: it falls back to the next available suffix.
				{Name: "bar_pkey1", Type: "PRIMARY KEY", Expr: `PRIMARY KEY ("id")`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "bar_pkey",
			Columns: []*ir.Column{{Name: "x", Type: ir.TypeRef{Name: "bigint"}}},
		},
		&ir.Table{
			Schema: "public", Name: "bar",
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}, NotNull: true}},
			Constraints: []*ir.Constraint{{Type: "PRIMARY KEY", Columns: []string{"id"}, Expr: `PRIMARY KEY ("id")`}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops: unnamed PK on \"bar\" must predict \"bar_pkey1\" (relation-name collision with table \"bar_pkey\"), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedCheckNoRelationNameCollision proves CHECK's collision
// universe stays constraint-names-only, unlike PRIMARY KEY/UNIQUE: CHECK
// isn't index-backed, so its real default-naming path (ChooseConstraintName,
// heap.c) never scans pg_class — a plain table named "orders_amount_check"
// must NOT force a predicted CHECK name to fall back to a "1" suffix.
func TestDiffUnnamedCheckNoRelationNameCollision(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders_amount_check", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders_amount_check",
			Columns: []snapshot.SnapColumn{{Name: "x", Type: "bigint"}},
		},
	})
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "amount", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "orders_amount_check", Type: "CHECK", Expr: `CHECK ((amount > 0))`},
			},
		},
	})
	col := "amount"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "orders_amount_check",
			Columns: []*ir.Column{{Name: "x", Type: ir.TypeRef{Name: "bigint"}}},
		},
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns:     []*ir.Column{{Name: "amount", Type: ir.TypeRef{Name: "integer"}}},
			Constraints: []*ir.Constraint{{Type: "CHECK", CheckColumn: &col, Expr: `CHECK (amount > 0)`}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops: CHECK naming must not collide with an unrelated relation name, got: %v", sqlList(ops))
	}
}

// ── Unnamed EXCLUDE / live-generated-name matching ──────────────────────────────

// TestDiffUnnamedExcludeMatchesLiveGeneratedName proves an unnamed EXCLUDE
// whose every element is a plain column matches PG's real generated name
// (confirmed live: "bookings_room_during_excl" for
// EXCLUDE USING gist (room WITH =, during WITH &&) on table "bookings" —
// heap.c/indexcmds.c's name2-is-every-colname-joined-by-underscore rule,
// same as UNIQUE).
func TestDiffUnnamedExcludeMatchesLiveGeneratedName(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.bookings", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "bookings",
			Columns: []snapshot.SnapColumn{
				{Name: "room", Type: "integer"},
				{Name: "during", Type: "tsrange"},
			},
			Constraints: []snapshot.SnapConstraint{
				{Name: "bookings_room_during_excl", Type: "EXCLUDE",
					Expr: `EXCLUDE USING gist ("room" WITH =, "during" WITH &&)`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "bookings",
			Columns: []*ir.Column{
				{Name: "room", Type: ir.TypeRef{Name: "integer"}},
				{Name: "during", Type: ir.TypeRef{Name: "tsrange"}},
			},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE", Columns: []string{"room", "during"},
					Exclude: &ir.ExcludeSpec{
						AccessMethod: "gist",
						Elements: []ir.ExcludeElement{
							{Column: "room", Operator: "="},
							{Column: "during", Operator: "&&"},
						},
					},
					Expr: `EXCLUDE USING gist ("room" WITH =, "during" WITH &&)`,
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unnamed EXCLUDE must match live-generated bookings_room_during_excl), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedExcludeSingleColumnMatchesLiveGeneratedName covers the
// single-element case (confirmed live: "singles_a_excl" for
// EXCLUDE (a WITH =) on table "singles").
func TestDiffUnnamedExcludeSingleColumnMatchesLiveGeneratedName(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.singles", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "singles",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "singles_a_excl", Type: "EXCLUDE", Expr: `EXCLUDE USING btree ("a" WITH =)`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "singles",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "integer"}}},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE", Columns: []string{"a"},
					Exclude: &ir.ExcludeSpec{
						AccessMethod: "btree",
						Elements:     []ir.ExcludeElement{{Column: "a", Operator: "="}},
					},
					Expr: `EXCLUDE USING btree ("a" WITH =)`,
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unnamed EXCLUDE must match live-generated singles_a_excl), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedExcludeRemovalStillDetected proves the fix doesn't paper
// over a genuinely removed EXCLUDE constraint.
func TestDiffUnnamedExcludeRemovalStillDetected(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.singles", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "singles",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "singles_a_excl", Type: "EXCLUDE", Expr: `EXCLUDE USING btree ("a" WITH =)`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "singles",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "integer"}}},
			// No EXCLUDE constraint at all — genuinely removed.
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "DROP CONSTRAINT") || !containsSQL(ops, "singles_a_excl") {
		t.Errorf("expected DROP CONSTRAINT singles_a_excl for the removed EXCLUDE, got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedExcludeExpressionElementMatchesLiveGeneratedName proves an
// EXCLUDE with a bare, uncast operator-expression element (not a plain
// column, function call, or cast) is still correctly matched against
// PostgreSQL's real generated name. PredictedName is empty at the IR layer
// for this shape (a syntax-only pass can't derive anything from "a + b"),
// but PostgreSQL's own ChooseIndexColumnNames (indexcmds.c) falls back to
// the literal string "expr" whenever an element's indexcolname is unset —
// confirmed live against PG 17: EXCLUDE USING btree ((a + b) WITH =) on
// table "t" really does generate "t_expr_excl". predictName's "excl" case
// reconstructs that same literal fallback, so this is no longer an
// unpredictable shape.
func TestDiffUnnamedExcludeExpressionElementMatchesLiveGeneratedName(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}, {Name: "b", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "t_expr_excl", Type: "EXCLUDE", Expr: `EXCLUDE USING btree ((a + b) WITH =)`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "t",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "integer"}}, {Name: "b", Type: ir.TypeRef{Name: "integer"}}},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE",
					Exclude: &ir.ExcludeSpec{
						AccessMethod: "btree",
						// PredictedName intentionally left unset — "a + b" is
						// a bare, uncast operator expression, matching what
						// buildConstraint would actually produce for it.
						Elements: []ir.ExcludeElement{{Expr: "a + b", Operator: "="}},
					},
					Expr: `EXCLUDE USING btree ((a + b) WITH =)`,
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unnamed EXCLUDE must match live-generated t_expr_excl), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedExcludeExpressionElementsDeduped proves two bare-expression
// elements on the same unnamed EXCLUDE dedup exactly like two same-named
// function-call elements do (TestDiffUnnamedExcludeSameFuncNameElementsDeduped)
// — confirmed live: EXCLUDE USING gist ((a + b) WITH =, (c * d) WITH =) on
// table "t2" generates "t2_expr_expr1_excl".
func TestDiffUnnamedExcludeExpressionElementsDeduped(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t2", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "t2",
			Columns: []snapshot.SnapColumn{
				{Name: "a", Type: "integer"}, {Name: "b", Type: "integer"},
				{Name: "c", Type: "integer"}, {Name: "d", Type: "integer"},
			},
			Constraints: []snapshot.SnapConstraint{
				{Name: "t2_expr_expr1_excl", Type: "EXCLUDE", Expr: `EXCLUDE USING gist ((a + b) WITH =, (c * d) WITH =)`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "t2",
			Columns: []*ir.Column{
				{Name: "a", Type: ir.TypeRef{Name: "integer"}}, {Name: "b", Type: ir.TypeRef{Name: "integer"}},
				{Name: "c", Type: ir.TypeRef{Name: "integer"}}, {Name: "d", Type: ir.TypeRef{Name: "integer"}},
			},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE",
					Exclude: &ir.ExcludeSpec{
						AccessMethod: "gist",
						Elements: []ir.ExcludeElement{
							{Expr: "a + b", Operator: "="},
							{Expr: "c * d", Operator: "="},
						},
					},
					Expr: `EXCLUDE USING gist ((a + b) WITH =, (c * d) WITH =)`,
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (must predict deduped t2_expr_expr1_excl), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedExcludeFuncCallElementMatchesLiveGeneratedName proves an
// unnamed EXCLUDE with a bare, top-level function-call element predicts
// PostgreSQL's real generated name (confirmed live: "t_lower_excl" for
// EXCLUDE ((lower(a)) WITH =) on table "t") — this is the case an earlier
// pass of this feature incorrectly excluded as "requires OID resolution,
// infeasible offline"; the exclusion's premise didn't hold once verified
// live: PostgreSQL's real algorithm only ever uses the bare function name
// for such an element, never descending into its arguments, so no OID
// lookup is actually needed.
func TestDiffUnnamedExcludeFuncCallElementMatchesLiveGeneratedName(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "text"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "t_lower_excl", Type: "EXCLUDE", Expr: `EXCLUDE USING btree ((lower(a)) WITH =)`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "t",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "text"}}},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE",
					Exclude: &ir.ExcludeSpec{
						AccessMethod: "btree",
						Elements:     []ir.ExcludeElement{{Expr: "lower(a)", PredictedName: "lower", Operator: "="}},
					},
					Expr: `EXCLUDE USING btree ((lower(a)) WITH =)`,
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unnamed function-call EXCLUDE must match live-generated t_lower_excl), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedExcludeSameFuncNameElementsDeduped proves two elements
// that independently derive the SAME function name (different columns,
// same function) get PostgreSQL's own per-element disambiguating suffix —
// confirmed live: EXCLUDE ((lower(a)) WITH =, (lower(b)) WITH =) on table
// "t" generates "t_lower_lower1_excl", not "t_lower_lower_excl".
func TestDiffUnnamedExcludeSameFuncNameElementsDeduped(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "text"}, {Name: "b", Type: "text"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "t_lower_lower1_excl", Type: "EXCLUDE", Expr: `EXCLUDE USING btree ((lower(a)) WITH =, (lower(b)) WITH =)`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "t",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "text"}}, {Name: "b", Type: ir.TypeRef{Name: "text"}}},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE",
					Exclude: &ir.ExcludeSpec{
						AccessMethod: "btree",
						Elements: []ir.ExcludeElement{
							{Expr: "lower(a)", PredictedName: "lower", Operator: "="},
							{Expr: "lower(b)", PredictedName: "lower", Operator: "="},
						},
					},
					Expr: `EXCLUDE USING btree ((lower(a)) WITH =, (lower(b)) WITH =)`,
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (must predict deduped t_lower_lower1_excl), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedExcludeMixedColumnAndFuncCallElements proves a mix of a
// plain column element and a function-call element predicts correctly
// (confirmed live: "t_a_lower_excl" for EXCLUDE (a WITH =, (lower(b)) WITH =)).
func TestDiffUnnamedExcludeMixedColumnAndFuncCallElements(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}, {Name: "b", Type: "text"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "t_a_lower_excl", Type: "EXCLUDE", Expr: `EXCLUDE USING btree ("a" WITH =, (lower(b)) WITH =)`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "t",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "integer"}}, {Name: "b", Type: ir.TypeRef{Name: "text"}}},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE", Columns: []string{"a"},
					Exclude: &ir.ExcludeSpec{
						AccessMethod: "btree",
						Elements: []ir.ExcludeElement{
							{Column: "a", Operator: "="},
							{Expr: "lower(b)", PredictedName: "lower", Operator: "="},
						},
					},
					Expr: `EXCLUDE USING btree ("a" WITH =, (lower(b)) WITH =)`,
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (must predict mixed-element t_a_lower_excl), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedExcludeCastElementMatchesLiveGeneratedName proves a cast
// wrapping a plain column predicts PostgreSQL's real generated name
// end-to-end through the diff layer — confirmed live: "t_a_excl" for
// EXCLUDE ((a::text) WITH =) on table "t" (the column's own name wins over
// the cast's target type, since a ColumnRef is a "strong" name).
func TestDiffUnnamedExcludeCastElementMatchesLiveGeneratedName(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "t_a_excl", Type: "EXCLUDE", Expr: `EXCLUDE USING btree ((a::text) WITH =)`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "t",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "integer"}}},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE",
					Exclude: &ir.ExcludeSpec{
						AccessMethod: "btree",
						Elements:     []ir.ExcludeElement{{Expr: "a::text", PredictedName: "a", Operator: "="}},
					},
					Expr: `EXCLUDE USING btree ((a::text) WITH =)`,
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unnamed cast-over-column EXCLUDE must match live-generated t_a_excl), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedExcludeCastOverOperatorMatchesLiveGeneratedName proves a
// cast wrapping a bare operator expression predicts the cast's OWN target
// type name — confirmed live: "t_text_excl" for
// EXCLUDE (((a + b)::text) WITH =) on table "t".
func TestDiffUnnamedExcludeCastOverOperatorMatchesLiveGeneratedName(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}, {Name: "b", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "t_text_excl", Type: "EXCLUDE", Expr: `EXCLUDE USING btree (((a + b)::text) WITH =)`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "t",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "integer"}}, {Name: "b", Type: ir.TypeRef{Name: "integer"}}},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE",
					Exclude: &ir.ExcludeSpec{
						AccessMethod: "btree",
						Elements:     []ir.ExcludeElement{{Expr: "(a + b)::text", PredictedName: "text", Operator: "="}},
					},
					Expr: `EXCLUDE USING btree (((a + b)::text) WITH =)`,
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unnamed cast-over-operator EXCLUDE must match live-generated t_text_excl), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedExcludeCoalesceElementMatchesLiveGeneratedName proves a
// COALESCE element predicts PostgreSQL's real generated name end-to-end
// through the diff layer — confirmed live: "t_coalesce_excl" for
// EXCLUDE ((coalesce(a,0)) WITH =) on table "t".
func TestDiffUnnamedExcludeCoalesceElementMatchesLiveGeneratedName(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "t_coalesce_excl", Type: "EXCLUDE", Expr: `EXCLUDE USING btree ((coalesce(a, 0)) WITH =)`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "t",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "integer"}}},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE",
					Exclude: &ir.ExcludeSpec{
						AccessMethod: "btree",
						Elements:     []ir.ExcludeElement{{Expr: "coalesce(a, 0)", PredictedName: "coalesce", Operator: "="}},
					},
					Expr: `EXCLUDE USING btree ((coalesce(a, 0)) WITH =)`,
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (unnamed coalesce EXCLUDE must match live-generated t_coalesce_excl), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedExcludeRelationNameCollision is the regression guard for
// EXCLUDE's schema-wide relation-name collision scope — confirmed live: a
// plain table literally named "t2_a_excl" forces PostgreSQL to fall back to
// "t2_a_excl1" for an unnamed EXCLUDE on table "t2", even though no OTHER
// constraint is named "t2_a_excl". Mirrors
// TestDiffUnnamedPrimaryKeyRelationNameCollision's PK case exactly.
func TestDiffUnnamedExcludeRelationNameCollision(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t2_a_excl", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t2_a_excl",
			Columns: []snapshot.SnapColumn{{Name: "x", Type: "bigint"}},
		},
	})
	_ = snap.SetObject("public.t2", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t2",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				// The name PG actually assigns once "t2_a_excl" the relation
				// is taken: it falls back to the next available suffix.
				{Name: "t2_a_excl1", Type: "EXCLUDE", Expr: `EXCLUDE USING btree ("a" WITH =)`},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "t2_a_excl",
			Columns: []*ir.Column{{Name: "x", Type: ir.TypeRef{Name: "bigint"}}},
		},
		&ir.Table{
			Schema: "public", Name: "t2",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "integer"}}},
			Constraints: []*ir.Constraint{
				{Type: "EXCLUDE", Columns: []string{"a"},
					Exclude: &ir.ExcludeSpec{
						AccessMethod: "btree",
						Elements:     []ir.ExcludeElement{{Column: "a", Operator: "="}},
					},
					Expr: `EXCLUDE USING btree ("a" WITH =)`,
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops: unnamed EXCLUDE on \"t2\" must predict \"t2_a_excl1\" (relation-name collision with table \"t2_a_excl\"), got: %v", sqlList(ops))
	}
}

// TestDiffUnnamedExcludeNoFalseCrossTableCollision proves two different
// tables' own unnamed EXCLUDE constraints predict two DIFFERENT names
// (name1 embeds the table name), so schemaConstraintNames/
// schemaRelationNames don't cause a false cross-table collision.
func TestDiffUnnamedExcludeNoFalseCrossTableCollision(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.widgets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "widgets",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "widgets_a_excl", Type: "EXCLUDE", Expr: `EXCLUDE USING btree ("a" WITH =)`},
			},
		},
	})
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				{Name: "orders_a_excl", Type: "EXCLUDE", Expr: `EXCLUDE USING btree ("a" WITH =)`},
			},
		},
	})
	excl := func() *ir.ExcludeSpec {
		return &ir.ExcludeSpec{AccessMethod: "btree", Elements: []ir.ExcludeElement{{Column: "a", Operator: "="}}}
	}
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public", Name: "widgets",
			Columns:     []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "integer"}}},
			Constraints: []*ir.Constraint{{Type: "EXCLUDE", Columns: []string{"a"}, Exclude: excl(), Expr: `EXCLUDE USING btree ("a" WITH =)`}},
		},
		&ir.Table{
			Schema: "public", Name: "orders",
			Columns:     []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "integer"}}},
			Constraints: []*ir.Constraint{{Type: "EXCLUDE", Columns: []string{"a"}, Exclude: excl(), Expr: `EXCLUDE USING btree ("a" WITH =)`}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for both tables' unnamed EXCLUDEs matching their own distinct generated names, got: %v", sqlList(ops))
	}
}

// ── Virtual type JSONB resolution ─────────────────────────────────────────────

// makeVtype is a helper to build a VirtualType with a simple type-ref body.
func makeVtype(schema, name string, body ir.VtypeBody) *ir.VirtualType {
	return &ir.VirtualType{Schema: schema, Name: name, Body: body}
}

func TestVirtualTypeNoSQL(t *testing.T) {
	// VIRTUAL TYPE declarations generate no SQL whatsoever.
	d := New()
	desired := []pipeline.IRObject{
		makeVtype("public", "user_state", ir.VtypeTypeRef{Name: "text"}),
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected 0 ops for VIRTUAL TYPE, got %d: %v", len(ops), sqlList(ops))
	}
}

func TestVirtualTypeNoSQLOnChange(t *testing.T) {
	// Changes to VIRTUAL TYPE also produce no SQL.
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.user_state", &snapshot.SnapObject{
		Kind: "virtual_type",
		VirtualType: &snapshot.SnapVirtualType{
			Schema: "public",
			Name:   "user_state",
			Body:   snapshot.SnapVtypeBody{Kind: "type_ref", Name: "text"},
		},
	})
	desired := []pipeline.IRObject{
		makeVtype("public", "user_state", ir.VtypeComposite{
			Fields: []ir.VtypeField{{Name: "val", Type: ir.VtypeTypeRef{Name: "integer"}}},
		}),
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected 0 ops for VIRTUAL TYPE change, got %d", len(ops))
	}
}

// ── SERIAL/BIGSERIAL/SMALLSERIAL ────────────────────────────────────────────

func serialPtr(s string) *string { return &s }

// TestCreateTableSerialEmitsLiteralKeyword proves createTable emits the
// literal SERIAL keyword (letting PostgreSQL's own macro-expansion create
// the sequence/default/ownership/NOT NULL) rather than the resolved
// underlying type with a hand-reconstructed NOT NULL/DEFAULT.
func TestCreateTableSerialEmitsLiteralKeyword(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "widgets",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "integer"}, Serial: serialPtr("SERIAL"), NotNull: true},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	sql := ops[0].SQL()
	if !strings.Contains(sql, `"id" SERIAL`) {
		t.Errorf("expected literal SERIAL keyword, got: %s", sql)
	}
	if strings.Contains(sql, "NOT NULL") {
		t.Errorf("expected NOT NULL to be suppressed for SERIAL (PG implies it), got: %s", sql)
	}
	if strings.Contains(sql, "nextval") || strings.Contains(sql, "DEFAULT") {
		t.Errorf("expected no DEFAULT clause for SERIAL, got: %s", sql)
	}
}

// TestDiffColumnsAddSerialColumnSafe proves a new SERIAL column added to an
// existing table classifies SAFE (PostgreSQL auto-populates it via the
// sequence default), the same exemption Identity/Generated already get, and
// emits the literal SERIAL keyword rather than "integer ... NOT NULL".
func TestDiffColumnsAddSerialColumnSafe(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.widgets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "widgets",
			Columns: []snapshot.SnapColumn{{Name: "name", Type: "text"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "widgets",
			Columns: []*ir.Column{
				{Name: "name", Type: ir.TypeRef{Name: "text"}},
				{Name: "id", Type: ir.TypeRef{Name: "integer"}, Serial: serialPtr("SERIAL"), NotNull: true},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var addOp pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "ADD COLUMN") {
			addOp = o
			break
		}
	}
	if addOp == nil {
		t.Fatal("expected ADD COLUMN op")
	}
	if !strings.Contains(addOp.SQL(), `"id" SERIAL`) {
		t.Errorf("expected literal SERIAL keyword, got: %s", addOp.SQL())
	}
	if strings.Contains(addOp.SQL(), "NOT NULL") {
		t.Errorf("expected NOT NULL suppressed, got: %s", addOp.SQL())
	}
	if addOp.Safety() != pipeline.Safe {
		t.Errorf("expected SAFE, got %v", addOp.Safety())
	}
}

// TestDiffColumnsLegacySerialSnapshotNoDrift is a regression guard for the
// stale-snapshot self-healing path: a pre-upgrade snapshot stores the
// literal, un-normalized "serial" type name (before Column.Serial existed).
// Without the isLegacySerialTypeName guard in diffColumns, comparing the
// new normalized "integer" against the old literal "serial" would emit a
// spurious destructive ALTER COLUMN TYPE on the very first plan after
// upgrading, even though the live column hasn't changed shape at all.
func TestDiffColumnsLegacySerialSnapshotNoDrift(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.widgets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "widgets",
			Columns: []snapshot.SnapColumn{
				{Name: "id", Type: "serial", NotNull: true},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "widgets",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "integer"}, Serial: serialPtr("SERIAL"), NotNull: true},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "ALTER COLUMN") && strings.Contains(o.SQL(), "TYPE") {
			t.Errorf("expected no ALTER COLUMN TYPE against a legacy 'serial' snapshot, got: %s", o.SQL())
		}
		if o.Safety() == pipeline.Destructive {
			t.Errorf("expected no destructive ops against a legacy 'serial' snapshot, got: %s", o.SQL())
		}
	}
}

func TestCreateTableWithVirtualTypeColumn(t *testing.T) {
	// A column typed as a virtual type must emit jsonb in CREATE TABLE.
	d := New()
	desired := []pipeline.IRObject{
		makeVtype("public", "user_profile", ir.VtypeComposite{
			Fields: []ir.VtypeField{
				{Name: "name", Type: ir.VtypeTypeRef{Name: "text"}},
				{Name: "age", Type: ir.VtypeTypeRef{Name: "integer"}},
			},
		}),
		&ir.Table{
			Schema: "public",
			Name:   "users",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{Name: "profile", Type: ir.TypeRef{Name: "user_profile"}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	combined := strings.Join(sqlList(ops), " ")
	if !strings.Contains(combined, "jsonb") {
		t.Errorf("expected jsonb in CREATE TABLE SQL, got: %s", combined)
	}
	if strings.Contains(combined, "user_profile") {
		t.Errorf("unexpected virtual type name in SQL (should be jsonb): %s", combined)
	}
}

func TestCreateTableWithVirtualTypeArrayColumn(t *testing.T) {
	// A column typed as virtual_type[] must emit jsonb[] in CREATE TABLE.
	d := New()
	desired := []pipeline.IRObject{
		makeVtype("public", "line_item", ir.VtypeComposite{
			Fields: []ir.VtypeField{
				{Name: "sku", Type: ir.VtypeTypeRef{Name: "text"}},
				{Name: "qty", Type: ir.VtypeTypeRef{Name: "integer"}},
			},
		}),
		&ir.Table{
			Schema: "public",
			Name:   "orders",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{Name: "items", Type: ir.TypeRef{Name: "line_item", ArrayDims: 1}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	combined := strings.Join(sqlList(ops), " ")
	if !strings.Contains(combined, "jsonb[]") {
		t.Errorf("expected jsonb[] in CREATE TABLE SQL, got: %s", combined)
	}
}

func TestCreateTableWithSchemaQualifiedVirtualTypeColumn(t *testing.T) {
	// Schema-qualified virtual type reference: billing.payment_method → jsonb.
	d := New()
	desired := []pipeline.IRObject{
		makeVtype("billing", "payment_method", ir.VtypeTypeRef{Name: "text"}),
		&ir.Table{
			Schema: "public",
			Name:   "invoices",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{Name: "payment", Type: ir.TypeRef{Schema: "billing", Name: "payment_method"}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	combined := strings.Join(sqlList(ops), " ")
	if !strings.Contains(combined, "jsonb") {
		t.Errorf("expected jsonb in CREATE TABLE SQL for schema-qualified virtual type, got: %s", combined)
	}
}

func TestAddColumnWithVirtualTypeResolvesToJsonb(t *testing.T) {
	// ADD COLUMN with a virtual type reference must use jsonb.
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public", Name: "orders",
			Columns: []snapshot.SnapColumn{
				{Name: "id", Type: "bigint"},
			},
		},
	})
	desired := []pipeline.IRObject{
		makeVtype("public", "line_item", ir.VtypeComposite{
			Fields: []ir.VtypeField{{Name: "sku", Type: ir.VtypeTypeRef{Name: "text"}}},
		}),
		&ir.Table{
			Schema: "public",
			Name:   "orders",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{Name: "items", Type: ir.TypeRef{Name: "line_item", ArrayDims: 1}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	combined := strings.Join(sqlList(ops), " ")
	if !strings.Contains(combined, "ADD COLUMN") || !strings.Contains(combined, "jsonb[]") {
		t.Errorf("expected ADD COLUMN ... jsonb[], got: %s", combined)
	}
}

func TestCompositeTypeWithVirtualTypeAttrResolvesToJsonb(t *testing.T) {
	// A composite type attribute typed as a virtual type must emit jsonb.
	d := New()
	desired := []pipeline.IRObject{
		makeVtype("public", "address_detail", ir.VtypeComposite{
			Fields: []ir.VtypeField{{Name: "line", Type: ir.VtypeTypeRef{Name: "text"}}},
		}),
		&ir.Type{
			Schema:  "public",
			Name:    "full_address",
			Variant: "COMPOSITE",
			CompositeAttrs: []*ir.Column{
				{Name: "street", Type: ir.TypeRef{Name: "text"}},
				{Name: "detail", Type: ir.TypeRef{Name: "address_detail"}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	combined := strings.Join(sqlList(ops), " ")
	if !strings.Contains(combined, "jsonb") {
		t.Errorf("expected jsonb in CREATE TYPE SQL for virtual type attr, got: %s", combined)
	}
	if strings.Contains(combined, "address_detail") {
		t.Errorf("unexpected virtual type name in SQL: %s", combined)
	}
}

func TestCreateTableVirtualTypePreferredJson(t *testing.T) {
	// PREFERRED JSON FORMAT json → column emits json, not jsonb.
	d := New()
	desired := []pipeline.IRObject{
		&ir.VirtualType{
			Schema:     "public",
			Name:       "event_payload",
			Body:       ir.VtypeTypeRef{Name: "text"},
			JsonFormat: "json",
		},
		&ir.Table{
			Schema: "public",
			Name:   "events",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{Name: "payload", Type: ir.TypeRef{Name: "event_payload"}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	combined := strings.Join(sqlList(ops), " ")
	if !strings.Contains(combined, `"payload" json`) {
		t.Errorf("expected 'json' column type for PREFERRED JSON FORMAT json, got: %s", combined)
	}
	if strings.Contains(combined, `"payload" jsonb`) {
		t.Errorf("unexpected 'jsonb' in SQL for json-preferred virtual type: %s", combined)
	}
}

func TestCreateTableVirtualTypePreferredJsonArray(t *testing.T) {
	// PREFERRED JSON FORMAT json with [] → json[].
	d := New()
	desired := []pipeline.IRObject{
		&ir.VirtualType{
			Schema:     "public",
			Name:       "event_payload",
			Body:       ir.VtypeTypeRef{Name: "text"},
			JsonFormat: "json",
		},
		&ir.Table{
			Schema: "public",
			Name:   "events",
			Columns: []*ir.Column{
				{Name: "payloads", Type: ir.TypeRef{Name: "event_payload", ArrayDims: 1}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	combined := strings.Join(sqlList(ops), " ")
	if !strings.Contains(combined, "json[]") {
		t.Errorf("expected json[] for json-preferred virtual type array, got: %s", combined)
	}
	if strings.Contains(combined, "jsonb[]") {
		t.Errorf("unexpected jsonb[] for json-preferred virtual type: %s", combined)
	}
}

func TestCreateTableVirtualTypeDefaultIsJsonb(t *testing.T) {
	// No PREFERRED JSON FORMAT → defaults to jsonb.
	d := New()
	desired := []pipeline.IRObject{
		&ir.VirtualType{Schema: "public", Name: "tag", Body: ir.VtypeTypeRef{Name: "text"}},
		&ir.Table{
			Schema: "public", Name: "items",
			Columns: []*ir.Column{
				{Name: "meta", Type: ir.TypeRef{Name: "tag"}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	combined := strings.Join(sqlList(ops), " ")
	if !strings.Contains(combined, `"meta" jsonb`) {
		t.Errorf("expected jsonb default for virtual type, got: %s", combined)
	}
}

func TestNonVirtualTypeColumnNotAffected(t *testing.T) {
	// A real PG type column (e.g. text) must not be changed to jsonb.
	d := New()
	desired := []pipeline.IRObject{
		makeVtype("public", "tag", ir.VtypeTypeRef{Name: "text"}),
		&ir.Table{
			Schema: "public",
			Name:   "products",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{Name: "name", Type: ir.TypeRef{Name: "text"}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), "\"name\" jsonb") {
			t.Errorf("plain text column was wrongly resolved to jsonb: %s", op.SQL())
		}
	}
}

func sqlList(ops []pipeline.DiffOp) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = o.SQL()
	}
	return out
}

// containsSQL returns true if any op's SQL contains substr.
func containsSQL(ops []pipeline.DiffOp, substr string) bool {
	for _, o := range ops {
		if strings.Contains(o.SQL(), substr) {
			return true
		}
	}
	return false
}

// ── Grant diffing ─────────────────────────────────────────────────────────────

func TestDiffTableGrantAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "orders",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Grants:  []ir.Grant{{Privileges: []string{"SELECT"}, Roles: []string{"readonly"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "GRANT SELECT ON TABLE") {
		t.Errorf("expected GRANT SELECT ON TABLE, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, `"readonly"`) {
		t.Errorf("expected quoted role name, got: %v", sqlList(ops))
	}
}

func TestDiffTableGrantRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Grants:  []snapshot.SnapGrant{{Privileges: []string{"SELECT"}, Roles: []string{"readonly"}}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "orders",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE SELECT ON TABLE") {
		t.Errorf("expected REVOKE SELECT ON TABLE, got: %v", sqlList(ops))
	}
}

// TestRoleListPublicNotQuoted guards a real bug found live-testing a demo
// project: roleList quoted every role name unconditionally, including the
// PostgreSQL pseudo-role keyword PUBLIC. Quoting it makes PG look up an
// actual role literally named "PUBLIC" (which never exists), erroring with
// `role "PUBLIC" does not exist` — hit live via introspection's grant
// queries, which emit the literal string "PUBLIC" as the grantee for PG's
// own implicit default EXECUTE-to-PUBLIC grant on a new function/aggregate.
func TestRoleListPublicNotQuoted(t *testing.T) {
	got := roleList([]string{"PUBLIC", "app_service", "public"})
	want := `PUBLIC, "app_service", PUBLIC`
	if got != want {
		t.Errorf("roleList: got %q, want %q", got, want)
	}
}

// TestDiffTableGrantRemovedFromPublic is TestDiffTableGrantRemoved's
// PUBLIC-specific sibling: the emitted REVOKE must not quote PUBLIC.
func TestDiffTableGrantRemovedFromPublic(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Grants:  []snapshot.SnapGrant{{Privileges: []string{"SELECT"}, Roles: []string{"PUBLIC"}}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "orders",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	want := `REVOKE SELECT ON TABLE "public"."orders" FROM PUBLIC;`
	if !containsSQL(ops, want) {
		t.Errorf("expected %q (PUBLIC unquoted), got: %v", want, sqlList(ops))
	}
	if containsSQL(ops, `"PUBLIC"`) {
		t.Errorf("PUBLIC must not be quoted, got: %v", sqlList(ops))
	}
}

func TestDiffTableGrantUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Grants:  []snapshot.SnapGrant{{Privileges: []string{"SELECT"}, Roles: []string{"readonly"}}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "orders",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Grants:  []ir.Grant{{Privileges: []string{"SELECT"}, Roles: []string{"readonly"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "GRANT") || containsSQL(ops, "REVOKE") {
		t.Errorf("expected no GRANT/REVOKE for unchanged grant, got: %v", sqlList(ops))
	}
}

// ── Explicit REVOCATIONS (RFC §11.3) ──────────────────────────────────────────
//
// Regression guard for the discovery that ir.Table/Column/View.Revocations was
// parsed into the IR but never read anywhere in diff/create/emit — an explicit
// REVOCATION was a total silent no-op regardless of Mode A/B syntax. These
// tests cover: emission on a brand-new object, emission when a REVOCATION is
// newly added to an already-applied object, restoration (re-GRANT) when a
// REVOCATION is removed, and idempotency (unchanged REVOCATION => zero ops,
// so a stored revocation isn't re-issued — PG errors on REVOKE of an
// already-absent privilege per the RFC).

func TestDiffCreateTableEmitsRevocation(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:      "public",
			Name:        "orders",
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Revocations: []ir.Revocation{{Privileges: []string{"UPDATE"}, Roles: []string{"readonly"}}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE UPDATE ON TABLE") {
		t.Errorf("expected REVOKE UPDATE ON TABLE at create time, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, `"readonly"`) {
		t.Errorf("expected quoted role name, got: %v", sqlList(ops))
	}
}

func TestDiffTableRevocationAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:      "public",
			Name:        "orders",
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Revocations: []ir.Revocation{{Privileges: []string{"UPDATE"}, Roles: []string{"readonly"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, o := range ops {
		if strings.Contains(o.SQL(), "REVOKE UPDATE ON TABLE") {
			found = true
			if o.Safety() != pipeline.Caution {
				t.Errorf("explicit revocation safety = %v, want Caution: %s", o.Safety(), o.SQL())
			}
		}
	}
	if !found {
		t.Errorf("expected REVOKE UPDATE ON TABLE, got: %v", sqlList(ops))
	}
}

func TestDiffTableRevocationRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:      "public",
			Name:        "orders",
			Columns:     []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Revocations: []snapshot.SnapGrant{{Privileges: []string{"UPDATE"}, Roles: []string{"readonly"}}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "orders",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "GRANT UPDATE ON TABLE") {
		t.Errorf("expected GRANT UPDATE ON TABLE to restore the revoked privilege, got: %v", sqlList(ops))
	}
	if containsSQL(ops, "REVOKE") {
		t.Errorf("must not also emit REVOKE when the revocation itself was removed: %v", sqlList(ops))
	}
}

// The idempotency guard: PG errors on REVOKE of an already-absent privilege
// (RFC §11.3), so an unchanged REVOCATION must produce zero ops once it's
// been applied and recorded in the snapshot — not re-issued on every apply.
func TestDiffTableRevocationUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:      "public",
			Name:        "orders",
			Columns:     []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Revocations: []snapshot.SnapGrant{{Privileges: []string{"UPDATE"}, Roles: []string{"readonly"}}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:      "public",
			Name:        "orders",
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Revocations: []ir.Revocation{{Privileges: []string{"UPDATE"}, Roles: []string{"readonly"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "GRANT") || containsSQL(ops, "REVOKE") {
		t.Errorf("expected no GRANT/REVOKE for unchanged revocation, got: %v", sqlList(ops))
	}
}

func TestDiffTableRevocationCascade(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.orders", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "orders",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "orders",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Revocations: []ir.Revocation{
				{Privileges: []string{"UPDATE"}, Roles: []string{"readonly"}, Cascade: true},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE UPDATE ON TABLE") || !containsSQL(ops, "CASCADE") {
		t.Errorf("expected REVOKE ... CASCADE, got: %v", sqlList(ops))
	}
}

func TestDiffViewGrantAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.v_active", &snapshot.SnapObject{
		Kind: "view",
		View: &snapshot.SnapView{
			Schema: "public",
			Name:   "v_active",
			Query:  "SELECT id FROM users WHERE active",
		},
	})
	desired := []pipeline.IRObject{
		&ir.View{
			Schema: "public",
			Name:   "v_active",
			Query:  "SELECT id FROM users WHERE active",
			Grants: []ir.Grant{{Privileges: []string{"SELECT"}, Roles: []string{"api"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "GRANT SELECT ON TABLE") {
		t.Errorf("expected GRANT SELECT ON TABLE for view, got: %v", sqlList(ops))
	}
}

func TestDiffViewGrantRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.v_active", &snapshot.SnapObject{
		Kind: "view",
		View: &snapshot.SnapView{
			Schema: "public",
			Name:   "v_active",
			Query:  "SELECT id FROM users WHERE active",
			Grants: []snapshot.SnapGrant{{Privileges: []string{"SELECT"}, Roles: []string{"api"}}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.View{
			Schema: "public",
			Name:   "v_active",
			Query:  "SELECT id FROM users WHERE active",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE SELECT ON TABLE") {
		t.Errorf("expected REVOKE SELECT ON TABLE for view, got: %v", sqlList(ops))
	}
}

func TestDiffCreateViewEmitsRevocation(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.View{
			Schema:      "public",
			Name:        "v_active",
			Query:       "SELECT id FROM users WHERE active",
			Revocations: []ir.Revocation{{Privileges: []string{"SELECT"}, Roles: []string{"guest"}}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE SELECT ON TABLE") {
		t.Errorf("expected REVOKE SELECT ON TABLE for a new view, got: %v", sqlList(ops))
	}
}

// TestDiffCreateViewEmitsOwner guards a real bug found live-testing a demo
// project: ir.View.Owner (and snapshot.SnapView.Owner) both existed and were
// populated by the builder, but createView/diffView never referenced Owner
// at all — an OWNER directive on a VIEW, RECURSIVE VIEW, or MATERIALIZED
// VIEW silently vanished with no error, no diff, nothing. Unlike TABLE's
// createTable (which always emits ALTER TABLE ... OWNER TO), createView had
// no equivalent statement whatsoever.
func TestDiffCreateViewEmitsOwner(t *testing.T) {
	d := New()
	owner := "app_role"
	desired := []pipeline.IRObject{
		&ir.View{Schema: "public", Name: "v_active", Query: "SELECT id FROM users", Owner: &owner},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	want := `ALTER VIEW "public"."v_active" OWNER TO "app_role";`
	if !containsSQL(ops, want) {
		t.Errorf("expected %q, got: %v", want, sqlList(ops))
	}
}

// TestDiffCreateMaterializedViewEmitsOwner is the materialized-view sibling
// of TestDiffCreateViewEmitsOwner — real PG uses "ALTER MATERIALIZED VIEW",
// not "ALTER VIEW", for a materialized view's owner.
func TestDiffCreateMaterializedViewEmitsOwner(t *testing.T) {
	d := New()
	owner := "app_role"
	desired := []pipeline.IRObject{
		&ir.View{Schema: "public", Name: "mv_totals", Materialized: true, Query: "SELECT 1", Owner: &owner},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	want := `ALTER MATERIALIZED VIEW "public"."mv_totals" OWNER TO "app_role";`
	if !containsSQL(ops, want) {
		t.Errorf("expected %q, got: %v", want, sqlList(ops))
	}
}

// TestDiffCreateMaterializedViewEmitsIndex guards RFC §8.2's matview-block
// INDICES support: a new materialized view's indexes must be created
// alongside it, non-concurrently (it doesn't exist yet — same reasoning as
// createTable's brand-new-table indexes).
func TestDiffCreateMaterializedViewEmitsIndex(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.View{
			Schema: "public", Name: "mv_totals", Materialized: true, Query: "SELECT status FROM orders",
			Indexes: []*ir.Index{
				{Name: "mv_totals_status_uq", Unique: true, Columns: []pipeline.IndexColumn{{Name: "status"}}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	want := `CREATE UNIQUE INDEX "mv_totals_status_uq" ON "public"."mv_totals" ("status");`
	if !containsSQL(ops, want) {
		t.Errorf("expected %q, got: %v", want, sqlList(ops))
	}
}

// TestDiffMaterializedViewIndexAddedToExisting guards diffView's index diff
// path (not just create): adding an INDICES entry to an already-applied
// materialized view must emit a CREATE INDEX without disturbing the view
// itself.
func TestDiffMaterializedViewIndexAddedToExisting(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.mv_totals", &snapshot.SnapObject{
		Kind: "view",
		View: &snapshot.SnapView{
			Schema: "public", Name: "mv_totals", Query: "SELECT status FROM orders",
		},
	})
	desired := []pipeline.IRObject{
		&ir.View{
			Schema: "public", Name: "mv_totals", Materialized: true, Query: "SELECT status FROM orders",
			Indexes: []*ir.Index{
				{Name: "mv_totals_status_uq", Unique: true, Columns: []pipeline.IndexColumn{{Name: "status"}}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	want := `CREATE UNIQUE INDEX "mv_totals_status_uq" ON "public"."mv_totals" ("status");`
	if !containsSQL(ops, want) {
		t.Errorf("expected %q, got: %v", want, sqlList(ops))
	}
	if containsSQL(ops, "DROP MATERIALIZED VIEW") || containsSQL(ops, "CREATE MATERIALIZED VIEW") {
		t.Errorf("adding an index should not recreate the view itself, got: %v", sqlList(ops))
	}
}

// TestDiffMaterializedViewIndexRemoved is the removal-side sibling.
func TestDiffMaterializedViewIndexRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.mv_totals", &snapshot.SnapObject{
		Kind: "view",
		View: &snapshot.SnapView{
			Schema: "public", Name: "mv_totals", Query: "SELECT status FROM orders",
			Indexes: []snapshot.SnapIndex{
				{Name: "mv_totals_status_uq", Unique: true, Method: "btree", Columns: "status"},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.View{Schema: "public", Name: "mv_totals", Materialized: true, Query: "SELECT status FROM orders"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP INDEX IF EXISTS "mv_totals_status_uq";`) {
		t.Errorf("expected DROP INDEX for removed matview index, got: %v", sqlList(ops))
	}
}

// TestDiffViewOwnerChanged guards the diff-existing-view path (not just
// create): changing OWNER on an already-applied view must also be detected.
func TestDiffViewOwnerChanged(t *testing.T) {
	d := New()
	oldOwner := "old_role"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.v_active", &snapshot.SnapObject{
		Kind: "view",
		View: &snapshot.SnapView{
			Schema: "public",
			Name:   "v_active",
			Query:  "SELECT id FROM users",
			Owner:  &oldOwner,
		},
	})
	newOwner := "new_role"
	desired := []pipeline.IRObject{
		&ir.View{Schema: "public", Name: "v_active", Query: "SELECT id FROM users", Owner: &newOwner},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	want := `ALTER VIEW "public"."v_active" OWNER TO "new_role";`
	if !containsSQL(ops, want) {
		t.Errorf("expected %q, got: %v", want, sqlList(ops))
	}
}

// TestDiffViewOwnerUnchangedIsNoop is the negative case: identical Owner
// must not produce a spurious ALTER on every plan.
func TestDiffViewOwnerUnchangedIsNoop(t *testing.T) {
	d := New()
	owner := "app_role"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.v_active", &snapshot.SnapObject{
		Kind: "view",
		View: &snapshot.SnapView{
			Schema: "public",
			Name:   "v_active",
			Query:  "SELECT id FROM users",
			Owner:  &owner,
		},
	})
	desired := []pipeline.IRObject{
		&ir.View{Schema: "public", Name: "v_active", Query: "SELECT id FROM users", Owner: &owner},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "OWNER TO") {
		t.Errorf("expected no OWNER TO op for unchanged owner, got: %v", sqlList(ops))
	}
}

func TestDiffViewRevocationAddedAndRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.v_active", &snapshot.SnapObject{
		Kind: "view",
		View: &snapshot.SnapView{
			Schema: "public",
			Name:   "v_active",
			Query:  "SELECT id FROM users WHERE active",
		},
	})
	desired := []pipeline.IRObject{
		&ir.View{
			Schema:      "public",
			Name:        "v_active",
			Query:       "SELECT id FROM users WHERE active",
			Revocations: []ir.Revocation{{Privileges: []string{"SELECT"}, Roles: []string{"guest"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE SELECT ON TABLE") {
		t.Errorf("expected REVOKE SELECT ON TABLE when adding a view revocation, got: %v", sqlList(ops))
	}

	// Now remove it: the snapshot side has the revocation, desired doesn't.
	snap2 := &pipeline.Snapshot{}
	_ = snap2.SetObject("public.v_active", &snapshot.SnapObject{
		Kind: "view",
		View: &snapshot.SnapView{
			Schema:      "public",
			Name:        "v_active",
			Query:       "SELECT id FROM users WHERE active",
			Revocations: []snapshot.SnapGrant{{Privileges: []string{"SELECT"}, Roles: []string{"guest"}}},
		},
	})
	desired2 := []pipeline.IRObject{
		&ir.View{Schema: "public", Name: "v_active", Query: "SELECT id FROM users WHERE active"},
	}
	ops2, err := d.Diff(desired2, snap2)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops2, "GRANT SELECT ON TABLE") {
		t.Errorf("expected GRANT SELECT ON TABLE to restore, got: %v", sqlList(ops2))
	}
}

func TestDiffFunctionGrantAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.get_user()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema:     "public",
			Name:       "get_user",
			ReturnType: "void",
			Language:   "plpgsql",
			Volatility: "VOLATILE",
			BodyHash:   "abc",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema:     "public",
			Name:       "get_user",
			ReturnType: ir.TypeRef{Name: "void"},
			BodyHash:   "abc",
			Attrs:      ir.FuncAttrs{Language: "plpgsql", Volatility: "VOLATILE", Body: "BEGIN END;"},
			Grants:     []ir.Grant{{Privileges: []string{"EXECUTE"}, Roles: []string{"app"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "GRANT EXECUTE ON FUNCTION") {
		t.Errorf("expected GRANT EXECUTE ON FUNCTION, got: %v", sqlList(ops))
	}
}

func TestDiffFunctionGrantRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.get_user()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema:     "public",
			Name:       "get_user",
			ReturnType: "void",
			Language:   "plpgsql",
			Volatility: "VOLATILE",
			BodyHash:   "abc",
			Grants:     []snapshot.SnapGrant{{Privileges: []string{"EXECUTE"}, Roles: []string{"app"}}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema:     "public",
			Name:       "get_user",
			ReturnType: ir.TypeRef{Name: "void"},
			BodyHash:   "abc",
			Attrs:      ir.FuncAttrs{Language: "plpgsql", Volatility: "VOLATILE", Body: "BEGIN END;"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE EXECUTE ON FUNCTION") {
		t.Errorf("expected REVOKE EXECUTE ON FUNCTION, got: %v", sqlList(ops))
	}
}

// ── CREATE-time grant emission ────────────────────────────────────────────────

func TestDiffCreateViewEmitsGrant(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.View{
			Schema: "public",
			Name:   "v_summary",
			Query:  "SELECT 1",
			Grants: []ir.Grant{{Privileges: []string{"SELECT"}, Roles: []string{"readonly"}}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "CREATE") {
		t.Fatal("expected CREATE VIEW")
	}
	if !containsSQL(ops, "GRANT SELECT ON TABLE") {
		t.Errorf("expected GRANT at creation time, got: %v", sqlList(ops))
	}
}

func TestDiffCreateFunctionEmitsGrant(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema:     "public",
			Name:       "do_work",
			ReturnType: ir.TypeRef{Name: "void"},
			BodyHash:   "h",
			Attrs:      ir.FuncAttrs{Language: "plpgsql", Body: "BEGIN END;"},
			Grants:     []ir.Grant{{Privileges: []string{"EXECUTE"}, Roles: []string{"worker"}}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "CREATE OR REPLACE FUNCTION") {
		t.Fatal("expected CREATE FUNCTION")
	}
	if !containsSQL(ops, "GRANT EXECUTE ON FUNCTION") {
		t.Errorf("expected GRANT at creation time, got: %v", sqlList(ops))
	}
}

// ── INHERITS diffing ──────────────────────────────────────────────────────────

func TestDiffTableInheritsAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.logs", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "logs",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:   "public",
			Name:     "logs",
			Columns:  []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Inherits: []string{"base_logs"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "ALTER TABLE") || !containsSQL(ops, "INHERIT") {
		t.Errorf("expected ALTER TABLE ... INHERIT, got: %v", sqlList(ops))
	}
	if containsSQL(ops, "NO INHERIT") {
		t.Errorf("unexpected NO INHERIT, got: %v", sqlList(ops))
	}
}

func TestDiffTableInheritsRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.logs", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:   "public",
			Name:     "logs",
			Columns:  []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Inherits: []string{"base_logs"},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "logs",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "NO INHERIT") {
		t.Errorf("expected NO INHERIT, got: %v", sqlList(ops))
	}
}

// ── Column attribute diffing ──────────────────────────────────────────────────

func strPtr(s string) *string { return &s }
func intPtr(n int) *int       { return &n }

func TestDiffColumnStorageChanged(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.docs", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "docs",
			Columns: []snapshot.SnapColumn{{Name: "body", Type: "text", Storage: strPtr("EXTENDED")}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "docs",
			Columns: []*ir.Column{{Name: "body", Type: ir.TypeRef{Name: "text"}, Storage: strPtr("EXTERNAL")}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "SET STORAGE EXTERNAL") {
		t.Errorf("expected SET STORAGE EXTERNAL, got: %v", sqlList(ops))
	}
}

func TestDiffColumnCompressionChanged(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.docs", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "docs",
			Columns: []snapshot.SnapColumn{{Name: "body", Type: "text"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "docs",
			Columns: []*ir.Column{{Name: "body", Type: ir.TypeRef{Name: "text"}, Compression: strPtr("lz4")}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "SET COMPRESSION lz4") {
		t.Errorf("expected SET COMPRESSION lz4, got: %v", sqlList(ops))
	}
}

func TestDiffColumnStatisticsSet(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.events", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "events",
			Columns: []snapshot.SnapColumn{{Name: "ts", Type: "timestamptz"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "events",
			Columns: []*ir.Column{{Name: "ts", Type: ir.TypeRef{Name: "timestamptz"}, Statistics: intPtr(500)}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "SET STATISTICS 500") {
		t.Errorf("expected SET STATISTICS 500, got: %v", sqlList(ops))
	}
}

func TestDiffColumnStatisticsReset(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.events", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "events",
			Columns: []snapshot.SnapColumn{{Name: "ts", Type: "timestamptz", Statistics: intPtr(500)}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "events",
			Columns: []*ir.Column{{Name: "ts", Type: ir.TypeRef{Name: "timestamptz"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "SET STATISTICS -1") {
		t.Errorf("expected SET STATISTICS -1 (reset), got: %v", sqlList(ops))
	}
}

// ── View structural changes ───────────────────────────────────────────────────

func TestDiffViewRecursiveChangedDropsAndRecretes(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.v_tree", &snapshot.SnapObject{
		Kind: "view",
		View: &snapshot.SnapView{
			Schema:    "public",
			Name:      "v_tree",
			Query:     "SELECT id FROM nodes",
			Recursive: false,
		},
	})
	desired := []pipeline.IRObject{
		&ir.View{
			Schema:    "public",
			Name:      "v_tree",
			Query:     "SELECT id FROM nodes",
			Recursive: true,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "DROP VIEW IF EXISTS") {
		t.Errorf("expected DROP VIEW IF EXISTS, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "RECURSIVE") {
		t.Errorf("expected RECURSIVE in CREATE VIEW, got: %v", sqlList(ops))
	}
	for _, o := range ops {
		if o.Safety() == pipeline.Safe && strings.Contains(o.SQL(), "DROP") {
			t.Errorf("DROP should be Destructive, got Safe: %s", o.SQL())
		}
	}
}

func TestDiffCreateMaterViewWithNoData(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.View{
			Schema:       "public",
			Name:         "mv_summary",
			Query:        "SELECT count(*) FROM orders",
			Materialized: true,
			WithNoData:   true,
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "CREATE MATERIALIZED VIEW") {
		t.Fatal("expected CREATE MATERIALIZED VIEW")
	}
	if !containsSQL(ops, "WITH NO DATA") {
		t.Errorf("expected WITH NO DATA clause, got: %v", sqlList(ops))
	}
}

func TestDiffMaterViewWithNoDataChangedIsManual(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.mv_summary", &snapshot.SnapObject{
		Kind: "view",
		View: &snapshot.SnapView{
			Schema:     "public",
			Name:       "mv_summary",
			Query:      "SELECT count(*) FROM orders",
			WithNoData: false,
		},
	})
	desired := []pipeline.IRObject{
		&ir.View{
			Schema:       "public",
			Name:         "mv_summary",
			Query:        "SELECT count(*) FROM orders",
			Materialized: true,
			WithNoData:   true,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REFRESH MATERIALIZED VIEW") {
		t.Errorf("expected REFRESH MATERIALIZED VIEW notice, got: %v", sqlList(ops))
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "REFRESH") && o.Safety() != pipeline.Manual {
			t.Errorf("WITH NO DATA change notice should be Manual, got %s", o.Safety())
		}
	}
}

// ── Partitioning ─────────────────────────────────────────────────────────────

// ── TRIGGERS diffing ─────────────────────────────────────────────────────────
//
// No unit test exercised diffTriggers at all before now — the function-
// qualification mismatch below (introspection always returns "schema.func",
// hand-written source commonly writes an unqualified "func") was invisible at
// both the unit and (until a live partition test happened to exercise the
// full introspect+diff round trip) integration level.

func TestDiffTriggerUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Triggers: []snapshot.SnapTrigger{
				{Name: "trg_a", When: "AFTER", Events: "INSERT", ForEach: "ROW", Function: "public.trg_touch"},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "t",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Triggers: []*ir.Trigger{
				{Name: "trg_a", When: "AFTER", Events: []string{"INSERT"}, ForEach: "ROW", Function: "trg_touch"},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "TRIGGER") {
		t.Errorf("expected no trigger ops: an unqualified source function name ('trg_touch') must compare equal to introspection's qualified form ('public.trg_touch'), got: %v", sqlList(ops))
	}
}

func TestDiffTriggerFunctionGenuinelyChanged(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Triggers: []snapshot.SnapTrigger{
				{Name: "trg_a", When: "AFTER", Events: "INSERT", ForEach: "ROW", Function: "public.old_func"},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "t",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Triggers: []*ir.Trigger{
				{Name: "trg_a", When: "AFTER", Events: []string{"INSERT"}, ForEach: "ROW", Function: "new_func"},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP TRIGGER IF EXISTS "trg_a"`) || !containsSQL(ops, "new_func") {
		t.Errorf("expected DROP+CREATE for a genuinely changed function, got: %v", sqlList(ops))
	}
}

func TestDiffTriggerAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "t",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Triggers: []*ir.Trigger{
				{Name: "trg_a", When: "AFTER", Events: []string{"INSERT"}, ForEach: "ROW", Function: "trg_touch"},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `CREATE TRIGGER "trg_a"`) {
		t.Errorf("expected CREATE TRIGGER for new trigger, got: %v", sqlList(ops))
	}
}

func TestDiffTriggerRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Triggers: []snapshot.SnapTrigger{
				{Name: "trg_a", When: "AFTER", Events: "INSERT", ForEach: "ROW", Function: "public.trg_touch"},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "t",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP TRIGGER IF EXISTS "trg_a"`) {
		t.Errorf("expected DROP TRIGGER for removed trigger, got: %v", sqlList(ops))
	}
}

func TestQualifyFuncForCompare(t *testing.T) {
	cases := []struct{ in, want string }{
		{"trg_touch", "public.trg_touch"},
		{"public.trg_touch", "public.trg_touch"},
		{"other_schema.trg_touch", "other_schema.trg_touch"},
	}
	for _, tc := range cases {
		if got := qualifyFuncForCompare(tc.in); got != tc.want {
			t.Errorf("qualifyFuncForCompare(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDiffCreateTableWithPartitionBy(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "events",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}, NotNull: true},
			},
			PartitionBy: &ir.PartitionSpec{Strategy: "RANGE", Columns: []string{"created_at"}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "PARTITION BY RANGE") {
		t.Errorf("expected PARTITION BY RANGE in CREATE TABLE, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "created_at") {
		t.Errorf("expected partition column in CREATE TABLE, got: %v", sqlList(ops))
	}
}

func TestDiffCreateTableWithPartitions(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "events",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}, NotNull: true},
			},
			PartitionBy: &ir.PartitionSpec{Strategy: "RANGE", Columns: []string{"created_at"}},
			Partitions: []*ir.Partition{
				{Name: "events_2024", Bounds: "FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')"},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	// Exact-text assertion, not a substring check: Bounds already carries the
	// full "FOR VALUES ..." clause (matching both the parser and
	// introspection), so a substring check for "FOR VALUES FROM" would pass
	// even with the once-real bug of prepending "FOR VALUES" a second time
	// ("... FOR VALUES FOR VALUES FROM (...)", a PG syntax error).
	want := `CREATE TABLE "public"."events_2024" PARTITION OF "public"."events" FOR VALUES FROM ('2024-01-01') TO ('2025-01-01');`
	if !containsSQL(ops, want) {
		t.Errorf("expected exact partition CREATE statement %q, got: %v", want, sqlList(ops))
	}
}

func TestDiffPartitionAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.events", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:      "public",
			Name:        "events",
			PartitionBy: "RANGE (created_at)",
			Columns:     []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:      "public",
			Name:        "events",
			PartitionBy: &ir.PartitionSpec{Strategy: "RANGE", Columns: []string{"created_at"}},
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Partitions: []*ir.Partition{
				{Name: "events_2024", Bounds: "FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')"},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	// Exact-text, not substring — see TestDiffCreateTableWithPartitions.
	want := `CREATE TABLE "public"."events_2024" PARTITION OF "public"."events" FOR VALUES FROM ('2024-01-01') TO ('2025-01-01');`
	if !containsSQL(ops, want) {
		t.Errorf("expected exact partition CREATE statement %q, got: %v", want, sqlList(ops))
	}
}

func TestDiffPartitionRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.events", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:      "public",
			Name:        "events",
			PartitionBy: "RANGE (created_at)",
			Columns:     []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Partitions: []snapshot.SnapPartition{
				{Schema: "public", Name: "events_2024", Bound: "FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')"},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:      "public",
			Name:        "events",
			PartitionBy: &ir.PartitionSpec{Strategy: "RANGE", Columns: []string{"created_at"}},
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "DROP TABLE") {
		t.Errorf("expected DROP TABLE for removed partition, got: %v", sqlList(ops))
	}
}

func TestDiffPartitionBoundChangedDropsAndRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.events", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:      "public",
			Name:        "events",
			PartitionBy: "RANGE (created_at)",
			Columns:     []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Partitions: []snapshot.SnapPartition{
				{Schema: "public", Name: "events_2024", Bound: "FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')"},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:      "public",
			Name:        "events",
			PartitionBy: &ir.PartitionSpec{Strategy: "RANGE", Columns: []string{"created_at"}},
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Partitions: []*ir.Partition{
				{Name: "events_2024", Bounds: "FOR VALUES FROM ('2024-01-01') TO ('2024-07-01')"},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP TABLE "public"."events_2024";`) {
		t.Errorf("expected DROP TABLE for bound change, got: %v", sqlList(ops))
	}
	// Exact-text, not substring — see TestDiffCreateTableWithPartitions.
	want := `CREATE TABLE "public"."events_2024" PARTITION OF "public"."events" FOR VALUES FROM ('2024-01-01') TO ('2024-07-01');`
	if !containsSQL(ops, want) {
		t.Errorf("expected exact partition CREATE statement %q, got: %v", want, sqlList(ops))
	}
}

// TestDiffCreateTableWithSubPartitions guards RFC §7.13 sub-partitioning:
// a partition entry may itself be PARTITION BY'd, in which case its CREATE
// TABLE ... PARTITION OF statement carries a trailing PARTITION BY clause,
// and each of ITS partitions is created as PARTITION OF the sub-partitioned
// child (not the top-level table).
func TestDiffCreateTableWithSubPartitions(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "metrics",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}, NotNull: true},
				{Name: "channel", Type: ir.TypeRef{Name: "text"}, NotNull: true},
			},
			PartitionBy: &ir.PartitionSpec{Strategy: "RANGE", Columns: []string{"created_at"}},
			Partitions: []*ir.Partition{
				{
					Name:        "metrics_2026",
					Bounds:      "FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')",
					PartitionBy: &ir.PartitionSpec{Strategy: "LIST", Columns: []string{"channel"}},
					Partitions: []*ir.Partition{
						{Name: "metrics_2026_web", Bounds: "FOR VALUES IN ('web')"},
					},
				},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	wantParent := `CREATE TABLE "public"."metrics_2026" PARTITION OF "public"."metrics" FOR VALUES FROM ('2026-01-01') TO ('2027-01-01') PARTITION BY LIST (channel);`
	if !containsSQL(ops, wantParent) {
		t.Errorf("expected sub-partitioned CREATE statement %q, got: %v", wantParent, sqlList(ops))
	}
	wantChild := `CREATE TABLE "public"."metrics_2026_web" PARTITION OF "public"."metrics_2026" FOR VALUES IN ('web');`
	if !containsSQL(ops, wantChild) {
		t.Errorf("expected nested partition CREATE statement %q, got: %v", wantChild, sqlList(ops))
	}
}

// TestDiffSubPartitionAdded guards adding a new leaf under an EXISTING
// sub-partitioned partition (the common ongoing-maintenance case, e.g.
// adding next quarter's channel partition).
func TestDiffSubPartitionAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.metrics", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:      "public",
			Name:        "metrics",
			PartitionBy: "RANGE (created_at)",
			Columns:     []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Partitions: []snapshot.SnapPartition{
				{
					Schema:      "public",
					Name:        "metrics_2026",
					Bound:       "FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')",
					PartitionBy: "LIST (channel)",
					Partitions: []snapshot.SnapPartition{
						{Schema: "public", Name: "metrics_2026_web", Bound: "FOR VALUES IN ('web')"},
					},
				},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:      "public",
			Name:        "metrics",
			PartitionBy: &ir.PartitionSpec{Strategy: "RANGE", Columns: []string{"created_at"}},
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Partitions: []*ir.Partition{
				{
					Name:        "metrics_2026",
					Bounds:      "FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')",
					PartitionBy: &ir.PartitionSpec{Strategy: "LIST", Columns: []string{"channel"}},
					Partitions: []*ir.Partition{
						{Name: "metrics_2026_web", Bounds: "FOR VALUES IN ('web')"},
						{Name: "metrics_2026_other", Bounds: "FOR VALUES IN ('mobile', 'api')"},
					},
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	// Only the new leaf should be created — the existing "metrics_2026" and
	// "metrics_2026_web" partitions must NOT be dropped/recreated.
	want := `CREATE TABLE "public"."metrics_2026_other" PARTITION OF "public"."metrics_2026" FOR VALUES IN ('mobile', 'api');`
	if !containsSQL(ops, want) {
		t.Errorf("expected exact new-leaf CREATE statement %q, got: %v", want, sqlList(ops))
	}
	if containsSQL(ops, `DROP TABLE "public"."metrics_2026"`) {
		t.Errorf("existing sub-partitioned parent should not be dropped, got: %v", sqlList(ops))
	}
	if containsSQL(ops, `DROP TABLE "public"."metrics_2026_web"`) {
		t.Errorf("existing leaf should not be dropped, got: %v", sqlList(ops))
	}
}

// TestDiffSubPartitionStrategyChangedIsManual mirrors
// TestDiffPartitionStrategyChangedIsManual one level down: a sub-partition's
// OWN PARTITION BY strategy also cannot be altered in place.
func TestDiffSubPartitionStrategyChangedIsManual(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.metrics", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:      "public",
			Name:        "metrics",
			PartitionBy: "RANGE (created_at)",
			Columns:     []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Partitions: []snapshot.SnapPartition{
				{
					Schema:      "public",
					Name:        "metrics_2026",
					Bound:       "FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')",
					PartitionBy: "LIST (channel)",
				},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:      "public",
			Name:        "metrics",
			PartitionBy: &ir.PartitionSpec{Strategy: "RANGE", Columns: []string{"created_at"}},
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Partitions: []*ir.Partition{
				{
					Name:        "metrics_2026",
					Bounds:      "FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')",
					PartitionBy: &ir.PartitionSpec{Strategy: "HASH", Columns: []string{"id"}},
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	hasManual := false
	for _, o := range ops {
		if o.Safety() == pipeline.Manual {
			hasManual = true
		}
	}
	if !hasManual {
		t.Errorf("expected a Manual op for sub-partition strategy change, got: %v", sqlList(ops))
	}
	if containsSQL(ops, "DROP TABLE") {
		t.Errorf("strategy change should not auto-drop, got: %v", sqlList(ops))
	}
}

func TestDiffPartitionStrategyChangedIsManual(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.events", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:      "public",
			Name:        "events",
			PartitionBy: "RANGE (created_at)",
			Columns:     []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:      "public",
			Name:        "events",
			PartitionBy: &ir.PartitionSpec{Strategy: "LIST", Columns: []string{"region"}},
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	hasManual := false
	for _, o := range ops {
		if o.Safety() == pipeline.Manual {
			hasManual = true
		}
	}
	if !hasManual {
		t.Errorf("expected Manual op for partition strategy change, got: %v", sqlList(ops))
	}
}

// ── Column-level grant tracking ───────────────────────────────────────────────

func TestDiffCreateTableEmitsColumnGrant(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "docs",
			Columns: []*ir.Column{
				{
					Name: "body",
					Type: ir.TypeRef{Name: "text"},
					Grants: []ir.Grant{
						{Privileges: []string{"SELECT"}, Roles: []string{"reader"}},
					},
				},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `GRANT SELECT ("body")`) {
		t.Errorf("expected column-level GRANT SELECT (body), got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "ON TABLE") {
		t.Errorf("expected ON TABLE in column grant, got: %v", sqlList(ops))
	}
}

func TestDiffColumnGrantAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.docs", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "docs",
			Columns: []snapshot.SnapColumn{{Name: "body", Type: "text"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "docs",
			Columns: []*ir.Column{
				{
					Name: "body",
					Type: ir.TypeRef{Name: "text"},
					Grants: []ir.Grant{
						{Privileges: []string{"SELECT"}, Roles: []string{"analyst"}},
					},
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `GRANT SELECT ("body")`) {
		t.Errorf("expected column GRANT SELECT, got: %v", sqlList(ops))
	}
}

func TestDiffColumnGrantRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.docs", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "docs",
			Columns: []snapshot.SnapColumn{
				{
					Name:   "body",
					Type:   "text",
					Grants: []snapshot.SnapGrant{{Privileges: []string{"SELECT"}, Roles: []string{"analyst"}}},
				},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "docs",
			Columns: []*ir.Column{{Name: "body", Type: ir.TypeRef{Name: "text"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `REVOKE SELECT ("body")`) {
		t.Errorf("expected column REVOKE SELECT, got: %v", sqlList(ops))
	}
}

func TestDiffCreateTableEmitsColumnRevocation(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "docs",
			Columns: []*ir.Column{
				{
					Name:        "body",
					Type:        ir.TypeRef{Name: "text"},
					Revocations: []ir.Revocation{{Privileges: []string{"SELECT"}, Roles: []string{"guest"}}},
				},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `REVOKE SELECT ("body")`) {
		t.Errorf("expected column-level REVOKE SELECT (body) at create time, got: %v", sqlList(ops))
	}
}

func TestDiffColumnRevocationAddedAndRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.docs", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "docs",
			Columns: []snapshot.SnapColumn{{Name: "body", Type: "text"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "docs",
			Columns: []*ir.Column{
				{
					Name:        "body",
					Type:        ir.TypeRef{Name: "text"},
					Revocations: []ir.Revocation{{Privileges: []string{"SELECT"}, Roles: []string{"guest"}}},
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `REVOKE SELECT ("body")`) {
		t.Errorf("expected column REVOKE SELECT when adding a revocation, got: %v", sqlList(ops))
	}

	// Now remove it: the snapshot side has the revocation, desired doesn't.
	snap2 := &pipeline.Snapshot{}
	_ = snap2.SetObject("public.docs", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "docs",
			Columns: []snapshot.SnapColumn{
				{
					Name:        "body",
					Type:        "text",
					Revocations: []snapshot.SnapGrant{{Privileges: []string{"SELECT"}, Roles: []string{"guest"}}},
				},
			},
		},
	})
	desired2 := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "docs",
			Columns: []*ir.Column{{Name: "body", Type: ir.TypeRef{Name: "text"}}},
		},
	}
	ops2, err := d.Diff(desired2, snap2)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops2, `GRANT SELECT ("body")`) {
		t.Errorf("expected column GRANT SELECT (body) to restore, got: %v", sqlList(ops2))
	}
}

func TestDiffColumnRevocationUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.docs", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "docs",
			Columns: []snapshot.SnapColumn{
				{
					Name:        "body",
					Type:        "text",
					Revocations: []snapshot.SnapGrant{{Privileges: []string{"SELECT"}, Roles: []string{"guest"}}},
				},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "docs",
			Columns: []*ir.Column{
				{
					Name:        "body",
					Type:        ir.TypeRef{Name: "text"},
					Revocations: []ir.Revocation{{Privileges: []string{"SELECT"}, Roles: []string{"guest"}}},
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "GRANT") || containsSQL(ops, "REVOKE") {
		t.Errorf("expected no GRANT/REVOKE for unchanged column revocation, got: %v", sqlList(ops))
	}
}

// New column added via ALTER TABLE ADD COLUMN, carrying its own REVOCATION —
// exercises the other diffColumns call site (new-column path vs
// existing-column path already covered above).
func TestDiffAddColumnEmitsRevocation(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.docs", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "docs",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "docs",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{
					Name:        "body",
					Type:        ir.TypeRef{Name: "text"},
					Revocations: []ir.Revocation{{Privileges: []string{"SELECT"}, Roles: []string{"guest"}}},
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `REVOKE SELECT ("body")`) {
		t.Errorf("expected column REVOKE SELECT for a newly added column, got: %v", sqlList(ops))
	}
}

func TestDiffColumnGrantUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.docs", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "docs",
			Columns: []snapshot.SnapColumn{
				{
					Name:   "body",
					Type:   "text",
					Grants: []snapshot.SnapGrant{{Privileges: []string{"SELECT"}, Roles: []string{"analyst"}}},
				},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "docs",
			Columns: []*ir.Column{
				{
					Name: "body",
					Type: ir.TypeRef{Name: "text"},
					Grants: []ir.Grant{
						{Privileges: []string{"SELECT"}, Roles: []string{"analyst"}},
					},
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "GRANT") || containsSQL(ops, "REVOKE") {
		t.Errorf("expected no grant ops when column grant unchanged, got: %v", sqlList(ops))
	}
}

func TestDiffAddColumnEmitsGrant(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.docs", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "docs",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "docs",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "bigint"}},
				{
					Name: "secret",
					Type: ir.TypeRef{Name: "text"},
					Grants: []ir.Grant{
						{Privileges: []string{"SELECT"}, Roles: []string{"admin"}},
					},
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "ADD COLUMN") {
		t.Errorf("expected ADD COLUMN, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, `GRANT SELECT ("secret")`) {
		t.Errorf("expected column grant after ADD COLUMN, got: %v", sqlList(ops))
	}
}

func TestDiffPartitionUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.events", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:      "public",
			Name:        "events",
			PartitionBy: "RANGE (created_at)",
			Columns:     []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Partitions: []snapshot.SnapPartition{
				{Schema: "public", Name: "events_2024", Bound: "FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')"},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:      "public",
			Name:        "events",
			PartitionBy: &ir.PartitionSpec{Strategy: "RANGE", Columns: []string{"created_at"}},
			Columns:     []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Partitions: []*ir.Partition{
				{Name: "events_2024", Bounds: "FOR VALUES FROM ('2024-01-01') TO ('2025-01-01')"},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "PARTITION") {
		t.Errorf("expected no partition ops when unchanged, got: %v", sqlList(ops))
	}
}

// ── MIGRATE REMOVE ─────────────────────────────────────────────────────────

func TestDiffEnumRemoveRequiresMigrateRemoveBlock(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.status", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "status", Variant: "ENUM",
			Values: []string{"active", "inactive", "cancelled"}},
	})
	desired := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "status", Variant: "ENUM",
			EnumValues: []string{"active", "inactive"}},
	}
	_, err := d.Diff(desired, snap)
	if err == nil {
		t.Fatal("expected error when MIGRATE REMOVE block is absent")
	}
	if !strings.Contains(err.Error(), "MIGRATE REMOVE") {
		t.Errorf("expected MIGRATE REMOVE in error, got: %s", err)
	}
}

func TestDiffEnumRemoveEmitsShadowTypeAndDrop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.status", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "status", Variant: "ENUM",
			Values: []string{"active", "inactive", "cancelled"}},
	})
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "status", Variant: "ENUM",
			EnumValues: []string{"active", "inactive"},
			MigrateRemove: &pipeline.MigrateRemoveBlock{
				SQL: pipeline.RawExpr{Text: "UPDATE orders SET status = 'active' WHERE status = 'cancelled';"},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	sqls := sqlList(ops)
	if !containsSQL(ops, "CREATE TYPE") || !containsSQL(ops, "__dpg_new") {
		t.Errorf("expected shadow type creation, got: %v", sqls)
	}
	if !containsSQL(ops, "UPDATE orders") {
		t.Errorf("expected DML passthrough, got: %v", sqls)
	}
	if !containsSQL(ops, "DROP TYPE") {
		t.Errorf("expected DROP TYPE, got: %v", sqls)
	}
	if !containsSQL(ops, "RENAME TO") {
		t.Errorf("expected RENAME TO, got: %v", sqls)
	}
}

func TestDiffEnumRemoveAltersAffectedColumns(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.status", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "status", Variant: "ENUM",
			Values: []string{"open", "closed", "archived"}},
	})
	_ = snap.SetObject("public.tickets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public", Name: "tickets",
			Columns: []snapshot.SnapColumn{
				{Name: "id", Type: "bigint"},
				{Name: "state", Type: "public.status"},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "status", Variant: "ENUM",
			EnumValues: []string{"open", "closed"},
			MigrateRemove: &pipeline.MigrateRemoveBlock{
				SQL: pipeline.RawExpr{Text: "UPDATE tickets SET state = 'closed' WHERE state = 'archived';"},
			},
		},
		&ir.Table{
			Schema:  "public",
			Name:    "tickets",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}, {Name: "state", Type: ir.TypeRef{Schema: "public", Name: "status"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	sqls := sqlList(ops)
	if !containsSQL(ops, "ALTER TABLE") || !containsSQL(ops, "ALTER COLUMN") || !containsSQL(ops, "TYPE") {
		t.Errorf("expected ALTER COLUMN TYPE for affected column, got: %v", sqls)
	}
	if !containsSQL(ops, "tickets") {
		t.Errorf("expected affected table tickets in ops, got: %v", sqls)
	}
	if !containsSQL(ops, "RAISE EXCEPTION") {
		t.Errorf("expected verification DO block, got: %v", sqls)
	}
}

func TestDiffEnumRemoveNoAffectedColumnsSkipsAlter(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.color", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "color", Variant: "ENUM",
			Values: []string{"red", "green", "blue"}},
	})
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "color", Variant: "ENUM",
			EnumValues: []string{"red", "green"},
			MigrateRemove: &pipeline.MigrateRemoveBlock{
				SQL: pipeline.RawExpr{Text: "DELETE FROM palette WHERE color = 'blue';"},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "ALTER TABLE") {
		t.Errorf("expected no ALTER TABLE when no columns reference the type, got: %v", sqlList(ops))
	}
	// Shadow type and rename must still be emitted.
	if !containsSQL(ops, "__dpg_new") {
		t.Errorf("expected shadow type, got: %v", sqlList(ops))
	}
}

func TestDiffEnumRemovePreservesComment(t *testing.T) {
	d := New()
	comment := "order lifecycle"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.order_status", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "order_status", Variant: "ENUM",
			Values: []string{"pending", "shipped", "cancelled"}},
	})
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "order_status", Variant: "ENUM",
			EnumValues: []string{"pending", "shipped"},
			Comment:    &comment,
			MigrateRemove: &pipeline.MigrateRemoveBlock{
				SQL: pipeline.RawExpr{Text: "UPDATE orders SET status = 'shipped' WHERE status = 'cancelled';"},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "COMMENT ON TYPE") {
		t.Errorf("expected COMMENT ON TYPE after rename, got: %v", sqlList(ops))
	}
}

func TestDiffEnumAddValueUnchangedByMigrateRemove(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.status", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "status", Variant: "ENUM",
			Values: []string{"active", "inactive"}},
	})
	desired := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "status", Variant: "ENUM",
			EnumValues: []string{"active", "inactive", "pending"}},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "ADD VALUE") {
		t.Errorf("expected ADD VALUE for added enum value, got: %v", sqlList(ops))
	}
	if containsSQL(ops, "__dpg_new") {
		t.Errorf("ADD VALUE should not trigger MIGRATE REMOVE procedure, got: %v", sqlList(ops))
	}
}

// ── Materialized view comment uses correct SQL object type ─────────────────

// ── Aggregate semantic diffing ────────────────────────────────────────────────

func TestDiffCreateAggregateEmitsBody(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Aggregate{
			Schema: "public",
			Name:   "my_agg",
			Args:   []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
			Body:   "CREATE AGGREGATE public.my_agg(numeric) (SFUNC = numeric_add, STYPE = numeric)",
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "CREATE AGGREGATE") {
		t.Errorf("expected CREATE AGGREGATE, got: %v", sqlList(ops))
	}
	if containsSQL(ops, "DROP AGGREGATE") {
		t.Errorf("unexpected DROP AGGREGATE on create, got: %v", sqlList(ops))
	}
}

func TestDiffCreateAggregateEmitsComment(t *testing.T) {
	d := New()
	comment := "sums numerics"
	desired := []pipeline.IRObject{
		&ir.Aggregate{
			Schema:  "public",
			Name:    "my_agg",
			Args:    []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
			Body:    "CREATE AGGREGATE public.my_agg(numeric) (SFUNC = numeric_add, STYPE = numeric)",
			Comment: &comment,
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "COMMENT ON AGGREGATE") {
		t.Errorf("expected COMMENT ON AGGREGATE, got: %v", sqlList(ops))
	}
}

func TestDiffCreateAggregateEmitsGrant(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Aggregate{
			Schema: "public",
			Name:   "my_agg",
			Args:   []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
			Body:   "CREATE AGGREGATE public.my_agg(numeric) (SFUNC = numeric_add, STYPE = numeric)",
			Grants: []ir.Grant{{Privileges: []string{"EXECUTE"}, Roles: []string{"analyst"}}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "GRANT EXECUTE ON FUNCTION") {
		t.Errorf("expected GRANT EXECUTE ON FUNCTION, got: %v", sqlList(ops))
	}
}

func TestDiffAggregateBodyChangedDropsAndRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	oldHash := fmt.Sprintf("%x", sha256Sum("CREATE AGGREGATE public.my_agg(numeric) (SFUNC = numeric_add, STYPE = numeric)"))
	_ = snap.SetObject(`public.my_agg(numeric)`, &snapshot.SnapObject{
		Kind: "aggregate",
		Opaque: &snapshot.SnapOpaque{
			Kind: "aggregate", Schema: "public", Name: "my_agg", Args: "numeric", BodyHash: oldHash,
		},
	})
	newBody := "CREATE AGGREGATE public.my_agg(numeric) (SFUNC = float8_accum, STYPE = float8[])"
	desired := []pipeline.IRObject{
		&ir.Aggregate{
			Schema: "public",
			Name:   "my_agg",
			Args:   []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
			Body:   newBody,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "DROP AGGREGATE IF EXISTS") {
		t.Errorf("expected DROP AGGREGATE IF EXISTS, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "CREATE AGGREGATE") {
		t.Errorf("expected CREATE AGGREGATE, got: %v", sqlList(ops))
	}
}

func TestDiffAggregateBodyChangedDropIsDestructive(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	oldHash := fmt.Sprintf("%x", sha256Sum("CREATE AGGREGATE public.my_agg(numeric) (SFUNC = numeric_add, STYPE = numeric)"))
	_ = snap.SetObject(`public.my_agg(numeric)`, &snapshot.SnapObject{
		Kind: "aggregate",
		Opaque: &snapshot.SnapOpaque{
			Kind: "aggregate", Schema: "public", Name: "my_agg", Args: "numeric", BodyHash: oldHash,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Aggregate{
			Schema: "public",
			Name:   "my_agg",
			Args:   []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
			Body:   "CREATE AGGREGATE public.my_agg(numeric) (SFUNC = float8_accum, STYPE = float8[])",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "DROP AGGREGATE") {
			if o.Safety() != pipeline.Destructive {
				t.Errorf("expected Destructive safety for DROP AGGREGATE, got %s", o.Safety())
			}
			return
		}
	}
	t.Error("DROP AGGREGATE op not found")
}

func TestDiffAggregateCommentOnlyChange(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	body := "CREATE AGGREGATE public.my_agg(numeric) (SFUNC = numeric_add, STYPE = numeric)"
	bodyHash := fmt.Sprintf("%x", sha256Sum(body))
	oldComment := "old comment"
	_ = snap.SetObject(`public.my_agg(numeric)`, &snapshot.SnapObject{
		Kind: "aggregate",
		Opaque: &snapshot.SnapOpaque{
			Kind: "aggregate", Schema: "public", Name: "my_agg", Args: "numeric",
			BodyHash: bodyHash, Comment: &oldComment,
		},
	})
	newComment := "updated comment"
	desired := []pipeline.IRObject{
		&ir.Aggregate{
			Schema:  "public",
			Name:    "my_agg",
			Args:    []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
			Body:    body,
			Comment: &newComment,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP AGGREGATE") {
		t.Errorf("unexpected DROP AGGREGATE for comment-only change, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "COMMENT ON AGGREGATE") {
		t.Errorf("expected COMMENT ON AGGREGATE, got: %v", sqlList(ops))
	}
}

func TestDiffAggregateGrantAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	body := "CREATE AGGREGATE public.my_agg(numeric) (SFUNC = numeric_add, STYPE = numeric)"
	bodyHash := fmt.Sprintf("%x", sha256Sum(body))
	_ = snap.SetObject(`public.my_agg(numeric)`, &snapshot.SnapObject{
		Kind: "aggregate",
		Opaque: &snapshot.SnapOpaque{
			Kind: "aggregate", Schema: "public", Name: "my_agg", Args: "numeric", BodyHash: bodyHash,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Aggregate{
			Schema: "public",
			Name:   "my_agg",
			Args:   []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
			Body:   body,
			Grants: []ir.Grant{{Privileges: []string{"EXECUTE"}, Roles: []string{"analyst"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP AGGREGATE") {
		t.Errorf("unexpected DROP AGGREGATE for grant-only change, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "GRANT EXECUTE ON FUNCTION") {
		t.Errorf("expected GRANT EXECUTE ON FUNCTION, got: %v", sqlList(ops))
	}
}

// ── AGGREGATE Revocations ──────────────────────────────────────────────────
// Regression guard mirroring TestDiffCreateProcedureEmitsRevocation and
// friends: ir.Aggregate had no Revocations field at all, so a declared
// REVOCATIONS block was parsed but silently dropped everywhere downstream
// (build, snapshot, diff, dump) — only the GRANT half ever took effect.

func TestDiffCreateAggregateEmitsRevocation(t *testing.T) {
	d := New()
	body := "CREATE AGGREGATE public.my_agg(numeric) (SFUNC = numeric_add, STYPE = numeric)"
	desired := []pipeline.IRObject{
		&ir.Aggregate{
			Schema:      "public",
			Name:        "my_agg",
			Args:        []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
			Body:        body,
			Revocations: []ir.Revocation{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE EXECUTE ON FUNCTION") {
		t.Errorf("expected REVOKE EXECUTE ON FUNCTION at create time, got: %v", sqlList(ops))
	}
	if containsSQL(ops, `"PUBLIC"`) {
		t.Errorf("PUBLIC must not be quoted, got: %v", sqlList(ops))
	}
}

func TestDiffAggregateRevocationAdded(t *testing.T) {
	d := New()
	body := "CREATE AGGREGATE public.my_agg(numeric) (SFUNC = numeric_add, STYPE = numeric)"
	bodyHash := fmt.Sprintf("%x", sha256Sum(body))
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject(`public.my_agg(numeric)`, &snapshot.SnapObject{
		Kind: "aggregate",
		Opaque: &snapshot.SnapOpaque{
			Kind: "aggregate", Schema: "public", Name: "my_agg", Args: "numeric", BodyHash: bodyHash,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Aggregate{
			Schema:      "public",
			Name:        "my_agg",
			Args:        []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
			Body:        body,
			Revocations: []ir.Revocation{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, o := range ops {
		if strings.Contains(o.SQL(), "REVOKE EXECUTE ON FUNCTION") {
			found = true
			if o.Safety() != pipeline.Caution {
				t.Errorf("explicit revocation safety = %v, want Caution: %s", o.Safety(), o.SQL())
			}
		}
	}
	if !found {
		t.Errorf("expected REVOKE EXECUTE ON FUNCTION, got: %v", sqlList(ops))
	}
}

func TestDiffAggregateRevocationRemoved(t *testing.T) {
	d := New()
	body := "CREATE AGGREGATE public.my_agg(numeric) (SFUNC = numeric_add, STYPE = numeric)"
	bodyHash := fmt.Sprintf("%x", sha256Sum(body))
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject(`public.my_agg(numeric)`, &snapshot.SnapObject{
		Kind: "aggregate",
		Opaque: &snapshot.SnapOpaque{
			Kind: "aggregate", Schema: "public", Name: "my_agg", Args: "numeric", BodyHash: bodyHash,
			Revocations: []snapshot.SnapGrant{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Aggregate{
			Schema: "public",
			Name:   "my_agg",
			Args:   []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
			Body:   body,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "GRANT EXECUTE ON FUNCTION") {
		t.Errorf("expected GRANT EXECUTE ON FUNCTION to restore the revoked privilege, got: %v", sqlList(ops))
	}
	if containsSQL(ops, "REVOKE") {
		t.Errorf("must not also emit REVOKE when the revocation itself was removed: %v", sqlList(ops))
	}
}

func TestDiffAggregateRevocationUnchangedIsNoop(t *testing.T) {
	d := New()
	body := "CREATE AGGREGATE public.my_agg(numeric) (SFUNC = numeric_add, STYPE = numeric)"
	bodyHash := fmt.Sprintf("%x", sha256Sum(body))
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject(`public.my_agg(numeric)`, &snapshot.SnapObject{
		Kind: "aggregate",
		Opaque: &snapshot.SnapOpaque{
			Kind: "aggregate", Schema: "public", Name: "my_agg", Args: "numeric", BodyHash: bodyHash,
			Revocations: []snapshot.SnapGrant{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Aggregate{
			Schema:      "public",
			Name:        "my_agg",
			Args:        []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
			Body:        body,
			Revocations: []ir.Revocation{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "GRANT") || containsSQL(ops, "REVOKE") {
		t.Errorf("expected no GRANT/REVOKE for unchanged revocation, got: %v", sqlList(ops))
	}
}

func TestDiffAggregateUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	body := "CREATE AGGREGATE public.my_agg(numeric) (SFUNC = numeric_add, STYPE = numeric)"
	bodyHash := fmt.Sprintf("%x", sha256Sum(body))
	_ = snap.SetObject(`public.my_agg(numeric)`, &snapshot.SnapObject{
		Kind: "aggregate",
		Opaque: &snapshot.SnapOpaque{
			Kind: "aggregate", Schema: "public", Name: "my_agg", Args: "numeric", BodyHash: bodyHash,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Aggregate{
			Schema: "public",
			Name:   "my_agg",
			Args:   []ir.FuncArg{{Type: ir.TypeRef{Name: "numeric"}}},
			Body:   body,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for unchanged aggregate, got: %v", sqlList(ops))
	}
}

func TestDiffMaterViewCommentUsesCorrectKind(t *testing.T) {
	d := New()
	comment := "a summary view"
	desired := []pipeline.IRObject{
		&ir.View{
			Schema:       "public",
			Name:         "mv_summary",
			Query:        "SELECT 1",
			Materialized: true,
			Comment:      &comment,
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "COMMENT ON VIEW") {
		t.Errorf("materialized view comment should use COMMENT ON MATERIALIZED VIEW, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "COMMENT ON MATERIALIZED VIEW") {
		t.Errorf("expected COMMENT ON MATERIALIZED VIEW, got: %v", sqlList(ops))
	}
}

// ── Extension diff ────────────────────────────────────────────────────────────

func TestDiffCreateExtension(t *testing.T) {
	d := New()
	schema := "public"
	ver := "1.3"
	desired := []pipeline.IRObject{
		&ir.Extension{Name: "pgcrypto", Schema: &schema, Version: &ver},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "CREATE EXTENSION IF NOT EXISTS") || !containsSQL(ops, `"pgcrypto"`) ||
		!containsSQL(ops, `SCHEMA "public"`) || !containsSQL(ops, "VERSION '1.3'") {
		t.Errorf("expected CREATE EXTENSION with schema+version, got: %v", sqlList(ops))
	}
	if ops[0].Safety() != pipeline.Safe {
		t.Errorf("expected Safe, got %s", ops[0].Safety())
	}
}

func TestDiffExtensionUnchangedIsNoop(t *testing.T) {
	d := New()
	ver := "1.0"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("pgcrypto", &snapshot.SnapObject{
		Kind:      "extension",
		Extension: &snapshot.SnapExtension{Name: "pgcrypto", Version: &ver},
	})
	desired := []pipeline.IRObject{
		&ir.Extension{Name: "pgcrypto", Version: &ver},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for unchanged extension, got: %v", sqlList(ops))
	}
}

func TestDiffExtensionVersionUpdated(t *testing.T) {
	d := New()
	old, new_ := "1.0", "1.1"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("pgcrypto", &snapshot.SnapObject{
		Kind:      "extension",
		Extension: &snapshot.SnapExtension{Name: "pgcrypto", Version: &old},
	})
	desired := []pipeline.IRObject{
		&ir.Extension{Name: "pgcrypto", Version: &new_},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "ALTER EXTENSION") || !containsSQL(ops, "UPDATE TO") || !containsSQL(ops, "'1.1'") {
		t.Errorf("expected ALTER EXTENSION ... UPDATE TO '1.1', got: %v", sqlList(ops))
	}
	if ops[0].Safety() != pipeline.Safe {
		t.Errorf("expected Safe, got %s", ops[0].Safety())
	}
}

// ── Extension Comment (ir.Extension previously had no Comment field at all;
// COMMENT ON EXTENSION is valid, real PostgreSQL syntax but was silently
// dropped by build/snapshot/diff/dump) ─────────────────────────────────────

func TestDiffCreateExtensionEmitsComment(t *testing.T) {
	d := New()
	comment := "crypto functions"
	desired := []pipeline.IRObject{
		&ir.Extension{Name: "pgcrypto", Comment: &comment},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "COMMENT ON EXTENSION") || !containsSQL(ops, "'crypto functions'") {
		t.Errorf("expected COMMENT ON EXTENSION at create time, got: %v", sqlList(ops))
	}
}

func TestDiffExtensionCommentAdded(t *testing.T) {
	d := New()
	ver := "1.0"
	comment := "crypto functions"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("pgcrypto", &snapshot.SnapObject{
		Kind:      "extension",
		Extension: &snapshot.SnapExtension{Name: "pgcrypto", Version: &ver},
	})
	desired := []pipeline.IRObject{
		&ir.Extension{Name: "pgcrypto", Version: &ver, Comment: &comment},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "COMMENT ON EXTENSION") || !containsSQL(ops, "'crypto functions'") {
		t.Errorf("expected COMMENT ON EXTENSION, got: %v", sqlList(ops))
	}
}

func TestDiffExtensionCommentRemoved(t *testing.T) {
	d := New()
	ver := "1.0"
	comment := "crypto functions"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("pgcrypto", &snapshot.SnapObject{
		Kind:      "extension",
		Extension: &snapshot.SnapExtension{Name: "pgcrypto", Version: &ver, Comment: &comment},
	})
	desired := []pipeline.IRObject{
		&ir.Extension{Name: "pgcrypto", Version: &ver},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "COMMENT ON EXTENSION") || !containsSQL(ops, "IS NULL") {
		t.Errorf("expected COMMENT ON EXTENSION ... IS NULL, got: %v", sqlList(ops))
	}
}

// ── Sequence diff ─────────────────────────────────────────────────────────────

func TestDiffCreateSequence(t *testing.T) {
	d := New()
	inc, start, cache := int64(2), int64(100), int64(10)
	cyc := true
	desired := []pipeline.IRObject{
		&ir.Sequence{Schema: "public", Name: "seq_id", IncrementBy: &inc, StartValue: &start, Cache: &cache, Cycle: &cyc},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "CREATE SEQUENCE IF NOT EXISTS") || !containsSQL(ops, "INCREMENT BY 2") ||
		!containsSQL(ops, "START WITH 100") || !containsSQL(ops, "CACHE 10") || !containsSQL(ops, "CYCLE") {
		t.Errorf("expected CREATE SEQUENCE with all params + CYCLE, got: %v", sqlList(ops))
	}
}

// TestDiffSequenceCycleChangedWithoutOtherParams is the regression guard for a
// bug found while pushing diff-package unit test coverage: paramsChanged in
// diffSequence originally gated the Cycle comparison on `o.IncrementBy !=
// nil`, so an explicit CYCLE with no other sequence option set (the common
// case — CYCLE is usually the only thing anyone changes on an existing
// sequence) was silently ignored by verify/plan --live. Cycle must be
// compared whenever it was itself explicitly set, independent of any other
// option.
func TestDiffSequenceCycleChangedWithoutOtherParams(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.seq_id", &snapshot.SnapObject{
		Kind:     "sequence",
		Sequence: &snapshot.SnapSequence{Schema: "public", Name: "seq_id", Cycle: false},
	})
	cyc := true
	desired := []pipeline.IRObject{
		&ir.Sequence{Schema: "public", Name: "seq_id", Cycle: &cyc},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "ALTER SEQUENCE") || !containsSQL(ops, "CYCLE") {
		t.Errorf("expected ALTER SEQUENCE ... CYCLE, got: %v", sqlList(ops))
	}
}

// TestDiffSequenceCycleUnspecifiedIsNoop proves the nil-means-unspecified
// semantics: a sequence source that never mentions CYCLE/NO CYCLE must not
// touch an existing sequence's cycle setting either way.
func TestDiffSequenceCycleUnspecifiedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.seq_id", &snapshot.SnapObject{
		Kind:     "sequence",
		Sequence: &snapshot.SnapSequence{Schema: "public", Name: "seq_id", Cycle: true},
	})
	desired := []pipeline.IRObject{
		&ir.Sequence{Schema: "public", Name: "seq_id"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops when CYCLE unspecified in source, got: %v", sqlList(ops))
	}
}

func TestDiffSequenceUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.seq_id", &snapshot.SnapObject{
		Kind:     "sequence",
		Sequence: &snapshot.SnapSequence{Schema: "public", Name: "seq_id"},
	})
	desired := []pipeline.IRObject{
		&ir.Sequence{Schema: "public", Name: "seq_id"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for unchanged sequence, got: %v", sqlList(ops))
	}
}

func TestDiffSequenceCommentAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.seq_id", &snapshot.SnapObject{
		Kind:     "sequence",
		Sequence: &snapshot.SnapSequence{Schema: "public", Name: "seq_id"},
	})
	comment := "order id sequence"
	desired := []pipeline.IRObject{
		&ir.Sequence{Schema: "public", Name: "seq_id", Comment: &comment},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "COMMENT ON SEQUENCE") || !containsSQL(ops, "'order id sequence'") {
		t.Errorf("expected COMMENT ON SEQUENCE with new comment, got: %v", sqlList(ops))
	}
}

func TestDiffSequenceCommentRemoved(t *testing.T) {
	d := New()
	old := "old comment"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.seq_id", &snapshot.SnapObject{
		Kind:     "sequence",
		Sequence: &snapshot.SnapSequence{Schema: "public", Name: "seq_id", Comment: &old},
	})
	desired := []pipeline.IRObject{
		&ir.Sequence{Schema: "public", Name: "seq_id"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "COMMENT ON SEQUENCE") || !containsSQL(ops, "IS NULL") {
		t.Errorf("expected COMMENT ON SEQUENCE ... IS NULL, got: %v", sqlList(ops))
	}
}

// TestDiffCreateSequenceWithOwner and TestDiffSequenceOwnerChanged are the
// regression guard for a bug found reviewing the dump Owner-rendering fix:
// createSequence never emitted ALTER SEQUENCE ... OWNER TO for a brand-new
// sequence, SnapSequence had no Owner field at all, and diffSequence never
// compared it — so a declared sequence Owner was completely inert, for both
// initial creation and subsequent drift, unlike Table/Schema (which both
// genuinely act on Owner). dump had already started rendering `OWNER role;`
// into sequence source (see the render-side fix), which without this fix
// would silently do nothing at either create or apply time.
func TestDiffCreateSequenceWithOwner(t *testing.T) {
	d := New()
	owner := "app_owner"
	desired := []pipeline.IRObject{
		&ir.Sequence{Schema: "public", Name: "seq_id", Owner: &owner},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "CREATE SEQUENCE IF NOT EXISTS") {
		t.Errorf("expected CREATE SEQUENCE, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, `ALTER SEQUENCE "public"."seq_id" OWNER TO "app_owner"`) {
		t.Errorf("expected ALTER SEQUENCE ... OWNER TO for a new sequence, got: %v", sqlList(ops))
	}
}

func TestDiffSequenceOwnerChanged(t *testing.T) {
	d := New()
	oldOwner := "old_owner"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.seq_id", &snapshot.SnapObject{
		Kind:     "sequence",
		Sequence: &snapshot.SnapSequence{Schema: "public", Name: "seq_id", Owner: &oldOwner},
	})
	newOwner := "new_owner"
	desired := []pipeline.IRObject{
		&ir.Sequence{Schema: "public", Name: "seq_id", Owner: &newOwner},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `ALTER SEQUENCE "public"."seq_id" OWNER TO "new_owner"`) {
		t.Errorf("expected ALTER SEQUENCE ... OWNER TO new_owner, got: %v", sqlList(ops))
	}
}

// ── Role diff ─────────────────────────────────────────────────────────────────

func TestDiffCreateRole(t *testing.T) {
	d := New()
	comment := "application role"
	desired := []pipeline.IRObject{
		&ir.Role{Name: "app_role", Comment: &comment},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "CREATE ROLE") || !containsSQL(ops, `"app_role"`) {
		t.Errorf("expected CREATE ROLE, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "COMMENT ON ROLE") || !containsSQL(ops, "'application role'") {
		t.Errorf("expected COMMENT ON ROLE, got: %v", sqlList(ops))
	}
}

func TestDiffRoleUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("app_role", &snapshot.SnapObject{
		Kind: "role",
		Role: &snapshot.SnapRole{Name: "app_role"},
	})
	desired := []pipeline.IRObject{
		&ir.Role{Name: "app_role"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for unchanged role, got: %v", sqlList(ops))
	}
}

func TestDiffRoleCommentAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("app_role", &snapshot.SnapObject{
		Kind: "role",
		Role: &snapshot.SnapRole{Name: "app_role"},
	})
	comment := "application role"
	desired := []pipeline.IRObject{
		&ir.Role{Name: "app_role", Comment: &comment},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "COMMENT ON ROLE") || !containsSQL(ops, "'application role'") {
		t.Errorf("expected COMMENT ON ROLE with new comment, got: %v", sqlList(ops))
	}
}

// ── DefaultPrivileges diffing ─────────────────────────────────────────────────

func TestDiffDefaultPrivilegesCreate(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.DefaultPrivileges{
			ObjectType: "TABLES",
			Grants:     []ir.Grant{{Privileges: []string{"SELECT"}, Roles: []string{"app_readonly"}}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "ALTER DEFAULT PRIVILEGES") || !containsSQL(ops, "GRANT SELECT") || !containsSQL(ops, "app_readonly") {
		t.Errorf("expected ALTER DEFAULT PRIVILEGES GRANT, got: %v", sqlList(ops))
	}
}

func TestDiffDefaultPrivilegesGrantAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	dp := &snapshot.SnapDefaultPrivileges{
		ObjectType: "TABLES",
		Grants:     []snapshot.SnapGrant{{Privileges: []string{"SELECT"}, Roles: []string{"app_readonly"}}},
	}
	_ = snap.SetObject("DEFAULT PRIVILEGES TABLES", &snapshot.SnapObject{
		Kind:              "default_privileges",
		DefaultPrivileges: dp,
	})
	desired := []pipeline.IRObject{
		&ir.DefaultPrivileges{
			ObjectType: "TABLES",
			Grants: []ir.Grant{
				{Privileges: []string{"SELECT"}, Roles: []string{"app_readonly"}},
				{Privileges: []string{"INSERT"}, Roles: []string{"app_writer"}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "GRANT INSERT") || !containsSQL(ops, "app_writer") {
		t.Errorf("expected GRANT for new privilege, got: %v", sqlList(ops))
	}
	if containsSQL(ops, "REVOKE") {
		t.Errorf("expected no REVOKE for unchanged grant, got: %v", sqlList(ops))
	}
}

func TestDiffDefaultPrivilegesGrantRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	dp := &snapshot.SnapDefaultPrivileges{
		ObjectType: "TABLES",
		Grants: []snapshot.SnapGrant{
			{Privileges: []string{"SELECT"}, Roles: []string{"app_readonly"}},
			{Privileges: []string{"INSERT"}, Roles: []string{"app_writer"}},
		},
	}
	_ = snap.SetObject("DEFAULT PRIVILEGES TABLES", &snapshot.SnapObject{
		Kind:              "default_privileges",
		DefaultPrivileges: dp,
	})
	desired := []pipeline.IRObject{
		&ir.DefaultPrivileges{
			ObjectType: "TABLES",
			Grants:     []ir.Grant{{Privileges: []string{"SELECT"}, Roles: []string{"app_readonly"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE INSERT") || !containsSQL(ops, "app_writer") {
		t.Errorf("expected REVOKE for removed grant, got: %v", sqlList(ops))
	}
	if containsSQL(ops, "REVOKE SELECT") {
		t.Errorf("unchanged SELECT grant should not be revoked, got: %v", sqlList(ops))
	}
}

func TestDiffDefaultPrivilegesUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	dp := &snapshot.SnapDefaultPrivileges{
		ObjectType: "TABLES",
		Grants:     []snapshot.SnapGrant{{Privileges: []string{"SELECT"}, Roles: []string{"app_readonly"}}},
	}
	_ = snap.SetObject("DEFAULT PRIVILEGES TABLES", &snapshot.SnapObject{
		Kind:              "default_privileges",
		DefaultPrivileges: dp,
	})
	desired := []pipeline.IRObject{
		&ir.DefaultPrivileges{
			ObjectType: "TABLES",
			Grants:     []ir.Grant{{Privileges: []string{"SELECT"}, Roles: []string{"app_readonly"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for unchanged default privileges, got: %v", sqlList(ops))
	}
}

func TestDiffDefaultPrivilegesDropEmitsRevoke(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	dp := &snapshot.SnapDefaultPrivileges{
		ObjectType: "TABLES",
		Grants:     []snapshot.SnapGrant{{Privileges: []string{"SELECT"}, Roles: []string{"app_readonly"}}},
	}
	_ = snap.SetObject("DEFAULT PRIVILEGES", &snapshot.SnapObject{
		Kind:              "default_privileges",
		DefaultPrivileges: dp,
	})
	// desired has no DefaultPrivileges — it was removed
	ops, err := d.Diff([]pipeline.IRObject{}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE SELECT") || !containsSQL(ops, "app_readonly") {
		t.Errorf("expected REVOKE when default privileges removed, got: %v", sqlList(ops))
	}
}

func TestDiffDefaultPrivilegesForRole(t *testing.T) {
	d := New()
	forRole := "dba"
	desired := []pipeline.IRObject{
		&ir.DefaultPrivileges{
			ForRole:    &forRole,
			ObjectType: "TABLES",
			Grants:     []ir.Grant{{Privileges: []string{"ALL"}, Roles: []string{"app_service"}}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "FOR ROLE") || !containsSQL(ops, "dba") {
		t.Errorf("expected FOR ROLE in DEFAULT PRIVILEGES, got: %v", sqlList(ops))
	}
}

// ── Operator class/family DROP access method (regression: hardcoded btree) ─────

func TestDiffDropOperatorClassUsesRecordedAccessMethod(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.my_ops", &snapshot.SnapObject{
		Kind: "operator_class",
		Opaque: &snapshot.SnapOpaque{
			Kind: "operator_class", Schema: "public", Name: "my_ops", Using: "gin",
		},
	})
	ops, err := d.Diff(nil, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected drop op")
	}
	sql := ops[0].SQL()
	if !strings.Contains(sql, "DROP OPERATOR CLASS") || !strings.Contains(sql, "USING gin") {
		t.Errorf("expected DROP … USING gin, got: %s", sql)
	}
	if strings.Contains(sql, "USING btree") {
		t.Errorf("must not hardcode btree, got: %s", sql)
	}
}

func TestDiffDropOperatorFamilyUsesRecordedAccessMethod(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.my_family", &snapshot.SnapObject{
		Kind: "operator_family",
		Opaque: &snapshot.SnapOpaque{
			Kind: "operator_family", Schema: "public", Name: "my_family", Using: "gist",
		},
	})
	ops, err := d.Diff(nil, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 || !strings.Contains(ops[0].SQL(), "USING gist") {
		t.Fatalf("expected DROP … USING gist, got: %v", ops)
	}
}

// ── Operator family loose members (RFC §14.4) ─────────────────────────────────

func opFamilyMemberObj(members ...pipeline.OpFamilyMember) *ir.OperatorFamily {
	return &ir.OperatorFamily{
		Schema: "public", Name: "member_fam", AccessMethod: "btree",
		Body:          `CREATE OPERATOR FAMILY "public"."member_fam" USING btree`,
		Reconstructed: true, Members: members,
	}
}

func opFamilyMemberSnap(structured bool, members ...snapshot.SnapOpFamilyMember) *snapshot.SnapObject {
	return &snapshot.SnapObject{Kind: "operator_family", Opaque: &snapshot.SnapOpaque{
		Kind: "operator_family", Schema: "public", Name: "member_fam", Using: "btree",
		OpFamilyMembersStructured: structured, OpFamilyMembers: members,
	}}
}

func TestDiffOpFamilyMemberAddOnly(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.member_fam USING btree FAMILY", opFamilyMemberSnap(true))
	m := pipeline.OpFamilyMember{Number: 1, Name: pipeline.Identifier{Name: "<"}, LeftType: "integer", RightType: "bigint"}
	ops, err := d.Diff([]pipeline.IRObject{opFamilyMemberObj(m)}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Safety() != pipeline.Safe || !strings.Contains(ops[0].SQL(), "ADD OPERATOR 1") {
		t.Fatalf("expected 1 SAFE ADD op, got %v", sqlList(ops))
	}
}

func TestDiffOpFamilyMemberRemoveOnly(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	sm := snapshot.SnapOpFamilyMember{Number: 1, Name: "<", LeftType: "integer", RightType: "bigint"}
	_ = snap.SetObject("public.member_fam USING btree FAMILY", opFamilyMemberSnap(true, sm))
	ops, err := d.Diff([]pipeline.IRObject{opFamilyMemberObj()}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Safety() != pipeline.Destructive || !strings.Contains(ops[0].SQL(), "DROP OPERATOR 1") {
		t.Fatalf("expected 1 DESTRUCTIVE DROP op, got %v", sqlList(ops))
	}
}

// TestDiffOpFamilyMemberInPlaceChange guards the "same slot, different
// operator" case: Key() deliberately excludes operator identity (see its
// doc comment), so a slot whose operator symbol changes must diff as
// DROP-then-ADD, not silently match as unchanged.
func TestDiffOpFamilyMemberInPlaceChange(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	sm := snapshot.SnapOpFamilyMember{Number: 1, Name: "<", LeftType: "integer", RightType: "bigint"}
	_ = snap.SetObject("public.member_fam USING btree FAMILY", opFamilyMemberSnap(true, sm))
	m := pipeline.OpFamilyMember{Number: 1, Name: pipeline.Identifier{Name: "<="}, LeftType: "integer", RightType: "bigint"}
	ops, err := d.Diff([]pipeline.IRObject{opFamilyMemberObj(m)}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 || ops[0].Safety() != pipeline.Destructive || ops[1].Safety() != pipeline.Safe {
		t.Fatalf("expected DROP then ADD, got %v", sqlList(ops))
	}
}

// TestDiffOpFamilyMemberUnqualifiedOperatorMatchesPgCatalog is a live-bug
// regression guard: source commonly writes a bare "<" (no schema), while
// introspection always returns the operator's real, fully qualified schema
// (pg_catalog for a built-in) — a raw Identifier comparison flagged every
// unqualified built-in operator as changed on every apply, even with zero
// actual difference (caught by TestRoundtripOpFamilyLooseMembers, an
// integration test, before this unit test existed).
func TestDiffOpFamilyMemberUnqualifiedOperatorMatchesPgCatalog(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	sm := snapshot.SnapOpFamilyMember{Number: 1, NameSchema: "pg_catalog", Name: "<", LeftType: "integer", RightType: "bigint"}
	_ = snap.SetObject("public.member_fam USING btree FAMILY", opFamilyMemberSnap(true, sm))
	m := pipeline.OpFamilyMember{Number: 1, Name: pipeline.Identifier{Name: "<"}, LeftType: "integer", RightType: "bigint"}
	ops, err := d.Diff([]pipeline.IRObject{opFamilyMemberObj(m)}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("expected zero ops (unqualified '<' must match pg_catalog.<), got %v", sqlList(ops))
	}
}

func TestDiffOpFamilyMemberNoChange(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	sm := snapshot.SnapOpFamilyMember{Number: 1, Name: "<", LeftType: "integer", RightType: "bigint"}
	_ = snap.SetObject("public.member_fam USING btree FAMILY", opFamilyMemberSnap(true, sm))
	m := pipeline.OpFamilyMember{Number: 1, Name: pipeline.Identifier{Name: "<"}, LeftType: "integer", RightType: "bigint"}
	ops, err := d.Diff([]pipeline.IRObject{opFamilyMemberObj(m)}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("expected zero ops, got %v", sqlList(ops))
	}
}

// TestDiffOpFamilyMemberStaleSnapshotNoOp guards the mandatory
// OpFamilyMembersStructured sentinel: a snapshot written before this
// feature existed must never be treated as "genuinely zero members" — that
// would emit an ADD for a member PostgreSQL may already have, which
// actually errors live (no "ADD ... IF NOT EXISTS").
func TestDiffOpFamilyMemberStaleSnapshotNoOp(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.member_fam USING btree FAMILY", opFamilyMemberSnap(false))
	m := pipeline.OpFamilyMember{Number: 1, Name: pipeline.Identifier{Name: "<"}, LeftType: "integer", RightType: "bigint"}
	ops, err := d.Diff([]pipeline.IRObject{opFamilyMemberObj(m)}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("expected zero ops for stale (unstructured) snapshot, got %v", sqlList(ops))
	}
}

// TestDiffOpFamilyMemberDeterministicOrder guards diffOpFamilyMembers's
// sorted-key iteration — unlike diffTSConfigMappings's plain map iteration,
// this must produce identical plan text across repeated runs.
func TestDiffOpFamilyMemberDeterministicOrder(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.member_fam USING btree FAMILY", opFamilyMemberSnap(true))
	members := []pipeline.OpFamilyMember{
		{Number: 5, Name: pipeline.Identifier{Name: ">"}, LeftType: "integer", RightType: "bigint"},
		{Number: 1, Name: pipeline.Identifier{Name: "<"}, LeftType: "integer", RightType: "bigint"},
		{IsFunction: true, Number: 1, Name: pipeline.Identifier{Name: "cmp"}, LeftType: "integer", RightType: "bigint", FuncArgs: []string{"integer", "bigint"}},
	}
	var first string
	for i := range 5 {
		ops, err := d.Diff([]pipeline.IRObject{opFamilyMemberObj(members...)}, snap)
		if err != nil {
			t.Fatal(err)
		}
		var sb strings.Builder
		for _, op := range ops {
			sb.WriteString(op.SQL())
		}
		if i == 0 {
			first = sb.String()
		} else if sb.String() != first {
			t.Fatalf("run %d produced different plan text:\n%s\nvs first run:\n%s", i, sb.String(), first)
		}
	}
}

// Legacy snapshots predate the access-method field; DROP falls back to btree.
func TestDiffDropOperatorClassLegacyFallsBackToBtree(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.my_ops", &snapshot.SnapObject{
		Kind: "operator_class",
		Opaque: &snapshot.SnapOpaque{
			Kind: "operator_class", Schema: "public", Name: "my_ops", // no Using
		},
	})
	ops, err := d.Diff(nil, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 || !strings.Contains(ops[0].SQL(), "USING btree") {
		t.Fatalf("expected fallback USING btree, got: %v", ops)
	}
}

// An introspected opaque object (used as the baseline by `plan --live`) may have
// no body hash; comparing a real body against an empty snapshot hash must NOT
// report spurious drift.
// TestDiffOpaqueNoSpuriousDriftWhenSnapHashEmpty predates Collation's
// structured diffing (RFC §14.2): originally asserted exactly 0 ops for a
// snap.BodyHash == "" scenario. diffCollation's own stale-snapshot guard
// (CollationStructured == false here, since this snap predates the field)
// now deliberately emits one harmless refresh-metadata comment op instead
// of staying silently stale forever — same self-healing pattern as
// DOMAIN/Tablespace/Cast/etc. (see diffCollation's doc comment) — so the
// real invariant this test protects (no spurious DESTRUCTIVE drift) is
// checked directly rather than asserting a literal op count.
func TestDiffOpaqueNoSpuriousDriftWhenSnapHashEmpty(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.my_coll", &snapshot.SnapObject{
		Kind: "collation",
		Opaque: &snapshot.SnapOpaque{
			Kind: "collation", Schema: "public", Name: "my_coll", // BodyHash == ""
		},
	})
	desired := []pipeline.IRObject{
		&ir.Collation{Schema: "public", Name: "my_coll", Body: "CREATE COLLATION public.my_coll (LOCALE = 'C')"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Safety() == pipeline.Destructive {
			t.Fatalf("want no spurious destructive drift, got: %v", sqlList(ops))
		}
	}
}

// A FOR PUBLIC user mapping has an empty user; the DROP must emit FOR PUBLIC,
// not FOR "" (a zero-length identifier that aborts the migration).
func TestDiffDropUserMappingForPublic(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("@dummy_srv", &snapshot.SnapObject{
		Kind: "user_mapping",
		Opaque: &snapshot.SnapOpaque{
			Kind: "user_mapping", Name: "@dummy_srv",
		},
	})
	ops, err := d.Diff(nil, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected drop op")
	}
	sql := ops[0].SQL()
	if !strings.Contains(sql, "FOR PUBLIC") {
		t.Errorf("expected FOR PUBLIC, got: %s", sql)
	}
	if strings.Contains(sql, `FOR ""`) {
		t.Errorf("must not emit zero-length identifier, got: %s", sql)
	}
}

// Collation/statistics bodies are catalog reconstructions on the live side and
// won't byte-match equivalent hand-written source; against a reconstructed
// baseline (plan --live) the differ must not report spurious "body changed"
// drift (regression: capturing Body activated this).
// TestDiffCollationNoSpuriousBodyDrift is the actual regression guard for
// the risk diffCollation's design was built to avoid (see its doc
// comment): LOCALE and LC_COLLATE/LC_CTYPE are two different DPG source
// spellings that PostgreSQL resolves to the identical collcollate/collctype
// pair (confirmed live) — Collate/Ctype are populated with those REAL
// resolved values on both sides (not left as Go zero values, which would
// make this test pass for the wrong reason — both sides merely
// "unpopulated," not "genuinely equivalent").
func TestDiffCollationNoSpuriousBodyDrift(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	// Baseline as introspection produces it (qualified, LOCALE form; Reconstructed).
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.Collation{
			Schema: "public", Name: "c", Provider: "c", Collate: strPtr("C"), Ctype: strPtr("C"), Deterministic: true,
			Body: "CREATE COLLATION public.c (LOCALE = 'C')", Reconstructed: true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	// Desired from hand-written source (unqualified, LC_* form) — resolves
	// to the same Collate/Ctype PostgreSQL would actually store.
	desired := []pipeline.IRObject{
		&ir.Collation{
			Schema: "public", Name: "c", Provider: "c", Collate: strPtr("C"), Ctype: strPtr("C"), Deterministic: true,
			Body: "CREATE COLLATION c (LC_COLLATE = 'C', LC_CTYPE = 'C')",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("spurious drift for equivalent collation spelling: %d ops: %v", len(ops), ops)
	}
}

// TestCommentOnOpaqueSQL guards a real bug found live-testing a demo
// project: PostgreSQL genuinely supports COMMENT ON for all 14 of these
// opaque kinds (confirmed via \h COMMENT against a real server), but the
// blockparser's generic { COMMENT '...'; } was silently discarded for
// every one of them — 9 had no Comment field at all, and the other 5
// (tablespace/fdw/server/ts_config/ts_dict) captured it but never emitted
// it anywhere. Table-driven across all 14 kinds, exercising every distinct
// identity shape dropObject already established: plain quoteIdent
// (tablespace/fdw/server/event_trigger), qualIdent (collation/statistics/
// the 4 TS kinds), and the 3 kinds with non-trivial COMMENT ON syntax
// (operator's operand-type suffix, operator class/family's USING method,
// cast's parenthesized source/target pair — none of which are simple
// identifiers).
func TestCommentOnOpaqueSQL(t *testing.T) {
	comment := "a comment"
	cases := []struct {
		name                               string
		kind, schema, objName, args, using string
		want                               string
	}{
		{"tablespace", "tablespace", "", "fast_ssd", "", "", `COMMENT ON TABLESPACE "fast_ssd" IS 'a comment';`},
		{"fdw", "fdw", "", "my_fdw", "", "", `COMMENT ON FOREIGN DATA WRAPPER "my_fdw" IS 'a comment';`},
		{"server", "server", "", "my_srv", "", "", `COMMENT ON SERVER "my_srv" IS 'a comment';`},
		{"publication", "publication", "", "order_changes", "", "", `COMMENT ON PUBLICATION "order_changes" IS 'a comment';`},
		{"event_trigger", "event_trigger", "", "log_ddl", "", "", `COMMENT ON EVENT TRIGGER "log_ddl" IS 'a comment';`},
		{"collation", "collation", "public", "case_insensitive", "", "", `COMMENT ON COLLATION "public"."case_insensitive" IS 'a comment';`},
		{"operator", "operator", "public", "<<<", "order_status, order_status", "", `COMMENT ON OPERATOR "public".<<<(order_status, order_status) IS 'a comment';`},
		{"operator_class", "operator_class", "public", "text_ci_ops", "", "btree", `COMMENT ON OPERATOR CLASS "public"."text_ci_ops" USING btree IS 'a comment';`},
		{"operator_family", "operator_family", "public", "text_ci_ops", "", "btree", `COMMENT ON OPERATOR FAMILY "public"."text_ci_ops" USING btree IS 'a comment';`},
		{"cast", "cast", "", "order_status->integer", "", "", `COMMENT ON CAST (order_status AS integer) IS 'a comment';`},
		{"statistics", "statistics", "public", "orders_stats", "", "", `COMMENT ON STATISTICS "public"."orders_stats" IS 'a comment';`},
		{"ts_config", "ts_config", "public", "my_cfg", "", "", `COMMENT ON TEXT SEARCH CONFIGURATION "public"."my_cfg" IS 'a comment';`},
		{"ts_dict", "ts_dict", "public", "my_dict", "", "", `COMMENT ON TEXT SEARCH DICTIONARY "public"."my_dict" IS 'a comment';`},
		{"ts_parser", "ts_parser", "public", "my_prs", "", "", `COMMENT ON TEXT SEARCH PARSER "public"."my_prs" IS 'a comment';`},
		{"ts_template", "ts_template", "public", "my_tmpl", "", "", `COMMENT ON TEXT SEARCH TEMPLATE "public"."my_tmpl" IS 'a comment';`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := commentOnOpaqueSQL(tc.kind, tc.schema, tc.objName, tc.args, tc.using, &comment)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCommentOnOpaqueSQLNull guards the comment-removal case (comment set
// to nil emits IS NULL, not an empty/quoted string).
func TestCommentOnOpaqueSQLNull(t *testing.T) {
	got := commentOnOpaqueSQL("statistics", "public", "orders_stats", "", "", nil)
	want := `COMMENT ON STATISTICS "public"."orders_stats" IS NULL;`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestCommentOnOpaqueSQLUnknownKind guards user_mapping and subscription
// (both genuinely have no Comment field/COMMENT ON support at the DPG
// level) and any other unrecognized kind: must return "", not a malformed
// statement — appendCommentOp relies on this to silently skip kinds that
// don't support COMMENT ON at all. Publication used to be the canonical
// example here (it had no Comment field), until it was found live-testing
// a demo project that real PostgreSQL genuinely supports
// COMMENT ON PUBLICATION (confirmed via \h COMMENT) — see
// TestCommentOnOpaqueSQL's "publication" case for its now-real support.
func TestCommentOnOpaqueSQLUnknownKind(t *testing.T) {
	comment := "x"
	if got := commentOnOpaqueSQL("user_mapping", "", "alice@my_srv", "", "", &comment); got != "" {
		t.Errorf("got %q, want empty string for user_mapping (no Comment field)", got)
	}
	if got := commentOnOpaqueSQL("nonexistent_kind", "", "whatever", "", "", &comment); got != "" {
		t.Errorf("got %q, want empty string for a genuinely unrecognized kind", got)
	}
}

func TestDiffStatisticsNoSpuriousBodyDrift(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.StatisticsObject{Schema: "public", Name: "st", Body: "CREATE STATISTICS public.st ON a, b FROM st", Reconstructed: true},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{Schema: "public", Name: "st", Body: "CREATE STATISTICS public.st ON a, b FROM public.st"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("spurious drift for equivalent statistics spelling: %d ops: %v", len(ops), ops)
	}
}

// TestCreateStatisticsEmitsCommentAfterCreate guards the CREATE-time half of
// the comment fix through the real Diff() pipeline (not just the SQL-string
// helper): a brand-new opaque object with Comment set must get both the
// CREATE and a following COMMENT ON, in that order (COMMENT ON a
// not-yet-existing object would itself error).
// TestCreatePublicationEmitsCommentAfterCreate and
// TestDiffPublicationCommentOnlyChangeNoDestroy guard the real gap found
// live-testing a demo project: ir.Publication had no Comment field at all,
// despite real PostgreSQL genuinely supporting COMMENT ON PUBLICATION
// (confirmed live via \h COMMENT) — Publication was excluded from the
// original 14-kind Comment/Grant fix on the mistaken assumption that it
// didn't apply.
func TestCreatePublicationEmitsCommentAfterCreate(t *testing.T) {
	d := New()
	comment := "large order inserts"
	desired := []pipeline.IRObject{
		&ir.Publication{
			Name:    "order_changes",
			Body:    "CREATE PUBLICATION order_changes FOR ALL TABLES",
			Comment: &comment,
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (CREATE + COMMENT), got %d: %v", len(ops), sqlList(ops))
	}
	if !strings.Contains(ops[0].SQL(), "CREATE PUBLICATION") {
		t.Errorf("expected CREATE PUBLICATION first, got: %s", ops[0].SQL())
	}
	want := `COMMENT ON PUBLICATION "order_changes" IS 'large order inserts';`
	if ops[1].SQL() != want {
		t.Errorf("got %q, want %q", ops[1].SQL(), want)
	}
}

func TestDiffPublicationCommentOnlyChangeNoDestroy(t *testing.T) {
	d := New()
	oldComment := "old comment"
	newComment := "new comment"
	body := "CREATE PUBLICATION order_changes FOR ALL TABLES"
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.Publication{Name: "order_changes", Body: body, Comment: &oldComment},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.Publication{Name: "order_changes", Body: body, Comment: &newComment},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 op (COMMENT ON only), got %d: %v", len(ops), sqlList(ops))
	}
	want := `COMMENT ON PUBLICATION "order_changes" IS 'new comment';`
	if ops[0].SQL() != want {
		t.Errorf("got %q, want %q", ops[0].SQL(), want)
	}
}

func TestCreateStatisticsEmitsCommentAfterCreate(t *testing.T) {
	d := New()
	comment := "order status correlation"
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{
			Schema: "public", Name: "orders_stats",
			Body:    "CREATE STATISTICS public.orders_stats ON a, b FROM orders",
			Comment: &comment,
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (CREATE + COMMENT), got %d: %v", len(ops), sqlList(ops))
	}
	if !strings.Contains(ops[0].SQL(), "CREATE STATISTICS") {
		t.Errorf("expected CREATE STATISTICS first, got: %s", ops[0].SQL())
	}
	want := `COMMENT ON STATISTICS "public"."orders_stats" IS 'order status correlation';`
	if ops[1].SQL() != want {
		t.Errorf("got %q, want %q", ops[1].SQL(), want)
	}
}

// TestDiffOpaqueCommentOnlyChangeNoDestroy guards the UPDATE-time half: a
// comment-only edit (body unchanged) must emit a bare COMMENT ON, never the
// DROP+CREATE a real body change requires — needlessly dropping and
// recreating a STATISTICS object (or worse, a COLLATION/OPERATOR CLASS)
// just to change its comment would be actively destructive for no reason.
func TestDiffOpaqueCommentOnlyChangeNoDestroy(t *testing.T) {
	d := New()
	oldComment := "old comment"
	newComment := "new comment"
	body := "CREATE STATISTICS public.orders_stats ON a, b FROM orders"
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.StatisticsObject{Schema: "public", Name: "orders_stats", Body: body, Comment: &oldComment},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{Schema: "public", Name: "orders_stats", Body: body, Comment: &newComment},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 op (COMMENT ON only), got %d: %v", len(ops), sqlList(ops))
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "DROP") || strings.Contains(o.SQL(), "CREATE STATISTICS") {
			t.Errorf("comment-only change must not DROP/CREATE, got: %s", o.SQL())
		}
	}
	want := `COMMENT ON STATISTICS "public"."orders_stats" IS 'new comment';`
	if ops[0].SQL() != want {
		t.Errorf("got %q, want %q", ops[0].SQL(), want)
	}
}

// TestDiffOpaqueCommentRemovalEmitsNull guards removing a comment entirely
// (desired.Comment nil, snapshot had one set) — must emit IS NULL, and must
// not be silently skipped just because the new value is nil.
func TestDiffOpaqueCommentRemovalEmitsNull(t *testing.T) {
	d := New()
	oldComment := "old comment"
	body := "CREATE STATISTICS public.orders_stats ON a, b FROM orders"
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.StatisticsObject{Schema: "public", Name: "orders_stats", Body: body, Comment: &oldComment},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{Schema: "public", Name: "orders_stats", Body: body}, // Comment nil
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 op, got %d: %v", len(ops), sqlList(ops))
	}
	want := `COMMENT ON STATISTICS "public"."orders_stats" IS NULL;`
	if ops[0].SQL() != want {
		t.Errorf("got %q, want %q", ops[0].SQL(), want)
	}
}

// TestCreateOpaqueSchemaScopedGetsSearchPath guards a real bug found
// live-testing a demo project: opaque schema-scoped kinds (COLLATION,
// OPERATOR, OPERATOR CLASS/FAMILY, STATISTICS, the 4 TEXT SEARCH kinds)
// render their Body as deparsed from source, which only carries a
// schema-qualified name if the user wrote one explicitly — DPG's own
// tracked Schema (from directory placement or an enclosing SCHEMA { }
// block) was never injected. Confirmed live: a STATISTICS object declared
// under a non-public schema was created in `public` instead, silently
// landing in the wrong place. Fixed via SET LOCAL search_path (the same
// technique pg_dump itself uses) rather than rewriting each of the 9
// differently-shaped CREATE statements to inject a qualified name.
func TestCreateOpaqueSchemaScopedGetsSearchPath(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{
			Schema: "billing", Name: "billing_stats",
			Body: "CREATE STATISTICS billing_stats (dependencies) ON status, amount FROM invoices",
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range ops {
		if strings.Contains(o.SQL(), "CREATE STATISTICS") {
			found = true
			if !strings.Contains(o.SQL(), `SET LOCAL search_path = "billing", public;`) {
				t.Errorf("expected SET LOCAL search_path before the CREATE, got: %s", o.SQL())
			}
		}
	}
	if !found {
		t.Fatal("expected a CREATE STATISTICS op")
	}
}

// TestCreateOpaquePublicSchemaNoSearchPath guards the common case: an
// object declared in the default "public" schema must not get a redundant
// SET LOCAL, keeping generated migrations for the overwhelmingly common
// case unchanged from before this fix.
func TestCreateOpaquePublicSchemaNoSearchPath(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{
			Schema: "public", Name: "st",
			Body: "CREATE STATISTICS st ON a, b FROM t",
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "search_path") {
			t.Errorf("did not expect a SET LOCAL search_path for the public schema, got: %s", o.SQL())
		}
	}
}

// TestCreateOpaqueNonSchemaScopedKindNoSearchPath guards CAST specifically:
// unlike the other 9 createOpaque-routed kinds, it has no schema concept at
// all in PostgreSQL (identified purely by its source/target type pair), so
// it must never get a SET LOCAL regardless of anything resembling a schema.
func TestCreateOpaqueNonSchemaScopedKindNoSearchPath(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Cast{
			SourceType: ir.TypeRef{Name: "int4"}, TargetType: ir.TypeRef{Name: "bool"},
			Body: "CREATE CAST (int4 AS bool) WITH INOUT AS IMPLICIT",
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "search_path") {
			t.Errorf("CAST has no schema concept, did not expect SET LOCAL search_path, got: %s", o.SQL())
		}
	}
}

// OFFLINE plan/apply: both sides source-derived (Reconstructed=false). A genuine
// edit to a collation/publication body MUST be detected (regression guard: the
// over-broad fix silently dropped this).
func TestDiffCollationOfflineDetectsEdit(t *testing.T) {
	d := New()
	cLocale, posixLocale := "C", "POSIX"
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.Collation{Schema: "public", Name: "c", Provider: "c", Collate: &cLocale, Ctype: &cLocale, Deterministic: true, Body: "CREATE COLLATION public.c (LOCALE = 'C')"},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.Collation{Schema: "public", Name: "c", Provider: "c", Collate: &posixLocale, Ctype: &posixLocale, Deterministic: true, Body: "CREATE COLLATION public.c (LOCALE = 'POSIX')"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("offline: genuine collation body edit must be detected, got 0 ops")
	}
}

func TestDiffPublicationOfflineDetectsEdit(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.Publication{Name: "p", AllTables: true, Insert: true, Update: true, Delete: true, Truncate: true, Body: "CREATE PUBLICATION p FOR ALL TABLES"},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.Publication{
			Name: "p", Tables: []ir.PublicationTableRef{{Schema: "public", Name: "t"}},
			Insert: true, Update: true, Delete: true, Truncate: true,
			Body: "CREATE PUBLICATION p FOR TABLE public.t",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("offline: genuine publication body edit must be detected, got 0 ops")
	}
}

func TestDiffSubscriptionUnchangedIsNoop(t *testing.T) {
	d := New()
	body := "CREATE SUBSCRIPTION sub CONNECTION '{{vault:secret/db#pw}}' PUBLICATION pub"
	comment := "replication for orders"
	sub := &ir.Subscription{Name: "sub", ConnInfo: "{{vault:secret/db#pw}}", Body: body, Comment: &comment}
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{sub}); err != nil {
		t.Fatal(err)
	}
	ops, err := d.Diff([]pipeline.IRObject{sub}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for an unchanged subscription, got %d: %v", len(ops), ops)
	}
}

// TestDiffSubscriptionCommentOnlyChangeIsFieldLevel guards Comment being
// diffed separately from Body: a comment-only edit must emit a plain
// COMMENT ON SUBSCRIPTION, not a structured DROP+CREATE (which would
// needlessly interrupt a working, already-syncing subscription).
func TestDiffSubscriptionCommentOnlyChangeIsFieldLevel(t *testing.T) {
	d := New()
	body := "CREATE SUBSCRIPTION sub CONNECTION 'host=x user=y' PUBLICATION pub"
	oldComment := "old comment"
	newComment := "new comment"
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.Subscription{Name: "sub", ConnInfo: "host=x user=y", Body: body, Comment: &oldComment},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.Subscription{Name: "sub", ConnInfo: "host=x user=y", Body: body, Comment: &newComment},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 op (COMMENT ON SUBSCRIPTION), got %d: %v", len(ops), ops)
	}
	if !strings.Contains(ops[0].SQL(), "COMMENT ON SUBSCRIPTION") || !strings.Contains(ops[0].SQL(), "new comment") {
		t.Errorf("expected a COMMENT ON SUBSCRIPTION op, got: %s", ops[0].SQL())
	}
	if strings.Contains(ops[0].SQL(), "DROP SUBSCRIPTION") {
		t.Error("comment-only change must not drop and recreate the subscription")
	}
}

// ── createSubscription / subscriptionCreateOp ───────────────────────────────────

type fakeDiffResolver struct{ values map[string]string }

func (f *fakeDiffResolver) Resolve(uri string) (string, error) {
	v, ok := f.values[uri]
	if !ok {
		return "", fmt.Errorf("no such secret: %s", uri)
	}
	return v, nil
}

func TestCreateSubscriptionSQLIsAlwaysThePlaceholderForm(t *testing.T) {
	o := &ir.Subscription{
		Name:     "sub",
		ConnInfo: "-",
		Body:     "CREATE SUBSCRIPTION sub CONNECTION '-' PUBLICATION pub",
	}
	ops, err := createSubscription(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if got := ops[0].SQL(); got != "CREATE SUBSCRIPTION sub CONNECTION '-' PUBLICATION pub;" {
		t.Errorf("SQL(): got %q, want the unresolved placeholder form", got)
	}
	if _, ok := ops[0].(pipeline.SecretBearingOp); !ok {
		t.Fatalf("expected %T to implement pipeline.SecretBearingOp", ops[0])
	}
}

func TestCreateSubscriptionExecSQLResolvesWholeValueTemplate(t *testing.T) {
	o := &ir.Subscription{
		Name:     "sub",
		ConnInfo: "{{vault:secret/db#conninfo}}",
		Body:     "CREATE SUBSCRIPTION sub CONNECTION '{{vault:secret/db#conninfo}}' PUBLICATION pub",
	}
	ops, _ := createSubscription(o)
	sb := ops[0].(pipeline.SecretBearingOp)
	resolver := &fakeDiffResolver{values: map[string]string{
		"vault:secret/db#conninfo": "host=real dbname=real user=repl password=s3cr3t",
	}}
	got, err := sb.ExecSQL(resolver)
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE SUBSCRIPTION sub CONNECTION 'host=real dbname=real user=repl password=s3cr3t' PUBLICATION pub;"
	if got != want {
		t.Errorf("ExecSQL: got %q, want %q", got, want)
	}
	// SQL() must be completely unaffected by the ExecSQL call above.
	if got := ops[0].SQL(); got != o.Body+";" {
		t.Errorf("SQL() changed after ExecSQL was called: got %q", got)
	}
}

func TestCreateSubscriptionExecSQLResolvesPartialTemplateInNativeLiteral(t *testing.T) {
	o := &ir.Subscription{
		Name:     "sub",
		ConnInfo: "host=x user=y password={{env:DB_PASS}}",
		Body:     "CREATE SUBSCRIPTION sub CONNECTION 'host=x user=y password={{env:DB_PASS}}' PUBLICATION pub",
	}
	ops, _ := createSubscription(o)
	sb := ops[0].(pipeline.SecretBearingOp)
	resolver := &fakeDiffResolver{values: map[string]string{"env:DB_PASS": "s3cr3t"}}
	got, err := sb.ExecSQL(resolver)
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE SUBSCRIPTION sub CONNECTION 'host=x user=y password=s3cr3t' PUBLICATION pub;"
	if got != want {
		t.Errorf("ExecSQL: got %q, want %q", got, want)
	}
}

func TestCreateSubscriptionExecSQLPlainLiteralNeverCallsResolver(t *testing.T) {
	o := &ir.Subscription{
		Name:     "sub",
		ConnInfo: "host=x user=y password=hunter2",
		Body:     "CREATE SUBSCRIPTION sub CONNECTION 'host=x user=y password=hunter2' PUBLICATION pub",
	}
	ops, _ := createSubscription(o)
	sb := ops[0].(pipeline.SecretBearingOp)
	// A resolver with no entries: if it's called at all for a plain literal
	// with no {{...}}, this must fail loudly rather than silently succeed
	// with an empty value.
	got, err := sb.ExecSQL(&fakeDiffResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if got != o.Body+";" {
		t.Errorf("ExecSQL: got %q, want the literal returned unchanged", got)
	}
}

// TestCreateSubscriptionIsNonTransactional and
// TestDropSubscriptionIsNonTransactional guard two pre-existing bugs found
// live-testing Phase 3 (predating secret-reference support entirely — the
// generic createOpaque/destructiveOp paths every other opaque kind still
// correctly uses were never live-tested for Subscription specifically):
// both CREATE SUBSCRIPTION (WITH (create_slot = true), the default) and
// DROP SUBSCRIPTION error "cannot run inside a transaction block" in real
// PostgreSQL — confirmed live against a real postgres:17 pair. Destructive
// safety must survive the fix for DROP (still gated by --allow-destructive
// in cmd/dpg/apply.go), which is why destructiveManualOp exists rather than
// reusing manualOp (Safety=Manual) for it.
func TestCreateSubscriptionIsNonTransactional(t *testing.T) {
	ops, err := createSubscription(&ir.Subscription{
		Name: "sub", ConnInfo: "-", Body: "CREATE SUBSCRIPTION sub CONNECTION '-' PUBLICATION pub",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ops[0].Transactional() {
		t.Error("CREATE SUBSCRIPTION must be non-transactional (PostgreSQL rejects create_slot=true inside a transaction block)")
	}
}

// TestCreateSubscriptionCommentIsAlsoNonTransactional guards a real ordering
// bug found live-testing: emit.Emit buckets ops purely by Transactional()
// into two separate lists, and the executor runs the whole transactional
// block before any non-transactional op. A transactional create-time COMMENT
// paired with the (necessarily non-transactional) CREATE above would run
// BEFORE it, erroring "subscription does not exist" against a real
// PostgreSQL server — confirmed live before this fix.
func TestCreateSubscriptionCommentIsAlsoNonTransactional(t *testing.T) {
	comment := "replication for orders"
	ops, err := createSubscription(&ir.Subscription{
		Name: "sub", ConnInfo: "-", Body: "CREATE SUBSCRIPTION sub CONNECTION '-' PUBLICATION pub", Comment: &comment,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (CREATE + COMMENT), got %d", len(ops))
	}
	if ops[1].Transactional() {
		t.Error("create-time COMMENT ON SUBSCRIPTION must be non-transactional, matching CREATE's own bucket, or it runs before the subscription exists")
	}
	if !strings.Contains(ops[1].SQL(), "COMMENT ON SUBSCRIPTION") || !strings.Contains(ops[1].SQL(), comment) {
		t.Errorf("unexpected COMMENT op: %s", ops[1].SQL())
	}
}

func TestDropSubscriptionIsNonTransactional(t *testing.T) {
	ops := dropObject(&snapshot.SnapObject{
		Kind: "subscription", Opaque: &snapshot.SnapOpaque{Kind: "subscription", Name: "sub"},
	})
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].Transactional() {
		t.Error("DROP SUBSCRIPTION must be non-transactional (PostgreSQL rejects it inside a transaction block)")
	}
	if ops[0].Safety() != pipeline.Destructive {
		t.Errorf("Safety() = %v, want Destructive (must still require --allow-destructive)", ops[0].Safety())
	}
}

func TestCreateSubscriptionExecSQLResolveErrorPropagates(t *testing.T) {
	o := &ir.Subscription{
		Name:     "sub",
		ConnInfo: "{{vault:secret/missing}}",
		Body:     "CREATE SUBSCRIPTION sub CONNECTION '{{vault:secret/missing}}' PUBLICATION pub",
	}
	ops, _ := createSubscription(o)
	sb := ops[0].(pipeline.SecretBearingOp)
	if _, err := sb.ExecSQL(&fakeDiffResolver{}); err == nil {
		t.Fatal("expected an error when the referenced secret doesn't resolve")
	}
}

// ── Role attributes (RFC §11.1) ──────────────────────────────────────────────

func boolp(v bool) *bool { return &v }
func intp(v int) *int    { return &v }
func strp(v string) *string {
	return &v
}

func TestCreateRoleAllAttributes(t *testing.T) {
	o := &ir.Role{
		Name: "app_service", CanLogin: boolp(true), Superuser: boolp(false),
		CreateDB: boolp(false), CreateRole: boolp(false), Inherit: boolp(true),
		IsReplication: boolp(false), BypassRLS: boolp(false), ConnectionLimit: intp(20),
		Password: strp("hunter2"), ValidUntil: strp("2030-01-01"),
		InRole: []string{"role_a"}, RoleMembers: []string{"role_b"}, AdminRoles: []string{"role_c"},
	}
	ops := createRole(o)
	if len(ops) != 1 {
		t.Fatalf("expected 1 op (no comment set), got %d: %v", len(ops), ops)
	}
	want := `CREATE ROLE "app_service" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 20 PASSWORD 'hunter2' VALID UNTIL '2030-01-01' IN ROLE "role_a" ROLE "role_b" ADMIN "role_c";`
	if got := ops[0].SQL(); got != want {
		t.Errorf("SQL():\n got  %q\n want %q", got, want)
	}
}

func TestCreateRoleWithPasswordIsSecretBearingOp(t *testing.T) {
	o := &ir.Role{Name: "svc", Password: strp("{{vault:secret/roles/svc#pw}}")}
	ops := createRole(o)
	sb, ok := ops[0].(pipeline.SecretBearingOp)
	if !ok {
		t.Fatalf("expected %T to implement pipeline.SecretBearingOp", ops[0])
	}
	if got := ops[0].SQL(); got != `CREATE ROLE "svc" PASSWORD '{{vault:secret/roles/svc#pw}}';` {
		t.Errorf("SQL(): got %q, want the unresolved placeholder form", got)
	}
	resolver := &fakeDiffResolver{values: map[string]string{"vault:secret/roles/svc#pw": "s3cr3t"}}
	got, err := sb.ExecSQL(resolver)
	if err != nil {
		t.Fatal(err)
	}
	if want := `CREATE ROLE "svc" PASSWORD 's3cr3t';`; got != want {
		t.Errorf("ExecSQL(): got %q, want %q", got, want)
	}
	// SQL() must be unaffected by the ExecSQL call above.
	if got := ops[0].SQL(); got != `CREATE ROLE "svc" PASSWORD '{{vault:secret/roles/svc#pw}}';` {
		t.Errorf("SQL() changed after ExecSQL was called: got %q", got)
	}
}

func TestCreateRoleNoPasswordIsPlainOp(t *testing.T) {
	o := &ir.Role{Name: "plain_role", CanLogin: boolp(false)}
	ops := createRole(o)
	if _, ok := ops[0].(pipeline.SecretBearingOp); ok {
		t.Error("a role with no PASSWORD must not implement SecretBearingOp")
	}
	if got := ops[0].SQL(); got != `CREATE ROLE "plain_role" NOLOGIN;` {
		t.Errorf("SQL(): got %q", got)
	}
}

func TestCreateRoleWithComment(t *testing.T) {
	o := &ir.Role{Name: "app_readonly", CanLogin: boolp(false), Comment: strp("Read-only access")}
	ops := createRole(o)
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (CREATE + COMMENT), got %d", len(ops))
	}
	if got := ops[1].SQL(); got != `COMMENT ON ROLE "app_readonly" IS 'Read-only access';` {
		t.Errorf("COMMENT op: got %q", got)
	}
}

func TestDiffRoleAttrChangesBatchedIntoOneAlter(t *testing.T) {
	o := &ir.Role{Name: "svc", CanLogin: boolp(true), ConnectionLimit: intp(5)}
	snap := &snapshot.SnapRole{Name: "svc", CanLogin: boolp(false), ConnectionLimit: intp(1)}
	ops := diffRole(o, snap)
	if len(ops) != 1 {
		t.Fatalf("expected 1 batched ALTER ROLE op, got %d: %v", len(ops), ops)
	}
	want := `ALTER ROLE "svc" LOGIN CONNECTION LIMIT 5;`
	if got := ops[0].SQL(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDiffRoleUndeclaredFieldsNeverCompared(t *testing.T) {
	// o declares nothing; snap has a totally different state. Since nothing
	// is declared, nothing should be diffed — undeclared means "not managed
	// by DPG for this role", not "reset to PostgreSQL's default".
	o := &ir.Role{Name: "svc"}
	snap := &snapshot.SnapRole{
		Name: "svc", CanLogin: boolp(true), Superuser: boolp(true),
		ConnectionLimit: intp(99), ValidUntil: strp("2020-01-01"),
	}
	ops := diffRole(o, snap)
	if len(ops) != 0 {
		t.Errorf("expected no ops for an undeclared-everything role, got %d: %v", len(ops), ops)
	}
}

func TestDiffRolePasswordChangeDetectedViaHash(t *testing.T) {
	o := &ir.Role{Name: "svc", Password: strp("{{vault:secret/svc#new}}")}
	snap := &snapshot.SnapRole{Name: "svc", PasswordHash: hashText("{{vault:secret/svc#old}}")}
	ops := diffRole(o, snap)
	if len(ops) != 1 {
		t.Fatalf("expected 1 op (ALTER ROLE PASSWORD), got %d: %v", len(ops), ops)
	}
	sb, ok := ops[0].(pipeline.SecretBearingOp)
	if !ok {
		t.Fatalf("expected %T to implement pipeline.SecretBearingOp", ops[0])
	}
	if got := ops[0].SQL(); got != `ALTER ROLE "svc" PASSWORD '{{vault:secret/svc#new}}';` {
		t.Errorf("SQL(): got %q", got)
	}
	if ops[0].Safety() != pipeline.Caution {
		t.Errorf("Safety() = %v, want Caution (invalidates existing sessions using the old password)", ops[0].Safety())
	}
	resolver := &fakeDiffResolver{values: map[string]string{"vault:secret/svc#new": "s3cr3t"}}
	got, err := sb.ExecSQL(resolver)
	if err != nil {
		t.Fatal(err)
	}
	if want := `ALTER ROLE "svc" PASSWORD 's3cr3t';`; got != want {
		t.Errorf("ExecSQL(): got %q, want %q", got, want)
	}
}

func TestDiffRolePasswordUnchangedIsNoop(t *testing.T) {
	o := &ir.Role{Name: "svc", Password: strp("{{vault:secret/svc#pw}}")}
	snap := &snapshot.SnapRole{Name: "svc", PasswordHash: hashText("{{vault:secret/svc#pw}}")}
	ops := diffRole(o, snap)
	if len(ops) != 0 {
		t.Errorf("expected no ops for an unchanged password, got %d: %v", len(ops), ops)
	}
}

func TestDiffRoleMembershipAddedAndRemoved(t *testing.T) {
	o := &ir.Role{
		Name:        "svc",
		InRole:      []string{"reader", "new_role"},
		RoleMembers: []string{"member_new"},
		AdminRoles:  []string{"admin_new"},
	}
	snap := &snapshot.SnapRole{
		Name:        "svc",
		InRole:      []string{"reader", "old_role"},
		RoleMembers: []string{"member_old"},
		AdminRoles:  []string{"admin_old"},
	}
	ops := diffRole(o, snap)
	var sqls []string
	for _, op := range ops {
		sqls = append(sqls, op.SQL())
	}
	wantContains := []string{
		`GRANT "new_role" TO "svc";`,
		`REVOKE "old_role" FROM "svc";`,
		`GRANT "svc" TO "member_new";`,
		`REVOKE "svc" FROM "member_old";`,
		`GRANT "svc" TO "admin_new" WITH ADMIN OPTION;`,
		`REVOKE "svc" FROM "admin_old";`,
	}
	for _, want := range wantContains {
		if !slices.Contains(sqls, want) {
			t.Errorf("expected an op with SQL %q, got: %v", want, sqls)
		}
	}
	if len(ops) != len(wantContains) {
		t.Errorf("expected exactly %d ops, got %d: %v", len(wantContains), len(ops), sqls)
	}
}

func TestDiffRoleMembershipUndeclaredNeverDiffed(t *testing.T) {
	o := &ir.Role{Name: "svc"} // InRole/RoleMembers/AdminRoles all nil
	snap := &snapshot.SnapRole{Name: "svc", InRole: []string{"reader"}, RoleMembers: []string{"m"}, AdminRoles: []string{"a"}}
	ops := diffRole(o, snap)
	if len(ops) != 0 {
		t.Errorf("expected no membership ops when membership fields are undeclared, got %d: %v", len(ops), ops)
	}
}

// ── UserMapping OPTIONS secret references (Secret resolution, Phase 5) ──────────

func TestCreateUserMappingSQLIsAlwaysThePlaceholderForm(t *testing.T) {
	o := &ir.UserMapping{
		User: "app", Server: "srv",
		Body: "CREATE USER MAPPING FOR app SERVER srv OPTIONS (user 'app', password '{{vault:secret/fdw/db#pw}}')",
	}
	ops, err := createUserMapping(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	want := o.Body + ";"
	if got := ops[0].SQL(); got != want {
		t.Errorf("SQL(): got %q, want %q", got, want)
	}
	if _, ok := ops[0].(pipeline.SecretBearingOp); !ok {
		t.Fatalf("expected %T to implement pipeline.SecretBearingOp", ops[0])
	}
}

func TestCreateUserMappingExecSQLResolvesWithinOptions(t *testing.T) {
	o := &ir.UserMapping{
		User: "app", Server: "srv",
		Body: "CREATE USER MAPPING FOR app SERVER srv OPTIONS (user 'app', password '{{vault:secret/fdw/db#pw}}')",
	}
	ops, _ := createUserMapping(o)
	sb := ops[0].(pipeline.SecretBearingOp)
	resolver := &fakeDiffResolver{values: map[string]string{"vault:secret/fdw/db#pw": "s3cr3t"}}
	got, err := sb.ExecSQL(resolver)
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE USER MAPPING FOR app SERVER srv OPTIONS (user 'app', password 's3cr3t');"
	if got != want {
		t.Errorf("ExecSQL(): got %q, want %q", got, want)
	}
	// SQL() must be unaffected by the ExecSQL call above.
	if got := ops[0].SQL(); got != o.Body+";" {
		t.Errorf("SQL() changed after ExecSQL was called: got %q", got)
	}
}

func TestCreateUserMappingExecSQLPlainLiteralNeverCallsResolver(t *testing.T) {
	o := &ir.UserMapping{
		User: "app", Server: "srv",
		Body: "CREATE USER MAPPING FOR app SERVER srv OPTIONS (user 'app', password 'hunter2')",
	}
	ops, _ := createUserMapping(o)
	sb := ops[0].(pipeline.SecretBearingOp)
	got, err := sb.ExecSQL(&fakeDiffResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if got != o.Body+";" {
		t.Errorf("ExecSQL(): got %q, want the literal returned unchanged", got)
	}
}

func TestCreateUserMappingExecSQLResolveErrorPropagates(t *testing.T) {
	o := &ir.UserMapping{
		User: "app", Server: "srv",
		Body: "CREATE USER MAPPING FOR app SERVER srv OPTIONS (password '{{vault:secret/missing}}')",
	}
	ops, _ := createUserMapping(o)
	sb := ops[0].(pipeline.SecretBearingOp)
	if _, err := sb.ExecSQL(&fakeDiffResolver{}); err == nil {
		t.Fatal("expected an error when the referenced secret doesn't resolve")
	}
}

func TestDiffUserMappingOfflineDetectsEdit(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.UserMapping{User: "app", Server: "srv", Body: "CREATE USER MAPPING FOR app SERVER srv OPTIONS (password '{{vault:secret/db#old}}')"},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.UserMapping{User: "app", Server: "srv", Body: "CREATE USER MAPPING FOR app SERVER srv OPTIONS (password '{{vault:secret/db#new}}')"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops (DROP + CREATE), got %d: %v", len(ops), ops)
	}
	if !strings.Contains(ops[0].SQL(), "DROP USER MAPPING") {
		t.Errorf("op[0] = %q, want DROP USER MAPPING", ops[0].SQL())
	}
	if _, ok := ops[1].(pipeline.SecretBearingOp); !ok {
		t.Errorf("expected the CREATE op (%T) to implement pipeline.SecretBearingOp", ops[1])
	}
}

func TestDiffUserMappingUnchangedIsNoop(t *testing.T) {
	d := New()
	body := "CREATE USER MAPPING FOR app SERVER srv OPTIONS (password '{{vault:secret/db#pw}}')"
	um := &ir.UserMapping{User: "app", Server: "srv", Body: body}
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{um}); err != nil {
		t.Fatal(err)
	}
	ops, err := d.Diff([]pipeline.IRObject{um}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for an unchanged user mapping, got %d: %v", len(ops), ops)
	}
}

// TestDiffOpaqueOfflineEditEmitsStructuredDropRecreate is the regression guard
// for #3 (real update path for opaque objects): a genuine offline body edit
// must emit a structured DROP (matching what dropObject emits when the object
// is removed outright) followed by a CREATE from the new body — not the old
// "-- WARNING: ... manual DROP + recreate required" comment placeholder. All
// 17 opaque kinds are covered, including "operator" — previously excluded
// because dropObject's operator case couldn't safely build a DROP OPERATOR
// statement (PG requires a mandatory (lefttype, righttype) clause ir.Operator
// didn't capture); see ir.Operator.LeftType/RightType and ir.OperandsKey.
func TestDiffOpaqueOfflineEditEmitsStructuredDropRecreate(t *testing.T) {
	intType := ir.TypeRef{Name: "integer"}
	cases := []struct {
		name             string
		oldObj, newObj   pipeline.IRObject
		wantDropSubstr   string
		wantNewInBody    string
		wantCreateSafety pipeline.Safety // zero value (Safe) unless overridden
		dropIsSafe       bool            // false (Destructive) unless overridden — event_trigger is the sole exception, RFC §14.1
	}{
		{
			name:           "tablespace",
			oldObj:         &ir.Tablespace{Name: "ts", Location: "/data/ts1", Body: "CREATE TABLESPACE ts LOCATION '/data/ts1'"},
			newObj:         &ir.Tablespace{Name: "ts", Location: "/data/ts2", Body: "CREATE TABLESPACE ts LOCATION '/data/ts2'"},
			wantDropSubstr: `DROP TABLESPACE IF EXISTS "ts";`,
			wantNewInBody:  "/data/ts2",
			// Manual (non-transactional), not Safe like every other opaque
			// kind: CREATE TABLESPACE cannot run inside a transaction block
			// (confirmed live: "ERROR: CREATE TABLESPACE cannot run inside
			// a transaction block") — see createOpaque's doc comment.
			wantCreateSafety: pipeline.Manual,
		},
		{
			name:           "fdw",
			oldObj:         &ir.ForeignDataWrapper{Name: "myfdw", Handler: "h1", Body: "CREATE FOREIGN DATA WRAPPER myfdw HANDLER h1"},
			newObj:         &ir.ForeignDataWrapper{Name: "myfdw", Handler: "h2", Body: "CREATE FOREIGN DATA WRAPPER myfdw HANDLER h2"},
			wantDropSubstr: `DROP FOREIGN DATA WRAPPER IF EXISTS "myfdw";`,
			wantNewInBody:  "h2",
		},
		{
			// A FDW-wrapper change specifically (not an OPTIONS-only change,
			// which RFC §14.9 gives a real, targeted ALTER SERVER for — see
			// TestDiffForeignServerOptionsChangeIsSafeAlter — real PostgreSQL
			// has no ALTER SERVER ... FOREIGN DATA WRAPPER at all, confirmed
			// via `\h ALTER SERVER`).
			name:           "server",
			oldObj:         &ir.ForeignServer{Name: "srv", FDWName: "myfdw1", Body: "CREATE SERVER srv FOREIGN DATA WRAPPER myfdw1"},
			newObj:         &ir.ForeignServer{Name: "srv", FDWName: "myfdw2", Body: "CREATE SERVER srv FOREIGN DATA WRAPPER myfdw2"},
			wantDropSubstr: `DROP SERVER IF EXISTS "srv";`,
			wantNewInBody:  "myfdw2",
		},
		{
			name:           "user_mapping",
			oldObj:         &ir.UserMapping{User: "alice", Server: "srv", Body: "CREATE USER MAPPING FOR alice SERVER srv OPTIONS (password 'p1')"},
			newObj:         &ir.UserMapping{User: "alice", Server: "srv", Body: "CREATE USER MAPPING FOR alice SERVER srv OPTIONS (password 'p2')"},
			wantDropSubstr: `DROP USER MAPPING IF EXISTS FOR "alice" SERVER "srv";`,
			wantNewInBody:  "'p2'",
		},
		{
			// An AllTables change specifically (not a Tables-list-only or
			// WITH (publish = ...)-only change, which RFC §13.1 gives a
			// real, targeted ALTER PUBLICATION for — see
			// TestDiffPublicationTablesChangeIsSafeAlter/
			// TestDiffPublicationPublishOptionsChangeIsSafeAlter — real
			// PostgreSQL has no way to convert a FOR ALL TABLES
			// publication to/from an explicit table list via ALTER,
			// confirmed via a live "Tables cannot be added to or dropped
			// from FOR ALL TABLES publications" error).
			name: "publication",
			oldObj: &ir.Publication{
				Name: "p", AllTables: true, Insert: true, Update: true, Delete: true, Truncate: true,
				Body: "CREATE PUBLICATION p FOR ALL TABLES",
			},
			newObj: &ir.Publication{
				Name: "p", Tables: []ir.PublicationTableRef{{Schema: "public", Name: "t"}},
				Insert: true, Update: true, Delete: true, Truncate: true,
				Body: "CREATE PUBLICATION p FOR TABLE public.t",
			},
			wantDropSubstr: `DROP PUBLICATION IF EXISTS "p";`,
			wantNewInBody:  "public.t",
		},
		{
			name:           "subscription",
			oldObj:         &ir.Subscription{Name: "sub", ConnInfo: "x", Body: "CREATE SUBSCRIPTION sub CONNECTION 'x' PUBLICATION p1"},
			newObj:         &ir.Subscription{Name: "sub", ConnInfo: "x", Body: "CREATE SUBSCRIPTION sub CONNECTION 'x' PUBLICATION p2"},
			wantDropSubstr: `DROP SUBSCRIPTION IF EXISTS "sub";`,
			wantNewInBody:  "p2",
			// Manual (non-transactional), not Safe like every other opaque
			// kind: PostgreSQL's CREATE SUBSCRIPTION defaults to WITH
			// (create_slot = true), which errors "cannot run inside a
			// transaction block" — confirmed live, see createSubscription's
			// doc comment.
			wantCreateSafety: pipeline.Manual,
		},
		{
			name:           "event_trigger",
			oldObj:         &ir.EventTrigger{Name: "et", Event: "ddl_command_start", Function: "f1", Body: "CREATE EVENT TRIGGER et ON ddl_command_start EXECUTE FUNCTION f1()"},
			newObj:         &ir.EventTrigger{Name: "et", Event: "ddl_command_start", Function: "f2", Body: "CREATE EVENT TRIGGER et ON ddl_command_start EXECUTE FUNCTION f2()"},
			wantDropSubstr: `DROP EVENT TRIGGER IF EXISTS "et";`,
			wantNewInBody:  "f2()",
			// Unlike every other opaque kind here, an event trigger holds no
			// data and nothing depends on it — RFC §14.1 explicitly classifies
			// its DROP+CREATE as SAFE, unlike Collation/Cast/Operator below
			// (all explicitly DESTRUCTIVE per their own RFC sections).
			dropIsSafe: true,
		},
		{
			name: "collation",
			oldObj: &ir.Collation{
				Schema: "public", Name: "c", Provider: "c", Collate: strPtr("C"), Ctype: strPtr("C"), Deterministic: true,
				Body: "CREATE COLLATION public.c (LOCALE = 'C')",
			},
			newObj: &ir.Collation{
				Schema: "public", Name: "c", Provider: "c", Collate: strPtr("POSIX"), Ctype: strPtr("POSIX"), Deterministic: true,
				Body: "CREATE COLLATION public.c (LOCALE = 'POSIX')",
			},
			wantDropSubstr: `DROP COLLATION IF EXISTS "public"."c";`,
			wantNewInBody:  "POSIX",
		},
		{
			name: "operator",
			oldObj: &ir.Operator{Schema: "public", Name: "===", LeftType: &intType, RightType: &intType,
				Body: "CREATE OPERATOR public.=== (FUNCTION = f1, LEFTARG = integer, RIGHTARG = integer)"},
			newObj: &ir.Operator{Schema: "public", Name: "===", LeftType: &intType, RightType: &intType,
				Body: "CREATE OPERATOR public.=== (FUNCTION = f2, LEFTARG = integer, RIGHTARG = integer)"},
			wantDropSubstr: `DROP OPERATOR IF EXISTS "public".===(integer, integer);`, // symbol never quoted — see qualOperatorIdent
			wantNewInBody:  "f2",
		},
		{
			name:           "operator_class",
			oldObj:         &ir.OperatorClass{Schema: "public", Name: "oc", AccessMethod: "gin", Body: "CREATE OPERATOR CLASS public.oc FOR TYPE int4 USING gin AS OPERATOR 1 = FUNCTION 1 f1()"},
			newObj:         &ir.OperatorClass{Schema: "public", Name: "oc", AccessMethod: "gin", Body: "CREATE OPERATOR CLASS public.oc FOR TYPE int4 USING gin AS OPERATOR 1 = FUNCTION 1 f2()"},
			wantDropSubstr: `DROP OPERATOR CLASS IF EXISTS "public"."oc" USING gin;`,
			wantNewInBody:  "f2()",
		},
		{
			name:           "operator_family",
			oldObj:         &ir.OperatorFamily{Schema: "public", Name: "of", AccessMethod: "gin", Body: "CREATE OPERATOR FAMILY public.of USING gin"},
			newObj:         &ir.OperatorFamily{Schema: "public", Name: "of", AccessMethod: "gin", Body: "CREATE OPERATOR FAMILY public.of USING gin AS OPERATOR 1 = (int4, int4)"},
			wantDropSubstr: `DROP OPERATOR FAMILY IF EXISTS "public"."of" USING gin;`,
			wantNewInBody:  "OPERATOR 1",
		},
		{
			name:           "cast",
			oldObj:         &ir.Cast{SourceType: ir.TypeRef{Name: "int4"}, TargetType: ir.TypeRef{Name: "text"}, Method: "f", Function: "f1", Body: "CREATE CAST (int4 AS text) WITH FUNCTION f1(int4)"},
			newObj:         &ir.Cast{SourceType: ir.TypeRef{Name: "int4"}, TargetType: ir.TypeRef{Name: "text"}, Method: "f", Function: "f2", Body: "CREATE CAST (int4 AS text) WITH FUNCTION f2(int4)"},
			wantDropSubstr: `DROP CAST IF EXISTS (int4 AS text);`,
			wantNewInBody:  "f2(int4)",
		},
		{
			name: "statistics",
			oldObj: &ir.StatisticsObject{
				Schema: "public", Name: "st", Table: "public.t", Kinds: []string{"ndistinct"}, Columns: []string{"a", "b"},
				Body: "CREATE STATISTICS public.st (ndistinct) ON a, b FROM t",
			},
			newObj: &ir.StatisticsObject{
				Schema: "public", Name: "st", Table: "public.t", Kinds: []string{"dependencies"}, Columns: []string{"a", "b"},
				Body: "CREATE STATISTICS public.st (dependencies) ON a, b FROM t",
			},
			wantDropSubstr: `DROP STATISTICS IF EXISTS "public"."st";`,
			wantNewInBody:  "dependencies",
		},
		{
			name:           "ts_config",
			oldObj:         &ir.TSConfig{Schema: "public", Name: "tc", Body: "CREATE TEXT SEARCH CONFIGURATION public.tc (COPY = simple1)"},
			newObj:         &ir.TSConfig{Schema: "public", Name: "tc", Body: "CREATE TEXT SEARCH CONFIGURATION public.tc (COPY = simple2)"},
			wantDropSubstr: `DROP TEXT SEARCH CONFIGURATION IF EXISTS "public"."tc";`,
			wantNewInBody:  "simple2",
		},
		{
			name:           "ts_dict",
			oldObj:         &ir.TSDict{Schema: "public", Name: "td", Body: "CREATE TEXT SEARCH DICTIONARY public.td (TEMPLATE = simple, stopwords = e1)"},
			newObj:         &ir.TSDict{Schema: "public", Name: "td", Body: "CREATE TEXT SEARCH DICTIONARY public.td (TEMPLATE = simple, stopwords = e2)"},
			wantDropSubstr: `DROP TEXT SEARCH DICTIONARY IF EXISTS "public"."td";`,
			wantNewInBody:  "e2",
		},
		{
			name:           "ts_parser",
			oldObj:         &ir.TSParser{Schema: "public", Name: "tp", Body: "CREATE TEXT SEARCH PARSER public.tp (START = f1, GETTOKEN = g, END = e, LEXTYPES = l)"},
			newObj:         &ir.TSParser{Schema: "public", Name: "tp", Body: "CREATE TEXT SEARCH PARSER public.tp (START = f2, GETTOKEN = g, END = e, LEXTYPES = l)"},
			wantDropSubstr: `DROP TEXT SEARCH PARSER IF EXISTS "public"."tp";`,
			wantNewInBody:  "f2",
		},
		{
			name:           "ts_template",
			oldObj:         &ir.TSTemplate{Schema: "public", Name: "tt", Body: "CREATE TEXT SEARCH TEMPLATE public.tt (LEXIZE = f1)"},
			newObj:         &ir.TSTemplate{Schema: "public", Name: "tt", Body: "CREATE TEXT SEARCH TEMPLATE public.tt (LEXIZE = f2)"},
			wantDropSubstr: `DROP TEXT SEARCH TEMPLATE IF EXISTS "public"."tt";`,
			wantNewInBody:  "f2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New()
			snap := &pipeline.Snapshot{}
			if err := snapshot.Populate(snap, []pipeline.IRObject{tc.oldObj}); err != nil {
				t.Fatal(err)
			}
			ops, err := d.Diff([]pipeline.IRObject{tc.newObj}, snap)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(ops) != 2 {
				t.Fatalf("want 2 ops (structured DROP + CREATE), got %d: %v", len(ops), ops)
			}
			dropSQL := ops[0].SQL()
			createSQL := ops[1].SQL()
			if !strings.Contains(dropSQL, tc.wantDropSubstr) {
				t.Errorf("op[0] = %q, want substring %q", dropSQL, tc.wantDropSubstr)
			}
			if !strings.Contains(createSQL, tc.wantNewInBody) {
				t.Errorf("op[1] = %q, want substring %q (new body)", createSQL, tc.wantNewInBody)
			}
			wantDropSafety := pipeline.Destructive
			if tc.dropIsSafe {
				wantDropSafety = pipeline.Safe
			}
			if ops[0].Safety() != wantDropSafety {
				t.Errorf("DROP op safety = %v, want %v", ops[0].Safety(), wantDropSafety)
			}
			if ops[1].Safety() != tc.wantCreateSafety {
				t.Errorf("CREATE op safety = %v, want %v", ops[1].Safety(), tc.wantCreateSafety)
			}
			for _, op := range ops {
				if strings.Contains(op.SQL(), "manual DROP + recreate required") {
					t.Errorf("structured path must not fall back to the manual warning, got: %s", op.SQL())
				}
			}
		})
	}
}

// ── TSConfig MAPPING FOR (RFC §12.1) ──────────────────────────────────────────
// tc.Mappings was parsed by the blockparser and copied onto ir.TSConfig by
// the builder, but never read by the differ at all — a declared MAPPING FOR
// entry silently produced no ALTER TEXT SEARCH CONFIGURATION statement
// whatsoever, found live-testing a demo project.

func TestDiffCreateTSConfigEmitsMapping(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.TSConfig{
			Schema: "public", Name: "demo_search",
			ParserSchema: "pg_catalog", ParserName: "default",
			Body: "CREATE TEXT SEARCH CONFIGURATION public.demo_search (PARSER = pg_catalog.default)",
			Mappings: []pipeline.TSMappingDef{
				{TokenTypes: []string{"word", "hword"}, Dictionaries: []pipeline.Identifier{{Name: "english_stem"}}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	want := `ALTER TEXT SEARCH CONFIGURATION "public"."demo_search" ALTER MAPPING FOR word, hword WITH english_stem;`
	if !containsSQL(ops, want) {
		t.Errorf("expected %q, got: %v", want, sqlList(ops))
	}
}

// TestDiffCreateTSConfigEmitsMappingFallbackChain guards the real
// multi-dictionary PG feature (WITH dict1, dict2 — a fallback chain).
func TestDiffCreateTSConfigEmitsMappingFallbackChain(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.TSConfig{
			Schema: "public", Name: "demo_search",
			ParserSchema: "pg_catalog", ParserName: "default",
			Body: "CREATE TEXT SEARCH CONFIGURATION public.demo_search (PARSER = pg_catalog.default)",
			Mappings: []pipeline.TSMappingDef{
				{TokenTypes: []string{"word"}, Dictionaries: []pipeline.Identifier{{Name: "unaccent"}, {Name: "english_stem"}}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	want := `ALTER TEXT SEARCH CONFIGURATION "public"."demo_search" ALTER MAPPING FOR word WITH unaccent, english_stem;`
	if !containsSQL(ops, want) {
		t.Errorf("expected %q, got: %v", want, sqlList(ops))
	}
}

func snapTSConfig(schema, name string, mappings []snapshot.SnapTSMapping) *pipeline.Snapshot {
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject(schema+"."+name, &snapshot.SnapObject{
		Kind: "ts_config",
		Opaque: &snapshot.SnapOpaque{
			Kind: "ts_config", Schema: schema, Name: name,
			BodyHash: "", // Reconstructed-equivalent: no body-hash comparison
			Mappings: mappings,
		},
	})
	return snap
}

// TestDiffTSConfigMappingAdded guards the diff-existing-config path (not
// just create): adding a MAPPING FOR entry to an already-applied config.
func TestDiffTSConfigMappingAdded(t *testing.T) {
	d := New()
	snap := snapTSConfig("public", "demo_search", nil)
	desired := []pipeline.IRObject{
		&ir.TSConfig{
			Schema: "public", Name: "demo_search",
			ParserSchema: "pg_catalog", ParserName: "default",
			Reconstructed: true, // skip body-hash recreate; only diffing mappings here
			Mappings: []pipeline.TSMappingDef{
				{TokenTypes: []string{"word"}, Dictionaries: []pipeline.Identifier{{Name: "english_stem"}}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	want := `ALTER TEXT SEARCH CONFIGURATION "public"."demo_search" ALTER MAPPING FOR word WITH english_stem;`
	if !containsSQL(ops, want) {
		t.Errorf("expected %q, got: %v", want, sqlList(ops))
	}
}

// TestDiffTSConfigMappingChanged guards changing an existing token type's
// dictionary chain.
func TestDiffTSConfigMappingChanged(t *testing.T) {
	d := New()
	snap := snapTSConfig("public", "demo_search", []snapshot.SnapTSMapping{
		{TokenTypes: []string{"word"}, Dictionaries: []string{"simple"}},
	})
	desired := []pipeline.IRObject{
		&ir.TSConfig{
			Schema: "public", Name: "demo_search",
			ParserSchema: "pg_catalog", ParserName: "default",
			Reconstructed: true,
			Mappings: []pipeline.TSMappingDef{
				{TokenTypes: []string{"word"}, Dictionaries: []pipeline.Identifier{{Name: "english_stem"}}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	want := `ALTER TEXT SEARCH CONFIGURATION "public"."demo_search" ALTER MAPPING FOR word WITH english_stem;`
	if !containsSQL(ops, want) {
		t.Errorf("expected %q, got: %v", want, sqlList(ops))
	}
}

// TestDiffTSConfigMappingRemoved guards the removal path: DROP MAPPING FOR.
func TestDiffTSConfigMappingRemoved(t *testing.T) {
	d := New()
	snap := snapTSConfig("public", "demo_search", []snapshot.SnapTSMapping{
		{TokenTypes: []string{"word"}, Dictionaries: []string{"english_stem"}},
	})
	desired := []pipeline.IRObject{
		&ir.TSConfig{
			Schema: "public", Name: "demo_search",
			ParserSchema: "pg_catalog", ParserName: "default",
			Reconstructed: true,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	want := `ALTER TEXT SEARCH CONFIGURATION "public"."demo_search" DROP MAPPING FOR word;`
	if !containsSQL(ops, want) {
		t.Errorf("expected %q, got: %v", want, sqlList(ops))
	}
}

// TestDiffTSConfigMappingUnchangedIsNoop is the negative case: identical
// mappings (even if grouped differently across MAPPING FOR entries in
// source vs the flattened snapshot) must not produce a spurious ALTER on
// every plan.
func TestDiffTSConfigMappingUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := snapTSConfig("public", "demo_search", []snapshot.SnapTSMapping{
		{TokenTypes: []string{"word", "hword"}, Dictionaries: []string{"english_stem"}},
	})
	desired := []pipeline.IRObject{
		&ir.TSConfig{
			Schema: "public", Name: "demo_search",
			ParserSchema: "pg_catalog", ParserName: "default",
			Reconstructed: true,
			Mappings: []pipeline.TSMappingDef{
				// Declared as two separate entries covering the same token
				// types with the same dictionary — must flatten identically.
				{TokenTypes: []string{"word"}, Dictionaries: []pipeline.Identifier{{Name: "english_stem"}}},
				{TokenTypes: []string{"hword"}, Dictionaries: []pipeline.Identifier{{Name: "english_stem"}}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "MAPPING") {
		t.Errorf("expected no MAPPING ops for unchanged mappings, got: %v", sqlList(ops))
	}
}

// TestDiffOperatorOverloadOnlyEditedOneChanges proves the QualifiedName
// widening (see ir.Operator.QualifiedName) doesn't just avoid a collision at
// rest — it also keeps two overloaded operators (same symbol, different
// operand types) independently diffable: editing one's body must produce ops
// for that one only, leaving the untouched overload alone.
func TestDiffOperatorOverloadOnlyEditedOneChanges(t *testing.T) {
	d := New()
	intType := ir.TypeRef{Name: "integer"}
	numType := ir.TypeRef{Name: "numeric"}
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.Operator{Schema: "public", Name: "+", LeftType: &intType, RightType: &intType,
			Body: "CREATE OPERATOR public.+ (FUNCTION = int4pl, LEFTARG = integer, RIGHTARG = integer)"},
		&ir.Operator{Schema: "public", Name: "+", LeftType: &numType, RightType: &numType,
			Body: "CREATE OPERATOR public.+ (FUNCTION = numeric_add, LEFTARG = numeric, RIGHTARG = numeric)"},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.Operator{Schema: "public", Name: "+", LeftType: &intType, RightType: &intType,
			Body: "CREATE OPERATOR public.+ (FUNCTION = int4pl_v2, LEFTARG = integer, RIGHTARG = integer)"},
		&ir.Operator{Schema: "public", Name: "+", LeftType: &numType, RightType: &numType,
			Body: "CREATE OPERATOR public.+ (FUNCTION = numeric_add, LEFTARG = numeric, RIGHTARG = numeric)"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("want 2 ops (DROP+CREATE for the edited integer overload only), got %d: %v", len(ops), sqlList(ops))
	}
	if !containsSQL(ops, `DROP OPERATOR IF EXISTS "public".+(integer, integer);`) {
		t.Errorf("expected DROP for the edited integer overload, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "int4pl_v2") {
		t.Errorf("expected CREATE with the new function, got: %v", sqlList(ops))
	}
	if containsSQL(ops, "numeric_add") {
		t.Errorf("untouched numeric overload must not be touched, got: %v", sqlList(ops))
	}
}

// VERIFY: desired side is the introspected reconstruction (Reconstructed=true),
// snap side is the stored source hash. Must NOT report spurious drift despite
// different-but-equivalent spelling.
func TestDiffCollationVerifyNoSpuriousDrift(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	// Stored snapshot from source apply (has a hash).
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.Collation{
			Schema: "public", Name: "c", Provider: "c", Collate: strPtr("C"), Ctype: strPtr("C"), Deterministic: true,
			Body: "CREATE COLLATION c (LC_COLLATE = 'C', LC_CTYPE = 'C')",
		},
	}); err != nil {
		t.Fatal(err)
	}
	// Introspected desired side: canonical reconstruction, marked Reconstructed.
	desired := []pipeline.IRObject{
		&ir.Collation{
			Schema: "public", Name: "c", Provider: "c", Collate: strPtr("C"), Ctype: strPtr("C"), Deterministic: true,
			Body: "CREATE COLLATION public.c (LOCALE = 'C')", Reconstructed: true,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("verify: spurious drift for reconstructed collation: %d ops: %v", len(ops), ops)
	}
}

// PLAN --LIVE: desired side is source, snap side is the introspected baseline
// (Reconstructed=true → no hash). Must NOT report spurious drift.
func TestDiffCollationPlanLiveNoSpuriousDrift(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	// Live baseline built from introspection (Reconstructed → BodyHash omitted).
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.Collation{
			Schema: "public", Name: "c", Provider: "c", Collate: strPtr("C"), Ctype: strPtr("C"), Deterministic: true,
			Body: "CREATE COLLATION public.c (LOCALE = 'C')", Reconstructed: true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.Collation{
			Schema: "public", Name: "c", Provider: "c", Collate: strPtr("C"), Ctype: strPtr("C"), Deterministic: true,
			Body: "CREATE COLLATION c (LC_COLLATE = 'C', LC_CTYPE = 'C')",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("plan --live: spurious drift vs reconstructed baseline: %d ops: %v", len(ops), ops)
	}
}

// createIndex must emit the partial-index WHERE predicate and INCLUDE columns;
// omitting them silently creates an index that differs from the declared one.
func TestDiffCreateIndexWhereAndInclude(t *testing.T) {
	d := New()
	where := "active"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "t",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "int4"}}, {Name: "active", Type: ir.TypeRef{Name: "bool"}}},
			Indexes: []*ir.Index{
				{Name: "t_a_idx", Columns: []pipeline.IndexColumn{{Name: "a"}}, Include: []string{"active"}, Where: &where},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	var sql string
	for _, o := range ops {
		if strings.Contains(o.SQL(), "CREATE") && strings.Contains(o.SQL(), "INDEX") {
			sql = o.SQL()
			break
		}
	}
	if sql == "" {
		t.Fatalf("no CREATE INDEX op: %v", sqlList(ops))
	}
	if !strings.Contains(sql, "INCLUDE (") || !strings.Contains(sql, `"active"`) {
		t.Errorf("missing INCLUDE clause: %s", sql)
	}
	if !strings.Contains(sql, "WHERE active") {
		t.Errorf("missing WHERE predicate: %s", sql)
	}
}

// TestDiffCreateIndexNullsNotDistinctAndWith is the regression guard for the
// S-tier index gap: WITH storage params and NULLS NOT DISTINCT were parsed
// into ir.Index (idx.With already existed, unused downstream) but createIndex
// silently dropped them from the emitted CREATE INDEX SQL — lossless-by-
// omission, same class of gap INCLUDE/WHERE used to have before they were
// wired up.
func TestDiffCreateIndexNullsNotDistinctAndWith(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "t",
			Columns: []*ir.Column{{Name: "a", Type: ir.TypeRef{Name: "int4"}}},
			Indexes: []*ir.Index{
				{
					Name:             "t_a_idx",
					Unique:           true,
					Columns:          []pipeline.IndexColumn{{Name: "a"}},
					NullsNotDistinct: true,
					With:             []pipeline.StorageParam{{Key: "fillfactor", Value: "70"}},
				},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	var sql string
	for _, o := range ops {
		if strings.Contains(o.SQL(), "CREATE") && strings.Contains(o.SQL(), "INDEX") {
			sql = o.SQL()
			break
		}
	}
	if sql == "" {
		t.Fatalf("no CREATE INDEX op: %v", sqlList(ops))
	}
	if !strings.Contains(sql, "NULLS NOT DISTINCT") {
		t.Errorf("missing NULLS NOT DISTINCT: %s", sql)
	}
	if !strings.Contains(sql, "WITH (fillfactor=70)") {
		t.Errorf("missing WITH clause: %s", sql)
	}
	// Grammar order: ... columns ... NULLS NOT DISTINCT ... WITH ( ... ) ...
	if strings.Index(sql, "NULLS NOT DISTINCT") > strings.Index(sql, "WITH (") {
		t.Errorf("NULLS NOT DISTINCT must precede WITH per PG grammar: %s", sql)
	}
}

// baseFullIndex returns a fully-populated index exercising every property
// diffIndexes must now compare, used as the common starting point for the
// content-comparison regression guards below.
func baseFullIndex() *ir.Index {
	where := "a > 0"
	return &ir.Index{
		Name:    "t_idx",
		Unique:  true,
		Method:  "btree",
		Columns: []pipeline.IndexColumn{{Name: "a", SortOrder: "ASC"}, {Name: "b"}},
		Where:   &where,
		Include: []string{"c"},
		With:    []pipeline.StorageParam{{Key: "fillfactor", Value: "70"}},
	}
}

func baseFullIndexTable(idx *ir.Index) *ir.Table {
	return &ir.Table{
		Schema: "public",
		Name:   "t",
		Columns: []*ir.Column{
			{Name: "a", Type: ir.TypeRef{Name: "int4"}},
			{Name: "b", Type: ir.TypeRef{Name: "int4"}},
			{Name: "c", Type: ir.TypeRef{Name: "int4"}},
		},
		Indexes: []*ir.Index{idx},
	}
}

// TestDiffIndexUnchangedNoOps is the baseline for the diffIndexes content-
// comparison fix: a same-named index whose definition is byte-for-byte
// identical (every property diffIndexes now compares) must still produce zero
// ops, not a spurious recreate.
func TestDiffIndexUnchangedNoOps(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{baseFullIndexTable(baseFullIndex())}); err != nil {
		t.Fatal(err)
	}
	ops, err := d.Diff([]pipeline.IRObject{baseFullIndexTable(baseFullIndex())}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("want 0 ops for an unchanged index, got %d: %v", len(ops), sqlList(ops))
	}
}

// TestDiffIndexContentChangeRecreates is the regression guard for the
// diffIndexes name-only-matching bug: previously a same-named index was
// matched purely by name, with zero comparison of its actual definition, so
// editing ANY property (method, uniqueness, columns, sort order/NULLS
// placement, WHERE, INCLUDE, WITH, or NULLS NOT DISTINCT) while keeping the
// name was a silent no-op on plan/apply. Each case here mutates exactly one
// property off the common baseFullIndex baseline and must now produce a
// structured DROP INDEX + CREATE INDEX pair reflecting the new definition.
func TestDiffIndexContentChangeRecreates(t *testing.T) {
	where2 := "a > 100"
	cases := []struct {
		name          string
		mutate        func(*ir.Index)
		wantInCreated string
	}{
		{"method", func(idx *ir.Index) { idx.Method = "gin" }, "USING gin"},
		{"unique", func(idx *ir.Index) { idx.Unique = false }, `CREATE INDEX "t_idx"`},
		{"columns_added", func(idx *ir.Index) {
			idx.Columns = append(idx.Columns, pipeline.IndexColumn{Name: "c"})
		}, `"c"`},
		{"sort_order", func(idx *ir.Index) { idx.Columns[0].SortOrder = "DESC" }, `"a" DESC`},
		{"where", func(idx *ir.Index) { idx.Where = &where2 }, "WHERE a > 100"},
		{"include", func(idx *ir.Index) { idx.Include = []string{"b"} }, `INCLUDE ("b")`},
		{"with", func(idx *ir.Index) {
			idx.With = []pipeline.StorageParam{{Key: "fillfactor", Value: "50"}}
		}, "WITH (fillfactor=50)"},
		{"nulls_not_distinct", func(idx *ir.Index) { idx.NullsNotDistinct = true }, "NULLS NOT DISTINCT"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New()
			snap := &pipeline.Snapshot{}
			if err := snapshot.Populate(snap, []pipeline.IRObject{baseFullIndexTable(baseFullIndex())}); err != nil {
				t.Fatal(err)
			}
			desiredIdx := baseFullIndex()
			tc.mutate(desiredIdx)
			ops, err := d.Diff([]pipeline.IRObject{baseFullIndexTable(desiredIdx)}, snap)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var dropSQL, createSQL string
			for _, o := range ops {
				sql := o.SQL()
				if strings.HasPrefix(sql, "DROP INDEX") {
					dropSQL = sql
				} else if strings.Contains(sql, "CREATE") && strings.Contains(sql, "INDEX") {
					createSQL = sql
				}
			}
			if dropSQL == "" || createSQL == "" {
				t.Fatalf("want DROP INDEX + CREATE INDEX ops, got %d ops: %v", len(ops), sqlList(ops))
			}
			if !strings.Contains(dropSQL, `DROP INDEX IF EXISTS "t_idx";`) {
				t.Errorf("unexpected DROP: %s", dropSQL)
			}
			if !strings.Contains(createSQL, tc.wantInCreated) {
				t.Errorf("recreated index missing %q: %s", tc.wantInCreated, createSQL)
			}
			for _, o := range ops {
				if o.Safety() != pipeline.Caution {
					t.Errorf("index recreate op safety = %v, want Caution: %s", o.Safety(), o.SQL())
				}
			}
		})
	}
}

// TestDiffIndexCascadeSuppressedWithSortSuffix guards translateIndexCols's
// suffix-stripping fix: SnapIndex.Columns can now carry an " ASC"/" DESC"/
// " NULLS FIRST"/" NULLS LAST" suffix (snapshot.ToSnapIndex), which
// translateIndexCols must strip before checking whether a dropped column
// takes the index with it — otherwise it would look for a column literally
// named "a DESC", never match, and emit a redundant standalone DROP INDEX
// alongside the DROP COLUMN cascade that already removes it in PG.
func TestDiffIndexCascadeSuppressedWithSortSuffix(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "int4"}},
			Indexes: []snapshot.SnapIndex{
				{Name: "t_a_idx", Method: "btree", Columns: "a DESC"},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "t", Columns: []*ir.Column{}},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "DROP INDEX") {
			t.Errorf("expected no standalone DROP INDEX for a column-dropped index (DESC suffix must not break cascade detection), got: %s", o.SQL())
		}
	}
}

// ── Procedure diff ────────────────────────────────────────────────────────────
// PROCEDURE had zero diff-level test coverage anywhere in the repo (unit or
// live integration) before this — found via a coverage pass on internal/diff.

func TestDiffCreateProcedure(t *testing.T) {
	d := New()
	comment := "recalculates totals"
	desired := []pipeline.IRObject{
		&ir.Procedure{
			Schema:   "public",
			Name:     "recalc_totals",
			Args:     []ir.FuncArg{{Type: ir.TypeRef{Name: "integer"}}},
			Attrs:    ir.FuncAttrs{Language: "plpgsql", Body: "BEGIN NULL; END;"},
			BodyHash: ir.HashBody("BEGIN NULL; END;"),
			Comment:  &comment,
			Grants:   []ir.Grant{{Privileges: []string{"EXECUTE"}, Roles: []string{"app"}}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "CREATE OR REPLACE PROCEDURE") || !containsSQL(ops, `"public"."recalc_totals"`) ||
		!containsSQL(ops, "LANGUAGE plpgsql") {
		t.Errorf("expected CREATE OR REPLACE PROCEDURE, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "COMMENT ON PROCEDURE") || !containsSQL(ops, "'recalculates totals'") {
		t.Errorf("expected COMMENT ON PROCEDURE, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "GRANT EXECUTE ON PROCEDURE") {
		t.Errorf("expected GRANT EXECUTE ON PROCEDURE, got: %v", sqlList(ops))
	}
}

func TestDiffProcedureUnchangedIsNoop(t *testing.T) {
	d := New()
	body := "BEGIN NULL; END;"
	hash := ir.HashBody(body)
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.recalc_totals(integer)", &snapshot.SnapObject{
		Kind: "procedure",
		Opaque: &snapshot.SnapOpaque{
			Kind: "procedure", Schema: "public", Name: "recalc_totals", Args: "integer", BodyHash: hash,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Procedure{
			Schema:   "public",
			Name:     "recalc_totals",
			Args:     []ir.FuncArg{{Type: ir.TypeRef{Name: "integer"}}},
			Attrs:    ir.FuncAttrs{Language: "plpgsql", Body: body},
			BodyHash: hash,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for unchanged procedure, got: %v", sqlList(ops))
	}
}

// TestDiffProcedureBodyChangedUsesCreateOrReplace proves a changed procedure
// body is re-emitted via CREATE OR REPLACE (unlike aggregates/other opaque
// kinds, which need a DROP first) — PostgreSQL supports CREATE OR REPLACE
// PROCEDURE directly, so no DROP is needed or expected.
func TestDiffProcedureBodyChangedUsesCreateOrReplace(t *testing.T) {
	d := New()
	oldHash := ir.HashBody("BEGIN NULL; END;")
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.recalc_totals(integer)", &snapshot.SnapObject{
		Kind: "procedure",
		Opaque: &snapshot.SnapOpaque{
			Kind: "procedure", Schema: "public", Name: "recalc_totals", Args: "integer", BodyHash: oldHash,
		},
	})
	newBody := "BEGIN UPDATE totals SET amount = amount + 1; END;"
	desired := []pipeline.IRObject{
		&ir.Procedure{
			Schema:   "public",
			Name:     "recalc_totals",
			Args:     []ir.FuncArg{{Type: ir.TypeRef{Name: "integer"}}},
			Attrs:    ir.FuncAttrs{Language: "plpgsql", Body: newBody},
			BodyHash: ir.HashBody(newBody),
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP PROCEDURE") {
		t.Errorf("unexpected DROP PROCEDURE for a body change, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "CREATE OR REPLACE PROCEDURE") || !containsSQL(ops, "amount + 1") {
		t.Errorf("expected CREATE OR REPLACE PROCEDURE with new body, got: %v", sqlList(ops))
	}
}

func TestDiffProcedureCommentAdded(t *testing.T) {
	d := New()
	body := "BEGIN NULL; END;"
	hash := ir.HashBody(body)
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.recalc_totals(integer)", &snapshot.SnapObject{
		Kind: "procedure",
		Opaque: &snapshot.SnapOpaque{
			Kind: "procedure", Schema: "public", Name: "recalc_totals", Args: "integer", BodyHash: hash,
		},
	})
	comment := "recalculates totals"
	desired := []pipeline.IRObject{
		&ir.Procedure{
			Schema:   "public",
			Name:     "recalc_totals",
			Args:     []ir.FuncArg{{Type: ir.TypeRef{Name: "integer"}}},
			Attrs:    ir.FuncAttrs{Language: "plpgsql", Body: body},
			BodyHash: hash,
			Comment:  &comment,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "COMMENT ON PROCEDURE") || !containsSQL(ops, "'recalculates totals'") {
		t.Errorf("expected COMMENT ON PROCEDURE, got: %v", sqlList(ops))
	}
}

// ── FUNCTION/PROCEDURE Revocations (RFC §11.3) ────────────────────────────────
// Regression guard for a real bug found live-testing REVOCATIONS against a
// demo project: ir.Function/ir.Procedure had no Revocations field at all, so
// a declared REVOCATIONS block was parsed but silently dropped everywhere
// downstream (build, snapshot, diff, dump) — only the GRANT half ever took
// effect. Mirrors the Table/View Revocations test suite above.

func TestDiffCreateProcedureEmitsRevocation(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Procedure{
			Schema:      "public",
			Name:        "recalc_totals",
			Args:        []ir.FuncArg{{Type: ir.TypeRef{Name: "integer"}}},
			Attrs:       ir.FuncAttrs{Language: "plpgsql", Body: "BEGIN NULL; END;"},
			BodyHash:    ir.HashBody("BEGIN NULL; END;"),
			Revocations: []ir.Revocation{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE EXECUTE ON PROCEDURE") {
		t.Errorf("expected REVOKE EXECUTE ON PROCEDURE at create time, got: %v", sqlList(ops))
	}
	if containsSQL(ops, `"PUBLIC"`) {
		t.Errorf("PUBLIC must not be quoted, got: %v", sqlList(ops))
	}
}

func TestDiffProcedureRevocationAdded(t *testing.T) {
	d := New()
	body := "BEGIN NULL; END;"
	hash := ir.HashBody(body)
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.recalc_totals(integer)", &snapshot.SnapObject{
		Kind: "procedure",
		Opaque: &snapshot.SnapOpaque{
			Kind: "procedure", Schema: "public", Name: "recalc_totals", Args: "integer", BodyHash: hash,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Procedure{
			Schema:      "public",
			Name:        "recalc_totals",
			Args:        []ir.FuncArg{{Type: ir.TypeRef{Name: "integer"}}},
			Attrs:       ir.FuncAttrs{Language: "plpgsql", Body: body},
			BodyHash:    hash,
			Revocations: []ir.Revocation{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, o := range ops {
		if strings.Contains(o.SQL(), "REVOKE EXECUTE ON PROCEDURE") {
			found = true
			if o.Safety() != pipeline.Caution {
				t.Errorf("explicit revocation safety = %v, want Caution: %s", o.Safety(), o.SQL())
			}
		}
	}
	if !found {
		t.Errorf("expected REVOKE EXECUTE ON PROCEDURE, got: %v", sqlList(ops))
	}
}

func TestDiffProcedureRevocationRemoved(t *testing.T) {
	d := New()
	body := "BEGIN NULL; END;"
	hash := ir.HashBody(body)
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.recalc_totals(integer)", &snapshot.SnapObject{
		Kind: "procedure",
		Opaque: &snapshot.SnapOpaque{
			Kind: "procedure", Schema: "public", Name: "recalc_totals", Args: "integer", BodyHash: hash,
			Revocations: []snapshot.SnapGrant{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Procedure{
			Schema:   "public",
			Name:     "recalc_totals",
			Args:     []ir.FuncArg{{Type: ir.TypeRef{Name: "integer"}}},
			Attrs:    ir.FuncAttrs{Language: "plpgsql", Body: body},
			BodyHash: hash,
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "GRANT EXECUTE ON PROCEDURE") {
		t.Errorf("expected GRANT EXECUTE ON PROCEDURE to restore the revoked privilege, got: %v", sqlList(ops))
	}
	if containsSQL(ops, "REVOKE") {
		t.Errorf("must not also emit REVOKE when the revocation itself was removed: %v", sqlList(ops))
	}
}

func TestDiffProcedureRevocationUnchangedIsNoop(t *testing.T) {
	d := New()
	body := "BEGIN NULL; END;"
	hash := ir.HashBody(body)
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.recalc_totals(integer)", &snapshot.SnapObject{
		Kind: "procedure",
		Opaque: &snapshot.SnapOpaque{
			Kind: "procedure", Schema: "public", Name: "recalc_totals", Args: "integer", BodyHash: hash,
			Revocations: []snapshot.SnapGrant{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Procedure{
			Schema:      "public",
			Name:        "recalc_totals",
			Args:        []ir.FuncArg{{Type: ir.TypeRef{Name: "integer"}}},
			Attrs:       ir.FuncAttrs{Language: "plpgsql", Body: body},
			BodyHash:    hash,
			Revocations: []ir.Revocation{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "GRANT") || containsSQL(ops, "REVOKE") {
		t.Errorf("expected no GRANT/REVOKE for unchanged revocation, got: %v", sqlList(ops))
	}
}

func TestDiffCreateFunctionEmitsRevocation(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema:      "public",
			Name:        "do_work",
			ReturnType:  ir.TypeRef{Name: "void"},
			BodyHash:    "h",
			Attrs:       ir.FuncAttrs{Language: "plpgsql", Body: "BEGIN END;"},
			Revocations: []ir.Revocation{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE EXECUTE ON FUNCTION") {
		t.Errorf("expected REVOKE EXECUTE ON FUNCTION at create time, got: %v", sqlList(ops))
	}
}

func TestDiffFunctionRevocationAdded(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.get_user()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema:     "public",
			Name:       "get_user",
			ReturnType: "void",
			Language:   "plpgsql",
			Volatility: "VOLATILE",
			BodyHash:   "abc",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema:      "public",
			Name:        "get_user",
			ReturnType:  ir.TypeRef{Name: "void"},
			BodyHash:    "abc",
			Attrs:       ir.FuncAttrs{Language: "plpgsql", Volatility: "VOLATILE", Body: "BEGIN END;"},
			Revocations: []ir.Revocation{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "REVOKE EXECUTE ON FUNCTION") {
		t.Errorf("expected REVOKE EXECUTE ON FUNCTION, got: %v", sqlList(ops))
	}
}

func TestDiffFunctionRevocationRemoved(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.get_user()", &snapshot.SnapObject{
		Kind: "function",
		Function: &snapshot.SnapFunction{
			Schema:      "public",
			Name:        "get_user",
			ReturnType:  "void",
			Language:    "plpgsql",
			Volatility:  "VOLATILE",
			BodyHash:    "abc",
			Revocations: []snapshot.SnapGrant{{Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"}}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Function{
			Schema:     "public",
			Name:       "get_user",
			ReturnType: ir.TypeRef{Name: "void"},
			BodyHash:   "abc",
			Attrs:      ir.FuncAttrs{Language: "plpgsql", Volatility: "VOLATILE", Body: "BEGIN END;"},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "GRANT EXECUTE ON FUNCTION") {
		t.Errorf("expected GRANT EXECUTE ON FUNCTION to restore the revoked privilege, got: %v", sqlList(ops))
	}
	if containsSQL(ops, "REVOKE") {
		t.Errorf("must not also emit REVOKE when the revocation itself was removed: %v", sqlList(ops))
	}
}

// ── TABLE/INDEX Tablespace (RFC §14.7) ────────────────────────────────────────
// Regression guard for a real bug found live-testing TABLESPACE against a
// demo project: ir.Table.Tablespace and ir.Index.Tablespace were both parsed
// from source but never read anywhere downstream — createTable/diffTable and
// createIndex/diffIndexes never referenced either field, so a declared
// TABLESPACE clause on a table or an index was a total silent no-op; `dpg
// plan` reported no changes even for a brand-new TABLESPACE declaration on
// an already-applied table.

func TestDiffCreateTableEmitsTablespace(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:     "public",
			Name:       "archive",
			Columns:    []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Tablespace: strPtr("archive_space"),
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `TABLESPACE "archive_space"`) {
		t.Errorf("expected CREATE TABLE with TABLESPACE, got: %v", sqlList(ops))
	}
}

func TestDiffTableTablespaceChanged(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.archive", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "archive",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:     "public",
			Name:       "archive",
			Columns:    []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Tablespace: strPtr("archive_space"),
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `ALTER TABLE "public"."archive" SET TABLESPACE "archive_space";`) {
		t.Errorf("expected ALTER TABLE SET TABLESPACE, got: %v", sqlList(ops))
	}
}

func TestDiffTableTablespaceUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.archive", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:     "public",
			Name:       "archive",
			Columns:    []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Tablespace: strPtr("archive_space"),
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:     "public",
			Name:       "archive",
			Columns:    []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Tablespace: strPtr("archive_space"),
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "TABLESPACE") {
		t.Errorf("expected no TABLESPACE op for unchanged tablespace, got: %v", sqlList(ops))
	}
}

func TestDiffCreateIndexEmitsTablespace(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "t",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Indexes: []*ir.Index{
				{
					Name:       "t_id_idx",
					Columns:    []pipeline.IndexColumn{{Name: "id"}},
					Tablespace: strPtr("archive_space"),
				},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `TABLESPACE "archive_space"`) {
		t.Errorf("expected CREATE INDEX with TABLESPACE, got: %v", sqlList(ops))
	}
}

func TestDiffIndexTablespaceChangedRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Indexes: []snapshot.SnapIndex{{Name: "t_id_idx", Columns: "id"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "t",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
			Indexes: []*ir.Index{
				{
					Name:       "t_id_idx",
					Columns:    []pipeline.IndexColumn{{Name: "id"}},
					Tablespace: strPtr("archive_space"),
				},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP INDEX IF EXISTS "t_id_idx";`) {
		t.Errorf("expected DROP INDEX for tablespace change, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, `TABLESPACE "archive_space"`) {
		t.Errorf("expected recreated CREATE INDEX with TABLESPACE, got: %v", sqlList(ops))
	}
}

// ── RANGE/BASE type body-change diffing (RFC §5.3/§5.5) ───────────────────────
// Regression guard for a real bug found live-testing BASE/VIRTUAL types
// against a demo project: diffType had explicit handling for COMPOSITE and
// ENUM only — RANGE and BASE both fell through to just the generic COMMENT
// check, so an already-applied RANGE or BASE type whose body changed was a
// silent no-op forever. Confirmed live on a throwaway RANGE type: before the
// fix, changing SUBTYPE produced "-- (no changes)"; after, it correctly
// emits DROP TYPE CASCADE + CREATE TYPE (RFC §5.3's exact stated semantics).

func TestDiffRangeTypeBodyChangedRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.zz_range", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{
			Schema: "public", Name: "zz_range", Variant: "RANGE",
			BodyHash: hashText("CREATE TYPE zz_range AS RANGE (subtype = int)"),
		},
	})
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "zz_range", Variant: "RANGE",
			Body: "CREATE TYPE zz_range AS RANGE (subtype = numeric)",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP TYPE IF EXISTS "public"."zz_range" CASCADE;`) {
		t.Errorf("expected DROP TYPE ... CASCADE, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "CREATE TYPE zz_range AS RANGE (subtype = numeric)") {
		t.Errorf("expected recreated CREATE TYPE with new body, got: %v", sqlList(ops))
	}
	var dropOp pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "DROP TYPE") {
			dropOp = o
		}
	}
	if dropOp == nil || dropOp.Safety() != pipeline.Destructive {
		t.Errorf("DROP TYPE safety = %v, want Destructive", dropOp)
	}
}

func TestDiffBaseTypeBodyChangedRecreatesNoCascade(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.zz_base", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{
			Schema: "public", Name: "zz_base", Variant: "BASE",
			BodyHash: hashText("CREATE TYPE zz_base (INPUT = zz_in, OUTPUT = zz_out)"),
		},
	})
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "zz_base", Variant: "BASE",
			Body: "CREATE TYPE zz_base (INPUT = zz_in2, OUTPUT = zz_out)",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP TYPE IF EXISTS "public"."zz_base";`) {
		t.Errorf("expected DROP TYPE (no CASCADE — RFC §5.5 doesn't specify it), got: %v", sqlList(ops))
	}
	if containsSQL(ops, "CASCADE") {
		t.Errorf("BASE type drop must not add CASCADE (RANGE-only per RFC §5.3), got: %v", sqlList(ops))
	}
}

func TestDiffRangeTypeBodyUnchangedIsNoop(t *testing.T) {
	d := New()
	body := "CREATE TYPE zz_range AS RANGE (subtype = numeric)"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.zz_range", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{
			Schema: "public", Name: "zz_range", Variant: "RANGE",
			BodyHash: hashText(body),
		},
	})
	desired := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "zz_range", Variant: "RANGE", Body: body},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for unchanged RANGE body, got: %v", sqlList(ops))
	}
}

// TestDiffRangeTypeReconstructedSnapshotSkipsComparison guards the same
// "reconstructed body isn't byte-identical to hand-written source" false-
// positive that diffOpaqueIR's own body-hash guard exists to avoid
// (Publication/Collation/...): a snapshot entry from live introspection has
// BodyHash == "" (see introspectRangeBodies setting Reconstructed = true),
// so a differently-formatted desired Body must NOT be treated as a change.
func TestDiffRangeTypeReconstructedSnapshotSkipsComparison(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.zz_range", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{
			Schema: "public", Name: "zz_range", Variant: "RANGE",
			BodyHash: "", // reconstructed snapshot entry
		},
	})
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "zz_range", Variant: "RANGE",
			Body: "CREATE TYPE zz_range AS RANGE (SUBTYPE = numeric)", // differently formatted
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP TYPE") {
		t.Errorf("must not recreate when the snapshot side is reconstructed (unhashable), got: %v", sqlList(ops))
	}
}

// ── DOMAIN structured diffing (RFC §5.4) ──────────────────────────────────────
// Regression guard for a real bug found live-testing a demo project:
// diffType had no case for DOMAIN at all — an already-applied domain whose
// DEFAULT/NOT NULL/constraints changed was a silent no-op forever, only the
// generic COMMENT check ever fired. Unlike RANGE/BASE (hash-diffed opaque
// bodies), RFC §5.4 requires per-property diffing with distinct safety
// levels, so each case below checks both the emitted SQL and its safety.

func TestDiffCreateDomainEmitsFullDefinition(t *testing.T) {
	d := New()
	def := "1"
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "positive_integer", Variant: "DOMAIN",
			DomainBaseType: ir.TypeRef{Name: "integer"},
			DomainDefault:  &def,
			DomainNotNull:  true,
			DomainConstraints: []*ir.Constraint{
				{Name: "positive_only", Type: "CHECK", Expr: "CHECK (VALUE > 0)"},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	sql := sqlList(ops)
	for _, want := range []string{"CREATE DOMAIN", "AS integer", "DEFAULT 1", "NOT NULL", `CONSTRAINT "positive_only" CHECK (VALUE > 0)`} {
		if !containsSQL(ops, want) {
			t.Errorf("expected %q in CREATE DOMAIN, got: %v", want, sql)
		}
	}
}

func TestDiffDomainDefaultAddedIsSafe(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.d", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: "integer"},
	})
	def := "1"
	desired := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: ir.TypeRef{Name: "integer"}, DomainDefault: &def},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "SET DEFAULT") {
			found = o
		}
	}
	if found == nil || found.SQL() != `ALTER DOMAIN "public"."d" SET DEFAULT 1;` {
		t.Fatalf("expected SET DEFAULT op, got: %v", sqlList(ops))
	}
	if found.Safety() != pipeline.Safe {
		t.Errorf("safety = %v, want Safe (RFC §5.4: adding a DEFAULT is SAFE)", found.Safety())
	}
}

func TestDiffDomainDefaultDroppedIsSafe(t *testing.T) {
	d := New()
	def := "1"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.d", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: "integer", DomainDefault: &def},
	})
	desired := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: ir.TypeRef{Name: "integer"}},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `ALTER DOMAIN "public"."d" DROP DEFAULT;`) {
		t.Fatalf("expected DROP DEFAULT op, got: %v", sqlList(ops))
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "DROP DEFAULT") && o.Safety() != pipeline.Safe {
			t.Errorf("safety = %v, want Safe (RFC §5.4: dropping a DEFAULT is SAFE)", o.Safety())
		}
	}
}

func TestDiffDomainSetNotNullIsCaution(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.d", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: "integer"},
	})
	desired := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: ir.TypeRef{Name: "integer"}, DomainNotNull: true},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "SET NOT NULL") {
			found = o
		}
	}
	if found == nil {
		t.Fatalf("expected SET NOT NULL op, got: %v", sqlList(ops))
	}
	if found.Safety() != pipeline.Caution {
		t.Errorf("safety = %v, want Caution (existing rows may violate it)", found.Safety())
	}
}

func TestDiffDomainDropNotNullIsSafe(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.d", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: "integer", DomainNotNull: true},
	})
	desired := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: ir.TypeRef{Name: "integer"}},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "DROP NOT NULL") {
			found = o
		}
	}
	if found == nil || found.Safety() != pipeline.Safe {
		t.Errorf("expected a Safe DROP NOT NULL op, got: %v", sqlList(ops))
	}
}

func TestDiffDomainConstraintAddedIsCaution(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.d", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: "integer"},
	})
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: ir.TypeRef{Name: "integer"},
			DomainConstraints: []*ir.Constraint{{Name: "positive_only", Type: "CHECK", Expr: "CHECK (VALUE > 0)"}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "ADD CONSTRAINT") {
			found = o
		}
	}
	if found == nil || !strings.Contains(found.SQL(), "positive_only") {
		t.Fatalf("expected ADD CONSTRAINT op, got: %v", sqlList(ops))
	}
	if found.Safety() != pipeline.Caution {
		t.Errorf("safety = %v, want Caution (RFC §5.4: adding a constraint is CAUTION)", found.Safety())
	}
}

func TestDiffDomainConstraintDroppedIsSafe(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.d", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{
			Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: "integer",
			DomainConstraints: []snapshot.SnapConstraint{{Name: "positive_only", Type: "CHECK", Expr: "CHECK (VALUE > 0)"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: ir.TypeRef{Name: "integer"}},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `ALTER DOMAIN "public"."d" DROP CONSTRAINT "positive_only";`) {
		t.Fatalf("expected DROP CONSTRAINT op, got: %v", sqlList(ops))
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "DROP CONSTRAINT") && o.Safety() != pipeline.Safe {
			t.Errorf("safety = %v, want Safe (RFC §5.4: dropping a constraint is SAFE)", o.Safety())
		}
	}
}

func TestDiffDomainConstraintChangedDropsAndAdds(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.d", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{
			Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: "integer",
			DomainConstraints: []snapshot.SnapConstraint{{Name: "positive_only", Type: "CHECK", Expr: "CHECK (VALUE > 0)"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: ir.TypeRef{Name: "integer"},
			DomainConstraints: []*ir.Constraint{{Name: "positive_only", Type: "CHECK", Expr: "CHECK (VALUE > 10)"}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP CONSTRAINT "positive_only"`) || !containsSQL(ops, "VALUE > 10") {
		t.Errorf("expected DROP + ADD CONSTRAINT for a changed expression, got: %v", sqlList(ops))
	}
}

func TestDiffDomainBaseTypeChangedRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.d", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: "integer"},
	})
	desired := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: ir.TypeRef{Name: "bigint"}},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP DOMAIN IF EXISTS "public"."d" CASCADE;`) {
		t.Errorf("expected DROP DOMAIN CASCADE, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "AS bigint") {
		t.Errorf("expected recreated CREATE DOMAIN with new base type, got: %v", sqlList(ops))
	}
	var dropOp pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "DROP DOMAIN") {
			dropOp = o
		}
	}
	if dropOp == nil || dropOp.Safety() != pipeline.Destructive {
		t.Errorf("DROP DOMAIN safety = %v, want Destructive", dropOp)
	}
}

func TestDiffDomainUnchangedIsNoop(t *testing.T) {
	d := New()
	def := "1"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.d", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{
			Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: "integer",
			DomainDefault: &def, DomainNotNull: true,
			DomainConstraints: []snapshot.SnapConstraint{{Name: "positive_only", Type: "CHECK", Expr: "CHECK (VALUE > 0)"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "d", Variant: "DOMAIN", DomainBaseType: ir.TypeRef{Name: "integer"},
			DomainDefault: &def, DomainNotNull: true,
			DomainConstraints: []*ir.Constraint{{Name: "positive_only", Type: "CHECK", Expr: "CHECK (VALUE > 0)"}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for an unchanged domain, got: %v", sqlList(ops))
	}
}

// Regression guard for a real bug found live-testing a demo project's
// pre-existing email_address domain: a snapshot written before domain
// structured-field tracking existed has DomainBaseType == "" (Go zero
// value), which used to compare unequal to any real desired base type and
// spuriously trigger the destructive DROP DOMAIN CASCADE + recreate branch
// on the very first plan after upgrading — for a domain that never actually
// changed. Must instead skip structural comparison and self-heal via a
// harmless comment op so the snapshot gets refreshed on apply.
func TestDiffDomainStaleSnapshotDoesNotRecreate(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.email_address", &snapshot.SnapObject{
		Kind: "type",
		Type: &snapshot.SnapType{Schema: "public", Name: "email_address", Variant: "DOMAIN"},
	})
	desired := []pipeline.IRObject{
		&ir.Type{
			Schema: "public", Name: "email_address", Variant: "DOMAIN",
			DomainBaseType:    ir.TypeRef{Name: "text"},
			DomainConstraints: []*ir.Constraint{{Name: "email_address_check", Type: "CHECK", Expr: "CHECK (value ~ '^[^@]+@[^@]+$')"}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP DOMAIN") {
		t.Errorf("stale snapshot must not trigger DROP DOMAIN, got: %v", sqlList(ops))
	}
	for _, o := range ops {
		if o.Safety() == pipeline.Destructive {
			t.Errorf("stale snapshot transition must never be destructive, got: %v", sqlList(ops))
		}
	}
}

// ── Tablespace/Cast/EventTrigger structured diffing (G-live closure) ──────────
// Regression guards for the "G-live" gap: the 9 reconstruction-tier opaque
// kinds set Reconstructed=true on introspection, which forced BodyHash to ""
// on the live side (sourceBodyHash), silently disabling diffOpaqueIR's
// body-hash comparison on verify/plan --live — a live-catalog-only change to
// one of these 3 kinds went completely undetected. diffTablespace/diffCast/
// diffEventTrigger replace that with field-level comparison (RFC
// §14.7/§14.5/§14.1), which works identically whether the snapshot side came
// from source or from introspection, since it never depends on Reconstructed
// or BodyHash at all.

func TestDiffTablespaceLocationChangeIsDestructiveManual(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("ts", &snapshot.SnapObject{
		Kind: "tablespace",
		Opaque: &snapshot.SnapOpaque{
			Kind: "tablespace", Name: "ts", TablespaceLocation: "/data/ts1",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Tablespace{Name: "ts", Location: "/data/ts2", Body: "CREATE TABLESPACE ts LOCATION '/data/ts2'"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP TABLESPACE IF EXISTS "ts"`) {
		t.Errorf("expected DROP TABLESPACE, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "/data/ts2") {
		t.Errorf("expected recreate to reflect the new location, got: %v", sqlList(ops))
	}
	// DROP TABLESPACE is Destructive (dropObject's destructiveManualOp);
	// CREATE TABLESPACE is Manual (createOpaque's tablespace special-case)
	// — both non-transactional (CREATE/DROP TABLESPACE cannot run inside a
	// transaction block), but Safety() and Transactional() are independent
	// fields (see destructiveManualOp's doc comment), so the two ops carry
	// different Safety() values despite both being non-transactional.
	for _, op := range ops {
		switch {
		case strings.Contains(op.SQL(), "DROP TABLESPACE"):
			if op.Safety() != pipeline.Destructive {
				t.Errorf("expected DROP TABLESPACE op to be Destructive safety, got %v: %s", op.Safety(), op.SQL())
			}
		case strings.Contains(op.SQL(), "CREATE TABLESPACE"):
			if op.Safety() != pipeline.Manual {
				t.Errorf("expected CREATE TABLESPACE op to be Manual safety, got %v: %s", op.Safety(), op.SQL())
			}
		}
	}
}

func TestDiffTablespaceUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("ts", &snapshot.SnapObject{
		Kind: "tablespace",
		Opaque: &snapshot.SnapOpaque{
			Kind: "tablespace", Name: "ts", TablespaceLocation: "/data/ts1",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Tablespace{Name: "ts", Location: "/data/ts1", Body: "CREATE TABLESPACE ts LOCATION '/data/ts1'"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for an unchanged tablespace, got: %v", sqlList(ops))
	}
}

// TestDiffTablespaceStaleSnapshotDoesNotRecreate is the G-live counterpart to
// TestDiffDomainStaleSnapshotDoesNotRecreate: a snapshot written before
// TablespaceLocation existed has the Go zero value "" even though the
// tablespace is real — without the guard, every already-applied tablespace
// would look like a spurious location change (DROP TABLESPACE, destructive)
// on the very first plan/apply after upgrading.
func TestDiffTablespaceStaleSnapshotDoesNotRecreate(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("ts", &snapshot.SnapObject{
		Kind:   "tablespace",
		Opaque: &snapshot.SnapOpaque{Kind: "tablespace", Name: "ts"},
	})
	desired := []pipeline.IRObject{
		&ir.Tablespace{Name: "ts", Location: "/data/ts1", Body: "CREATE TABLESPACE ts LOCATION '/data/ts1'"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP TABLESPACE") {
		t.Errorf("stale snapshot must not trigger DROP TABLESPACE, got: %v", sqlList(ops))
	}
	for _, o := range ops {
		if o.Safety() == pipeline.Destructive || o.Safety() == pipeline.Manual {
			t.Errorf("stale snapshot transition must never be destructive/manual, got: %v", sqlList(ops))
		}
	}
}

func TestDiffCastFunctionChangeRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("int4->text", &snapshot.SnapObject{
		Kind: "cast",
		Opaque: &snapshot.SnapOpaque{
			Kind: "cast", Name: "int4->text", CastMethod: "f", CastContext: "e", CastFunction: "public.f1",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Cast{
			SourceType: ir.TypeRef{Name: "int4"}, TargetType: ir.TypeRef{Name: "text"},
			Method: "f", Context: "e", Function: "f2", // unqualified — must still compare equal via qualifyFuncForCompare against a *different* function
			Body: "CREATE CAST (int4 AS text) WITH FUNCTION f2(int4)",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "DROP CAST IF EXISTS (int4 AS text)") {
		t.Errorf("expected DROP CAST, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "f2(int4)") {
		t.Errorf("expected recreate to reflect the new function, got: %v", sqlList(ops))
	}
}

// TestDiffCastFunctionUnqualifiedMatchesQualifiedNoOp proves
// qualifyFuncForCompare's reuse is wired correctly: introspection always
// returns a schema-qualified CastFunction ("public.f1"), while hand-written
// source commonly leaves it unqualified ("f1") relying on the default
// "public" schema — these must compare equal, not spuriously recreate.
func TestDiffCastFunctionUnqualifiedMatchesQualifiedNoOp(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("int4->text", &snapshot.SnapObject{
		Kind: "cast",
		Opaque: &snapshot.SnapOpaque{
			Kind: "cast", Name: "int4->text", CastMethod: "f", CastContext: "e", CastFunction: "public.f1",
		},
	})
	desired := []pipeline.IRObject{
		&ir.Cast{
			SourceType: ir.TypeRef{Name: "int4"}, TargetType: ir.TypeRef{Name: "text"},
			Method: "f", Context: "e", Function: "f1",
			Body: "CREATE CAST (int4 AS text) WITH FUNCTION f1(int4)",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for a schema-unqualified-vs-qualified match, got: %v", sqlList(ops))
	}
}

// TestDiffCastStaleSnapshotDoesNotRecreate mirrors
// TestDiffTablespaceStaleSnapshotDoesNotRecreate: CastMethod == "" (Go zero
// value) must be treated as "not yet populated," not "changed."
func TestDiffCastStaleSnapshotDoesNotRecreate(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("int4->text", &snapshot.SnapObject{
		Kind:   "cast",
		Opaque: &snapshot.SnapOpaque{Kind: "cast", Name: "int4->text"},
	})
	desired := []pipeline.IRObject{
		&ir.Cast{
			SourceType: ir.TypeRef{Name: "int4"}, TargetType: ir.TypeRef{Name: "text"},
			Method: "f", Context: "e", Function: "f1",
			Body: "CREATE CAST (int4 AS text) WITH FUNCTION f1(int4)",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP CAST") {
		t.Errorf("stale snapshot must not trigger DROP CAST, got: %v", sqlList(ops))
	}
}

func TestDiffEventTriggerTagChangeRecreatesAndIsSafe(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("et", &snapshot.SnapObject{
		Kind: "event_trigger",
		Opaque: &snapshot.SnapOpaque{
			Kind: "event_trigger", Name: "et",
			EventTriggerEvent: "sql_drop", EventTriggerTags: []string{"DROP TABLE"}, EventTriggerFunction: "public.f1",
		},
	})
	desired := []pipeline.IRObject{
		&ir.EventTrigger{
			Name: "et", Event: "sql_drop", Tags: []string{"DROP TABLE", "DROP SCHEMA"}, Function: "f1",
			Body: "CREATE EVENT TRIGGER et ON sql_drop WHEN TAG IN ('DROP TABLE', 'DROP SCHEMA') EXECUTE FUNCTION f1()",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP EVENT TRIGGER IF EXISTS "et"`) {
		t.Errorf("expected DROP EVENT TRIGGER, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "DROP SCHEMA") {
		t.Errorf("expected recreate to reflect the new tag list, got: %v", sqlList(ops))
	}
	// RFC §14.1: event trigger DROP+CREATE is SAFE (no data involved),
	// unlike every other opaque-tier DROP+CREATE.
	for _, op := range ops {
		if op.Safety() != pipeline.Safe {
			t.Errorf("expected event trigger DROP+CREATE ops to be Safe, got %v: %s", op.Safety(), op.SQL())
		}
	}
}

// TestDiffEventTriggerTagOrderIsNotDrift proves tags are compared as a set
// (stringSetEqual), not positionally — PostgreSQL's evttags is an unordered
// array and WHEN TAG IN (...)'s list order carries no meaning.
func TestDiffEventTriggerTagOrderIsNotDrift(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("et", &snapshot.SnapObject{
		Kind: "event_trigger",
		Opaque: &snapshot.SnapOpaque{
			Kind: "event_trigger", Name: "et",
			EventTriggerEvent: "sql_drop", EventTriggerTags: []string{"DROP TABLE", "DROP SCHEMA"}, EventTriggerFunction: "public.f1",
		},
	})
	desired := []pipeline.IRObject{
		&ir.EventTrigger{
			Name: "et", Event: "sql_drop", Tags: []string{"DROP SCHEMA", "DROP TABLE"}, Function: "f1",
			Body: "CREATE EVENT TRIGGER et ON sql_drop WHEN TAG IN ('DROP SCHEMA', 'DROP TABLE') EXECUTE FUNCTION f1()",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for a reordered-but-identical tag set, got: %v", sqlList(ops))
	}
}

// TestDiffEventTriggerStaleSnapshotDoesNotRecreate mirrors
// TestDiffTablespaceStaleSnapshotDoesNotRecreate: EventTriggerEvent == ""
// (Go zero value) must be treated as "not yet populated," not "changed."
func TestDiffEventTriggerStaleSnapshotDoesNotRecreate(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("et", &snapshot.SnapObject{
		Kind:   "event_trigger",
		Opaque: &snapshot.SnapOpaque{Kind: "event_trigger", Name: "et"},
	})
	desired := []pipeline.IRObject{
		&ir.EventTrigger{
			Name: "et", Event: "sql_drop", Tags: []string{"DROP TABLE"}, Function: "f1",
			Body: "CREATE EVENT TRIGGER et ON sql_drop WHEN TAG IN ('DROP TABLE') EXECUTE FUNCTION f1()",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP EVENT TRIGGER") {
		t.Errorf("stale snapshot must not trigger DROP EVENT TRIGGER, got: %v", sqlList(ops))
	}
}

// ── FDW/ForeignServer/UserMapping structured OPTIONS diffing (G-live Tier 2) ──
// Same G-live gap as Tier 1 (Tablespace/Cast/EventTrigger): Reconstructed
// forced BodyHash to "" on the live side, silently disabling diffOpaqueIR's
// comparison on verify/plan --live. diffFDW/diffForeignServer/diffUserMapping
// replace that with field-level comparison, gated by the explicit
// OptionsStructured sentinel (none of these 3 kinds has a field guaranteed
// non-empty the way Tier 1's did).

func TestDiffFDWOptionsChangeRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("myfdw", &snapshot.SnapObject{
		Kind: "fdw",
		Opaque: &snapshot.SnapOpaque{
			Kind: "fdw", Name: "myfdw", OptionsStructured: true,
			FDWOptions: []snapshot.SnapOptionKV{{Key: "debug", Value: "false"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.ForeignDataWrapper{
			Name: "myfdw", Options: []pipeline.StorageParam{{Key: "debug", Value: "true"}},
			Body: "CREATE FOREIGN DATA WRAPPER myfdw OPTIONS (debug 'true')",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP FOREIGN DATA WRAPPER IF EXISTS "myfdw"`) {
		t.Errorf("expected DROP FOREIGN DATA WRAPPER, got: %v", sqlList(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), "DROP FOREIGN DATA WRAPPER") && op.Safety() != pipeline.Destructive {
			t.Errorf("expected DROP FOREIGN DATA WRAPPER to be Destructive (RFC §14.8), got %v: %s", op.Safety(), op.SQL())
		}
	}
}

func TestDiffFDWUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("myfdw", &snapshot.SnapObject{
		Kind: "fdw",
		Opaque: &snapshot.SnapOpaque{
			Kind: "fdw", Name: "myfdw", OptionsStructured: true,
			FDWHandler: "public.h1", FDWOptions: []snapshot.SnapOptionKV{{Key: "debug", Value: "true"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.ForeignDataWrapper{
			Name: "myfdw", Handler: "h1", // unqualified — must match "public.h1" via qualifyFuncForCompare
			Options: []pipeline.StorageParam{{Key: "debug", Value: "true"}},
			Body:    "CREATE FOREIGN DATA WRAPPER myfdw HANDLER h1 OPTIONS (debug 'true')",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for an unchanged FDW, got: %v", sqlList(ops))
	}
}

func TestDiffFDWStaleSnapshotDoesNotRecreate(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("myfdw", &snapshot.SnapObject{
		Kind:   "fdw",
		Opaque: &snapshot.SnapOpaque{Kind: "fdw", Name: "myfdw"},
	})
	desired := []pipeline.IRObject{
		&ir.ForeignDataWrapper{Name: "myfdw", Handler: "h1", Body: "CREATE FOREIGN DATA WRAPPER myfdw HANDLER h1"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP FOREIGN DATA WRAPPER") {
		t.Errorf("stale snapshot must not trigger DROP FOREIGN DATA WRAPPER, got: %v", sqlList(ops))
	}
}

func TestDiffForeignServerOptionsChangeIsSafeAlter(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("srv", &snapshot.SnapObject{
		Kind: "server",
		Opaque: &snapshot.SnapOpaque{
			Kind: "server", Name: "srv", OptionsStructured: true, ServerFDWName: "myfdw",
			ServerOptions: []snapshot.SnapOptionKV{{Key: "host", Value: "a"}, {Key: "port", Value: "5432"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.ForeignServer{
			Name: "srv", FDWName: "myfdw",
			Options: []pipeline.StorageParam{{Key: "host", Value: "b"}, {Key: "dbname", Value: "mydb"}},
			Body:    "CREATE SERVER srv FOREIGN DATA WRAPPER myfdw OPTIONS (host 'b', dbname 'mydb')",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 op (targeted ALTER SERVER, RFC §14.9), got %d: %v", len(ops), sqlList(ops))
	}
	sql := ops[0].SQL()
	if !strings.HasPrefix(sql, "ALTER SERVER") {
		t.Errorf("expected a targeted ALTER SERVER, not DROP+CREATE, got: %s", sql)
	}
	if ops[0].Safety() != pipeline.Safe {
		t.Errorf("expected ALTER SERVER OPTIONS to be Safe (RFC §14.9), got %v: %s", ops[0].Safety(), sql)
	}
	if !strings.Contains(sql, "SET host 'b'") {
		t.Errorf("expected SET host 'b', got: %s", sql)
	}
	if !strings.Contains(sql, "ADD dbname 'mydb'") {
		t.Errorf("expected ADD dbname 'mydb', got: %s", sql)
	}
	if !strings.Contains(sql, "DROP port") {
		t.Errorf("expected DROP port, got: %s", sql)
	}
}

func TestDiffForeignServerVersionChangeIsSafeAlter(t *testing.T) {
	d := New()
	v1 := "1.0"
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("srv", &snapshot.SnapObject{
		Kind: "server",
		Opaque: &snapshot.SnapOpaque{
			Kind: "server", Name: "srv", OptionsStructured: true, ServerFDWName: "myfdw", ServerVersion: &v1,
		},
	})
	v2 := "2.0"
	desired := []pipeline.IRObject{
		&ir.ForeignServer{Name: "srv", FDWName: "myfdw", Version: &v2, Body: "CREATE SERVER srv VERSION '2.0' FOREIGN DATA WRAPPER myfdw"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 op (targeted ALTER SERVER VERSION), got %d: %v", len(ops), sqlList(ops))
	}
	if !strings.Contains(ops[0].SQL(), "VERSION '2.0'") || ops[0].Safety() != pipeline.Safe {
		t.Errorf("expected Safe ALTER SERVER VERSION '2.0', got %v: %s", ops[0].Safety(), ops[0].SQL())
	}
}

func TestDiffForeignServerFDWChangeRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("srv", &snapshot.SnapObject{
		Kind: "server",
		Opaque: &snapshot.SnapOpaque{
			Kind: "server", Name: "srv", OptionsStructured: true, ServerFDWName: "fdw1",
		},
	})
	desired := []pipeline.IRObject{
		&ir.ForeignServer{Name: "srv", FDWName: "fdw2", Body: "CREATE SERVER srv FOREIGN DATA WRAPPER fdw2"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP SERVER IF EXISTS "srv"`) {
		t.Errorf("expected DROP SERVER for a FDW-wrapper change, got: %v", sqlList(ops))
	}
}

func TestDiffForeignServerStaleSnapshotDoesNotRecreate(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("srv", &snapshot.SnapObject{
		Kind:   "server",
		Opaque: &snapshot.SnapOpaque{Kind: "server", Name: "srv"},
	})
	desired := []pipeline.IRObject{
		&ir.ForeignServer{Name: "srv", FDWName: "myfdw", Body: "CREATE SERVER srv FOREIGN DATA WRAPPER myfdw"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP SERVER") {
		t.Errorf("stale snapshot must not trigger DROP SERVER, got: %v", sqlList(ops))
	}
}

// TestDiffUserMappingLiveOnlyNonSensitiveOptionsChangeDetected is the actual
// G-live proof for UserMapping: on the live path (snap.BodyHash == "", i.e.
// Reconstructed), a non-sensitive OPTIONS change must now be detected via
// the new structural comparison — before this fix, diffUserMapping's sole
// check (snap.BodyHash == "" || newHash == snap.BodyHash) treated
// snap.BodyHash == "" as unconditionally "no change."
func TestDiffUserMappingLiveOnlyNonSensitiveOptionsChangeDetected(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("app@srv", &snapshot.SnapObject{
		Kind: "user_mapping",
		Opaque: &snapshot.SnapOpaque{
			Kind: "user_mapping", Name: "app@srv", OptionsStructured: true,
			UserMappingOptions: []snapshot.SnapOptionKV{{Key: "user", Value: "app_v1"}},
			// BodyHash intentionally "" — simulates the introspected/live side.
		},
	})
	desired := []pipeline.IRObject{
		&ir.UserMapping{
			User: "app", Server: "srv",
			Options: []pipeline.StorageParam{{Key: "user", Value: "app_v2"}},
			Body:    "CREATE USER MAPPING FOR app SERVER srv OPTIONS (user 'app_v2')",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP USER MAPPING IF EXISTS FOR "app" SERVER "srv"`) {
		t.Fatalf("G-live gap not closed: expected the live-only OPTIONS change to be detected, got %d ops: %v", len(ops), sqlList(ops))
	}
}

// TestDiffUserMappingLiveOnlyPasswordChangeStaysUndetected documents the
// remaining, genuinely inherent limitation (RFC §24): the live side can
// never expose a real password value to compare against, only a fixed
// redaction placeholder, so a live-only password change cannot be detected
// — this is a deliberate scope boundary, not an oversight. Non-sensitive
// OPTIONS on the exact same mapping still work (proven above).
func TestDiffUserMappingLiveOnlyPasswordChangeStaysUndetected(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("app@srv", &snapshot.SnapObject{
		Kind: "user_mapping",
		Opaque: &snapshot.SnapOpaque{
			Kind: "user_mapping", Name: "app@srv", OptionsStructured: true,
			UserMappingOptions: nil, // password-like keys are never stored
		},
	})
	desired := []pipeline.IRObject{
		&ir.UserMapping{
			User: "app", Server: "srv",
			Options: []pipeline.StorageParam{{Key: "password", Value: "{{vault:secret/db#new}}"}},
			Body:    "CREATE USER MAPPING FOR app SERVER srv OPTIONS (password '{{vault:secret/db#new}}')",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops (password changes are undetectable on the live path, by design), got: %v", sqlList(ops))
	}
}

func TestDiffUserMappingStaleSnapshotDoesNotRecreate(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("app@srv", &snapshot.SnapObject{
		Kind:   "user_mapping",
		Opaque: &snapshot.SnapOpaque{Kind: "user_mapping", Name: "app@srv"},
	})
	desired := []pipeline.IRObject{
		&ir.UserMapping{User: "app", Server: "srv", Body: "CREATE USER MAPPING FOR app SERVER srv OPTIONS (user 'app')"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP USER MAPPING") {
		t.Errorf("stale snapshot must not trigger DROP USER MAPPING, got: %v", sqlList(ops))
	}
}

// ── Publication structured diffing (G-live Tier 3) ────────────────────────────
// Same G-live gap as Tiers 1/2: Reconstructed forced BodyHash to "" on the
// live side, silently disabling diffOpaqueIR's comparison on verify/
// plan --live. diffPublication replaces that with field-level comparison,
// gated by the explicit PublicationStructured sentinel (AllTables can
// legitimately be false and Insert/Update/Delete/Truncate default true, so
// no single field is a reliable "not yet populated" signal).

func TestDiffPublicationTablesChangeIsSafeAlter(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("p", &snapshot.SnapObject{
		Kind: "publication",
		Opaque: &snapshot.SnapOpaque{
			Kind: "publication", Name: "p", PublicationStructured: true,
			PublicationTables: []string{"public.t1"},
			PublicationInsert: true, PublicationUpdate: true, PublicationDelete: true, PublicationTruncate: true,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Publication{
			Name: "p", Tables: []ir.PublicationTableRef{{Schema: "public", Name: "t1"}, {Schema: "public", Name: "t2"}},
			Insert: true, Update: true, Delete: true, Truncate: true,
			Body: "CREATE PUBLICATION p FOR TABLE public.t1, public.t2",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 op (targeted ALTER PUBLICATION SET TABLE, RFC §13.1), got %d: %v", len(ops), sqlList(ops))
	}
	sql := ops[0].SQL()
	if !strings.HasPrefix(sql, "ALTER PUBLICATION") || !strings.Contains(sql, "SET TABLE") {
		t.Errorf("expected a targeted ALTER PUBLICATION ... SET TABLE, not DROP+CREATE, got: %s", sql)
	}
	if ops[0].Safety() != pipeline.Safe {
		t.Errorf("expected ALTER PUBLICATION SET TABLE to be Safe (RFC §13.1), got %v: %s", ops[0].Safety(), sql)
	}
	if !strings.Contains(sql, `"public"."t1"`) || !strings.Contains(sql, `"public"."t2"`) {
		t.Errorf("expected both tables in the SET TABLE list, got: %s", sql)
	}
}

func TestDiffPublicationUnqualifiedTableMatchesQualifiedNoOp(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("p", &snapshot.SnapObject{
		Kind: "publication",
		Opaque: &snapshot.SnapOpaque{
			Kind: "publication", Name: "p", PublicationStructured: true,
			PublicationTables: []string{"public.t1"}, // introspection always qualifies
			PublicationInsert: true, PublicationUpdate: true, PublicationDelete: true, PublicationTruncate: true,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Publication{
			Name: "p", Tables: []ir.PublicationTableRef{{Name: "t1"}}, // unqualified in source
			Insert: true, Update: true, Delete: true, Truncate: true,
			Body: "CREATE PUBLICATION p FOR TABLE t1",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for a schema-unqualified-vs-qualified table match, got: %v", sqlList(ops))
	}
}

func TestDiffPublicationPublishOptionsChangeIsSafeAlter(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("p", &snapshot.SnapObject{
		Kind: "publication",
		Opaque: &snapshot.SnapOpaque{
			Kind: "publication", Name: "p", PublicationStructured: true, PublicationAllTables: true,
			PublicationInsert: true, PublicationUpdate: true, PublicationDelete: true, PublicationTruncate: true,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Publication{
			Name: "p", AllTables: true, Insert: true, Update: false, Delete: false, Truncate: true,
			Body: "CREATE PUBLICATION p FOR ALL TABLES WITH (publish = 'insert, truncate')",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 op (targeted ALTER PUBLICATION SET (publish=...)), got %d: %v", len(ops), sqlList(ops))
	}
	sql := ops[0].SQL()
	if !strings.HasPrefix(sql, "ALTER PUBLICATION") || !strings.Contains(sql, "SET (publish") {
		t.Errorf("expected a targeted ALTER PUBLICATION ... SET (publish = ...), got: %s", sql)
	}
	if ops[0].Safety() != pipeline.Safe {
		t.Errorf("expected ALTER PUBLICATION SET (publish) to be Safe, got %v: %s", ops[0].Safety(), sql)
	}
	if !strings.Contains(sql, "'insert, truncate'") {
		t.Errorf("expected publish = 'insert, truncate', got: %s", sql)
	}
}

func TestDiffPublicationAllTablesChangeRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("p", &snapshot.SnapObject{
		Kind: "publication",
		Opaque: &snapshot.SnapOpaque{
			Kind: "publication", Name: "p", PublicationStructured: true, PublicationAllTables: true,
			PublicationInsert: true, PublicationUpdate: true, PublicationDelete: true, PublicationTruncate: true,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Publication{
			Name: "p", Tables: []ir.PublicationTableRef{{Schema: "public", Name: "t1"}},
			Insert: true, Update: true, Delete: true, Truncate: true,
			Body: "CREATE PUBLICATION p FOR TABLE public.t1",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP PUBLICATION IF EXISTS "p"`) {
		t.Errorf("expected DROP PUBLICATION for an AllTables change, got: %v", sqlList(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), "DROP PUBLICATION") && op.Safety() != pipeline.Destructive {
			t.Errorf("expected DROP PUBLICATION to be Destructive (RFC §13.1), got %v: %s", op.Safety(), op.SQL())
		}
	}
}

func TestDiffPublicationUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("p", &snapshot.SnapObject{
		Kind: "publication",
		Opaque: &snapshot.SnapOpaque{
			Kind: "publication", Name: "p", PublicationStructured: true, PublicationAllTables: true,
			PublicationInsert: true, PublicationUpdate: true, PublicationDelete: true, PublicationTruncate: true,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Publication{Name: "p", AllTables: true, Insert: true, Update: true, Delete: true, Truncate: true, Body: "CREATE PUBLICATION p FOR ALL TABLES"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for an unchanged publication, got: %v", sqlList(ops))
	}
}

func TestDiffPublicationStaleSnapshotDoesNotRecreate(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("p", &snapshot.SnapObject{
		Kind:   "publication",
		Opaque: &snapshot.SnapOpaque{Kind: "publication", Name: "p"},
	})
	desired := []pipeline.IRObject{
		&ir.Publication{Name: "p", AllTables: true, Insert: true, Update: true, Delete: true, Truncate: true, Body: "CREATE PUBLICATION p FOR ALL TABLES"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP PUBLICATION") {
		t.Errorf("stale snapshot must not trigger DROP PUBLICATION, got: %v", sqlList(ops))
	}
}

// TestDiffPublicationFilteredTableChangeFallsBackToDropCreate is a
// correctness regression guard, not just a G-live proof: a Tables-set
// change on a publication using a column-list/WHERE filter (real syntax:
// FOR TABLE t (col1, col2) WHERE (expr)) must NOT be rendered as
// ALTER PUBLICATION ... SET TABLE from PublicationTableRef alone — doing
// so would silently rebuild the table list without the filter, an
// unintentional widening of what's replicated. Falling back to DROP+CREATE
// (which regenerates from the complete Body) is the only safe option here.
func TestDiffPublicationFilteredTableChangeFallsBackToDropCreate(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("p", &snapshot.SnapObject{
		Kind: "publication",
		Opaque: &snapshot.SnapOpaque{
			Kind: "publication", Name: "p", PublicationStructured: true,
			PublicationTables: []string{"public.orders"}, PublicationHasFilteredTables: true,
			PublicationInsert: true, PublicationUpdate: true, PublicationDelete: true, PublicationTruncate: true,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Publication{
			Name: "p",
			Tables: []ir.PublicationTableRef{
				{Schema: "public", Name: "orders"},
				{Schema: "public", Name: "line_items"},
			},
			HasFilteredTables: true,
			Insert:            true, Update: true, Delete: true, Truncate: true,
			Body: "CREATE PUBLICATION p FOR TABLE public.orders (id, status) WHERE (status = 'active'), public.line_items",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "SET TABLE") {
		t.Fatalf("expected the lossy ALTER PUBLICATION SET TABLE path to be avoided for a filtered publication, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, `DROP PUBLICATION IF EXISTS "p"`) {
		t.Errorf("expected a fallback DROP+CREATE, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "line_items") {
		t.Errorf("expected the recreate to include the new table, got: %v", sqlList(ops))
	}
}

// ── Collation structured diffing (G-live Tier 4) ───────────────────────────────
// Same G-live gap as Tiers 1-3: Reconstructed forced BodyHash to "" on the
// live side, silently disabling diffOpaqueIR's comparison on verify/
// plan --live. diffCollation replaces that with field-level comparison,
// gated by the explicit CollationStructured sentinel (Provider "c" is
// itself PostgreSQL's real default, not a reliable "unpopulated" signal).

func TestDiffCollationLocaleChangeRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.c", &snapshot.SnapObject{
		Kind: "collation",
		Opaque: &snapshot.SnapOpaque{
			Kind: "collation", Schema: "public", Name: "c", CollationStructured: true,
			CollationProvider: "c", CollationCollate: strPtr("C"), CollationCtype: strPtr("C"), CollationDeterministic: true,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Collation{
			Schema: "public", Name: "c", Provider: "c", Collate: strPtr("POSIX"), Ctype: strPtr("POSIX"), Deterministic: true,
			Body: "CREATE COLLATION public.c (LOCALE = 'POSIX')",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP COLLATION IF EXISTS "public"."c"`) {
		t.Errorf("expected DROP COLLATION, got: %v", sqlList(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), "DROP COLLATION") && op.Safety() != pipeline.Destructive {
			t.Errorf("expected DROP COLLATION to be Destructive (RFC §14.2), got %v: %s", op.Safety(), op.SQL())
		}
	}
}

func TestDiffCollationDeterministicChangeRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.c", &snapshot.SnapObject{
		Kind: "collation",
		Opaque: &snapshot.SnapOpaque{
			Kind: "collation", Schema: "public", Name: "c", CollationStructured: true,
			CollationProvider: "i", CollationICULocale: strPtr("und-u-ks-level2"), CollationDeterministic: true,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Collation{
			Schema: "public", Name: "c", Provider: "i", ICULocale: strPtr("und-u-ks-level2"), Deterministic: false,
			Body: "CREATE COLLATION public.c (PROVIDER = icu, LOCALE = 'und-u-ks-level2', DETERMINISTIC = false)",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP COLLATION IF EXISTS "public"."c"`) {
		t.Errorf("expected DROP COLLATION for a DETERMINISTIC change, got: %v", sqlList(ops))
	}
}

func TestDiffCollationUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.c", &snapshot.SnapObject{
		Kind: "collation",
		Opaque: &snapshot.SnapOpaque{
			Kind: "collation", Schema: "public", Name: "c", CollationStructured: true,
			CollationProvider: "c", CollationCollate: strPtr("C"), CollationCtype: strPtr("C"), CollationDeterministic: true,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Collation{
			Schema: "public", Name: "c", Provider: "c", Collate: strPtr("C"), Ctype: strPtr("C"), Deterministic: true,
			Body: "CREATE COLLATION public.c (LOCALE = 'C')",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for an unchanged collation, got: %v", sqlList(ops))
	}
}

func TestDiffCollationStaleSnapshotDoesNotRecreate(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.c", &snapshot.SnapObject{
		Kind:   "collation",
		Opaque: &snapshot.SnapOpaque{Kind: "collation", Schema: "public", Name: "c"},
	})
	desired := []pipeline.IRObject{
		&ir.Collation{
			Schema: "public", Name: "c", Provider: "c", Collate: strPtr("C"), Ctype: strPtr("C"), Deterministic: true,
			Body: "CREATE COLLATION public.c (LOCALE = 'C')",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP COLLATION") {
		t.Errorf("stale snapshot must not trigger DROP COLLATION, got: %v", sqlList(ops))
	}
}

// ── StatisticsObject structured diffing (G-live Tier 4, closing) ──────────────
// Same G-live gap as every kind before it in this tier: Reconstructed
// forced BodyHash to "" on the live side, silently disabling diffOpaqueIR's
// comparison on verify/plan --live. diffStatisticsObject replaces that with
// field-level comparison, gated by the explicit StatisticsStructured
// sentinel (an empty Kinds/Columns list is not itself a reliable
// "unpopulated" signal).

func TestDiffStatisticsObjectKindsChangeRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.st", &snapshot.SnapObject{
		Kind: "statistics",
		Opaque: &snapshot.SnapOpaque{
			Kind: "statistics", Schema: "public", Name: "st", StatisticsStructured: true,
			StatisticsTable: "public.orders", StatisticsKinds: []string{"ndistinct"}, StatisticsColumns: []string{"a", "b"},
		},
	})
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{
			Schema: "public", Name: "st", Table: "public.orders", Kinds: []string{"ndistinct", "dependencies"}, Columns: []string{"a", "b"},
			Body: "CREATE STATISTICS public.st (ndistinct, dependencies) ON a, b FROM orders",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP STATISTICS IF EXISTS "public"."st"`) {
		t.Errorf("expected DROP STATISTICS for a kinds change, got: %v", sqlList(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), "DROP STATISTICS") && op.Safety() != pipeline.Destructive {
			t.Errorf("expected DROP STATISTICS to be Destructive (RFC §14.6), got %v: %s", op.Safety(), op.SQL())
		}
	}
}

func TestDiffStatisticsObjectColumnsChangeRecreates(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.st", &snapshot.SnapObject{
		Kind: "statistics",
		Opaque: &snapshot.SnapOpaque{
			Kind: "statistics", Schema: "public", Name: "st", StatisticsStructured: true,
			StatisticsTable: "public.orders", StatisticsKinds: []string{"ndistinct"}, StatisticsColumns: []string{"a", "b"},
		},
	})
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{
			Schema: "public", Name: "st", Table: "public.orders", Kinds: []string{"ndistinct"}, Columns: []string{"a", "b", "c"},
			Body: "CREATE STATISTICS public.st (ndistinct) ON a, b, c FROM orders",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `DROP STATISTICS IF EXISTS "public"."st"`) {
		t.Errorf("expected DROP STATISTICS for a columns change, got: %v", sqlList(ops))
	}
}

func TestDiffStatisticsObjectUnqualifiedTableMatchesQualifiedNoOp(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.st", &snapshot.SnapObject{
		Kind: "statistics",
		Opaque: &snapshot.SnapOpaque{
			Kind: "statistics", Schema: "public", Name: "st", StatisticsStructured: true,
			StatisticsTable: "public.orders", StatisticsKinds: []string{"ndistinct"}, StatisticsColumns: []string{"a", "b"},
		},
	})
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{
			Schema: "public", Name: "st", Table: "orders", Kinds: []string{"ndistinct"}, Columns: []string{"a", "b"},
			Body: "CREATE STATISTICS public.st (ndistinct) ON a, b FROM orders",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for a schema-unqualified-vs-qualified table match, got: %v", sqlList(ops))
	}
}

func TestDiffStatisticsObjectTargetChangeIsSafeAlter(t *testing.T) {
	d := New()
	oldTarget := 100
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.st", &snapshot.SnapObject{
		Kind: "statistics",
		Opaque: &snapshot.SnapOpaque{
			Kind: "statistics", Schema: "public", Name: "st", StatisticsStructured: true,
			StatisticsTable: "public.orders", StatisticsKinds: []string{"ndistinct"}, StatisticsColumns: []string{"a", "b"},
			StatisticsTarget: &oldTarget,
		},
	})
	newTarget := 500
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{
			Schema: "public", Name: "st", Table: "public.orders", Kinds: []string{"ndistinct"}, Columns: []string{"a", "b"},
			StatisticsTarget: &newTarget,
			Body:             "CREATE STATISTICS public.st (ndistinct) ON a, b FROM orders",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected exactly 1 op (targeted ALTER STATISTICS SET STATISTICS), got %d: %v", len(ops), sqlList(ops))
	}
	sql := ops[0].SQL()
	if !strings.HasPrefix(sql, "ALTER STATISTICS") || !strings.Contains(sql, "SET STATISTICS 500") {
		t.Errorf("expected ALTER STATISTICS ... SET STATISTICS 500, got: %s", sql)
	}
	if ops[0].Safety() != pipeline.Safe {
		t.Errorf("expected ALTER STATISTICS SET STATISTICS to be Safe (RFC §14.6), got %v: %s", ops[0].Safety(), sql)
	}
}

func TestDiffStatisticsObjectTargetResetToDefault(t *testing.T) {
	d := New()
	oldTarget := 100
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.st", &snapshot.SnapObject{
		Kind: "statistics",
		Opaque: &snapshot.SnapOpaque{
			Kind: "statistics", Schema: "public", Name: "st", StatisticsStructured: true,
			StatisticsTable: "public.orders", StatisticsKinds: []string{"ndistinct"}, StatisticsColumns: []string{"a", "b"},
			StatisticsTarget: &oldTarget,
		},
	})
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{
			Schema: "public", Name: "st", Table: "public.orders", Kinds: []string{"ndistinct"}, Columns: []string{"a", "b"},
			Body: "CREATE STATISTICS public.st (ndistinct) ON a, b FROM orders",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || !strings.Contains(ops[0].SQL(), "SET STATISTICS DEFAULT") {
		t.Errorf("expected ALTER STATISTICS ... SET STATISTICS DEFAULT, got: %v", sqlList(ops))
	}
}

func TestDiffStatisticsObjectUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.st", &snapshot.SnapObject{
		Kind: "statistics",
		Opaque: &snapshot.SnapOpaque{
			Kind: "statistics", Schema: "public", Name: "st", StatisticsStructured: true,
			StatisticsTable: "public.orders", StatisticsKinds: []string{"ndistinct"}, StatisticsColumns: []string{"a", "b"},
		},
	})
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{
			Schema: "public", Name: "st", Table: "public.orders", Kinds: []string{"ndistinct"}, Columns: []string{"a", "b"},
			Body: "CREATE STATISTICS public.st (ndistinct) ON a, b FROM orders",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected zero ops for an unchanged statistics object, got: %v", sqlList(ops))
	}
}

func TestDiffStatisticsObjectStaleSnapshotDoesNotRecreate(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.st", &snapshot.SnapObject{
		Kind:   "statistics",
		Opaque: &snapshot.SnapOpaque{Kind: "statistics", Schema: "public", Name: "st"},
	})
	desired := []pipeline.IRObject{
		&ir.StatisticsObject{
			Schema: "public", Name: "st", Table: "public.orders", Kinds: []string{"ndistinct"}, Columns: []string{"a", "b"},
			Body: "CREATE STATISTICS public.st (ndistinct) ON a, b FROM orders",
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if containsSQL(ops, "DROP STATISTICS") {
		t.Errorf("stale snapshot must not trigger DROP STATISTICS, got: %v", sqlList(ops))
	}
}

// ── Table PROTECTED / DROP CASCADE (RFC §7.11) ────────────────────────────────
// Regression guard for a real bug found live-testing PROTECTED against a
// demo project: SnapTable.Protected was populated all the way from source
// but the Pass-2 deletion loop never read it back — a PROTECTED table was
// silently DROPped exactly like any other, with zero actual protection
// (RFC §15.10 Phase 9 Pass 3 / DPG-E022 says the diff must instead error).
// DROP CASCADE was already correctly wired (found already-working while
// investigating), so its test here is a regression guard, not a bug fix.

func TestDiffProtectedTableDropIsBlocked(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.locked", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:    "public",
			Name:      "locked",
			Columns:   []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Protected: true,
		},
	})
	_, err := d.Diff(nil, snap)
	if err == nil {
		t.Fatal("expected an error blocking the drop of a PROTECTED table, got nil")
	}
	if !strings.Contains(err.Error(), "PROTECTED") {
		t.Errorf("expected error to mention PROTECTED, got: %v", err)
	}
}

func TestDiffUnprotectedTableDropSucceeds(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.plain", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "plain",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	ops, err := d.Diff(nil, snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsSQL(ops, `DROP TABLE IF EXISTS "public"."plain";`) {
		t.Errorf("expected normal DROP TABLE for an unprotected table, got: %v", sqlList(ops))
	}
}

func TestDiffTableDropCascadeEmitsCascade(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.plain", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:      "public",
			Name:        "plain",
			Columns:     []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			DropCascade: true,
		},
	})
	ops, err := d.Diff(nil, snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsSQL(ops, `DROP TABLE IF EXISTS "public"."plain" CASCADE;`) {
		t.Errorf("expected DROP TABLE ... CASCADE, got: %v", sqlList(ops))
	}
}

// TestDiffTableProtectedRemovedEmitsOp guards the OTHER half of the same
// bug: PROTECTED has no PG DDL equivalent, so removing it (table still
// desired) produced zero DiffOps — and apply's len(ops)==0 "already up to
// date" short-circuit means the snapshot's stale Protected=true was never
// cleared, permanently blocking the RFC's own documented "remove PROTECTED
// first, then drop" two-step workflow. Live-confirmed via the demo project:
// this exact sequence (unprotect, apply, then remove the table, apply)
// before the fix left the table permanently undroppable.
func TestDiffTableProtectedRemovedEmitsOp(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.locked", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:    "public",
			Name:      "locked",
			Columns:   []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
			Protected: true,
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "locked",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) == 0 {
		t.Fatal("expected a non-empty op set so apply doesn't short-circuit and skip the snapshot refresh")
	}
	if !containsSQL(ops, "PROTECTED") {
		t.Errorf("expected an op reflecting the PROTECTED removal, got: %v", sqlList(ops))
	}
}

func TestDiffTableProtectedUnchangedIsNoop(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.plain", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "plain",
			Columns: []snapshot.SnapColumn{{Name: "id", Type: "bigint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "plain",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "bigint"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("expected no ops for an unprotected table staying unprotected, got: %v", sqlList(ops))
	}
}

// ── Column type-change USING safety (RFC §7.2) ────────────────────────────────
// Regression guard for a real bug found live-testing COLUMN { USING expr; }
// against a demo project: the USING expression WAS correctly appended to the
// emitted ALTER COLUMN TYPE SQL, but the op's safety was hardcoded
// destructiveOp unconditionally — so a user-supplied, correct USING
// conversion was still treated as DESTRUCTIVE (blocked without
// --allow-destructive) exactly like a bare, unguarded type change. RFC §7.2
// is explicit: "unless a USING expression is present ... in which case it
// is classified CAUTION."

func TestDiffColumnTypeChangeWithUsingIsCaution(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "code", Type: "text"}},
		},
	})
	using := "code::integer"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "t",
			Columns: []*ir.Column{{Name: "code", Type: ir.TypeRef{Name: "integer"}, Using: &using}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "ALTER COLUMN") {
			found = o
		}
	}
	if found == nil {
		t.Fatalf("expected an ALTER COLUMN TYPE op, got: %v", sqlList(ops))
	}
	if !strings.Contains(found.SQL(), "USING code::integer") {
		t.Errorf("expected USING clause in the emitted SQL, got: %s", found.SQL())
	}
	if found.Safety() != pipeline.Caution {
		t.Errorf("safety = %v, want Caution (USING clause supplied)", found.Safety())
	}
}

func TestDiffColumnTypeChangeWithoutUsingIsDestructive(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "code", Type: "text"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "t",
			Columns: []*ir.Column{{Name: "code", Type: ir.TypeRef{Name: "integer"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	var found pipeline.DiffOp
	for _, o := range ops {
		if strings.Contains(o.SQL(), "ALTER COLUMN") {
			found = o
		}
	}
	if found == nil {
		t.Fatalf("expected an ALTER COLUMN TYPE op, got: %v", sqlList(ops))
	}
	if strings.Contains(found.SQL(), "USING") {
		t.Errorf("expected no USING clause when none was declared, got: %s", found.SQL())
	}
	if found.Safety() != pipeline.Destructive {
		t.Errorf("safety = %v, want Destructive (no USING clause, cast compatibility unknown offline)", found.Safety())
	}
}

// ── Table-level policy + grant emission at CREATE time ───────────────────────
// createPolicy and tableGrantOp (used for a GRANT declared inline on a new
// table) had zero unit coverage — only proven live via integration tests.

func TestDiffCreateTableWithPolicyAndGrant(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "t",
			Columns: []*ir.Column{
				{Name: "id", Type: ir.TypeRef{Name: "integer"}},
			},
			Policies: []*ir.Policy{
				{Name: "p_owner", Permissive: true, Command: "SELECT", Using: strPtr("owner_id = current_user_id()")},
			},
			Grants: []ir.Grant{{Privileges: []string{"SELECT"}, Roles: []string{"app_readonly"}}},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "CREATE POLICY") || !containsSQL(ops, `"p_owner"`) ||
		!containsSQL(ops, "FOR SELECT") || !containsSQL(ops, "USING (owner_id = current_user_id())") {
		t.Errorf("expected CREATE POLICY on new table, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "GRANT SELECT") || !containsSQL(ops, "app_readonly") {
		t.Errorf("expected inline GRANT on new table, got: %v", sqlList(ops))
	}
}

// TestDiffCreatePolicyToPublicNotQuoted is createPolicy's PUBLIC-role
// sibling: a POLICY's TO clause used its own inline quoting loop (not
// roleList), carrying the same bug — PUBLIC must render bare.
func TestDiffCreatePolicyToPublicNotQuoted(t *testing.T) {
	d := New()
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "t",
			Columns: []*ir.Column{{Name: "id", Type: ir.TypeRef{Name: "integer"}}},
			Policies: []*ir.Policy{
				{Name: "p_all", Permissive: true, Command: "SELECT", Roles: []string{"PUBLIC"}},
			},
		},
	}
	ops, err := d.Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "TO PUBLIC") {
		t.Errorf("expected unquoted TO PUBLIC, got: %v", sqlList(ops))
	}
	if containsSQL(ops, `"PUBLIC"`) {
		t.Errorf("PUBLIC must not be quoted, got: %v", sqlList(ops))
	}
}

// ── trivial helpers (coverage tail, see .dpg-notes/dpg-status-accounting.md §9's
// "#5 diff coverage push, remaining tail") ─────────────────────────────────────

// TestOpPos guards the op.Pos() accessor — trivial but was the last uncovered
// method on the pipeline.DiffOp implementation.
func TestOpPos(t *testing.T) {
	pos := pipeline.SourcePos{File: "t.dpg", Line: 3, Col: 7}
	o := safeOp("SELECT 1;", pos)
	if o.Pos() != pos {
		t.Errorf("Pos(): got %+v, want %+v", o.Pos(), pos)
	}
}

// TestPtrStr covers both branches of the nil-safe string-pointer dereference.
func TestPtrStr(t *testing.T) {
	if got := ptrStr(nil); got != "" {
		t.Errorf("ptrStr(nil): got %q, want empty", got)
	}
	s := "hello"
	if got := ptrStr(&s); got != "hello" {
		t.Errorf("ptrStr(&s): got %q, want hello", got)
	}
}

// TestInt64PtrEq covers all 4 branches of the nil-safe int64-pointer
// comparison: both nil, one nil, both set equal, both set different.
func TestInt64PtrEq(t *testing.T) {
	a, b := int64(5), int64(5)
	c := int64(6)
	cases := []struct {
		name     string
		x, y     *int64
		expected bool
	}{
		{"both nil", nil, nil, true},
		{"x nil", nil, &a, false},
		{"y nil", &a, nil, false},
		{"equal", &a, &b, true},
		{"different", &a, &c, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := int64PtrEq(tc.x, tc.y); got != tc.expected {
				t.Errorf("int64PtrEq(%v, %v): got %v, want %v", tc.x, tc.y, got, tc.expected)
			}
		})
	}
}

// TestCompositeAttrsChanged covers compositeAttrsChanged's 3 change signals
// (length, name, type) plus the unchanged control.
func TestCompositeAttrsChanged(t *testing.T) {
	base := []*ir.Column{
		{Name: "a", Type: ir.TypeRef{Name: "integer"}},
		{Name: "b", Type: ir.TypeRef{Name: "text"}},
	}
	baseSnap := []snapshot.SnapColumn{
		{Name: "a", Type: "integer"},
		{Name: "b", Type: "text"},
	}
	if compositeAttrsChanged(base, baseSnap) {
		t.Error("expected no change for identical attribute lists")
	}
	if !compositeAttrsChanged(base[:1], baseSnap) {
		t.Error("expected a change when the attribute count differs")
	}
	renamedSnap := []snapshot.SnapColumn{
		{Name: "a", Type: "integer"},
		{Name: "c", Type: "text"},
	}
	if !compositeAttrsChanged(base, renamedSnap) {
		t.Error("expected a change when an attribute name differs")
	}
	retypedSnap := []snapshot.SnapColumn{
		{Name: "a", Type: "integer"},
		{Name: "b", Type: "varchar"},
	}
	if !compositeAttrsChanged(base, retypedSnap) {
		t.Error("expected a change when an attribute type differs")
	}
}

// ── Column type change: implicit-cast safety classification ────────────────

// TestDiffColumnTypeImplicitCastNoUsingIsCaution proves RFC §17.2's
// "ALTER TABLE ALTER COLUMN TYPE (implicit cast) -> CAUTION" row: widening
// smallint -> integer with no USING clause must be CAUTION, not the
// previous hardcoded DESTRUCTIVE default, since PostgreSQL has a real
// implicit cast between them (verified live against a real pg_cast, see
// implicit_casts.go).
func TestDiffColumnTypeImplicitCastNoUsingIsCaution(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.widgets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "widgets",
			Columns: []snapshot.SnapColumn{{Name: "qty", Type: "smallint"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "widgets",
			Columns: []*ir.Column{{Name: "qty", Type: ir.TypeRef{Name: "integer"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `ALTER COLUMN "qty" TYPE integer`) {
		t.Fatalf("expected an ALTER COLUMN TYPE op, got: %v", sqlList(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), `ALTER COLUMN "qty" TYPE integer`) {
			if op.Safety() != pipeline.Caution {
				t.Errorf("expected Caution for an implicit-cast widening with no USING, got %s", op.Safety())
			}
		}
	}
}

// TestDiffColumnTypeNoImplicitCastNoUsingStaysDestructive is the negative
// case: text -> integer has no implicit cast (can fail at runtime on
// non-numeric data) and no USING clause is given, so it must stay the
// conservative DESTRUCTIVE default — proving the implicit-cast table only
// ever ADDS precision, never silently downgrades a genuinely risky change.
func TestDiffColumnTypeNoImplicitCastNoUsingStaysDestructive(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.widgets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "widgets",
			Columns: []snapshot.SnapColumn{{Name: "code", Type: "text"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "widgets",
			Columns: []*ir.Column{{Name: "code", Type: ir.TypeRef{Name: "integer"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `ALTER COLUMN "code" TYPE integer`) {
		t.Fatalf("expected an ALTER COLUMN TYPE op, got: %v", sqlList(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), `ALTER COLUMN "code" TYPE integer`) {
			if op.Safety() != pipeline.Destructive {
				t.Errorf("expected Destructive when no implicit cast exists and no USING is given, got %s", op.Safety())
			}
		}
	}
}

// TestDiffColumnTypeExplicitUsingStillCautionRegardlessOfImplicitCast proves
// an explicit USING clause is still respected exactly as before — CAUTION —
// even for a pair that also happens to have no implicit cast, and that the
// USING clause's own SQL text still renders (the implicit-cast table must
// never suppress a user-supplied USING expression).
func TestDiffColumnTypeExplicitUsingStillCautionRegardlessOfImplicitCast(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.widgets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "widgets",
			Columns: []snapshot.SnapColumn{{Name: "code", Type: "text"}},
		},
	})
	using := "code::integer"
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "widgets",
			Columns: []*ir.Column{
				{Name: "code", Type: ir.TypeRef{Name: "integer"}, Using: &using},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, "USING code::integer") {
		t.Fatalf("expected the explicit USING clause to render, got: %v", sqlList(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), `ALTER COLUMN "code" TYPE integer`) {
			if op.Safety() != pipeline.Caution {
				t.Errorf("expected Caution for an explicit USING clause, got %s", op.Safety())
			}
		}
	}
}

// ── Legacy bare timestamp/time/varbit snapshot self-heal ───────────────────

// TestDiffColumnsLegacyTimestampSnapshotNoDrift proves a pre-2026-08-17
// snapshot's stale "timestamp"/"time"/"varbit" Type string (the un-mapped
// short spelling, before PGCatalogName started aliasing them to their full
// canonical form) self-heals to zero drift instead of showing a permanent
// spurious DESTRUCTIVE ALTER COLUMN TYPE — the exact upgrade-compatibility
// concern the new "time"/"timestamp"/"varbit" mappings introduced, guarded
// the same way isLegacySerialTypeName already guards the SERIAL rename.
func TestDiffColumnsLegacyTimestampSnapshotNoDrift(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.widgets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema: "public",
			Name:   "widgets",
			Columns: []snapshot.SnapColumn{
				{Name: "created", Type: "timestamp"},
				{Name: "opens", Type: "time(2)"},
				{Name: "flags", Type: "varbit(8)"},
			},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "widgets",
			Columns: []*ir.Column{
				{Name: "created", Type: ir.TypeRef{Name: "timestamp without time zone"}},
				{Name: "opens", Type: ir.TypeRef{Name: "time without time zone", Mods: "(2)"}},
				{Name: "flags", Type: ir.TypeRef{Name: "bit varying", Mods: "(8)"}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		if strings.Contains(o.SQL(), "ALTER COLUMN") && strings.Contains(o.SQL(), "TYPE") {
			t.Errorf("expected no ALTER COLUMN TYPE against a legacy timestamp/time/varbit snapshot, got: %s", o.SQL())
		}
	}
}

// TestDiffColumnsLegacyTimestampSnapshotStillCatchesRealChange proves the
// self-heal guard is narrow — a genuine type change starting from a legacy
// snapshot value must still be detected, not accidentally swallowed by the
// new guard.
func TestDiffColumnsLegacyTimestampSnapshotStillCatchesRealChange(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.widgets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "widgets",
			Columns: []snapshot.SnapColumn{{Name: "created", Type: "timestamp"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema: "public",
			Name:   "widgets",
			Columns: []*ir.Column{
				{Name: "created", Type: ir.TypeRef{Name: "timestamp with time zone"}},
			},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `ALTER COLUMN "created" TYPE timestamp with time zone`) {
		t.Errorf("expected a genuine ALTER COLUMN TYPE for timestamp -> timestamptz, got: %v", sqlList(ops))
	}
}

// ── Column type change: typmod-widening safety classification ──────────────

// TestDiffColumnTypeVarcharWideningNoUsingIsCaution proves RFC §7.2's own
// primary example end to end: VARCHAR(10) -> VARCHAR(20) with no USING must
// be CAUTION, not DESTRUCTIVE — this was the RFC's literal illustrative
// case for "a type change PostgreSQL can apply implicitly," and it stayed
// unimplemented even after the different-base-type implicit-cast case
// (hasImplicitCast) was fixed, since pg_cast has no entry for a type to
// itself.
func TestDiffColumnTypeVarcharWideningNoUsingIsCaution(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.widgets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "widgets",
			Columns: []snapshot.SnapColumn{{Name: "label", Type: "character varying(10)"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "widgets",
			Columns: []*ir.Column{{Name: "label", Type: ir.TypeRef{Name: "character varying", Mods: "(20)"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSQL(ops, `ALTER COLUMN "label" TYPE character varying(20)`) {
		t.Fatalf("expected an ALTER COLUMN TYPE op, got: %v", sqlList(ops))
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), `ALTER COLUMN "label" TYPE character varying(20)`) {
			if op.Safety() != pipeline.Caution {
				t.Errorf("expected Caution for VARCHAR(10) -> VARCHAR(20) with no USING, got %s", op.Safety())
			}
		}
	}
}

// TestDiffColumnTypeVarcharShrinkingNoUsingStaysDestructive is the negative
// case: shrinking must stay the conservative DESTRUCTIVE default, since it
// can genuinely fail/truncate at apply time.
func TestDiffColumnTypeVarcharShrinkingNoUsingStaysDestructive(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.widgets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "widgets",
			Columns: []snapshot.SnapColumn{{Name: "label", Type: "character varying(20)"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "widgets",
			Columns: []*ir.Column{{Name: "label", Type: ir.TypeRef{Name: "character varying", Mods: "(10)"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), `ALTER COLUMN "label" TYPE character varying(10)`) {
			if op.Safety() != pipeline.Destructive {
				t.Errorf("expected Destructive for VARCHAR(20) -> VARCHAR(10), got %s", op.Safety())
			}
		}
	}
}

// TestDiffColumnTypeNumericScaleShrinkStaysDestructive proves the genuinely
// surprising numeric case reaches the real diff path too: PostgreSQL
// silently rounds on a scale-only shrink (no error), so this must stay
// DESTRUCTIVE even though precision alone doesn't decrease.
func TestDiffColumnTypeNumericScaleShrinkStaysDestructive(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.widgets", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "widgets",
			Columns: []snapshot.SnapColumn{{Name: "price", Type: "numeric(10,4)"}},
		},
	})
	desired := []pipeline.IRObject{
		&ir.Table{
			Schema:  "public",
			Name:    "widgets",
			Columns: []*ir.Column{{Name: "price", Type: ir.TypeRef{Name: "numeric", Mods: "(10,1)"}}},
		},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), `ALTER COLUMN "price" TYPE numeric(10,1)`) {
			if op.Safety() != pipeline.Destructive {
				t.Errorf("expected Destructive for numeric(10,4) -> numeric(10,1) (silent rounding), got %s", op.Safety())
			}
		}
	}
}
