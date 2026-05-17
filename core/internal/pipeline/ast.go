package pipeline

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
	Name         Identifier
	Unique       bool
	Method       *Identifier
	Columns      []IndexColumn
	Where        *RawExpr
	Include      []Identifier
	With         []StorageParam
	Tablespace   *Identifier
	Concurrently bool
	Pos          SourcePos
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
	Pos        SourcePos
}

// TriggerDef is a single trigger definition inside a { } block.
type TriggerDef struct {
	Name      Identifier
	When      string   // "BEFORE", "AFTER", "INSTEAD OF"
	Events    []string // "INSERT", "UPDATE", "DELETE", "TRUNCATE"
	ForEach   string   // "ROW", "STATEMENT"
	Condition *RawExpr
	Function  Identifier
	Args      []string
	Pos       SourcePos
}

// ColumnBlock holds DPG-specific attributes for a single column.
type ColumnBlock struct {
	Name        Identifier
	Comment     *StringLit
	Statistics  *int
	Compression *Identifier
	Storage     *Identifier
	Deprecated  *StringLit
	RenamedFrom *Identifier
	Using       *RawExpr
	Grants      []GrantEntry
	Revocations []RevocationEntry
	NameMaps    []NameMapEntry
	Pos         SourcePos
}

// ConstraintDef is an additional constraint attached in the { } block.
// Used for NOT VALID constraints or cross-file constraint additions.
type ConstraintDef struct {
	Name     Identifier
	Expr     RawExpr
	NotValid bool
	Pos      SourcePos
}

// PartitionBound describes a single partition's bounds.
type PartitionBound struct {
	Name   Identifier
	Bounds RawExpr
	Pos    SourcePos
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

// DefaultPrivilegesBlock is a DEFAULT PRIVILEGES { } entry.
type DefaultPrivilegesBlock struct {
	InSchema    *Identifier
	ForRole     *Identifier
	ObjectType  string // "TABLES", "SEQUENCES", "FUNCTIONS", etc.
	Grants      []GrantEntry
	Revocations []RevocationEntry
	Pos         SourcePos
}

// TSMappingDef is a MAPPING FOR { } entry (TEXT SEARCH CONFIGURATION).
type TSMappingDef struct {
	TokenTypes []string
	Dictionary Identifier
	Pos        SourcePos
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
	Columns             []ColumnBlock
	Constraints         []ConstraintDef
	EnableRLS           bool
	ForceRLS            bool
	Partitions          *PartitionDef
	MigrateRemove       *MigrateRemoveBlock
	DefaultPrivileges   []DefaultPrivilegesBlock
	Mappings            []TSMappingDef
	PreferredJsonFormat string // "json" or "jsonb"; empty = not set (default jsonb)
	NameMaps            []NameMapEntry
}
