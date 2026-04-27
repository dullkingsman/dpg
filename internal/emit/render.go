package emit

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

// RenderOptions controls what the renderer writes.
type RenderOptions struct {
	// ShowSafety annotates each statement with its safety class.
	ShowSafety bool
	// ShowSourcePos annotates each statement with its source location.
	ShowSourcePos bool
}

// DefaultRenderOptions returns options matching the RFC §20.2 format.
func DefaultRenderOptions() RenderOptions {
	return RenderOptions{ShowSafety: true, ShowSourcePos: true}
}

// Render writes a Migration to w in the RFC §20.2 SQL format.
func Render(w io.Writer, m pipeline.Migration, opts RenderOptions) error {
	// Header comment.
	genAt := m.Meta.GeneratedAt
	if genAt.IsZero() {
		genAt = time.Now().UTC()
	}
	fmt.Fprintf(w, "-- DPG Migration\n")
	fmt.Fprintf(w, "-- Generated:       %s\n", genAt.UTC().Format(time.RFC3339))
	if m.Meta.SourceRevision != "" {
		fmt.Fprintf(w, "-- Source revision: %s\n", m.Meta.SourceRevision)
	}
	if m.Meta.Cluster != "" {
		fmt.Fprintf(w, "-- Cluster:         %s\n", m.Meta.Cluster)
	}
	if m.Meta.Database != "" {
		fmt.Fprintf(w, "-- Database:        %s\n", m.Meta.Database)
	}

	if len(m.Transactional) == 0 && len(m.NonTransactional) == 0 {
		fmt.Fprintf(w, "\n-- (no changes)\n")
		return nil
	}

	// Transactional block.
	if len(m.Transactional) > 0 {
		fmt.Fprintf(w, "\nBEGIN;\n")
		for _, op := range m.Transactional {
			writeOp(w, op, opts)
		}
		fmt.Fprintf(w, "\nCOMMIT;\n")
	}

	// Non-transactional steps.
	if len(m.NonTransactional) > 0 {
		fmt.Fprintf(w, "\n-- Non-transactional steps (executed after COMMIT):\n")
		for _, op := range m.NonTransactional {
			writeOp(w, op, opts)
		}
	}

	return nil
}

func writeOp(w io.Writer, op pipeline.DiffOp, opts RenderOptions) {
	var annotations []string
	if opts.ShowSourcePos {
		if pos := op.Pos(); pos.File != "" {
			annotations = append(annotations, fmt.Sprintf("source: %s:%d", pos.File, pos.Line))
		}
	}
	if opts.ShowSafety && op.Safety() != pipeline.Safe {
		annotations = append(annotations, fmt.Sprintf("safety: %s", op.Safety()))
	}
	if len(annotations) > 0 {
		fmt.Fprintf(w, "\n-- [%s]\n", strings.Join(annotations, ", "))
	} else {
		fmt.Fprintf(w, "\n")
	}
	fmt.Fprintf(w, "%s\n", op.SQL())
}
