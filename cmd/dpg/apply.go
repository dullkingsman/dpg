package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/project"
	snapshotpkg "github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/ui"
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
			loadEnv(proj, envFile)

			clusters, err := resolveClusters(proj, clusterName)
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

			_ = approvePartitionRebuild
			opts := applyOptions{
				yes:              yes,
				allowDestructive: allowDestructive,
				migrationsDir:    proj.MigrationsDir(),
				lintCfg:          linterConfigFrom(proj.RootConfig.Linter),
			}

			for _, cl := range clusters {
				// Apply cluster-level objects (roles) before databases.
				if len(cl.SourceFiles) > 0 {
					if err := runClusterApply(cl, store, differ, emitter, applyExec, secretResolver, opts); err != nil {
						return fmt.Errorf("%s (cluster): %w", cl.Name(), err)
					}
				}

				databases, err := resolveDatabases(cl, databaseName)
				if err != nil {
					return err
				}
				for _, db := range databases {
					if err := runApply(cl, db, store, differ, emitter, applyExec, secretResolver, opts); err != nil {
						return fmt.Errorf("%s/%s: %w", cl.Name(), db.Name(), err)
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster to apply (required when multiple clusters exist)")
	cmd.Flags().StringVar(&databaseName, "database", "", "database to apply (required when multiple databases exist)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip interactive approval prompt")
	cmd.Flags().BoolVar(&allowDestructive, "allow-destructive", false, "allow destructive operations")
	cmd.Flags().BoolVar(&approvePartitionRebuild, "approve-partition-rebuild", false,
		"allow partition strategy rebuild (implies --allow-destructive for partition ops)")

	return cmd
}

type applyOptions struct {
	yes              bool
	allowDestructive bool
	migrationsDir    string
	lintCfg          pipeline.LinterConfig
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
	color := ui.IsColorEnabled(os.Stdout)
	errColor := ui.IsColorEnabled(os.Stderr)

	desired, err := compiler.Compile(db.SourceFiles, db.Dir, pipeline.Default)
	if err != nil {
		return err
	}

	if linter, ok := pipeline.Resolve[pipeline.Linter](pipeline.Default, pipeline.KeyLinter); ok {
		diags, lintErr := linter.Lint(desired, opts.lintCfg)
		if lintErr != nil {
			return lintErr
		}
		if ui.PrintLintDiagnostics(os.Stderr, diags, errColor) {
			return ui.ErrSilent
		}
	}

	snap, err := store.Load(cl.Name(), db.Name())
	if err != nil {
		return fmt.Errorf("snapshot load: %w", err)
	}

	ops, err := differ.Diff(desired, snap)
	if err != nil {
		return err
	}

	if len(ops) == 0 {
		ui.PrintInfo(os.Stdout, cl.Name()+"/"+db.Name(), "already up to date", color)
		return nil
	}

	// Warn about new tables being created without a primary key.
	warnMissingPK(os.Stderr, desired, snap, errColor)

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

	// Render plain SQL for the archive file.
	var sqlBuf strings.Builder
	if err := emit.Render(&sqlBuf, migration, emit.DefaultRenderOptions()); err != nil {
		return err
	}
	plainSQL := sqlBuf.String()

	// Print with colour to stdout.
	if err := emit.Render(os.Stdout, migration, emit.RenderOptions{
		ShowSafety:    true,
		ShowSourcePos: true,
		Color:         color,
	}); err != nil {
		return err
	}

	if !opts.yes {
		fmt.Printf("\n%s [y/N] ", ui.Bold("Apply this migration?", color))
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || !strings.EqualFold(strings.TrimSpace(scanner.Text()), "y") {
			ui.PrintInfo(os.Stdout, "", "Aborted.", color)
			return nil
		}
	}

	connStr := cl.ConnectionString()
	if connStr == "" {
		return fmt.Errorf("cluster %q has no connection configured (set url or link in cluster dpg.toml)", cl.Name())
	}
	if cl.IsLink() {
		connStr, err = secretResolver.Resolve(connStr)
		if err != nil {
			return ui.WrapDB(fmt.Errorf("resolve connection secret: %w", err))
		}
	}

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		return ui.WrapDB(err)
	}
	defer conn.Close(ctx)

	if err := applyExec.Apply(ctx, migration, conn); err != nil {
		return ui.WrapDB(err)
	}

	// Archive migration file before updating snapshot.
	migPath, err := snapshotpkg.SaveMigration(opts.migrationsDir, cl.Name(), db.Name(), plainSQL)
	if err != nil {
		ui.PrintInfo(os.Stderr, "warn", "could not archive migration file: "+err.Error(), errColor)
	}

	newSnap := &pipeline.Snapshot{}
	if err := snapshotpkg.Populate(newSnap, desired); err != nil {
		return fmt.Errorf("build snapshot: %w", err)
	}
	if err := store.Save(cl.Name(), db.Name(), newSnap); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}

	detail := cl.Name() + "/" + db.Name() + " — snapshot updated"
	if migPath != "" {
		detail += "\n         " + ui.Dim(migPath, color)
	}
	ui.PrintSuccess(os.Stdout, "Applied", detail, color)
	return nil
}

