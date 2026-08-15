package snapshot

// SnapNameMapEntry is one NAME MAP entry stored in the snapshot for an object.
// Exactly one of Rule or Name is non-empty:
//   - Rule is a DPG naming-convention rule keyword (e.g. "LOWER_SNAKE_CASE").
//   - Name is a literal target identifier (from a double-quoted source value).
type SnapNameMapEntry struct {
	Tool string `json:"tool"`
	Rule string `json:"rule,omitempty"`
	Name string `json:"name,omitempty"`
}

// SnapObject is the discriminated JSON form stored per object in the snapshot.
// The Kind field allows the Differ to load the right Go type.
type SnapObject struct {
	Kind string `json:"kind"`
	// One of the following is populated, depending on Kind.
	Table             *SnapTable             `json:"table,omitempty"`
	View              *SnapView              `json:"view,omitempty"`
	Function          *SnapFunction          `json:"function,omitempty"`
	Type              *SnapType              `json:"type,omitempty"`
	Schema            *SnapSchema            `json:"schema,omitempty"`
	Extension         *SnapExtension         `json:"extension,omitempty"`
	Sequence          *SnapSequence          `json:"sequence,omitempty"`
	Role              *SnapRole              `json:"role,omitempty"`
	VirtualType       *SnapVirtualType       `json:"virtual_type,omitempty"`
	DefaultPrivileges *SnapDefaultPrivileges `json:"default_privileges,omitempty"`
	Opaque            *SnapOpaque            `json:"opaque,omitempty"`
}

