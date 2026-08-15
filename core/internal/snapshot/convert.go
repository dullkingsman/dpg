package snapshot

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

// toSnapNameMaps converts a slice of pipeline.NameMapEntry to the snapshot form.
func toSnapNameMaps(entries []pipeline.NameMapEntry) []SnapNameMapEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]SnapNameMapEntry, len(entries))
	for i, e := range entries {
		if e.IsLiteral {
			out[i] = SnapNameMapEntry{Tool: e.Tool, Name: e.Value}
		} else {
			out[i] = SnapNameMapEntry{Tool: e.Tool, Rule: e.Value}
		}
	}
	return out
}

// toSnapOptions converts an OPTIONS (...) list to its snapshot form.
func toSnapOptions(opts []pipeline.StorageParam) []SnapOptionKV {
	if len(opts) == 0 {
		return nil
	}
	out := make([]SnapOptionKV, len(opts))
	for i, o := range opts {
		out[i] = SnapOptionKV{Key: o.Key, Value: o.Value}
	}
	return out
}

// userMappingPasswordKeys mirrors internal/introspect's own copy of the same
// list (which itself mirrors internal/linter's passwordColNames) — kept as
// a local duplicate rather than a cross-package import, following this
// codebase's established pattern for this exact recurring 5-string need
// (see internal/introspect/opaque.go's identical comment).
var userMappingPasswordKeys = []string{"password", "passwd", "pwd", "secret", "passphrase"}

