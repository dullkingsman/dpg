package blockparser_test

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/blockparser"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

var zeroPos = pipeline.SourcePos{File: "test.dpg", Line: 1, Col: 1}

func parse(t *testing.T, src string) pipeline.BlockAST {
	t.Helper()
	p := blockparser.New()
	ast, err := p.Parse(pipeline.KindTable, src, zeroPos)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return ast
}

func parseErr(t *testing.T, src string) error {
	t.Helper()
	p := blockparser.New()
	_, err := p.Parse(pipeline.KindTable, src, zeroPos)
	return err
}

// ── empty / blank ─────────────────────────────────────────────────────────────

func TestEmptyBlock(t *testing.T) {
	ast := parse(t, "")
	if ast.Comment != nil || ast.Owner != nil {
		t.Error("expected zero-value BlockAST for empty input")
	}
}

func TestBlankBlock(t *testing.T) {
	ast := parse(t, "   \n\t  ")
	if ast.Protected || ast.DropCascade {
		t.Error("expected zero-value BlockAST for whitespace input")
	}
}

// ── simple directives ─────────────────────────────────────────────────────────

func TestComment(t *testing.T) {
	ast := parse(t, `COMMENT 'hello world';`)
	if ast.Comment == nil || ast.Comment.Value != "hello world" {
		t.Errorf("Comment: got %v", ast.Comment)
	}
}

func TestOwner(t *testing.T) {
	ast := parse(t, `OWNER "app_role";`)
	if ast.Owner == nil || ast.Owner.Name != "app_role" {
		t.Errorf("Owner: got %v", ast.Owner)
	}
}

func TestRenamedFrom(t *testing.T) {
	ast := parse(t, `RENAMED FROM old_table;`)
	if ast.RenamedFrom == nil || ast.RenamedFrom.Name != "old_table" {
		t.Errorf("RenamedFrom: got %v", ast.RenamedFrom)
	}
}

func TestProtected(t *testing.T) {
	ast := parse(t, `PROTECTED;`)
	if !ast.Protected {
		t.Error("expected Protected = true")
	}
}

func TestDeprecated(t *testing.T) {
	ast := parse(t, `DEPRECATED 'Use new_table instead';`)
	if ast.Deprecated == nil || ast.Deprecated.Value != "Use new_table instead" {
		t.Errorf("Deprecated: got %v", ast.Deprecated)
	}
}

func TestDropCascade(t *testing.T) {
	ast := parse(t, `DROP CASCADE;`)
	if !ast.DropCascade {
		t.Error("expected DropCascade = true")
	}
}

func TestEnableRLS(t *testing.T) {
	ast := parse(t, `ENABLE ROW LEVEL SECURITY;`)
	if !ast.EnableRLS {
		t.Error("expected EnableRLS = true")
	}
}

func TestForceRLS(t *testing.T) {
	ast := parse(t, `FORCE ROW LEVEL SECURITY;`)
	if !ast.ForceRLS {
		t.Error("expected ForceRLS = true")
	}
}

// ── INDICES ───────────────────────────────────────────────────────────────────

func TestSimpleIndex(t *testing.T) {
	ast := parse(t, `INDICES { idx_email (email); }`)
	if len(ast.Indices) != 1 {
		t.Fatalf("expected 1 index, got %d", len(ast.Indices))
	}
	idx := ast.Indices[0]
	if idx.Name.Name != "idx_email" {
		t.Errorf("index name: got %q", idx.Name.Name)
	}
	if len(idx.Columns) != 1 || idx.Columns[0].Name != "email" {
		t.Errorf("index columns: got %v", idx.Columns)
	}
	if idx.Unique {
		t.Error("expected non-unique")
	}
}

func TestUniqueIndex(t *testing.T) {
	ast := parse(t, `INDICES { idx_uq UNIQUE (email, name); }`)
	if len(ast.Indices) != 1 {
		t.Fatalf("expected 1 index, got %d", len(ast.Indices))
	}
	idx := ast.Indices[0]
	if !idx.Unique {
		t.Error("expected Unique = true")
	}
	if len(idx.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(idx.Columns))
	}
}

func TestIndexWithWhere(t *testing.T) {
	ast := parse(t, `INDICES { idx_active (status) WHERE (status != 'deleted'); }`)
	if len(ast.Indices) == 0 {
		t.Fatal("expected index")
	}
	idx := ast.Indices[0]
	if idx.Where == nil {
		t.Fatal("expected WHERE clause")
	}
	if idx.Where.Text == "" {
		t.Error("WHERE text should not be empty")
	}
}

