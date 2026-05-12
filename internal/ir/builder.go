// Package ir implements pipeline.IRBuilder. The Builder converts a
// (pipeline.PGParseResult, pipeline.BlockAST) pair into a pipeline.IRObject.
package ir

import (
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"

	"github.com/dullkingsman/dpg/internal/ast"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	pipeline.Default.Register(pipeline.KeyIRBuilder, NewBuilder())
}

// Builder implements pipeline.IRBuilder.
type Builder struct{}

// NewBuilder returns a Builder.
func NewBuilder() *Builder { return &Builder{} }

// Build implements pipeline.IRBuilder. It dispatches on the ObjectKind embedded
// in pg.Pos (via the RawObject that produced pg) — but since PGParseResult does
// not carry the ObjectKind directly, we inspect the protobuf node type instead.
// For passthrough kinds (KindVirtualType), pg.Kind is set and pg.Raw is a string.
func (b *Builder) Build(pg pipeline.PGParseResult, block pipeline.BlockAST) (pipeline.IRObject, error) {
	if pg.Kind == pipeline.KindVirtualType {
		return b.buildVirtualType(pg.Raw.(string), block, pg.Pos, pg.SchemaContext)
	}

	node := ast.FirstStmt(pg)
	if node == nil {
		return nil, pipeline.Errorf(pg.Pos, "empty PG parse result")
	}
	pos := pg.Pos

	var obj pipeline.IRObject
	var err error
	switch n := node.Node.(type) {
	case *pg_query.Node_CreateStmt:
		obj, err = b.buildTable(n.CreateStmt, block, pos, false, false)
	case *pg_query.Node_CreateForeignTableStmt:
		obj, err = b.buildForeignTable(n.CreateForeignTableStmt, block, pos)
	case *pg_query.Node_ViewStmt:
		obj, err = b.buildView(n.ViewStmt, block, pos, false, false)
	case *pg_query.Node_CreateFunctionStmt:
		obj, err = b.buildFunction(n.CreateFunctionStmt, pg, block, pos)
	case *pg_query.Node_CreateEnumStmt:
		obj, err = b.buildEnum(n.CreateEnumStmt, block, pos)
	case *pg_query.Node_CreateSchemaStmt:
		obj, err = b.buildSchema(n.CreateSchemaStmt, block, pos)
	case *pg_query.Node_CreateExtensionStmt:
		obj, err = b.buildExtension(n.CreateExtensionStmt, block, pos)
	case *pg_query.Node_CreateSeqStmt:
		obj, err = b.buildSequence(n.CreateSeqStmt, block, pos)
	case *pg_query.Node_CreateRoleStmt:
		obj, err = b.buildRole(n.CreateRoleStmt, block, pos)
	case *pg_query.Node_CreateTableSpaceStmt:
		obj, err = b.buildTablespace(n.CreateTableSpaceStmt, block, pos, rawSQL(node))
	case *pg_query.Node_CreateFdwStmt:
		obj, err = b.buildFDW(n.CreateFdwStmt, block, pos, rawSQL(node))
	case *pg_query.Node_CreateForeignServerStmt:
		obj, err = b.buildServer(n.CreateForeignServerStmt, block, pos, rawSQL(node))
	case *pg_query.Node_CreateUserMappingStmt:
		obj, err = b.buildUserMapping(n.CreateUserMappingStmt, block, pos, rawSQL(node))
	case *pg_query.Node_CreatePublicationStmt:
		obj, err = b.buildOpaque(node, block, pos, "PUBLICATION")
	case *pg_query.Node_CreateSubscriptionStmt:
		obj, err = b.buildOpaque(node, block, pos, "SUBSCRIPTION")
	case *pg_query.Node_CreateEventTrigStmt:
		obj, err = b.buildOpaque(node, block, pos, "EVENT TRIGGER")
	case *pg_query.Node_DefineStmt:
		obj, err = b.buildDefineStmt(n.DefineStmt, block, pos, rawSQL(node))
	case *pg_query.Node_CreateDomainStmt:
		obj, err = b.buildDomain(n.CreateDomainStmt, block, pos, rawSQL(node))
	case *pg_query.Node_CreateOpClassStmt:
		obj, err = b.buildOpaque(node, block, pos, "OPERATOR CLASS")
	case *pg_query.Node_CreateOpFamilyStmt:
		obj, err = b.buildOpaque(node, block, pos, "OPERATOR FAMILY")
	case *pg_query.Node_CreateStatsStmt:
		obj, err = b.buildStatistics(n.CreateStatsStmt, block, pos)
	case *pg_query.Node_AlterDefaultPrivilegesStmt:
		obj, err = b.buildDefaultPrivileges(n.AlterDefaultPrivilegesStmt, block, pos)
	case *pg_query.Node_CreateOpClassItem:
		obj, err = b.buildOpaque(node, block, pos, "OPERATOR")
	case *pg_query.Node_CreateCastStmt:
		obj, err = b.buildCast(n.CreateCastStmt, block, pos, rawSQL(node))
	default:
		obj = &OpaqueObject{kind: "UNKNOWN", body: "", SrcPos: pos}
	}
	if err != nil {
		return nil, err
	}
	// Apply schema context from enclosing SCHEMA block or directory inference.
	// Fall back to "public" so desired IR always uses explicit schema names that
	// match what the introspector returns from pg_namespace.
	schemaCtx := pg.SchemaContext
	if schemaCtx == "" {
		schemaCtx = "public"
	}
	if obj != nil {
		applySchemaContext(obj, schemaCtx)
	}
	return obj, nil
}

// applySchemaContext sets the Schema field on schema-scoped IR objects when it
// is empty, using the enclosing SCHEMA { } block's name as the context.
func applySchemaContext(obj pipeline.IRObject, schema string) {
	switch o := obj.(type) {
	case *Table:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *View:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *Function:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *Procedure:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *Type:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *Sequence:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *Aggregate:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *Operator:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *Collation:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *TSConfig:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *TSDict:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *TSParser:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *TSTemplate:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *StatisticsObject:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *OperatorClass:
		if o.Schema == "" {
			o.Schema = schema
		}
	case *OperatorFamily:
		if o.Schema == "" {
			o.Schema = schema
		}
	}
}

// ── Table ─────────────────────────────────────────────────────────────────────

