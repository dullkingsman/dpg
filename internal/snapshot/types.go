package snapshot

// SnapObject is the discriminated JSON form stored per object in the snapshot.
// The Kind field allows the Differ to load the right Go type.
type SnapObject struct {
	Kind string `json:"kind"`
	// One of the following is populated, depending on Kind.
	Table     *SnapTable     `json:"table,omitempty"`
	View      *SnapView      `json:"view,omitempty"`
	Function  *SnapFunction  `json:"function,omitempty"`
	Type      *SnapType      `json:"type,omitempty"`
	Schema    *SnapSchema    `json:"schema,omitempty"`
	Extension *SnapExtension `json:"extension,omitempty"`
	Sequence  *SnapSequence  `json:"sequence,omitempty"`
	Role      *SnapRole      `json:"role,omitempty"`
	Opaque    *SnapOpaque    `json:"opaque,omitempty"`
}

// SnapOpaque covers body-based objects: Procedure, Aggregate, Tablespace, FDW,
// ForeignServer, UserMapping, Publication, Subscription, EventTrigger, Collation,
// Operator, OperatorClass, OperatorFamily, Cast, StatisticsObject, and TS objects.
type SnapOpaque struct {
	Kind     string  `json:"kind"` // e.g. "procedure", "tablespace"
	Schema   string  `json:"schema,omitempty"`
	Name     string  `json:"name"`
	Args     string  `json:"args,omitempty"` // type-only arg list (proc/agg identity)
	BodyHash string  `json:"body_hash,omitempty"`
	Comment  *string `json:"comment,omitempty"`
}

type SnapSchema struct {
	Name        string  `json:"name"`
	Owner       *string `json:"owner,omitempty"`
	Comment     *string `json:"comment,omitempty"`
	RenamedFrom *string `json:"renamed_from,omitempty"`
}

type SnapExtension struct {
	Name    string  `json:"name"`
	Schema  *string `json:"schema,omitempty"`
	Version *string `json:"version,omitempty"`
}

type SnapTable struct {
	Schema      string           `json:"schema"`
	Name        string           `json:"name"`
	Unlogged    bool             `json:"unlogged,omitempty"`
	Foreign     bool             `json:"foreign,omitempty"`
	Owner       *string          `json:"owner,omitempty"`
	Comment     *string          `json:"comment,omitempty"`
	RenamedFrom *string          `json:"renamed_from,omitempty"`
	Deprecated  *string          `json:"deprecated,omitempty"`
	Protected   bool             `json:"protected,omitempty"`
	DropCascade bool             `json:"drop_cascade,omitempty"`
	RLSEnabled  bool             `json:"rls_enabled,omitempty"`
	RLSForced   bool             `json:"rls_forced,omitempty"`
	Inherits    []string         `json:"inherits,omitempty"`
	PartitionBy string           `json:"partition_by,omitempty"` // e.g. "RANGE (created_at)"
	Partitions  []SnapPartition  `json:"partitions,omitempty"`
	Columns     []SnapColumn     `json:"columns,omitempty"`
	Constraints []SnapConstraint `json:"constraints,omitempty"`
	Indexes     []SnapIndex      `json:"indexes,omitempty"`
	Policies    []SnapPolicy     `json:"policies,omitempty"`
	Triggers    []SnapTrigger    `json:"triggers,omitempty"`
	Grants      []SnapGrant      `json:"grants,omitempty"`
}

// SnapPartition is one partition entry attached to a partitioned table.
type SnapPartition struct {
	Schema string `json:"schema,omitempty"`
	Name   string `json:"name"`
	Bound  string `json:"bound"` // raw FOR VALUES … expression
}

type SnapColumn struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	NotNull     bool        `json:"not_null,omitempty"`
	Default     *string     `json:"default,omitempty"`
	Identity    *string     `json:"identity,omitempty"` // "ALWAYS" or "BY DEFAULT"
	Generated   *string     `json:"generated,omitempty"`
	Comment     *string     `json:"comment,omitempty"`
	Statistics  *int        `json:"statistics,omitempty"`
	Compression *string     `json:"compression,omitempty"`
	Storage     *string     `json:"storage,omitempty"`
	Deprecated  *string     `json:"deprecated,omitempty"`
	RenamedFrom *string     `json:"renamed_from,omitempty"`
	Grants      []SnapGrant `json:"grants,omitempty"`
}

type SnapConstraint struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Expr       string `json:"expr,omitempty"`
	NotValid   bool   `json:"not_valid,omitempty"`
	Deferrable bool   `json:"deferrable,omitempty"`
}

type SnapIndex struct {
	Name    string `json:"name"`
	Unique  bool   `json:"unique,omitempty"`
	Method  string `json:"method"`
	Columns string `json:"columns"` // comma-separated
	Where   string `json:"where,omitempty"`
}

type SnapPolicy struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	Permissive bool   `json:"permissive"`
	Using      string `json:"using,omitempty"`
	WithCheck  string `json:"with_check,omitempty"`
}

type SnapTrigger struct {
	Name     string `json:"name"`
	When     string `json:"when"`
	Events   string `json:"events"` // comma-separated
	ForEach  string `json:"for_each"`
	Function string `json:"function"`
}

type SnapGrant struct {
	Privileges []string `json:"privileges,omitempty"` // nil = ALL
	Roles      []string `json:"roles"`
	WithGrant  bool     `json:"with_grant,omitempty"`
}

type SnapView struct {
	Schema     string      `json:"schema"`
	Name       string      `json:"name"`
	Query      string      `json:"query"`
	Owner      *string     `json:"owner,omitempty"`
	Comment    *string     `json:"comment,omitempty"`
	Recursive  bool        `json:"recursive,omitempty"`
	WithNoData bool        `json:"with_no_data,omitempty"`
	Grants     []SnapGrant `json:"grants,omitempty"`
}

type SnapFunction struct {
	Schema     string      `json:"schema"`
	Name       string      `json:"name"`
	Args       string      `json:"args"` // type-only signature key
	ReturnType string      `json:"return_type"`
	Language   string      `json:"language"`
	Volatility string      `json:"volatility"`
	BodyHash   string      `json:"body_hash"`
	Comment    *string     `json:"comment,omitempty"`
	Grants     []SnapGrant `json:"grants,omitempty"`
}

type SnapType struct {
	Schema  string   `json:"schema"`
	Name    string   `json:"name"`
	Variant string   `json:"variant"`          // ENUM, COMPOSITE, RANGE, DOMAIN, BASE
	Values  []string `json:"values,omitempty"` // ENUM only
	Comment *string  `json:"comment,omitempty"`
}

type SnapSequence struct {
	Schema  string  `json:"schema"`
	Name    string  `json:"name"`
	Comment *string `json:"comment,omitempty"`
}

type SnapRole struct {
	Name    string  `json:"name"`
	Comment *string `json:"comment,omitempty"`
}
