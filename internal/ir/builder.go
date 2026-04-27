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
func (b *Builder) Build(pg pipeline.PGParseResult, block pipeline.BlockAST) (pipeline.IRObject, error) {
	node := ast.FirstStmt(pg)
	if node == nil {
		return nil, pipeline.Errorf(pg.Pos, "empty PG parse result")
	}
	pos := pg.Pos

	switch n := node.Node.(type) {
	case *pg_query.Node_CreateStmt:
		return b.buildTable(n.CreateStmt, block, pos, false, false)
	case *pg_query.Node_CreateForeignTableStmt:
		return b.buildForeignTable(n.CreateForeignTableStmt, block, pos)
	case *pg_query.Node_ViewStmt:
		return b.buildView(n.ViewStmt, block, pos, false, false)
	case *pg_query.Node_CreateFunctionStmt:
		return b.buildFunction(n.CreateFunctionStmt, pg, block, pos)
	case *pg_query.Node_CreateEnumStmt:
		return b.buildEnum(n.CreateEnumStmt, block, pos)
	case *pg_query.Node_CreateSchemaStmt:
		return b.buildSchema(n.CreateSchemaStmt, block, pos)
	case *pg_query.Node_CreateExtensionStmt:
		return b.buildExtension(n.CreateExtensionStmt, block, pos)
	case *pg_query.Node_CreateSeqStmt:
		return b.buildSequence(n.CreateSeqStmt, block, pos)
	case *pg_query.Node_CreateRoleStmt:
		return b.buildRole(n.CreateRoleStmt, block, pos)
	case *pg_query.Node_CreateTableSpaceStmt:
		return b.buildTablespace(n.CreateTableSpaceStmt, block, pos, rawSQL(node))
	case *pg_query.Node_CreateFdwStmt:
		return b.buildFDW(n.CreateFdwStmt, block, pos, rawSQL(node))
	case *pg_query.Node_CreateForeignServerStmt:
		return b.buildServer(n.CreateForeignServerStmt, block, pos, rawSQL(node))
	case *pg_query.Node_CreateUserMappingStmt:
		return b.buildUserMapping(n.CreateUserMappingStmt, block, pos, rawSQL(node))
	case *pg_query.Node_CreatePublicationStmt:
		return b.buildOpaque(node, block, pos, "PUBLICATION")
	case *pg_query.Node_CreateSubscriptionStmt:
		return b.buildOpaque(node, block, pos, "SUBSCRIPTION")
	case *pg_query.Node_CreateEventTrigStmt:
		return b.buildOpaque(node, block, pos, "EVENT TRIGGER")
	case *pg_query.Node_DefineStmt:
		return b.buildDefineStmt(n.DefineStmt, block, pos)
	case *pg_query.Node_CreateDomainStmt:
		return b.buildDomain(n.CreateDomainStmt, block, pos)
	case *pg_query.Node_CreateOpClassStmt:
		return b.buildOpaque(node, block, pos, "OPERATOR CLASS")
	case *pg_query.Node_CreateOpFamilyStmt:
		return b.buildOpaque(node, block, pos, "OPERATOR FAMILY")
	case *pg_query.Node_CreateStatsStmt:
		return b.buildStatistics(n.CreateStatsStmt, block, pos)
	case *pg_query.Node_AlterDefaultPrivilegesStmt:
		return b.buildDefaultPrivileges(n.AlterDefaultPrivilegesStmt, block, pos)
	case *pg_query.Node_CreateOpClassItem:
		return b.buildOpaque(node, block, pos, "OPERATOR")
	case *pg_query.Node_CreateCastStmt:
		return b.buildCast(n.CreateCastStmt, block, pos, rawSQL(node))
	default:
		// Generic fallback: store as opaque with qualified name from block or pos.
		return &OpaqueObject{kind: "UNKNOWN", body: "", SrcPos: pos}, nil
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
			col, err := b.buildColumn(e.ColumnDef, pos)
			if err != nil {
				return nil, err
			}
			tbl.Columns = append(tbl.Columns, col)
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
	mergeTableBlock(tbl, block)
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

func (b *Builder) buildColumn(cd *pg_query.ColumnDef, pos pipeline.SourcePos) (*Column, error) {
	col := &Column{
		Name:   cd.Colname,
		SrcPos: pos,
	}
	if cd.TypeName != nil {
		col.Type = typeNameToRef(cd.TypeName)
	}

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
			// GENERATED ALWAYS AS (expr) STORED
			if cst.RawExpr != nil {
				expr := nodeToText(cst.RawExpr)
				col.Generated = &Generated{Expr: expr, Stored: true}
			}
		case pg_query.ConstrType_CONSTR_IDENTITY:
			// GENERATED [ALWAYS|BY DEFAULT] AS IDENTITY: GeneratedWhen = "a" or "d"
			col.Identity = &Identity{Always: cst.GeneratedWhen == "a"}
		}
	}

	return col, nil
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
	case pg_query.ConstrType_CONSTR_UNIQUE:
		cst.Type = "UNIQUE"
	case pg_query.ConstrType_CONSTR_CHECK:
		cst.Type = "CHECK"
		if c.RawExpr != nil {
			cst.Expr = nodeToText(c.RawExpr)
		}
	case pg_query.ConstrType_CONSTR_FOREIGN:
		cst.Type = "FOREIGN KEY"
	case pg_query.ConstrType_CONSTR_EXCLUSION:
		cst.Type = "EXCLUDE"
	default:
		cst.Type = "UNKNOWN"
	}
	for _, k := range c.Keys {
		if sv := k.GetString_(); sv != nil {
			cst.Columns = append(cst.Columns, sv.Sval)
		}
	}
	return cst
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

func mergeTableBlock(tbl *Table, block pipeline.BlockAST) {
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

	// Columns: merge block attributes into existing columns.
	colMap := make(map[string]*Column, len(tbl.Columns))
	for _, c := range tbl.Columns {
		colMap[c.Name] = c
	}
	for _, cb := range block.Columns {
		col, ok := colMap[cb.Name.Name]
		if !ok {
			col = &Column{Name: cb.Name.Name, SrcPos: cb.Pos}
			tbl.Columns = append(tbl.Columns, col)
			colMap[col.Name] = col
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
	// Query: deparse the SelectStmt back to text (best-effort).
	if vs.Query != nil {
		v.Query = nodeToText(vs.Query)
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
	fn.BodyHash = hashBody(body)

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
	proc.BodyHash = hashBody(proc.Attrs.Body)
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
	return s, nil
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

func (b *Builder) buildDomain(cs *pg_query.CreateDomainStmt, block pipeline.BlockAST, pos pipeline.SourcePos) (pipeline.IRObject, error) {
	schema, name := extractTypeName(cs.Domainname)
	t := &Type{
		Schema:  schema,
		Name:    name,
		Variant: "DOMAIN",
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

func (b *Builder) buildDefineStmt(ds *pg_query.DefineStmt, block pipeline.BlockAST, pos pipeline.SourcePos) (pipeline.IRObject, error) {
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
		} else {
			t.Variant = "BASE"
		}
		return t, nil

	case pg_query.ObjectType_OBJECT_AGGREGATE:
		agg := &Aggregate{Schema: schema, Name: name, SrcPos: pos}
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
	// For complex nodes, deparse via pg_query.
	pr := &pg_query.ParseResult{
		Stmts: []*pg_query.RawStmt{{Stmt: n}},
	}
	if sql, err := pg_query.Deparse(pr); err == nil {
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