func TestIndexWithUsing(t *testing.T) {
	ast := parse(t, `INDICES { idx_text (content) USING gin; }`)
	idx := ast.Indices[0]
	if idx.Method == nil || idx.Method.Name != "gin" {
		t.Errorf("Method: got %v", idx.Method)
	}
}

func TestMultipleIndices(t *testing.T) {
	src := `INDICES {
		idx_email  (email);
		idx_status (status) WHERE (status != 'deleted');
	}`
	ast := parse(t, src)
	if len(ast.Indices) != 2 {
		t.Fatalf("expected 2 indices, got %d", len(ast.Indices))
	}
}

// Mode B (§4.8 Dual Definition Modes): the singular INDEX keyword precedes a
// single entry outside a plural block and must parse without a wrapping '{ }'
// (previously this went through the same brace-requiring parser as INDICES
// and was a hard parse error, not just "unsupported").
func TestIndexModeBSingularKeyword(t *testing.T) {
	ast := parse(t, `INDEX idx_email (email);`)
	if len(ast.Indices) != 1 {
		t.Fatalf("expected 1 index, got %d", len(ast.Indices))
	}
	idx := ast.Indices[0]
	if idx.Name.Name != "idx_email" {
		t.Errorf("index name: got %q", idx.Name.Name)
	}
	if len(idx.Columns) != 1 || idx.Columns[0].Name != "email" {
		t.Errorf("index columns: got %v", idx.Columns)
	}
}

func TestIndexModeBWithWhereAndUsing(t *testing.T) {
	ast := parse(t, `INDEX idx_status (status) USING gin WHERE (status != 'deleted');`)
	if len(ast.Indices) != 1 {
		t.Fatalf("expected 1 index, got %d", len(ast.Indices))
	}
	idx := ast.Indices[0]
	if idx.Method == nil || idx.Method.Name != "gin" {
		t.Errorf("Method: got %v", idx.Method)
	}
	if idx.Where == nil {
		t.Fatal("expected WHERE clause")
	}
}

func TestIndexModeAAndBCanMix(t *testing.T) {
	src := `
		INDICES { idx_a (a); }
		INDEX idx_b (b);
	`
	ast := parse(t, src)
	if len(ast.Indices) != 2 {
		t.Fatalf("expected 2 indices (one per mode), got %d", len(ast.Indices))
	}
}

func TestIndexNullsNotDistinct(t *testing.T) {
	ast := parse(t, `INDICES { idx_uq UNIQUE (email) NULLS NOT DISTINCT; }`)
	idx := ast.Indices[0]
	if !idx.NullsNotDistinct {
		t.Error("expected NullsNotDistinct = true")
	}
}

// The explicit-default spelling ("NULLS DISTINCT") must parse (not error) but
// records no special state — it's PG's default made explicit.
func TestIndexNullsDistinctExplicitDefault(t *testing.T) {
	ast := parse(t, `INDICES { idx_uq UNIQUE (email) NULLS DISTINCT; }`)
	idx := ast.Indices[0]
	if idx.NullsNotDistinct {
		t.Error("expected NullsNotDistinct = false for explicit NULLS DISTINCT")
	}
}

func TestIndexNullsInvalidSuffix(t *testing.T) {
	if err := parseErr(t, `INDICES { idx_uq UNIQUE (email) NULLS MAYBE; }`); err == nil {
		t.Error("expected parse error for NULLS MAYBE")
	}
}

func TestIndexWithStorageParams(t *testing.T) {
	ast := parse(t, `INDICES { idx_a (a) WITH (fillfactor = 70, deduplicate_items = off); }`)
	idx := ast.Indices[0]
	if len(idx.With) != 2 {
		t.Fatalf("expected 2 storage params, got %d: %v", len(idx.With), idx.With)
	}
	if idx.With[0].Key != "fillfactor" || idx.With[0].Value != "70" {
		t.Errorf("param[0]: got %+v", idx.With[0])
	}
	if idx.With[1].Key != "deduplicate_items" || idx.With[1].Value != "off" {
		t.Errorf("param[1]: got %+v", idx.With[1])
	}
}

// ── COLUMN ────────────────────────────────────────────────────────────────────

func TestColumnComment(t *testing.T) {
	src := `COLUMN email { COMMENT 'Primary email address'; }`
	ast := parse(t, src)
	if len(ast.Columns) != 1 {
		t.Fatalf("expected 1 column block, got %d", len(ast.Columns))
	}
	col := ast.Columns[0]
	if col.Name.Name != "email" {
		t.Errorf("column name: got %q", col.Name.Name)
	}
	if col.Comment == nil || col.Comment.Value != "Primary email address" {
		t.Errorf("column comment: got %v", col.Comment)
	}
}

