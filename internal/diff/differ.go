// Package diff implements pipeline.Differ. It compares a slice of desired
// IRObjects against a pipeline.Snapshot and produces an ordered list of DiffOps
// representing the minimal set of DDL changes needed.
package diff

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/snapshot"
)

func init() {
	pipeline.Default.Register(pipeline.KeyDiffer, New())
}

// op implements pipeline.DiffOp.
type op struct {
	sql    string
	safety pipeline.Safety
	pos    pipeline.SourcePos
	txn    bool
}

func (o *op) SQL() string             { return o.sql }
func (o *op) Safety() pipeline.Safety { return o.safety }
func (o *op) Pos() pipeline.SourcePos { return o.pos }
func (o *op) Transactional() bool     { return o.txn }

func safeOp(sql string, pos pipeline.SourcePos) *op {
	return &op{sql: sql, safety: pipeline.Safe, pos: pos, txn: true}
}
func cautionOp(sql string, pos pipeline.SourcePos) *op {
	return &op{sql: sql, safety: pipeline.Caution, pos: pos, txn: true}
}
func destructiveOp(sql string, pos pipeline.SourcePos) *op {
	return &op{sql: sql, safety: pipeline.Destructive, pos: pos, txn: true}
}
func manualOp(sql string, pos pipeline.SourcePos) *op {
	return &op{sql: sql, safety: pipeline.Manual, pos: pos, txn: false}
}

// Differ implements pipeline.Differ.
type Differ struct{}

// New returns a Differ.
func New() *Differ { return &Differ{} }

// Diff compares desired IR state against snap and returns ordered DiffOps.
func (d *Differ) Diff(desired []pipeline.IRObject, snap *pipeline.Snapshot) ([]pipeline.DiffOp, error) {
	var ops []pipeline.DiffOp

	desiredByName := make(map[string]pipeline.IRObject, len(desired))
	for _, obj := range desired {
		desiredByName[obj.QualifiedName()] = obj
	}

	// consumed tracks snapshot keys claimed by a rename, so they are not dropped.
	consumed := make(map[string]bool)

	// Pass 1: handle object renames.
	//
	// A rename is detected when desired has RenamedFrom, the snapshot has the
	// OLD key, and the snapshot does NOT yet have the NEW key. After a rename
	// is applied, the snapshot is rewritten to use the new key — so on every
	// subsequent run the new key IS present and the old key is gone. RFC §7.4
	// step 5 says a stale RENAMED FROM is a compiler error; the trick is to
	// distinguish "stale because user typo'd" from "stale because the rename
	// already happened." We use the new key's presence as the discriminator:
	//
	//   • new in snap                → rename already landed (or no-op); skip
	//                                   directive validation, fall through to
	//                                   the normal alter pipeline.
	//   • new not in snap, old in    → State A, fresh rename; emit it.
	//   • new not in snap, old not   → State C, stale/typo'd directive on a
	//                                   brand-new object; error.
	//
	// State D (both in snap, e.g. a hand-edited snapshot or a partial apply)
	// is intentionally NOT an error: the new key already exists, so we treat
	// it as a post-apply state and let Pass 2 drop the orphaned old key.
	for _, obj := range desired {
		oldKey := renamedFromKey(obj)
		if oldKey == "" {
			continue
		}
		newKey := obj.QualifiedName()

		var oldSnap snapshot.SnapObject
		oldFound, _ := snap.GetObject(oldKey, &oldSnap)
		var newSnap snapshot.SnapObject
		newFound, _ := snap.GetObject(newKey, &newSnap)

		if newFound {
			// Post-apply (or State D): nothing to rename. Don't consume the
			// old key — if it still exists in snap, Pass 2 will drop it.
			continue
		}
		if !oldFound {
			return nil, pipeline.Errorf(obj.Pos(),
				"RENAMED FROM %q on %s %q does not match the snapshot — neither the old nor the new name exists there. Remove RENAMED FROM if this is a genuinely new object.",
				oldKey, describeKind(obj), newKey)
		}
		consumed[oldKey] = true
		// Route to diffObject; individual diff functions emit RENAME when
		// the snap name differs from the desired name.
		alterOps, err := diffObject(obj, &oldSnap)
		if err != nil {
			return nil, err
		}
		ops = append(ops, alterOps...)
	}

	// Pass 2: drop objects in snapshot that are absent from desired and not consumed.
	for key, raw := range snap.Objects {
		if consumed[key] {
			continue
		}
		if _, ok := desiredByName[key]; ok {
			continue
		}
		var so snapshot.SnapObject
		if err := json.Unmarshal(raw, &so); err != nil {
			return nil, fmt.Errorf("diff: corrupted snapshot entry %q: %w", key, err)
		}
		ops = append(ops, dropObject(key, &so)...)
	}

	// Pass 3: create new or alter existing objects.
	for _, obj := range desired {
		// Skip objects already handled in pass 1.
		if oldKey := renamedFromKey(obj); oldKey != "" && consumed[oldKey] {
			continue
		}
		key := obj.QualifiedName()
		var so snapshot.SnapObject
		found, err := snap.GetObject(key, &so)
		if err != nil {
			return nil, fmt.Errorf("diff: decoding snapshot for %q: %w", key, err)
		}
		if !found {
			createOps, err := createObject(obj)
			if err != nil {
				return nil, err
			}
			ops = append(ops, createOps...)
		} else {
			alterOps, err := diffObject(obj, &so)
			if err != nil {
				return nil, err
			}
			ops = append(ops, alterOps...)
		}
	}

	return ops, nil
}

// renamedFromKey returns the snapshot key under the OLD name for objects that
// carry a RENAMED FROM directive. Returns "" if the object has no such field.
func renamedFromKey(obj pipeline.IRObject) string {
	switch o := obj.(type) {
	case *ir.Table:
		if o.RenamedFrom != nil {
			return qualKey(o.Schema, *o.RenamedFrom)
		}
	case *ir.Schema:
		if o.RenamedFrom != nil {
			return *o.RenamedFrom
		}
	case *ir.View:
		if o.RenamedFrom != nil {
			return qualKey(o.Schema, *o.RenamedFrom)
		}
	case *ir.Function:
		if o.RenamedFrom != nil {
			return qualKey(o.Schema, *o.RenamedFrom)
		}
	}
	return ""
}

func qualKey(schema, name string) string {
	if schema == "" {
		return name
	}
	return schema + "." + name
}

// describeKind returns a lowercase noun for an IRObject — used in user-facing
// error messages so "RENAMED FROM ..." failures name the kind concretely
// ("table", "column", ...) instead of a generic "object".
func describeKind(obj pipeline.IRObject) string {
	switch obj.(type) {
	case *ir.Table:
		return "table"
	case *ir.Schema:
		return "schema"
	case *ir.View:
		return "view"
	case *ir.Function:
		return "function"
	}
	return "object"
}

// ── SQL identifier helpers ─────────────────────────────────────────────────────

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func qualIdent(schema, name string) string {
	if schema == "" {
		return quoteIdent(name)
	}
	return quoteIdent(schema) + "." + quoteIdent(name)
}

