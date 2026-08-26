package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thec1oud/dpg/internal/config"
	"github.com/thec1oud/dpg/internal/project"
)

// TestLoadEnvPlainURLCluster guards a real bug found live-testing a demo
// project: loadEnv used to additionally gate on "at least one cluster uses a
// link: connection string" (project.Cluster.IsLink()), so a project using a
// plain url= cluster connection — the common case — never had its .env file
// read at all, even when source elsewhere referenced {{env:VAR}} (ROLE
// PASSWORD, SUBSCRIPTION CONNECTION, USER MAPPING OPTIONS). The secret
// reference mechanism is independent of how the cluster itself connects, so
// gating .env loading on IsLink() was wrong. Confirmed live: CREATE ROLE ...
// PASSWORD '{{env:...}}' failed with "environment variable is not set"
// despite a correctly-named, present .env file, until --env was passed
// explicitly to bypass the gate.
func TestLoadEnvPlainURLCluster(t *testing.T) {
	const key = "DPG_TEST_LOADENV_PLAIN_URL_VAR"
	os.Unsetenv(key)
	t.Cleanup(func() { os.Unsetenv(key) })

	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte(key+"=from-dotenv\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	proj := &project.Project{
		RootDir: dir,
		Clusters: []*project.Cluster{
			{Config: config.ClusterConfig{Cluster: config.ClusterDef{URL: "postgres://localhost/x"}}},
		},
	}
	if proj.Clusters[0].IsLink() {
		t.Fatal("test fixture invalid: cluster must NOT be link-based (that's the case this test guards)")
	}

	loadEnv(proj, "") // envFilePath="" -> falls back to <RootDir>/.env

	if got := os.Getenv(key); got != "from-dotenv" {
		t.Errorf("%s: got %q, want %q — .env was not loaded for a plain url= cluster", key, got, "from-dotenv")
	}
}

// TestLoadEnvNeverOverwritesProcessEnv guards parseEnvFile's existing
// "process env wins" contract, unaffected by the IsLink()-gate fix above but
// worth pinning down alongside it.
func TestLoadEnvNeverOverwritesProcessEnv(t *testing.T) {
	const key = "DPG_TEST_LOADENV_NO_OVERWRITE_VAR"
	os.Setenv(key, "from-process")
	t.Cleanup(func() { os.Unsetenv(key) })

	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte(key+"=from-dotenv\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	proj := &project.Project{RootDir: dir}
	loadEnv(proj, "")

	if got := os.Getenv(key); got != "from-process" {
		t.Errorf("%s: got %q, want %q (process env must win over .env)", key, got, "from-process")
	}
}

// TestLoadEnvMissingFileIsNonFatal guards loadEnv's documented non-fatal
// contract: a project with no .env file at all (and no --env override) must
// not panic or error, regardless of cluster connection kind.
func TestLoadEnvMissingFileIsNonFatal(t *testing.T) {
	dir := t.TempDir() // no .env written
	proj := &project.Project{RootDir: dir}
	loadEnv(proj, "") // must not panic
}
