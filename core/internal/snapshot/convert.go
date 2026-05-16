package snapshot

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

// hashBodyStr returns a SHA-256 hex digest of the body string (trimmed).
// Returns "" for empty strings.
func hashBodyStr(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return fmt.Sprintf("%x", sum)
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
		}
		for _, g := range o.Grants {
			so.Grants = append(so.Grants, toSnapGrant(g))
		}
		return &SnapObject{Kind: "procedure", Opaque: so}
	case *ir.Aggregate:
		so := &SnapOpaque{
			Kind: "aggregate", Schema: o.Schema, Name: o.Name,
			Args: ir.ArgsKey(o.Args), BodyHash: hashBodyStr(o.Body), Comment: o.Comment,
		}
		for _, g := range o.Grants {
			so.Grants = append(so.Grants, toSnapGrant(g))
		}
		return &SnapObject{Kind: "aggregate", Opaque: so}
	case *ir.Tablespace:
		return &SnapObject{Kind: "tablespace", Opaque: &SnapOpaque{
			Kind: "tablespace", Name: o.Name, BodyHash: hashBodyStr(o.Body), Comment: o.Comment,
		}}
	case *ir.ForeignDataWrapper:
		return &SnapObject{Kind: "fdw", Opaque: &SnapOpaque{
			Kind: "fdw", Name: o.Name, BodyHash: hashBodyStr(o.Body), Comment: o.Comment,
		}}
	case *ir.ForeignServer:
		return &SnapObject{Kind: "server", Opaque: &SnapOpaque{
			Kind: "server", Name: o.Name, BodyHash: hashBodyStr(o.Body), Comment: o.Comment,
		}}
	case *ir.UserMapping:
		return &SnapObject{Kind: "user_mapping", Opaque: &SnapOpaque{
			Kind: "user_mapping", Name: o.User + "@" + o.Server, BodyHash: hashBodyStr(o.Body),
		}}
	case *ir.Publication:
		return &SnapObject{Kind: "publication", Opaque: &SnapOpaque{
			Kind: "publication", Name: o.Name, BodyHash: hashBodyStr(o.Body),
		}}
	case *ir.Subscription:
		return &SnapObject{Kind: "subscription", Opaque: &SnapOpaque{
			Kind: "subscription", Name: o.Name, BodyHash: hashBodyStr(o.Body),
		}}
	case *ir.EventTrigger:
		return &SnapObject{Kind: "event_trigger", Opaque: &SnapOpaque{
			Kind: "event_trigger", Name: o.Name, BodyHash: hashBodyStr(o.Body),
		}}
	case *ir.Collation:
		return &SnapObject{Kind: "collation", Opaque: &SnapOpaque{
			Kind: "collation", Schema: o.Schema, Name: o.Name, BodyHash: hashBodyStr(o.Body),
		}}
	case *ir.Operator:
		return &SnapObject{Kind: "operator", Opaque: &SnapOpaque{
			Kind: "operator", Schema: o.Schema, Name: o.Name, BodyHash: hashBodyStr(o.Body),
		}}
	case *ir.OperatorClass:
		return &SnapObject{Kind: "operator_class", Opaque: &SnapOpaque{
			Kind: "operator_class", Schema: o.Schema, Name: o.Name, BodyHash: hashBodyStr(o.Body),
		}}
	case *ir.OperatorFamily:
		return &SnapObject{Kind: "operator_family", Opaque: &SnapOpaque{
			Kind: "operator_family", Schema: o.Schema, Name: o.Name, BodyHash: hashBodyStr(o.Body),
		}}
	case *ir.Cast:
		return &SnapObject{Kind: "cast", Opaque: &SnapOpaque{
			Kind:     "cast",
			Name:     o.SourceType.String() + "->" + o.TargetType.String(),
			BodyHash: hashBodyStr(o.Body),
		}}
	case *ir.StatisticsObject:
		return &SnapObject{Kind: "statistics", Opaque: &SnapOpaque{
			Kind: "statistics", Schema: o.Schema, Name: o.Name, BodyHash: hashBodyStr(o.Body),
		}}
	case *ir.TSConfig:
		return &SnapObject{Kind: "ts_config", Opaque: &SnapOpaque{
			Kind: "ts_config", Schema: o.Schema, Name: o.Name, Comment: o.Comment,
		}}
	case *ir.TSDict:
		return &SnapObject{Kind: "ts_dict", Opaque: &SnapOpaque{
			Kind: "ts_dict", Schema: o.Schema, Name: o.Name, BodyHash: hashBodyStr(o.Body), Comment: o.Comment,
		}}
	case *ir.TSParser:
		return &SnapObject{Kind: "ts_parser", Opaque: &SnapOpaque{
			Kind: "ts_parser", Schema: o.Schema, Name: o.Name, BodyHash: hashBodyStr(o.Body),
		}}
	case *ir.TSTemplate:
		return &SnapObject{Kind: "ts_template", Opaque: &SnapOpaque{
			Kind: "ts_template", Schema: o.Schema, Name: o.Name, BodyHash: hashBodyStr(o.Body),
		}}
	case *ir.DefaultPrivileges:
		return &SnapObject{Kind: "default_privileges", Opaque: &SnapOpaque{
			Kind: "default_privileges", Name: o.QualifiedName(),
		}}
	case *ir.VirtualType:
		return &SnapObject{Kind: "virtual_type", VirtualType: &SnapVirtualType{
			Schema:  o.Schema,
			Name:    o.Name,
			Body:    o.Body,
			Comment: o.Comment,
		}}
	default:
		return nil
	}
}

