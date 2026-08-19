package pipeline

import (
	"strconv"
	"strings"
)

// Identifier is a (possibly schema-qualified) SQL identifier.
type Identifier struct {
	Schema string
	Name   string
}

func (id Identifier) String() string {
	if id.Schema == "" {
		return id.Name
	}
	return id.Schema + "." + id.Name
}

// StringLit is a string literal value (unquoted).
type StringLit struct {
	Value string
	Pos   SourcePos
}

// RawExpr is an opaque SQL expression stored as raw text.
type RawExpr struct {
	Text string
	Pos  SourcePos
}

// StorageParam is a key=value pair from a WITH (...) clause.
type StorageParam struct {
	Key   string
	Value string
}

// IndexColumn is one column entry in an index definition.
type IndexColumn struct {
	Name      string // column name, or "" if Expr is set
	Expr      *RawExpr
	Collation *Identifier
	OpClass   *Identifier
	Nulls     string // "FIRST", "LAST", or ""
	SortOrder string // "ASC", "DESC", or ""
}

// IndexDef is a DPG INDICES { } entry.
type IndexDef struct {
	Name             Identifier
	Unique           bool
	Method           *Identifier
	Columns          []IndexColumn
	Where            *RawExpr
	Include          []Identifier
	NullsNotDistinct bool
	With             []StorageParam
	Tablespace       *Identifier
	Concurrently     bool
	Comment          *StringLit
	Pos              SourcePos
}

// GrantEntry is a single GRANTS directive.
type GrantEntry struct {
	Privileges []string // "SELECT", "INSERT", etc.; nil = ALL
	Roles      []Identifier
	WithGrant  bool
	Pos        SourcePos
}

// RevocationEntry is a single REVOCATIONS directive.
type RevocationEntry struct {
	Privileges []string
	Roles      []Identifier
	Cascade    bool
	Pos        SourcePos
}

// PolicyDef is a single row-security policy definition.
type PolicyDef struct {
	Name       Identifier
	Command    string // "ALL", "SELECT", "INSERT", "UPDATE", "DELETE"
	Permissive bool   // true = PERMISSIVE (default), false = RESTRICTIVE
	Using      *RawExpr
	WithCheck  *RawExpr
	Roles      []Identifier
	Comment    *StringLit
	Pos        SourcePos
}

// TriggerDef is a single trigger definition inside a { } block.
type TriggerDef struct {
	Name    Identifier
	When    string   // "BEFORE", "AFTER", "INSTEAD OF"
	Events  []string // "INSERT", "UPDATE", "DELETE", "TRUNCATE"
	ForEach string   // "ROW", "STATEMENT"
	// UpdateOfColumns is the column list from "UPDATE OF col1, col2, ..."
	// (RFC audit item #1) — nil when the UPDATE event has no OF clause at
	// all (fires on any column update, PostgreSQL's own default). Only
	// ever set when Events contains "UPDATE".
	UpdateOfColumns []string
	// OldTransitionName/NewTransitionName are RFC §7.9's "REFERENCING OLD
	// TABLE AS ... NEW TABLE AS ..." transition-table names (audit item
	// #2) — nil when REFERENCING isn't present, or for whichever of
	// OLD/NEW wasn't named. A transition relation name is always a plain,
	// unqualified identifier (not a real relation, just a local alias
	// scoped to the trigger's execution), unlike Function below.
	OldTransitionName *string
	NewTransitionName *string
	Condition         *RawExpr
	Function          Identifier
	Args            []string
	Comment         *StringLit
	Pos             SourcePos
}

// ColumnBlock holds DPG-specific attributes for a single column.
type ColumnBlock struct {
	Name           Identifier
	Comment        *StringLit
	Statistics     *int
	Compression    *Identifier
	Storage        *Identifier
	Deprecated     *StringLit
	RenamedFrom    *Identifier
	Using          *RawExpr
	Grants         []GrantEntry
	Revocations    []RevocationEntry
	SecurityLabels []SecurityLabel
	NameMaps       []NameMapEntry
	Pos            SourcePos
}

// SecurityLabel is one "SECURITY LABEL [FOR provider] '...'" directive
// inside a { } block, mirroring real PostgreSQL's SECURITY LABEL statement
// (RFC §14.11 — only meaningful with a label provider installed, e.g.
// sepgsql). Provider == "" is the unqualified form: PostgreSQL resolves it
// to the sole loaded provider, and errors if zero or more than one is
// loaded. Unlike Comment (a single nilable value), a block may declare
// several SecurityLabel entries — one per provider — since real PostgreSQL
// lets multiple independent label providers label the same object at once.
type SecurityLabel struct {
	Provider string
	Label    string
	Pos      SourcePos
}

