package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindProjectRoot_FindsToml(t *testing.T) {
	root := t.TempDir()
	// Nested structure: root/cluster/db/schemas/public/
	schemaDir := filepath.Join(root, "cluster", "db", "schemas", "public")
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Place dpg.toml at root
	if err := os.WriteFile(filepath.Join(root, "dpg.toml"), []byte("[root]"), 0o644); err != nil {
		t.Fatal(err)
	}

	dpgFile := filepath.Join(schemaDir, "tables.dpg")
	got := FindProjectRoot(dpgFile)
	if got != root {
		t.Errorf("FindProjectRoot = %q, want %q", got, root)
	}
}

func TestFindProjectRoot_NoToml_ReturnsDir(t *testing.T) {
	tmp := t.TempDir()
	dpgFile := filepath.Join(tmp, "schema.dpg")

	got := FindProjectRoot(dpgFile)
	if got != tmp {
		t.Errorf("FindProjectRoot without dpg.toml = %q, want %q", got, tmp)
	}
}

func TestFindProjectRoot_TomlInSubdir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "cluster")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// dpg.toml at sub level (cluster), not root
	if err := os.WriteFile(filepath.Join(sub, "dpg.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	dpgFile := filepath.Join(sub, "cluster", "tables.dpg")
	got := FindProjectRoot(dpgFile)
	if got != sub {
		t.Errorf("FindProjectRoot = %q, want cluster dir %q", got, sub)
	}
}

func TestListDPGFiles_ReturnsDpgFiles(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "schemas", "public", "tables.dpg"),
		filepath.Join(root, "schemas", "iam", "schema.dpg"),
		filepath.Join(root, "extensions.dpg"),
	}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Non-.dpg file that should be excluded
	other := filepath.Join(root, "dpg.toml")
	if err := os.WriteFile(other, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	files := ListDPGFiles(root)
	if len(files) != 3 {
		t.Fatalf("ListDPGFiles returned %d files, want 3; got: %v", len(files), files)
	}
	for _, f := range files {
		if filepath.Ext(f) != ".dpg" {
			t.Errorf("non-.dpg file in results: %q", f)
		}
	}
}

func TestListDPGFiles_SkipsDotDpgDir(t *testing.T) {
	root := t.TempDir()
	// .dpg/snapshots/ should be excluded
	snapDir := filepath.Join(root, ".dpg", "snapshots")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapFile := filepath.Join(snapDir, "state.dpg")
	if err := os.WriteFile(snapFile, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "schema.dpg")
	if err := os.WriteFile(real, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	files := ListDPGFiles(root)
	if len(files) != 1 {
		t.Fatalf("expected 1 file (snapshot excluded), got %d: %v", len(files), files)
	}
	if files[0] != real {
		t.Errorf("unexpected file: %q", files[0])
	}
}

func TestListDPGFiles_EmptyDir(t *testing.T) {
	root := t.TempDir()
	files := ListDPGFiles(root)
	if len(files) != 0 {
		t.Fatalf("expected 0 files, got %d", len(files))
	}
}