func TestColumnStatistics(t *testing.T) {
	src := `COLUMN status { STATISTICS 300; }`
	ast := parse(t, src)
	col := ast.Columns[0]
	if col.Statistics == nil || *col.Statistics != 300 {
		t.Errorf("Statistics: got %v", col.Statistics)
	}
}

func TestColumnRenamedFrom(t *testing.T) {
	src := `COLUMN email_address { RENAMED FROM email; }`
	ast := parse(t, src)
	col := ast.Columns[0]
	if col.RenamedFrom == nil || col.RenamedFrom.Name != "email" {
		t.Errorf("RenamedFrom: got %v", col.RenamedFrom)
	}
}

func TestColumnsBlock(t *testing.T) {
	src := `COLUMNS {
		email { COMMENT 'Email'; }
		status { STATISTICS 500; }
	}`
	ast := parse(t, src)
	if len(ast.Columns) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(ast.Columns))
	}
}

// ── GRANTS / REVOCATIONS ──────────────────────────────────────────────────────

func TestGrants(t *testing.T) {
	src := `GRANTS { SELECT, INSERT TO app_service; SELECT TO app_readonly; }`
	ast := parse(t, src)
	if len(ast.Grants) != 2 {
		t.Fatalf("expected 2 grants, got %d", len(ast.Grants))
	}
	g0 := ast.Grants[0]
	if len(g0.Privileges) != 2 {
		t.Errorf("grant 0 privs: got %v", g0.Privileges)
	}
	if len(g0.Roles) != 1 || g0.Roles[0].Name != "app_service" {
		t.Errorf("grant 0 roles: got %v", g0.Roles)
	}
}

func TestGrantAllPrivileges(t *testing.T) {
	src := `GRANTS { ALL PRIVILEGES TO admin; }`
	ast := parse(t, src)
	if len(ast.Grants) != 1 {
		t.Fatal("expected 1 grant")
	}
	if ast.Grants[0].Privileges != nil {
		t.Error("expected nil Privileges for ALL")
	}
}

func TestRevocations(t *testing.T) {
	src := `REVOCATIONS { ALL PRIVILEGES FROM PUBLIC; }`
	ast := parse(t, src)
	if len(ast.Revocations) != 1 {
		t.Fatalf("expected 1 revocation, got %d", len(ast.Revocations))
	}
	r := ast.Revocations[0]
	if len(r.Roles) != 1 || r.Roles[0].Name != "PUBLIC" {
		t.Errorf("revocation roles: got %v", r.Roles)
	}
}

// Mode B (§4.8 Dual Definition Modes): the singular GRANT/REVOCATION keyword
// precedes a single entry outside a plural block and must parse without a
// wrapping '{ }' — previously this went through the same brace-requiring
// parser as GRANTS/REVOCATIONS and was a hard parse error, exactly the same
// conflation bug fixed for INDEX/INDICES.
func TestGrantModeBSingularKeyword(t *testing.T) {
	ast := parse(t, `GRANT SELECT, INSERT TO app_service;`)
	if len(ast.Grants) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(ast.Grants))
	}
	g := ast.Grants[0]
	if len(g.Privileges) != 2 || len(g.Roles) != 1 || g.Roles[0].Name != "app_service" {
		t.Errorf("grant: got %+v", g)
	}
}

func TestRevocationModeBSingularKeyword(t *testing.T) {
	ast := parse(t, `REVOCATION ALL PRIVILEGES FROM PUBLIC;`)
	if len(ast.Revocations) != 1 {
		t.Fatalf("expected 1 revocation, got %d", len(ast.Revocations))
	}
	r := ast.Revocations[0]
	if r.Privileges != nil || len(r.Roles) != 1 || r.Roles[0].Name != "PUBLIC" {
		t.Errorf("revocation: got %+v", r)
	}
}

func TestGrantRevocationModeAAndBCanMix(t *testing.T) {
	src := `
		GRANTS { SELECT TO reader; }
		GRANT INSERT TO writer;
		REVOCATIONS { ALL PRIVILEGES FROM PUBLIC; }
		REVOCATION UPDATE FROM guest;
	`
	ast := parse(t, src)
	if len(ast.Grants) != 2 {
		t.Fatalf("expected 2 grants (one per mode), got %d", len(ast.Grants))
	}
	if len(ast.Revocations) != 2 {
		t.Fatalf("expected 2 revocations (one per mode), got %d", len(ast.Revocations))
	}
}

