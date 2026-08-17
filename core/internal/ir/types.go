// Package ir defines the fully-resolved Internal Representation of all DPG
// objects. Every IR value is fully qualified, schema-scoped, and source-annotated.
// IR types are produced by the IRBuilder (Phase 5) and consumed by the Merger,
// DependencyResolver, Differ, and Emitter.
package ir

import (
	"fmt"
	"strings"

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
	// SetOf is only meaningful on a Function's ReturnType (PostgreSQL's
	// RETURNS SETOF <type>) — pg_query's TypeName carries this field
	// regardless of context, but the grammar only ever sets it true when
	// parsing a function's return type, so every other TypeRef consumer
	// (columns, casts, function arguments) sees it as permanently false.
	SetOf bool
}

func (t TypeRef) String() string {
	s := qualName(t.Schema, t.Name)
	if t.Mods != "" {
		// PostgreSQL's "... with/without time zone" types take their
		// precision immediately after the base keyword, not at the end of
		// the full name — "timestamp(3) with time zone" / "timestamp(3)
		// without time zone", never "...time zone(3)" (the latter is a
		// syntax error; confirmed live via format_type() against a real
		// column, for both qualifiers). Every other modifier-bearing type
		// (numeric, character varying, plain bit/varbit) has no trailing
		// qualifier, so appending at the end is correct for them. Checked
		// in this order deliberately: " with time zone" never appears as a
		// substring of "...without time zone" (the word is "without", not
		// "with" + "out"), so there's no ambiguity, but without checks both
		// since either can be present depending on PGCatalogName's mapping.
		idx := strings.Index(s, " with time zone")
		if idx < 0 {
			idx = strings.Index(s, " without time zone")
		}
		if idx >= 0 {
			s = s[:idx] + t.Mods + s[idx:]
		} else {
			s += t.Mods
		}
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
	// Serial holds the SERIAL sugar kind ("SMALLSERIAL", "SERIAL", or
	// "BIGSERIAL") when this column was declared (or introspected as) one
	// of PostgreSQL's SERIAL pseudo-types. Type is always normalized to the
	// real underlying integer type (smallint/integer/bigint) — Serial is a
	// sibling marker, not an alternative to Type, mirroring how Identity is
	// a sibling marker to Type rather than a replacement for it. Default is
	// always nil when Serial is set (mirrors Identity's own Default==nil
	// convention): the auto-managed nextval() default is never represented
	// as an ir.Column scalar, on either the source-built or introspected
	// side, so the differ never has anything to compare there. NotNull is
	// always true when Serial is set (SERIAL implies NOT NULL in real
	// PostgreSQL, independent of any PRIMARY KEY). RFC §10: the backing
	// sequence itself is never declared in DPG and never appears as a
	// separate ir.Sequence object — introspectSequences's existing
	// deptype-'a'/'i'/'e' exclusion already (and correctly) excludes
	// SERIAL-owned sequences the same way it excludes IDENTITY-owned ones.
	Serial      *string
	Comment     *string
	Statistics  *int
	Compression *string
	Storage     *string
	// StorageIsTypeDefault is set only by introspection: true when Storage
	// equals the column's type's own default storage mode (pg_type.typstorage),
	// i.e. nobody ever explicitly overrode it. dump uses this to decide
	// whether rendering STORAGE would be a genuine declaration or just noise —
	// every real column has a concrete attstorage value, so Storage itself
	// can't tell "unset" apart from "set to the default" the way a nil
	// pointer normally would. Diffing is unaffected: diffColumns only acts
	// when the DESIRED side sets Storage, never based on this flag.
	StorageIsTypeDefault bool
	Deprecated           *string
	Using                *string // USING expression for ALTER COLUMN TYPE
	Grants               []Grant
	Revocations          []Revocation
	SecurityLabels       []pipeline.SecurityLabel
	NameMaps             []pipeline.NameMapEntry
	SrcPos               pipeline.SourcePos
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
	Name             string
	Unique           bool
	Method           string // "btree" (default), "hash", "gin", "gist", etc.
	Columns          []pipeline.IndexColumn
	Where            *string // partial index predicate
	Include          []string
	NullsNotDistinct bool // UNIQUE ... NULLS NOT DISTINCT
	With             []pipeline.StorageParam
	Tablespace       *string
	Concurrently     bool
	Comment          *string
	Pos              pipeline.SourcePos
}

// Constraint is a table or column constraint.
type Constraint struct {
	Name    string
	Type    string // "PRIMARY KEY", "UNIQUE", "CHECK", "FOREIGN KEY", "EXCLUDE"
	Expr    string // raw constraint expression/definition
	Columns []string
	// CheckColumn is the single column PostgreSQL's own auto-naming
	// algorithm considers this CHECK constraint's own (heap.c's
	// AddRelationNewConstraints: pull_var_clause over the expression, then
	// nil unless exactly one distinct column remains after dedup). Set only
	// when Type == "CHECK" and the expression references exactly one
	// distinct column; nil for zero or multiple. Populated by an AST walk,
	// not syntactic position, so it matches PG's real (expression-based)
	// behavior even for a promoted column-level CHECK that references other
	// columns too. Used solely to reconstruct PG's generated constraint name
	// (see pgAutoConstraintName in internal/diff) — unrelated to Columns,
	// which createTable uses for a different purpose (inline-rendering
	// promotion signal).
	CheckColumn *string
	// Exclude holds the structured EXCLUDE definition when Type == "EXCLUDE"
	// (nil otherwise). Expr still carries the fully rendered SQL text (built
	// from this struct — see renderExclude) for CREATE/ALTER TABLE emission
	// and dump rendering, the same way every other constraint type's Expr
	// works; Exclude exists alongside it because the exclusion elements and
	// operators can't be losslessly recovered by re-parsing Expr's text, and
	// the EXCLUDE-naming reconciliation (see pgConstraintNameLabel/
	// predictName's "excl" case in internal/diff) needs the element list
	// directly, not a re-parse of rendered SQL.
	Exclude *ExcludeSpec
	// RefSchema/RefTable/RefColumns hold the structured REFERENCES target
	// when Type == "FOREIGN KEY" (all zero otherwise) — structured siblings
	// of Expr, the same "keep the rendered SQL text AND the structured data
	// separately" shape Exclude already uses. RefSchema is "" when the
	// reference was written unqualified in source (resolves against the
	// referencing table's own schema, same convention as ir.TypeRef.Schema).
	// Populated straight from the already-parsed Pktable/PkAttrs AST data at
	// build time; exists so consumers like the deprecated-reference lint
	// rule don't need to re-parse Expr's rendered SQL text to recover the FK
	// target.
	RefSchema         string
	RefTable          string
	RefColumns        []string
	NotValid          bool
	Deferrable        bool
	InitiallyDeferred bool
	Comment           *string
	Pos               pipeline.SourcePos
}

// ExcludeSpec is the structured body of an EXCLUDE constraint: PostgreSQL's
// `EXCLUDE [USING access_method] (element WITH operator [, ...]) [WHERE (predicate)]`.
type ExcludeSpec struct {
	AccessMethod string           // index access method, e.g. "gist"; "" means PG's default (btree)
	Elements     []ExcludeElement // one per "element WITH operator" pair, in source order
	Where        string           // raw WHERE predicate text (no surrounding parens); "" if absent
}

// ExcludeElement is one column-or-expression + operator pair inside an
// EXCLUDE constraint's element list. Mirrors PostgreSQL's IndexElem grammar
// (the same production CREATE INDEX uses), plus the WITH operator EXCLUDE
// adds on top.
type ExcludeElement struct {
	Column string // plain column name; "" if Expr is set instead
	Expr   string // parenthesization-free expression text; "" if Column is set instead
	// PredictedName is set when Expr's shape lets PostgreSQL's own naming
	// rule (FigureColnameInternal, parser/parse_target.c — called via
	// FigureIndexColname from parse_utilcmd.c's transformIndexStmt BEFORE
	// transformExpr runs, confirmed live and via source that this is
	// genuinely syntax-only, no catalog/OID resolution involved) be
	// reconstructed purely from the parsed tree: a bare column reference in
	// parens ("(a)" -> "a"), a bare top-level function call ("lower(a)" ->
	// "lower", schema stripped, NEVER descending into the call's own
	// arguments regardless of their complexity — confirmed live), a
	// NULLIF(a,b) call (-> the literal "nullif"), or a type cast (-> its
	// argument's own PredictedName if that one is itself "strong" —
	// ColumnRef or FuncCall — else the cast's OWN target type name, e.g.
	// "(a + b)::text" -> "text", confirmed live). "" for any other
	// expression shape (a bare, uncast operator expression, e.g. "a + b" —
	// this syntax-only pass genuinely can't derive anything from it, matching
	// real PostgreSQL's own FigureIndexColname). This is NOT PostgreSQL's
	// final answer for such a shape, though: ChooseIndexColumnNames
	// (indexcmds.c) falls back to the literal string "expr" (deduplicated
	// like any other repeated element name) whenever indexcolname is unset —
	// confirmed live against PG 17 — so an empty PredictedName here is
	// deliberately picked up downstream by pgAutoConstraintName's "excl" case
	// (internal/diff), not left unpredictable. Used solely to reconstruct
	// PostgreSQL's own auto-generated constraint name — extracted directly
	// from the parsed tree, never by re-parsing Expr's rendered text.
	PredictedName string
	Collation     string // optional COLLATE target; "" if unspecified
	OpClass       string // optional operator class; "" if unspecified (uses the type's default)
	SortOrder     string // "ASC", "DESC", or "" (unspecified)
	Nulls         string // "FIRST", "LAST", or "" (unspecified)
	Operator      string // the WITH operator, e.g. "=", "&&"
}

// Policy is a row-security policy.
type Policy struct {
	Name       string
	Command    string // "ALL", "SELECT", etc.
	Permissive bool
	Using      *string
	WithCheck  *string
	Roles      []string
	Comment    *string
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
	Comment   *string
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

// Partition is one partition entry. PartitionBy/Partitions describe
// sub-partitioning (RFC §7.13): PartitionBy is nil for a leaf partition; when
// set, Partitions holds that partition's own nested partition entries, which
// may themselves be further sub-partitioned (arbitrary depth).
type Partition struct {
	Name        string
	Bounds      string // raw bounds expression
	PartitionBy *PartitionSpec
	Partitions  []*Partition
	SrcPos      pipeline.SourcePos
}

// ── concrete IR object types ──────────────────────────────────────────────────

// Schema is a CREATE SCHEMA declaration.
type Schema struct {
	Name           string
	Owner          *string
	Comment        *string
	RenamedFrom    *string
	Grants         []Grant
	Revocations    []Revocation
	SecurityLabels []pipeline.SecurityLabel
	NameMaps       []pipeline.NameMapEntry
	SrcPos         pipeline.SourcePos
}

func (s *Schema) QualifiedName() string   { return s.Name }
func (s *Schema) Pos() pipeline.SourcePos { return s.SrcPos }
func (s *Schema) irObject()               {}

// Extension is a CREATE EXTENSION declaration.
type Extension struct {
	Name     string
	Schema   *string
	Version  *string
	Comment  *string
	NameMaps []pipeline.NameMapEntry
	SrcPos   pipeline.SourcePos
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
	// ForeignOptions is FOREIGN TABLE's OPTIONS (...) clause — ordered like
	// Index.With, since it renders back into deterministic DPG source and
	// SQL text the same way.
	ForeignOptions []pipeline.StorageParam
	Owner          *string
	Comment        *string
	Columns        []*Column
	Constraints    []*Constraint
	Indexes        []*Index
	Policies       []*Policy
	Triggers       []*Trigger
	Grants         []Grant
	Revocations    []Revocation
	SecurityLabels []pipeline.SecurityLabel
	RLSEnabled     bool
	RLSForced      bool
	Inherits       []string
	PartitionBy    *PartitionSpec
	Partitions     []*Partition
	StorageParams  map[string]string
	Tablespace     *string
	NameMaps       []pipeline.NameMapEntry
	SrcPos         pipeline.SourcePos
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
	// Indexes is only meaningful when Materialized is true — real PostgreSQL
	// does not support indexes on a plain or recursive view, only on a
	// materialized view (or a table). RFC §8.2's matview-block grammar is
	// the only view-block variant that includes indices-block.
	Indexes        []*Index
	SecurityLabels []pipeline.SecurityLabel
	NameMaps       []pipeline.NameMapEntry
	SrcPos         pipeline.SourcePos
}

func (v *View) QualifiedName() string   { return qualName(v.Schema, v.Name) }
func (v *View) Pos() pipeline.SourcePos { return v.SrcPos }
func (v *View) irObject()               {}

// Function is a CREATE FUNCTION declaration.
type Function struct {
	Schema         string
	Name           string
	Args           []FuncArg
	ReturnType     TypeRef
	Attrs          FuncAttrs
	BodyHash       string // SHA-256 of normalised body
	Comment        *string
	Deprecated     *string
	RenamedFrom    *string
	Grants         []Grant
	Revocations    []Revocation
	SecurityLabels []pipeline.SecurityLabel
	NameMaps       []pipeline.NameMapEntry
	SrcPos         pipeline.SourcePos
}

func (f *Function) QualifiedName() string {
	return fmt.Sprintf("%s(%s)", qualName(f.Schema, f.Name), ArgsKey(f.Args))
}
func (f *Function) Pos() pipeline.SourcePos { return f.SrcPos }
func (f *Function) irObject()               {}

// Procedure is a CREATE PROCEDURE declaration.
type Procedure struct {
	Schema         string
	Name           string
	Args           []FuncArg
	Attrs          FuncAttrs
	BodyHash       string // SHA-256 of normalised body
	Comment        *string
	Grants         []Grant
	Revocations    []Revocation
	SecurityLabels []pipeline.SecurityLabel
	NameMaps       []pipeline.NameMapEntry
	SrcPos         pipeline.SourcePos
}

func (p *Procedure) QualifiedName() string {
	return fmt.Sprintf("%s(%s)", qualName(p.Schema, p.Name), ArgsKey(p.Args))
}
func (p *Procedure) Pos() pipeline.SourcePos { return p.SrcPos }
func (p *Procedure) irObject()               {}

// Aggregate is a CREATE AGGREGATE declaration.
type Aggregate struct {
	Schema string
	Name   string
	Args   []FuncArg
	Body   string // full "CREATE AGGREGATE ..." statement text, used directly for SQL emission
	// Options is the same SFUNC/STYPE/INITCOND/... key=value list as Body,
	// kept structured (and in source order) so dump can reconstruct the DPG
	// "AGGREGATE name (args) (options)" declaration without re-parsing Body.
	Options        []pipeline.StorageParam
	Comment        *string
	Grants         []Grant
	Revocations    []Revocation
	SecurityLabels []pipeline.SecurityLabel
	NameMaps       []pipeline.NameMapEntry
	SrcPos         pipeline.SourcePos
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
	Body           string    // raw Part1 for range/base (opaque); DOMAIN's full CREATE DOMAIN text, used only for a base-type change (DROP+CREATE)
	Reconstructed  bool      // Body rebuilt from the catalog; see Tablespace.Reconstructed. RANGE/BASE only.
	Comment        *string
	Owner          *string
	Deprecated     *string
	MigrateRemove  *pipeline.MigrateRemoveBlock // ENUM only: MIGRATE REMOVE { } block
	NameMaps       []pipeline.NameMapEntry
	// DomainBaseType/DomainDefault/DomainNotNull/DomainConstraints (DOMAIN
	// only) are RFC §5.4's structured domain diffing inputs — a domain is
	// NOT purely opaque like RANGE/BASE despite sharing the Body field, so
	// property-level changes (DEFAULT, NOT NULL, individual CHECK
	// constraints) can each get their own targeted ALTER DOMAIN op instead
	// of an unconditional DROP+CREATE. Populated from whichever source the
	// user wrote: real PG's own inline "CREATE DOMAIN name AS type DEFAULT
	// expr CONSTRAINT c CHECK (...)" syntax (parsed from Part 1 via
	// pg_query), RFC §5.4's block-based "{ DEFAULT expr; CONSTRAINT c
	// CHECK (...); }" form, or both merged together.
	DomainBaseType    TypeRef
	DomainDefault     *string
	DomainNotNull     bool
	DomainConstraints []*Constraint
	SecurityLabels    []pipeline.SecurityLabel
	SrcPos            pipeline.SourcePos
}

func (t *Type) QualifiedName() string   { return qualName(t.Schema, t.Name) }
func (t *Type) Pos() pipeline.SourcePos { return t.SrcPos }
func (t *Type) irObject()               {}

// Sequence is a CREATE SEQUENCE declaration.
type Sequence struct {
	Schema         string
	Name           string
	Owner          *string
	Comment        *string
	Grants         []Grant
	SecurityLabels []pipeline.SecurityLabel
	NameMaps       []pipeline.NameMapEntry
	// Options (nil = use PostgreSQL default for that parameter)
	IncrementBy *int64
	MinValue    *int64
	MaxValue    *int64
	StartValue  *int64
	Cache       *int64
	// Cycle is nil when the source didn't write CYCLE or NO CYCLE at all
	// (unlike the other options above, false is a real, explicit value:
	// NO CYCLE), so it needs the same nil-means-unspecified treatment.
	Cycle  *bool
	SrcPos pipeline.SourcePos
}

func (s *Sequence) QualifiedName() string   { return qualName(s.Schema, s.Name) }
func (s *Sequence) Pos() pipeline.SourcePos { return s.SrcPos }
func (s *Sequence) irObject()               {}

// Role is a CREATE ROLE declaration (RFC §11.1). Every attribute is native
// PostgreSQL CREATE ROLE/ALTER ROLE grammar, extracted from
// CreateRoleStmt.Options — not a DPG-invented block directive.
//
// Every pointer/nil-slice field means "not declared, not managed by DPG for
// this role" — offline diffing only ever compares what source explicitly
// sets, never PostgreSQL's own default for an omitted option (mirrors
// Sequence's optional-param convention).
//
// Password is the raw declared PASSWORD text, verbatim — a literal, or one
// containing {{<secret-uri>}} placeholders resolved only at apply time (see
// pipeline.ResolveTemplate), same mechanism as Subscription.ConnInfo (§13.2).
// Never the resolved value.
type Role struct {
	Name            string
	CanLogin        *bool
	Superuser       *bool
	CreateDB        *bool
	CreateRole      *bool
	Inherit         *bool
	IsReplication   *bool
	BypassRLS       *bool
	ConnectionLimit *int
	Password        *string
	ValidUntil      *string
	InRole          []string // IN ROLE role-list: this role becomes a member of these
	RoleMembers     []string // ROLE role-list: these become members of this role
	AdminRoles      []string // ADMIN role-list: these become members of this role, WITH ADMIN OPTION
	Comment         *string
	SecurityLabels  []pipeline.SecurityLabel
	NameMaps        []pipeline.NameMapEntry
	SrcPos          pipeline.SourcePos
}

func (r *Role) QualifiedName() string   { return r.Name }
func (r *Role) Pos() pipeline.SourcePos { return r.SrcPos }
func (r *Role) irObject()               {}

// Tablespace is a CREATE TABLESPACE declaration.
type Tablespace struct {
	Name string
	// Location is RFC §14.7's structured diffing input: LOCATION cannot be
	// changed after creation in real PostgreSQL, so a Location mismatch is
	// what actually decides DROP+CREATE — previously only Body's opaque
	// hash decided this, which (via Reconstructed, below) went silently
	// unset on every live path, missing any live-catalog LOCATION change.
	Location string
	Body     string // raw Part1 text
	Comment  *string
	// Reconstructed marks a Body rebuilt from the live catalog by the
	// introspector (as opposed to parsed from source). Reconstructed bodies are
	// canonical but not byte-identical to hand-written source, so the snapshot
	// omits their body hash to avoid spurious text-diff drift. See sourceBodyHash.
	Reconstructed  bool
	SecurityLabels []pipeline.SecurityLabel
	SrcPos         pipeline.SourcePos
}

func (ts *Tablespace) QualifiedName() string   { return ts.Name }
func (ts *Tablespace) Pos() pipeline.SourcePos { return ts.SrcPos }
func (ts *Tablespace) irObject()               {}

// ForeignDataWrapper is a CREATE FOREIGN DATA WRAPPER declaration.
type ForeignDataWrapper struct {
	Name string
	// Handler/Validator/Options are RFC §14.8's structured diffing inputs:
	// "any change to a FDW requires drop + recreate" is the RFC's own
	// documented semantics (no ALTER FOREIGN DATA WRAPPER path, even
	// though real PostgreSQL has one — a deliberate DPG simplification),
	// so this drives a single "did anything change" comparison rather
	// than field-level ALTER clauses. Previously only Body's opaque hash
	// decided this, which (via Reconstructed, below) went silently unset
	// on every live path, missing any live-catalog change to a FDW's
	// definition. Handler/Validator are "" for NO HANDLER/NO VALIDATOR or
	// when omitted (both mean the same thing to PostgreSQL).
	Handler       string
	Validator     string
	Options       []pipeline.StorageParam
	Body          string
	Comment       *string
	Reconstructed bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SrcPos        pipeline.SourcePos
}

func (f *ForeignDataWrapper) QualifiedName() string   { return f.Name }
func (f *ForeignDataWrapper) Pos() pipeline.SourcePos { return f.SrcPos }
func (f *ForeignDataWrapper) irObject()               {}

// ForeignServer is a CREATE SERVER declaration.
type ForeignServer struct {
	Name string
	// FDWName/Type/Options are RFC §14.9's structured diffing inputs: a
	// FDW-wrapper change or a TYPE change (real PostgreSQL has no
	// ALTER SERVER ... TYPE) decides DROP+CREATE, same reasoning as
	// ForeignDataWrapper.Handler/Validator/Options above; a VERSION or
	// OPTIONS change gets a real, targeted ALTER SERVER per RFC §14.9's
	// own diffing table ("OPTIONS changed" -> SAFE). Previously only
	// Body's opaque hash decided any of this, which (via Reconstructed,
	// below) went silently unset on every live path.
	FDWName       string
	Type          *string
	Version       *string
	Options       []pipeline.StorageParam
	Body          string
	Comment       *string
	Reconstructed bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SrcPos        pipeline.SourcePos
}

func (f *ForeignServer) QualifiedName() string   { return f.Name }
func (f *ForeignServer) Pos() pipeline.SourcePos { return f.SrcPos }
func (f *ForeignServer) irObject()               {}

// UserMapping is a CREATE USER MAPPING declaration.
type UserMapping struct {
	User   string
	Server string
	// Options is RFC §14.10's structured diffing input: "any change to
	// the mapping is a full DROP USER MAPPING + CREATE USER MAPPING, not
	// a targeted ALTER USER MAPPING" is the RFC's own explicit,
	// deliberately-corrected semantics (§14.10's text notes an earlier
	// draft described a targeted ALTER that was never implemented), so —
	// same as ForeignDataWrapper — this drives a single "did anything
	// change" comparison, not field-level ALTER clauses. Previously only
	// Body's opaque hash decided this, which (via Reconstructed, below)
	// went silently unset on every live path. Password-like keys are
	// deliberately excluded from the diff comparison (see
	// introspect.UserMappingRedactedPlaceholder): the live side never
	// exposes the real value, only a fixed redaction placeholder, so
	// comparing it against the desired side's real declared value would
	// otherwise show permanent, spurious drift on every plan.
	Options       []pipeline.StorageParam
	Body          string
	Reconstructed bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SrcPos        pipeline.SourcePos
}

func (u *UserMapping) QualifiedName() string   { return u.User + "@" + u.Server }
func (u *UserMapping) Pos() pipeline.SourcePos { return u.SrcPos }
func (u *UserMapping) irObject()               {}

// PublicationTableRef is one FOR TABLE target of a CREATE PUBLICATION
// declaration, used only to compute dependency-graph ordering (graph.go) —
// Publication itself stays opaque (Body carries the full statement text) for
// diff/snapshot/dump purposes, same as every other reconstruction-tier kind.
type PublicationTableRef struct {
	Schema string // empty for an unqualified reference
	Name   string
}

// Publication is a CREATE PUBLICATION declaration.
type Publication struct {
	Name string
	Body string
	// Comment is diffed/emitted independently of Body (COMMENT ON
	// PUBLICATION), same as every other Comment-bearing opaque kind —
	// confirmed live via \h COMMENT that real PostgreSQL genuinely supports
	// this, despite Publication being excluded from the original 14-kind
	// Comment/Grant fix on the mistaken assumption that it didn't apply.
	Comment *string
	// Tables is the FOR TABLE target list (PUBLICATIONOBJ_TABLE entries
	// only — FOR TABLES IN SCHEMA/FOR ALL TABLES have no single fixed table
	// to order against). See PublicationTableRef.
	Tables []PublicationTableRef
	// AllTables/Insert/Update/Delete/Truncate are RFC §13.1's structured
	// diffing inputs, alongside Tables above: a FOR ALL TABLES publication
	// can never be converted to/from an explicit table list via ALTER
	// (confirmed live against a real PostgreSQL 17 server: "Tables cannot
	// be added to or dropped from FOR ALL TABLES publications"), so an
	// AllTables change decides DROP+CREATE, while a Tables or WITH
	// (publish = ...) change each get their own real, targeted
	// ALTER PUBLICATION (RFC §13.1's own diffing table: both rows say
	// SAFE). Insert/Update/Delete/Truncate always hold PostgreSQL's
	// concrete resolved value (true for all four when WITH (publish = ...)
	// is omitted — PostgreSQL's own default, not a DPG-invented one) on
	// both sides, so no nil/unset tri-state is needed. Previously only
	// Body's opaque hash decided any of this, which (via Reconstructed,
	// below) went silently unset on every live path. FOR TABLES IN SCHEMA
	// targets are a pre-existing, separate gap: never modeled as a
	// structured field on either side (only captured in the opaque Body
	// text), so a schema-target-only change stays undetected by this
	// comparison — the same status quo as before this fix, not a
	// regression it introduces.
	AllTables                        bool
	Insert, Update, Delete, Truncate bool
	// HasFilteredTables is true when any FOR TABLE entry carries an
	// explicit column list or WHERE row-filter (real PostgreSQL syntax:
	// FOR TABLE t (col1, col2) WHERE (expr)) — neither is captured by
	// PublicationTableRef (schema/name only), so a Tables-set change on a
	// publication using either can't be safely rendered as a targeted
	// ALTER PUBLICATION ... SET TABLE: doing so from PublicationTableRef
	// alone would silently rebuild the table list WITHOUT the original
	// column-list/WHERE filter, an unintentional narrowing (or removal)
	// of what's actually replicated — confirmed live: pg_publication_rel.
	// prattrs/prqual are non-NULL exactly when a filter was written,
	// distinguishing this from the implicit "all columns" case (which
	// pg_publication_tables.attnames can't distinguish, since it always
	// resolves to concrete column names either way). When true on either
	// side, diffPublication falls back to DROP+CREATE for a Tables
	// change instead of the lossy ALTER — full correctness at the cost of
	// forgoing the optimization for filtered publications specifically;
	// unfiltered publications (the common case) still get the real
	// ALTER PUBLICATION ... SET TABLE this fix adds.
	HasFilteredTables bool
	Reconstructed     bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SecurityLabels    []pipeline.SecurityLabel
	SrcPos            pipeline.SourcePos
}

func (p *Publication) QualifiedName() string   { return p.Name }
func (p *Publication) Pos() pipeline.SourcePos { return p.SrcPos }
func (p *Publication) irObject()               {}

// Subscription is a CREATE SUBSCRIPTION declaration.
//
// ConnInfo is the native CONNECTION '...' literal, verbatim — an ordinary
// libpq conninfo string, resolved at apply time via pipeline.ResolveTemplate:
// plain text is used as-is, and any {{<secret-uri>}} placeholders within it
// (the whole value, or just a fragment like the password) are substituted.
// {{...}} is the only way a secret reference is ever recognized — a real
// conninfo/DSN literal may itself contain a ':' (e.g. a postgresql:// URI),
// so nothing else triggers resolution.
type Subscription struct {
	Name           string
	ConnInfo       string
	Body           string
	Comment        *string
	Reconstructed  bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SecurityLabels []pipeline.SecurityLabel
	SrcPos         pipeline.SourcePos
}

func (s *Subscription) QualifiedName() string   { return s.Name }
func (s *Subscription) Pos() pipeline.SourcePos { return s.SrcPos }
func (s *Subscription) irObject()               {}

// EventTrigger is a CREATE EVENT TRIGGER declaration.
type EventTrigger struct {
	Name string
	// Event/Tags are RFC §14.1's structured diffing inputs, alongside
	// Function below: PostgreSQL has no ALTER EVENT TRIGGER for any of
	// these three (only ENABLE/DISABLE/OWNER TO/RENAME TO, none modeled
	// here), so any change to Event, Tags, or Function decides DROP+CREATE
	// — previously only Body's opaque hash decided this, which (via
	// Reconstructed, below) went silently unset on every live path,
	// missing any live-catalog change to an event trigger's definition.
	Event string
	Tags  []string
	// Function is the qualified name of the EXECUTE FUNCTION target, extracted
	// separately from Body for the dependency graph — an event trigger created
	// before the function it calls exists fails at apply time (confirmed
	// live: "function ... does not exist"), and nothing else in the IR
	// records this reference since EventTrigger is otherwise fully opaque.
	Function string
	// Comment is diffed/emitted independently of Body (COMMENT ON EVENT
	// TRIGGER), same as every other Comment-bearing opaque kind — added
	// alongside Cast/Operator/OperatorClass/OperatorFamily/Collation/
	// StatisticsObject/TSParser/TSTemplate, found live-testing a demo
	// project: PostgreSQL genuinely supports COMMENT ON for all of these
	// (confirmed via \h COMMENT against a real server), but the blockparser's
	// generic { COMMENT '...'; } was silently discarded for every one of
	// them — no field existed to store it, so dpg plan reported
	// "-- (no changes)" with no error and no effect.
	Comment        *string
	Body           string
	Reconstructed  bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SecurityLabels []pipeline.SecurityLabel
	SrcPos         pipeline.SourcePos
}

func (e *EventTrigger) QualifiedName() string   { return e.Name }
func (e *EventTrigger) Pos() pipeline.SourcePos { return e.SrcPos }
func (e *EventTrigger) irObject()               {}

// Collation is a CREATE COLLATION declaration.
type Collation struct {
	Schema string
	Name   string
	// Provider/Collate/Ctype/ICULocale/Deterministic are RFC §14.2's
	// structured diffing inputs: "any property change requires DROP
	// COLLATION + CREATE COLLATION" (real PostgreSQL's ALTER COLLATION
	// supports only REFRESH VERSION/OWNER TO/RENAME TO/SET SCHEMA, none
	// of these five), so this is a single "did anything change"
	// comparison, same shape as ForeignDataWrapper. Comparing these
	// resolved fields directly — rather than the LOCALE-vs-LC_COLLATE/
	// LC_CTYPE shorthand text — is what actually closes the gap
	// convert.go's sourceBodyHash doc comment names as the reason
	// Collation needed Reconstructed's hash-exclusion in the first place:
	// PostgreSQL always resolves LOCALE into concrete collcollate/
	// collctype (libc/default/builtin providers) or colllocale (icu)
	// values regardless of which shorthand the source used (confirmed
	// live against a real PostgreSQL 17 server), so comparing the
	// resolved values sidesteps the shorthand-equivalence problem
	// entirely rather than needing a text-normalization function.
	// Provider is PostgreSQL's own single-letter catalog code ("c" =
	// libc, the default when PROVIDER is omitted; "i" = icu; "b" =
	// builtin). Deterministic defaults true (PostgreSQL's own default
	// when DETERMINISTIC is omitted), not a DPG-invented default.
	Provider       string
	Collate, Ctype *string
	ICULocale      *string
	Deterministic  bool
	Comment        *string // see EventTrigger.Comment
	Body           string
	Reconstructed  bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SrcPos         pipeline.SourcePos
}

func (c *Collation) QualifiedName() string   { return qualName(c.Schema, c.Name) }
func (c *Collation) Pos() pipeline.SourcePos { return c.SrcPos }
func (c *Collation) irObject()               {}

// Operator is a CREATE OPERATOR declaration.
type Operator struct {
	Schema string
	Name   string
	// LeftType/RightType are the operator's operand types (nil for the side
	// a unary/prefix operator omits — PostgreSQL requires RightType in
	// practice since postfix operators were removed in PG14, but this
	// mirrors the grammar's actual optionality rather than assuming it).
	LeftType  *TypeRef
	RightType *TypeRef
	// Function is the qualified name of the PROCEDURE target, extracted
	// separately from Body for the dependency graph — same reasoning as
	// EventTrigger.Function/Cast.Function: an operator created before its
	// function exists fails at apply time ("function ... does not exist"),
	// and nothing else in the IR records this reference since Operator is
	// otherwise fully opaque.
	Function      string
	Comment       *string // see EventTrigger.Comment
	Body          string
	Reconstructed bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SrcPos        pipeline.SourcePos
}

// QualifiedName includes the operand types because PostgreSQL identifies an
// operator by (schema, name, lefttype, righttype) — the same symbol can be
// overloaded across operand types (e.g. integer + integer vs numeric +
// numeric) — so the flat, name-keyed snapshot and diff maps must key on all
// of it or two distinct operators would silently collide, one overwriting
// the other (the same class of bug OperatorClass/OperatorFamily had before
// their QualifiedName was widened for the same reason). The format mirrors
// PostgreSQL's own DROP OPERATOR operand-list syntax verbatim (including the
// literal NONE for a missing side) so OperandsKey doubles as both the
// identity suffix here and dropObject's DROP OPERATOR argument list.
func (o *Operator) QualifiedName() string {
	return qualName(o.Schema, o.Name) + "(" + OperandsKey(o.LeftType, o.RightType) + ")"
}
func (o *Operator) Pos() pipeline.SourcePos { return o.SrcPos }
func (o *Operator) irObject()               {}

// OperandsKey renders an operator's operand types as PostgreSQL's own DROP
// OPERATOR syntax requires them: "lefttype, righttype", substituting the
// literal NONE for whichever side a unary operator omits.
func OperandsKey(left, right *TypeRef) string {
	l, r := "NONE", "NONE"
	if left != nil {
		l = left.String()
	}
	if right != nil {
		r = right.String()
	}
	return l + ", " + r
}

// OperatorClass is a CREATE OPERATOR CLASS declaration.
type OperatorClass struct {
	Schema       string
	Name         string
	AccessMethod string // the index access method (btree/gin/gist/...); needed for DROP
	// FamilyName, when non-empty, is the class's explicit FAMILY clause target
	// (FamilySchema empty means unqualified, i.e. the class's own schema) —
	// parsed identically from hand-written source (CreateOpClassStmt.Opfamilyname)
	// and introspection. Populated purely for dependency-edge ordering (see
	// graph.go): the FAMILY clause text itself always lives in Body, since
	// introspection now always emits it explicitly (pg_dump's model — no
	// "implicit same-name family" special case). Empty FamilyName is only
	// possible for hand-written source that omits FAMILY, relying on
	// PostgreSQL's own same-name auto-creation.
	FamilySchema string
	FamilyName   string
	// Functions holds the qualified names of every FUNCTION support-item
	// target in the class body (a class can declare several, numbered per
	// support-function slot — e.g. FUNCTION 1/2/3/4 for btree). Populated
	// purely for dependency-edge ordering, same reasoning as
	// EventTrigger.Function/Cast.Function/Operator.Function: a class
	// created before one of its support functions exists fails at apply
	// time, and nothing else in the IR records these references since the
	// class is otherwise fully opaque.
	Functions []string
	// Members and StorageType are the AS-list's OPERATOR/FUNCTION items and
	// optional STORAGE clause, captured structurally (previously the builder
	// captured only FUNCTION items, and only as bare names via Functions
	// above — OPERATOR and STORAGETYPE items were silently dropped, while
	// introspection captured all three but only as flattened clause text
	// folded into Body, never structurally — an asymmetry that left the same
	// class represented inconsistently depending on which path produced it).
	// Diffing still can't be incremental the way OperatorFamily's loose
	// members are (PostgreSQL has no ALTER OPERATOR CLASS ADD/DROP at all —
	// RFC §14.4), so Body remains the source of truth for DROP+CREATE SQL;
	// Members/StorageType exist so the differ can compare structurally
	// instead of on raw BodyHash, avoiding a false DESTRUCTIVE diff when
	// hand-written source and an introspected reconstruction differ only in
	// cosmetic text (whitespace, operator qualification, type spelling).
	Members       []pipeline.OpFamilyMember
	StorageType   string  // STORAGE clause's type name; "" if the class declares none
	Comment       *string // see EventTrigger.Comment
	Body          string
	Reconstructed bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SrcPos        pipeline.SourcePos
}

// QualifiedName includes the access method because operator class names are
// unique only within a method, so two same-named classes under different methods
// must key distinctly in the flat, name-keyed snapshot and diff maps. See
// OperatorFamily.QualifiedName for why classes and families must not collide.
func (o *OperatorClass) QualifiedName() string {
	return qualName(o.Schema, o.Name) + " USING " + o.AccessMethod
}
func (o *OperatorClass) Pos() pipeline.SourcePos { return o.SrcPos }
func (o *OperatorClass) irObject()               {}

// OperatorFamily is a CREATE OPERATOR FAMILY declaration.
type OperatorFamily struct {
	Schema        string
	Name          string
	AccessMethod  string  // the index access method (btree/gin/gist/...); needed for DROP
	Comment       *string // see EventTrigger.Comment
	Body          string
	Reconstructed bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	// Members are the family's "loose" members — the ones real PG only lets
	// you attach via ALTER OPERATOR FAMILY ... ADD, i.e. those that belong to
	// the family directly rather than to one of its operator classes'
	// AS-lists (RFC §14.4). Declared in the DPG { } block (Part 2), so Part 1
	// stays exactly the bare, valid CREATE OPERATOR FAMILY statement
	// pgparser/reconstruct.go's "CREATE OPERATOR FAMILY " prefix assumes.
	// Populated for both source-declared and introspected/reconstructed
	// families alike (unlike Body, never gated on Reconstructed) — diffed
	// structurally and incrementally (diff.diffOpFamilyMembers), unlike
	// OperatorClass's AS-list, which stays whole-body passthrough because
	// PostgreSQL genuinely offers no incremental opclass member DDL.
	Members []pipeline.OpFamilyMember
	SrcPos  pipeline.SourcePos
}

// QualifiedName carries a trailing " FAMILY" so an operator family never
// collides with a same-named operator class in the flat, name-keyed snapshot and
// diff maps — PostgreSQL auto-creates a same-named family for every class, so the
// collision is the common case, not an edge one. The access method is included
// for the same reason as OperatorClass: names are unique only per method.
func (o *OperatorFamily) QualifiedName() string {
	return qualName(o.Schema, o.Name) + " USING " + o.AccessMethod + " FAMILY"
}
func (o *OperatorFamily) Pos() pipeline.SourcePos { return o.SrcPos }
func (o *OperatorFamily) irObject()               {}

// Cast is a CREATE CAST declaration.
type Cast struct {
	SourceType TypeRef
	TargetType TypeRef
	// Method/Context are RFC §14.5's structured diffing inputs, alongside
	// Function below: PostgreSQL provides no ALTER CAST at all, so any
	// change to Method, Function, or Context decides DROP+CREATE —
	// previously only Body's opaque hash decided this, which (via
	// Reconstructed, below) went silently unset on every live path,
	// missing any live-catalog change to a cast's definition. Values use
	// the same single-letter catalog vocabulary as pg_cast.castmethod
	// ("f" = WITH FUNCTION, "i" = WITH INOUT, "b" = WITHOUT FUNCTION/
	// binary-coercible) and pg_cast.castcontext ("e" = no AS clause/
	// explicit-only — PostgreSQL's grammar has no "AS EXPLICIT" — "a" =
	// AS ASSIGNMENT, "i" = AS IMPLICIT), so both sides (source-parsed and
	// introspected) always populate the same three-value vocabulary with
	// no translation needed.
	Method  string
	Context string
	// Function is the qualified name of the WITH FUNCTION target, extracted
	// separately from Body for the dependency graph — empty for WITHOUT
	// FUNCTION/WITH INOUT casts, which have no function to order against.
	// Same reasoning as EventTrigger.Function: a cast created before its
	// function exists fails at apply time ("function ... does not exist"),
	// and nothing else in the IR records this reference since Cast is
	// otherwise fully opaque.
	Function      string
	Comment       *string // see EventTrigger.Comment
	Body          string
	Reconstructed bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SrcPos        pipeline.SourcePos
}

func (c *Cast) QualifiedName() string   { return c.SourceType.String() + "->" + c.TargetType.String() }
func (c *Cast) Pos() pipeline.SourcePos { return c.SrcPos }
func (c *Cast) irObject()               {}

// StatisticsObject is a CREATE STATISTICS declaration.
type StatisticsObject struct {
	Schema string
	Name   string
	// Table/Kinds/Columns/StatisticsTarget are RFC §14.6's structured
	// diffing inputs: real PostgreSQL's ALTER STATISTICS supports only
	// OWNER TO/RENAME TO/SET SCHEMA/SET STATISTICS (confirmed live via
	// `\h ALTER STATISTICS` against a real PostgreSQL 17 server), so a
	// Table, Kinds, or Columns change decides DROP+CREATE (RFC's own
	// "Column list or kinds changed" row, DESTRUCTIVE — Table isn't
	// separately named in the RFC's table but requires the identical
	// treatment, since there's no ALTER STATISTICS ... FROM either), while
	// a StatisticsTarget-only change gets a real, targeted
	// ALTER STATISTICS ... SET STATISTICS (RFC: SAFE). Kinds uses DPG's
	// own source spelling ("ndistinct"/"dependencies"/"mcv"), converted
	// to/from PostgreSQL's single-letter stxkind codes ('d'/'f'/'m') at
	// the introspection/builder boundary — pg_statistic_ext's 'e' code
	// (has-expressions) is an internal marker PostgreSQL adds
	// automatically, never a user-requested kind, and is excluded.
	// Columns holds both plain column names and expression text exactly
	// as pg_get_statisticsobjdef_expressions renders it (canonical
	// deparse form, confirmed live) — the builder canonicalizes
	// expressions the same way (via nodeToText's existing deparse
	// wrapping) so both sides compare equal regardless of source
	// formatting. Both Kinds and Columns are compared as sets
	// (order-independent). StatisticsTarget is nil when unset (matches
	// Column.Statistics' existing *int nil-means-default precedent) —
	// pg_statistic_ext.stxstattarget is genuinely NULL in this case,
	// confirmed live, not a sentinel value.
	Table            string
	Kinds            []string
	Columns          []string
	StatisticsTarget *int
	Comment          *string // see EventTrigger.Comment
	Body             string
	Reconstructed    bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SrcPos           pipeline.SourcePos
}

func (s *StatisticsObject) QualifiedName() string   { return qualName(s.Schema, s.Name) }
func (s *StatisticsObject) Pos() pipeline.SourcePos { return s.SrcPos }
func (s *StatisticsObject) irObject()               {}

// TSConfig is a CREATE TEXT SEARCH CONFIGURATION declaration.
type TSConfig struct {
	Schema string
	Name   string
	// ParserName is always non-empty for valid source/introspection — PARSER
	// is mandatory in CREATE TEXT SEARCH CONFIGURATION's grammar, unlike
	// OperatorClass's optional FAMILY. Populated purely for dependency-edge
	// ordering (see graph.go); the PARSER clause text itself always lives in
	// Body already. ParserSchema empty means unqualified (the config's own
	// schema).
	ParserSchema  string
	ParserName    string
	Body          string
	Mappings      []pipeline.TSMappingDef
	Comment       *string
	Reconstructed bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SrcPos        pipeline.SourcePos
}

func (t *TSConfig) QualifiedName() string   { return qualName(t.Schema, t.Name) }
func (t *TSConfig) Pos() pipeline.SourcePos { return t.SrcPos }
func (t *TSConfig) irObject()               {}

// TSDict is a CREATE TEXT SEARCH DICTIONARY declaration.
type TSDict struct {
	Schema string
	Name   string
	// TemplateName is always non-empty for valid source/introspection —
	// TEMPLATE is mandatory in CREATE TEXT SEARCH DICTIONARY's grammar.
	// Populated purely for dependency-edge ordering (see graph.go); the
	// TEMPLATE clause text itself always lives in Body already.
	// TemplateSchema empty means unqualified (the dict's own schema).
	TemplateSchema string
	TemplateName   string
	Body           string
	Comment        *string
	Reconstructed  bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SrcPos         pipeline.SourcePos
}

func (t *TSDict) QualifiedName() string   { return qualName(t.Schema, t.Name) }
func (t *TSDict) Pos() pipeline.SourcePos { return t.SrcPos }
func (t *TSDict) irObject()               {}

// TSParser is a CREATE TEXT SEARCH PARSER declaration.
type TSParser struct {
	Schema string
	Name   string
	// Functions holds the qualified names of the parser's START/GETTOKEN/
	// END/LEXTYPES/HEADLINE support functions. Populated purely for
	// dependency-edge ordering (see graph.go) — same reasoning as
	// OperatorClass.Functions: a parser created before one of its support
	// functions exists fails at apply time, and nothing else in the IR
	// records these references since the parser is otherwise fully opaque.
	// In practice these almost always name a pg_catalog built-in (custom
	// ones require C, since the required internal return types can't be
	// produced from SQL/PLpgSQL) — never errors when unresolved, same as
	// every other soft reference in this file.
	Functions     []string
	Comment       *string // see EventTrigger.Comment
	Body          string
	Reconstructed bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SrcPos        pipeline.SourcePos
}

func (t *TSParser) QualifiedName() string   { return qualName(t.Schema, t.Name) }
func (t *TSParser) Pos() pipeline.SourcePos { return t.SrcPos }
func (t *TSParser) irObject()               {}

// TSTemplate is a CREATE TEXT SEARCH TEMPLATE declaration.
type TSTemplate struct {
	Schema string
	Name   string
	// Functions holds the qualified names of the template's INIT/LEXIZE
	// support functions — same reasoning as TSParser.Functions.
	Functions     []string
	Comment       *string // see EventTrigger.Comment
	Body          string
	Reconstructed bool // Body rebuilt from the catalog; see Tablespace.Reconstructed
	SrcPos        pipeline.SourcePos
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
	NameMaps   []pipeline.NameMapEntry
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

// QualifiedName must include ObjectType: PostgreSQL's own pg_default_acl
// catalog has one row per (role, schema, object type) tuple, and a single
// DPG declaration naming multiple object types (RFC's own worked example:
// TABLES, FUNCTIONS, and SEQUENCES together) is built into one
// *DefaultPrivileges per type (see Builder.BuildDefaultPrivileges) — without
// ObjectType here, those independently-diffable objects would collide under
// one identity and the merger would silently drop all but one.
func (d *DefaultPrivileges) QualifiedName() string {
	key := "DEFAULT PRIVILEGES"
	if d.ForRole != nil {
		key += " FOR " + *d.ForRole
	}
	if d.InSchema != nil {
		key += " IN " + *d.InSchema
	}
	if d.ObjectType != "" {
		key += " " + d.ObjectType
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

// FuncTableColumns returns just the RETURNS TABLE(...) column list from a
// function's Args, in declaration order. A function using RETURNS TABLE
// carries its output columns as ordinary Args entries with Mode "TABLE" —
// they must never be rendered inline in the main parameter list (that's a
// distinct, invalid syntax; the real columns belong inside a separate
// RETURNS TABLE(...) clause).
func FuncTableColumns(args []FuncArg) []FuncArg {
	var cols []FuncArg
	for _, a := range args {
		if a.Mode == "TABLE" {
			cols = append(cols, a)
		}
	}
	return cols
}

// FormatTableColumns renders a RETURNS TABLE(...) column list's inner text
// ("name type, name type, ..."). Used both to render the clause (dump,
// diff-generated SQL) and as the comparable snapshot representation for
// drift detection — the two must always agree, since a mismatch here would
// either silently miss a genuine column-list change or falsely report one.
func FormatTableColumns(cols []FuncArg) string {
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		parts = append(parts, c.Name+" "+c.Type.String())
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