// runClusterApply applies cluster-level objects (roles, tablespaces, etc.).
func runClusterApply(
	cl *project.Cluster,
	store pipeline.SnapshotStore,
	differ pipeline.Differ,
	emitter pipeline.Emitter,
	applyExec pipeline.ApplyExecutor,
	secretResolver pipeline.SecretResolver,
	opts applyOptions,
) error {
	ctx := context.Background()
	color := ui.IsColorEnabled(os.Stdout)
	errColor := ui.IsColorEnabled(os.Stderr)

	desired, err := compiler.Compile(cl.SourceFiles, cl.ObjectsDir, pipeline.Default)
	if err != nil {
		return err
	}

	if linter, ok := pipeline.Resolve[pipeline.Linter](pipeline.Default, pipeline.KeyLinter); ok {
		diags, lintErr := linter.Lint(desired, opts.lintCfg)
		if lintErr != nil {
			return lintErr
		}
		if ui.PrintLintDiagnostics(os.Stderr, diags, errColor) {
			return ui.ErrSilent
		}
	}

	snap, err := store.Load(cl.Name(), cl.ClusterSnapshotKey())
	if err != nil {
		return fmt.Errorf("snapshot load: %w", err)
	}

	ops, err := differ.Diff(desired, snap)
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		ui.PrintInfo(os.Stdout, cl.Name()+" (cluster)", "already up to date", color)
		return nil
	}

	if !opts.allowDestructive {
		for _, op := range ops {
			if op.Safety() == pipeline.Destructive {
				return fmt.Errorf("cluster migration contains destructive operations; re-run with --allow-destructive\n  first: %s", op.SQL())
			}
		}
	}

	rev, _ := gitRevision()
	migration, err := emitter.Emit(ops, pipeline.MigrationMeta{
		GeneratedAt:    time.Now().UTC(),
		SourceRevision: rev,
		Cluster:        cl.Name(),
		Database:       cl.ClusterSnapshotKey(),
	})
	if err != nil {
		return err
	}

	var sqlBuf strings.Builder
	if err := emit.Render(&sqlBuf, migration, emit.DefaultRenderOptions()); err != nil {
		return err
	}

	if err := emit.Render(os.Stdout, migration, emit.RenderOptions{
		ShowSafety:    true,
		ShowSourcePos: true,
		Color:         color,
	}); err != nil {
		return err
	}

	if !opts.yes {
		fmt.Printf("\n%s [y/N] ", ui.Bold("Apply cluster changes?", color))
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || !strings.EqualFold(strings.TrimSpace(scanner.Text()), "y") {
			ui.PrintInfo(os.Stdout, "", "Aborted.", color)
			return nil
		}
	}

	connStr := cl.ConnectionString()
	if connStr == "" {
		return fmt.Errorf("cluster %q has no connection configured", cl.Name())
	}
	if cl.IsLink() {
		connStr, err = secretResolver.Resolve(connStr)
		if err != nil {
			return ui.WrapDB(fmt.Errorf("resolve connection secret: %w", err))
		}
	}

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		return ui.WrapDB(err)
	}
	defer conn.Close(ctx)

	if err := applyExec.Apply(ctx, migration, conn); err != nil {
		return ui.WrapDB(err)
	}

	migPath, err := snapshotpkg.SaveMigration(opts.migrationsDir, cl.Name(), cl.ClusterSnapshotKey(), sqlBuf.String())
	if err != nil {
		ui.PrintInfo(os.Stderr, "warn", "could not archive cluster migration: "+err.Error(), errColor)
	}

	newSnap := &pipeline.Snapshot{}
	if err := snapshotpkg.Populate(newSnap, desired); err != nil {
		return fmt.Errorf("build cluster snapshot: %w", err)
	}
	if err := store.Save(cl.Name(), cl.ClusterSnapshotKey(), newSnap); err != nil {
		return fmt.Errorf("save cluster snapshot: %w", err)
	}

	detail := cl.Name() + " (cluster) — snapshot updated"
	if migPath != "" {
		detail += "\n         " + ui.Dim(migPath, color)
	}
	ui.PrintSuccess(os.Stdout, "Applied", detail, color)
	return nil
}

// warnMissingPK writes a bold warning for every table that is being newly
// created (absent from snap) and has no PRIMARY KEY constraint.
func warnMissingPK(w io.Writer, desired []pipeline.IRObject, snap *pipeline.Snapshot, color bool) {
	for _, obj := range desired {
		tbl, ok := obj.(*ir.Table)
		if !ok {
			continue
		}
		// Only warn for new tables — existing ones are the user's responsibility.
		if _, exists := snap.Objects[tbl.QualifiedName()]; exists {
			continue
		}
		hasPK := false
		for _, cst := range tbl.Constraints {
			if cst.Type == "PRIMARY KEY" {
				hasPK = true
				break
			}
		}
		if !hasPK {
			fmt.Fprintf(w, "%s  table %s has no PRIMARY KEY — consider adding one\n",
				ui.Bold("WARNING", color), tbl.QualifiedName())
		}
	}
}
