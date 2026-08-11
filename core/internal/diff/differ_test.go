package diff

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestDiffConcurrentIndex(t *testing.T) {
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

// TestDiffUnnamedExcludeExpressionElementNotPredicted proves an EXCLUDE with
// an expression-based element (not a plain column) is correctly NOT given a
// predicted name — PostgreSQL's own ChooseIndexExpressionName needs a fully
// analyzed, OID-resolved expression tree that pg_query's raw parse never
// has, so guessing would risk a FALSE match (silently hiding a real
// definition change) rather than just a missed one. This must fall back to
// the structural-signature-only strategy (still correct offline; only
// verify/plan --live loses reconciliation for this specific EXCLUDE) rather
// than silently predicting a wrong name.
func TestDiffUnnamedExcludeExpressionElementNotPredicted(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	_ = snap.SetObject("public.t", &snapshot.SnapObject{
		Kind: "table",
		Table: &snapshot.SnapTable{
			Schema:  "public",
			Name:    "t",
			Columns: []snapshot.SnapColumn{{Name: "a", Type: "integer"}, {Name: "b", Type: "integer"}},
			Constraints: []snapshot.SnapConstraint{
				// A live-generated name for an operator-based expression
				// element — confirmed live that PostgreSQL's real algorithm
				// produces the literal "expr" here (unlike a bare function
				// call, which gets its own name — see
				// TestDiffUnnamedExcludeFuncCallElementMatchesLiveGeneratedName),
				// which this tool does not attempt to replicate.
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
	// Not predicted, so the unnamed desired side and the named snapshot side
	// don't match by name — but the desired side's structural signature also
	// doesn't match the snapshot's (named) key, so this correctly falls
	// through to "genuinely different constraint": a DROP of the old name
	// plus an ADD of the new (unnamed) one. This is the documented, accepted
	// limitation — not silence, and not a false match.
	if !containsSQL(ops, "DROP CONSTRAINT") || !containsSQL(ops, "t_expr_excl") {
		t.Errorf("expected DROP CONSTRAINT t_expr_excl (no false match), got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "ADD") {
		t.Errorf("expected the unnamed EXCLUDE to be (re-)added, got: %v", sqlList(ops))
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
	_ = snap.SetObject("DEFAULT PRIVILEGES", &snapshot.SnapObject{
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
	_ = snap.SetObject("DEFAULT PRIVILEGES", &snapshot.SnapObject{
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
}

func TestDiffDefaultPrivilegesUnchangedIsNoop(t *testing.T) {
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
	if len(ops) != 0 {
		t.Fatalf("want 0 ops (no spurious drift), got %d: %v", len(ops), ops)
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
func TestDiffCollationNoSpuriousBodyDrift(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	// Baseline as introspection produces it (qualified, LOCALE form; Reconstructed).
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.Collation{Schema: "public", Name: "c", Body: "CREATE COLLATION public.c (LOCALE = 'C')", Reconstructed: true},
	}); err != nil {
		t.Fatal(err)
	}
	// Desired from hand-written source (unqualified, LC_* form) — semantically equal.
	desired := []pipeline.IRObject{
		&ir.Collation{Schema: "public", Name: "c", Body: "CREATE COLLATION c (LC_COLLATE = 'C', LC_CTYPE = 'C')"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("spurious drift for equivalent collation spelling: %d ops: %v", len(ops), ops)
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
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.Collation{Schema: "public", Name: "c", Body: "CREATE COLLATION public.c (LOCALE = 'C')"},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.Collation{Schema: "public", Name: "c", Body: "CREATE COLLATION public.c (LOCALE = 'POSIX')"},
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
		&ir.Publication{Name: "p", Body: "CREATE PUBLICATION p FOR ALL TABLES"},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.Publication{Name: "p", Body: "CREATE PUBLICATION p FOR TABLE public.t"},
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
	}{
		{
			name:           "tablespace",
			oldObj:         &ir.Tablespace{Name: "ts", Body: "CREATE TABLESPACE ts LOCATION '/data/ts1'"},
			newObj:         &ir.Tablespace{Name: "ts", Body: "CREATE TABLESPACE ts LOCATION '/data/ts2'"},
			wantDropSubstr: `DROP TABLESPACE IF EXISTS "ts";`,
			wantNewInBody:  "/data/ts2",
		},
		{
			name:           "fdw",
			oldObj:         &ir.ForeignDataWrapper{Name: "myfdw", Body: "CREATE FOREIGN DATA WRAPPER myfdw HANDLER h1"},
			newObj:         &ir.ForeignDataWrapper{Name: "myfdw", Body: "CREATE FOREIGN DATA WRAPPER myfdw HANDLER h2"},
			wantDropSubstr: `DROP FOREIGN DATA WRAPPER IF EXISTS "myfdw";`,
			wantNewInBody:  "h2",
		},
		{
			name:           "server",
			oldObj:         &ir.ForeignServer{Name: "srv", Body: "CREATE SERVER srv FOREIGN DATA WRAPPER myfdw OPTIONS (host 'a')"},
			newObj:         &ir.ForeignServer{Name: "srv", Body: "CREATE SERVER srv FOREIGN DATA WRAPPER myfdw OPTIONS (host 'b')"},
			wantDropSubstr: `DROP SERVER IF EXISTS "srv";`,
			wantNewInBody:  "'b'",
		},
		{
			name:           "user_mapping",
			oldObj:         &ir.UserMapping{User: "alice", Server: "srv", Body: "CREATE USER MAPPING FOR alice SERVER srv OPTIONS (password 'p1')"},
			newObj:         &ir.UserMapping{User: "alice", Server: "srv", Body: "CREATE USER MAPPING FOR alice SERVER srv OPTIONS (password 'p2')"},
			wantDropSubstr: `DROP USER MAPPING IF EXISTS FOR "alice" SERVER "srv";`,
			wantNewInBody:  "'p2'",
		},
		{
			name:           "publication",
			oldObj:         &ir.Publication{Name: "p", Body: "CREATE PUBLICATION p FOR ALL TABLES"},
			newObj:         &ir.Publication{Name: "p", Body: "CREATE PUBLICATION p FOR TABLE public.t"},
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
			oldObj:         &ir.EventTrigger{Name: "et", Body: "CREATE EVENT TRIGGER et ON ddl_command_start EXECUTE FUNCTION f1()"},
			newObj:         &ir.EventTrigger{Name: "et", Body: "CREATE EVENT TRIGGER et ON ddl_command_start EXECUTE FUNCTION f2()"},
			wantDropSubstr: `DROP EVENT TRIGGER IF EXISTS "et";`,
			wantNewInBody:  "f2()",
		},
		{
			name:           "collation",
			oldObj:         &ir.Collation{Schema: "public", Name: "c", Body: "CREATE COLLATION public.c (LOCALE = 'C')"},
			newObj:         &ir.Collation{Schema: "public", Name: "c", Body: "CREATE COLLATION public.c (LOCALE = 'POSIX')"},
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
			oldObj:         &ir.Cast{SourceType: ir.TypeRef{Name: "int4"}, TargetType: ir.TypeRef{Name: "text"}, Body: "CREATE CAST (int4 AS text) WITH FUNCTION f1(int4)"},
			newObj:         &ir.Cast{SourceType: ir.TypeRef{Name: "int4"}, TargetType: ir.TypeRef{Name: "text"}, Body: "CREATE CAST (int4 AS text) WITH FUNCTION f2(int4)"},
			wantDropSubstr: `DROP CAST IF EXISTS (int4 AS text);`,
			wantNewInBody:  "f2(int4)",
		},
		{
			name:           "statistics",
			oldObj:         &ir.StatisticsObject{Schema: "public", Name: "st", Body: "CREATE STATISTICS public.st (ndistinct) ON a, b FROM t"},
			newObj:         &ir.StatisticsObject{Schema: "public", Name: "st", Body: "CREATE STATISTICS public.st (dependencies) ON a, b FROM t"},
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
			if ops[0].Safety() != pipeline.Destructive {
				t.Errorf("DROP op safety = %v, want Destructive", ops[0].Safety())
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
		&ir.Collation{Schema: "public", Name: "c", Body: "CREATE COLLATION c (LC_COLLATE = 'C', LC_CTYPE = 'C')"},
	}); err != nil {
		t.Fatal(err)
	}
	// Introspected desired side: canonical reconstruction, marked Reconstructed.
	desired := []pipeline.IRObject{
		&ir.Collation{Schema: "public", Name: "c", Body: "CREATE COLLATION public.c (LOCALE = 'C')", Reconstructed: true},
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
		&ir.Collation{Schema: "public", Name: "c", Body: "CREATE COLLATION public.c (LOCALE = 'C')", Reconstructed: true},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.Collation{Schema: "public", Name: "c", Body: "CREATE COLLATION c (LC_COLLATE = 'C', LC_CTYPE = 'C')"},
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