func (b *Builder) buildTable(cs *pg_query.CreateStmt, block pipeline.BlockAST, pos pipeline.SourcePos, unlogged, foreign bool) (pipeline.IRObject, error) {
	tbl := &Table{
		Schema:   rangeVarSchema(cs.Relation),
		Name:     cs.Relation.Relname,
		Unlogged: unlogged,
		SrcPos:   pos,
	}

	// Columns and table-level constraints from the pg_query parse.
	for _, elt := range cs.TableElts {
		switch e := elt.Node.(type) {
		case *pg_query.Node_ColumnDef:
			col, promoted, err := b.buildColumn(e.ColumnDef, pos)
			if err != nil {
				return nil, err
			}
			tbl.Columns = append(tbl.Columns, col)
			tbl.Constraints = append(tbl.Constraints, promoted...)
		case *pg_query.Node_Constraint:
			cst := buildConstraint(e.Constraint, pos)
			tbl.Constraints = append(tbl.Constraints, cst)
		}
	}

	// Inheritance
	for _, inh := range cs.InhRelations {
		if rv := inh.GetRangeVar(); rv != nil {
			tbl.Inherits = append(tbl.Inherits, qualName(rv.Schemaname, rv.Relname))
		}
	}

	// Partition strategy
	if cs.Partspec != nil {
		tbl.PartitionBy = buildPartitionSpec(cs.Partspec)
	}

	// Storage params (WITH clause)
	if len(cs.Options) > 0 {
		tbl.StorageParams = buildStorageParams(cs.Options)
	}

	// Tablespace
	if cs.Tablespacename != "" {
		ts := cs.Tablespacename
		tbl.Tablespace = &ts
	}

	// Merge in the BlockAST.
	if err := mergeTableBlock(tbl, block); err != nil {
		return nil, err
	}
	return tbl, nil
}

func (b *Builder) buildForeignTable(cs *pg_query.CreateForeignTableStmt, block pipeline.BlockAST, pos pipeline.SourcePos) (pipeline.IRObject, error) {
	tbl, err := b.buildTable(cs.BaseStmt, block, pos, false, true)
	if err != nil {
		return nil, err
	}
	t := tbl.(*Table)
	t.Foreign = true
	if cs.Servername != "" {
		t.ForeignServer = &cs.Servername
	}
	return t, nil
}

// buildColumn returns the Column and any table-level constraints promoted from
// inline column syntax (PRIMARY KEY, UNIQUE, REFERENCES).
func (b *Builder) buildColumn(cd *pg_query.ColumnDef, pos pipeline.SourcePos) (*Column, []*Constraint, error) {
	col := &Column{
		Name:   cd.Colname,
		SrcPos: pos,
	}
	if cd.TypeName != nil {
		col.Type = typeNameToRef(cd.TypeName)
	}

	var promoted []*Constraint

	for _, cn := range cd.Constraints {
		cst := cn.GetConstraint()
		if cst == nil {
			continue
		}
		switch cst.Contype {
		case pg_query.ConstrType_CONSTR_NOTNULL:
			col.NotNull = true

		case pg_query.ConstrType_CONSTR_DEFAULT:
			if cst.RawExpr != nil {
				raw := nodeToText(cst.RawExpr)
				col.Default = &raw
			}

		case pg_query.ConstrType_CONSTR_GENERATED:
			if cst.RawExpr != nil {
				expr := nodeToText(cst.RawExpr)
				col.Generated = &Generated{Expr: expr, Stored: true}
			}

		case pg_query.ConstrType_CONSTR_IDENTITY:
			col.Identity = &Identity{Always: cst.GeneratedWhen == "a"}
			col.NotNull = true // identity columns are always implicitly NOT NULL in PG

		case pg_query.ConstrType_CONSTR_PRIMARY:
			col.NotNull = true // PRIMARY KEY implies NOT NULL in PostgreSQL
			// Inline PRIMARY KEY — promote to a table-level constraint.
			tc := &Constraint{
				Name:              cst.Conname,
				Type:              "PRIMARY KEY",
				Columns:           []string{cd.Colname},
				Deferrable:        cst.Deferrable,
				InitiallyDeferred: cst.Initdeferred,
				Pos:               pos,
			}
			tc.Expr = "PRIMARY KEY (" + quoteIdent(cd.Colname) + ")"
			promoted = append(promoted, tc)

		case pg_query.ConstrType_CONSTR_UNIQUE:
			// Inline UNIQUE — promote to a table-level constraint.
			tc := &Constraint{
				Name:              cst.Conname,
				Type:              "UNIQUE",
				Columns:           []string{cd.Colname},
				Deferrable:        cst.Deferrable,
				InitiallyDeferred: cst.Initdeferred,
				Pos:               pos,
			}
			nd := ""
			if cst.NullsNotDistinct {
				nd = "NULLS NOT DISTINCT "
			}
			tc.Expr = "UNIQUE " + nd + "(" + quoteIdent(cd.Colname) + ")"
			promoted = append(promoted, tc)

		case pg_query.ConstrType_CONSTR_CHECK:
			// Inline CHECK — promote to a table-level constraint.
			// Columns is set to [colname] so createTable can inline it back.
			if cst.RawExpr != nil {
				expr := nodeToText(cst.RawExpr)
				tc := &Constraint{
					Name:    cst.Conname,
					Type:    "CHECK",
					Columns: []string{cd.Colname},
					Expr:    "CHECK (" + expr + ")",
					Pos:     pos,
				}
				promoted = append(promoted, tc)
			}

		case pg_query.ConstrType_CONSTR_FOREIGN:
			// Inline REFERENCES — promote to a table-level FK constraint.
			refCols := nodeListToNames(cst.PkAttrs)
			var fkBuf strings.Builder
			fkBuf.WriteString("FOREIGN KEY (")
			fkBuf.WriteString(quoteIdent(cd.Colname))
			fkBuf.WriteString(") REFERENCES ")
			if cst.Pktable != nil {
				if cst.Pktable.Schemaname != "" {
					fkBuf.WriteString(quoteIdent(cst.Pktable.Schemaname))
					fkBuf.WriteByte('.')
				}
				fkBuf.WriteString(quoteIdent(cst.Pktable.Relname))
			}
			if len(refCols) > 0 {
				fkBuf.WriteString(" (")
				fkBuf.WriteString(strings.Join(quoteIdents(refCols), ", "))
				fkBuf.WriteByte(')')
			}
			if action := fkAction(cst.FkUpdAction); action != "" {
				fkBuf.WriteString(" ON UPDATE ")
				fkBuf.WriteString(action)
			}
			if action := fkAction(cst.FkDelAction); action != "" {
				fkBuf.WriteString(" ON DELETE ")
				fkBuf.WriteString(action)
			}
			if cst.Deferrable {
				fkBuf.WriteString(" DEFERRABLE")
				if cst.Initdeferred {
					fkBuf.WriteString(" INITIALLY DEFERRED")
				}
			}
			tc := &Constraint{
				Name:              cst.Conname,
				Type:              "FOREIGN KEY",
				Columns:           []string{cd.Colname},
				Deferrable:        cst.Deferrable,
				InitiallyDeferred: cst.Initdeferred,
				Expr:              fkBuf.String(),
				Pos:               pos,
			}
			promoted = append(promoted, tc)
		}
	}

	return col, promoted, nil
}