// quoteLit single-quotes a SQL string literal.
func quoteLit(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func ptrEq(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func normalizeWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ── Grant helpers ─────────────────────────────────────────────────────────────

// grantKey returns a canonical string key for a grant entry, allowing grant
// sets to be compared regardless of ordering in the source file.
func grantKey(privs []string, roles []string, withGrant bool) string {
	p := append([]string(nil), privs...)
	sort.Strings(p)
	r := append([]string(nil), roles...)
	sort.Strings(r)
	if len(p) == 0 {
		p = []string{"ALL"}
	}
	wg := ""
	if withGrant {
		wg = "+wg"
	}
	return strings.Join(p, ",") + "|" + strings.Join(r, ",") + wg
}

// privStr returns the SQL privilege list, or ALL for an empty list.
func privStr(privs []string) string {
	if len(privs) == 0 {
		return "ALL"
	}
	return strings.Join(privs, ", ")
}

// roleList quotes and joins a list of role names.
func roleList(roles []string) string {
	quoted := make([]string, len(roles))
	for i, r := range roles {
		quoted[i] = quoteIdent(r)
	}
	return strings.Join(quoted, ", ")
}

// diffGrantSet diffs two grant lists and emits GRANT/REVOKE ops.
// onClause is the SQL object specifier after ON, e.g. "TABLE \"public\".\"users\"".
func diffGrantSet(
	snapGrants []snapshot.SnapGrant,
	desiredGrants []ir.Grant,
	onClause string,
	pos pipeline.SourcePos,
) []pipeline.DiffOp {
	var ops []pipeline.DiffOp

	snapByKey := make(map[string]snapshot.SnapGrant, len(snapGrants))
	for _, g := range snapGrants {
		snapByKey[grantKey(g.Privileges, g.Roles, g.WithGrant)] = g
	}
	desiredByKey := make(map[string]ir.Grant, len(desiredGrants))
	for _, g := range desiredGrants {
		desiredByKey[grantKey(g.Privileges, g.Roles, g.WithGrant)] = g
	}

	for k, sg := range snapByKey {
		if _, ok := desiredByKey[k]; !ok {
			ops = append(ops, safeOp(
				fmt.Sprintf("REVOKE %s ON %s FROM %s;", privStr(sg.Privileges), onClause, roleList(sg.Roles)),
				pos,
			))
		}
	}
	for k, g := range desiredByKey {
		if _, ok := snapByKey[k]; !ok {
			sql := fmt.Sprintf("GRANT %s ON %s TO %s", privStr(g.Privileges), onClause, roleList(g.Roles))
			if g.WithGrant {
				sql += " WITH GRANT OPTION"
			}
			ops = append(ops, safeOp(sql+";", pos))
		}
	}
	return ops
}

// ── DROP operations ───────────────────────────────────────────────────────────

func dropObject(key string, so *snapshot.SnapObject) []pipeline.DiffOp {
	zero := pipeline.SourcePos{}
	switch so.Kind {
	case "schema":
		if so.Schema != nil {
			return []pipeline.DiffOp{
				destructiveOp(fmt.Sprintf("DROP SCHEMA IF EXISTS %s;", quoteIdent(so.Schema.Name)), zero),
			}
		}
	case "extension":
		if so.Extension != nil {
			return []pipeline.DiffOp{
				destructiveOp(fmt.Sprintf("DROP EXTENSION IF EXISTS %s;", quoteIdent(so.Extension.Name)), zero),
			}
		}
	case "table":
		if so.Table != nil {
			t := so.Table
			suffix := ""
			if t.DropCascade {
				suffix = " CASCADE"
			}
			return []pipeline.DiffOp{
				destructiveOp(fmt.Sprintf("DROP TABLE IF EXISTS %s%s;", qualIdent(t.Schema, t.Name), suffix), zero),
			}
		}
	case "view":
		if so.View != nil {
			v := so.View
			return []pipeline.DiffOp{
				destructiveOp(fmt.Sprintf("DROP VIEW IF EXISTS %s;", qualIdent(v.Schema, v.Name)), zero),
			}
		}
	case "function":
		if so.Function != nil {
			f := so.Function
			return []pipeline.DiffOp{
				destructiveOp(fmt.Sprintf("DROP FUNCTION IF EXISTS %s(%s);", qualIdent(f.Schema, f.Name), f.Args), zero),
			}
		}
	case "type":
		if so.Type != nil {
			t := so.Type
			return []pipeline.DiffOp{
				destructiveOp(fmt.Sprintf("DROP TYPE IF EXISTS %s;", qualIdent(t.Schema, t.Name)), zero),
			}
		}
	case "sequence":
		if so.Sequence != nil {
			s := so.Sequence
			return []pipeline.DiffOp{
				destructiveOp(fmt.Sprintf("DROP SEQUENCE IF EXISTS %s;", qualIdent(s.Schema, s.Name)), zero),
			}
		}
	case "role":
		if so.Role != nil {
			return []pipeline.DiffOp{
				destructiveOp(fmt.Sprintf("DROP ROLE IF EXISTS %s;", quoteIdent(so.Role.Name)), zero),
			}
		}
	case "procedure":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(
				fmt.Sprintf("DROP PROCEDURE IF EXISTS %s(%s);", qualIdent(so.Opaque.Schema, so.Opaque.Name), so.Opaque.Args),
				zero,
			)}
		}
	case "aggregate":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(
				fmt.Sprintf("DROP AGGREGATE IF EXISTS %s(%s);", qualIdent(so.Opaque.Schema, so.Opaque.Name), so.Opaque.Args),
				zero,
			)}
		}
	case "tablespace":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP TABLESPACE IF EXISTS %s;", quoteIdent(so.Opaque.Name)), zero)}
		}
	case "fdw":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP FOREIGN DATA WRAPPER IF EXISTS %s;", quoteIdent(so.Opaque.Name)), zero)}
		}
	case "server":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP SERVER IF EXISTS %s;", quoteIdent(so.Opaque.Name)), zero)}
		}
	case "user_mapping":
		if so.Opaque != nil {
			parts := strings.SplitN(so.Opaque.Name, "@", 2)
			if len(parts) == 2 {
				return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP USER MAPPING IF EXISTS FOR %s SERVER %s;", quoteIdent(parts[0]), quoteIdent(parts[1])), zero)}
			}
		}
	case "publication":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP PUBLICATION IF EXISTS %s;", quoteIdent(so.Opaque.Name)), zero)}
		}
	case "subscription":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP SUBSCRIPTION IF EXISTS %s;", quoteIdent(so.Opaque.Name)), zero)}
		}
	case "event_trigger":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP EVENT TRIGGER IF EXISTS %s;", quoteIdent(so.Opaque.Name)), zero)}
		}
	case "collation":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP COLLATION IF EXISTS %s;", qualIdent(so.Opaque.Schema, so.Opaque.Name)), zero)}
		}
	case "operator":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP OPERATOR IF EXISTS %s;", qualIdent(so.Opaque.Schema, so.Opaque.Name)), zero)}
		}
	case "operator_class":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP OPERATOR CLASS IF EXISTS %s USING btree;", qualIdent(so.Opaque.Schema, so.Opaque.Name)), zero)}
		}
	case "operator_family":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP OPERATOR FAMILY IF EXISTS %s USING btree;", qualIdent(so.Opaque.Schema, so.Opaque.Name)), zero)}
		}
	case "cast":
		if so.Opaque != nil {
			parts := strings.SplitN(so.Opaque.Name, "->", 2)
			if len(parts) == 2 {
				return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP CAST IF EXISTS (%s AS %s);", parts[0], parts[1]), zero)}
			}
		}
	case "statistics":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP STATISTICS IF EXISTS %s;", qualIdent(so.Opaque.Schema, so.Opaque.Name)), zero)}
		}
	case "ts_config":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP TEXT SEARCH CONFIGURATION IF EXISTS %s;", qualIdent(so.Opaque.Schema, so.Opaque.Name)), zero)}
		}
	case "ts_dict":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP TEXT SEARCH DICTIONARY IF EXISTS %s;", qualIdent(so.Opaque.Schema, so.Opaque.Name)), zero)}
		}
	case "ts_parser":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP TEXT SEARCH PARSER IF EXISTS %s;", qualIdent(so.Opaque.Schema, so.Opaque.Name)), zero)}
		}
	case "ts_template":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP TEXT SEARCH TEMPLATE IF EXISTS %s;", qualIdent(so.Opaque.Schema, so.Opaque.Name)), zero)}
		}
	}
	return nil
}

// ── CREATE operations ─────────────────────────────────────────────────────────