// ConstraintDef is an additional constraint attached in the { } block.
// Used for NOT VALID constraints or cross-file constraint additions.
type ConstraintDef struct {
	Name     Identifier
	Expr     RawExpr
	NotValid bool
	Comment  *StringLit
	Pos      SourcePos
}

// PartitionBound describes a single partition's bounds, optionally with its
// own nested PARTITION BY clause and PARTITIONS block (sub-partitioning,
// RFC §7.13). SubStrategy is "" for a leaf partition; when non-empty,
// SubColumns/SubPartitions describe that partition's own partitioning —
// recursively, since a sub-partition may itself be further sub-partitioned.
type PartitionBound struct {
	Name          Identifier
	Bounds        RawExpr
	SubStrategy   string // "RANGE"/"LIST"/"HASH"; "" means no sub-partitioning
	SubColumns    []string
	SubPartitions []PartitionBound
	Pos           SourcePos
}

// PartitionDef is the PARTITIONS { } directive.
type PartitionDef struct {
	Partitions []PartitionBound
	Pos        SourcePos
}

// MigrateRemoveBlock is the MIGRATE REMOVE { } directive.
type MigrateRemoveBlock struct {
	Reason string
	SQL    RawExpr
	Pos    SourcePos
}

// DefaultPrivilegeGrant is one GRANTS { } entry inside a DEFAULT PRIVILEGES
// block. Unlike a regular GrantEntry, real PostgreSQL's ALTER DEFAULT
// PRIVILEGES grammar requires an "ON <object-type>" clause per grant — the
// object type isn't implicit from an enclosing table/view the way it is for
// every other GRANTS block: "GRANT priv[, ...] ON {TABLES|SEQUENCES|
// FUNCTIONS|TYPES|SCHEMAS} TO role[, ...] [WITH GRANT OPTION]".
type DefaultPrivilegeGrant struct {
	ObjectType string // "TABLES", "SEQUENCES", "FUNCTIONS", "TYPES", "SCHEMAS"
	Privileges []string
	Roles      []Identifier
	WithGrant  bool
	Pos        SourcePos
}

// DefaultPrivilegeRevocation is REVOCATIONS { } entry's DefaultPrivileges sibling.
type DefaultPrivilegeRevocation struct {
	ObjectType string
	Privileges []string
	Roles      []Identifier
	Cascade    bool
	Pos        SourcePos
}

// DefaultPrivilegesBlock is a DEFAULT PRIVILEGES { } entry. Real PostgreSQL's
// ALTER DEFAULT PRIVILEGES statement can never be complete without its
// GRANT/REVOKE action inline — a "[FOR ROLE x] [IN SCHEMA y]" header alone
// is not valid PG SQL on its own (confirmed live) — so, unlike every other
// DPG object kind, this is never split into a pg_query-parsed Part 1 and a
// blockparser-parsed Part 2; the whole declaration (header + block) is
// parsed here, with zero pg_query involvement. See
// blockparser.Parser.ParseDefaultPrivileges.
type DefaultPrivilegesBlock struct {
	InSchema    *Identifier
	ForRole     *Identifier
	Grants      []DefaultPrivilegeGrant
	Revocations []DefaultPrivilegeRevocation
	Pos         SourcePos
}

// TSMappingDef is a MAPPING FOR { } entry (TEXT SEARCH CONFIGURATION).
// Dictionaries is a fallback chain (real PG syntax: "WITH dict1, dict2, ...";
// dictionaries are tried in order until one recognizes the token) — a single
// entry is the common case, but the RFC's own worked example uses two.
type TSMappingDef struct {
	TokenTypes   []string
	Dictionaries []Identifier
	Pos          SourcePos
}

// OpFamilyMember is one "loose" (family-level) member declared in an
// OPERATOR FAMILY { } block — real PG only lets you attach these via
// ALTER OPERATOR FAMILY ... ADD, since a member may belong to the family
// directly rather than to any one of its operator classes (RFC §14.4). Each
// item's grammar is copied verbatim from that ALTER statement's own ADD
// list-item grammar, just without repeating the ALTER/family header.
type OpFamilyMember struct {
	IsFunction bool       // false = OPERATOR item, true = FUNCTION item
	Number     int        // strategy number (OPERATOR) / support number (FUNCTION)
	Name       Identifier // operator symbol ("<", "@>", ...) or function name
	LeftType   string     // op_type left; canonicalized by the IR builder
	RightType  string     // op_type right; canonicalized by the IR builder
	FuncArgs   []string   // FUNCTION only: declared argument types, canonicalized
	OrderBy    bool       // OPERATOR only: true = FOR ORDER BY, false (default) = FOR SEARCH
	SortFamily Identifier // OPERATOR + OrderBy only: the FOR ORDER BY target family
	Pos        SourcePos
}

