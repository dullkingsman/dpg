package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/ui"
)

func newPortabilityCmd() *cobra.Command {
	var (
		clusterName  string
		databaseName string
	)

	cmd := &cobra.Command{
		Use:   "portability",
		Short: "Report PostgreSQL-specific constructs in use",
		Long: `Parses the .dpg source files and reports all constructs that are
PostgreSQL-specific (not covered by ISO/IEC 9075 standard SQL), along
with standard SQL alternatives where available.

This command never blocks compilation or apply.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, err := discoverProject()
			if err != nil {
				return err
			}

			clusters, err := resolveClusters(proj, clusterName)
			if err != nil {
				return err
			}

			analyzer, err := pipeline.MustResolve[pipeline.PortabilityAnalyzer](pipeline.Default, pipeline.KeyPortabilityAnalyzer)
			if err != nil {
				return err
			}

			color := ui.IsColorEnabled(os.Stdout)

			for _, cl := range clusters {
				databases, err := resolveDatabases(cl, databaseName)
				if err != nil {
					return err
				}
				for _, db := range databases {
					objects, err := compiler.Compile(db.SourceFiles, db.Dir, pipeline.Default)
					if err != nil {
						return fmt.Errorf("%s/%s: %w", cl.Name(), db.Name(), err)
					}
					issues, err := analyzer.Analyze(objects)
					if err != nil {
						return fmt.Errorf("%s/%s: analyze: %w", cl.Name(), db.Name(), err)
					}
					label := cl.Name() + "/" + db.Name()
					if len(issues) == 0 {
						ui.PrintInfo(os.Stdout, label, "no portability issues found", color)
						continue
					}
					fmt.Fprintf(os.Stdout, "%s  %s\n\n",
						ui.DimCyan(label, color),
						fmt.Sprintf("%d portability issue(s)", len(issues)),
					)
					for _, iss := range issues {
						ui.PrintPortabilityIssue(os.Stdout, iss.Pos, iss.Construct, iss.Alternative, color)
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster to analyze (required when multiple clusters exist)")
	cmd.Flags().StringVar(&databaseName, "database", "", "database to analyze (required when multiple databases exist)")

	return cmd
}