func buildConstraint(c *pg_query.Constraint, pos pipeline.SourcePos) *Constraint {
	cst := &Constraint{
		Name:              c.Conname,
		NotValid:          c.SkipValidation,
		Deferrable:        c.Deferrable,
		InitiallyDeferred: c.Initdeferred,
		Pos:               pos,
	}
	switch c.Contype {
	case pg_query.ConstrType_CONSTR_PRIMARY:
		cst.Type = "PRIMARY KEY"
		cols := nodeListToNames(c.Keys)
		cst.Columns = cols
		if len(cols) > 0 {
			cst.Expr = "PRIMARY KEY (" + strings.Join(quoteIdents(cols), ", ") + ")"
		}

	case pg_query.ConstrType_CONSTR_UNIQUE:
		cst.Type = "UNIQUE"
		cols := nodeListToNames(c.Keys)
		cst.Columns = cols
		if len(cols) > 0 {
			nd := ""
			if c.NullsNotDistinct {
				nd = "NULLS NOT DISTINCT "
			}
			cst.Expr = "UNIQUE " + nd + "(" + strings.Join(quoteIdents(cols), ", ") + ")"
		}

	case pg_query.ConstrType_CONSTR_CHECK:
		cst.Type = "CHECK"
		if c.RawExpr != nil {
			expr := nodeToText(c.RawExpr)
			cst.Expr = "CHECK (" + expr + ")"
		}

	case pg_query.ConstrType_CONSTR_FOREIGN:
		cst.Type = "FOREIGN KEY"
		localCols := nodeListToNames(c.FkAttrs)
		refCols := nodeListToNames(c.PkAttrs)
		cst.Columns = localCols
		var b strings.Builder
		b.WriteString("FOREIGN KEY (")
		b.WriteString(strings.Join(quoteIdents(localCols), ", "))
		b.WriteString(") REFERENCES ")
		if c.Pktable != nil {
			if c.Pktable.Schemaname != "" {
				b.WriteString(quoteIdent(c.Pktable.Schemaname))
				b.WriteByte('.')
			}
			b.WriteString(quoteIdent(c.Pktable.Relname))
		}
		if len(refCols) > 0 {
			b.WriteString(" (")
			b.WriteString(strings.Join(quoteIdents(refCols), ", "))
			b.WriteByte(')')
		}
		if action := fkAction(c.FkUpdAction); action != "" {
			b.WriteString(" ON UPDATE ")
			b.WriteString(action)
		}
		if action := fkAction(c.FkDelAction); action != "" {
			b.WriteString(" ON DELETE ")
			b.WriteString(action)
		}
		if c.Deferrable {
			b.WriteString(" DEFERRABLE")
			if c.Initdeferred {
				b.WriteString(" INITIALLY DEFERRED")
			}
		}
		cst.Expr = b.String()

	case pg_query.ConstrType_CONSTR_EXCLUSION:
		cst.Type = "EXCLUDE"
		cst.Expr = "EXCLUDE" // full exclusion body is complex; preserve via rawSQL if needed
	default:
		cst.Type = "UNKNOWN"
	}
	return cst
}

// quoteIdent double-quotes a SQL identifier, escaping embedded quotes.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// nodeListToNames extracts string values from a pg_query Node list (Keys, FkAttrs, etc.).
func nodeListToNames(nodes []*pg_query.Node) []string {
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if sv := n.GetString_(); sv != nil {
			names = append(names, sv.Sval)
		}
	}
	return names
}

// quoteIdents returns a slice of double-quoted identifiers.
func quoteIdents(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = quoteIdent(n)
	}
	return out
}

// fkAction converts a pg_query FK action char to its SQL keyword.
// Returns "" for NO ACTION (the PostgreSQL default) to keep DDL concise.
func fkAction(action string) string {
	switch action {
	case "a", "": // NO ACTION is the default; omit it
		return ""
	case "r":
		return "RESTRICT"
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	}
	return ""
}

func buildPartitionSpec(ps *pg_query.PartitionSpec) *PartitionSpec {
	spec := &PartitionSpec{}
	switch ps.Strategy {
	case pg_query.PartitionStrategy_PARTITION_STRATEGY_RANGE:
		spec.Strategy = "RANGE"
	case pg_query.PartitionStrategy_PARTITION_STRATEGY_LIST:
		spec.Strategy = "LIST"
	case pg_query.PartitionStrategy_PARTITION_STRATEGY_HASH:
		spec.Strategy = "HASH"
	default:
		spec.Strategy = "RANGE"
	}
	for _, pe := range ps.PartParams {
		if pelem := pe.GetPartitionElem(); pelem != nil {
			if pelem.Name != "" {
				spec.Columns = append(spec.Columns, pelem.Name)
			}
		}
	}
	return spec
}

func buildStorageParams(options []*pg_query.Node) map[string]string {
	m := make(map[string]string)
	for _, opt := range options {
		if de := opt.GetDefElem(); de != nil {
			val := ""
			if de.Arg != nil {
				val = nodeToText(de.Arg)
			}
			m[de.Defname] = val
		}
	}
	return m
}

