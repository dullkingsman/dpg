package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/format"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/project"
	"github.com/dullkingsman/dpg/internal/snapshot"
	"github.com/dullkingsman/dpg/internal/ui"
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
			proj, err := discoverProject()
			if err != nil {
				return err
			}
			loadEnv(proj, envFile)

			clusters, err := resolveClusters(proj, clusterName)
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

			for _, cl := range clusters {
				databases, err := resolveDatabases(cl, databaseName)
				if err != nil {
					return err
				}
				fmtOpts := format.Options{
					IndentSize:  proj.RootConfig.Fmt.IndentSize,
					KeywordCase: proj.RootConfig.Fmt.KeywordCase,
				}
				if fmtOpts.IndentSize <= 0 {
					fmtOpts.IndentSize = 4
				}
				if fmtOpts.KeywordCase == "" {
					fmtOpts.KeywordCase = "upper"
				}

				for _, db := range databases {
					out := outputDir
					if out == "" {
						out = filepath.Join(proj.RootDir, cl.Name(), db.Name())
					}
					if err := runDump(cl, db, out, introspector, store, secretResolver, fmtOpts); err != nil {
						return fmt.Errorf("%s/%s: %w", cl.Name(), db.Name(), err)
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&clusterName, "cluster", "", "cluster to dump (required when multiple clusters exist)")
	cmd.Flags().StringVar(&databaseName, "database", "", "database to dump (required when multiple databases exist)")
	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "output directory (default: cluster/database/ within project root)")

	return cmd
}

