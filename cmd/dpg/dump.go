package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/project"
	"github.com/dullkingsman/dpg/internal/snapshot"
)

func newDumpCmd() *cobra.Command {
	var (
		clusterName  string
		databaseName string
		outputDir    string
	)

	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Introspect a live database and produce initial .dpg source files",
		Long: `Connects to the primary node, reads the live catalog, and writes
.dpg source files and an initial snapshot to the output directory.
Use this to bootstrap a DPG project from an existing database.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if clusterName == "" {
				return fmt.Errorf("--cluster is required")
			}
			if databaseName == "" {
				return fmt.Errorf("--database is required")
			}

			dir, err := resolveProjectDir()
			if err != nil {
				return err
			}
			proj, err := project.Discover(dir)
			if err != nil {
				return err
			}

			introspector, err := pipeline.MustResolve[pipeline.Introspector](pipeline.Default, pipeline.KeyIntrospector)
			if err != nil {
				return err
			}
			store, err := pipeline.MustResolve[pipeline.SnapshotStore](pipeline.Default, pipeline.KeySnapshotStore)
			if err != nil {
				return err
			}
			secretResolver, err := pipeline.MustResolve[pipeline.SecretResolver](pipeline.Default, pipeline.KeySecretResolver)
			if err != nil {
				return err
			}

			var cl *project.Cluster
			for _, c := range proj.Clusters {
				if c.Name() == clusterName {
					cl = c
					break
				}
			}
			if cl == nil {
				return fmt.Errorf("cluster %q not found in project", clusterName)
			}

			out := outputDir
			if out == "" {
				out = filepath.Join(proj.RootDir, clusterName, databaseName)
			}

			return runDump(cl, databaseName, out, introspector, store, secretResolver)
		},
	}

	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster name (required)")
	cmd.Flags().StringVar(&databaseName, "database", "", "database name (required)")
	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory (default: cluster/database/ within project root)")

	return cmd
}

func runDump(
	cl *project.Cluster,
	dbName string,
	outDir string,
	introspector pipeline.Introspector,
	store pipeline.SnapshotStore,
	secretResolver pipeline.SecretResolver,
) error {
	ctx := context.Background()

	primary := cl.PrimaryNode()
	if primary == nil {
		return fmt.Errorf("cluster %q has no primary node configured", cl.Name())
	}
	connStr := primary.URL
	if primary.Link != "" {
		var err error
		connStr, err = secretResolver.Resolve(primary.Link)
		if err != nil {
			return fmt.Errorf("resolve connection secret: %w", err)
		}
	}

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	objects, err := introspector.Introspect(ctx, conn)
	if err != nil {
		return fmt.Errorf("introspect: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// Write one .dpg file per schema.
	schemaFiles := map[string]*strings.Builder{}
	for _, obj := range objects {
		schema := objectSchema(obj)
		if schema == "" {
			continue
		}
		if _, ok := schemaFiles[schema]; !ok {
			schemaFiles[schema] = &strings.Builder{}
		}
		renderObjectDPG(schemaFiles[schema], obj)
	}

	for schema, content := range schemaFiles {
		schemaDir := filepath.Join(outDir, "schemas", schema)
		if err := os.MkdirAll(schemaDir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(schemaDir, "schema.dpg")
		if err := os.WriteFile(path, []byte(content.String()), 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", path)
	}

	// Write snapshot.
	snap := &pipeline.Snapshot{}
	if err := snapshot.Populate(snap, objects); err != nil {
		return fmt.Errorf("build snapshot: %w", err)
	}
	if err := store.Save(cl.Name(), dbName, snap); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	fmt.Printf("snapshot written for %s/%s\n", cl.Name(), dbName)
	return nil
}

// objectSchema returns the schema name for schema-scoped objects.
func objectSchema(obj pipeline.IRObject) string {
	switch o := obj.(type) {
	case *ir.Table:
		return o.Schema
	case *ir.View:
		return o.Schema
	case *ir.Function:
		return o.Schema
	case *ir.Type:
		return o.Schema
	case *ir.Sequence:
		return o.Schema
	}
	return ""
}

// renderObjectDPG writes a minimal DPG declaration for obj into b.
func renderObjectDPG(b *strings.Builder, obj pipeline.IRObject) {
	switch o := obj.(type) {
	case *ir.Table:
		fmt.Fprintf(b, "\nTABLE %s (\n", o.Name)
		for i, col := range o.Columns {
			sep := ","
			if i == len(o.Columns)-1 && len(o.Constraints) == 0 {
				sep = ""
			}
			fmt.Fprintf(b, "    %s %s", col.Name, col.Type.String())
			if col.NotNull {
				fmt.Fprintf(b, " NOT NULL")
			}
			if col.Default != nil {
				fmt.Fprintf(b, " DEFAULT %s", *col.Default)
			}
			fmt.Fprintf(b, "%s\n", sep)
		}
		for i, cst := range o.Constraints {
			sep := ","
			if i == len(o.Constraints)-1 {
				sep = ""
			}
			if cst.Name != "" {
				fmt.Fprintf(b, "    CONSTRAINT %s %s%s\n", cst.Name, cst.Expr, sep)
			} else {
				fmt.Fprintf(b, "    %s%s\n", cst.Expr, sep)
			}
		}
		b.WriteString(")")
		if o.Comment != nil || o.RLSEnabled || len(o.Indexes) > 0 {
			b.WriteString(" {\n")
			if o.Comment != nil {
				fmt.Fprintf(b, "    COMMENT %q;\n", *o.Comment)
			}
			if o.RLSEnabled {
				b.WriteString("    ENABLE ROW LEVEL SECURITY;\n")
			}
			b.WriteString("}")
		}
		b.WriteString(";\n")

	case *ir.View:
		fmt.Fprintf(b, "\nVIEW %s AS %s;\n", o.Name, o.Query)

	case *ir.Function:
		fmt.Fprintf(b, "\n-- function %s (body omitted; use source files for full definition)\n", o.QualifiedName())

	case *ir.Type:
		switch o.Variant {
		case "ENUM":
			fmt.Fprintf(b, "\nENUM %s (", o.Name)
			for i, v := range o.EnumValues {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(b, "'%s'", v)
			}
			b.WriteString(");\n")
		default:
			fmt.Fprintf(b, "\n-- type %s (%s) omitted\n", o.Name, o.Variant)
		}

	case *ir.Sequence:
		fmt.Fprintf(b, "\nSEQUENCE %s;\n", o.Name)
	}
}