func mergeTableBlock(tbl *Table, block pipeline.BlockAST) error {
	if block.MigrateRemove != nil {
		return pipeline.Errorf(block.MigrateRemove.Pos,
			"MIGRATE REMOVE is not supported for TABLE objects; it is only valid for TYPE (ENUM) value removal")
	}
	if block.Comment != nil {
		tbl.Comment = &block.Comment.Value
	}
	if block.Owner != nil {
		tbl.Owner = &block.Owner.Name
	}
	if block.RenamedFrom != nil {
		tbl.RenamedFrom = &block.RenamedFrom.Name
	}
	tbl.Protected = block.Protected
	if block.Deprecated != nil {
		tbl.Deprecated = &block.Deprecated.Value
	}
	tbl.DropCascade = block.DropCascade
	tbl.RLSEnabled = block.EnableRLS
	tbl.RLSForced = block.ForceRLS

	// Indexes
	for _, idx := range block.Indices {
		tbl.Indexes = append(tbl.Indexes, blockIndexToIR(idx))
	}

	// Policies
	for _, pol := range block.Policies {
		tbl.Policies = append(tbl.Policies, blockPolicyToIR(pol))
	}

	// Triggers
	for _, tr := range block.Triggers {
		tbl.Triggers = append(tbl.Triggers, blockTriggerToIR(tr))
	}

	// Grants
	for _, g := range block.Grants {
		tbl.Grants = append(tbl.Grants, blockGrantToIR(g))
	}
	for _, r := range block.Revocations {
		tbl.Revocations = append(tbl.Revocations, blockRevocationToIR(r))
	}

	// Columns: per RFC §7.2, `COLUMN name { }` references an *existing* column
	// in the DDL. A name that doesn't match is almost always a typo (e.g.
	// "locality_ids" vs. "locality_id"); silently inventing a phantom column
	// produces broken SQL downstream (empty type, mismatched FKs), so reject
	// it at build time with a list of legal names.
	colMap := make(map[string]*Column, len(tbl.Columns))
	for _, c := range tbl.Columns {
		colMap[c.Name] = c
	}
	for _, cb := range block.Columns {
		col, ok := colMap[cb.Name.Name]
		if !ok {
			return pipeline.Errorf(cb.Pos,
				"COLUMN %q is not declared in TABLE %s; the COLUMN block must reference a column listed in the table's ( ) section%s",
				cb.Name.Name, qualName(tbl.Schema, tbl.Name), suggestColumns(cb.Name.Name, tbl.Columns))
		}
		if cb.Comment != nil {
			col.Comment = &cb.Comment.Value
		}
		if cb.Statistics != nil {
			col.Statistics = cb.Statistics
		}
		if cb.Compression != nil {
			col.Compression = &cb.Compression.Name
		}
		if cb.Storage != nil {
			col.Storage = &cb.Storage.Name
		}
		if cb.Deprecated != nil {
			col.Deprecated = &cb.Deprecated.Value
		}
		if cb.RenamedFrom != nil {
			col.RenamedFrom = &cb.RenamedFrom.Name
		}
		if cb.Using != nil {
			col.Using = &cb.Using.Text
		}
		for _, g := range cb.Grants {
			col.Grants = append(col.Grants, blockGrantToIR(g))
		}
		for _, rv := range cb.Revocations {
			col.Revocations = append(col.Revocations, blockRevocationToIR(rv))
		}
	}

	// Additional constraints from block.
	for _, cst := range block.Constraints {
		tbl.Constraints = append(tbl.Constraints, &Constraint{
			Name:     cst.Name.Name,
			Expr:     cst.Expr.Text,
			NotValid: cst.NotValid,
			Pos:      cst.Pos,
		})
	}

	// Partitions
	if block.Partitions != nil {
		for _, p := range block.Partitions.Partitions {
			tbl.Partitions = append(tbl.Partitions, &Partition{
				Name:   p.Name.Name,
				Bounds: p.Bounds.Text,
				SrcPos: p.Pos,
			})
		}
	}
	return nil
}

// suggestColumns formats a "; did you mean ..." or "; declared columns are ..."
// hint for COLUMN-block resolution errors. Returns "" when the table has no
// columns, so callers can append it unconditionally.
func suggestColumns(want string, cols []*Column) string {
	if len(cols) == 0 {
		return ""
	}
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		names = append(names, c.Name)
	}
	if best, ok := nearestColumn(want, names); ok {
		return fmt.Sprintf("; did you mean %q?", best)
	}
	return "; declared columns: " + strings.Join(names, ", ")
}

// nearestColumn returns the column name within edit distance 2 of want, or
// false if none qualify. Edit distance 2 catches typos like a single dropped
// or doubled char ("locality_ids" → "locality_id") without matching unrelated
// names — which would be more confusing than helpful.
func nearestColumn(want string, names []string) (string, bool) {
	const maxDist = 2
	best, bestDist := "", maxDist+1
	for _, n := range names {
		d := levenshtein(want, n)
		if d < bestDist {
			best, bestDist = n, d
		}
	}
	if bestDist <= maxDist {
		return best, true
	}
	return "", false
}

// levenshtein returns the edit distance between a and b. Small dedicated
// implementation — pulling in a dependency for one suggestion message is
// disproportionate.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// ── View ──────────────────────────────────────────────────────────────────────

func (b *Builder) buildView(vs *pg_query.ViewStmt, block pipeline.BlockAST, pos pipeline.SourcePos, materialized, recursive bool) (pipeline.IRObject, error) {
	v := &View{
		Schema:       rangeVarSchema(vs.View),
		Name:         vs.View.Relname,
		Materialized: materialized,
		Recursive:    recursive,
		SrcPos:       pos,
	}
	// Deparse the view query as a full statement, not as a subexpression.
	if vs.Query != nil {
		pr := &pg_query.ParseResult{Stmts: []*pg_query.RawStmt{{Stmt: vs.Query}}}
		if sql, err := pg_query.Deparse(pr); err == nil {
			v.Query = sql
		} else {
			v.Query = nodeToText(vs.Query)
		}
	}
	if block.Comment != nil {
		v.Comment = &block.Comment.Value
	}
	if block.Owner != nil {
		v.Owner = &block.Owner.Name
	}
	if block.RenamedFrom != nil {
		v.RenamedFrom = &block.RenamedFrom.Name
	}
	if block.Deprecated != nil {
		v.Deprecated = &block.Deprecated.Value
	}
	for _, g := range block.Grants {
		v.Grants = append(v.Grants, blockGrantToIR(g))
	}
	for _, r := range block.Revocations {
		v.Revocations = append(v.Revocations, blockRevocationToIR(r))
	}
	return v, nil
}

// ── Function / Procedure ──────────────────────────────────────────────────────

