package emit

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/ui"
)

// RenderOptions controls what the renderer writes.
type RenderOptions struct {
	// ShowSafety annotates each statement with its safety class.
	ShowSafety bool
	// ShowSourcePos annotates each statement with its source location.
	ShowSourcePos bool
	// Color enables ANSI colour output.
	Color bool
}

// DefaultRenderOptions returns options matching the RFC §20.2 format (no colour).
func DefaultRenderOptions() RenderOptions {
	return RenderOptions{ShowSafety: true, ShowSourcePos: true}
}

// ColoredRenderOptions returns options with colour output enabled.
func ColoredRenderOptions() RenderOptions {
	return RenderOptions{ShowSafety: true, ShowSourcePos: true, Color: true}
}

// Render writes a Migration to w in the RFC §20.2 SQL format.
func Render(w io.Writer, m pipeline.Migration, opts RenderOptions) error {
	c := opts.Color
	dim := func(s string) string { return ui.Dim(s, c) }
	cyan := func(s string) string { return ui.Cyan(s, c) }

	genAt := m.Meta.GeneratedAt
	if genAt.IsZero() {
		genAt = time.Now().UTC()
	}

	// Header block.
	fmt.Fprintf(w, "%s\n", dim("-- DPG Migration"))
	fmt.Fprintf(w, "%s %s\n", dim("-- Generated:      "), genAt.UTC().Format(time.RFC3339))
	if m.Meta.SourceRevision != "" {
		fmt.Fprintf(w, "%s %s\n", dim("-- Source revision:"), m.Meta.SourceRevision)
	}
	if m.Meta.Cluster != "" {
		fmt.Fprintf(w, "%s %s\n", dim("-- Cluster:        "), cyan(m.Meta.Cluster))
	}
	if m.Meta.Database != "" {
		fmt.Fprintf(w, "%s %s\n", dim("-- Database:       "), cyan(m.Meta.Database))
	}

	if len(m.Transactional) == 0 && len(m.NonTransactional) == 0 {
		fmt.Fprintf(w, "\n%s\n", dim("-- (no changes)"))
		return nil
	}

	// Transactional block.
	if len(m.Transactional) > 0 {
		fmt.Fprintf(w, "\n%s\n", ui.HighlightSQL("BEGIN;", c))
		for _, op := range m.Transactional {
			writeOp(w, op, opts)
		}
		fmt.Fprintf(w, "\n%s\n", ui.HighlightSQL("COMMIT;", c))
	}

	// Non-transactional steps.
	if len(m.NonTransactional) > 0 {
		fmt.Fprintf(w, "\n%s\n", dim("-- Non-transactional steps (run outside transaction):"))
		for _, op := range m.NonTransactional {
			writeOp(w, op, opts)
		}
	}

	return nil
}

func writeOp(w io.Writer, op pipeline.DiffOp, opts RenderOptions) {
	c := opts.Color
	var parts []string

	if opts.ShowSourcePos {
		if pos := op.Pos(); pos.File != "" {
			posStr := fmt.Sprintf("%s:%d", pos.File, pos.Line)
			parts = append(parts, "source: "+ui.Magenta(posStr, c))
		}
	}
	if opts.ShowSafety && op.Safety() != pipeline.Safe {
		parts = append(parts, "safety: "+safetyLabel(op.Safety(), c))
	}

	fmt.Fprintln(w)
	if len(parts) > 0 {
		fmt.Fprintf(w, "%s %s\n", ui.Dim("--", c), strings.Join(parts, ui.Dim(", ", c)))
	}
	fmt.Fprintf(w, "%s\n", ui.HighlightSQL(op.SQL(), c))
}

func safetyLabel(s pipeline.Safety, color bool) string {
	switch s {
	case pipeline.Caution:
		return ui.Yellow(s.String(), color)
	case pipeline.Destructive:
		return ui.Red(s.String(), color)
	case pipeline.Manual:
		return ui.Blue(s.String(), color)
	default:
		return s.String()
	}
}