// SnapOpaque covers body-based objects: Procedure, Aggregate, Tablespace, FDW,
// ForeignServer, UserMapping, Publication, Subscription, EventTrigger, Collation,
// Operator, OperatorClass, OperatorFamily, Cast, StatisticsObject, and TS objects.
type SnapOpaque struct {
	Kind   string `json:"kind"` // e.g. "procedure", "tablespace"
	Schema string `json:"schema,omitempty"`
	Name   string `json:"name"`
	// Args is reused across two unrelated purposes, distinguished by Kind:
	// for procedure/aggregate it's the type-only arg list used for identity
	// AND for DROP PROCEDURE/AGGREGATE's "(args)" clause; for operator it's
	// "lefttype, righttype" (see ir.OperandsKey) used for DROP OPERATOR's
	// mandatory operand-type clause, in the same string-goes-straight-into-
	// the-parens shape.
	Args        string             `json:"args,omitempty"`
	Using       string             `json:"using,omitempty"` // index access method (operator_class/operator_family)
	BodyHash    string             `json:"body_hash,omitempty"`
	Comment     *string            `json:"comment,omitempty"`
	Grants      []SnapGrant        `json:"grants,omitempty"`      // aggregate and procedure
	Revocations []SnapGrant        `json:"revocations,omitempty"` // procedure only
	Mappings    []SnapTSMapping    `json:"mappings,omitempty"`    // ts_config only (RFC §12.1 MAPPING FOR)
	NameMaps    []SnapNameMapEntry `json:"name_maps,omitempty"`
	// TablespaceLocation (RFC §14.7), CastMethod/CastContext/CastFunction
	// (RFC §14.5), and EventTriggerEvent/EventTriggerTags/
	// EventTriggerFunction (RFC §14.1) are structured diffing inputs for
	// their respective kinds only — see ir.Tablespace.Location/ir.Cast.
	// Method/ir.EventTrigger.Event's doc comments for why BodyHash alone
	// was insufficient (it goes unset, via Reconstructed, on every live
	// path, so a live-catalog-only change was silently invisible).
	TablespaceLocation   string   `json:"tablespace_location,omitempty"`
	CastMethod           string   `json:"cast_method,omitempty"`
	CastContext          string   `json:"cast_context,omitempty"`
	CastFunction         string   `json:"cast_function,omitempty"`
	EventTriggerEvent    string   `json:"event_trigger_event,omitempty"`
	EventTriggerTags     []string `json:"event_trigger_tags,omitempty"`
	EventTriggerFunction string   `json:"event_trigger_function,omitempty"`
	// OptionsStructured/FDWHandler/FDWValidator/FDWOptions/ServerFDWName/
	// ServerType/ServerVersion/ServerOptions/UserMappingOptions are RFC
	// §14.8/§14.9/§14.10's structured diffing inputs for fdw/server/
	// user_mapping respectively. Unlike Tablespace/Cast/EventTrigger
	// (Tier 1), none of these three kinds has a field guaranteed non-empty
	// on every real object (a bare FOREIGN DATA WRAPPER with no HANDLER/
	// VALIDATOR/OPTIONS is valid), so the usual "Go zero value means
	// stale snapshot" guard doesn't work here — OptionsStructured is an
	// explicit sentinel instead, set true only by current code, so its
	// absence unambiguously means "snapshot predates this feature."
	OptionsStructured  bool           `json:"options_structured,omitempty"`
	FDWHandler         string         `json:"fdw_handler,omitempty"`
	FDWValidator       string         `json:"fdw_validator,omitempty"`
	FDWOptions         []SnapOptionKV `json:"fdw_options,omitempty"`
	ServerFDWName      string         `json:"server_fdw_name,omitempty"`
	ServerType         *string        `json:"server_type,omitempty"`
	ServerVersion      *string        `json:"server_version,omitempty"`
	ServerOptions      []SnapOptionKV `json:"server_options,omitempty"`
	UserMappingOptions []SnapOptionKV `json:"user_mapping_options,omitempty"`
	// PublicationStructured/PublicationAllTables/PublicationTables/
	// PublicationInsert/Update/Delete/Truncate are RFC §13.1's structured
	// diffing inputs — see ir.Publication.AllTables' doc comment.
	// PublicationTables holds "schema.name" qualified strings, compared as
	// a set (order-independent). PublicationStructured is the same
	// explicit-sentinel pattern as OptionsStructured above (Publish flags
	// default true, AllTables can legitimately be false — no field is a
	// reliable "not yet populated" signal on its own).
	PublicationStructured        bool     `json:"publication_structured,omitempty"`
	PublicationAllTables         bool     `json:"publication_all_tables,omitempty"`
	PublicationTables            []string `json:"publication_tables,omitempty"`
	PublicationInsert            bool     `json:"publication_insert,omitempty"`
	PublicationUpdate            bool     `json:"publication_update,omitempty"`
	PublicationDelete            bool     `json:"publication_delete,omitempty"`
	PublicationTruncate          bool     `json:"publication_truncate,omitempty"`
	PublicationHasFilteredTables bool     `json:"publication_has_filtered_tables,omitempty"`
}

// SnapOptionKV is one OPTIONS (...) key/value entry, used by the
// fdw/server/user_mapping kinds above.
type SnapOptionKV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// SnapTSMapping is one MAPPING FOR entry (RFC §12.1) on a text search
// configuration. Dictionaries is a fallback chain — real PG syntax, tried in
// order until one recognizes the token.
type SnapTSMapping struct {
	TokenTypes   []string `json:"token_types"`
	Dictionaries []string `json:"dictionaries"`
}

type SnapSchema struct {
	Name        string             `json:"name"`
	Owner       *string            `json:"owner,omitempty"`
	Comment     *string            `json:"comment,omitempty"`
	RenamedFrom *string            `json:"renamed_from,omitempty"`
	NameMaps    []SnapNameMapEntry `json:"name_maps,omitempty"`
}

type SnapExtension struct {
	Name     string             `json:"name"`
	Schema   *string            `json:"schema,omitempty"`
	Version  *string            `json:"version,omitempty"`
	NameMaps []SnapNameMapEntry `json:"name_maps,omitempty"`
}

