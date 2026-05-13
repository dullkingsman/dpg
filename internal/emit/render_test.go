package emit

import (
	"strings"
	"testing"
	"time"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

type testOp struct {
	sql    string
	safety pipeline.Safety
	pos    pipeline.SourcePos
}

func (o testOp) SQL() string             { return o.sql }
func (o testOp) Safety() pipeline.Safety { return o.safety }
func (o testOp) Pos() pipeline.SourcePos { return o.pos }
func (o testOp) Transactional() bool     { return true }

func TestRenderNoChanges(t *testing.T) {
	var buf strings.Builder
	m := pipeline.Migration{
		Meta: pipeline.MigrationMeta{
			GeneratedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	if err := Render(&buf, m, DefaultRenderOptions()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "no changes") {
		t.Errorf("expected '(no changes)' in output, got:\n%s", out)
	}
	if strings.Contains(out, "BEGIN") {
		t.Errorf("unexpected BEGIN in empty migration output")
	}
}

func TestRenderTransactional(t *testing.T) {
	var buf strings.Builder
	op := testOp{sql: "ALTER TABLE foo ADD COLUMN bar text;", safety: pipeline.Safe}
	m := pipeline.Migration{
		Meta:          pipeline.MigrationMeta{GeneratedAt: time.Now()},
		Transactional: []pipeline.DiffOp{op},
	}
	if err := Render(&buf, m, DefaultRenderOptions()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "BEGIN") {
		t.Errorf("expected BEGIN in output")
	}
	if !strings.Contains(out, "COMMIT") {
		t.Errorf("expected COMMIT in output")
	}
	if !strings.Contains(out, op.sql) {
		t.Errorf("expected SQL in output")
	}
}

func TestRenderSafetyAnnotation(t *testing.T) {
	var buf strings.Builder
	op := testOp{sql: "DROP TABLE foo;", safety: pipeline.Destructive}
	m := pipeline.Migration{
		Meta:          pipeline.MigrationMeta{GeneratedAt: time.Now()},
		Transactional: []pipeline.DiffOp{op},
	}
	if err := Render(&buf, m, RenderOptions{ShowSafety: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "DESTRUCTIVE") {
		t.Errorf("expected safety annotation in output, got:\n%s", out)
	}
}

func TestRenderSourcePosAnnotation(t *testing.T) {
	var buf strings.Builder
	op := testOp{
		sql:    "ALTER TABLE foo ADD COLUMN x integer;",
		safety: pipeline.Safe,
		pos:    pipeline.SourcePos{File: "schemas/public/tables.dpg", Line: 42},
	}
	m := pipeline.Migration{
		Meta:          pipeline.MigrationMeta{GeneratedAt: time.Now()},
		Transactional: []pipeline.DiffOp{op},
	}
	if err := Render(&buf, m, RenderOptions{ShowSourcePos: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "schemas/public/tables.dpg:42") {
		t.Errorf("expected source annotation in output, got:\n%s", out)
	}
}

func TestRenderNonTransactional(t *testing.T) {
	var buf strings.Builder
	txnOp := testOp{sql: "CREATE TABLE foo (id integer);", safety: pipeline.Safe}
	nonTxnOp := testOp{sql: "CREATE INDEX CONCURRENTLY idx ON foo(id);", safety: pipeline.Safe}
	m := pipeline.Migration{
		Meta:             pipeline.MigrationMeta{GeneratedAt: time.Now()},
		Transactional:    []pipeline.DiffOp{txnOp},
		NonTransactional: []pipeline.DiffOp{nonTxnOp},
	}
	if err := Render(&buf, m, DefaultRenderOptions()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "non-transactional") {
		t.Errorf("expected non-transactional section header in output")
	}
	if !strings.Contains(out, "--------") {
		t.Errorf("expected section separator in output")
	}
	if !strings.Contains(out, nonTxnOp.sql) {
		t.Errorf("expected non-transactional SQL in output")
	}
}

func TestRenderHeader(t *testing.T) {
	var buf strings.Builder
	m := pipeline.Migration{
		Meta: pipeline.MigrationMeta{
			GeneratedAt:    time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			SourceRevision: "abc1234",
			Cluster:        "prod",
			Database:       "mydb",
		},
	}
	if err := Render(&buf, m, DefaultRenderOptions()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"DPG Migration", "abc1234", "prod", "mydb"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output header, got:\n%s", want, out)
		}
	}
}
