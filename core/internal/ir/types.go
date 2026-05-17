// Package ir defines the fully-resolved Internal Representation of all DPG
// objects. Every IR value is fully qualified, schema-scoped, and source-annotated.
// IR types are produced by the IRBuilder (Phase 5) and consumed by the Merger,
// DependencyResolver, Differ, and Emitter.
package ir

import (
	"fmt"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

// ── common helpers ────────────────────────────────────────────────────────────

// qualName formats a schema-qualified name.
func qualName(schema, name string) string {
	if schema == "" {
		return name
	}
	return schema + "." + name
}

// ── type references ───────────────────────────────────────────────────────────

// TypeRef is a SQL type reference extracted from a pg_query TypeName node.
type TypeRef struct {
	Schema    string // empty for built-in types (pg_catalog)
	Name      string // e.g. "int4", "text", "varchar"
	Mods      string // raw typemod text, e.g. "(255)" for varchar(255)
	ArrayDims int    // number of [] dimensions (0 = scalar)
}

func (t TypeRef) String() string {
	s := qualName(t.Schema, t.Name)
	if t.Mods != "" {
		s += t.Mods
	}
	for i := 0; i < t.ArrayDims; i++ {
		s += "[]"
	}
	return s
}

// ── shared sub-types ──────────────────────────────────────────────────────────

// Column is one column in a table, view-column-list, or composite type.
type Column struct {
	Name        string
	RenamedFrom *string
	Type        TypeRef
	NotNull     bool
	Default     *string    // raw expression text
	Generated   *Generated // GENERATED ALWAYS AS
	Identity    *Identity  // GENERATED [ALWAYS|BY DEFAULT] AS IDENTITY
	Comment     *string
	Statistics  *int
	Compression *string
	Storage     *string
	Deprecated  *string
	Using       *string // USING expression for ALTER COLUMN TYPE
	Grants      []Grant
	Revocations []Revocation
	SrcPos      pipeline.SourcePos
}

// Generated holds a GENERATED ALWAYS AS (expr) STORED column spec.
type Generated struct {
	Expr   string // the generating expression
	Stored bool   // always true in PG currently
}

// Identity holds a GENERATED [ALWAYS|BY DEFAULT] AS IDENTITY column spec.
type Identity struct {
	Always bool // true = ALWAYS, false = BY DEFAULT
}

// Index is a CREATE INDEX / INDICES entry.
type Index struct {
	Name         string
	Unique       bool
	Method       string // "btree" (default), "hash", "gin", "gist", etc.
	Columns      []pipeline.IndexColumn
	Where        *string // partial index predicate
	Include      []string
	With         []pipeline.StorageParam
	Tablespace   *string
	Concurrently bool
	Pos          pipeline.SourcePos
}

// Constraint is a table or column constraint.
type Constraint struct {
	Name              string
	Type              string // "PRIMARY KEY", "UNIQUE", "CHECK", "FOREIGN KEY", "EXCLUDE"
	Expr              string // raw constraint expression/definition
	Columns           []string
	NotValid          bool
	Deferrable        bool
	InitiallyDeferred bool
	Pos               pipeline.SourcePos
}

// Policy is a row-security policy.
type Policy struct {
	Name       string
	Command    string // "ALL", "SELECT", etc.
	Permissive bool
	Using      *string
	WithCheck  *string
	Roles      []string
	Pos        pipeline.SourcePos
}

// Trigger is a trigger definition.
type Trigger struct {
	Name      string
	When      string   // "BEFORE", "AFTER", "INSTEAD OF"
	Events    []string // "INSERT", "UPDATE", "DELETE", "TRUNCATE"
	ForEach   string   // "ROW", "STATEMENT"
	Condition *string
	Function  string // qualified function name
	Args      []string
	Pos       pipeline.SourcePos
}

// Grant is a single GRANT directive.
type Grant struct {
	Privileges []string // nil = ALL
	Roles      []string
	WithGrant  bool
	Pos        pipeline.SourcePos
}

// Revocation is a single REVOKE directive.
type Revocation struct {
	Privileges []string // nil = ALL
	Roles      []string
	Cascade    bool
	Pos        pipeline.SourcePos
}

// FuncArg is a function/procedure/aggregate parameter.
type FuncArg struct {
	Name    string
	Type    TypeRef
	Mode    string // "IN", "OUT", "INOUT", "VARIADIC", "TABLE"
	Default *string
}

// FuncAttrs holds function/procedure attributes extracted from pg_query.
type FuncAttrs struct {
	Language    string // "sql", "plpgsql", etc.
	Volatility  string // "VOLATILE", "STABLE", "IMMUTABLE"
	Strict      bool   // RETURNS NULL ON NULL INPUT
	SecurityDef bool   // SECURITY DEFINER
	Parallel    string // "UNSAFE", "RESTRICTED", "SAFE"
	Cost        *float64
	Rows        *float64
	Body        string // raw dollar-quoted body text
}

// PartitionSpec describes a table's partition strategy.
type PartitionSpec struct {
	Strategy string   // "RANGE", "LIST", "HASH"
	Columns  []string // partitioning columns/expressions
}

// Partition is one partition entry.
type Partition struct {
	Name   string
	Bounds string // raw bounds expression
	SrcPos pipeline.SourcePos
}

// ── concrete IR object types ──────────────────────────────────────────────────

// Schema is a CREATE SCHEMA declaration.
type Schema struct {
	Name        string
	Owner       *string
	Comment     *string
	RenamedFrom *string
	SrcPos      pipeline.SourcePos
}

func (s *Schema) QualifiedName() string   { return s.Name }
func (s *Schema) Pos() pipeline.SourcePos { return s.SrcPos }
func (s *Schema) irObject()               {}

// Extension is a CREATE EXTENSION declaration.
type Extension struct {
	Name    string
	Schema  *string
	Version *string
	SrcPos  pipeline.SourcePos
}

func (e *Extension) QualifiedName() string   { return e.Name }
func (e *Extension) Pos() pipeline.SourcePos { return e.SrcPos }
func (e *Extension) irObject()               {}

// Table is a CREATE TABLE / UNLOGGED TABLE / FOREIGN TABLE declaration.
type Table struct {
	Schema        string
	Name          string
	RenamedFrom   *string
	Protected     bool
	Deprecated    *string
	DropCascade   bool
	Unlogged      bool
	Foreign       bool
	ForeignServer *string
	Owner         *string
	Comment       *string
	Columns       []*Column
	Constraints   []*Constraint
	Indexes       []*Index
	Policies      []*Policy
	Triggers      []*Trigger
	Grants        []Grant
	Revocations   []Revocation
	RLSEnabled    bool
	RLSForced     bool
	Inherits      []string
	PartitionBy   *PartitionSpec
	Partitions    []*Partition
	StorageParams map[string]string
	Tablespace    *string
	SrcPos        pipeline.SourcePos
}

func (t *Table) QualifiedName() string   { return qualName(t.Schema, t.Name) }
func (t *Table) Pos() pipeline.SourcePos { return t.SrcPos }
func (t *Table) irObject()               {}

// View is a CREATE [MATERIALIZED|RECURSIVE] VIEW declaration.
type View struct {
	Schema       string
	Name         string
	RenamedFrom  *string
	Materialized bool
	Recursive    bool
	Query        string // raw query text (opaque)
	Owner        *string
	Comment      *string
	Deprecated   *string
	Grants       []Grant
	Revocations  []Revocation
	WithNoData   bool // MATERIALIZED VIEW ... WITH NO DATA
	SrcPos       pipeline.SourcePos
}

func (v *View) QualifiedName() string   { return qualName(v.Schema, v.Name) }
func (v *View) Pos() pipeline.SourcePos { return v.SrcPos }
func (v *View) irObject()               {}

// Function is a CREATE FUNCTION declaration.
type Function struct {
	Schema      string
	Name        string
	Args        []FuncArg
	ReturnType  TypeRef
	Attrs       FuncAttrs
	BodyHash    string // SHA-256 of normalised body
	Comment     *string
	Deprecated  *string
	RenamedFrom *string
	Grants      []Grant
	SrcPos      pipeline.SourcePos
}

func (f *Function) QualifiedName() string {
	return fmt.Sprintf("%s(%s)", qualName(f.Schema, f.Name), ArgsKey(f.Args))
}
func (f *Function) Pos() pipeline.SourcePos { return f.SrcPos }
func (f *Function) irObject()               {}

// Procedure is a CREATE PROCEDURE declaration.
type Procedure struct {
	Schema   string
	Name     string
	Args     []FuncArg
	Attrs    FuncAttrs
	BodyHash string // SHA-256 of normalised body
	Comment  *string
	Grants   []Grant
	SrcPos   pipeline.SourcePos
}

func (p *Procedure) QualifiedName() string {
	return fmt.Sprintf("%s(%s)", qualName(p.Schema, p.Name), ArgsKey(p.Args))
}
func (p *Procedure) Pos() pipeline.SourcePos { return p.SrcPos }
func (p *Procedure) irObject()               {}

// Aggregate is a CREATE AGGREGATE declaration.
type Aggregate struct {
	Schema  string
	Name    string
	Args    []FuncArg
	Body    string // raw aggregate definition options text
	Comment *string
	Grants  []Grant
	SrcPos  pipeline.SourcePos
}

func (a *Aggregate) QualifiedName() string {
	return fmt.Sprintf("%s(%s)", qualName(a.Schema, a.Name), ArgsKey(a.Args))
}
func (a *Aggregate) Pos() pipeline.SourcePos { return a.SrcPos }
func (a *Aggregate) irObject()               {}

// Type covers ENUM, COMPOSITE, RANGE, DOMAIN, and BASE types.
type Type struct {
	Schema         string
	Name           string
	Variant        string    // "ENUM", "COMPOSITE", "RANGE", "DOMAIN", "BASE"
	EnumValues     []string  // ENUM only
	CompositeAttrs []*Column // COMPOSITE only: ordered list of attributes
	Body           string    // raw Part1 for range/domain/base (opaque for now)
	Comment        *string
	Owner          *string
	Deprecated     *string
	MigrateRemove  *pipeline.MigrateRemoveBlock // ENUM only: MIGRATE REMOVE { } block
	SrcPos         pipeline.SourcePos
}

func (t *Type) QualifiedName() string   { return qualName(t.Schema, t.Name) }
func (t *Type) Pos() pipeline.SourcePos { return t.SrcPos }
func (t *Type) irObject()               {}

// Sequence is a CREATE SEQUENCE declaration.
type Sequence struct {
	Schema  string
	Name    string
	Owner   *string
	Comment *string
	Grants  []Grant
	// Options (nil = use PostgreSQL default for that parameter)
	IncrementBy *int64
	MinValue    *int64
	MaxValue    *int64
	StartValue  *int64
	Cache       *int64
	Cycle       bool
	SrcPos      pipeline.SourcePos
}

func (s *Sequence) QualifiedName() string   { return qualName(s.Schema, s.Name) }
func (s *Sequence) Pos() pipeline.SourcePos { return s.SrcPos }
func (s *Sequence) irObject()               {}

// Role is a CREATE ROLE declaration.
type Role struct {
	Name    string
	Body    string // raw Part1 options text
	Comment *string
	SrcPos  pipeline.SourcePos
}

func (r *Role) QualifiedName() string   { return r.Name }
func (r *Role) Pos() pipeline.SourcePos { return r.SrcPos }
func (r *Role) irObject()               {}

// Tablespace is a CREATE TABLESPACE declaration.
type Tablespace struct {
	Name    string
	Body    string // raw Part1 text
	Comment *string
	SrcPos  pipeline.SourcePos
}

func (ts *Tablespace) QualifiedName() string   { return ts.Name }
func (ts *Tablespace) Pos() pipeline.SourcePos { return ts.SrcPos }
func (ts *Tablespace) irObject()               {}

// ForeignDataWrapper is a CREATE FOREIGN DATA WRAPPER declaration.
type ForeignDataWrapper struct {
	Name    string
	Body    string
	Comment *string
	SrcPos  pipeline.SourcePos
}

func (f *ForeignDataWrapper) QualifiedName() string   { return f.Name }
func (f *ForeignDataWrapper) Pos() pipeline.SourcePos { return f.SrcPos }
func (f *ForeignDataWrapper) irObject()               {}

// ForeignServer is a CREATE SERVER declaration.
type ForeignServer struct {
	Name    string
	Body    string
	Comment *string
	SrcPos  pipeline.SourcePos
}

func (f *ForeignServer) QualifiedName() string   { return f.Name }
func (f *ForeignServer) Pos() pipeline.SourcePos { return f.SrcPos }
func (f *ForeignServer) irObject()               {}

// UserMapping is a CREATE USER MAPPING declaration.
type UserMapping struct {
	User   string
	Server string
	Body   string
	SrcPos pipeline.SourcePos
}

func (u *UserMapping) QualifiedName() string   { return u.User + "@" + u.Server }
func (u *UserMapping) Pos() pipeline.SourcePos { return u.SrcPos }
func (u *UserMapping) irObject()               {}

// Publication is a CREATE PUBLICATION declaration.
type Publication struct {
	Name   string
	Body   string
	SrcPos pipeline.SourcePos
}

func (p *Publication) QualifiedName() string   { return p.Name }
func (p *Publication) Pos() pipeline.SourcePos { return p.SrcPos }
func (p *Publication) irObject()               {}

// Subscription is a CREATE SUBSCRIPTION declaration.
type Subscription struct {
	Name   string
	Body   string
	SrcPos pipeline.SourcePos
}

func (s *Subscription) QualifiedName() string   { return s.Name }
func (s *Subscription) Pos() pipeline.SourcePos { return s.SrcPos }
func (s *Subscription) irObject()               {}

// EventTrigger is a CREATE EVENT TRIGGER declaration.
type EventTrigger struct {
	Name   string
	Body   string
	SrcPos pipeline.SourcePos
}

func (e *EventTrigger) QualifiedName() string   { return e.Name }
func (e *EventTrigger) Pos() pipeline.SourcePos { return e.SrcPos }
func (e *EventTrigger) irObject()               {}

// Collation is a CREATE COLLATION declaration.
type Collation struct {
	Schema string
	Name   string
	Body   string
	SrcPos pipeline.SourcePos
}

func (c *Collation) QualifiedName() string   { return qualName(c.Schema, c.Name) }
func (c *Collation) Pos() pipeline.SourcePos { return c.SrcPos }
func (c *Collation) irObject()               {}

// Operator is a CREATE OPERATOR declaration.
type Operator struct {
	Schema string
	Name   string
	Body   string
	SrcPos pipeline.SourcePos
}

func (o *Operator) QualifiedName() string   { return qualName(o.Schema, o.Name) }
func (o *Operator) Pos() pipeline.SourcePos { return o.SrcPos }
func (o *Operator) irObject()               {}

// OperatorClass is a CREATE OPERATOR CLASS declaration.
type OperatorClass struct {
	Schema string
	Name   string
	Body   string
	SrcPos pipeline.SourcePos
}

func (o *OperatorClass) QualifiedName() string   { return qualName(o.Schema, o.Name) }
func (o *OperatorClass) Pos() pipeline.SourcePos { return o.SrcPos }
func (o *OperatorClass) irObject()               {}

// OperatorFamily is a CREATE OPERATOR FAMILY declaration.
type OperatorFamily struct {
	Schema string
	Name   string
	Body   string
	SrcPos pipeline.SourcePos
}

func (o *OperatorFamily) QualifiedName() string   { return qualName(o.Schema, o.Name) }
func (o *OperatorFamily) Pos() pipeline.SourcePos { return o.SrcPos }
func (o *OperatorFamily) irObject()               {}

// Cast is a CREATE CAST declaration.
type Cast struct {
	SourceType TypeRef
	TargetType TypeRef
	Body       string
	SrcPos     pipeline.SourcePos
}

func (c *Cast) QualifiedName() string   { return c.SourceType.String() + "->" + c.TargetType.String() }
func (c *Cast) Pos() pipeline.SourcePos { return c.SrcPos }
func (c *Cast) irObject()               {}

// StatisticsObject is a CREATE STATISTICS declaration.
type StatisticsObject struct {
	Schema string
	Name   string
	Body   string
	SrcPos pipeline.SourcePos
}

func (s *StatisticsObject) QualifiedName() string   { return qualName(s.Schema, s.Name) }
func (s *StatisticsObject) Pos() pipeline.SourcePos { return s.SrcPos }
func (s *StatisticsObject) irObject()               {}

// TSConfig is a CREATE TEXT SEARCH CONFIGURATION declaration.
type TSConfig struct {
	Schema   string
	Name     string
	Body     string
	Mappings []pipeline.TSMappingDef
	Comment  *string
	SrcPos   pipeline.SourcePos
}

func (t *TSConfig) QualifiedName() string   { return qualName(t.Schema, t.Name) }
func (t *TSConfig) Pos() pipeline.SourcePos { return t.SrcPos }
func (t *TSConfig) irObject()               {}

// TSDict is a CREATE TEXT SEARCH DICTIONARY declaration.
type TSDict struct {
	Schema  string
	Name    string
	Body    string
	Comment *string
	SrcPos  pipeline.SourcePos
}

func (t *TSDict) QualifiedName() string   { return qualName(t.Schema, t.Name) }
func (t *TSDict) Pos() pipeline.SourcePos { return t.SrcPos }
func (t *TSDict) irObject()               {}

// TSParser is a CREATE TEXT SEARCH PARSER declaration.
type TSParser struct {
	Schema string
	Name   string
	Body   string
	SrcPos pipeline.SourcePos
}

func (t *TSParser) QualifiedName() string   { return qualName(t.Schema, t.Name) }
func (t *TSParser) Pos() pipeline.SourcePos { return t.SrcPos }
func (t *TSParser) irObject()               {}

// TSTemplate is a CREATE TEXT SEARCH TEMPLATE declaration.
type TSTemplate struct {
	Schema string
	Name   string
	Body   string
	SrcPos pipeline.SourcePos
}

func (t *TSTemplate) QualifiedName() string   { return qualName(t.Schema, t.Name) }
func (t *TSTemplate) Pos() pipeline.SourcePos { return t.SrcPos }
func (t *TSTemplate) irObject()               {}

// ── VtypeBody — virtual type body DSL ────────────────────────────────────────

// VtypeBody is a discriminated union for the body of a VIRTUAL TYPE declaration.
// It is one of VtypeTypeRef, VtypeComposite, or VtypeUnion.
type VtypeBody interface{ vtypeBody() }

// VtypeTypeRef references a PostgreSQL built-in type or another declared
// VIRTUAL TYPE.  IsArray marks a [] suffix (used when assigning as a column
// type to get jsonb[] instead of jsonb).
type VtypeTypeRef struct {
	Schema  string // empty for unqualified references
	Name    string
	IsArray bool
}

func (r VtypeTypeRef) vtypeBody() {}

func (r VtypeTypeRef) String() string {
	s := qualName(r.Schema, r.Name)
	if r.IsArray {
		s += "[]"
	}
	return s
}

// VtypeField is a named field inside a VtypeComposite body.
type VtypeField struct {
	Name string
	Type VtypeTypeRef // field types are simple type references
}

// VtypeComposite is an inline record definition: (field1 TYPE1, field2 TYPE2, ...).
type VtypeComposite struct {
	Fields []VtypeField
}

func (c VtypeComposite) vtypeBody() {}

// VtypeUnion is a union of two or more VtypeBody terms joined with |.
type VtypeUnion struct {
	Members []VtypeBody // each member is VtypeComposite or VtypeTypeRef
}

func (u VtypeUnion) vtypeBody() {}

// ── VirtualType ───────────────────────────────────────────────────────────────

// VirtualType is a VIRTUAL TYPE declaration — a DPG-native construct that gives
// a structural schema to JSON/JSONB columns and JSON array columns.  It has no
// backing PostgreSQL DDL (no CREATE/ALTER/DROP TYPE is ever emitted).  Columns
// and composite type attributes may reference a virtual type directly; DPG
// resolves those references to jsonb / jsonb[] in generated SQL.  The structured
// body is stored in the snapshot for downstream consumers (ORMs, type-safe query
// builders) that read the DPG snapshot or IR via the pkg/dpg API.
type VirtualType struct {
	Schema     string
	Name       string
	Body       VtypeBody // structured body: VtypeTypeRef | VtypeComposite | VtypeUnion
	JsonFormat string    // "json" or "jsonb"; empty means default (jsonb)
	Comment    *string
	SrcPos     pipeline.SourcePos
}

func (v *VirtualType) QualifiedName() string   { return qualName(v.Schema, v.Name) }
func (v *VirtualType) Pos() pipeline.SourcePos { return v.SrcPos }
func (v *VirtualType) irObject()               {}

// DefaultPrivileges is a DEFAULT PRIVILEGES declaration.
type DefaultPrivileges struct {
	InSchema    *string
	ForRole     *string
	ObjectType  string
	Grants      []Grant
	Revocations []Revocation
	SrcPos      pipeline.SourcePos
}

func (d *DefaultPrivileges) QualifiedName() string {
	key := "DEFAULT PRIVILEGES"
	if d.ForRole != nil {
		key += " FOR " + *d.ForRole
	}
	if d.InSchema != nil {
		key += " IN " + *d.InSchema
	}
	return key
}
func (d *DefaultPrivileges) Pos() pipeline.SourcePos { return d.SrcPos }
func (d *DefaultPrivileges) irObject()               {}

// ── helpers ───────────────────────────────────────────────────────────────────

// ArgsKey returns a compact type-only argument key for use in qualified names
// and snapshot identity. OUT and TABLE params are excluded — PG's overload
// identity is based on IN and INOUT types only.
func ArgsKey(args []FuncArg) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if a.Mode == "OUT" || a.Mode == "TABLE" {
			continue
		}
		parts = append(parts, a.Type.String())
	}
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	return result
}