type SnapTable struct {
	Schema         string             `json:"schema"`
	Name           string             `json:"name"`
	Unlogged       bool               `json:"unlogged,omitempty"`
	Foreign        bool               `json:"foreign,omitempty"`
	ForeignServer  *string            `json:"foreign_server,omitempty"`
	ForeignOptions string             `json:"foreign_options,omitempty"` // comma-separated key=value options
	Owner          *string            `json:"owner,omitempty"`
	Tablespace     *string            `json:"tablespace,omitempty"`
	Comment        *string            `json:"comment,omitempty"`
	RenamedFrom    *string            `json:"renamed_from,omitempty"`
	Deprecated     *string            `json:"deprecated,omitempty"`
	Protected      bool               `json:"protected,omitempty"`
	DropCascade    bool               `json:"drop_cascade,omitempty"`
	RLSEnabled     bool               `json:"rls_enabled,omitempty"`
	RLSForced      bool               `json:"rls_forced,omitempty"`
	Inherits       []string           `json:"inherits,omitempty"`
	PartitionBy    string             `json:"partition_by,omitempty"` // e.g. "RANGE (created_at)"
	Partitions     []SnapPartition    `json:"partitions,omitempty"`
	Columns        []SnapColumn       `json:"columns,omitempty"`
	Constraints    []SnapConstraint   `json:"constraints,omitempty"`
	Indexes        []SnapIndex        `json:"indexes,omitempty"`
	Policies       []SnapPolicy       `json:"policies,omitempty"`
	Triggers       []SnapTrigger      `json:"triggers,omitempty"`
	Grants         []SnapGrant        `json:"grants,omitempty"`
	Revocations    []SnapGrant        `json:"revocations,omitempty"`
	NameMaps       []SnapNameMapEntry `json:"name_maps,omitempty"`
}

// SnapPartition is one partition entry attached to a partitioned table.
// PartitionBy/Partitions describe sub-partitioning (RFC §7.13): PartitionBy
// is "" for a leaf partition; when set, Partitions holds that partition's
// own nested partition entries, recursively.
type SnapPartition struct {
	Schema      string          `json:"schema,omitempty"`
	Name        string          `json:"name"`
	Bound       string          `json:"bound"` // raw FOR VALUES … expression
	PartitionBy string          `json:"partition_by,omitempty"`
	Partitions  []SnapPartition `json:"partitions,omitempty"`
}

type SnapColumn struct {
	Name        string             `json:"name"`
	Type        string             `json:"type"`
	NotNull     bool               `json:"not_null,omitempty"`
	Default     *string            `json:"default,omitempty"`
	Identity    *string            `json:"identity,omitempty"` // "ALWAYS" or "BY DEFAULT"
	Generated   *string            `json:"generated,omitempty"`
	Comment     *string            `json:"comment,omitempty"`
	Statistics  *int               `json:"statistics,omitempty"`
	Compression *string            `json:"compression,omitempty"`
	Storage     *string            `json:"storage,omitempty"`
	Deprecated  *string            `json:"deprecated,omitempty"`
	RenamedFrom *string            `json:"renamed_from,omitempty"`
	Grants      []SnapGrant        `json:"grants,omitempty"`
	Revocations []SnapGrant        `json:"revocations,omitempty"`
	NameMaps    []SnapNameMapEntry `json:"name_maps,omitempty"`
}

type SnapConstraint struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Expr       string `json:"expr,omitempty"`
	NotValid   bool   `json:"not_valid,omitempty"`
	Deferrable bool   `json:"deferrable,omitempty"`
}