// GRANT/REVOCATION Mode B inside a COLUMN block exercises the second of the
// three dispatch sites that had this conflation (table-level, column-level,
// DEFAULT PRIVILEGES-level).
func TestGrantModeBInColumnBlock(t *testing.T) {
	ast := parse(t, `COLUMN email { GRANT SELECT TO reader; REVOCATION UPDATE FROM guest; }`)
	if len(ast.Columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(ast.Columns))
	}
	col := ast.Columns[0]
	if len(col.Grants) != 1 || len(col.Revocations) != 1 {
		t.Errorf("column grants/revocations: got %d/%d", len(col.Grants), len(col.Revocations))
	}
}

// GRANT/REVOCATION Mode B inside a DEFAULT PRIVILEGES block exercises the
// third of the three dispatch sites that had this conflation.
func TestGrantModeBInDefaultPrivilegesBlock(t *testing.T) {
	ast := parse(t, `DEFAULT PRIVILEGES { GRANT SELECT TO reader; REVOCATION UPDATE FROM guest; }`)
	if len(ast.DefaultPrivileges) != 1 {
		t.Fatalf("expected 1 default privileges block, got %d", len(ast.DefaultPrivileges))
	}
	dp := ast.DefaultPrivileges[0]
	if len(dp.Grants) != 1 || len(dp.Revocations) != 1 {
		t.Errorf("dp grants/revocations: got %d/%d", len(dp.Grants), len(dp.Revocations))
	}
}

// ── POLICIES ─────────────────────────────────────────────────────────────────

func TestSimplePolicy(t *testing.T) {
	src := `POLICIES {
		view_self FOR SELECT USING (id = auth.uid());
	}`
	ast := parse(t, src)
	if len(ast.Policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(ast.Policies))
	}
	pol := ast.Policies[0]
	if pol.Name.Name != "view_self" {
		t.Errorf("policy name: got %q", pol.Name.Name)
	}
	if pol.Command != "SELECT" {
		t.Errorf("policy command: got %q", pol.Command)
	}
	if pol.Using == nil {
		t.Error("expected USING clause")
	}
}

// Mode B (§4.8 Dual Definition Modes): the singular POLICY keyword precedes a
// single entry outside a plural block — previously this would have hit the
// same conflation bug already fixed for INDEX/INDICES and GRANT/REVOCATION
// (POLICY wasn't even in the dispatch switch at all, so it was "unknown block
// directive", not just brace-mismatched).
func TestPolicyModeBSingularKeyword(t *testing.T) {
	ast := parse(t, `POLICY view_self FOR SELECT USING (id = auth.uid());`)
	if len(ast.Policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(ast.Policies))
	}
	pol := ast.Policies[0]
	if pol.Name.Name != "view_self" {
		t.Errorf("policy name: got %q", pol.Name.Name)
	}
	if pol.Command != "SELECT" {
		t.Errorf("policy command: got %q", pol.Command)
	}
}

func TestPolicyModeAAndBCanMix(t *testing.T) {
	src := `
		POLICIES { p_a FOR SELECT USING (true); }
		POLICY p_b FOR INSERT USING (true);
	`
	ast := parse(t, src)
	if len(ast.Policies) != 2 {
		t.Fatalf("expected 2 policies (one per mode), got %d", len(ast.Policies))
	}
}

// ── TRIGGERS ─────────────────────────────────────────────────────────────────

func TestSimpleTrigger(t *testing.T) {
	src := `TRIGGERS {
		after_insert AFTER INSERT
			FOR EACH ROW
			EXECUTE FUNCTION on_insert();
	}`
	ast := parse(t, src)
	if len(ast.Triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(ast.Triggers))
	}
	tr := ast.Triggers[0]
	if tr.Name.Name != "after_insert" {
		t.Errorf("trigger name: got %q", tr.Name.Name)
	}
	if tr.When != "AFTER" {
		t.Errorf("trigger when: got %q", tr.When)
	}
	if len(tr.Events) != 1 || tr.Events[0] != "INSERT" {
		t.Errorf("trigger events: got %v", tr.Events)
	}
	if tr.ForEach != "ROW" {
		t.Errorf("trigger forEach: got %q", tr.ForEach)
	}
	if tr.Function.Name != "on_insert" {
		t.Errorf("trigger function: got %q", tr.Function.Name)
	}
}

func TestTriggerWithWhen(t *testing.T) {
	src := `TRIGGERS {
		after_email_change AFTER UPDATE
			FOR EACH ROW
			WHEN (OLD.email IS DISTINCT FROM NEW.email)
			EXECUTE FUNCTION notify_email_change();
	}`
	ast := parse(t, src)
	if len(ast.Triggers) == 0 {
		t.Fatal("expected trigger")
	}
	if ast.Triggers[0].Condition == nil {
		t.Error("expected WHEN condition")
	}
}

