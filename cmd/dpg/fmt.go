package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dullkingsman/dpg/internal/format"
	"github.com/dullkingsman/dpg/internal/project"
)

func newFmtCmd() *cobra.Command {
	var (
		check bool
		diff  bool
	)

	cmd := &cobra.Command{
		Use:   "fmt [files or dirs...]",
		Short: "Format .dpg source files in place",
		Long: `Reformat .dpg source files to the canonical DPG style.

With no arguments, all .dpg files under the project root are formatted.
Pass specific files or directories to restrict formatting to those paths.

  --check   Exit 1 if any file would change; no files are written (CI gate).
  --diff    Print a unified diff of what would change; no files are written.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := format.Options{
				IndentSize:  4,
				KeywordCase: "upper",
			}

			// Discover the project to get formatter config. If no project is
			// found (e.g. formatting standalone files), fall back to defaults.
			proj, projErr := discoverProject()
			if projErr == nil {
				if proj.RootConfig.Fmt.IndentSize > 0 {
					opts.IndentSize = proj.RootConfig.Fmt.IndentSize
				}
				if proj.RootConfig.Fmt.KeywordCase != "" {
					opts.KeywordCase = proj.RootConfig.Fmt.KeywordCase
				}
			}

			targets, err := resolveFmtTargetsRaw(proj, projErr, args)
			if err != nil {
				return err
			}
			if len(targets) == 0 {
				return fmt.Errorf("dpg fmt: no .dpg files found")
			}

			return runFmt(targets, opts, check, diff)
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "exit 1 if any file would change (no files written)")
	cmd.Flags().BoolVar(&diff, "diff", false, "print unified diff of changes (no files written)")
	return cmd
}

// resolveFmtTargetsRaw returns the list of .dpg files to format.
// proj/projErr are the results of discoverProject — proj may be nil.
// If args is empty and a project was found, all project source files are used.
// If args is empty and no project, returns an error.
// If args are given, resolves each as a file or directory regardless of project.
func resolveFmtTargetsRaw(proj *project.Project, projErr error, args []string) ([]string, error) {
	if len(args) == 0 {
		if projErr != nil {
			return nil, fmt.Errorf("dpg fmt: no files specified and no project found: %w", projErr)
		}
		var files []string
		for _, cluster := range proj.Clusters {
			files = append(files, cluster.SourceFiles...)
			for _, db := range cluster.Databases {
				files = append(files, db.SourceFiles...)
			}
		}
		return files, nil
	}

	var files []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("fmt: %w", err)
		}
		if info.IsDir() {
			walked, err := walkDpgFiles(arg)
			if err != nil {
				return nil, err
			}
			files = append(files, walked...)
		} else if strings.HasSuffix(arg, ".dpg") {
			files = append(files, arg)
		}
	}
	return files, nil
}

// walkDpgFiles recursively finds .dpg files under dir.
func walkDpgFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".dpg") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// runFmt formats each file according to opts.
// In --check mode it reports which files would change and returns a non-nil
// error if any would. In --diff mode it prints a human-readable diff.
// Otherwise it writes formatted content back to disk.
func runFmt(files []string, opts format.Options, check, showDiff bool) error {
	anyChanged := false

	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("fmt: reading %s: %w", path, err)
		}

		formatted, err := format.Format(path, src, opts)
		if err != nil {
			// Non-fatal: print warning and skip the file.
			fmt.Fprintf(os.Stderr, "dpg fmt: warning: could not format %s: %v\n", path, err)
			continue
		}

		if bytes.Equal(src, formatted) {
			continue
		}

		anyChanged = true

		if check {
			fmt.Fprintf(os.Stderr, "would reformat: %s\n", path)
			continue
		}

		if showDiff {
			printUnifiedDiff(os.Stdout, path, string(src), string(formatted))
			continue
		}

		if err := os.WriteFile(path, formatted, 0644); err != nil {
			return fmt.Errorf("fmt: writing %s: %w", path, err)
		}
	}

	if check && anyChanged {
		return fmt.Errorf("dpg fmt --check: %d file(s) would be reformatted", countChanged(files, opts))
	}
	return nil
}

// countChanged returns the number of files that would be reformatted.
func countChanged(files []string, opts format.Options) int {
	n := 0
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		formatted, err := format.Format(path, src, opts)
		if err != nil {
			continue
		}
		if !bytes.Equal(src, formatted) {
			n++
		}
	}
	return n
}

// printUnifiedDiff writes a minimal unified diff of old vs new to w.
func printUnifiedDiff(w *os.File, name, old, new string) {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	fmt.Fprintf(w, "--- %s\n", name)
	fmt.Fprintf(w, "+++ %s\n", name)

	// Simple line-by-line diff: show all changed lines without context.
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}
	for i := 0; i < maxLen; i++ {
		oldLine := ""
		newLine := ""
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if i < len(oldLines) {
				fmt.Fprintf(w, "-%s\n", oldLine)
			}
			if i < len(newLines) {
				fmt.Fprintf(w, "+%s\n", newLine)
			}
		}
	}
}
