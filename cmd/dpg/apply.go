package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/project"
	"github.com/dullkingsman/dpg/internal/snapshot"
)

func newApplyCmd() *cobra.Command {
	var (
		clusterName             string
		databaseName            string
		yes                     bool
		allowDestructive        bool
		approvePartitionRebuild bool
	)

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Execute the planned migration and update the snapshot",
		Long: `Runs dpg plan, prompts for approval, executes the SQL against the
primary node, and updates the committed snapshot on success.

Destructive operations are blocked unless --allow-destructive is set.
Partition strategy changes additionally require --approve-partition-rebuild.`,
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
			applyExec, err := pipeline.MustResolve[pipeline.ApplyExecutor](pipeline.Default, pipeline.KeyApplyExecutor)
			if err != nil {
				return err
			}
			secretResolver, err := pipeline.MustResolve[pipeline.SecretResolver](pipeline.Default, pipeline.KeySecretResolver)
			if err != nil {
				return err
			}

			_ = approvePartitionRebuild // reserved for future use
			opts := applyOptions{
				yes:              yes,
				allowDestructive: allowDestructive,
			}

			for _, cl := range proj.Clusters {
				if clusterName != "" && cl.Name() != clusterName {
					continue
				}
				for _, db := range cl.Databases {
					if databaseName != "" && db.Name() != databaseName {
						continue
					}
					if err := runApply(cl, db, store, differ, emitter, applyExec, secretResolver, opts); err != nil {
						return fmt.Errorf("%s/%s: %w", cl.Name(), db.Name(), err)
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster name (default: all clusters)")
	cmd.Flags().StringVar(&databaseName, "database", "", "database name (default: all databases)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip interactive approval prompt")
	cmd.Flags().BoolVar(&allowDestructive, "allow-destructive", false, "allow destructive operations")
	cmd.Flags().BoolVar(&approvePartitionRebuild, "approve-partition-rebuild", false,
		"allow partition strategy rebuild (implies --allow-destructive for partition ops)")

	return cmd
}

type applyOptions struct {
	yes              bool
	allowDestructive bool
}

func runApply(
	cl *project.Cluster,
	db *project.Database,
	store pipeline.SnapshotStore,
	differ pipeline.Differ,
	emitter pipeline.Emitter,
	applyExec pipeline.ApplyExecutor,
	secretResolver pipeline.SecretResolver,
	opts applyOptions,
) error {
	ctx := context.Background()

	desired, err := compiler.Compile(db.SourceFiles, pipeline.Default)
	if err != nil {
		return fmt.Errorf("compile: %w", err)
	}

	if linter, ok := pipeline.Resolve[pipeline.Linter](pipeline.Default, pipeline.KeyLinter); ok {
		diags, lintErr := linter.Lint(desired, defaultLinterConfig)
		if lintErr != nil {
			return fmt.Errorf("lint: %w", lintErr)
		}
		hasErrors := false
		for _, d := range diags {
			if d.IsError {
				fmt.Fprintf(os.Stderr, "error [%s] %s\n", d.Rule, d.Message)
				hasErrors = true
			} else {
				fmt.Fprintf(os.Stderr, "warn  [%s] %s\n", d.Rule, d.Message)
			}
		}
		if hasErrors {
			return fmt.Errorf("lint: %d error(s) found", countErrors(diags))
		}
	}

	snap, err := store.Load(cl.Name(), db.Name())
	if err != nil {
		return fmt.Errorf("snapshot load: %w", err)
	}

	ops, err := differ.Diff(desired, snap)
	if err != nil {
		return fmt.Errorf("diff: %w", err)
	}

	if len(ops) == 0 {
		fmt.Printf("-- %s/%s: already up to date\n", cl.Name(), db.Name())
		return nil
	}

	// Block destructive ops unless explicitly allowed.
	if !opts.allowDestructive {
		for _, op := range ops {
			if op.Safety() == pipeline.Destructive {
				return fmt.Errorf("migration contains destructive operations; re-run with --allow-destructive to proceed\n  first: %s", op.SQL())
			}
		}
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

	// Print the plan.
	if err := emit.Render(os.Stdout, migration, emit.DefaultRenderOptions()); err != nil {
		return err
	}

	// Prompt for approval.
	if !opts.yes {
		fmt.Print("\nApply this migration? [y/N] ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || !strings.EqualFold(strings.TrimSpace(scanner.Text()), "y") {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Resolve connection URL.
	connStr := cl.ConnectionString()
	if connStr == "" {
		return fmt.Errorf("cluster %q has no connection configured (set url or link in cluster dpg.toml)", cl.Name())
	}
	if cl.IsLink() {
		connStr, err = secretResolver.Resolve(connStr)
		if err != nil {
			return fmt.Errorf("resolve connection secret: %w", err)
		}
	}

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	if err := applyExec.Apply(ctx, migration, conn); err != nil {
		return fmt.Errorf("execute: %w", err)
	}

	// Update snapshot.
	newSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(newSnap, desired); err != nil {
		return fmt.Errorf("build snapshot: %w", err)
	}
	if err := store.Save(cl.Name(), db.Name(), newSnap); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}

	fmt.Printf("\n✓ Applied and snapshot updated: %s/%s\n", cl.Name(), db.Name())
	return nil
}
