package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dullkingsman/dpg/internal/ui"
)

const watchInterval = 500 * time.Millisecond

// runWatch runs fn immediately, then polls for source file changes and re-runs
// fn whenever any file's modification time changes. It exits cleanly on
// SIGINT or SIGTERM.
func runWatch(cmd *cobra.Command, fn func() error) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "dpg plan --watch: polling every %s; press Ctrl-C to stop\n\n", watchInterval)

	runAndReport := func() map[string]time.Time {
		if err := fn(); err != nil && err != ui.ErrSilent {
			fmt.Fprintf(os.Stderr, "plan error: %v\n", err)
		}
		mtimes, _ := collectProjectMtimes()
		return mtimes
	}

	prev := runAndReport()

	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			cur, err := collectProjectMtimes()
			if err != nil {
				continue
			}
			if mtimesChanged(prev, cur) {
				fmt.Fprintf(os.Stderr, "\n--- source changed, re-running ---\n\n")
				prev = runAndReport()
			} else {
				prev = cur
			}
		}
	}
}

// collectProjectMtimes re-discovers the project and returns a map of
// source-file path → mtime for all .dpg files.
func collectProjectMtimes() (map[string]time.Time, error) {
	proj, err := discoverProject()
	if err != nil {
		return nil, err
	}
	mtimes := make(map[string]time.Time)
	for _, cl := range proj.Clusters {
		for _, f := range cl.SourceFiles {
			if info, err := os.Stat(f); err == nil {
				mtimes[f] = info.ModTime()
			}
		}
		for _, db := range cl.Databases {
			for _, f := range db.SourceFiles {
				if info, err := os.Stat(f); err == nil {
					mtimes[f] = info.ModTime()
				}
			}
		}
	}
	return mtimes, nil
}

// mtimesChanged reports whether cur has any different mtimes from prev,
// including new or deleted files.
func mtimesChanged(prev, cur map[string]time.Time) bool {
	if len(prev) != len(cur) {
		return true
	}
	for path, t := range cur {
		if prev[path] != t {
			return true
		}
	}
	return false
}