func createObject(obj pipeline.IRObject) ([]pipeline.DiffOp, error) {
	switch o := obj.(type) {
	case *ir.Schema:
		return createSchema(o), nil
	case *ir.Extension:
		return createExtension(o), nil
	case *ir.Table:
		return createTable(o), nil
	case *ir.View:
		return createView(o), nil
	case *ir.Function:
		return createFunction(o), nil
	case *ir.Type:
		return createType(o), nil
	case *ir.Sequence:
		return createSequence(o), nil
	case *ir.Role:
		return createRole(o), nil
	case *ir.Procedure:
		return createProcedure(o), nil
	case *ir.Aggregate:
		return createOpaque(o.QualifiedName(), o.Body, "aggregate", o.SrcPos)
	case *ir.Tablespace:
		return createOpaque(o.Name, o.Body, "TABLESPACE", o.SrcPos)
	case *ir.ForeignDataWrapper:
		return createOpaque(o.Name, o.Body, "FOREIGN DATA WRAPPER", o.SrcPos)
	case *ir.ForeignServer:
		return createOpaque(o.Name, o.Body, "SERVER", o.SrcPos)
	case *ir.UserMapping:
		return createOpaque(o.QualifiedName(), o.Body, "USER MAPPING", o.SrcPos)
	case *ir.Publication:
		return createOpaque(o.Name, o.Body, "PUBLICATION", o.SrcPos)
	case *ir.Subscription:
		return createOpaque(o.Name, o.Body, "SUBSCRIPTION", o.SrcPos)
	case *ir.EventTrigger:
		return createOpaque(o.Name, o.Body, "EVENT TRIGGER", o.SrcPos)
	case *ir.Collation:
		return createOpaque(o.QualifiedName(), o.Body, "COLLATION", o.SrcPos)
	case *ir.Operator:
		return createOpaque(o.QualifiedName(), o.Body, "OPERATOR", o.SrcPos)
	case *ir.OperatorClass:
		return createOpaque(o.QualifiedName(), o.Body, "OPERATOR CLASS", o.SrcPos)
	case *ir.OperatorFamily:
		return createOpaque(o.QualifiedName(), o.Body, "OPERATOR FAMILY", o.SrcPos)
	case *ir.Cast:
		return createOpaque(o.QualifiedName(), o.Body, "CAST", o.SrcPos)
	case *ir.StatisticsObject:
		return createOpaque(o.QualifiedName(), o.Body, "STATISTICS", o.SrcPos)
	case *ir.TSConfig:
		return createOpaque(o.QualifiedName(), o.Body, "TEXT SEARCH CONFIGURATION", o.SrcPos)
	case *ir.TSDict:
		return createOpaque(o.QualifiedName(), o.Body, "TEXT SEARCH DICTIONARY", o.SrcPos)
	case *ir.TSParser:
		return createOpaque(o.QualifiedName(), o.Body, "TEXT SEARCH PARSER", o.SrcPos)
	case *ir.TSTemplate:
		return createOpaque(o.QualifiedName(), o.Body, "TEXT SEARCH TEMPLATE", o.SrcPos)
	case *ir.DefaultPrivileges:
		return createDefaultPrivileges(o), nil
	}
	return nil, nil
}

// createOpaque emits a CREATE statement from a pre-built Body SQL string.
// Returns an error if Body is empty — the builder failed to capture the source SQL,
// which would otherwise produce a silent no-op migration.
func createOpaque(name, body, kind string, pos pipeline.SourcePos) ([]pipeline.DiffOp, error) {
	if body != "" {
		return []pipeline.DiffOp{safeOp(body+";", pos)}, nil
	}
	return nil, fmt.Errorf("%s %s: body not captured; define it explicitly in a .dpg source file", kind, name)
}

func createProcedure(o *ir.Procedure) []pipeline.DiffOp {
	var b strings.Builder
	b.WriteString("CREATE OR REPLACE PROCEDURE ")
	b.WriteString(qualIdent(o.Schema, o.Name))
	b.WriteString("(")
	for i, a := range o.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		if a.Mode != "" && a.Mode != "IN" {
			b.WriteString(a.Mode)
			b.WriteString(" ")
		}
		if a.Name != "" {
			b.WriteString(a.Name)
			b.WriteString(" ")
		}
		b.WriteString(a.Type.String())
	}
	b.WriteString(") LANGUAGE ")
	b.WriteString(o.Attrs.Language)
	b.WriteString(" AS $$")
	b.WriteString(o.Attrs.Body)
	b.WriteString("$$;")
	ops := []pipeline.DiffOp{safeOp(b.String(), o.SrcPos)}
	if o.Comment != nil {
		sig := qualIdent(o.Schema, o.Name) + "("
		ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON PROCEDURE %s IS %s;", sig+")", quoteLit(*o.Comment)), o.SrcPos))
	}
	return ops
}

func createDefaultPrivileges(o *ir.DefaultPrivileges) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	pos := o.SrcPos
	for _, g := range o.Grants {
		var b strings.Builder
		b.WriteString("ALTER DEFAULT PRIVILEGES")
		if o.ForRole != nil {
			b.WriteString(" FOR ROLE ")
			b.WriteString(quoteIdent(*o.ForRole))
		}
		if o.InSchema != nil {
			b.WriteString(" IN SCHEMA ")
			b.WriteString(quoteIdent(*o.InSchema))
		}
		b.WriteString(" GRANT ")
		if len(g.Privileges) == 0 {
			b.WriteString("ALL")
		} else {
			b.WriteString(strings.Join(g.Privileges, ", "))
		}
		b.WriteString(" ON ")
		b.WriteString(o.ObjectType)
		b.WriteString(" TO ")
		roles := make([]string, len(g.Roles))
		for i, r := range g.Roles {
			roles[i] = quoteIdent(r)
		}
		b.WriteString(strings.Join(roles, ", "))
		if g.WithGrant {
			b.WriteString(" WITH GRANT OPTION")
		}
		b.WriteString(";")
		ops = append(ops, safeOp(b.String(), pos))
	}
	return ops
}

func createSchema(o *ir.Schema) []pipeline.DiffOp {
	var b strings.Builder
	b.WriteString("CREATE SCHEMA IF NOT EXISTS ")
	b.WriteString(quoteIdent(o.Name))
	if o.Owner != nil {
		b.WriteString(" AUTHORIZATION ")
		b.WriteString(quoteIdent(*o.Owner))
	}
	b.WriteString(";")
	ops := []pipeline.DiffOp{safeOp(b.String(), o.SrcPos)}
	if o.Comment != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("COMMENT ON SCHEMA %s IS %s;", quoteIdent(o.Name), quoteLit(*o.Comment)),
			o.SrcPos,
		))
	}
	return ops
}

func createExtension(o *ir.Extension) []pipeline.DiffOp {
	var b strings.Builder
	b.WriteString("CREATE EXTENSION IF NOT EXISTS ")
	b.WriteString(quoteIdent(o.Name))
	if o.Schema != nil {
		b.WriteString(" SCHEMA ")
		b.WriteString(quoteIdent(*o.Schema))
	}
	if o.Version != nil {
		b.WriteString(" VERSION '")
		b.WriteString(*o.Version)
		b.WriteString("'")
	}
	b.WriteString(";")
	return []pipeline.DiffOp{safeOp(b.String(), o.SrcPos)}
}

func createTable(o *ir.Table) []pipeline.DiffOp {
	var b strings.Builder
	switch {
	case o.Unlogged:
		b.WriteString("CREATE UNLOGGED TABLE ")
	case o.Foreign:
		b.WriteString("CREATE FOREIGN TABLE ")
	default:
		b.WriteString("CREATE TABLE ")
	}
	b.WriteString(qualIdent(o.Schema, o.Name))
	b.WriteString(" (")
	for i, col := range o.Columns {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n    ")
		b.WriteString(quoteIdent(col.Name))
		b.WriteString(" ")
		b.WriteString(col.Type.String())
		if col.NotNull {
			b.WriteString(" NOT NULL")
		}
		if col.Default != nil {
			b.WriteString(" DEFAULT ")
			b.WriteString(*col.Default)
		}
		if col.Identity != nil {
			if col.Identity.Always {
				b.WriteString(" GENERATED ALWAYS AS IDENTITY")
			} else {
				b.WriteString(" GENERATED BY DEFAULT AS IDENTITY")
			}
		}
		if col.Generated != nil {
			b.WriteString(" GENERATED ALWAYS AS (")
			b.WriteString(col.Generated.Expr)
			b.WriteString(") STORED")
		}
	}
	for _, cst := range o.Constraints {
		b.WriteString(",\n    ")
		if cst.Name != "" {
			b.WriteString("CONSTRAINT ")
			b.WriteString(quoteIdent(cst.Name))
			b.WriteString(" ")
		}
		b.WriteString(cst.Expr)
	}
	b.WriteString("\n);")

	var ops []pipeline.DiffOp
	ops = append(ops, safeOp(b.String(), o.SrcPos))

	if o.Owner != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("ALTER TABLE %s OWNER TO %s;", qualIdent(o.Schema, o.Name), quoteIdent(*o.Owner)),
			o.SrcPos,
		))
	}
	if o.Comment != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("COMMENT ON TABLE %s IS %s;", qualIdent(o.Schema, o.Name), quoteLit(*o.Comment)),
			o.SrcPos,
		))
	}
	for _, col := range o.Columns {
		if col.Comment != nil {
			ops = append(ops, safeOp(
				fmt.Sprintf("COMMENT ON COLUMN %s.%s IS %s;",
					qualIdent(o.Schema, o.Name), quoteIdent(col.Name), quoteLit(*col.Comment)),
				col.SrcPos,
			))
		}
	}
	if o.RLSEnabled {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY;", qualIdent(o.Schema, o.Name)), o.SrcPos))
	}
	if o.RLSForced {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER TABLE %s FORCE ROW LEVEL SECURITY;", qualIdent(o.Schema, o.Name)), o.SrcPos))
	}
	for _, idx := range o.Indexes {
		ops = append(ops, createIndex(o.Schema, o.Name, idx)...)
	}
	for _, pol := range o.Policies {
		ops = append(ops, createPolicy(o.Schema, o.Name, pol)...)
	}
	for _, trg := range o.Triggers {
		ops = append(ops, createTrigger(o.Schema, o.Name, trg)...)
	}
	for _, g := range o.Grants {
		ops = append(ops, tableGrantOp(g, qualIdent(o.Schema, o.Name), o.SrcPos))
	}
	return ops
}

