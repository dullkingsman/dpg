package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
