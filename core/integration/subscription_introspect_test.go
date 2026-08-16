//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/introspect"
	"github.com/dullkingsman/dpg/internal/pipeline"
	snapshotpkg "github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestSubscriptionPlanLiveNoSpuriousRecreate proves the actual bug §6z/§6ff
// closed: before introspectSubscriptions existed, a subscription created via
// apply was invisible to plan --live's live-comparison snapshot (built
// purely from introspection — nothing else populates it), so plan --live
// proposed a spurious CREATE SUBSCRIPTION for a subscription that already
// existed, which would then error on real apply ("subscription ... already
// exists"). This reproduces plan --live's actual mechanism end to end
// (compile desired source -> introspect the live catalog ->
// snapshot.Populate -> diff.Diff), the same sequence cmd/dpg/plan.go's
// introspectSnapshot + buildPlan use, rather than asserting against
// introspection in isolation.
func TestSubscriptionPlanLiveNoSpuriousRecreate(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.StartLogical(t)
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	// connect = false: this test proves the diff/introspection contract, not
	// replication itself (TestSubscriptionConnectionSecretRoundtrip already
	// proves a real replicating connection separately).
	const connInfo = "host=127.0.0.1 port=5432 dbname=dpgtest user=dpg password=dpg"
	for _, stmt := range []string{
		`CREATE PUBLICATION my_pub FOR ALL TABLES`,
		"CREATE SUBSCRIPTION my_sub CONNECTION '" + connInfo + "' PUBLICATION my_pub " +
			"WITH (connect = false, create_slot = false)",
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	fixture := "SUBSCRIPTION my_sub\n" +
		"    CONNECTION '" + connInfo + "'\n" +
		"    PUBLICATION my_pub\n" +
		"    WITH (connect = false, create_slot = false);\n"
	dir := t.TempDir()
	f := filepath.Join(dir, "sub.dpg")
	if err := os.WriteFile(f, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	desired, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	liveObjects, err := introspect.New().Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	liveSnap := &pipeline.Snapshot{}
	if err := snapshotpkg.Populate(liveSnap, liveObjects); err != nil {
		t.Fatalf("populate live snapshot: %v", err)
	}

	differ := diff.New()
	ops, err := differ.Diff(desired, liveSnap)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	for _, op := range ops {
		if strings.Contains(op.SQL(), "CREATE SUBSCRIPTION") {
			t.Fatalf("spurious CREATE SUBSCRIPTION proposed for an already-existing subscription: %s", op.SQL())
		}
	}
}