func (b *Builder) buildFunction(cfs *pg_query.CreateFunctionStmt, pg pipeline.PGParseResult, block pipeline.BlockAST, pos pipeline.SourcePos) (pipeline.IRObject, error) {
	raw := ast.Unwrap(pg)

	if cfs.IsProcedure {
		return b.buildProcedure(cfs, raw, block, pos)
	}

	fn := &Function{SrcPos: pos}
	if len(cfs.Funcname) > 0 {
		fn.Schema, fn.Name = extractFuncName(cfs.Funcname)
	}
	for _, p := range cfs.Parameters {
		if fp := p.GetFunctionParameter(); fp != nil {
			fn.Args = append(fn.Args, buildFuncArg(fp))
		}
	}
	if cfs.ReturnType != nil {
		fn.ReturnType = typeNameToRef(cfs.ReturnType)
	}
	fn.Attrs = extractFuncAttrs(cfs.Options)

	// Body hash from the Part1 raw text (we can recover it via deparse or from raw).
	body := fn.Attrs.Body
	fn.BodyHash = HashBody(body)

	if block.Comment != nil {
		fn.Comment = &block.Comment.Value
	}
	if block.RenamedFrom != nil {
		fn.RenamedFrom = &block.RenamedFrom.Name
	}
	if block.Deprecated != nil {
		fn.Deprecated = &block.Deprecated.Value
	}
	for _, g := range block.Grants {
		fn.Grants = append(fn.Grants, blockGrantToIR(g))
	}
	return fn, nil
}

func (b *Builder) buildProcedure(cfs *pg_query.CreateFunctionStmt, _ *pg_query.ParseResult, block pipeline.BlockAST, pos pipeline.SourcePos) (pipeline.IRObject, error) {
	proc := &Procedure{SrcPos: pos}
	if len(cfs.Funcname) > 0 {
		proc.Schema, proc.Name = extractFuncName(cfs.Funcname)
	}
	for _, p := range cfs.Parameters {
		if fp := p.GetFunctionParameter(); fp != nil {
			proc.Args = append(proc.Args, buildFuncArg(fp))
		}
	}
	proc.Attrs = extractFuncAttrs(cfs.Options)
	proc.BodyHash = HashBody(proc.Attrs.Body)
	if block.Comment != nil {
		proc.Comment = &block.Comment.Value
	}
	for _, g := range block.Grants {
		proc.Grants = append(proc.Grants, blockGrantToIR(g))
	}
	return proc, nil
}

func extractFuncName(funcname []*pg_query.Node) (schema, name string) {
	switch len(funcname) {
	case 1:
		if sv := funcname[0].GetString_(); sv != nil {
			name = sv.Sval
		}
	case 2:
		if sv := funcname[0].GetString_(); sv != nil {
			schema = sv.Sval
		}
		if sv := funcname[1].GetString_(); sv != nil {
			name = sv.Sval
		}
	}
	return
}

func buildFuncArg(fp *pg_query.FunctionParameter) FuncArg {
	arg := FuncArg{
		Name: fp.Name,
	}
	if fp.ArgType != nil {
		arg.Type = typeNameToRef(fp.ArgType)
	}
	switch fp.Mode {
	case pg_query.FunctionParameterMode_FUNC_PARAM_IN:
		arg.Mode = "IN"
	case pg_query.FunctionParameterMode_FUNC_PARAM_OUT:
		arg.Mode = "OUT"
	case pg_query.FunctionParameterMode_FUNC_PARAM_INOUT:
		arg.Mode = "INOUT"
	case pg_query.FunctionParameterMode_FUNC_PARAM_VARIADIC:
		arg.Mode = "VARIADIC"
	case pg_query.FunctionParameterMode_FUNC_PARAM_TABLE:
		arg.Mode = "TABLE"
	default:
		arg.Mode = "IN"
	}
	return arg
}

func extractFuncAttrs(options []*pg_query.Node) FuncAttrs {
	attrs := FuncAttrs{Volatility: "VOLATILE", Parallel: "UNSAFE"}
	for _, opt := range options {
		de := opt.GetDefElem()
		if de == nil {
			continue
		}
		switch strings.ToLower(de.Defname) {
		case "language":
			if sv := de.Arg.GetString_(); sv != nil {
				attrs.Language = sv.Sval
			}
		case "volatility":
			if sv := de.Arg.GetString_(); sv != nil {
				attrs.Volatility = strings.ToUpper(sv.Sval)
			}
		case "strict":
			attrs.Strict = de.Arg.GetBoolean() != nil && de.Arg.GetBoolean().Boolval
		case "security":
			attrs.SecurityDef = de.Arg.GetBoolean() != nil && de.Arg.GetBoolean().Boolval
		case "parallel":
			if sv := de.Arg.GetString_(); sv != nil {
				attrs.Parallel = strings.ToUpper(sv.Sval)
			}
		case "as":
			// The body is in the Arg list as a List node for dollar-quoted bodies.
			if list := de.Arg.GetList(); list != nil && len(list.Items) > 0 {
				if sv := list.Items[0].GetString_(); sv != nil {
					attrs.Body = sv.Sval
				}
			} else if sv := de.Arg.GetString_(); sv != nil {
				attrs.Body = sv.Sval
			}
		}
	}
	return attrs
}

// ── Enum ─────────────────────────────────────────────────────────────────────

func (b *Builder) buildEnum(cs *pg_query.CreateEnumStmt, block pipeline.BlockAST, pos pipeline.SourcePos) (pipeline.IRObject, error) {
	t := &Type{
		Variant: "ENUM",
		SrcPos:  pos,
	}
	if len(cs.TypeName) > 0 {
		t.Schema, t.Name = extractTypeName(cs.TypeName)
	}
	for _, v := range cs.Vals {
		if sv := v.GetString_(); sv != nil {
			t.EnumValues = append(t.EnumValues, sv.Sval)
		}
	}
	if block.Comment != nil {
		t.Comment = &block.Comment.Value
	}
	if block.Owner != nil {
		t.Owner = &block.Owner.Name
	}
	if block.Deprecated != nil {
		t.Deprecated = &block.Deprecated.Value
	}
	if block.MigrateRemove != nil {
		t.MigrateRemove = block.MigrateRemove
	}
	return t, nil
}

func extractTypeName(names []*pg_query.Node) (schema, name string) {
	switch len(names) {
	case 1:
		if sv := names[0].GetString_(); sv != nil {
			name = sv.Sval
		}
	case 2:
		if sv := names[0].GetString_(); sv != nil {
			schema = sv.Sval
		}
		if sv := names[1].GetString_(); sv != nil {
			name = sv.Sval
		}
	}
	return
}

// ── Schema ────────────────────────────────────────────────────────────────────

func (b *Builder) buildSchema(cs *pg_query.CreateSchemaStmt, block pipeline.BlockAST, pos pipeline.SourcePos) (pipeline.IRObject, error) {
	s := &Schema{Name: cs.Schemaname, SrcPos: pos}
	if block.Comment != nil {
		s.Comment = &block.Comment.Value
	}
	if block.Owner != nil {
		s.Owner = &block.Owner.Name
	}
	if block.RenamedFrom != nil {
		s.RenamedFrom = &block.RenamedFrom.Name
	}
	return s, nil
}