func createIndex(schema, table string, idx *ir.Index) []pipeline.DiffOp {
	var b strings.Builder
	b.WriteString("CREATE ")
	if idx.Unique {
		b.WriteString("UNIQUE ")
	}
	b.WriteString("INDEX ")
	if idx.Concurrently {
		b.WriteString("CONCURRENTLY ")
	}
	b.WriteString(quoteIdent(idx.Name))
	b.WriteString(" ON ")
	b.WriteString(qualIdent(schema, table))
	if idx.Method != "" && idx.Method != "btree" {
		b.WriteString(" USING ")
		b.WriteString(idx.Method)
	}
	b.WriteString(" (")
	for i, col := range idx.Columns {
		if i > 0 {
			b.WriteString(", ")
		}
		if col.Name != "" {
			b.WriteString(quoteIdent(col.Name))
		} else if col.Expr != nil {
			b.WriteString("(")
			b.WriteString(col.Expr.Text)
			b.WriteString(")")
		}
		if col.SortOrder != "" {
			b.WriteString(" ")
			b.WriteString(col.SortOrder)
		}
		if col.Nulls != "" {
			b.WriteString(" NULLS ")
			b.WriteString(col.Nulls)
		}
	}
	b.WriteString(");")

	if idx.Concurrently {
		return []pipeline.DiffOp{manualOp(b.String(), idx.Pos)}
	}
	return []pipeline.DiffOp{cautionOp(b.String(), idx.Pos)}
}

func createPolicy(schema, table string, pol *ir.Policy) []pipeline.DiffOp {
	tbl := qualIdent(schema, table)
	var b strings.Builder
	b.WriteString("CREATE POLICY ")
	b.WriteString(quoteIdent(pol.Name))
	b.WriteString(" ON ")
	b.WriteString(tbl)
	if !pol.Permissive {
		b.WriteString(" AS RESTRICTIVE")
	}
	if pol.Command != "" && pol.Command != "ALL" {
		b.WriteString(" FOR ")
		b.WriteString(pol.Command)
	}
	if len(pol.Roles) > 0 {
		b.WriteString(" TO ")
		for i, r := range pol.Roles {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(quoteIdent(r))
		}
	}
	if pol.Using != nil {
		b.WriteString(" USING (")
		b.WriteString(*pol.Using)
		b.WriteString(")")
	}
	if pol.WithCheck != nil {
		b.WriteString(" WITH CHECK (")
		b.WriteString(*pol.WithCheck)
		b.WriteString(")")
	}
	b.WriteString(";")
	return []pipeline.DiffOp{safeOp(b.String(), pol.Pos)}
}

func createTrigger(schema, table string, trg *ir.Trigger) []pipeline.DiffOp {
	var b strings.Builder
	b.WriteString("CREATE TRIGGER ")
	b.WriteString(quoteIdent(trg.Name))
	b.WriteString(" ")
	b.WriteString(trg.When)
	b.WriteString(" ")
	b.WriteString(strings.Join(trg.Events, " OR "))
	b.WriteString(" ON ")
	b.WriteString(qualIdent(schema, table))
	b.WriteString(" FOR EACH ")
	b.WriteString(trg.ForEach)
	if trg.Condition != nil {
		b.WriteString(" WHEN (")
		b.WriteString(*trg.Condition)
		b.WriteString(")")
	}
	b.WriteString(" EXECUTE FUNCTION ")
	b.WriteString(trg.Function)
	b.WriteString("(")
	b.WriteString(strings.Join(trg.Args, ", "))
	b.WriteString(");")
	return []pipeline.DiffOp{safeOp(b.String(), trg.Pos)}
}

func tableGrantOp(g ir.Grant, tblIdent string, pos pipeline.SourcePos) *op {
	var privs string
	if len(g.Privileges) == 0 {
		privs = "ALL"
	} else {
		privs = strings.Join(g.Privileges, ", ")
	}
	roles := make([]string, len(g.Roles))
	for i, r := range g.Roles {
		roles[i] = quoteIdent(r)
	}
	sql := fmt.Sprintf("GRANT %s ON TABLE %s TO %s", privs, tblIdent, strings.Join(roles, ", "))
	if g.WithGrant {
		sql += " WITH GRANT OPTION"
	}
	sql += ";"
	return safeOp(sql, pos)
}

func createView(o *ir.View) []pipeline.DiffOp {
	var b strings.Builder
	b.WriteString("CREATE ")
	if o.Materialized {
		b.WriteString("MATERIALIZED ")
	} else if o.Recursive {
		b.WriteString("RECURSIVE ")
	}
	b.WriteString("VIEW ")
	b.WriteString(qualIdent(o.Schema, o.Name))
	b.WriteString(" AS ")
	// Strip trailing semicolons from the query — we control the final delimiter.
	b.WriteString(strings.TrimRight(strings.TrimSpace(o.Query), ";"))
	if o.Materialized && o.WithNoData {
		b.WriteString(" WITH NO DATA")
	}
	b.WriteString(";")
	ops := []pipeline.DiffOp{safeOp(b.String(), o.SrcPos)}
	viewKind := "VIEW"
	if o.Materialized {
		viewKind = "MATERIALIZED VIEW"
	}
	if o.Comment != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("COMMENT ON %s %s IS %s;", viewKind, qualIdent(o.Schema, o.Name), quoteLit(*o.Comment)),
			o.SrcPos,
		))
	}
	viewIdent := qualIdent(o.Schema, o.Name)
	for _, g := range o.Grants {
		sql := fmt.Sprintf("GRANT %s ON TABLE %s TO %s", privStr(g.Privileges), viewIdent, roleList(g.Roles))
		if g.WithGrant {
			sql += " WITH GRANT OPTION"
		}
		ops = append(ops, safeOp(sql+";", o.SrcPos))
	}
	return ops
}

func createFunction(o *ir.Function) []pipeline.DiffOp {
	ops := []pipeline.DiffOp{safeOp(buildFunctionSQL(o), o.SrcPos)}
	sig := buildFuncSignature(o)
	if o.Comment != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("COMMENT ON FUNCTION %s IS %s;", sig, quoteLit(*o.Comment)),
			o.SrcPos,
		))
	}
	for _, g := range o.Grants {
		sql := fmt.Sprintf("GRANT %s ON FUNCTION %s TO %s", privStr(g.Privileges), sig, roleList(g.Roles))
		if g.WithGrant {
			sql += " WITH GRANT OPTION"
		}
		ops = append(ops, safeOp(sql+";", o.SrcPos))
	}
	return ops
}

func buildFuncSignature(o *ir.Function) string {
	args := make([]string, 0, len(o.Args))
	for _, a := range o.Args {
		if a.Mode != "OUT" && a.Mode != "TABLE" {
			args = append(args, a.Type.String())
		}
	}
	return fmt.Sprintf("%s(%s)", qualIdent(o.Schema, o.Name), strings.Join(args, ", "))
}

func buildFunctionSQL(o *ir.Function) string {
	var b strings.Builder
	b.WriteString("CREATE OR REPLACE FUNCTION ")
	b.WriteString(qualIdent(o.Schema, o.Name))
	b.WriteString("(")
	for i, a := range o.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		if a.Mode != "" && a.Mode != "IN" {
			b.WriteString(a.Mode)
			b.WriteString(" ")
		}
		if a.Name != "" {
			b.WriteString(a.Name)
			b.WriteString(" ")
		}
		b.WriteString(a.Type.String())
		if a.Default != nil {
			b.WriteString(" DEFAULT ")
			b.WriteString(*a.Default)
		}
	}
	b.WriteString(") RETURNS ")
	b.WriteString(o.ReturnType.String())
	b.WriteString(" LANGUAGE ")
	b.WriteString(o.Attrs.Language)
	if o.Attrs.Volatility != "" && o.Attrs.Volatility != "VOLATILE" {
		b.WriteString(" ")
		b.WriteString(o.Attrs.Volatility)
	}
	if o.Attrs.Strict {
		b.WriteString(" STRICT")
	}
	if o.Attrs.SecurityDef {
		b.WriteString(" SECURITY DEFINER")
	}
	b.WriteString(" AS $$")
	b.WriteString(o.Attrs.Body)
	b.WriteString("$$;")
	return b.String()
}