// Mode B: the singular TRIGGER keyword precedes a single entry outside a
// plural block — same conflation-bug family as POLICY/INDEX/GRANT/REVOCATION.
func TestTriggerModeBSingularKeyword(t *testing.T) {
	ast := parse(t, `TRIGGER after_insert AFTER INSERT FOR EACH ROW EXECUTE FUNCTION on_insert();`)
	if len(ast.Triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(ast.Triggers))
	}
	tr := ast.Triggers[0]
	if tr.Name.Name != "after_insert" {
		t.Errorf("trigger name: got %q", tr.Name.Name)
	}
	if tr.Function.Name != "on_insert" {
		t.Errorf("trigger function: got %q", tr.Function.Name)
	}
}

func TestTriggerModeAAndBCanMix(t *testing.T) {
	src := `
		TRIGGERS { t_a AFTER INSERT FOR EACH ROW EXECUTE FUNCTION f_a(); }
		TRIGGER t_b AFTER UPDATE FOR EACH ROW EXECUTE FUNCTION f_b();
	`
	ast := parse(t, src)
	if len(ast.Triggers) != 2 {
		t.Fatalf("expected 2 triggers (one per mode), got %d", len(ast.Triggers))
	}
}

// ── CONSTRAINT ────────────────────────────────────────────────────────────────

func TestConstraintNotValid(t *testing.T) {
	src := `CONSTRAINT ck_positive CHECK (amount > 0) NOT VALID;`
	ast := parse(t, src)
	if len(ast.Constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(ast.Constraints))
	}
	cst := ast.Constraints[0]
	if cst.Name.Name != "ck_positive" {
		t.Errorf("constraint name: got %q", cst.Name.Name)
	}
	if !cst.NotValid {
		t.Error("expected NotValid = true")
	}
}

// ── PARTITIONS ────────────────────────────────────────────────────────────────

func TestPartitions(t *testing.T) {
	src := `PARTITIONS {
		events_2024_q1 FOR VALUES FROM ('2024-01-01') TO ('2024-04-01');
		events_2024_q2 FOR VALUES FROM ('2024-04-01') TO ('2024-07-01');
	}`
	ast := parse(t, src)
	if ast.Partitions == nil || len(ast.Partitions.Partitions) != 2 {
		t.Fatalf("expected 2 partitions, got %v", ast.Partitions)
	}
	if ast.Partitions.Partitions[0].Name.Name != "events_2024_q1" {
		t.Errorf("partition name: got %q", ast.Partitions.Partitions[0].Name.Name)
	}
}

// Mode B: the singular PARTITION keyword precedes a single entry outside a
// plural block — same conflation-bug family as POLICY/TRIGGER/INDEX/GRANT/
// REVOCATION. PartitionDef wraps a slice (unlike the others, nothing else
// merges multiple PARTITIONS{} declarations), so Mode B must lazily create it
// on first use rather than assume it already exists.
func TestPartitionModeBSingularKeyword(t *testing.T) {
	ast := parse(t, `PARTITION events_2024_q1 FOR VALUES FROM ('2024-01-01') TO ('2024-04-01');`)
	if ast.Partitions == nil || len(ast.Partitions.Partitions) != 1 {
		t.Fatalf("expected 1 partition, got %v", ast.Partitions)
	}
	if ast.Partitions.Partitions[0].Name.Name != "events_2024_q1" {
		t.Errorf("partition name: got %q", ast.Partitions.Partitions[0].Name.Name)
	}
}

// Both orderings are exercised deliberately: PartitionDef wraps a slice, and
// the PARTITIONS (plural) dispatch case originally did a bare assignment
// (ast.Partitions = ...) rather than merging — which silently discarded
// whatever a PRECEDING Mode-B PARTITION entry had already added. PARTITIONS-
// then-PARTITION happens not to trigger it (PARTITIONS runs first and
// PARTITION's lazy-init sees a non-nil ast.Partitions to append to), so only
// testing that order would have missed the bug — confirmed by a live
// integration test (TestRoundtripPolicyTriggerPartitionModeB) using the other
// order, which failed until this was fixed.
func TestPartitionModeAAndBCanMix(t *testing.T) {
	src := `
		PARTITIONS { events_2024_q1 FOR VALUES FROM ('2024-01-01') TO ('2024-04-01'); }
		PARTITION events_2024_q2 FOR VALUES FROM ('2024-04-01') TO ('2024-07-01');
	`
	ast := parse(t, src)
	if ast.Partitions == nil || len(ast.Partitions.Partitions) != 2 {
		t.Fatalf("expected 2 partitions (one per mode), got %v", ast.Partitions)
	}
}

