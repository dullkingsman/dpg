package diff

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

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

// TestDiffOpaqueOfflineEditEmitsStructuredDropRecreate is the regression guard
// for #3 (real update path for opaque objects): a genuine offline body edit
// must emit a structured DROP (matching what dropObject emits when the object
// is removed outright) followed by a CREATE from the new body — not the old
// "-- WARNING: ... manual DROP + recreate required" comment placeholder. Every
// opaque kind except "operator" is covered; see
// TestDiffOperatorOfflineEditStillWarnsManual for why operator is excluded.
func TestDiffOpaqueOfflineEditEmitsStructuredDropRecreate(t *testing.T) {
	cases := []struct {
		name           string
		oldObj, newObj pipeline.IRObject
		wantDropSubstr string
		wantNewInBody  string
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
			oldObj:         &ir.Subscription{Name: "sub", Body: "CREATE SUBSCRIPTION sub CONNECTION 'x' PUBLICATION p1"},
			newObj:         &ir.Subscription{Name: "sub", Body: "CREATE SUBSCRIPTION sub CONNECTION 'x' PUBLICATION p2"},
			wantDropSubstr: `DROP SUBSCRIPTION IF EXISTS "sub";`,
			wantNewInBody:  "p2",
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
			if ops[1].Safety() != pipeline.Safe {
				t.Errorf("CREATE op safety = %v, want Safe", ops[1].Safety())
			}
			for _, op := range ops {
				if strings.Contains(op.SQL(), "manual DROP + recreate required") {
					t.Errorf("structured path must not fall back to the manual warning, got: %s", op.SQL())
				}
			}
		})
	}
}

// TestDiffOperatorOfflineEditStillWarnsManual guards the intentional exclusion
// of "operator" from the structured drop+recreate path: dropObject's operator
// case cannot safely construct "DROP OPERATOR" (PG requires a mandatory
// (lefttype, righttype) clause that ir.Operator does not capture), so wiring it
// in would emit invalid SQL. Until operand types are modelled, operator keeps
// the old manual-warning behavior — this must not regress to either the broken
// DROP or silence.
func TestDiffOperatorOfflineEditStillWarnsManual(t *testing.T) {
	d := New()
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, []pipeline.IRObject{
		&ir.Operator{Schema: "public", Name: "=>", Body: "CREATE OPERATOR public.=> (PROCEDURE = f1, LEFTARG = int4, RIGHTARG = int4)"},
	}); err != nil {
		t.Fatal(err)
	}
	desired := []pipeline.IRObject{
		&ir.Operator{Schema: "public", Name: "=>", Body: "CREATE OPERATOR public.=> (PROCEDURE = f2, LEFTARG = int4, RIGHTARG = int4)"},
	}
	ops, err := d.Diff(desired, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("want 1 op (manual warning), got %d: %v", len(ops), ops)
	}
	sql := ops[0].SQL()
	if !strings.Contains(sql, "manual DROP + recreate required") {
		t.Errorf("expected manual-warning fallback for operator, got: %s", sql)
	}
	if strings.Contains(sql, "DROP OPERATOR") {
		t.Errorf("operator must not emit a bare (invalid) DROP OPERATOR statement, got: %s", sql)
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