func createType(o *ir.Type) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	switch o.Variant {
	case "ENUM":
		var b strings.Builder
		b.WriteString("CREATE TYPE ")
		b.WriteString(qualIdent(o.Schema, o.Name))
		b.WriteString(" AS ENUM (")
		for i, v := range o.EnumValues {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(quoteLit(v))
		}
		b.WriteString(");")
		ops = append(ops, safeOp(b.String(), o.SrcPos))
	case "DOMAIN":
		body := o.Body
		// rawSQL produces "CREATE DOMAIN unqualname AS ..."; qualify the name when schema is set.
		if o.Schema != "" && body != "" {
			unqualPrefix := "CREATE DOMAIN " + o.Name
			if strings.HasPrefix(body, unqualPrefix) {
				body = "CREATE DOMAIN " + qualIdent(o.Schema, o.Name) + body[len(unqualPrefix):]
			}
		}
		if body != "" {
			ops = append(ops, safeOp(body+";", o.SrcPos))
		}
		if o.Comment != nil {
			ops = append(ops, safeOp(
				fmt.Sprintf("COMMENT ON DOMAIN %s IS %s;", qualIdent(o.Schema, o.Name), quoteLit(*o.Comment)),
				o.SrcPos,
			))
		}
		return ops
	default:
		if o.Body != "" {
			ops = append(ops, safeOp(o.Body+";", o.SrcPos))
		}
	}
	if o.Comment != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("COMMENT ON TYPE %s IS %s;", qualIdent(o.Schema, o.Name), quoteLit(*o.Comment)),
			o.SrcPos,
		))
	}
	return ops
}

func createSequence(o *ir.Sequence) []pipeline.DiffOp {
	ops := []pipeline.DiffOp{
		safeOp(fmt.Sprintf("CREATE SEQUENCE IF NOT EXISTS %s;", qualIdent(o.Schema, o.Name)), o.SrcPos),
	}
	if o.Comment != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("COMMENT ON SEQUENCE %s IS %s;", qualIdent(o.Schema, o.Name), quoteLit(*o.Comment)),
			o.SrcPos,
		))
	}
	return ops
}

func createRole(o *ir.Role) []pipeline.DiffOp {
	ops := []pipeline.DiffOp{
		safeOp(fmt.Sprintf("CREATE ROLE %s;", quoteIdent(o.Name)), o.SrcPos),
	}
	if o.Comment != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("COMMENT ON ROLE %s IS %s;", quoteIdent(o.Name), quoteLit(*o.Comment)),
			o.SrcPos,
		))
	}
	return ops
}

// ── DIFF / ALTER operations ───────────────────────────────────────────────────

func diffObject(desired pipeline.IRObject, snap *snapshot.SnapObject) ([]pipeline.DiffOp, error) {
	switch o := desired.(type) {
	case *ir.Schema:
		if snap.Schema == nil {
			return nil, nil
		}
		return diffSchema(o, snap.Schema), nil
	case *ir.Table:
		if snap.Table == nil {
			return nil, nil
		}
		return diffTable(o, snap.Table)
	case *ir.View:
		if snap.View == nil {
			return nil, nil
		}
		return diffView(o, snap.View), nil
	case *ir.Function:
		if snap.Function == nil {
			return nil, nil
		}
		return diffFunction(o, snap.Function), nil
	case *ir.Type:
		if snap.Type == nil {
			return nil, nil
		}
		return diffType(o, snap.Type), nil
	case *ir.Procedure:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffProcedure(o, snap.Opaque)
	case *ir.Aggregate:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, o.Comment, snap.Opaque, o.SrcPos)
	case *ir.Tablespace:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.Name, o.Body, o.Comment, snap.Opaque, o.SrcPos)
	case *ir.ForeignDataWrapper:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.Name, o.Body, o.Comment, snap.Opaque, o.SrcPos)
	case *ir.ForeignServer:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.Name, o.Body, o.Comment, snap.Opaque, o.SrcPos)
	case *ir.UserMapping:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, nil, snap.Opaque, o.SrcPos)
	case *ir.Publication:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.Name, o.Body, nil, snap.Opaque, o.SrcPos)
	case *ir.Subscription:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.Name, o.Body, nil, snap.Opaque, o.SrcPos)
	case *ir.EventTrigger:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.Name, o.Body, nil, snap.Opaque, o.SrcPos)
	case *ir.Collation:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, nil, snap.Opaque, o.SrcPos)
	case *ir.Operator:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, nil, snap.Opaque, o.SrcPos)
	case *ir.OperatorClass:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, nil, snap.Opaque, o.SrcPos)
	case *ir.OperatorFamily:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, nil, snap.Opaque, o.SrcPos)
	case *ir.Cast:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, nil, snap.Opaque, o.SrcPos)
	case *ir.StatisticsObject:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, nil, snap.Opaque, o.SrcPos)
	case *ir.TSConfig:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, o.Comment, snap.Opaque, o.SrcPos)
	case *ir.TSDict:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, o.Comment, snap.Opaque, o.SrcPos)
	case *ir.TSParser:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, nil, snap.Opaque, o.SrcPos)
	case *ir.TSTemplate:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, nil, snap.Opaque, o.SrcPos)
	case *ir.DefaultPrivileges:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), "", nil, snap.Opaque, o.SrcPos)
	}
	return nil, nil
}

// diffOpaqueIR checks if the body hash has changed and emits a warning op if so.
func diffOpaqueIR(name, body string, _ *string, snap *snapshot.SnapOpaque, pos pipeline.SourcePos) ([]pipeline.DiffOp, error) {
	if body == "" {
		return nil, nil
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(body)))
	newHash := fmt.Sprintf("%x", sum)
	if newHash != snap.BodyHash {
		return []pipeline.DiffOp{destructiveOp(
			fmt.Sprintf("-- WARNING: %s body changed; manual DROP + recreate required", name),
			pos,
		)}, nil
	}
	return nil, nil
}

func diffProcedure(o *ir.Procedure, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	if o.BodyHash != snap.BodyHash || o.Attrs.Language != "" {
		ops := createProcedure(o)
		return ops, nil
	}
	return nil, nil
}

func diffSchema(o *ir.Schema, snap *snapshot.SnapSchema) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	pos := o.SrcPos

	// Rename: snap stores the old name; desired has the new name.
	if snap.Name != o.Name {
		ops = append(ops, safeOp(
			fmt.Sprintf("ALTER SCHEMA %s RENAME TO %s;", quoteIdent(snap.Name), quoteIdent(o.Name)),
			pos,
		))
	}
	if !ptrEq(o.Owner, snap.Owner) && o.Owner != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("ALTER SCHEMA %s OWNER TO %s;", quoteIdent(o.Name), quoteIdent(*o.Owner)),
			pos,
		))
	}
	if !ptrEq(o.Comment, snap.Comment) {
		if o.Comment != nil {
			ops = append(ops, safeOp(
				fmt.Sprintf("COMMENT ON SCHEMA %s IS %s;", quoteIdent(o.Name), quoteLit(*o.Comment)),
				pos,
			))
		} else {
			ops = append(ops, safeOp(
				fmt.Sprintf("COMMENT ON SCHEMA %s IS NULL;", quoteIdent(o.Name)),
				pos,
			))
		}
	}
	return ops
}

