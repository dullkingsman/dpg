package compiler_test

import (
	"os"
	"path/filepath"
	"testing"

	_ "github.com/dullkingsman/dpg/internal/blockparser"
	"github.com/dullkingsman/dpg/internal/compiler"
	_ "github.com/dullkingsman/dpg/internal/graph"
	"github.com/dullkingsman/dpg/internal/ir"
	_ "github.com/dullkingsman/dpg/internal/merger"
	_ "github.com/dullkingsman/dpg/internal/pgparser"
	"github.com/dullkingsman/dpg/internal/pipeline"
	_ "github.com/dullkingsman/dpg/internal/scanner"
)

// ── inferSchemaFromPath ───────────────────────────────────────────────────────

// inferSchemaFromPath is unexported, so we test it indirectly via Compile:
// a file placed under <dbDir>/schemas/<name>/... should produce an object with
// Schema == <name>.

func writeDPG(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func compile(t *testing.T, dbDir string, files []string) []pipeline.IRObject {
	t.Helper()
	out, err := compiler.Compile(files, dbDir, pipeline.Default)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return out
}

// ── Schema inference from directory ──────────────────────────────────────────

func TestCompile_InferSchemaFromPath(t *testing.T) {
	dbDir := t.TempDir()
	f := filepath.Join(dbDir, "schemas", "iam", "tables.dpg")
	writeDPG(t, f, `TABLE users (id BIGINT NOT NULL);`)

	objects := compile(t, dbDir, []string{f})

	var tbl *ir.Table
	for _, o := range objects {
		if t2, ok := o.(*ir.Table); ok && t2.Name == "users" {
			tbl = t2
			break
		}
	}
	if tbl == nil {
		t.Fatal("table 'users' not found in output")
	}
	if tbl.Schema != "iam" {
		t.Errorf("Schema inferred from directory: got %q", tbl.Schema)
	}
}

func TestCompile_InferSchemaInjectsSyntheticSchema(t *testing.T) {
	dbDir := t.TempDir()
	f := filepath.Join(dbDir, "schemas", "billing", "tables.dpg")
	writeDPG(t, f, `TABLE invoices (id BIGINT NOT NULL);`)

	objects := compile(t, dbDir, []string{f})

	var found bool
	for _, o := range objects {
		if s, ok := o.(*ir.Schema); ok && s.Name == "billing" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected synthetic 'billing' schema in output")
	}
}

func TestCompile_PublicSchemaNotInjected(t *testing.T) {
	dbDir := t.TempDir()
	f := filepath.Join(dbDir, "schemas", "public", "tables.dpg")
	writeDPG(t, f, `TABLE accounts (id BIGINT NOT NULL);`)

	objects := compile(t, dbDir, []string{f})

	for _, o := range objects {
		if s, ok := o.(*ir.Schema); ok && s.Name == "public" {
			t.Error("'public' schema should not be injected")
			break
		}
	}
}

// ── Schema block forbidden inside schemas/ hierarchy ─────────────────────────

func TestCompile_SchemaBlockInSchemasDirErrors(t *testing.T) {
	dbDir := t.TempDir()
	f := filepath.Join(dbDir, "schemas", "iam", "schema.dpg")
	writeDPG(t, f, `SCHEMA iam {}`)

	_, err := compiler.Compile([]string{f}, dbDir, pipeline.Default)
	if err == nil {
		t.Fatal("expected error for SCHEMA block inside schemas/ directory")
	}
}

// ── Multi-file merge ──────────────────────────────────────────────────────────

func TestCompile_MultiFileTableMerge(t *testing.T) {
	dbDir := t.TempDir()
	f1 := filepath.Join(dbDir, "schemas", "app", "tables.dpg")
	f2 := filepath.Join(dbDir, "schemas", "app", "extra.dpg")
	writeDPG(t, f1, `TABLE users (id BIGINT NOT NULL);`)
	writeDPG(t, f2, `TABLE users (email TEXT NOT NULL);`)

	objects := compile(t, dbDir, []string{f1, f2})

	var tbl *ir.Table
	for _, o := range objects {
		if t2, ok := o.(*ir.Table); ok && t2.Name == "users" {
			tbl = t2
			break
		}
	}
	if tbl == nil {
		t.Fatal("merged 'users' table not found")
	}
	if len(tbl.Columns) != 2 {
		t.Errorf("merged Columns: expected 2, got %d", len(tbl.Columns))
	}
}

// ── FK dependency ordering in output ─────────────────────────────────────────

func TestCompile_FKDependencyOrdering(t *testing.T) {
	dbDir := t.TempDir()
	f := filepath.Join(dbDir, "schemas", "app", "tables.dpg")
	writeDPG(t, f, `
TABLE orders (
    id      BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    CONSTRAINT fk_user FOREIGN KEY (user_id) REFERENCES app.users (id)
);
TABLE users (
    id BIGINT NOT NULL
);
`)
	objects := compile(t, dbDir, []string{f})

	usersIdx, ordersIdx := -1, -1
	for i, o := range objects {
		if t2, ok := o.(*ir.Table); ok {
			switch t2.Name {
			case "users":
				usersIdx = i
			case "orders":
				ordersIdx = i
			}
		}
	}
	if usersIdx < 0 || ordersIdx < 0 {
		t.Fatalf("could not find both tables (users=%d, orders=%d)", usersIdx, ordersIdx)
	}
	if usersIdx >= ordersIdx {
		t.Errorf("users (pos %d) must come before orders (pos %d)", usersIdx, ordersIdx)
	}
}

// ── File not found ────────────────────────────────────────────────────────────

func TestCompile_MissingFileErrors(t *testing.T) {
	_, err := compiler.Compile([]string{"/nonexistent/file.dpg"}, "/nonexistent", pipeline.Default)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// ── No files ──────────────────────────────────────────────────────────────────

func TestCompile_EmptyFileList(t *testing.T) {
	objects, err := compiler.Compile(nil, t.TempDir(), pipeline.Default)
	if err != nil {
		t.Fatalf("Compile with nil files: %v", err)
	}
	if len(objects) != 0 {
		t.Errorf("expected empty output, got %d objects", len(objects))
	}
}
