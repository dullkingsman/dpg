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
	if !containsSQL(ops, "CREATE TABLE") || !containsSQL(ops, "PARTITION OF") {
		t.Errorf("expected CREATE TABLE … PARTITION OF, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "events_2024") {
		t.Errorf("expected partition name in output, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "FOR VALUES FROM") {
		t.Errorf("expected partition bounds in output, got: %v", sqlList(ops))
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
	if !containsSQL(ops, "CREATE TABLE") || !containsSQL(ops, "PARTITION OF") {
		t.Errorf("expected CREATE TABLE … PARTITION OF for new partition, got: %v", sqlList(ops))
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
	if !containsSQL(ops, "DROP TABLE") {
		t.Errorf("expected DROP TABLE for bound change, got: %v", sqlList(ops))
	}
	if !containsSQL(ops, "CREATE TABLE") {
		t.Errorf("expected CREATE TABLE for bound change, got: %v", sqlList(ops))
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

// ── Materialized view comment uses correct SQL object type ─────────────────

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