func TestPartitionModeBThenModeACanMix(t *testing.T) {
	src := `
		PARTITION events_2024_q1 FOR VALUES FROM ('2024-01-01') TO ('2024-04-01');
		PARTITIONS { events_2024_q2 FOR VALUES FROM ('2024-04-01') TO ('2024-07-01'); }
	`
	ast := parse(t, src)
	if ast.Partitions == nil || len(ast.Partitions.Partitions) != 2 {
		t.Fatalf("expected 2 partitions (one per mode), got %v — PARTITIONS must merge into, not overwrite, a preceding Mode-B PARTITION", ast.Partitions)
	}
}

// ── MIGRATE REMOVE ────────────────────────────────────────────────────────────

func TestMigrateRemove(t *testing.T) {
	src := `MIGRATE REMOVE ('cancelled') {
		UPDATE orders SET status = 'closed' WHERE status = 'cancelled';
	}`
	ast := parse(t, src)
	if ast.MigrateRemove == nil {
		t.Fatal("expected MigrateRemove")
	}
	if ast.MigrateRemove.SQL.Text == "" {
		t.Error("expected non-empty SQL in MigrateRemove")
	}
}

// ── combined ──────────────────────────────────────────────────────────────────

func TestFullTableBlock(t *testing.T) {
	src := `
		COMMENT 'Primary identity store';
		OWNER   "app_role";

		COLUMN email {
			COMMENT    'Verified email address';
			STATISTICS 300;
		}

		INDICES {
			idx_email  (email);
			idx_status (status) WHERE (status != 'deleted');
		}

		ENABLE ROW LEVEL SECURITY;

		POLICIES {
			view_self FOR SELECT USING (id = auth.uid());
		}

		TRIGGERS {
			after_insert AFTER INSERT
				FOR EACH ROW
				EXECUTE FUNCTION on_insert();
		}

		GRANTS {
			SELECT, INSERT, UPDATE TO app_service;
			SELECT                 TO app_readonly;
		}

		REVOCATIONS {
			ALL PRIVILEGES FROM PUBLIC;
		}
	`
	ast := parse(t, src)

	if ast.Comment == nil || ast.Comment.Value != "Primary identity store" {
		t.Errorf("Comment: got %v", ast.Comment)
	}
	if ast.Owner == nil || ast.Owner.Name != "app_role" {
		t.Errorf("Owner: got %v", ast.Owner)
	}
	if len(ast.Columns) != 1 {
		t.Errorf("Columns: got %d", len(ast.Columns))
	}
	if len(ast.Indices) != 2 {
		t.Errorf("Indices: got %d", len(ast.Indices))
	}
	if !ast.EnableRLS {
		t.Error("expected EnableRLS")
	}
	if len(ast.Policies) != 1 {
		t.Errorf("Policies: got %d", len(ast.Policies))
	}
	if len(ast.Triggers) != 1 {
		t.Errorf("Triggers: got %d", len(ast.Triggers))
	}
	if len(ast.Grants) != 2 {
		t.Errorf("Grants: got %d", len(ast.Grants))
	}
	if len(ast.Revocations) != 1 {
		t.Errorf("Revocations: got %d", len(ast.Revocations))
	}
}

// ── PREFERRED JSON FORMAT ─────────────────────────────────────────────────────

func TestPreferredJsonFormatJsonb(t *testing.T) {
	p := blockparser.New()
	ast, err := p.Parse(pipeline.KindVirtualType, `PREFERRED JSON FORMAT jsonb;`, zeroPos)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if ast.PreferredJsonFormat != "jsonb" {
		t.Errorf("PreferredJsonFormat: got %q, want %q", ast.PreferredJsonFormat, "jsonb")
	}
}

func TestPreferredJsonFormatJson(t *testing.T) {
	p := blockparser.New()
	ast, err := p.Parse(pipeline.KindVirtualType, `PREFERRED JSON FORMAT json;`, zeroPos)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if ast.PreferredJsonFormat != "json" {
		t.Errorf("PreferredJsonFormat: got %q, want %q", ast.PreferredJsonFormat, "json")
	}
}

func TestPreferredJsonFormatWithComment(t *testing.T) {
	p := blockparser.New()
	ast, err := p.Parse(pipeline.KindVirtualType,
		`COMMENT 'some type'; PREFERRED JSON FORMAT json;`, zeroPos)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if ast.PreferredJsonFormat != "json" {
		t.Errorf("PreferredJsonFormat: got %q, want json", ast.PreferredJsonFormat)
	}
	if ast.Comment == nil || ast.Comment.Value != "some type" {
		t.Errorf("Comment: got %v", ast.Comment)
	}
}

