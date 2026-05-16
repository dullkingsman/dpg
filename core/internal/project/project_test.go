package project_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dullkingsman/dpg/internal/project"
)

// buildTree creates a minimal project layout under a temp dir.
//
//	root/
//	  dpg.toml                   (root config)
//	  mycluster/
//	    dpg.toml                 (cluster config)
//	    mydb/
//	      dpg.toml               (database config)
//	      schemas/public/
//	        tables.dpg
func buildTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeF := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	writeF("dpg.toml", `
[compiler]
default_drop_behavior = "restrict"
`)
	writeF("mycluster/dpg.toml", `
[cluster]
name = "mycluster"
url  = "postgres://localhost/test"
`)
	writeF("mycluster/mydb/dpg.toml", `
[database]
name = "mydb"
`)
	writeF("mycluster/mydb/schemas/public/tables.dpg", `TABLE users (id BIGINT);`)

	return root
}

// ── Discover ──────────────────────────────────────────────────────────────────

func TestDiscover_FromRoot(t *testing.T) {
	root := buildTree(t)
	proj, err := project.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if proj.RootDir != root {
		t.Errorf("RootDir: got %q", proj.RootDir)
	}
	if len(proj.Clusters) != 1 {
		t.Fatalf("Clusters: expected 1, got %d", len(proj.Clusters))
	}
	cl := proj.Clusters[0]
	if cl.Name() != "mycluster" {
		t.Errorf("Cluster.Name: got %q", cl.Name())
	}
	if len(cl.Databases) != 1 {
		t.Fatalf("Databases: expected 1, got %d", len(cl.Databases))
	}
	db := cl.Databases[0]
	if db.Name() != "mydb" {
		t.Errorf("Database.Name: got %q", db.Name())
	}
	if len(db.SourceFiles) != 1 {
		t.Errorf("SourceFiles: expected 1, got %d", len(db.SourceFiles))
	}
}

func TestDiscover_FromSubdir(t *testing.T) {
	root := buildTree(t)
	// Discover should walk up from a subdirectory.
	sub := filepath.Join(root, "mycluster", "mydb")
	proj, err := project.Discover(sub)
	if err != nil {
		t.Fatalf("Discover from subdir: %v", err)
	}
	if proj.RootDir != root {
		t.Errorf("RootDir: expected %q, got %q", root, proj.RootDir)
	}
}

func TestDiscover_NoDPGToml(t *testing.T) {
	_, err := project.Discover(t.TempDir())
	if err == nil {
		t.Fatal("expected error when no dpg.toml found")
	}
}

// ── Project helper methods ─────────────────────────────────────────────────────

func TestProject_DPGDir(t *testing.T) {
	root := buildTree(t)
	proj, _ := project.Discover(root)
	want := filepath.Join(root, ".dpg")
	if proj.DPGDir() != want {
		t.Errorf("DPGDir: got %q, want %q", proj.DPGDir(), want)
	}
}

func TestProject_SnapshotDir(t *testing.T) {
	root := buildTree(t)
	proj, _ := project.Discover(root)
	want := filepath.Join(root, ".dpg/snapshots")
	if proj.SnapshotDir() != want {
		t.Errorf("SnapshotDir: got %q, want %q", proj.SnapshotDir(), want)
	}
}

// ── Multi-cluster / multi-database ────────────────────────────────────────────

func TestDiscover_MultipleClustersDatabases(t *testing.T) {
	root := t.TempDir()

	writeF := func(rel, content string) {
		path := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(content), 0o644)
	}

	writeF("dpg.toml", "[compiler]\ndefault_drop_behavior = \"restrict\"\n")
	writeF("cluster-a/dpg.toml", "[cluster]\nname = \"a\"\nurl = \"postgres://a\"\n")
	writeF("cluster-a/db1/dpg.toml", "[database]\nname = \"db1\"\n")
	writeF("cluster-a/db2/dpg.toml", "[database]\nname = \"db2\"\n")
	writeF("cluster-b/dpg.toml", "[cluster]\nname = \"b\"\nurl = \"postgres://b\"\n")
	writeF("cluster-b/db3/dpg.toml", "[database]\nname = \"db3\"\n")

	proj, err := project.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(proj.Clusters) != 2 {
		t.Errorf("Clusters: expected 2, got %d", len(proj.Clusters))
	}
	totalDBs := 0
	for _, cl := range proj.Clusters {
		totalDBs += len(cl.Databases)
	}
	if totalDBs != 3 {
		t.Errorf("total databases: expected 3, got %d", totalDBs)
	}
}

// ── Cluster helpers ────────────────────────────────────────────────────────────

func TestCluster_IsLink(t *testing.T) {
	root := t.TempDir()
	writeF := func(rel, content string) {
		p := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(content), 0o644)
	}
	writeF("dpg.toml", "[compiler]\ndefault_drop_behavior = \"restrict\"\n")
	writeF("c/dpg.toml", "[cluster]\nname = \"c\"\nlink = \"env:DB_URL\"\n")
	writeF("c/db/dpg.toml", "[database]\nname = \"db\"\n")

	proj, _ := project.Discover(root)
	if len(proj.Clusters) == 0 {
		t.Fatal("no clusters found")
	}
	cl := proj.Clusters[0]
	if !cl.IsLink() {
		t.Error("IsLink: expected true")
	}
	if cl.ConnectionString() != "env:DB_URL" {
		t.Errorf("ConnectionString: got %q", cl.ConnectionString())
	}
}

func TestCluster_ClusterObjectsDirExcludedFromDatabases(t *testing.T) {
	root := t.TempDir()
	writeF := func(rel, content string) {
		p := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(content), 0o644)
	}
	writeF("dpg.toml", "[compiler]\ndefault_drop_behavior = \"restrict\"\n")
	writeF("c/dpg.toml", "[cluster]\nname = \"c\"\nurl = \"postgres://x\"\n[cluster.options]\n")
	// "cluster" is the default ClusterObjectsDir — it must not appear as a database.
	writeF("c/cluster/roles.dpg", "ROLE app;")
	writeF("c/db/dpg.toml", "[database]\nname = \"db\"\n")

	proj, err := project.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(proj.Clusters) != 1 {
		t.Fatalf("Clusters: expected 1, got %d", len(proj.Clusters))
	}
	cl := proj.Clusters[0]
	// Only "db" should be a database; "cluster" directory is the objects dir.
	if len(cl.Databases) != 1 {
		t.Errorf("Databases: expected 1, got %d", len(cl.Databases))
	}
	if cl.Databases[0].Name() != "db" {
		t.Errorf("Database name: got %q", cl.Databases[0].Name())
	}
}
