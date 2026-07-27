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

	// Route objects into three buckets: cluster-scoped (roles, tablespaces —
	// shared across databases), database-scoped but schemaless (FDWs, servers,
	// user mappings, publications, event triggers, casts), and schema-scoped
	// (everything owned by a schema).
	schemaFiles := map[string]*strings.Builder{}
	var clusterFile, dbLevelFile strings.Builder
	var dbObjects, clusterObjects []pipeline.IRObject
	for _, obj := range objects {
		if isClusterScoped(obj) {
			renderObjectDPG(&clusterFile, obj, fmtOpts)
			clusterObjects = append(clusterObjects, obj)
			continue
		}
		schema := objectSchema(obj)
		if schema == "" {
			renderObjectDPG(&dbLevelFile, obj, fmtOpts)
			dbObjects = append(dbObjects, obj)
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

	// Write database-scoped schemaless objects to a database-level file.
	if dbLevelFile.Len() > 0 {
		path := filepath.Join(outDir, "objects.dpg")
		if err := os.WriteFile(path, []byte(dbLevelFile.String()), 0o644); err != nil {
			return err
		}
		dpgFiles = append(dpgFiles, path)
		ui.PrintInfo(os.Stdout, "wrote", path, color)
	}

	// Write cluster-level objects (roles, tablespaces) to the cluster objects dir.
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
	// If the freshly written .dpg files fail to recompile, fall back to the raw
	// introspected objects but surface the error — a silent fallback would hide a
	// dump that emits non-compiling source (the snapshot would then diverge from
	// what `plan` reads back).
	dbSnapObjects := dbObjects
	if len(dpgFiles) > 0 {
		if compiled, compileErr := compiler.Compile(dpgFiles, outDir, pipeline.Default); compileErr == nil {
			dbSnapObjects = compiled
		} else {
			fmt.Fprintf(os.Stderr, "%s  dumped source did not recompile for %s/%s; snapshot built from live catalog instead: %v\n",
				ui.Yellow("warning", color), cl.Name(), db.Name(), compileErr)
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

	// Build cluster snapshot (roles, tablespaces). These are cluster-global, so
	// every database dumps identical content — written once per cluster, safe to repeat.
	if len(clusterObjects) > 0 {
		clusterSnapObjects := clusterObjects
		if len(clusterDPGFiles) > 0 {
			if compiled, compileErr := compiler.Compile(clusterDPGFiles, cl.ObjectsDir, pipeline.Default); compileErr == nil {
				clusterSnapObjects = compiled
			} else {
				fmt.Fprintf(os.Stderr, "%s  dumped cluster source did not recompile for %s; snapshot built from live catalog instead: %v\n",
					ui.Yellow("warning", color), cl.Name(), compileErr)
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

// isClusterScoped reports whether obj lives at the cluster level (shared across
// every database) rather than inside one database. Only roles and tablespaces
// qualify; all other schemaless objects (FDWs, servers, user mappings,
// publications, event triggers, casts) are database-scoped.
func isClusterScoped(obj pipeline.IRObject) bool {
	switch obj.(type) {
	case *ir.Role, *ir.Tablespace:
		return true
	}
	return false
}

// excludeClusterScoped returns the objects that belong to a database (i.e. not
// cluster-scoped), preserving order. Used to build database-level baselines for
// verify and plan --live so roles/tablespaces are handled only at cluster level.
func excludeClusterScoped(objs []pipeline.IRObject) []pipeline.IRObject {
	out := make([]pipeline.IRObject, 0, len(objs))
	for _, obj := range objs {
		if !isClusterScoped(obj) {
			out = append(out, obj)
		}
	}
	return out
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
	case *ir.Collation:
		return o.Schema
	case *ir.StatisticsObject:
		return o.Schema
	case *ir.Operator:
		return o.Schema
	case *ir.OperatorClass:
		return o.Schema
	case *ir.OperatorFamily:
		return o.Schema
	case *ir.TSConfig:
		return o.Schema
	case *ir.TSDict:
		return o.Schema
	case *ir.TSParser:
		return o.Schema
	case *ir.TSTemplate:
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
			fmt.Fprintf(&sb, "%s%s %s", ind, quoteIdentIfNeeded(col.Name), col.Type.String())
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
				return fmt.Sprintf("%s%s %s %s", ind, kw("CONSTRAINT"), quoteIdentIfNeeded(cst.Name), cst.Expr)
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

		fmt.Fprintf(b, "\n%s %s (\n", kw("TABLE"), quoteIdentIfNeeded(o.Name))
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
		var colsWithAttrs []*ir.Column
		for _, col := range o.Columns {
			hasStorage := col.Storage != nil && !col.StorageIsTypeDefault
			if col.Comment != nil || hasStorage || col.Compression != nil || col.Statistics != nil {
				colsWithAttrs = append(colsWithAttrs, col)
			}
		}
		if o.Owner != nil || o.Comment != nil || o.RLSEnabled || len(o.Indexes) > 0 || len(colsWithAttrs) > 0 {
			b.WriteString(" {\n")
			blockHasContent := false
			if o.Owner != nil {
				fmt.Fprintf(b, "%s%s %s;\n", ind, kw("OWNER"), quoteIdentIfNeeded(*o.Owner))
				blockHasContent = true
			}
			if o.Comment != nil {
				fmt.Fprintf(b, "%s%s %s;\n", ind, kw("COMMENT"), sqlStringLit(*o.Comment))
				blockHasContent = true
			}
			if o.RLSEnabled {
				fmt.Fprintf(b, "%s%s %s %s %s;\n", ind, kw("ENABLE"), kw("ROW"), kw("LEVEL"), kw("SECURITY"))
				blockHasContent = true
			}
			if len(colsWithAttrs) > 0 {
				if blockHasContent {
					b.WriteString("\n")
				}
				// Mode A (COLUMNS { … }) — each entry omits the COLUMN verb:
				// "name { COMMENT/STORAGE/COMPRESSION/STATISTICS … }". These
				// attributes are genuinely diffed (diffColumns) but were
				// previously never rendered by dump, so a dumped project
				// silently never detected drift on any of them. STORAGE is
				// suppressed when it's just the column's type's own default
				// (col.StorageIsTypeDefault, set by introspection from
				// pg_type.typstorage) — unlike the others, every real column
				// has a concrete storage mode, so rendering it unconditionally
				// would add a STORAGE line to nearly every variable-length
				// column in every dumped table for no reason.
				fmt.Fprintf(b, "%s%s {\n", ind, kw("COLUMNS"))
				colInd := ind + ind
				for _, col := range colsWithAttrs {
					fmt.Fprintf(b, "%s%s {\n", ind, quoteIdentIfNeeded(col.Name))
					if col.Comment != nil {
						fmt.Fprintf(b, "%s%s %s;\n", colInd, kw("COMMENT"), sqlStringLit(*col.Comment))
					}
					if col.Storage != nil && !col.StorageIsTypeDefault {
						fmt.Fprintf(b, "%s%s %s;\n", colInd, kw("STORAGE"), quoteIdentIfNeeded(*col.Storage))
					}
					if col.Compression != nil {
						fmt.Fprintf(b, "%s%s %s;\n", colInd, kw("COMPRESSION"), quoteIdentIfNeeded(*col.Compression))
					}
					if col.Statistics != nil {
						fmt.Fprintf(b, "%s%s %d;\n", colInd, kw("STATISTICS"), *col.Statistics)
					}
					fmt.Fprintf(b, "%s}\n", ind)
				}
				fmt.Fprintf(b, "%s}\n", ind)
				blockHasContent = true
			}
			if len(o.Indexes) > 0 {
				if blockHasContent {
					b.WriteString("\n")
				}
				// Mode A (INDICES { … }) — the form the scanner accepts. Each entry
				// omits the INDEX verb: "name [UNIQUE] (cols) [USING m] [WHERE …];".
				fmt.Fprintf(b, "%s%s {\n", ind, kw("INDICES"))
				for _, idx := range o.Indexes {
					renderIndex(b, idx, fmtOpts)
				}
				fmt.Fprintf(b, "%s}\n", ind)
			}
			b.WriteString("}")
		}
		b.WriteString(";\n")

	case *ir.View:
		fmt.Fprintf(b, "\n%s %s %s %s;\n", kw("VIEW"), quoteIdentIfNeeded(o.Name), kw("AS"), o.Query)

	case *ir.Function:
		b.WriteString("\n")
		b.WriteString(kw("FUNCTION"))
		b.WriteString(" ")
		b.WriteString(quoteIdentIfNeeded(o.Name))
		b.WriteString("(")
		writeFuncArgs(b, o.Args)
		b.WriteString(") ")
		b.WriteString(kw("RETURNS"))
		b.WriteString(" ")
		b.WriteString(o.ReturnType.String())
		b.WriteString(" ")
		b.WriteString(kw("LANGUAGE"))
		b.WriteString(" ")
		b.WriteString(o.Attrs.Language)
		if o.Attrs.Volatility != "" && o.Attrs.Volatility != "VOLATILE" {
			b.WriteString(" ")
			b.WriteString(kw(o.Attrs.Volatility))
		}
		if o.Attrs.Strict {
			fmt.Fprintf(b, " %s", kw("STRICT"))
		}
		if o.Attrs.SecurityDef {
			fmt.Fprintf(b, " %s %s", kw("SECURITY"), kw("DEFINER"))
		}
		if o.Attrs.Parallel != "" && o.Attrs.Parallel != "UNSAFE" {
			fmt.Fprintf(b, " %s %s", kw("PARALLEL"), kw(o.Attrs.Parallel))
		}
		if o.Attrs.Cost != nil {
			fmt.Fprintf(b, " %s %v", kw("COST"), *o.Attrs.Cost)
		}
		if o.Attrs.Rows != nil {
			fmt.Fprintf(b, " %s %v", kw("ROWS"), *o.Attrs.Rows)
		}
		fmt.Fprintf(b, " %s $$%s$$", kw("AS"), o.Attrs.Body)
		writeFuncBlock(b, ind, fmtOpts, o.Comment, o.Grants)

	case *ir.Procedure:
		b.WriteString("\n")
		b.WriteString(kw("PROCEDURE"))
		b.WriteString(" ")
		b.WriteString(quoteIdentIfNeeded(o.Name))
		b.WriteString("(")
		writeFuncArgs(b, o.Args)
		b.WriteString(") ")
		b.WriteString(kw("LANGUAGE"))
		b.WriteString(" ")
		b.WriteString(o.Attrs.Language)
		fmt.Fprintf(b, " %s $$%s$$", kw("AS"), o.Attrs.Body)
		writeFuncBlock(b, ind, fmtOpts, o.Comment, o.Grants)

	case *ir.Type:
		switch o.Variant {
		case "ENUM":
			// DPG enum form: ENUM <name> AS ENUM ('a', 'b', …);
			fmt.Fprintf(b, "\n%s %s %s %s (", kw("ENUM"), quoteIdentIfNeeded(o.Name), kw("AS"), kw("ENUM"))
			for i, v := range o.EnumValues {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(sqlStringLit(v))
			}
			b.WriteString(");\n")
		case "DOMAIN":
			fmt.Fprintf(b, "\n%s %s %s %s;\n", kw("DOMAIN"), quoteIdentIfNeeded(o.Name), kw("AS"), o.Body)
		default:
			fmt.Fprintf(b, "\n-- type %s (%s) omitted\n", o.Name, o.Variant)
		}

	case *ir.Sequence:
		b.WriteString("\n")
		b.WriteString(kw("SEQUENCE"))
		b.WriteString(" ")
		b.WriteString(quoteIdentIfNeeded(o.Name))
		if o.IncrementBy != nil {
			fmt.Fprintf(b, " %s %d", kw("INCREMENT BY"), *o.IncrementBy)
		}
		if o.MinValue != nil {
			fmt.Fprintf(b, " %s %d", kw("MINVALUE"), *o.MinValue)
		}
		if o.MaxValue != nil {
			fmt.Fprintf(b, " %s %d", kw("MAXVALUE"), *o.MaxValue)
		}
		if o.StartValue != nil {
			fmt.Fprintf(b, " %s %d", kw("START WITH"), *o.StartValue)
		}
		if o.Cache != nil {
			fmt.Fprintf(b, " %s %d", kw("CACHE"), *o.Cache)
		}
		if o.Cycle != nil && *o.Cycle {
			b.WriteString(" ")
			b.WriteString(kw("CYCLE"))
		}
		if o.Owner != nil || o.Comment != nil {
			b.WriteString(" {\n")
			if o.Owner != nil {
				fmt.Fprintf(b, "%s%s %s;\n", ind, kw("OWNER"), quoteIdentIfNeeded(*o.Owner))
			}
			if o.Comment != nil {
				fmt.Fprintf(b, "%s%s %s;\n", ind, kw("COMMENT"), sqlStringLit(*o.Comment))
			}
			b.WriteString("}\n")
		} else {
			b.WriteString(";\n")
		}

	case *ir.Role:
		fmt.Fprintf(b, "\n%s %s;\n", kw("ROLE"), quoteIdentIfNeeded(o.Name))

	case *ir.Schema:
		// Owner/comment are rendered when present so plan --live doesn't
		// perpetually miss owner drift or emit a spurious COMMENT ... IS NULL.
		name := quoteIdentIfNeeded(o.Name)
		fmt.Fprintf(b, "\n%s %s {\n", kw("SCHEMA"), name)
		if o.Owner != nil {
			fmt.Fprintf(b, "%s%s %s;\n", ind, kw("OWNER"), quoteIdentIfNeeded(*o.Owner))
		}
		if o.Comment != nil {
			fmt.Fprintf(b, "%s%s %s;\n", ind, kw("COMMENT"), sqlStringLit(*o.Comment))
		}
		b.WriteString("}\n")

	case *ir.Extension:
		// Bare form (no VERSION) so plan --live never pins/updates a version:
		// diffExtension only acts when the desired side declares a version.
		fmt.Fprintf(b, "\n%s %s;\n", kw("EXTENSION"), quoteIdentIfNeeded(o.Name))

	// Reliable-tier opaque objects carry a canonicalised CREATE statement in
	// Body; renderOpaqueBody strips the CREATE verb to satisfy the no-verb mandate.
	case *ir.Collation:
		renderOpaqueBody(b, o.Body)
	case *ir.StatisticsObject:
		renderOpaqueBody(b, o.Body)
	case *ir.Cast:
		renderOpaqueBody(b, o.Body)
	case *ir.EventTrigger:
		renderOpaqueBody(b, o.Body)
	case *ir.ForeignDataWrapper:
		renderOpaqueBody(b, o.Body)
	case *ir.ForeignServer:
		renderOpaqueBody(b, o.Body)
	case *ir.UserMapping:
		renderOpaqueBody(b, o.Body)
	case *ir.Publication:
		renderOpaqueBody(b, o.Body)
	case *ir.Tablespace:
		renderOpaqueBody(b, o.Body)
	case *ir.Operator:
		renderOpaqueBody(b, o.Body)
	case *ir.OperatorClass:
		renderOpaqueBody(b, o.Body)
	case *ir.OperatorFamily:
		renderOpaqueBody(b, o.Body)
	case *ir.TSConfig:
		renderOpaqueBody(b, o.Body)
	case *ir.TSDict:
		renderOpaqueBody(b, o.Body)
	case *ir.TSParser:
		renderOpaqueBody(b, o.Body)
	case *ir.TSTemplate:
		renderOpaqueBody(b, o.Body)
	}
}

// writeFuncArgs renders a FUNCTION/PROCEDURE argument list's interior
// ("mode name type [DEFAULT expr]", comma-joined) — shared between the two,
// since their parameter syntax is identical.
func writeFuncArgs(b *strings.Builder, args []ir.FuncArg) {
	for i, a := range args {
		if i > 0 {
			b.WriteString(", ")
		}
		if a.Mode != "" && a.Mode != "IN" {
			b.WriteString(a.Mode)
			b.WriteString(" ")
		}
		if a.Name != "" {
			b.WriteString(a.Name)
			b.WriteString(" ")
		}
		b.WriteString(a.Type.String())
		if a.Default != nil {
			b.WriteString(" DEFAULT ")
			b.WriteString(*a.Default)
		}
	}
}

// writeFuncBlock terminates a FUNCTION/PROCEDURE declaration: a bare ";" when
// there's no COMMENT/GRANT to declare, or a "{ }" block carrying them when
// there is. Comment/Grants are genuinely compared by diffFunction/
// diffProcedure but were previously never rendered by dump at all (the
// object type had no case in this switch whatsoever for PROCEDURE, and
// FUNCTION rendered only a placeholder comment) — a dumped project could
// never detect drift on either, or even reconstruct a function's/procedure's
// body to begin with.
func writeFuncBlock(b *strings.Builder, ind string, fmtOpts format.Options, comment *string, grants []ir.Grant) {
	kw := fmtOpts.Keyword
	if comment == nil && len(grants) == 0 {
		b.WriteString(";\n")
		return
	}
	b.WriteString(" {\n")
	if comment != nil {
		fmt.Fprintf(b, "%s%s %s;\n", ind, kw("COMMENT"), sqlStringLit(*comment))
	}
	for _, g := range grants {
		priv := "ALL"
		if len(g.Privileges) > 0 {
			priv = strings.Join(g.Privileges, ", ")
		}
		fmt.Fprintf(b, "%s%s %s %s %s", ind, kw("GRANT"), priv, kw("TO"), strings.Join(g.Roles, ", "))
		if g.WithGrant {
			fmt.Fprintf(b, " %s %s %s", kw("WITH"), kw("GRANT"), kw("OPTION"))
		}
		b.WriteString(";\n")
	}
	b.WriteString("}\n")
}

// renderOpaqueBody writes a reconstructed CREATE statement (already
// canonicalised by the introspector) as a DPG declaration. DPG source obeys the
// no-verb mandate — declarations must not begin with CREATE — so the leading
// CREATE verb is stripped. Empty bodies are skipped so a body-less object never
// emits a bare, invalid ";".
func renderOpaqueBody(b *strings.Builder, body string) {
	body = strings.TrimSpace(body)
	const createPrefix = "CREATE "
	if len(body) >= len(createPrefix) && strings.EqualFold(body[:len(createPrefix)], createPrefix) {
		body = strings.TrimSpace(body[len(createPrefix):])
	}
	if body == "" {
		return
	}
	fmt.Fprintf(b, "\n%s;\n", body)
}

// sqlStringLit renders s as a single-quoted SQL string literal, doubling any
// embedded single quotes. DPG string values (COMMENT text, ENUM labels) use SQL
// single-quote literals, not Go %q double quotes.
func sqlStringLit(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// isSafeBareIdent reports whether s can appear as an unquoted PostgreSQL
// identifier: non-empty, starting with a lowercase letter or underscore, and
// containing only lowercase letters, digits, underscores, or dollar signs. Any
// uppercase forces quoting, since an unquoted identifier folds to lowercase.
func isSafeBareIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r == '_':
		case i > 0 && (r >= '0' && r <= '9' || r == '$'):
		default:
			return false
		}
	}
	return true
}

// quoteIdentIfNeeded double-quotes an identifier when it is not a safe bare
// identifier or is a PostgreSQL reserved / type-or-function-name keyword, so that
// dumped DPG source recompiles. Safe lowercase non-keyword names are left bare to
// keep the output idiomatic. Mirrors PostgreSQL's quote_identifier().
func quoteIdentIfNeeded(s string) string {
	if isSafeBareIdent(s) && !dumpReservedKeywords[s] {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// dumpReservedKeywords is the set of PostgreSQL RESERVED and TYPE_FUNC_NAME
// keywords — the categories that cannot be a bare identifier in the positions
// dump emits (table/column/schema/role/etc. names). Unreserved and col-name
// keywords are valid bare and are omitted. Stable across PG 14–17; a missing
// future keyword degrades only to the pre-existing unquoted behaviour.
var dumpReservedKeywords = func() map[string]bool {
	const words = `all analyse analyze and any array as asc asymmetric both case cast check
collate column constraint create current_catalog current_date current_role
current_time current_timestamp current_user default deferrable desc distinct do
else end except false fetch for foreign from grant group having in initially
intersect into lateral leading limit localtime localtimestamp not null offset on
only or order placing primary references returning select session_user some
symmetric system_user table then to trailing true union unique user using
variadic when where window with
authorization binary collation concurrently cross current_schema freeze full
ilike inner is isnull join left like natural notnull outer overlaps right similar
tablesample verbose`
	m := make(map[string]bool)
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}()

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

// renderIndex writes one entry inside a table's INDICES { } block.
// Format: name [UNIQUE] (cols) [USING method] [WHERE pred]; (two-level indent,
// no INDEX verb — that is the grammar parseOneIndex accepts).
func renderIndex(b *strings.Builder, idx *ir.Index, fmtOpts format.Options) {
	ind := fmtOpts.Indent()
	kw := fmtOpts.Keyword
	fmt.Fprintf(b, "%s%s%s", ind, ind, quoteIdentIfNeeded(idx.Name))
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
			// Index column names are emitted bare: the blockparser stores the
			// column text verbatim and the differ's createIndex quotes it when
			// generating SQL, so quoting here would double-quote it.
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
	if len(idx.Include) > 0 {
		// Bare column names; createIndex quotes them when generating SQL.
		fmt.Fprintf(b, " %s (%s)", kw("INCLUDE"), strings.Join(idx.Include, ", "))
	}
	if idx.NullsNotDistinct {
		fmt.Fprintf(b, " %s %s %s", kw("NULLS"), kw("NOT"), kw("DISTINCT"))
	}
	if len(idx.With) > 0 {
		parts := make([]string, len(idx.With))
		for i, p := range idx.With {
			parts[i] = p.Key + "=" + p.Value
		}
		fmt.Fprintf(b, " %s (%s)", kw("WITH"), strings.Join(parts, ", "))
	}
	if idx.Where != nil {
		fmt.Fprintf(b, " %s %s", kw("WHERE"), *idx.Where)
	}
	b.WriteString(";\n")
}
