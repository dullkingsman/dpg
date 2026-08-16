//go:build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/config"
	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/format"
	"github.com/dullkingsman/dpg/internal/introspect"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/project"
	"github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestDumpDashOSandboxesClusterOutput proves the `-o` fix: with -o given,
// `dpg dump` must never write cluster-scoped roles.dpg or the snapshot to
// the real project — both used to bypass -o entirely and land in the real
// project's cluster/ObjectsDir and the registered snapshot store regardless
// of where -o pointed. A live ROLE (cluster-scoped, so it's the object kind
// that specifically exercises the previously-broken write path) is applied
// against a real Postgres container, then dumped with -o pointed at a
// scratch directory; asserts roles.dpg and the snapshot both land under the
// scratch directory and that the real project's cluster ObjectsDir is never
// even created.
func TestDumpDashOSandboxesClusterOutput(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.Start(t)
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// Apply a real cluster-scoped ROLE so introspection has something to
	// route into the cluster-scoped bucket (isClusterScoped) and trigger
	// the roles.dpg write path.
	roleDir := t.TempDir()
	roleFile := filepath.Join(roleDir, "roles.dpg")
	if err := os.WriteFile(roleFile, []byte(`ROLE dump_o_probe NOLOGIN;`), 0o644); err != nil {
		t.Fatalf("write role fixture: %v", err)
	}
	desired, _, err := compiler.Compile([]string{roleFile}, roleDir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile role fixture: %v", err)
	}
	ops, err := diff.New().Diff(desired, &pipeline.Snapshot{})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	migration, err := emit.New().Emit(ops, pipeline.MigrationMeta{Cluster: "test", Database: "dpgtest"})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := executor.New().Apply(ctx, migration, conn); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Real "project" locations that -o must never touch.
	realProjectDir := t.TempDir()
	realClusterDir := filepath.Join(realProjectDir, "cluster1")
	realObjectsDir := filepath.Join(realClusterDir, "cluster")

	cl := &project.Cluster{
		Dir: realClusterDir,
		Config: config.ClusterConfig{
			Cluster: config.ClusterDef{Name: "cluster1", URL: connStr},
		},
		ObjectsDir: realObjectsDir,
	}
	db := &project.Database{
		Dir:    filepath.Join(realClusterDir, "db1"),
		Config: config.DatabaseConfig{Database: config.DatabaseDef{Name: "db1"}},
	}

	// Scratch -o target, structurally separate from realProjectDir.
	scratchOut := t.TempDir()
	dbOut := filepath.Join(scratchOut, "db1")
	clusterOut := filepath.Join(scratchOut, cl.Name(), "cluster")
	scratchStore := &snapshot.FileStore{Dir: filepath.Join(scratchOut, ".dpg", "snapshots")}

	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}
	if err := runDump(cl, db, dbOut, clusterOut, introspect.New(), scratchStore, &mockSecretResolver{}, fmtOpts); err != nil {
		t.Fatalf("runDump: %v", err)
	}

	// The real project's cluster ObjectsDir must never even be created.
	if _, statErr := os.Stat(realObjectsDir); !os.IsNotExist(statErr) {
		t.Errorf("real project cluster ObjectsDir %s must not be created by a sandboxed -o dump (stat err: %v)", realObjectsDir, statErr)
	}

	// roles.dpg must land under the scratch clusterOut instead.
	scratchRoles := filepath.Join(clusterOut, "roles.dpg")
	rolesContent, err := os.ReadFile(scratchRoles)
	if err != nil {
		t.Fatalf("expected roles.dpg at %s: %v", scratchRoles, err)
	}
	if !strings.Contains(string(rolesContent), "dump_o_probe") {
		t.Errorf("scratch roles.dpg missing the applied role, got: %s", rolesContent)
	}

	// The snapshot must land under the scratch store's directory, both for
	// the database and the cluster snapshot key.
	dbSnap, err := scratchStore.Load(cl.Name(), db.Name())
	if err != nil {
		t.Fatalf("load scratch db snapshot: %v", err)
	}
	if len(dbSnap.Objects) == 0 && dbSnap.DPGVersion == "" {
		t.Error("expected a populated db snapshot written to the scratch store")
	}
	clusterSnap, err := scratchStore.Load(cl.Name(), cl.ClusterSnapshotKey())
	if err != nil {
		t.Fatalf("load scratch cluster snapshot: %v", err)
	}
	if clusterSnap.DPGVersion == "" {
		t.Error("expected a populated cluster snapshot written to the scratch store")
	}

	// The real project's own .dpg/ directory (where its snapshot would live)
	// must never be created by a sandboxed -o dump either.
	if _, statErr := os.Stat(filepath.Join(realProjectDir, ".dpg")); !os.IsNotExist(statErr) {
		t.Errorf("real project .dpg dir must not be created by a sandboxed -o dump (stat err: %v)", statErr)
	}
}
