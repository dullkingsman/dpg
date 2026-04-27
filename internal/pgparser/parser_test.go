package pgparser_test

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/ast"
	"github.com/dullkingsman/dpg/internal/pgparser"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

var zeroPos = pipeline.SourcePos{File: "test.dpg", Line: 1, Col: 1}

func parse(t *testing.T, kind pipeline.ObjectKind, part1 string) pipeline.PGParseResult {
	t.Helper()
	p := pgparser.New()
	result, err := p.Parse(kind, part1, zeroPos)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return result
}

func parseErr(t *testing.T, kind pipeline.ObjectKind, part1 string) error {
	t.Helper()
	p := pgparser.New()
	_, err := p.Parse(kind, part1, zeroPos)
	return err
}

// ── Reconstruct ───────────────────────────────────────────────────────────────

func TestReconstructTable(t *testing.T) {
	sql := pgparser.Reconstruct(pipeline.KindTable, "users (id BIGINT)")
	if sql != "CREATE TABLE users (id BIGINT)" {
		t.Errorf("unexpected: %q", sql)
	}
}

func TestReconstructEnum(t *testing.T) {
	sql := pgparser.Reconstruct(pipeline.KindEnum, "status AS ENUM ('active', 'inactive')")
	if sql != "CREATE TYPE status AS ENUM ('active', 'inactive')" {
		t.Errorf("unexpected: %q", sql)
	}
}

// ── Parse — happy path ────────────────────────────────────────────────────────

func TestParseTable(t *testing.T) {
	r := parse(t, pipeline.KindTable, `users (
		id    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		email TEXT NOT NULL
	)`)
	cs, ok := ast.AsCreateTable(r)
	if !ok {
		t.Fatal("expected CreateStmt")
	}
	if cs.Relation.Relname != "users" {
		t.Errorf("table name: got %q, want %q", cs.Relation.Relname, "users")
	}
}

func TestParseView(t *testing.T) {
	r := parse(t, pipeline.KindView, `users_summary AS SELECT id, email FROM users`)
	vs, ok := ast.AsViewStmt(r)
	if !ok {
		t.Fatal("expected ViewStmt")
	}
	if vs.View.Relname != "users_summary" {
		t.Errorf("view name: got %q, want %q", vs.View.Relname, "users_summary")
	}
}

func TestParseEnum(t *testing.T) {
	r := parse(t, pipeline.KindEnum, `status AS ENUM ('active', 'pending', 'inactive')`)
	cs, ok := ast.AsCreateEnum(r)
	if !ok {
		t.Fatal("expected CreateEnumStmt")
	}
	if len(cs.Vals) != 3 {
		t.Errorf("enum vals: got %d, want 3", len(cs.Vals))
	}
}

func TestParseFunction(t *testing.T) {
	r := parse(t, pipeline.KindFunction, `add(a INT, b INT) RETURNS INT LANGUAGE sql AS $$ SELECT a + b $$;`)
	cf, ok := ast.AsCreateFunction(r)
	if !ok {
		t.Fatal("expected CreateFunctionStmt")
	}
	if cf.Funcname[0].GetString_().Sval != "add" {
		t.Errorf("function name: got %v", cf.Funcname)
	}
}

func TestParseSchema(t *testing.T) {
	r := parse(t, pipeline.KindSchema, `myschema`)
	cs, ok := ast.AsCreateSchema(r)
	if !ok {
		t.Fatal("expected CreateSchemaStmt")
	}
	if cs.Schemaname != "myschema" {
		t.Errorf("schema name: got %q, want %q", cs.Schemaname, "myschema")
	}
}

func TestParseExtension(t *testing.T) {
	r := parse(t, pipeline.KindExtension, `pgcrypto`)
	cs, ok := ast.AsCreateExtension(r)
	if !ok {
		t.Fatal("expected CreateExtensionStmt")
	}
	if cs.Extname != "pgcrypto" {
		t.Errorf("extension name: got %q, want %q", cs.Extname, "pgcrypto")
	}
}

func TestParseSequence(t *testing.T) {
	r := parse(t, pipeline.KindSequence, `order_id_seq START WITH 1000 INCREMENT BY 1`)
	cs, ok := ast.AsCreateSeq(r)
	if !ok {
		t.Fatal("expected CreateSeqStmt")
	}
	if cs.Sequence.Relname != "order_id_seq" {
		t.Errorf("sequence name: got %q", cs.Sequence.Relname)
	}
}

func TestParseRole(t *testing.T) {
	r := parse(t, pipeline.KindRole, `app_readonly LOGIN`)
	cs, ok := ast.AsCreateRole(r)
	if !ok {
		t.Fatal("expected CreateRoleStmt")
	}
	if cs.Role != "app_readonly" {
		t.Errorf("role name: got %q", cs.Role)
	}
}

// ── Parse — error path ────────────────────────────────────────────────────────

func TestParseInvalidSQL(t *testing.T) {
	err := parseErr(t, pipeline.KindTable, `(this is not valid sql`)
	if err == nil {
		t.Fatal("expected parse error for invalid SQL")
	}
}

// ── Registry ──────────────────────────────────────────────────────────────────

func TestRegistration(t *testing.T) {
	impl, ok := pipeline.Resolve[pipeline.PGSQLParser](pipeline.Default, pipeline.KeyPGSQLParser)
	if !ok {
		t.Fatal("PGSQLParser not registered; check that pgparser init() ran")
	}
	if impl == nil {
		t.Fatal("registered PGSQLParser is nil")
	}
}
