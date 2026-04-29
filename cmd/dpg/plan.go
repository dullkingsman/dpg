package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/project"
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
	)

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Diff desired state vs snapshot and print the SQL migration",
		Long: `Compare .dpg source files against the committed snapshot and print the
minimal SQL required to reach the desired state. No database connection
is required. Safe, Caution, Destructive, and Manual operations are
labelled in the output.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, err := discoverProject()
			if err != nil {
				return err
			}

			store, err := pipeline.MustResolve[pipeline.SnapshotStore](pipeline.Default, pipeline.KeySnapshotStore)
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

			if len(proj.Clusters) == 0 {
				return fmt.Errorf("no clusters found under project root %s\n  (each cluster must be a subdirectory containing dpg.toml)", proj.RootDir)
			}
			printed := false
			for _, cl := range proj.Clusters {
				if clusterName != "" && cl.Name() != clusterName {
					continue
				}
				if len(cl.Databases) == 0 {
					ui.PrintInfo(os.Stderr, cl.Name(), "no databases configured", ui.IsColorEnabled(os.Stderr))
					continue
				}
				for _, db := range cl.Databases {
					if databaseName != "" && db.Name() != databaseName {
						continue
					}
					printed = true
					if err := runPlan(cl, db, store, differ, emitter); err != nil {
						return fmt.Errorf("%s/%s: %w", cl.Name(), db.Name(), err)
					}
				}
			}
			if !printed {
				if clusterName != "" || databaseName != "" {
					return fmt.Errorf("no cluster/database matched (--cluster=%q --database=%q)", clusterName, databaseName)
				}
				return fmt.Errorf("no databases found in any cluster under %s", proj.RootDir)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster name to plan (default: all clusters)")
	cmd.Flags().StringVar(&databaseName, "database", "", "database name to plan (default: all databases)")

	return cmd
}

func runPlan(
	cl *project.Cluster,
	db *project.Database,
	store pipeline.SnapshotStore,
	differ pipeline.Differ,
	emitter pipeline.Emitter,
) error {
	color := ui.IsColorEnabled(os.Stdout)
	errColor := ui.IsColorEnabled(os.Stderr)

	desired, err := compiler.Compile(db.SourceFiles, pipeline.Default)
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

	snap, err := store.Load(cl.Name(), db.Name())
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
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
	// Detached HEAD: raw 40-char hash.
	if len(ref) >= 7 && len(ref) < 10 {
		return ref[:7], nil
	}
	return "", nil
}
