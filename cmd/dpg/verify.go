package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/project"
	"github.com/dullkingsman/dpg/internal/snapshot"
)

func newVerifyCmd() *cobra.Command {
	var (
		clusterName  string
		databaseName string
	)

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check the live database for drift against the snapshot",
		Long: `Introspects the live database catalog and compares it against the committed
snapshot. Reports objects present in the snapshot but absent from the live
catalog, and DPG-managed grants that are missing from the live catalog.

Extra grants present in the live catalog but absent from DPG source are
not reported (additive grant model).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, err := discoverProject()
			if err != nil {
				return err
			}

			store, err := pipeline.MustResolve[pipeline.SnapshotStore](pipeline.Default, pipeline.KeySnapshotStore)
			if err != nil {
				return err
			}
			introspector, err := pipeline.MustResolve[pipeline.Introspector](pipeline.Default, pipeline.KeyIntrospector)
			if err != nil {
				return err
			}
			differ, err := pipeline.MustResolve[pipeline.Differ](pipeline.Default, pipeline.KeyDiffer)
			if err != nil {
				return err
			}
			emitter, err := pipeline.MustResolve[pipeline.Emitter](pipeline.Default, pipeline.KeyEmitter)
			if err != nil {
				return err
			}
			secretResolver, err := pipeline.MustResolve[pipeline.SecretResolver](pipeline.Default, pipeline.KeySecretResolver)
			if err != nil {
				return err
			}

			driftFound := false
			for _, cl := range proj.Clusters {
				if clusterName != "" && cl.Name() != clusterName {
					continue
				}
				for _, db := range cl.Databases {
					if databaseName != "" && db.Name() != databaseName {
						continue
					}
					hasDrift, err := runVerify(cl, db, store, introspector, differ, emitter, secretResolver)
					if err != nil {
						return fmt.Errorf("%s/%s: %w", cl.Name(), db.Name(), err)
					}
					if hasDrift {
						driftFound = true
					}
				}
			}
			if driftFound {
				return fmt.Errorf("drift detected")
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster name (default: all clusters)")
	cmd.Flags().StringVar(&databaseName, "database", "", "database name (default: all databases)")

	return cmd
}

func runVerify(
	cl *project.Cluster,
	db *project.Database,
	store pipeline.SnapshotStore,
	introspector pipeline.Introspector,
	differ pipeline.Differ,
	emitter pipeline.Emitter,
	secretResolver pipeline.SecretResolver,
) (bool, error) {
	ctx := context.Background()

	// Load the committed snapshot (what DPG last applied).
	snap, err := store.Load(cl.Name(), db.Name())
	if err != nil {
		return false, fmt.Errorf("load snapshot: %w", err)
	}

	// Connect to the live database.
	connStr := cl.ConnectionString()
	if connStr == "" {
		return false, fmt.Errorf("cluster %q has no connection configured (set url or link in cluster dpg.toml)", cl.Name())
	}
	if cl.IsLink() {
		connStr, err = secretResolver.Resolve(connStr)
		if err != nil {
			return false, fmt.Errorf("resolve connection secret: %w", err)
		}
	}

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		return false, err
	}
	defer conn.Close(ctx)

	// Introspect the live catalog.
	liveObjects, err := introspector.Introspect(ctx, conn)
	if err != nil {
		return false, fmt.Errorf("introspect: %w", err)
	}

	// Build a synthetic snapshot from the live state and diff it against the
	// committed snapshot. Any differences represent drift.
	liveSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(liveSnap, liveObjects); err != nil {
		return false, fmt.Errorf("build live snapshot: %w", err)
	}

	// Desired = what snapshot says should be there; current = what live catalog has.
	// We diff desired (from snapshot) against live (as if snap were the "base").
	// Build desired slice from snapshot objects via a synthetic diff.
	ops, err := differ.Diff(liveObjects, snap)
	if err != nil {
		return false, fmt.Errorf("diff: %w", err)
	}

	if len(ops) == 0 {
		fmt.Printf("-- %s/%s: no drift detected\n", cl.Name(), db.Name())
		return false, nil
	}

	fmt.Fprintf(os.Stderr, "DRIFT DETECTED in %s/%s:\n", cl.Name(), db.Name())
	migration, _ := emitter.Emit(ops, pipeline.MigrationMeta{
		Cluster:  cl.Name(),
		Database: db.Name(),
	})
	_ = emit.Render(os.Stderr, migration, emit.DefaultRenderOptions())
	return true, nil
}