// ── Extension ─────────────────────────────────────────────────────────────────

func (b *Builder) buildExtension(cs *pg_query.CreateExtensionStmt, block pipeline.BlockAST, pos pipeline.SourcePos) (pipeline.IRObject, error) {
	e := &Extension{Name: cs.Extname, SrcPos: pos}
	// Schema and version come from the options list.
	for _, opt := range cs.Options {
		if de := opt.GetDefElem(); de != nil {
			switch de.Defname {
			case "schema":
				if sv := de.Arg.GetString_(); sv != nil {
					s := sv.Sval
					e.Schema = &s
				}
			case "new_version":
				if sv := de.Arg.GetString_(); sv != nil {
					v := sv.Sval
					e.Version = &v
				}
			}
		}
	}
	return e, nil
}

// ── Sequence ──────────────────────────────────────────────────────────────────

func (b *Builder) buildSequence(cs *pg_query.CreateSeqStmt, block pipeline.BlockAST, pos pipeline.SourcePos) (pipeline.IRObject, error) {
	s := &Sequence{
		Schema: rangeVarSchema(cs.Sequence),
		Name:   cs.Sequence.Relname,
		SrcPos: pos,
	}
	if block.Comment != nil {
		s.Comment = &block.Comment.Value
	}
	if block.Owner != nil {
		s.Owner = &block.Owner.Name
	}
	for _, g := range block.Grants {
		s.Grants = append(s.Grants, blockGrantToIR(g))
	}
	for _, opt := range cs.Options {
		de := opt.GetDefElem()
		if de == nil {
			continue
		}
		v := seqOptionInt(de)
		switch de.Defname {
		case "increment":
			s.IncrementBy = v
		case "start":
			s.StartValue = v
		case "minvalue":
			s.MinValue = v
		case "maxvalue":
			s.MaxValue = v
		case "cache":
			s.Cache = v
		case "cycle":
			if v != nil {
				s.Cycle = *v != 0
			}
		}
	}
	return s, nil
}

// seqOptionInt extracts an int64 value from a sequence DefElem node.
// pg_query represents integer sequence options as either a pg_query.Integer
// or an A_Const Integer node.
func seqOptionInt(de *pg_query.DefElem) *int64 {
	if de.Arg == nil {
		return nil
	}
	if ic := de.Arg.GetInteger(); ic != nil {
		v := int64(ic.Ival)
		return &v
	}
	if ac := de.Arg.GetAConst(); ac != nil {
		if ic2 := ac.GetIval(); ic2 != nil {
			v := int64(ic2.Ival)
			return &v
		}
	}
	return nil
}

// ── Role ──────────────────────────────────────────────────────────────────────

func (b *Builder) buildRole(cs *pg_query.CreateRoleStmt, block pipeline.BlockAST, pos pipeline.SourcePos) (pipeline.IRObject, error) {
	r := &Role{Name: cs.Role, SrcPos: pos}
	if block.Comment != nil {
		r.Comment = &block.Comment.Value
	}
	return r, nil
}

// ── Tablespace ────────────────────────────────────────────────────────────────

func (b *Builder) buildTablespace(cs *pg_query.CreateTableSpaceStmt, block pipeline.BlockAST, pos pipeline.SourcePos, body string) (pipeline.IRObject, error) {
	ts := &Tablespace{Name: cs.Tablespacename, Body: body, SrcPos: pos}
	if block.Comment != nil {
		ts.Comment = &block.Comment.Value
	}
	return ts, nil
}

// ── FDW / Server / User Mapping ───────────────────────────────────────────────

func (b *Builder) buildFDW(cs *pg_query.CreateFdwStmt, block pipeline.BlockAST, pos pipeline.SourcePos, body string) (pipeline.IRObject, error) {
	f := &ForeignDataWrapper{Name: cs.Fdwname, Body: body, SrcPos: pos}
	if block.Comment != nil {
		f.Comment = &block.Comment.Value
	}
	return f, nil
}

func (b *Builder) buildServer(cs *pg_query.CreateForeignServerStmt, block pipeline.BlockAST, pos pipeline.SourcePos, body string) (pipeline.IRObject, error) {
	s := &ForeignServer{Name: cs.Servername, Body: body, SrcPos: pos}
	if block.Comment != nil {
		s.Comment = &block.Comment.Value
	}
	return s, nil
}

func (b *Builder) buildUserMapping(cs *pg_query.CreateUserMappingStmt, block pipeline.BlockAST, pos pipeline.SourcePos, body string) (pipeline.IRObject, error) {
	user := ""
	if cs.User != nil {
		user = cs.User.Rolename
	}
	return &UserMapping{
		User:   user,
		Server: cs.Servername,
		Body:   body,
		SrcPos: pos,
	}, nil
}

// ── Domain ────────────────────────────────────────────────────────────────────

func (b *Builder) buildDomain(cs *pg_query.CreateDomainStmt, block pipeline.BlockAST, pos pipeline.SourcePos, body string) (pipeline.IRObject, error) {
	schema, name := extractTypeName(cs.Domainname)
	t := &Type{
		Schema:  schema,
		Name:    name,
		Variant: "DOMAIN",
		Body:    body,
		SrcPos:  pos,
	}
	if block.Comment != nil {
		t.Comment = &block.Comment.Value
	}
	return t, nil
}

// ── Statistics ────────────────────────────────────────────────────────────────

func (b *Builder) buildStatistics(cs *pg_query.CreateStatsStmt, block pipeline.BlockAST, pos pipeline.SourcePos) (pipeline.IRObject, error) {
	s := &StatisticsObject{SrcPos: pos}
	if len(cs.Defnames) > 0 {
		s.Schema, s.Name = extractTypeName(cs.Defnames)
	}
	return s, nil
}

// ── DefineStmt (composite/range/base type, aggregate, operator, collation, TS objects) ──