func isUserMappingPasswordKey(key string) bool {
	lower := strings.ToLower(key)
	for _, kw := range userMappingPasswordKeys {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// toSnapNonSensitiveOptions is toSnapOptions for UserMapping specifically:
// password-like keys are skipped entirely (not just redacted) so they
// never enter the structural diff comparison — see
// ir.UserMapping.Options' doc comment.
func toSnapNonSensitiveOptions(opts []pipeline.StorageParam) []SnapOptionKV {
	var out []SnapOptionKV
	for _, o := range opts {
		if isUserMappingPasswordKey(o.Key) {
			continue
		}
		out = append(out, SnapOptionKV{Key: o.Key, Value: o.Value})
	}
	return out
}

// hashBodyStr returns a SHA-256 hex digest of the body string (trimmed).
// Returns "" for empty strings.
func hashBodyStr(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return fmt.Sprintf("%x", sum)
}

// sourceBodyHash hashes a body for change detection, but only when the body was
// derived from source (parsed and deparsed by the compiler). A reconstructed
// body — rebuilt from the live catalog by the introspector — is canonical but
// not byte-identical to arbitrary hand-written source (schema qualification,
// LC_COLLATE/LC_CTYPE vs LOCALE, option ordering), so hashing it would report
// spurious "body changed" drift when it lands on either side of a diff. For
// reconstructed bodies we store "", which the differ's guard treats as "no
// comparison". Offline plan/apply (source vs source) keep a real hash and so
// still detect genuine edits.
func sourceBodyHash(body string, reconstructed bool) string {
	if reconstructed {
		return ""
	}
	return hashBodyStr(body)
}

// Populate converts objects into SnapObjects and stores them in snap.
func Populate(snap *pipeline.Snapshot, objects []pipeline.IRObject) error {
	for _, obj := range objects {
		so := toSnapObject(obj)
		if so == nil {
			continue
		}
		if err := snap.SetObject(obj.QualifiedName(), so); err != nil {
			return err
		}
	}
	return nil
}

func toSnapObject(obj pipeline.IRObject) *SnapObject {
	switch o := obj.(type) {
	case *ir.Table:
		return &SnapObject{Kind: "table", Table: toSnapTable(o)}
	case *ir.View:
		return &SnapObject{Kind: "view", View: toSnapView(o)}
	case *ir.Function:
		return &SnapObject{Kind: "function", Function: toSnapFunction(o)}
	case *ir.Type:
		return &SnapObject{Kind: "type", Type: toSnapType(o)}
	case *ir.Schema:
		return &SnapObject{Kind: "schema", Schema: toSnapSchema(o)}
	case *ir.Extension:
		return &SnapObject{Kind: "extension", Extension: toSnapExtension(o)}
	case *ir.Sequence:
		return &SnapObject{Kind: "sequence", Sequence: toSnapSequence(o)}
	case *ir.Role:
		return &SnapObject{Kind: "role", Role: toSnapRole(o)}
	case *ir.Procedure:
		so := &SnapOpaque{
			Kind: "procedure", Schema: o.Schema, Name: o.Name,
			Args: ir.ArgsKey(o.Args), BodyHash: o.BodyHash, Comment: o.Comment,
			NameMaps: toSnapNameMaps(o.NameMaps),
		}
		for _, g := range o.Grants {
			so.Grants = append(so.Grants, toSnapGrant(g))
		}
		for _, r := range o.Revocations {
			so.Revocations = append(so.Revocations, toSnapRevocation(r))
		}
		return &SnapObject{Kind: "procedure", Opaque: so}
	case *ir.Aggregate:
		so := &SnapOpaque{
			Kind: "aggregate", Schema: o.Schema, Name: o.Name,
			Args: ir.ArgsKey(o.Args), BodyHash: hashBodyStr(o.Body), Comment: o.Comment,
			NameMaps: toSnapNameMaps(o.NameMaps),
		}
		for _, g := range o.Grants {
			so.Grants = append(so.Grants, toSnapGrant(g))
		}
		return &SnapObject{Kind: "aggregate", Opaque: so}
	// The reliable-tier opaque types below can be introspected, in which case
	// their Body is a catalog reconstruction (Reconstructed == true) whose text
	// won't byte-match hand-written source. sourceBodyHash stores a hash only for
	// source-derived bodies, so offline plan/apply still detect edits while
	// verify/plan --live (which involve a reconstruction) skip the comparison.
	case *ir.Tablespace:
		return &SnapObject{Kind: "tablespace", Opaque: &SnapOpaque{
			Kind: "tablespace", Name: o.Name, TablespaceLocation: o.Location,
			BodyHash: sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.ForeignDataWrapper:
		return &SnapObject{Kind: "fdw", Opaque: &SnapOpaque{
			Kind: "fdw", Name: o.Name,
			OptionsStructured: true, FDWHandler: o.Handler, FDWValidator: o.Validator, FDWOptions: toSnapOptions(o.Options),
			BodyHash: sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.ForeignServer:
		return &SnapObject{Kind: "server", Opaque: &SnapOpaque{
			Kind: "server", Name: o.Name,
			OptionsStructured: true, ServerFDWName: o.FDWName, ServerType: o.Type, ServerVersion: o.Version, ServerOptions: toSnapOptions(o.Options),
			BodyHash: sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.UserMapping:
		return &SnapObject{Kind: "user_mapping", Opaque: &SnapOpaque{
			Kind: "user_mapping", Name: o.User + "@" + o.Server,
			OptionsStructured: true, UserMappingOptions: toSnapNonSensitiveOptions(o.Options),
			BodyHash: sourceBodyHash(o.Body, o.Reconstructed),
		}}
	case *ir.Publication:
		tables := make([]string, len(o.Tables))
		for i, t := range o.Tables {
			if t.Schema != "" {
				tables[i] = t.Schema + "." + t.Name
			} else {
				tables[i] = t.Name
			}
		}
		return &SnapObject{Kind: "publication", Opaque: &SnapOpaque{
			Kind: "publication", Name: o.Name,
			PublicationStructured: true, PublicationAllTables: o.AllTables, PublicationTables: tables,
			PublicationInsert: o.Insert, PublicationUpdate: o.Update, PublicationDelete: o.Delete, PublicationTruncate: o.Truncate,
			PublicationHasFilteredTables: o.HasFilteredTables,
			BodyHash:                     sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.Subscription:
		return &SnapObject{Kind: "subscription", Opaque: &SnapOpaque{
			Kind: "subscription", Name: o.Name, BodyHash: sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.EventTrigger:
		return &SnapObject{Kind: "event_trigger", Opaque: &SnapOpaque{
			Kind: "event_trigger", Name: o.Name,
			EventTriggerEvent: o.Event, EventTriggerTags: o.Tags, EventTriggerFunction: o.Function,
			BodyHash: sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.Collation:
		return &SnapObject{Kind: "collation", Opaque: &SnapOpaque{
			Kind: "collation", Schema: o.Schema, Name: o.Name,
			CollationStructured: true, CollationProvider: o.Provider,
			CollationCollate: o.Collate, CollationCtype: o.Ctype, CollationICULocale: o.ICULocale,
			CollationDeterministic: o.Deterministic,
			BodyHash:               sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.Operator:
		return &SnapObject{Kind: "operator", Opaque: &SnapOpaque{
			Kind: "operator", Schema: o.Schema, Name: o.Name,
			Args:     ir.OperandsKey(o.LeftType, o.RightType),
			BodyHash: sourceBodyHash(o.Body, o.Reconstructed),
			Comment:  o.Comment,
		}}
	case *ir.OperatorClass:
		return &SnapObject{Kind: "operator_class", Opaque: &SnapOpaque{
			Kind: "operator_class", Schema: o.Schema, Name: o.Name, Using: o.AccessMethod, BodyHash: sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.OperatorFamily:
		return &SnapObject{Kind: "operator_family", Opaque: &SnapOpaque{
			Kind: "operator_family", Schema: o.Schema, Name: o.Name, Using: o.AccessMethod, BodyHash: sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.Cast:
		return &SnapObject{Kind: "cast", Opaque: &SnapOpaque{
			Kind:         "cast",
			Name:         o.SourceType.String() + "->" + o.TargetType.String(),
			CastMethod:   o.Method,
			CastContext:  o.Context,
			CastFunction: o.Function,
			BodyHash:     sourceBodyHash(o.Body, o.Reconstructed),
			Comment:      o.Comment,
		}}
	case *ir.StatisticsObject:
		return &SnapObject{Kind: "statistics", Opaque: &SnapOpaque{
			Kind: "statistics", Schema: o.Schema, Name: o.Name, BodyHash: sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.TSConfig:
		opaque := &SnapOpaque{
			Kind: "ts_config", Schema: o.Schema, Name: o.Name, BodyHash: sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}
		for _, m := range o.Mappings {
			dicts := make([]string, len(m.Dictionaries))
			for i, d := range m.Dictionaries {
				dicts[i] = d.String()
			}
			opaque.Mappings = append(opaque.Mappings, SnapTSMapping{
				TokenTypes:   append([]string(nil), m.TokenTypes...),
				Dictionaries: dicts,
			})
		}
		return &SnapObject{Kind: "ts_config", Opaque: opaque}
	case *ir.TSDict:
		return &SnapObject{Kind: "ts_dict", Opaque: &SnapOpaque{
			Kind: "ts_dict", Schema: o.Schema, Name: o.Name, BodyHash: sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.TSParser:
		return &SnapObject{Kind: "ts_parser", Opaque: &SnapOpaque{
			Kind: "ts_parser", Schema: o.Schema, Name: o.Name, BodyHash: sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.TSTemplate:
		return &SnapObject{Kind: "ts_template", Opaque: &SnapOpaque{
			Kind: "ts_template", Schema: o.Schema, Name: o.Name, BodyHash: sourceBodyHash(o.Body, o.Reconstructed), Comment: o.Comment,
		}}
	case *ir.DefaultPrivileges:
		sdp := &SnapDefaultPrivileges{
			ForRole:    o.ForRole,
			InSchema:   o.InSchema,
			ObjectType: o.ObjectType,
		}
		for _, g := range o.Grants {
			sdp.Grants = append(sdp.Grants, toSnapGrant(g))
		}
		for _, r := range o.Revocations {
			sdp.Revocations = append(sdp.Revocations, toSnapRevocation(r))
		}
		return &SnapObject{Kind: "default_privileges", DefaultPrivileges: sdp}
	case *ir.VirtualType:
		return &SnapObject{Kind: "virtual_type", VirtualType: &SnapVirtualType{
			Schema:     o.Schema,
			Name:       o.Name,
			Body:       toSnapVtypeBody(o.Body),
			JsonFormat: o.JsonFormat,
			Comment:    o.Comment,
			NameMaps:   toSnapNameMaps(o.NameMaps),
		}}
	default:
		return nil
	}
}

// toSnapVtypeBody converts an ir.VtypeBody to its serialisable snapshot form.
func toSnapVtypeBody(body ir.VtypeBody) SnapVtypeBody {
	switch b := body.(type) {
	case ir.VtypeTypeRef:
		return SnapVtypeBody{Kind: "type_ref", Schema: b.Schema, Name: b.Name, IsArray: b.IsArray}
	case ir.VtypeComposite:
		fields := make([]SnapVtypeField, len(b.Fields))
		for i, f := range b.Fields {
			fields[i] = SnapVtypeField{
				Name: f.Name,
				Type: SnapVtypeBody{Kind: "type_ref", Schema: f.Type.Schema, Name: f.Type.Name, IsArray: f.Type.IsArray},
			}
		}
		return SnapVtypeBody{Kind: "composite", Fields: fields}
	case ir.VtypeUnion:
		members := make([]SnapVtypeBody, len(b.Members))
		for i, m := range b.Members {
			members[i] = toSnapVtypeBody(m)
		}
		return SnapVtypeBody{Kind: "union", Members: members}
	default:
		return SnapVtypeBody{Kind: "type_ref", Name: "unknown"}
	}
}

func toSnapSchema(o *ir.Schema) *SnapSchema {
	return &SnapSchema{
		Name:        o.Name,
		Owner:       o.Owner,
		Comment:     o.Comment,
		RenamedFrom: o.RenamedFrom,
		NameMaps:    toSnapNameMaps(o.NameMaps),
	}
}

func toSnapExtension(o *ir.Extension) *SnapExtension {
	return &SnapExtension{
		Name:     o.Name,
		Schema:   o.Schema,
		Version:  o.Version,
		NameMaps: toSnapNameMaps(o.NameMaps),
	}
}

// toSnapPartition converts an ir.Partition into a SnapPartition, recursing
// into nested sub-partitions (RFC §7.13). schema is the owning table's (or
// parent partition's) schema, since a partition itself carries no schema
// field.
func toSnapPartition(schema string, p *ir.Partition) SnapPartition {
	sp := SnapPartition{
		Schema: schema,
		Name:   p.Name,
		Bound:  p.Bounds,
	}
	if p.PartitionBy != nil {
		sp.PartitionBy = p.PartitionBy.Strategy + " (" + strings.Join(p.PartitionBy.Columns, ", ") + ")"
	}
	for _, sub := range p.Partitions {
		sp.Partitions = append(sp.Partitions, toSnapPartition(schema, sub))
	}
	return sp
}

func toSnapTable(o *ir.Table) *SnapTable {
	t := &SnapTable{
		Schema:         o.Schema,
		Name:           o.Name,
		Unlogged:       o.Unlogged,
		Foreign:        o.Foreign,
		ForeignServer:  o.ForeignServer,
		ForeignOptions: flattenParams(o.ForeignOptions),
		Owner:          o.Owner,
		Tablespace:     o.Tablespace,
		Comment:        o.Comment,
		RenamedFrom:    o.RenamedFrom,
		Deprecated:     o.Deprecated,
		Protected:      o.Protected,
		DropCascade:    o.DropCascade,
		RLSEnabled:     o.RLSEnabled,
		RLSForced:      o.RLSForced,
		Inherits:       append([]string(nil), o.Inherits...),
	}
	if o.PartitionBy != nil {
		t.PartitionBy = o.PartitionBy.Strategy + " (" + strings.Join(o.PartitionBy.Columns, ", ") + ")"
	}
	for _, p := range o.Partitions {
		t.Partitions = append(t.Partitions, toSnapPartition(o.Schema, p))
	}
	for _, col := range o.Columns {
		t.Columns = append(t.Columns, toSnapColumn(col))
	}
	for _, cst := range o.Constraints {
		t.Constraints = append(t.Constraints, toSnapConstraint(cst))
	}
	for _, idx := range o.Indexes {
		t.Indexes = append(t.Indexes, ToSnapIndex(idx))
	}
	for _, pol := range o.Policies {
		t.Policies = append(t.Policies, toSnapPolicy(pol))
	}
	for _, trg := range o.Triggers {
		t.Triggers = append(t.Triggers, toSnapTrigger(trg))
	}
	for _, g := range o.Grants {
		t.Grants = append(t.Grants, toSnapGrant(g))
	}
	for _, r := range o.Revocations {
		t.Revocations = append(t.Revocations, toSnapRevocation(r))
	}
	t.NameMaps = toSnapNameMaps(o.NameMaps)
	return t
}

func toSnapColumn(col *ir.Column) SnapColumn {
	sc := SnapColumn{
		Name:        col.Name,
		Type:        col.Type.String(),
		NotNull:     col.NotNull,
		Default:     col.Default,
		Comment:     col.Comment,
		Statistics:  col.Statistics,
		Compression: col.Compression,
		Storage:     col.Storage,
		Deprecated:  col.Deprecated,
		RenamedFrom: col.RenamedFrom,
	}
	if col.Identity != nil {
		var s string
		if col.Identity.Always {
			s = "ALWAYS"
		} else {
			s = "BY DEFAULT"
		}
		sc.Identity = &s
	}
	if col.Generated != nil {
		sc.Generated = &col.Generated.Expr
	}
	for _, g := range col.Grants {
		sc.Grants = append(sc.Grants, toSnapGrant(g))
	}
	for _, r := range col.Revocations {
		sc.Revocations = append(sc.Revocations, toSnapRevocation(r))
	}
	sc.NameMaps = toSnapNameMaps(col.NameMaps)
	return sc
}

func toSnapConstraint(cst *ir.Constraint) SnapConstraint {
	return SnapConstraint{
		Name:       cst.Name,
		Type:       cst.Type,
		Expr:       cst.Expr,
		NotValid:   cst.NotValid,
		Deferrable: cst.Deferrable,
	}
}

// ToSnapIndex converts an ir.Index into its flat, fully comparable snapshot
// form. Exported so diff.diffIndexes can build the same representation from
// the desired side and compare it against the stored snapshot with `==` to
// detect a same-named index whose definition changed.
// flattenParams renders an ordered key/value param list (Index.With,
// Table.ForeignOptions) as a comma-separated "key=value" string for
// snapshot storage/comparison. Reloption/option values are often quoted by
// PostgreSQL's own reconstruction (pg_get_indexdef, pg_options_to_table) but
// not necessarily by hand-written source — stripping one layer of
// surrounding single quotes keeps the two spellings comparing equal, so a
// live/introspected catalog never shows spurious drift against unchanged
// source.
func flattenParams(params []pipeline.StorageParam) string {
	parts := make([]string, 0, len(params))
	for _, p := range params {
		v := p.Value
		if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
			v = v[1 : len(v)-1]
		}
		parts = append(parts, p.Key+"="+v)
	}
	return strings.Join(parts, ", ")
}

func ToSnapIndex(idx *ir.Index) SnapIndex {
	cols := make([]string, 0, len(idx.Columns))
	for _, c := range idx.Columns {
		var s string
		if c.Name != "" {
			s = c.Name
		} else if c.Expr != nil {
			s = "(" + c.Expr.Text + ")"
		}
		if c.SortOrder != "" {
			s += " " + c.SortOrder
		}
		if c.Nulls != "" {
			s += " NULLS " + c.Nulls
		}
		cols = append(cols, s)
	}
	si := SnapIndex{
		Name:             idx.Name,
		Unique:           idx.Unique,
		Method:           idx.Method,
		Columns:          strings.Join(cols, ", "),
		Include:          strings.Join(idx.Include, ", "),
		NullsNotDistinct: idx.NullsNotDistinct,
		With:             flattenParams(idx.With),
	}
	if idx.Where != nil {
		si.Where = *idx.Where
	}
	if idx.Tablespace != nil {
		si.Tablespace = *idx.Tablespace
	}
	return si
}

func toSnapPolicy(pol *ir.Policy) SnapPolicy {
	sp := SnapPolicy{
		Name:       pol.Name,
		Command:    pol.Command,
		Permissive: pol.Permissive,
	}
	if pol.Using != nil {
		sp.Using = *pol.Using
	}
	if pol.WithCheck != nil {
		sp.WithCheck = *pol.WithCheck
	}
	return sp
}

func toSnapTrigger(trg *ir.Trigger) SnapTrigger {
	return SnapTrigger{
		Name:     trg.Name,
		When:     trg.When,
		Events:   strings.Join(trg.Events, ", "),
		ForEach:  trg.ForEach,
		Function: trg.Function,
	}
}

func toSnapGrant(g ir.Grant) SnapGrant {
	return SnapGrant{
		Privileges: g.Privileges,
		Roles:      g.Roles,
		WithGrant:  g.WithGrant,
	}
}

// toSnapRevocation converts an explicit ir.Revocation into a SnapGrant for
// storage/comparison purposes. Reuses SnapGrant (rather than a dedicated type)
// to match the existing DefaultPrivileges precedent; Cascade is intentionally
// not stored, same as that precedent — a revocation is matched for diffing
// purposes by privileges+roles only.
func toSnapRevocation(r ir.Revocation) SnapGrant {
	return SnapGrant{
		Privileges: r.Privileges,
		Roles:      r.Roles,
	}
}

func toSnapView(o *ir.View) *SnapView {
	sv := &SnapView{
		Schema:     o.Schema,
		Name:       o.Name,
		Query:      o.Query,
		Owner:      o.Owner,
		Comment:    o.Comment,
		Deprecated: o.Deprecated,
		Recursive:  o.Recursive,
		WithNoData: o.WithNoData,
	}
	for _, g := range o.Grants {
		sv.Grants = append(sv.Grants, toSnapGrant(g))
	}
	for _, r := range o.Revocations {
		sv.Revocations = append(sv.Revocations, toSnapRevocation(r))
	}
	for _, idx := range o.Indexes {
		sv.Indexes = append(sv.Indexes, ToSnapIndex(idx))
	}
	sv.NameMaps = toSnapNameMaps(o.NameMaps)
	return sv
}

func toSnapFunction(o *ir.Function) *SnapFunction {
	sf := &SnapFunction{
		Schema:      o.Schema,
		Name:        o.Name,
		Args:        ir.ArgsKey(o.Args),
		ReturnType:  o.ReturnType.String(),
		ReturnsSet:  o.ReturnType.SetOf,
		ReturnTable: ir.FormatTableColumns(ir.FuncTableColumns(o.Args)),
		Language:    o.Attrs.Language,
		Volatility:  o.Attrs.Volatility,
		Parallel:    o.Attrs.Parallel,
		Cost:        o.Attrs.Cost,
		Rows:        o.Attrs.Rows,
		BodyHash:    o.BodyHash,
		Comment:     o.Comment,
		Deprecated:  o.Deprecated,
	}
	for _, g := range o.Grants {
		sf.Grants = append(sf.Grants, toSnapGrant(g))
	}
	for _, r := range o.Revocations {
		sf.Revocations = append(sf.Revocations, toSnapRevocation(r))
	}
	sf.NameMaps = toSnapNameMaps(o.NameMaps)
	return sf
}

func toSnapType(o *ir.Type) *SnapType {
	st := &SnapType{
		Schema:  o.Schema,
		Name:    o.Name,
		Variant: o.Variant,
		Values:  o.EnumValues,
		Comment: o.Comment,
	}
	if o.Variant == "RANGE" || o.Variant == "BASE" {
		st.BodyHash = sourceBodyHash(o.Body, o.Reconstructed)
	}
	if o.Variant == "DOMAIN" {
		st.DomainBaseType = o.DomainBaseType.String()
		st.DomainDefault = o.DomainDefault
		st.DomainNotNull = o.DomainNotNull
		for _, cst := range o.DomainConstraints {
			st.DomainConstraints = append(st.DomainConstraints, toSnapConstraint(cst))
		}
	}
	for _, attr := range o.CompositeAttrs {
		st.CompositeAttrs = append(st.CompositeAttrs, toSnapColumn(attr))
	}
	st.NameMaps = toSnapNameMaps(o.NameMaps)
	return st
}

func toSnapSequence(o *ir.Sequence) *SnapSequence {
	return &SnapSequence{
		Schema:      o.Schema,
		Name:        o.Name,
		Owner:       o.Owner,
		Comment:     o.Comment,
		IncrementBy: o.IncrementBy,
		MinValue:    o.MinValue,
		MaxValue:    o.MaxValue,
		StartValue:  o.StartValue,
		Cache:       o.Cache,
		Cycle:       o.Cycle != nil && *o.Cycle,
		NameMaps:    toSnapNameMaps(o.NameMaps),
	}
}

func toSnapRole(o *ir.Role) *SnapRole {
	var pwHash string
	if o.Password != nil {
		pwHash = hashBodyStr(*o.Password)
	}
	return &SnapRole{
		Name:            o.Name,
		CanLogin:        o.CanLogin,
		Superuser:       o.Superuser,
		CreateDB:        o.CreateDB,
		CreateRole:      o.CreateRole,
		Inherit:         o.Inherit,
		IsReplication:   o.IsReplication,
		BypassRLS:       o.BypassRLS,
		ConnectionLimit: o.ConnectionLimit,
		PasswordHash:    pwHash,
		ValidUntil:      o.ValidUntil,
		InRole:          o.InRole,
		RoleMembers:     o.RoleMembers,
		AdminRoles:      o.AdminRoles,
		Comment:         o.Comment,
		NameMaps:        toSnapNameMaps(o.NameMaps),
	}
}