// Assert that all concrete types implement pipeline.IRObject.
var (
	_ pipeline.IRObject = (*Schema)(nil)
	_ pipeline.IRObject = (*Extension)(nil)
	_ pipeline.IRObject = (*Table)(nil)
	_ pipeline.IRObject = (*View)(nil)
	_ pipeline.IRObject = (*Function)(nil)
	_ pipeline.IRObject = (*Procedure)(nil)
	_ pipeline.IRObject = (*Aggregate)(nil)
	_ pipeline.IRObject = (*Type)(nil)
	_ pipeline.IRObject = (*Sequence)(nil)
	_ pipeline.IRObject = (*Role)(nil)
	_ pipeline.IRObject = (*Tablespace)(nil)
	_ pipeline.IRObject = (*ForeignDataWrapper)(nil)
	_ pipeline.IRObject = (*ForeignServer)(nil)
	_ pipeline.IRObject = (*UserMapping)(nil)
	_ pipeline.IRObject = (*Publication)(nil)
	_ pipeline.IRObject = (*Subscription)(nil)
	_ pipeline.IRObject = (*EventTrigger)(nil)
	_ pipeline.IRObject = (*Collation)(nil)
	_ pipeline.IRObject = (*Operator)(nil)
	_ pipeline.IRObject = (*OperatorClass)(nil)
	_ pipeline.IRObject = (*OperatorFamily)(nil)
	_ pipeline.IRObject = (*Cast)(nil)
	_ pipeline.IRObject = (*StatisticsObject)(nil)
	_ pipeline.IRObject = (*TSConfig)(nil)
	_ pipeline.IRObject = (*TSDict)(nil)
	_ pipeline.IRObject = (*TSParser)(nil)
	_ pipeline.IRObject = (*TSTemplate)(nil)
	_ pipeline.IRObject = (*DefaultPrivileges)(nil)
	_ pipeline.IRObject = (*VirtualType)(nil)
)