// SnapIndex is a flat, fully comparable (string/bool fields only) snapshot of
// an index's definition. diff.diffIndexes builds one from the desired ir.Index
// via ToSnapIndex and compares it against the stored one with plain `==` to
// decide whether a same-named index needs to be recreated.
type SnapIndex struct {
	Name             string `json:"name"`
	Unique           bool   `json:"unique,omitempty"`
	Method           string `json:"method"`
	Columns          string `json:"columns"` // comma-separated; may carry ASC/DESC and NULLS FIRST/LAST suffixes
	Where            string `json:"where,omitempty"`
	Include          string `json:"include,omitempty"` // comma-separated covering columns
	NullsNotDistinct bool   `json:"nulls_not_distinct,omitempty"`
	With             string `json:"with,omitempty"` // comma-separated key=value storage params
	Tablespace       string `json:"tablespace,omitempty"`
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
	Schema      string      `json:"schema"`
	Name        string      `json:"name"`
	Query       string      `json:"query"`
	Owner       *string     `json:"owner,omitempty"`
	Comment     *string     `json:"comment,omitempty"`
	Deprecated  *string     `json:"deprecated,omitempty"`
	Recursive   bool        `json:"recursive,omitempty"`
	WithNoData  bool        `json:"with_no_data,omitempty"`
	Grants      []SnapGrant `json:"grants,omitempty"`
	Revocations []SnapGrant `json:"revocations,omitempty"`
	// Indexes is only meaningful when Materialized (see ir.View.Indexes).
	Indexes  []SnapIndex        `json:"indexes,omitempty"`
	NameMaps []SnapNameMapEntry `json:"name_maps,omitempty"`
}

type SnapFunction struct {
	Schema     string `json:"schema"`
	Name       string `json:"name"`
	Args       string `json:"args"` // type-only signature key
	ReturnType string `json:"return_type"`
	ReturnsSet bool   `json:"returns_set,omitempty"`
	// ReturnTable is the RETURNS TABLE(...) column list ("a integer, b text"),
	// empty for an ordinary function. ReturnType/ReturnsSet alone can't tell
	// two different TABLE column lists apart (both show "record"/true), so
	// this is compared independently — see ir.FormatTableColumns.
	ReturnTable string             `json:"return_table,omitempty"`
	Language    string             `json:"language"`
	Volatility  string             `json:"volatility"`
	Parallel    string             `json:"parallel,omitempty"`
	Cost        *float64           `json:"cost,omitempty"`
	Rows        *float64           `json:"rows,omitempty"`
	BodyHash    string             `json:"body_hash"`
	Comment     *string            `json:"comment,omitempty"`
	Deprecated  *string            `json:"deprecated,omitempty"`
	Grants      []SnapGrant        `json:"grants,omitempty"`
	Revocations []SnapGrant        `json:"revocations,omitempty"`
	NameMaps    []SnapNameMapEntry `json:"name_maps,omitempty"`
}

type SnapType struct {
	Schema         string       `json:"schema"`
	Name           string       `json:"name"`
	Variant        string       `json:"variant"`                   // ENUM, COMPOSITE, RANGE, DOMAIN, BASE
	Values         []string     `json:"values,omitempty"`          // ENUM only
	CompositeAttrs []SnapColumn `json:"composite_attrs,omitempty"` // COMPOSITE only
	BodyHash       string       `json:"body_hash,omitempty"`       // RANGE/BASE only; see sourceBodyHash
	// DomainBaseType/DomainDefault/DomainNotNull/DomainConstraints are
	// DOMAIN-only (RFC §5.4): structured diffing inputs, not just an opaque
	// body hash, so property-level changes get their own targeted ALTER
	// DOMAIN op instead of an unconditional DROP+CREATE.
	DomainBaseType    string             `json:"domain_base_type,omitempty"`
	DomainDefault     *string            `json:"domain_default,omitempty"`
	DomainNotNull     bool               `json:"domain_not_null,omitempty"`
	DomainConstraints []SnapConstraint   `json:"domain_constraints,omitempty"`
	Comment           *string            `json:"comment,omitempty"`
	NameMaps          []SnapNameMapEntry `json:"name_maps,omitempty"`
}

