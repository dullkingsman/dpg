package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/project"
	snapshotpkg "github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/ui"
)

var defaultLinterConfig = pipeline.LinterConfig{
	WarnOnDeprecated:         true,
	RequireColumnComments:    false,
	ForbidHardcodedPasswords: true,
}

func newPlanCmd() *cobra.Command {
	var (
		clusterName  string
		databaseName string
		live         bool
	)

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Diff desired state vs snapshot and print the SQL migration",
		Long: `Compare .dpg source files against the committed snapshot (or the live
database with --live) and print the minimal SQL required to reach the
desired state. No database connection is required unless --live is set.

Safe, Caution, Destructive, and Manual operations are labelled in the output.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, err := discoverProject()
			if err != nil {
				return err
			}

			clusters, err := resolveClusters(proj, clusterName)
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

			// --live resolvers (only needed when live mode is active)
			var (
				introspector   pipeline.Introspector
				secretResolver pipeline.SecretResolver
				store          pipeline.SnapshotStore
			)
			if live {
				loadEnv(proj, envFile)
				introspector, err = pipeline.MustResolve[pipeline.Introspector](pipeline.Default, pipeline.KeyIntrospector)
				if err != nil {
					return err
				}
				secretResolver, err = pipeline.MustResolve[pipeline.SecretResolver](pipeline.Default, pipeline.KeySecretResolver)
				if err != nil {
					return err
				}
			} else {
				store, err = pipeline.MustResolve[pipeline.SnapshotStore](pipeline.Default, pipeline.KeySnapshotStore)
				if err != nil {
					return err
				}
			}

			for _, cl := range clusters {
				databases, err := resolveDatabases(cl, databaseName)
				if err != nil {
					return err
				}
				for _, db := range databases {
					var snap *pipeline.Snapshot
					if live {
						snap, err = introspectSnapshot(cmd.Context(), cl, secretResolver, introspector)
						if err != nil {
							return fmt.Errorf("%s/%s: %w", cl.Name(), db.Name(), err)
						}
					} else {
						snap, err = store.Load(cl.Name(), db.Name())
						if err != nil {
							return fmt.Errorf("%s/%s: snapshot: %w", cl.Name(), db.Name(), err)
						}
					}
					if err := runPlan(cl, db, snap, differ, emitter); err != nil {
						return fmt.Errorf("%s/%s: %w", cl.Name(), db.Name(), err)
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster to plan (required when multiple clusters exist)")
	cmd.Flags().StringVar(&databaseName, "database", "", "database to plan (required when multiple databases exist)")
	cmd.Flags().BoolVar(&live, "live", false, "diff against the live database instead of the stored snapshot")

	return cmd
}

// introspectSnapshot connects to the live database for cl, introspects its
// catalog, and returns a Snapshot built from the live state.
func introspectSnapshot(ctx context.Context, cl *project.Cluster, secretResolver pipeline.SecretResolver, introspector pipeline.Introspector) (*pipeline.Snapshot, error) {
	connStr := cl.ConnectionString()
	if connStr == "" {
		return nil, fmt.Errorf("cluster %q has no connection configured (set url or link in cluster dpg.toml)", cl.Name())
	}
	if cl.IsLink() {
		var err error
		connStr, err = secretResolver.Resolve(connStr)
		if err != nil {
			return nil, ui.WrapDB(fmt.Errorf("resolve connection secret: %w", err))
		}
	}
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		return nil, ui.WrapDB(err)
	}
	defer conn.Close(ctx)

	liveObjects, err := introspector.Introspect(ctx, conn)
	if err != nil {
		return nil, ui.WrapDB(fmt.Errorf("introspect: %w", err))
	}

	snap := &pipeline.Snapshot{}
	if err := snapshotpkg.Populate(snap, liveObjects); err != nil {
		return nil, fmt.Errorf("build live snapshot: %w", err)
	}
	return snap, nil
}

func runPlan(
	cl *project.Cluster,
	db *project.Database,
	snap *pipeline.Snapshot,
	differ pipeline.Differ,
	emitter pipeline.Emitter,
) error {
	color := ui.IsColorEnabled(os.Stdout)
	errColor := ui.IsColorEnabled(os.Stderr)

	desired, err := compiler.Compile(db.SourceFiles, db.Dir, pipeline.Default)
	if err != nil {
		return err
	}

	if linter, ok := pipeline.Resolve[pipeline.Linter](pipeline.Default, pipeline.KeyLinter); ok {
		diags, lintErr := linter.Lint(desired, defaultLinterConfig)
		if lintErr != nil {
			return lintErr
		}
		if ui.PrintLintDiagnostics(os.Stderr, diags, errColor) {
			return ui.ErrSilent
		}
	}

	ops, err := differ.Diff(desired, snap)
	if err != nil {
		return err
	}

	rev, _ := gitRevision()
	migration, err := emitter.Emit(ops, pipeline.MigrationMeta{
		GeneratedAt:    time.Now().UTC(),
		SourceRevision: rev,
		Cluster:        cl.Name(),
		Database:       db.Name(),
	})
	if err != nil {
		return err
	}

	return emit.Render(os.Stdout, migration, emit.RenderOptions{
		ShowSafety:    true,
		ShowSourcePos: true,
		Color:         color,
	})
}

// gitRevision returns the current HEAD short hash, or "" if git is unavailable.
func gitRevision() (string, error) {
	data, err := os.ReadFile(".git/HEAD")
	if err != nil {
		return "", nil
	}
	ref := string(data)
	if len(ref) >= 7 && len(ref) < 10 {
		return ref[:7], nil
	}
	return "", nil
}