func diffView(o *ir.View, snap *snapshot.SnapView) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	pos := o.SrcPos
	tbl := qualIdent(o.Schema, o.Name)
	viewKind := "VIEW"
	if o.Materialized {
		viewKind = "MATERIALIZED VIEW"
	}

	if snap.Name != o.Name {
		ops = append(ops, safeOp(
			fmt.Sprintf("ALTER VIEW %s RENAME TO %s;", qualIdent(o.Schema, snap.Name), quoteIdent(o.Name)),
			pos,
		))
	}

	// Recursive flag change or a materialized view query change requires DROP + CREATE
	// because PG has no in-place ALTER for these.
	if snap.Recursive != o.Recursive ||
		(o.Materialized && normalizeWS(o.Query) != normalizeWS(snap.Query)) {
		ops = append(ops, destructiveOp(fmt.Sprintf("DROP %s IF EXISTS %s;", viewKind, tbl), pos))
		ops = append(ops, createView(o)...)
		// createView emits comments and grants; nothing more to do.
		return ops
	}

	if normalizeWS(o.Query) != normalizeWS(snap.Query) {
		ops = append(ops, safeOp(fmt.Sprintf("CREATE OR REPLACE VIEW %s AS %s;", tbl, o.Query), pos))
	}

	if o.Materialized && snap.WithNoData != o.WithNoData {
		ops = append(ops, manualOp(
			fmt.Sprintf("-- WITH NO DATA changed on %s %s; refresh manually: REFRESH MATERIALIZED VIEW %s;",
				viewKind, tbl, tbl),
			pos,
		))
	}

	if !ptrEq(o.Comment, snap.Comment) {
		if o.Comment != nil {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON %s %s IS %s;", viewKind, tbl, quoteLit(*o.Comment)), pos))
		} else {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON %s %s IS NULL;", viewKind, tbl), pos))
		}
	}

	ops = append(ops, diffGrantSet(snap.Grants, o.Grants, "TABLE "+tbl, pos)...)
	return ops
}

func diffFunction(o *ir.Function, snap *snapshot.SnapFunction) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	pos := o.SrcPos
	sig := buildFuncSignature(o)

	if o.BodyHash != snap.BodyHash || o.Attrs.Language != snap.Language || o.Attrs.Volatility != snap.Volatility {
		ops = append(ops, safeOp(buildFunctionSQL(o), pos))
	}
	if !ptrEq(o.Comment, snap.Comment) {
		if o.Comment != nil {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON FUNCTION %s IS %s;", sig, quoteLit(*o.Comment)), pos))
		} else {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON FUNCTION %s IS NULL;", sig), pos))
		}
	}
	ops = append(ops, diffGrantSet(snap.Grants, o.Grants, "FUNCTION "+sig, pos)...)
	return ops
}

func diffType(o *ir.Type, snap *snapshot.SnapType) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	pos := o.SrcPos
	typeIdent := qualIdent(o.Schema, o.Name)

	if o.Variant == "ENUM" && snap.Variant == "ENUM" {
		snapVals := make(map[string]bool, len(snap.Values))
		for _, v := range snap.Values {
			snapVals[v] = true
		}
		for _, v := range o.EnumValues {
			if !snapVals[v] {
				// ALTER TYPE ADD VALUE cannot run inside a transaction in PG < 16.
				ops = append(ops, manualOp(
					fmt.Sprintf("ALTER TYPE %s ADD VALUE %s;", typeIdent, quoteLit(v)),
					pos,
				))
			}
		}
	}
	if !ptrEq(o.Comment, snap.Comment) {
		if o.Comment != nil {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON TYPE %s IS %s;", typeIdent, quoteLit(*o.Comment)), pos))
		} else {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON TYPE %s IS NULL;", typeIdent), pos))
		}
	}
	return ops
}

func diffTable(o *ir.Table, snap *snapshot.SnapTable) ([]pipeline.DiffOp, error) {
	var ops []pipeline.DiffOp
	pos := o.SrcPos
	tbl := qualIdent(o.Schema, o.Name)

	// Rename: snap stores the old name.
	if snap.Name != o.Name {
		ops = append(ops, safeOp(
			fmt.Sprintf("ALTER TABLE %s RENAME TO %s;", qualIdent(o.Schema, snap.Name), quoteIdent(o.Name)),
			pos,
		))
	}
	if !ptrEq(o.Owner, snap.Owner) && o.Owner != nil {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER TABLE %s OWNER TO %s;", tbl, quoteIdent(*o.Owner)), pos))
	}
	if !ptrEq(o.Comment, snap.Comment) {
		if o.Comment != nil {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON TABLE %s IS %s;", tbl, quoteLit(*o.Comment)), pos))
		} else {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON TABLE %s IS NULL;", tbl), pos))
		}
	}

	// RLS changes.
	if o.RLSEnabled && !snap.RLSEnabled {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY;", tbl), pos))
	} else if !o.RLSEnabled && snap.RLSEnabled {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER TABLE %s DISABLE ROW LEVEL SECURITY;", tbl), pos))
	}
	if o.RLSForced && !snap.RLSForced {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER TABLE %s FORCE ROW LEVEL SECURITY;", tbl), pos))
	} else if !o.RLSForced && snap.RLSForced {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER TABLE %s NO FORCE ROW LEVEL SECURITY;", tbl), pos))
	}

	colOps, renamedCols, droppedCols, err := diffColumns(tbl, o, snap)
	if err != nil {
		return nil, err
	}
	ops = append(ops, colOps...)
	ops = append(ops, diffConstraints(tbl, o, snap, pos, renamedCols, droppedCols)...)
	ops = append(ops, diffIndexes(o.Schema, o.Name, o, snap, renamedCols, droppedCols)...)
	ops = append(ops, diffPolicies(o.Schema, o.Name, o, snap)...)
	ops = append(ops, diffTriggers(o.Schema, o.Name, o, snap)...)
	ops = append(ops, diffTableInherits(tbl, o, snap, pos)...)
	ops = append(ops, diffGrantSet(snap.Grants, o.Grants, "TABLE "+tbl, pos)...)
	return ops, nil
}

func diffTableInherits(tbl string, o *ir.Table, snap *snapshot.SnapTable, pos pipeline.SourcePos) []pipeline.DiffOp {
	var ops []pipeline.DiffOp

	snapSet := make(map[string]bool, len(snap.Inherits))
	for _, p := range snap.Inherits {
		snapSet[p] = true
	}
	desiredSet := make(map[string]bool, len(o.Inherits))
	for _, p := range o.Inherits {
		desiredSet[p] = true
	}

	for _, p := range o.Inherits {
		if !snapSet[p] {
			ops = append(ops, safeOp(fmt.Sprintf("ALTER TABLE %s INHERIT %s;", tbl, quoteIdent(p)), pos))
		}
	}
	for _, p := range snap.Inherits {
		if !desiredSet[p] {
			ops = append(ops, cautionOp(fmt.Sprintf("ALTER TABLE %s NO INHERIT %s;", tbl, quoteIdent(p)), pos))
		}
	}
	return ops
}

