package diff

import (
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/snapshot"
)

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

// TestDiffTableRenameNewNameCollidesErrors guards the symmetric case: the new
// name already exists in the snapshot. A rename can't proceed without a free
// landing spot.
func TestDiffTableRenameNewNameCollidesErrors(t *testing.T) {
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
	_, err := d.Diff(desired, snap)
	if err == nil {
		t.Fatal("expected diff error for rename collision, got nil")
	}
	if !strings.Contains(err.Error(), "conflicts") {
		t.Errorf("error missing expected substring 'conflicts': %s", err.Error())
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

func sqlList(ops []pipeline.DiffOp) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = o.SQL()
	}
	return out
}
