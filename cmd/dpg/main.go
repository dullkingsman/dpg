package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dullkingsman/dpg/internal/version"

	// Import default pipeline implementations to trigger their init() registration.
	_ "github.com/dullkingsman/dpg/internal/blockparser"
	_ "github.com/dullkingsman/dpg/internal/diff"
	_ "github.com/dullkingsman/dpg/internal/emit"
	_ "github.com/dullkingsman/dpg/internal/executor"
	_ "github.com/dullkingsman/dpg/internal/graph"
	_ "github.com/dullkingsman/dpg/internal/introspect"
	_ "github.com/dullkingsman/dpg/internal/ir"
	_ "github.com/dullkingsman/dpg/internal/linter"
	_ "github.com/dullkingsman/dpg/internal/merger"
	_ "github.com/dullkingsman/dpg/internal/pgparser"
	_ "github.com/dullkingsman/dpg/internal/portability"
	_ "github.com/dullkingsman/dpg/internal/scanner"
	_ "github.com/dullkingsman/dpg/internal/secrets"
	_ "github.com/dullkingsman/dpg/internal/snapshot"
)

var projectDir string

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "dpg",
		Short:   "Declarative PG — schema compiler and migration tool",
		Version: fmt.Sprintf("%s (commit: %s, built: %s)", version.Version, version.Commit, version.Date),
		Long: `DPG is a declarative, state-based superset of PostgreSQL SQL that compiles
to idiomatic PG DDL. Describe what your database should be; DPG figures
out what needs to change.

Source: https://github.com/dullkingsman/dpg`,
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVarP(
		&projectDir, "project-dir", "C", "",
		"project root directory (default: working directory)",
	)

	root.AddCommand(
		newPlanCmd(),
		newApplyCmd(),
		newVerifyCmd(),
		newDumpCmd(),
		newDiffCmd(),
		newPortabilityCmd(),
	)

	return root
}

// resolveProjectDir returns the effective project root directory.
func resolveProjectDir() (string, error) {
	if projectDir != "" {
		abs, err := absPath(projectDir)
		if err != nil {
			return "", fmt.Errorf("--project-dir: %w", err)
		}
		return abs, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot get working directory: %w", err)
	}
	return dir, nil
}

func absPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}