type SnapSequence struct {
	Schema      string             `json:"schema"`
	Name        string             `json:"name"`
	Owner       *string            `json:"owner,omitempty"`
	Comment     *string            `json:"comment,omitempty"`
	IncrementBy *int64             `json:"increment_by,omitempty"`
	MinValue    *int64             `json:"min_value,omitempty"`
	MaxValue    *int64             `json:"max_value,omitempty"`
	StartValue  *int64             `json:"start_value,omitempty"`
	Cache       *int64             `json:"cache,omitempty"`
	Cycle       bool               `json:"cycle,omitempty"`
	NameMaps    []SnapNameMapEntry `json:"name_maps,omitempty"`
}

// SnapRole is a Role's stored state (RFC §11.1). PasswordHash is a hash of
// the *declared* PASSWORD text (literal or {{secret-uri}} reference,
// verbatim) — never the resolved value; see ir.Role's doc comment and RFC
// §11.1's "Password drift detection" for why hashing the declared text
// (not just a boolean has_password) is safe and enables rotation detection.
type SnapRole struct {
	Name            string             `json:"name"`
	CanLogin        *bool              `json:"can_login,omitempty"`
	Superuser       *bool              `json:"superuser,omitempty"`
	CreateDB        *bool              `json:"create_db,omitempty"`
	CreateRole      *bool              `json:"create_role,omitempty"`
	Inherit         *bool              `json:"inherit,omitempty"`
	IsReplication   *bool              `json:"is_replication,omitempty"`
	BypassRLS       *bool              `json:"bypass_rls,omitempty"`
	ConnectionLimit *int               `json:"connection_limit,omitempty"`
	PasswordHash    string             `json:"password_hash,omitempty"`
	ValidUntil      *string            `json:"valid_until,omitempty"`
	InRole          []string           `json:"in_role,omitempty"`
	RoleMembers     []string           `json:"role_members,omitempty"`
	AdminRoles      []string           `json:"admin_roles,omitempty"`
	Comment         *string            `json:"comment,omitempty"`
	NameMaps        []SnapNameMapEntry `json:"name_maps,omitempty"`
}

// SnapVtypeBody is the serialised form of an ir.VtypeBody discriminated union.
// Kind is one of "type_ref", "composite", or "union".
type SnapVtypeBody struct {
	Kind string `json:"kind"`
	// type_ref fields
	Schema  string `json:"schema,omitempty"`
	Name    string `json:"name,omitempty"`
	IsArray bool   `json:"array,omitempty"`
	// composite fields
	Fields []SnapVtypeField `json:"fields,omitempty"`
	// union fields
	Members []SnapVtypeBody `json:"members,omitempty"`
}

// SnapVtypeField is one named field inside a SnapVtypeBody composite.
type SnapVtypeField struct {
	Name string        `json:"name"`
	Type SnapVtypeBody `json:"type"`
}

// SnapVirtualType is the snapshot form of a VIRTUAL TYPE declaration.
// Columns and composite type attributes may reference a virtual type; DPG
// resolves those to jsonb / jsonb[] in generated SQL.  The structured body is
// stored here for downstream consumers (ORMs, type-safe query builders).
type SnapVirtualType struct {
	Schema     string             `json:"schema,omitempty"`
	Name       string             `json:"name"`
	Body       SnapVtypeBody      `json:"body"`
	JsonFormat string             `json:"json_format,omitempty"` // "json" or "jsonb"; empty = default (jsonb)
	Comment    *string            `json:"comment,omitempty"`
	NameMaps   []SnapNameMapEntry `json:"name_maps,omitempty"`
}

// SnapDefaultPrivileges is the snapshot form of a DEFAULT PRIVILEGES declaration.
// Each entry identifies a (ForRole, InSchema, ObjectType) combination and its grants.
type SnapDefaultPrivileges struct {
	ForRole     *string     `json:"for_role,omitempty"`
	InSchema    *string     `json:"in_schema,omitempty"`
	ObjectType  string      `json:"object_type"`
	Grants      []SnapGrant `json:"grants,omitempty"`
	Revocations []SnapGrant `json:"revocations,omitempty"`
}
