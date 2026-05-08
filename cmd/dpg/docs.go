package main

import (
	"github.com/spf13/cobra"

	"github.com/dullkingsman/dpg/internal/docssite"
)

func newDocsCmd() *cobra.Command {
	var port int
	var open bool

	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Serve the DPG documentation locally",
		Long: `Start a local HTTP server and serve the embedded DPG documentation.

The documentation site is compiled into the binary at release build time.
Development builds (make build) do not embed the documentation; use a
release binary or build with: make build-full`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return docssite.Serve(port, open)
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 6060, "port to serve on")
	cmd.Flags().BoolVar(&open, "open", false, "open the browser automatically")

	return cmd
}