// diffColumns returns the column DDL ops along with a snap→desired rename map
// and the set of snapshot columns being dropped. Constraint and index diffing
// use these so a column rename doesn't fabricate spurious drop/recreate pairs,
// and so PG-cascaded objects on dropped columns aren't double-emitted.
//
// RENAMED FROM is validated using the same logic as object-level renames in
// Diff(): the new column's presence in the snapshot is the discriminator
// between "stale typo" and "rename already applied". The snapshot is rewritten
// after every apply (see snapshot.Populate) so the new column appears there
// from the next plan onward — erroring on a missing OLD name without checking
// for the NEW name would make every directive a one-shot. The collision check
// (RENAMED FROM names a column ALSO present in the desired DDL) stays
// snapshot-independent because it's incoherent intent regardless of state.
func diffColumns(tbl string, o *ir.Table, snap *snapshot.SnapTable) ([]pipeline.DiffOp, map[string]string, map[string]bool, error) {
	var ops []pipeline.DiffOp

	snapByName := make(map[string]*snapshot.SnapColumn, len(snap.Columns))
	for i := range snap.Columns {
		snapByName[snap.Columns[i].Name] = &snap.Columns[i]
	}

	desiredHasName := make(map[string]bool, len(o.Columns))
	for _, col := range o.Columns {
		desiredHasName[col.Name] = true
	}

	// Columns renamed in desired: map old→new name.
	renamedFrom := make(map[string]string) // snapName → desiredName
	for _, col := range o.Columns {
		if col.RenamedFrom == nil {
			continue
		}
		if desiredHasName[*col.RenamedFrom] {
			// Caught even in post-apply state: the user listed both old and
			// new in the table's ( ) section while also asserting a rename.
			// Snapshot state can't disambiguate this — it's always wrong.
			return nil, nil, nil, pipeline.Errorf(col.SrcPos,
				"RENAMED FROM %q on column %q in %s collides with another column of the same name in the desired DDL. Remove the stale column from the table's ( ) list.",
				*col.RenamedFrom, col.Name, tbl)
		}
		_, oldInSnap := snapByName[*col.RenamedFrom]
		_, newInSnap := snapByName[col.Name]
		if newInSnap {
			// Post-apply / no-op state: the snapshot already has the new
			// name. Don't add to the rename map (no SQL needed) and don't
			// validate the directive — the rename has already happened.
			continue
		}
		if !oldInSnap {
			return nil, nil, nil, pipeline.Errorf(col.SrcPos,
				"RENAMED FROM %q on column %q in %s does not match the snapshot — neither the old nor the new name exists there. Remove RENAMED FROM if this is a genuinely new column.",
				*col.RenamedFrom, col.Name, tbl)
		}
		renamedFrom[*col.RenamedFrom] = col.Name
		ops = append(ops, safeOp(
			fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s;",
				tbl, quoteIdent(*col.RenamedFrom), quoteIdent(col.Name)),
			col.SrcPos,
		))
	}

	desiredByName := make(map[string]*ir.Column, len(o.Columns))
	for _, col := range o.Columns {
		desiredByName[col.Name] = col
	}

	// Drop columns absent from desired (and not just renamed).
	droppedCols := make(map[string]bool)
	for _, sc := range snap.Columns {
		if _, ok := renamedFrom[sc.Name]; ok {
			continue // renamed away
		}
		if _, ok := desiredByName[sc.Name]; !ok {
			droppedCols[sc.Name] = true
			ops = append(ops, destructiveOp(
				fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", tbl, quoteIdent(sc.Name)),
				pipeline.SourcePos{},
			))
		}
	}

	// Add new columns or alter existing ones.
	for _, col := range o.Columns {
		// After a rename, the snap col is stored under the old name.
		snapColName := col.Name
		if col.RenamedFrom != nil {
			if _, ok := snapByName[*col.RenamedFrom]; ok {
				snapColName = *col.RenamedFrom
			}
		}
		sc, exists := snapByName[snapColName]

		if !exists {
			// ADD COLUMN
			var b strings.Builder
			b.WriteString(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", tbl, quoteIdent(col.Name), col.Type.String()))
			if col.NotNull && col.Default == nil && col.Identity == nil {
				b.WriteString(" NOT NULL")
			}
			if col.Default != nil {
				b.WriteString(" DEFAULT ")
				b.WriteString(*col.Default)
			}
			if col.Identity != nil {
				if col.Identity.Always {
					b.WriteString(" GENERATED ALWAYS AS IDENTITY")
				} else {
					b.WriteString(" GENERATED BY DEFAULT AS IDENTITY")
				}
			}
			b.WriteString(";")
			safety := pipeline.Safe
			if col.NotNull && col.Default == nil && col.Identity == nil {
				safety = pipeline.Caution
			}
			ops = append(ops, &op{sql: b.String(), safety: safety, pos: col.SrcPos, txn: true})
			if col.Comment != nil {
				ops = append(ops, safeOp(
					fmt.Sprintf("COMMENT ON COLUMN %s.%s IS %s;", tbl, quoteIdent(col.Name), quoteLit(*col.Comment)),
					col.SrcPos,
				))
			}
			continue
		}

		// Alter existing column.
		if col.Type.String() != sc.Type {
			using := ""
			if col.Using != nil {
				using = " USING " + *col.Using
			}
			ops = append(ops, destructiveOp(
				fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s%s;",
					tbl, quoteIdent(col.Name), col.Type.String(), using),
				col.SrcPos,
			))
		}
		if col.NotNull && !sc.NotNull {
			ops = append(ops, cautionOp(
				fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL;", tbl, quoteIdent(col.Name)),
				col.SrcPos,
			))
		} else if !col.NotNull && sc.NotNull {
			ops = append(ops, safeOp(
				fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP NOT NULL;", tbl, quoteIdent(col.Name)),
				col.SrcPos,
			))
		}
		if !ptrEq(col.Default, sc.Default) {
			if col.Default != nil {
				ops = append(ops, safeOp(
					fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s;",
						tbl, quoteIdent(col.Name), *col.Default),
					col.SrcPos,
				))
			} else {
				ops = append(ops, safeOp(
					fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT;", tbl, quoteIdent(col.Name)),
					col.SrcPos,
				))
			}
		}
		if !ptrEq(col.Comment, sc.Comment) {
			if col.Comment != nil {
				ops = append(ops, safeOp(
					fmt.Sprintf("COMMENT ON COLUMN %s.%s IS %s;", tbl, quoteIdent(col.Name), quoteLit(*col.Comment)),
					col.SrcPos,
				))
			} else {
				ops = append(ops, safeOp(
					fmt.Sprintf("COMMENT ON COLUMN %s.%s IS NULL;", tbl, quoteIdent(col.Name)),
					col.SrcPos,
				))
			}
		}
		if !ptrEq(col.Storage, sc.Storage) && col.Storage != nil {
			ops = append(ops, safeOp(
				fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET STORAGE %s;",
					tbl, quoteIdent(col.Name), *col.Storage),
				col.SrcPos,
			))
		}
		if !ptrEq(col.Compression, sc.Compression) && col.Compression != nil {
			ops = append(ops, safeOp(
				fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET COMPRESSION %s;",
					tbl, quoteIdent(col.Name), *col.Compression),
				col.SrcPos,
			))
		}
		if col.Statistics != nil && (sc.Statistics == nil || *col.Statistics != *sc.Statistics) {
			ops = append(ops, safeOp(
				fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET STATISTICS %d;",
					tbl, quoteIdent(col.Name), *col.Statistics),
				col.SrcPos,
			))
		} else if col.Statistics == nil && sc.Statistics != nil {
			// Reset to server default (-1 instructs PG to use default_statistics_target).
			ops = append(ops, safeOp(
				fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET STATISTICS -1;",
					tbl, quoteIdent(col.Name)),
				col.SrcPos,
			))
		}
	}

	return ops, renamedFrom, droppedCols, nil
}

func diffConstraints(tbl string, o *ir.Table, snap *snapshot.SnapTable, pos pipeline.SourcePos, renamedCols map[string]string, droppedCols map[string]bool) []pipeline.DiffOp {
	var ops []pipeline.DiffOp

	// Inline constraints (e.g. `id BIGINT PRIMARY KEY`) have no user-supplied
	// name, so matching by name alone would treat them as new on every run.
	// Fall back to a signature derived from type + normalized expression. The
	// snapshot expression still references pre-rename column names, so apply
	// the rename map first — otherwise a plain RENAMED FROM would surface as a
	// spurious drop+recreate of every constraint touching the renamed column.
	key := func(name, typ, expr string) string {
		if name != "" {
			return "n:" + name
		}
		return "s:" + typ + "|" + normalizeWS(expr)
	}

	snapByKey := make(map[string]*snapshot.SnapConstraint, len(snap.Constraints))
	for i := range snap.Constraints {
		sc := &snap.Constraints[i]
		snapByKey[key(sc.Name, sc.Type, translateConstraintExpr(sc.Expr, renamedCols))] = sc
	}
	desiredByKey := make(map[string]*ir.Constraint, len(o.Constraints))
	for _, c := range o.Constraints {
		desiredByKey[key(c.Name, c.Type, c.Expr)] = c
	}

	for i := range snap.Constraints {
		sc := &snap.Constraints[i]
		if _, ok := desiredByKey[key(sc.Name, sc.Type, translateConstraintExpr(sc.Expr, renamedCols))]; ok {
			continue
		}
		// PG cascades constraint removal when the underlying column is dropped.
		// If every local column referenced by this constraint is being dropped,
		// skip emitting anything — DROP COLUMN already handles it.
		if cols := localConstraintCols(sc.Expr); allDropped(cols, droppedCols) {
			continue
		}
		if sc.Name == "" {
			// Cannot DROP CONSTRAINT without a name; surface a manual notice.
			ops = append(ops, destructiveOp(
				fmt.Sprintf("-- WARNING: unnamed constraint on %s (%s %s) is no longer in desired; drop it manually",
					tbl, sc.Type, sc.Expr),
				pos,
			))
			continue
		}
		ops = append(ops, destructiveOp(
			fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;", tbl, quoteIdent(sc.Name)),
			pos,
		))
	}
	for _, c := range o.Constraints {
		if _, exists := snapByKey[key(c.Name, c.Type, c.Expr)]; exists {
			continue
		}
		notValid := ""
		if c.NotValid {
			notValid = " NOT VALID"
		}
		var sql string
		if c.Name != "" {
			sql = fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s%s;",
				tbl, quoteIdent(c.Name), c.Expr, notValid)
		} else {
			sql = fmt.Sprintf("ALTER TABLE %s ADD %s%s;", tbl, c.Expr, notValid)
		}
		ops = append(ops, cautionOp(sql, c.Pos))
	}
	return ops
}