func TestPreferredJsonFormatInvalidValue(t *testing.T) {
	p := blockparser.New()
	_, err := p.Parse(pipeline.KindVirtualType, `PREFERRED JSON FORMAT text;`, zeroPos)
	if err == nil {
		t.Error("expected error for invalid format value, got nil")
	}
}

func TestPreferredJsonFormatMissingKeywords(t *testing.T) {
	p := blockparser.New()
	_, err := p.Parse(pipeline.KindVirtualType, `PREFERRED jsonb;`, zeroPos)
	if err == nil {
		t.Error("expected error for missing JSON keyword, got nil")
	}
}

// ── NAME MAP / NAME MAPS ──────────────────────────────────────────────────────

func TestNameMapDefaultImplicit(t *testing.T) {
	ast := parse(t, `NAME MAP TO LOWER_SNAKE_CASE;`)
	if len(ast.NameMaps) != 1 {
		t.Fatalf("expected 1 NameMap entry, got %d", len(ast.NameMaps))
	}
	e := ast.NameMaps[0]
	if e.Tool != "default" {
		t.Errorf("Tool: got %q, want %q", e.Tool, "default")
	}
	if e.Value != "LOWER_SNAKE_CASE" {
		t.Errorf("Value: got %q, want %q", e.Value, "LOWER_SNAKE_CASE")
	}
	if e.IsLiteral {
		t.Error("expected IsLiteral=false for a rule")
	}
}

func TestNameMapDefaultExplicit(t *testing.T) {
	ast := parse(t, `NAME MAP default TO UPPER_CAMEL_CASE;`)
	if len(ast.NameMaps) != 1 {
		t.Fatalf("expected 1 NameMap entry, got %d", len(ast.NameMaps))
	}
	e := ast.NameMaps[0]
	if e.Tool != "default" {
		t.Errorf("Tool: got %q, want %q", e.Tool, "default")
	}
	if e.Value != "UPPER_CAMEL_CASE" {
		t.Errorf("Value: got %q, want %q", e.Value, "UPPER_CAMEL_CASE")
	}
}

func TestNameMapToolWithRule(t *testing.T) {
	ast := parse(t, `NAME MAP prisma TO LOWER_CAMEL_CASE;`)
	if len(ast.NameMaps) != 1 {
		t.Fatalf("expected 1 NameMap entry, got %d", len(ast.NameMaps))
	}
	e := ast.NameMaps[0]
	if e.Tool != "prisma" {
		t.Errorf("Tool: got %q, want %q", e.Tool, "prisma")
	}
	if e.Value != "LOWER_CAMEL_CASE" {
		t.Errorf("Value: got %q, want %q", e.Value, "LOWER_CAMEL_CASE")
	}
	if e.IsLiteral {
		t.Error("expected IsLiteral=false for a rule")
	}
}

func TestNameMapToolWithLiteralName(t *testing.T) {
	ast := parse(t, `NAME MAP prisma TO "ProductVariant";`)
	if len(ast.NameMaps) != 1 {
		t.Fatalf("expected 1 NameMap entry, got %d", len(ast.NameMaps))
	}
	e := ast.NameMaps[0]
	if e.Tool != "prisma" {
		t.Errorf("Tool: got %q, want %q", e.Tool, "prisma")
	}
	if e.Value != "ProductVariant" {
		t.Errorf("Value: got %q, want %q", e.Value, "ProductVariant")
	}
	if !e.IsLiteral {
		t.Error("expected IsLiteral=true for a double-quoted name")
	}
}

func TestNameMapsBlock(t *testing.T) {
	src := `NAME MAPS {
		default TO LOWER_SNAKE_CASE;
		prisma  TO "Order";
		drizzle TO LOWER_CAMEL_CASE;
	}`
	ast := parse(t, src)
	if len(ast.NameMaps) != 3 {
		t.Fatalf("expected 3 NameMap entries, got %d", len(ast.NameMaps))
	}
	if ast.NameMaps[0].Tool != "default" || ast.NameMaps[0].Value != "LOWER_SNAKE_CASE" || ast.NameMaps[0].IsLiteral {
		t.Errorf("entry[0]: %+v", ast.NameMaps[0])
	}
	if ast.NameMaps[1].Tool != "prisma" || ast.NameMaps[1].Value != "Order" || !ast.NameMaps[1].IsLiteral {
		t.Errorf("entry[1]: %+v", ast.NameMaps[1])
	}
	if ast.NameMaps[2].Tool != "drizzle" || ast.NameMaps[2].Value != "LOWER_CAMEL_CASE" || ast.NameMaps[2].IsLiteral {
		t.Errorf("entry[2]: %+v", ast.NameMaps[2])
	}
}