func (b *Builder) buildDefineStmt(ds *pg_query.DefineStmt, block pipeline.BlockAST, pos pipeline.SourcePos, rawBody string) (pipeline.IRObject, error) {
	schema, name := extractTypeName(ds.Defnames)
	switch ds.Kind {
	case pg_query.ObjectType_OBJECT_TYPE:
		t := &Type{Schema: schema, Name: name, SrcPos: pos}
		if block.Comment != nil {
			t.Comment = &block.Comment.Value
		}
		// Distinguish composite/range/base by the definition elements.
		// Composite: has list of column defs
		// Range: has "subtype" element
		// Base: has "input" element (input/output functions)
		isRange, isComposite := false, false
		for _, de := range ds.Definition {
			if elem := de.GetDefElem(); elem != nil {
				switch elem.Defname {
				case "subtype":
					isRange = true
				case "input":
					// base type
				case "column":
					isComposite = true
				}
			}
		}
		if isRange {
			t.Variant = "RANGE"
		} else if isComposite {
			t.Variant = "COMPOSITE"
			for _, de := range ds.Definition {
				elem := de.GetDefElem()
				if elem == nil || elem.Defname != "column" {
					continue
				}
				cd := elem.Arg.GetColumnDef()
				if cd == nil {
					continue
				}
				col := &Column{Name: cd.Colname}
				if cd.TypeName != nil {
					col.Type = typeNameToRef(cd.TypeName)
				}
				t.CompositeAttrs = append(t.CompositeAttrs, col)
			}
		} else {
			t.Variant = "BASE"
		}
		return t, nil

	case pg_query.ObjectType_OBJECT_AGGREGATE:
		agg := &Aggregate{Schema: schema, Name: name, Body: rawBody, SrcPos: pos}
		for _, p := range ds.Args {
			if fp := p.GetFunctionParameter(); fp != nil {
				agg.Args = append(agg.Args, buildFuncArg(fp))
			}
		}
		if block.Comment != nil {
			agg.Comment = &block.Comment.Value
		}
		for _, g := range block.Grants {
			agg.Grants = append(agg.Grants, blockGrantToIR(g))
		}
		return agg, nil

	case pg_query.ObjectType_OBJECT_OPERATOR:
		return &Operator{Schema: schema, Name: name, SrcPos: pos}, nil

	case pg_query.ObjectType_OBJECT_COLLATION:
		return &Collation{Schema: schema, Name: name, SrcPos: pos}, nil

	case pg_query.ObjectType_OBJECT_TSCONFIGURATION:
		tc := &TSConfig{Schema: schema, Name: name, SrcPos: pos}
		tc.Mappings = append(tc.Mappings, block.Mappings...)
		if block.Comment != nil {
			tc.Comment = &block.Comment.Value
		}
		return tc, nil

	case pg_query.ObjectType_OBJECT_TSDICTIONARY:
		return &TSDict{Schema: schema, Name: name, SrcPos: pos}, nil

	case pg_query.ObjectType_OBJECT_TSPARSER:
		return &TSParser{Schema: schema, Name: name, SrcPos: pos}, nil

	case pg_query.ObjectType_OBJECT_TSTEMPLATE:
		return &TSTemplate{Schema: schema, Name: name, SrcPos: pos}, nil
	}

	return &OpaqueObject{kind: ds.Kind.String(), body: name, SrcPos: pos}, nil
}

// ── Default Privileges ────────────────────────────────────────────────────────

func (b *Builder) buildDefaultPrivileges(stmt *pg_query.AlterDefaultPrivilegesStmt, block pipeline.BlockAST, pos pipeline.SourcePos) (pipeline.IRObject, error) {
	dp := &DefaultPrivileges{SrcPos: pos}
	for _, g := range block.Grants {
		dp.Grants = append(dp.Grants, blockGrantToIR(g))
	}
	for _, r := range block.Revocations {
		dp.Revocations = append(dp.Revocations, blockRevocationToIR(r))
	}
	if block.Owner != nil {
		dp.ForRole = &block.Owner.Name
	}
	return dp, nil
}

// ── Cast ──────────────────────────────────────────────────────────────────────

func (b *Builder) buildCast(cs *pg_query.CreateCastStmt, _ pipeline.BlockAST, pos pipeline.SourcePos, body string) (pipeline.IRObject, error) {
	c := &Cast{Body: body, SrcPos: pos}
	if cs.Sourcetype != nil {
		c.SourceType = typeNameToRef(cs.Sourcetype)
	}
	if cs.Targettype != nil {
		c.TargetType = typeNameToRef(cs.Targettype)
	}
	return c, nil
}

// ── opaque fallback ───────────────────────────────────────────────────────────

// OpaqueObject stores any IR object that doesn't have a dedicated concrete type yet.
type OpaqueObject struct {
	kind   string
	body   string
	SrcPos pipeline.SourcePos
}

func (o *OpaqueObject) QualifiedName() string   { return o.body }
func (o *OpaqueObject) Pos() pipeline.SourcePos { return o.SrcPos }
func (o *OpaqueObject) irObject()               {}

// rawSQL deparsed a single node back to SQL, returning "" on error.
func rawSQL(node *pg_query.Node) string {
	pr := &pg_query.ParseResult{Stmts: []*pg_query.RawStmt{{Stmt: node}}}
	sql, err := pg_query.Deparse(pr)
	if err != nil {
		return ""
	}
	return sql
}

func (b *Builder) buildOpaque(node *pg_query.Node, _ pipeline.BlockAST, pos pipeline.SourcePos, kind string) (pipeline.IRObject, error) {
	sql := rawSQL(node)
	switch n := node.Node.(type) {
	case *pg_query.Node_CreatePublicationStmt:
		return &Publication{Name: n.CreatePublicationStmt.Pubname, Body: sql, SrcPos: pos}, nil
	case *pg_query.Node_CreateSubscriptionStmt:
		return &Subscription{Name: n.CreateSubscriptionStmt.Subname, Body: sql, SrcPos: pos}, nil
	case *pg_query.Node_CreateEventTrigStmt:
		return &EventTrigger{Name: n.CreateEventTrigStmt.Trigname, Body: sql, SrcPos: pos}, nil
	case *pg_query.Node_CreateOpClassStmt:
		schema, name := extractTypeName(n.CreateOpClassStmt.Opclassname)
		return &OperatorClass{Schema: schema, Name: name, Body: sql, SrcPos: pos}, nil
	case *pg_query.Node_CreateOpFamilyStmt:
		schema, name := extractTypeName(n.CreateOpFamilyStmt.Opfamilyname)
		return &OperatorFamily{Schema: schema, Name: name, Body: sql, SrcPos: pos}, nil
	}
	return &OpaqueObject{kind: kind, body: kind, SrcPos: pos}, nil
}

// ── conversion helpers ────────────────────────────────────────────────────────

func rangeVarSchema(rv *pg_query.RangeVar) string {
	if rv == nil {
		return ""
	}
	return rv.Schemaname
}