func diffIndexes(schema, table string, o *ir.Table, snap *snapshot.SnapTable, renamedCols map[string]string, droppedCols map[string]bool) []pipeline.DiffOp {
	var ops []pipeline.DiffOp

	snapByName := make(map[string]*snapshot.SnapIndex, len(snap.Indexes))
	for i := range snap.Indexes {
		snapByName[snap.Indexes[i].Name] = &snap.Indexes[i]
	}
	desiredByName := make(map[string]*ir.Index, len(o.Indexes))
	for _, idx := range o.Indexes {
		desiredByName[idx.Name] = idx
	}

	for _, si := range snap.Indexes {
		if _, ok := desiredByName[si.Name]; ok {
			continue
		}
		// Indexes are matched by name. If a column was renamed via RENAMED FROM
		// and the index name is unchanged, PG keeps the index transparently.
		// Apply the rename map before deciding whether the snap index has truly
		// disappeared from desired (i.e. its only columns were dropped).
		cols := translateIndexCols(si.Columns, renamedCols)
		if allDropped(cols, droppedCols) {
			continue // DROP COLUMN cascade handles it.
		}
		ops = append(ops, cautionOp(
			fmt.Sprintf("DROP INDEX IF EXISTS %s;", quoteIdent(si.Name)),
			pipeline.SourcePos{},
		))
	}
	for _, idx := range o.Indexes {
		if _, exists := snapByName[idx.Name]; !exists {
			ops = append(ops, createIndex(schema, table, idx)...)
		}
	}
	return ops
}

// translateConstraintExpr rewrites quoted column identifiers inside a
// constraint's local-column list (or, for CHECK, the entire expression) so a
// snapshot expression captured before a RENAMED FROM matches the desired one.
// For PRIMARY KEY / UNIQUE / FOREIGN KEY only the first parenthesized group is
// touched — substituting globally would also rewrite remote-column refs after
// REFERENCES if a renamed name happened to collide.
func translateConstraintExpr(expr string, renamedCols map[string]string) string {
	if len(renamedCols) == 0 || expr == "" {
		return expr
	}
	upper := strings.ToUpper(strings.TrimSpace(expr))
	switch {
	case strings.HasPrefix(upper, "PRIMARY KEY"),
		strings.HasPrefix(upper, "UNIQUE"),
		strings.HasPrefix(upper, "FOREIGN KEY"):
		open, close := firstParenGroup(expr)
		if open == -1 {
			return expr
		}
		return expr[:open] + replaceQuotedIdents(expr[open:close+1], renamedCols) + expr[close+1:]
	case strings.HasPrefix(upper, "CHECK"):
		return replaceQuotedIdents(expr, renamedCols)
	}
	return expr
}

// localConstraintCols returns the unquoted local column names referenced in
// the first parenthesized group of a constraint expression. Used to decide
// whether a snapshot constraint's columns are entirely being dropped.
func localConstraintCols(expr string) []string {
	open, close := firstParenGroup(expr)
	if open == -1 {
		return nil
	}
	inside := expr[open+1 : close]
	var names []string
	for _, part := range strings.Split(inside, ",") {
		part = strings.TrimSpace(part)
		// Strip optional sort/nulls suffixes that may appear on PK/UNIQUE.
		if sp := strings.IndexAny(part, " \t"); sp != -1 {
			part = part[:sp]
		}
		if len(part) >= 2 && part[0] == '"' && part[len(part)-1] == '"' {
			part = part[1 : len(part)-1]
		}
		if part != "" {
			names = append(names, part)
		}
	}
	return names
}

// translateIndexCols applies the rename map to a SnapIndex.Columns field
// (a comma-separated list of column names or `(expression)` entries) and
// returns the resulting plain column names. Expression entries are returned
// as empty strings so they don't accidentally appear "dropped".
func translateIndexCols(cols string, renamedCols map[string]string) []string {
	if cols == "" {
		return nil
	}
	parts := strings.Split(cols, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "(") {
			out = append(out, "")
			continue
		}
		if newName, ok := renamedCols[p]; ok {
			p = newName
		}
		out = append(out, p)
	}
	return out
}

// firstParenGroup returns the byte indices of the matching '(' and ')' that
// open and close the first balanced parenthesized group, or (-1, -1).
func firstParenGroup(s string) (int, int) {
	open := strings.IndexByte(s, '(')
	if open == -1 {
		return -1, -1
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return open, i
			}
		}
	}
	return -1, -1
}

// replaceQuotedIdents substitutes "old" → "new" for every old name in the
// rename map, matching only fully quoted identifiers so unquoted keywords
// (e.g. ASC, DESC) aren't touched.
func replaceQuotedIdents(s string, renamedCols map[string]string) string {
	for old, newName := range renamedCols {
		s = strings.ReplaceAll(s, `"`+old+`"`, `"`+newName+`"`)
	}
	return s
}

// allDropped reports whether the given column names are non-empty and every
// one is a member of the dropped set. Empty input returns false so we don't
// suppress drops for constraints/indexes whose columns we couldn't parse.
func allDropped(cols []string, droppedCols map[string]bool) bool {
	if len(cols) == 0 || len(droppedCols) == 0 {
		return false
	}
	for _, c := range cols {
		if c == "" || !droppedCols[c] {
			return false
		}
	}
	return true
}

func diffPolicies(schema, table string, o *ir.Table, snap *snapshot.SnapTable) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	tblIdent := qualIdent(schema, table)

	snapByName := make(map[string]*snapshot.SnapPolicy, len(snap.Policies))
	for i := range snap.Policies {
		snapByName[snap.Policies[i].Name] = &snap.Policies[i]
	}
	desiredByName := make(map[string]*ir.Policy, len(o.Policies))
	for _, p := range o.Policies {
		desiredByName[p.Name] = p
	}

	for _, sp := range snap.Policies {
		if _, ok := desiredByName[sp.Name]; !ok {
			ops = append(ops, safeOp(
				fmt.Sprintf("DROP POLICY IF EXISTS %s ON %s;", quoteIdent(sp.Name), tblIdent),
				pipeline.SourcePos{},
			))
		}
	}
	for _, pol := range o.Policies {
		existing, exists := snapByName[pol.Name]
		if !exists {
			ops = append(ops, createPolicy(schema, table, pol)...)
		} else if pol.Command != existing.Command ||
			pol.Permissive != existing.Permissive ||
			ptrStr(pol.Using) != existing.Using ||
			ptrStr(pol.WithCheck) != existing.WithCheck {
			ops = append(ops, safeOp(
				fmt.Sprintf("DROP POLICY IF EXISTS %s ON %s;", quoteIdent(pol.Name), tblIdent),
				pol.Pos,
			))
			ops = append(ops, createPolicy(schema, table, pol)...)
		}
	}
	return ops
}

func diffTriggers(schema, table string, o *ir.Table, snap *snapshot.SnapTable) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	tblIdent := qualIdent(schema, table)

	snapByName := make(map[string]*snapshot.SnapTrigger, len(snap.Triggers))
	for i := range snap.Triggers {
		snapByName[snap.Triggers[i].Name] = &snap.Triggers[i]
	}
	desiredByName := make(map[string]*ir.Trigger, len(o.Triggers))
	for _, t := range o.Triggers {
		desiredByName[t.Name] = t
	}

	for _, st := range snap.Triggers {
		if _, ok := desiredByName[st.Name]; !ok {
			ops = append(ops, safeOp(
				fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;", quoteIdent(st.Name), tblIdent),
				pipeline.SourcePos{},
			))
		}
	}
	for _, trg := range o.Triggers {
		existing, exists := snapByName[trg.Name]
		if !exists {
			ops = append(ops, createTrigger(schema, table, trg)...)
		} else if trg.When != existing.When ||
			strings.Join(trg.Events, ", ") != existing.Events ||
			trg.ForEach != existing.ForEach ||
			trg.Function != existing.Function {
			ops = append(ops, safeOp(
				fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;", quoteIdent(trg.Name), tblIdent),
				trg.Pos,
			))
			ops = append(ops, createTrigger(schema, table, trg)...)
		}
	}
	return ops
}

var _ pipeline.Differ = (*Differ)(nil)
