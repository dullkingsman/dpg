package ir

import "github.com/dullkingsman/dpg/internal/pipeline"

// ResolveLikeClauses implements pipeline.IRBuilder.
func (b *Builder) ResolveLikeClauses(objects []pipeline.IRObject) error {
	return ResolveLikeClauses(objects)
}

// ResolveLikeClauses resolves every Table's pending LikeClauses (Section
// 7.1's `LIKE source_table [{INCLUDING|EXCLUDING} attr ...]`) into concrete
// Columns/Constraints, splicing them in at the position the clause appeared.
// Must run once, after every object in the compile unit has been built and
// merged (a Builder builds one object at a time with no visibility into any
// other declared table) — see Table.LikeClauses' doc comment.
//
// The source table may itself have unresolved LikeClauses (a LIKE chain);
// those are resolved first, recursively, with cycle detection. A source
// table not found among objects is a compile error — DPG is offline-first,
// so a LIKE target must be a table declared somewhere in the same compile
// unit, not something only a live catalog could resolve.
func ResolveLikeClauses(objects []pipeline.IRObject) error {
	tables := make(map[string]*Table)
	for _, obj := range objects {
		if t, ok := obj.(*Table); ok {
			tables[t.QualifiedName()] = t
		}
	}
	resolved := make(map[string]bool)
	visiting := make(map[string]bool)
	for _, t := range tables {
		if err := resolveTableLikes(t, tables, resolved, visiting); err != nil {
			return err
		}
	}
	return nil
}

func resolveTableLikes(t *Table, tables map[string]*Table, resolved, visiting map[string]bool) error {
	key := t.QualifiedName()
	if resolved[key] {
		return nil
	}
	if len(t.LikeClauses) == 0 {
		resolved[key] = true
		return nil
	}
	if visiting[key] {
		return pipeline.Errorf(t.SrcPos, "circular LIKE reference involving table %q", key)
	}
	visiting[key] = true

	// Resolve in original declaration order, adjusting each subsequent
	// InsertAt by how many columns earlier clauses in this same table have
	// already spliced in.
	offset := 0
	for _, lc := range t.LikeClauses {
		srcSchema := lc.SourceSchema
		if srcSchema == "" {
			srcSchema = t.Schema
		}
		srcKey := qualName(srcSchema, lc.SourceName)
		src, ok := tables[srcKey]
		if !ok {
			visiting[key] = false
			return pipeline.Errorf(lc.Pos, "LIKE %q on table %q does not match any declared table", srcKey, key)
		}
		if err := resolveTableLikes(src, tables, resolved, visiting); err != nil {
			return err
		}

		insertAt := lc.InsertAt + offset
		newCols := cloneLikeColumns(src.Columns, lc.Options, lc.Pos)
		t.Columns = spliceColumns(t.Columns, insertAt, newCols)
		offset += len(newCols)

		t.Constraints = append(t.Constraints, cloneLikeConstraints(src.Constraints, lc.Options, lc.Pos)...)
	}
	t.LikeClauses = nil
	visiting[key] = false
	resolved[key] = true
	return nil
}

func spliceColumns(cols []*Column, at int, ins []*Column) []*Column {
	if at > len(cols) {
		at = len(cols)
	}
	out := make([]*Column, 0, len(cols)+len(ins))
	out = append(out, cols[:at]...)
	out = append(out, ins...)
	out = append(out, cols[at:]...)
	return out
}

// cloneLikeColumns copies source columns per RFC Section 7.1's LIKE
// semantics: name, type, and NOT NULL are always copied; every other
// property is copied only when its corresponding INCLUDING option is set —
// matching real PostgreSQL's own documented default (copy names/types/
// not-null only) and per-option behavior exactly.
func cloneLikeColumns(src []*Column, opts uint32, pos pipeline.SourcePos) []*Column {
	out := make([]*Column, 0, len(src))
	for _, c := range src {
		nc := &Column{
			Name:    c.Name,
			Type:    c.Type,
			NotNull: c.NotNull,
			Serial:  clonePtrStr(c.Serial),
			SrcPos:  pos,
		}
		if opts&LikeIncludeDefaults != 0 {
			nc.Default = clonePtrStr(c.Default)
		}
		if opts&LikeIncludeGenerated != 0 && c.Generated != nil {
			g := *c.Generated
			nc.Generated = &g
		}
		if opts&LikeIncludeIdentity != 0 && c.Identity != nil {
			id := *c.Identity
			nc.Identity = &id
		}
		if opts&LikeIncludeStorage != 0 {
			nc.Storage = clonePtrStr(c.Storage)
			nc.StorageIsTypeDefault = c.StorageIsTypeDefault
		}
		if opts&LikeIncludeCompression != 0 {
			nc.Compression = clonePtrStr(c.Compression)
		}
		if opts&LikeIncludeComments != 0 {
			nc.Comment = clonePtrStr(c.Comment)
		}
		out = append(out, nc)
	}
	return out
}

// cloneLikeConstraints copies CHECK constraints (CONSTRAINTS option) and
// PRIMARY KEY/UNIQUE/EXCLUDE constraints (the constraint-shaped half of the
// INDEXES option) with Name cleared — real PostgreSQL "chooses [names for
// the new indexes and constraints] according to the default rules,
// regardless of how the originals were named," and DPG already predicts
// that same auto-naming for any unnamed constraint (pgAutoConstraintName in
// internal/diff), so clearing Name here reuses that existing machinery
// rather than needing a new one.
//
// Deliberately out of scope, same as PostgreSQL's own INDEXES option not
// being fully mirrored here: plain (non-constraint-backed) index copying
// and the STATISTICS option (extended statistics objects). Both would need
// PostgreSQL's index/statistics auto-naming replicated for a different IR
// shape (ir.Index, ir.StatisticsObject) than the constraint-naming
// machinery already built; tracked as a follow-up, not silently attempted
// half-right.
func cloneLikeConstraints(src []*Constraint, opts uint32, pos pipeline.SourcePos) []*Constraint {
	var out []*Constraint
	for _, c := range src {
		switch c.Type {
		case "CHECK":
			if opts&LikeIncludeConstraints == 0 {
				continue
			}
		case "PRIMARY KEY", "UNIQUE", "EXCLUDE":
			if opts&LikeIncludeIndexes == 0 {
				continue
			}
		default:
			continue
		}
		nc := &Constraint{
			Type:              c.Type,
			Expr:              c.Expr,
			Columns:           append([]string(nil), c.Columns...),
			CheckColumn:       clonePtrStr(c.CheckColumn),
			NotValid:          c.NotValid,
			Deferrable:        c.Deferrable,
			InitiallyDeferred: c.InitiallyDeferred,
			Pos:               pos,
		}
		if c.Exclude != nil {
			ex := *c.Exclude
			ex.Elements = append([]ExcludeElement(nil), c.Exclude.Elements...)
			nc.Exclude = &ex
		}
		if opts&LikeIncludeComments != 0 {
			nc.Comment = clonePtrStr(c.Comment)
		}
		out = append(out, nc)
	}
	return out
}

func clonePtrStr(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}
