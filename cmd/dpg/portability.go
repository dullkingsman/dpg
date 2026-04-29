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

			analyzer, err := pipeline.MustResolve[pipeline.PortabilityAnalyzer](pipeline.Default, pipeline.KeyPortabilityAnalyzer)
			if err != nil {
				return err
			}

			color := ui.IsColorEnabled(os.Stdout)

			for _, cl := range proj.Clusters {
				if clusterName != "" && cl.Name() != clusterName {
					continue
				}
				for _, db := range cl.Databases {
					if databaseName != "" && db.Name() != databaseName {
						continue
					}
					objects, err := compiler.Compile(db.SourceFiles, pipeline.Default)
					if err != nil {
						return fmt.Errorf("%s/%s: %w", cl.Name(), db.Name(), err)
					}
					issues, err := analyzer.Analyze(objects)
					if err != nil {
						return fmt.Errorf("%s/%s: analyze: %w", cl.Name(), db.Name(), err)
					}
					context := cl.Name() + "/" + db.Name()
					if len(issues) == 0 {
						ui.PrintInfo(os.Stdout, context, "no portability issues found", color)
						continue
					}
					fmt.Fprintf(os.Stdout, "%s  %s\n\n",
						ui.DimCyan(context, color),
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

	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster name (default: all clusters)")
	cmd.Flags().StringVar(&databaseName, "database", "", "database name (default: all databases)")

	return cmd
}