func toSnapSchema(o *ir.Schema) *SnapSchema {
	return &SnapSchema{
		Name:        o.Name,
		Owner:       o.Owner,
		Comment:     o.Comment,
		RenamedFrom: o.RenamedFrom,
	}
}

func toSnapExtension(o *ir.Extension) *SnapExtension {
	return &SnapExtension{
		Name:    o.Name,
		Schema:  o.Schema,
		Version: o.Version,
	}
}

func toSnapTable(o *ir.Table) *SnapTable {
	t := &SnapTable{
		Schema:      o.Schema,
		Name:        o.Name,
		Unlogged:    o.Unlogged,
		Foreign:     o.Foreign,
		Owner:       o.Owner,
		Comment:     o.Comment,
		RenamedFrom: o.RenamedFrom,
		Deprecated:  o.Deprecated,
		Protected:   o.Protected,
		DropCascade: o.DropCascade,
		RLSEnabled:  o.RLSEnabled,
		RLSForced:   o.RLSForced,
		Inherits:    append([]string(nil), o.Inherits...),
	}
	if o.PartitionBy != nil {
		t.PartitionBy = o.PartitionBy.Strategy + " (" + strings.Join(o.PartitionBy.Columns, ", ") + ")"
	}
	for _, p := range o.Partitions {
		t.Partitions = append(t.Partitions, SnapPartition{
			Schema: o.Schema,
			Name:   p.Name,
			Bound:  p.Bounds,
		})
	}
	for _, col := range o.Columns {
		t.Columns = append(t.Columns, toSnapColumn(col))
	}
	for _, cst := range o.Constraints {
		t.Constraints = append(t.Constraints, toSnapConstraint(cst))
	}
	for _, idx := range o.Indexes {
		t.Indexes = append(t.Indexes, toSnapIndex(idx))
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

func toSnapIndex(idx *ir.Index) SnapIndex {
	cols := make([]string, 0, len(idx.Columns))
	for _, c := range idx.Columns {
		if c.Name != "" {
			cols = append(cols, c.Name)
		} else if c.Expr != nil {
			cols = append(cols, "("+c.Expr.Text+")")
		}
	}
	si := SnapIndex{
		Name:    idx.Name,
		Unique:  idx.Unique,
		Method:  idx.Method,
		Columns: strings.Join(cols, ", "),
	}
	if idx.Where != nil {
		si.Where = *idx.Where
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

func toSnapView(o *ir.View) *SnapView {
	sv := &SnapView{
		Schema:     o.Schema,
		Name:       o.Name,
		Query:      o.Query,
		Owner:      o.Owner,
		Comment:    o.Comment,
		Recursive:  o.Recursive,
		WithNoData: o.WithNoData,
	}
	for _, g := range o.Grants {
		sv.Grants = append(sv.Grants, toSnapGrant(g))
	}
	return sv
}

func toSnapFunction(o *ir.Function) *SnapFunction {
	sf := &SnapFunction{
		Schema:     o.Schema,
		Name:       o.Name,
		Args:       ir.ArgsKey(o.Args),
		ReturnType: o.ReturnType.String(),
		Language:   o.Attrs.Language,
		Volatility: o.Attrs.Volatility,
		BodyHash:   o.BodyHash,
		Comment:    o.Comment,
	}
	for _, g := range o.Grants {
		sf.Grants = append(sf.Grants, toSnapGrant(g))
	}
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
	for _, attr := range o.CompositeAttrs {
		st.CompositeAttrs = append(st.CompositeAttrs, toSnapColumn(attr))
	}
	return st
}

func toSnapSequence(o *ir.Sequence) *SnapSequence {
	return &SnapSequence{
		Schema:      o.Schema,
		Name:        o.Name,
		Comment:     o.Comment,
		IncrementBy: o.IncrementBy,
		MinValue:    o.MinValue,
		MaxValue:    o.MaxValue,
		StartValue:  o.StartValue,
		Cache:       o.Cache,
		Cycle:       o.Cycle,
	}
}

func toSnapRole(o *ir.Role) *SnapRole {
	return &SnapRole{
		Name:    o.Name,
		Comment: o.Comment,
	}
}