func runDump(
	cl *project.Cluster,
	db *project.Database,
	outDir string,
	introspector pipeline.Introspector,
	store pipeline.SnapshotStore,
	secretResolver pipeline.SecretResolver,
	fmtOpts format.Options,
) error {
	ctx := context.Background()
	color := ui.IsColorEnabled(os.Stdout)

	connStr := cl.ConnectionString()
	if connStr == "" {
		return fmt.Errorf("cluster %q has no connection configured (set url or link in cluster dpg.toml)", cl.Name())
	}
	if cl.IsLink() {
		var err error
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

	objects, err := introspector.Introspect(ctx, conn)
	if err != nil {
		return ui.WrapDB(fmt.Errorf("introspect: %w", err))
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// Separate schema-scoped (DB-level) from cluster-level objects (roles).
	schemaFiles := map[string]*strings.Builder{}
	var clusterFile strings.Builder
	var dbObjects, clusterObjects []pipeline.IRObject
	for _, obj := range objects {
		schema := objectSchema(obj)
		if schema == "" {
			renderObjectDPG(&clusterFile, obj, fmtOpts)
			clusterObjects = append(clusterObjects, obj)
			continue
		}
		if _, ok := schemaFiles[schema]; !ok {
			schemaFiles[schema] = &strings.Builder{}
		}
		renderObjectDPG(schemaFiles[schema], obj, fmtOpts)
		dbObjects = append(dbObjects, obj)
	}

	// Write DB-level schema files.
	var dpgFiles []string
	for schema, content := range schemaFiles {
		schemaDir := filepath.Join(outDir, "schemas", schema)
		if err := os.MkdirAll(schemaDir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(schemaDir, "schema.dpg")
		if err := os.WriteFile(path, []byte(content.String()), 0o644); err != nil {
			return err
		}
		dpgFiles = append(dpgFiles, path)
		ui.PrintInfo(os.Stdout, "wrote", path, color)
	}

	// Write cluster-level roles file to the cluster objects directory.
	var clusterDPGFiles []string
	if clusterFile.Len() > 0 {
		if err := os.MkdirAll(cl.ObjectsDir, 0o755); err != nil {
			return fmt.Errorf("create cluster objects directory: %w", err)
		}
		path := filepath.Join(cl.ObjectsDir, "roles.dpg")
		if err := os.WriteFile(path, []byte(clusterFile.String()), 0o644); err != nil {
			return err
		}
		clusterDPGFiles = append(clusterDPGFiles, path)
		ui.PrintInfo(os.Stdout, "wrote", path, color)
	}

	// Build DB snapshot from compiled source (ensures plan produces no diff).
	dbSnapObjects := dbObjects
	if len(dpgFiles) > 0 {
		if compiled, compileErr := compiler.Compile(dpgFiles, outDir, pipeline.Default); compileErr == nil {
			dbSnapObjects = compiled
		}
	}
	dbSnap := &pipeline.Snapshot{}
	if err := snapshot.Populate(dbSnap, dbSnapObjects); err != nil {
		return fmt.Errorf("build snapshot: %w", err)
	}
	if err := store.Save(cl.Name(), db.Name(), dbSnap); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	ui.PrintSuccess(os.Stdout, "DB snapshot written", cl.Name()+"/"+db.Name(), color)

	// Build cluster snapshot (roles). Written once per cluster; safe to repeat.
	if len(clusterObjects) > 0 {
		clusterSnapObjects := clusterObjects
		if len(clusterDPGFiles) > 0 {
			if compiled, compileErr := compiler.Compile(clusterDPGFiles, cl.ObjectsDir, pipeline.Default); compileErr == nil {
				clusterSnapObjects = compiled
			}
		}
		clusterSnap := &pipeline.Snapshot{}
		if err := snapshot.Populate(clusterSnap, clusterSnapObjects); err != nil {
			return fmt.Errorf("build cluster snapshot: %w", err)
		}
		if err := store.Save(cl.Name(), cl.ClusterSnapshotKey(), clusterSnap); err != nil {
			return fmt.Errorf("save cluster snapshot: %w", err)
		}
		ui.PrintSuccess(os.Stdout, "Cluster snapshot written", cl.Name(), color)
	}
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
	case *ir.Procedure:
		return o.Schema
	case *ir.Aggregate:
		return o.Schema
	case *ir.Type:
		return o.Schema
	case *ir.Sequence:
		return o.Schema
	}
	return ""
}

// renderObjectDPG writes a minimal DPG declaration for obj into b using fmtOpts
// for keyword case and indentation.
func renderObjectDPG(b *strings.Builder, obj pipeline.IRObject, fmtOpts format.Options) {
	ind := fmtOpts.Indent()
	kw := fmtOpts.Keyword

	switch o := obj.(type) {
	case *ir.Table:
		inlinedByCol := map[string][]string{}
		var refCSTs []*ir.Constraint
		var otherCSTs []*ir.Constraint
		for _, cst := range o.Constraints {
			if len(cst.Columns) == 1 && isInlineable(cst.Type) {
				inlinedByCol[cst.Columns[0]] = append(inlinedByCol[cst.Columns[0]], inlineConstraintClause(cst))
			} else if cst.Type == "FOREIGN KEY" {
				refCSTs = append(refCSTs, cst)
			} else {
				otherCSTs = append(otherCSTs, cst)
			}
		}

		type tableItem struct {
			section string
			text    string
		}
		var items []tableItem

		renderColText := func(col *ir.Column) string {
			var sb strings.Builder
			fmt.Fprintf(&sb, "%s%s %s", ind, col.Name, col.Type.String())
			if col.NotNull && col.Identity == nil {
				fmt.Fprintf(&sb, " %s %s", kw("NOT"), kw("NULL"))
			}
			if col.Default != nil {
				fmt.Fprintf(&sb, " %s %s", kw("DEFAULT"), *col.Default)
			}
			if col.Identity != nil {
				if col.Identity.Always {
					fmt.Fprintf(&sb, " %s %s %s %s", kw("GENERATED"), kw("ALWAYS"), kw("AS"), kw("IDENTITY"))
				} else {
					fmt.Fprintf(&sb, " %s %s %s %s %s", kw("GENERATED"), kw("BY"), kw("DEFAULT"), kw("AS"), kw("IDENTITY"))
				}
			}
			if col.Generated != nil {
				fmt.Fprintf(&sb, " %s %s %s (%s) %s", kw("GENERATED"), kw("ALWAYS"), kw("AS"), col.Generated.Expr, kw("STORED"))
			}
			for _, clause := range inlinedByCol[col.Name] {
				fmt.Fprintf(&sb, " %s", clause)
			}
			return sb.String()
		}
		for _, col := range o.Columns {
			items = append(items, tableItem{section: classifyColumn(col), text: renderColText(col)})
		}
		renderCSTText := func(cst *ir.Constraint) string {
			if cst.Name != "" {
				return fmt.Sprintf("%s%s %s %s", ind, kw("CONSTRAINT"), cst.Name, cst.Expr)
			}
			return ind + cst.Expr
		}
		for _, cst := range refCSTs {
			items = append(items, tableItem{section: "references", text: renderCSTText(cst)})
		}
		for _, cst := range otherCSTs {
			items = append(items, tableItem{section: "constraints", text: renderCSTText(cst)})
		}

		sectionOrder := map[string]int{"": 0, "lifecycle": 1, "timestamps": 2, "references": 3, "constraints": 4}
		sort.SliceStable(items, func(i, j int) bool {
			return sectionOrder[items[i].section] < sectionOrder[items[j].section]
		})

		fmt.Fprintf(b, "\n%s %s (\n", kw("TABLE"), o.Name)
		hasContent := false
		prevSection := "__none__"
		for i, item := range items {
			sep := ","
			if i == len(items)-1 {
				sep = ""
			}
			if item.section != prevSection {
				if item.section != "" {
					if hasContent {
						b.WriteString("\n")
					}
					fmt.Fprintf(b, "%s-- %s\n", ind, item.section)
				}
				prevSection = item.section
			}
			fmt.Fprintf(b, "%s%s\n", item.text, sep)
			hasContent = true
		}
		b.WriteString(")")
		if o.Comment != nil || o.RLSEnabled || len(o.Indexes) > 0 {
			b.WriteString(" {\n")
			blockHasContent := false
			if o.Comment != nil {
				fmt.Fprintf(b, "%s%s %q;\n", ind, kw("COMMENT"), *o.Comment)
				blockHasContent = true
			}
			if o.RLSEnabled {
				fmt.Fprintf(b, "%s%s %s %s %s;\n", ind, kw("ENABLE"), kw("ROW"), kw("LEVEL"), kw("SECURITY"))
				blockHasContent = true
			}
			if len(o.Indexes) > 0 {
				if blockHasContent {
					b.WriteString("\n")
				}
				fmt.Fprintf(b, "%s-- indices\n", ind)
				for _, idx := range o.Indexes {
					renderIndex(b, idx, fmtOpts)
				}
			}
			b.WriteString("}")
		}
		b.WriteString(";\n")

	case *ir.View:
		fmt.Fprintf(b, "\n%s %s %s %s;\n", kw("VIEW"), o.Name, kw("AS"), o.Query)

	case *ir.Function:
		fmt.Fprintf(b, "\n-- function %s (body omitted; use source files for full definition)\n", o.QualifiedName())

	case *ir.Type:
		switch o.Variant {
		case "ENUM":
			fmt.Fprintf(b, "\n%s %s (", kw("ENUM"), o.Name)
			for i, v := range o.EnumValues {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(b, "'%s'", v)
			}
			b.WriteString(");\n")
		case "DOMAIN":
			fmt.Fprintf(b, "\n%s %s %s %s;\n", kw("DOMAIN"), o.Name, kw("AS"), o.Body)
		default:
			fmt.Fprintf(b, "\n-- type %s (%s) omitted\n", o.Name, o.Variant)
		}

	case *ir.Sequence:
		fmt.Fprintf(b, "\n%s %s;\n", kw("SEQUENCE"), o.Name)

	case *ir.Role:
		fmt.Fprintf(b, "\n%s %s;\n", kw("ROLE"), o.Name)
	}
}

// classifyColumn returns the presentation section for a column.
// Priority: generated > lifecycle > timestamps > "" (regular).
func classifyColumn(col *ir.Column) string {
	name := strings.ToLower(col.Name)
	for _, kw := range []string{"delet", "archiv", "activ", "enabl", "disabl", "publish", "expir", "suspend"} {
		if strings.Contains(name, kw) {
			return "lifecycle"
		}
	}
	if strings.HasSuffix(name, "_at") || strings.HasSuffix(name, "_on") {
		for _, p := range []string{"creat", "updat", "modif", "insert"} {
			if strings.HasPrefix(name, p) {
				return "timestamps"
			}
		}
	}
	return ""
}

// isInlineable reports whether a constraint type can be written as a column-level clause.
func isInlineable(typ string) bool {
	switch typ {
	case "PRIMARY KEY", "UNIQUE", "FOREIGN KEY":
		return true
	}
	return false
}

// inlineConstraintClause returns the bare inline column-level clause for a
// single-column constraint: "PRIMARY KEY", "UNIQUE", or "REFERENCES t(c) ...".
// Constraint names are intentionally omitted; PostgreSQL auto-generates them.
func inlineConstraintClause(cst *ir.Constraint) string {
	switch cst.Type {
	case "PRIMARY KEY":
		return "PRIMARY KEY"
	case "UNIQUE":
		return "UNIQUE"
	case "FOREIGN KEY":
		// pg_get_constraintdef: "FOREIGN KEY (col) REFERENCES tbl(col) [actions]"
		// Strip the "FOREIGN KEY (col) " prefix, leaving "REFERENCES ...".
		upper := strings.ToUpper(cst.Expr)
		if idx := strings.Index(upper, " REFERENCES "); idx >= 0 {
			return strings.TrimSpace(cst.Expr[idx+1:])
		}
		return cst.Expr
	}
	return cst.Expr
}

// renderIndex writes one INDEX entry for a table's {} block.
// Format: INDEX name [UNIQUE] (cols) [USING method] [WHERE pred];
func renderIndex(b *strings.Builder, idx *ir.Index, fmtOpts format.Options) {
	ind := fmtOpts.Indent()
	kw := fmtOpts.Keyword
	fmt.Fprintf(b, "%s%s %s", ind, kw("INDEX"), idx.Name)
	if idx.Unique {
		fmt.Fprintf(b, " %s", kw("UNIQUE"))
	}
	b.WriteString(" (")
	for i, col := range idx.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		if col.Expr != nil {
			b.WriteString(col.Expr.Text)
		} else {
			b.WriteString(col.Name)
		}
		if col.SortOrder != "" {
			b.WriteString(" ")
			b.WriteString(col.SortOrder)
		}
		if col.Nulls != "" {
			fmt.Fprintf(b, " %s %s", kw("NULLS"), col.Nulls)
		}
	}
	b.WriteString(")")
	if idx.Method != "" && idx.Method != "btree" {
		fmt.Fprintf(b, " %s %s", kw("USING"), idx.Method)
	}
	if idx.Where != nil {
		fmt.Fprintf(b, " %s %s", kw("WHERE"), *idx.Where)
	}
	b.WriteString(";\n")
}