// Key identifies this member's catalog slot: PostgreSQL's own unique indexes
// on pg_amop ((amopfamily, amoplefttype, amoprighttype, amopstrategy)) and
// pg_amproc ((amprocfamily, amproclefttype, amprocrighttype, amprocnum))
// never include the operator/function identity itself — so two members at
// the same slot with a different operator/function are the same catalog row
// changing shape (DROP the old one, ADD the new one), not two independent
// members. LeftType/RightType MUST already be canonicalized (see
// ir.ParseTypeText) before this is used as a diff key, or a same-type member
// written as "int4" on one side and "integer" on the other will misdiff.
func (m OpFamilyMember) Key() string {
	kind := "OPERATOR"
	if m.IsFunction {
		kind = "FUNCTION"
	}
	return kind + "|" + strconv.Itoa(m.Number) + "|" + m.LeftType + "|" + m.RightType
}

// AddClause renders this member's contribution to an
// "ALTER OPERATOR FAMILY ... ADD <clause>[, <clause>...]" statement.
func (m OpFamilyMember) AddClause() string {
	if m.IsFunction {
		return "FUNCTION " + strconv.Itoa(m.Number) + " (" + m.LeftType + ", " + m.RightType + ") " +
			m.Name.String() + "(" + strings.Join(m.FuncArgs, ", ") + ")"
	}
	clause := "OPERATOR " + strconv.Itoa(m.Number) + " " + m.Name.String() + "(" + m.LeftType + ", " + m.RightType + ")"
	if m.OrderBy {
		clause += " FOR ORDER BY " + m.SortFamily.String()
	}
	return clause
}

// DropClause renders this member's contribution to an
// "ALTER OPERATOR FAMILY ... DROP <clause>[, <clause>...]" statement. Real
// PG's DROP form identifies a member by slot only (number + op_types) — the
// operator/function name is never repeated.
func (m OpFamilyMember) DropClause() string {
	kind := "OPERATOR"
	if m.IsFunction {
		kind = "FUNCTION"
	}
	return kind + " " + strconv.Itoa(m.Number) + " (" + m.LeftType + ", " + m.RightType + ")"
}

// NameMapEntry is a single NAME MAP directive inside a { } block.
// Tool is the target tool identifier (e.g. "default", "prisma").
// IsLiteral=false: Value is a rule keyword (e.g. "LOWER_SNAKE_CASE").
// IsLiteral=true: Value is a literal target name (from a double-quoted string).
type NameMapEntry struct {
	Tool      string
	Value     string
	IsLiteral bool
	Pos       SourcePos
}

// ValidNameMapRules is the closed set of DPG-defined naming convention rules.
// Rule values in [namemaps] config sections and NAME MAP block directives must
// be one of these identifiers.
var ValidNameMapRules = map[string]bool{
	"LOWER_SNAKE_CASE":  true,
	"UPPER_SNAKE_CASE":  true,
	"LOWER_CAMEL_CASE":  true,
	"UPPER_CAMEL_CASE":  true,
	"LOWER_KEBAB_CASE":  true,
	"UPPER_KEBAB_CASE":  true,
	"TRAIN_CASE":        true,
	"LOWER_CASE":        true,
	"UPPER_CASE":        true,
	"PASCAL_SNAKE_CASE": true,
}

// BlockAST is the parsed representation of a DPG { } block.
// Populated by the BlockParser (Phase 4b). Fields absent from a given block
// remain at their zero value.
type BlockAST struct {
	Pos                 SourcePos
	Comment             *StringLit
	Owner               *Identifier
	RenamedFrom         *Identifier
	Protected           bool
	Deprecated          *StringLit
	DropCascade         bool
	Indices             []IndexDef
	Policies            []PolicyDef
	Triggers            []TriggerDef
	Grants              []GrantEntry
	Revocations         []RevocationEntry
	SecurityLabels      []SecurityLabel
	Columns             []ColumnBlock
	Constraints         []ConstraintDef
	EnableRLS           bool
	ForceRLS            bool
	Partitions          *PartitionDef
	MigrateRemove       *MigrateRemoveBlock
	DefaultPrivileges   []DefaultPrivilegesBlock
	Mappings            []TSMappingDef
	OpFamilyMembers     []OpFamilyMember
	PreferredJsonFormat string // "json" or "jsonb"; empty = not set (default jsonb)
	NameMaps            []NameMapEntry
	// DomainDefault/DomainNotNull are RFC §5.4's DOMAIN-only "DEFAULT expr;"
	// and "NOT NULL;" block directives, MERGED with any DEFAULT/NOT NULL/
	// CHECK already present in the domain's Part 1 (real PG's own inline
	// CREATE DOMAIN syntax remains fully supported; this block form is
	// additive, not a replacement). CHECK constraints declared this way use
	// the existing Constraints field — a domain CHECK has the exact same
	// "CONSTRAINT name CHECK (expr);" shape as a table's.
	DomainDefault *RawExpr
	DomainNotNull bool
}