func TestNameMapMultipleSingular(t *testing.T) {
	src := `NAME MAP default TO LOWER_SNAKE_CASE;
		NAME MAP prisma TO "User";`
	ast := parse(t, src)
	if len(ast.NameMaps) != 2 {
		t.Fatalf("expected 2 NameMap entries, got %d", len(ast.NameMaps))
	}
}

func TestNameMapUnknownRuleErrors(t *testing.T) {
	err := parseErr(t, `NAME MAP default TO SNAKE_LOWER;`)
	if err == nil {
		t.Error("expected error for unknown rule, got nil")
	}
}

func TestNameMapColumnBlock(t *testing.T) {
	src := `COLUMN created_at {
		NAME MAP default TO LOWER_SNAKE_CASE;
		NAME MAP prisma TO "createdAt";
	}`
	ast := parse(t, src)
	if len(ast.Columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(ast.Columns))
	}
	col := ast.Columns[0]
	if len(col.NameMaps) != 2 {
		t.Fatalf("expected 2 column NameMap entries, got %d", len(col.NameMaps))
	}
	if col.NameMaps[0].Tool != "default" || col.NameMaps[0].Value != "LOWER_SNAKE_CASE" {
		t.Errorf("column entry[0]: %+v", col.NameMaps[0])
	}
	if col.NameMaps[1].Tool != "prisma" || col.NameMaps[1].Value != "createdAt" || !col.NameMaps[1].IsLiteral {
		t.Errorf("column entry[1]: %+v", col.NameMaps[1])
	}
}

func TestNameMapsColumnBlock(t *testing.T) {
	src := `COLUMN user_id {
		NAME MAPS {
			default TO LOWER_SNAKE_CASE;
			drizzle TO "userId";
		}
	}`
	ast := parse(t, src)
	if len(ast.Columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(ast.Columns))
	}
	if len(ast.Columns[0].NameMaps) != 2 {
		t.Fatalf("expected 2 column NameMap entries, got %d", len(ast.Columns[0].NameMaps))
	}
}

func TestAllRules(t *testing.T) {
	rules := []string{
		"LOWER_SNAKE_CASE", "UPPER_SNAKE_CASE", "LOWER_CAMEL_CASE", "UPPER_CAMEL_CASE",
		"LOWER_KEBAB_CASE", "UPPER_KEBAB_CASE", "TRAIN_CASE", "LOWER_CASE",
		"UPPER_CASE", "PASCAL_SNAKE_CASE",
	}
	for _, rule := range rules {
		src := "NAME MAP TO " + rule + ";"
		a := parse(t, src)
		if len(a.NameMaps) != 1 || a.NameMaps[0].Value != rule {
			t.Errorf("rule %q: got %+v", rule, a.NameMaps)
		}
	}
}

// ── registry ──────────────────────────────────────────────────────────────────

func TestRegistration(t *testing.T) {
	impl, ok := pipeline.Resolve[pipeline.BlockParser](pipeline.Default, pipeline.KeyBlockParser)
	if !ok {
		t.Fatal("BlockParser not registered; check that blockparser init() ran")
	}
	if impl == nil {
		t.Fatal("registered BlockParser is nil")
	}
}

// Index column sort order/NULLS must parse into structured fields (not be stored
// as part of the column name), and INCLUDE columns must be unquoted — matching
// key columns — so the differ doesn't double-quote them.
func TestIndexSortOrderAndInclude(t *testing.T) {
	ast := parse(t, `INDICES { i (a DESC NULLS LAST, b) INCLUDE ("c", d); }`)
	idx := ast.Indices[0]
	if len(idx.Columns) != 2 {
		t.Fatalf("expected 2 key columns, got %d: %+v", len(idx.Columns), idx.Columns)
	}
	if idx.Columns[0].Name != "a" || idx.Columns[0].SortOrder != "DESC" || idx.Columns[0].Nulls != "LAST" {
		t.Errorf("column 0: got %+v, want a/DESC/LAST", idx.Columns[0])
	}
	if idx.Columns[1].Name != "b" || idx.Columns[1].SortOrder != "" {
		t.Errorf("column 1: got %+v, want b with no sort", idx.Columns[1])
	}
	if len(idx.Include) != 2 || idx.Include[0].Name != "c" || idx.Include[1].Name != "d" {
		t.Errorf("INCLUDE: got %+v, want unquoted [c d]", idx.Include)
	}
}
