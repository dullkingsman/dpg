package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dullkingsman/dpg/internal/config"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/project"
	"github.com/dullkingsman/dpg/internal/ui"
)

// TestPlanPrintsScalarMergeConflict proves a scalar-merge-conflict
// diagnostic (RFC §19.1, surfaced via compiler.Compile's second return
// value) reaches buildPlan's print logic and can block planning on its own,
// same as TestApplyPrintsScalarMergeConflict in apply_test.go — [linter.rules]
// promotes it to an error here (plan has no --strict flag of its own) to
// prove the diagnostic is visible without depending on any Linter-registered
// rule ever firing.
func TestPlanPrintsScalarMergeConflict(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.dpg")
	f2 := filepath.Join(dir, "b.dpg")
	if err := os.WriteFile(f1, []byte(`TABLE users (id BIGINT NOT NULL) { OWNER "alice"; }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte(`TABLE users (email TEXT NOT NULL) { OWNER "bob"; }`), 0o644); err != nil {
		t.Fatal(err)
	}

	cl := &project.Cluster{
		Dir:        dir,
		Config:     config.ClusterConfig{Cluster: config.ClusterDef{Name: "test-cluster"}},
		ObjectsDir: dir,
	}
	db := &project.Database{
		Dir:         dir,
		Config:      config.DatabaseConfig{Database: config.DatabaseDef{Name: "test-db"}},
		SourceFiles: []string{f1, f2},
	}

	_, err := buildPlan(cl, db, &pipeline.Snapshot{}, &mockDiffer{}, &mockEmitter{},
		pipeline.LinterConfig{
			WarnOnScalarMergeConflict: true,
			Rules:                     map[string]string{"scalar-merge-conflict": "error"},
		},
		"text",
	)

	if err != ui.ErrSilent {
		t.Fatalf("expected ui.ErrSilent (blocked on a [linter.rules]-promoted scalar-merge-conflict), got: %v", err)
	}
}

func TestGitRevisionDetachedHead(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hash := "abc1234def5678901234"
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(hash+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig) //nolint:errcheck

	rev, err := gitRevision()
	if err != nil {
		t.Fatal(err)
	}
	if rev != hash[:7] {
		t.Errorf("expected %q, got %q", hash[:7], rev)
	}
}

func TestGitRevisionBranchRef(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	refsDir := filepath.Join(gitDir, "refs", "heads")
	if err := os.MkdirAll(refsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	hash := "deadbeef12345678901234567890123456789012"
	headContent := "ref: refs/heads/master\n"
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(headContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refsDir, "master"), []byte(hash+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig) //nolint:errcheck

	rev, err := gitRevision()
	if err != nil {
		t.Fatal(err)
	}
	if rev != hash[:7] {
		t.Errorf("expected %q, got %q", hash[:7], rev)
	}
}

func TestGitRevisionNoGit(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig) //nolint:errcheck

	rev, err := gitRevision()
	if err != nil {
		t.Fatal(err)
	}
	if rev != "" {
		t.Errorf("expected empty revision when no .git dir, got %q", rev)
	}
}