// nodeToText produces a best-effort text representation of a pg_query Node.
// For simple cases (string literals, identifiers) it returns the exact value.
// For complex expressions it returns the pg_query JSON representation (debug only).
func nodeToText(n *pg_query.Node) string {
	if n == nil {
		return ""
	}
	if sv := n.GetString_(); sv != nil {
		return sv.Sval
	}
	if ic := n.GetInteger(); ic != nil {
		return fmt.Sprintf("%d", ic.Ival)
	}
	if fc := n.GetFloat(); fc != nil {
		return fc.Fval
	}
	if bv := n.GetBoolean(); bv != nil {
		if bv.Boolval {
			return "true"
		}
		return "false"
	}
	// A_Const: typed literal constant (string, int, float, boolean, null).
	if ac := n.GetAConst(); ac != nil {
		if ac.GetIsnull() {
			return "NULL"
		}
		if sv := ac.GetSval(); sv != nil {
			return "'" + sv.Sval + "'"
		}
		if iv := ac.GetIval(); iv != nil {
			return fmt.Sprintf("%d", iv.Ival)
		}
		if fv := ac.GetFval(); fv != nil {
			return fv.Fval
		}
		if bv := ac.GetBoolval(); bv != nil {
			if bv.Boolval {
				return "true"
			}
			return "false"
		}
	}
	// For expression nodes (FuncCall, TypeCast, etc.) deparse by wrapping in
	// SELECT so pg_query sees a full statement, then strip the SELECT prefix.
	selectStmt := &pg_query.Node{
		Node: &pg_query.Node_SelectStmt{
			SelectStmt: &pg_query.SelectStmt{
				TargetList: []*pg_query.Node{
					{Node: &pg_query.Node_ResTarget{
						ResTarget: &pg_query.ResTarget{Val: n},
					}},
				},
			},
		},
	}
	pr := &pg_query.ParseResult{
		Stmts: []*pg_query.RawStmt{{Stmt: selectStmt}},
	}
	if sql, err := pg_query.Deparse(pr); err == nil {
		if after, ok := strings.CutPrefix(sql, "SELECT "); ok {
			return after
		}
		return sql
	}
	return "<expr>"
}

// fmt is imported for Sprintf in nodeToText; declare the import.

func blockGrantToIR(g pipeline.GrantEntry) Grant {
	gr := Grant{WithGrant: g.WithGrant, Privileges: g.Privileges, Pos: g.Pos}
	for _, r := range g.Roles {
		gr.Roles = append(gr.Roles, r.String())
	}
	return gr
}

func blockRevocationToIR(r pipeline.RevocationEntry) Revocation {
	rev := Revocation{Cascade: r.Cascade, Privileges: r.Privileges, Pos: r.Pos}
	for _, role := range r.Roles {
		rev.Roles = append(rev.Roles, role.String())
	}
	return rev
}

func blockIndexToIR(idx pipeline.IndexDef) *Index {
	ir := &Index{
		Name:         idx.Name.Name,
		Unique:       idx.Unique,
		Concurrently: idx.Concurrently,
		Columns:      idx.Columns,
		Pos:          idx.Pos,
	}
	if idx.Method != nil {
		ir.Method = idx.Method.Name
	} else {
		ir.Method = "btree"
	}
	if idx.Where != nil {
		ir.Where = &idx.Where.Text
	}
	for _, inc := range idx.Include {
		ir.Include = append(ir.Include, inc.Name)
	}
	ir.With = idx.With
	if idx.Tablespace != nil {
		ir.Tablespace = &idx.Tablespace.Name
	}
	return ir
}

func blockPolicyToIR(pol pipeline.PolicyDef) *Policy {
	p := &Policy{
		Name:       pol.Name.Name,
		Command:    pol.Command,
		Permissive: pol.Permissive,
		Pos:        pol.Pos,
	}
	if pol.Using != nil {
		p.Using = &pol.Using.Text
	}
	if pol.WithCheck != nil {
		p.WithCheck = &pol.WithCheck.Text
	}
	for _, r := range pol.Roles {
		p.Roles = append(p.Roles, r.String())
	}
	return p
}

func blockTriggerToIR(tr pipeline.TriggerDef) *Trigger {
	t := &Trigger{
		Name:    tr.Name.Name,
		When:    tr.When,
		Events:  tr.Events,
		ForEach: tr.ForEach,
		Args:    tr.Args,
		Pos:     tr.Pos,
	}
	t.Function = tr.Function.String()
	if tr.Condition != nil {
		t.Condition = &tr.Condition.Text
	}
	return t
}

// ── VirtualType ───────────────────────────────────────────────────────────────

// buildVirtualType parses a VIRTUAL TYPE declaration from the raw Part1 text.
// Part1 format: [schema.]name AS body
// The body (after AS) is stored verbatim for downstream consumers; DPG generates
// no SQL for virtual types.
func (b *Builder) buildVirtualType(part1 string, block pipeline.BlockAST, pos pipeline.SourcePos, schemaCtx string) (*VirtualType, error) {
	// Find the standalone AS keyword by scanning word-by-word.
	upper := strings.ToUpper(part1)
	asIdx := -1
	for i := 0; i < len(upper); {
		// Skip whitespace.
		for i < len(upper) && isWS(upper[i]) {
			i++
		}
		if i >= len(upper) {
			break
		}
		// If this character starts a word, read it.
		if isWordChar(upper[i]) {
			start := i
			for i < len(upper) && isWordChar(upper[i]) {
				i++
			}
			if upper[start:i] == "AS" {
				asIdx = start
				break
			}
		} else {
			i++ // skip any non-word, non-whitespace character (e.g. '.')
		}
	}
	if asIdx < 0 {
		return nil, pipeline.Errorf(pos, "VIRTUAL TYPE: expected AS keyword in %q", part1)
	}

	namePart := strings.TrimSpace(part1[:asIdx])
	body := strings.TrimSpace(part1[asIdx+2:]) // skip "AS"

	// Parse the name (possibly schema-qualified: schema.name).
	var schema, name string
	if dotIdx := strings.LastIndex(namePart, "."); dotIdx >= 0 {
		schema = namePart[:dotIdx]
		name = namePart[dotIdx+1:]
	} else {
		name = namePart
	}
	if schema == "" {
		if schemaCtx != "" {
			schema = schemaCtx
		} else {
			schema = "public"
		}
	}

	vt := &VirtualType{
		Schema: schema,
		Name:   name,
		Body:   body,
		SrcPos: pos,
	}
	if block.Comment != nil {
		vt.Comment = &block.Comment.Value
	}
	return vt, nil
}

func isWS(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}
