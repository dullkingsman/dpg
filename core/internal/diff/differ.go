// Package diff implements pipeline.Differ. It compares a slice of desired
// IRObjects against a pipeline.Snapshot and produces an ordered list of DiffOps
// representing the minimal set of DDL changes needed.
package diff

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

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

// destructiveManualOp is destructiveOp's non-transactional counterpart —
// Safety() and Transactional() are independent fields on op (Emit buckets
// purely on Transactional(); apply's --allow-destructive gate checks purely
// on Safety()), so this combination is safe to construct directly rather
// than needing a new interface. Used for DROP SUBSCRIPTION, which errors
// "cannot run inside a transaction block" (confirmed live) — reusing
// manualOp here would have silently dropped the --allow-destructive gate,
// since apply.go's gate checks Safety() == Destructive specifically.
func destructiveManualOp(sql string, pos pipeline.SourcePos) *op {
	return &op{sql: sql, safety: pipeline.Destructive, pos: pos, txn: false}
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

	// Build virtual type set up-front so all passes can resolve jsonb references.
	vtypes := buildVTypeSet(desired)

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
		alterOps, err := diffObject(obj, &oldSnap, snap, vtypes)
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
		// RFC §7.11/§15.10 Phase 9 Pass 3: a table marked PROTECTED in the
		// snapshot must never be silently dropped just because it's absent
		// from desired — that's error DPG-E022, not a DROP TABLE. Found via
		// this session's live-testing sweep: Protected was parsed all the way
		// into SnapTable but nothing ever read it back out again, so a
		// declared PROTECTED table had zero actual protection.
		if so.Kind == "table" && so.Table != nil && so.Table.Protected {
			return nil, pipeline.Errorf(pipeline.SourcePos{},
				"table %q is PROTECTED and cannot be dropped; remove the PROTECTED directive first",
				key,
			)
		}
		ops = append(ops, dropObject(&so)...)
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
			createOps, err := createObject(obj, vtypes)
			if err != nil {
				return nil, err
			}
			ops = append(ops, createOps...)
		} else {
			alterOps, err := diffObject(obj, &so, snap, vtypes)
			if err != nil {
				return nil, err
			}
			ops = append(ops, alterOps...)
		}
	}

	return ops, nil
}

// buildVTypeSet collects all virtual type names from the desired IR, mapping
// each name (qualified and unqualified) to its preferred JSON format ("json"
// or "jsonb").  An empty JsonFormat on the VirtualType defaults to "jsonb".
func buildVTypeSet(desired []pipeline.IRObject) map[string]string {
	vtypes := make(map[string]string)
	for _, obj := range desired {
		if vt, ok := obj.(*ir.VirtualType); ok {
			format := vt.JsonFormat
			if format == "" {
				format = "jsonb"
			}
			vtypes[vt.QualifiedName()] = format
			vtypes[vt.Name] = format
		}
	}
	return vtypes
}

// resolveColType returns the SQL type string for a column TypeRef.  When the
// type refers to a declared virtual type it is replaced by the virtual type's
// preferred JSON format (json or jsonb), with [] repeated for array dimensions.
// isLegacySerialTypeName reports whether typeName is the literal,
// un-normalized SERIAL-family type name a pre-upgrade snapshot may have
// stored (before Column.Serial existed, typeNameToRef passed "serial"/
// "bigserial"/"smallserial"/their serialN spellings straight through
// unchanged). Used solely as a one-time, self-clearing stale-snapshot guard
// in diffColumns — see the call site for the full explanation.
func isLegacySerialTypeName(typeName string) bool {
	switch strings.ToLower(typeName) {
	case "smallserial", "serial2", "serial", "serial4", "bigserial", "serial8":
		return true
	default:
		return false
	}
}

// legacyTypeNameBeforeFix reconstructs what resolveColType/TypeRef.String()
// would have rendered for resolvedType before 2026-08-17's PGCatalogName fix
// added "time"->"time without time zone", "timestamp"->"timestamp without
// time zone", and "varbit"->"bit varying" mappings (all three previously
// passed through PGCatalogName's default case unchanged, silently
// mismatching format_type()'s own canonical spelling — a real, live-verified
// bug that caused permanent spurious DESTRUCTIVE drift for every plain bare
// TIMESTAMP/TIME column, and dropped the typmod entirely for BIT/BIT
// VARYING). Used only to recognize a pre-upgrade snapshot's stale Type
// string as "no real drift, only DPG's own rendering changed" so it
// self-heals on save — the same pattern as isLegacySerialTypeName just
// above.
func legacyTypeNameBeforeFix(resolvedType string) string {
	s := resolvedType
	arraySuffix := ""
	for strings.HasSuffix(s, "[]") {
		arraySuffix += "[]"
		s = strings.TrimSuffix(s, "[]")
	}
	switch {
	case strings.HasSuffix(s, " without time zone"):
		s = strings.TrimSuffix(s, " without time zone")
	case s == "bit varying" || strings.HasPrefix(s, "bit varying("):
		s = "varbit" + strings.TrimPrefix(s, "bit varying")
	}
	return s + arraySuffix
}

func resolveColType(t ir.TypeRef, vtypes map[string]string) string {
	key := t.Name
	if t.Schema != "" {
		key = t.Schema + "." + t.Name
	}
	if format, ok := vtypes[key]; ok {
		s := format
		for i := 0; i < t.ArrayDims; i++ {
			s += "[]"
		}
		return s
	}
	return t.String()
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

// qualOperatorIdent qualifies an operator symbol with its schema, like
// qualIdent, except the symbol itself (e.g. "===", "@>") is never quoted —
// unlike a regular identifier, it's a lexical operator token, and quoting it
// (as qualIdent would) is a PostgreSQL syntax error. Mirrors introspection's
// operatorRef (internal/introspect/opaque.go), which renders the same way
// for the same reason.
func qualOperatorIdent(schema, name string) string {
	if schema == "" {
		return name
	}
	return quoteIdent(schema) + "." + name
}

// quoteLit single-quotes a SQL string literal.
func quoteLit(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// hashText returns a SHA-256 hex digest of s (trimmed) — used wherever a
// declared-but-never-persisted-verbatim value (a secret reference or
// literal) needs offline drift detection without ever storing the value
// itself in the snapshot. Mirrors snapshot.hashBodyStr's exact formula,
// duplicated here since that one is unexported in a different package.
func hashText(s string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(s)))
	return fmt.Sprintf("%x", sum)
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

// stringSetEqual compares two string slices as sets (order-independent,
// duplicates collapsed) — used for EVENT TRIGGER's WHEN TAG IN (...) list,
// which PostgreSQL stores as an unordered evttags array.
func stringSetEqual(a, b []string) bool {
	if len(a) != len(b) {
		// A dedup-aware length check would be more correct for duplicate
		// entries, but WHEN TAG IN (...) listing the same tag twice is not
		// a real, meaningful shape (RFC §14.1's tag-list is just an
		// enumeration of distinct DDL command tags) — a plain length
		// mismatch is sufficient signal here without building a multiset.
		return false
	}
	set := make(map[string]bool, len(a))
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		if !set[s] {
			return false
		}
	}
	return true
}

// effectiveComment resolves an object's live COMMENT text — used for
// columns, tables, views, and functions (the RFC's own stated scope for
// DEPRECATED, §19.1: "Applied to tables, columns, views, functions").
// DEPRECATED (when set) always wins, rendered with the RFC's own
// "[DEPRECATED] msg" prefix — real PostgreSQL only ever stores one comment
// per object, so an explicit Comment is shadowed whenever Deprecated is
// also set, rather than inventing an undocumented combined format. Found
// live-testing a demo project: DEPRECATED was captured by the parser and
// snapshot and used by the linter (hence its warning appearing in `dpg
// plan` output), but the differ never referenced it at all for ANY of the
// four kinds — it had zero effect on the actual generated SQL, despite the
// RFC documenting `COMMENT ON COLUMN t.c IS '[DEPRECATED] msg'` (and the
// table/view/function equivalents) as the expected output.
func effectiveComment(comment, deprecated *string) *string {
	if deprecated != nil {
		s := "[DEPRECATED] " + *deprecated
		return &s
	}
	return comment
}

// compositeAttrsChanged returns true if the composite type attribute list has
// changed compared to the snapshot. Any addition, removal, or type change
// counts as a change (PG requires DROP + CREATE for attribute changes).
func compositeAttrsChanged(attrs []*ir.Column, snap []snapshot.SnapColumn) bool {
	if len(attrs) != len(snap) {
		return true
	}
	for i, attr := range attrs {
		if attr.Name != snap[i].Name || attr.Type.String() != snap[i].Type {
			return true
		}
	}
	return false
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

// roleList quotes and joins a list of role names. PUBLIC is PostgreSQL's
// pseudo-role keyword, not an actual role — GRANT/REVOKE ... TO/FROM PUBLIC
// must stay bare. Quoting it (as every other role name is) makes PG look up
// an actual role literally named "PUBLIC", which doesn't exist and errors
// with "role \"PUBLIC\" does not exist". Confirmed live: introspection's
// grant queries emit the literal string "PUBLIC" as the grantee for PG's
// own implicit default EXECUTE-to-PUBLIC grant (grantee = 0), across every
// grant-bearing object kind (tables, columns, views, functions, sequences,
// aggregates, ...) — any REVOKE reconciling that implicit grant away hit
// this.
func roleList(roles []string) string {
	quoted := make([]string, len(roles))
	for i, r := range roles {
		if strings.EqualFold(r, "PUBLIC") {
			quoted[i] = "PUBLIC"
		} else {
			quoted[i] = quoteIdent(r)
		}
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

// diffRevocationSet diffs an explicit REVOCATIONS list (RFC §11.3) against its
// snapshot. Unlike grants (the additive model in diffGrantSet — removing a
// GRANT declaration emits nothing), a revocation is itself tracked as a
// persistent declaration: once applied it's remembered in the snapshot so a
// later unchanged run doesn't re-issue REVOKE — not because re-running it
// would error (REVOKE on an already-absent privilege is a harmless no-op in
// PG), but to keep migration output free of redundant statements, matching
// the same idempotency style already used for grants. Removing a REVOCATION
// entry re-GRANTs the privilege to restore it. Mirrors diffDefaultPrivileges's
// existing revocation-diffing logic, generalized so table/view/column
// REVOCATION can share it.
func diffRevocationSet(
	snapRevs []snapshot.SnapGrant,
	desiredRevs []ir.Revocation,
	onClause string,
	pos pipeline.SourcePos,
) []pipeline.DiffOp {
	var ops []pipeline.DiffOp

	snapByKey := make(map[string]snapshot.SnapGrant, len(snapRevs))
	for _, r := range snapRevs {
		snapByKey[grantKey(r.Privileges, r.Roles, false)] = r
	}
	desiredByKey := make(map[string]ir.Revocation, len(desiredRevs))
	for _, r := range desiredRevs {
		desiredByKey[grantKey(r.Privileges, r.Roles, false)] = r
	}

	for k, sr := range snapByKey {
		if _, ok := desiredByKey[k]; !ok {
			// Revocation removed from desired: re-grant to restore.
			ops = append(ops, safeOp(
				fmt.Sprintf("GRANT %s ON %s TO %s;", privStr(sr.Privileges), onClause, roleList(sr.Roles)),
				pos,
			))
		}
	}
	for k, r := range desiredByKey {
		if _, ok := snapByKey[k]; !ok {
			cascade := ""
			if r.Cascade {
				cascade = " CASCADE"
			}
			ops = append(ops, cautionOp(
				fmt.Sprintf("REVOKE %s ON %s FROM %s%s;", privStr(r.Privileges), onClause, roleList(r.Roles), cascade),
				pos,
			))
		}
	}
	return ops
}

// ── DROP operations ───────────────────────────────────────────────────────────

func dropObject(so *snapshot.SnapObject) []pipeline.DiffOp {
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
			// DROP TABLESPACE cannot run inside a transaction block
			// (confirmed live) — same restriction as CREATE TABLESPACE.
			return []pipeline.DiffOp{destructiveManualOp(fmt.Sprintf("DROP TABLESPACE IF EXISTS %s;", quoteIdent(so.Opaque.Name)), zero)}
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
				// An empty user is a FOR PUBLIC mapping; quoting "" would emit a
				// zero-length identifier and abort the migration.
				forClause := "PUBLIC"
				if parts[0] != "" {
					forClause = quoteIdent(parts[0])
				}
				return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP USER MAPPING IF EXISTS FOR %s SERVER %s;", forClause, quoteIdent(parts[1])), zero)}
			}
		}
	case "publication":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP PUBLICATION IF EXISTS %s;", quoteIdent(so.Opaque.Name)), zero)}
		}
	case "subscription":
		if so.Opaque != nil {
			// destructiveManualOp, not destructiveOp: DROP SUBSCRIPTION
			// errors "cannot run inside a transaction block" (confirmed
			// live) — the same non-transactional constraint CREATE
			// SUBSCRIPTION has, see createSubscription's doc comment.
			return []pipeline.DiffOp{destructiveManualOp(fmt.Sprintf("DROP SUBSCRIPTION IF EXISTS %s;", quoteIdent(so.Opaque.Name)), zero)}
		}
	case "event_trigger":
		if so.Opaque != nil {
			// Unlike its opaque-tier siblings (Collation, Cast, Operator, ...),
			// an event trigger holds no data and nothing else in the catalog
			// depends on it — RFC §14.1 explicitly classifies its DROP+CREATE
			// cycle as SAFE ("no data involved"), not DESTRUCTIVE like the
			// rest of dropObject's cases.
			return []pipeline.DiffOp{safeOp(fmt.Sprintf("DROP EVENT TRIGGER IF EXISTS %s;", quoteIdent(so.Opaque.Name)), zero)}
		}
	case "collation":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP COLLATION IF EXISTS %s;", qualIdent(so.Opaque.Schema, so.Opaque.Name)), zero)}
		}
	case "operator":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP OPERATOR IF EXISTS %s(%s);", qualOperatorIdent(so.Opaque.Schema, so.Opaque.Name), so.Opaque.Args), zero)}
		}
	case "operator_class":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP OPERATOR CLASS IF EXISTS %s USING %s;", qualIdent(so.Opaque.Schema, so.Opaque.Name), accessMethodOrDefault(so.Opaque.Using)), zero)}
		}
	case "operator_family":
		if so.Opaque != nil {
			return []pipeline.DiffOp{destructiveOp(fmt.Sprintf("DROP OPERATOR FAMILY IF EXISTS %s USING %s;", qualIdent(so.Opaque.Schema, so.Opaque.Name), accessMethodOrDefault(so.Opaque.Using)), zero)}
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
	case "default_privileges":
		if so.DefaultPrivileges != nil {
			// Revoking a DEFAULT PRIVILEGES declaration means revoking all its grants.
			dp := so.DefaultPrivileges
			prefix := buildDefaultPrivPrefix(dp.ForRole, dp.InSchema)
			var ops []pipeline.DiffOp
			for _, g := range dp.Grants {
				ops = append(ops, cautionOp(
					fmt.Sprintf("%s REVOKE %s ON %s FROM %s;",
						prefix, privStr(g.Privileges), dp.ObjectType, roleList(g.Roles)),
					zero,
				))
			}
			return ops
		}
	case "virtual_type":
		// Virtual types have no SQL backing — nothing to drop.
		return nil
	}
	return nil
}

// ── CREATE operations ─────────────────────────────────────────────────────────

func createObject(obj pipeline.IRObject, vtypes map[string]string) ([]pipeline.DiffOp, error) {
	switch o := obj.(type) {
	case *ir.Schema:
		return createSchema(o), nil
	case *ir.Extension:
		return createExtension(o), nil
	case *ir.Table:
		return createTable(o, vtypes), nil
	case *ir.View:
		return createView(o), nil
	case *ir.Function:
		return createFunction(o), nil
	case *ir.Type:
		return createType(o, vtypes), nil
	case *ir.Sequence:
		return createSequence(o), nil
	case *ir.Role:
		return createRole(o), nil
	case *ir.Procedure:
		return createProcedure(o), nil
	case *ir.Aggregate:
		return createAggregate(o)
	case *ir.Tablespace:
		ops, err := createOpaque(o.Name, o.Body, "TABLESPACE", "", o.SrcPos)
		return appendCommentOp(ops, err, "tablespace", "", o.Name, "", "", o.Comment, o.SrcPos)
	case *ir.ForeignDataWrapper:
		ops, err := createOpaque(o.Name, o.Body, "FOREIGN DATA WRAPPER", "", o.SrcPos)
		return appendCommentOp(ops, err, "fdw", "", o.Name, "", "", o.Comment, o.SrcPos)
	case *ir.ForeignServer:
		ops, err := createOpaque(o.Name, o.Body, "SERVER", "", o.SrcPos)
		return appendCommentOp(ops, err, "server", "", o.Name, "", "", o.Comment, o.SrcPos)
	case *ir.UserMapping:
		return createUserMapping(o)
	case *ir.Publication:
		ops, err := createOpaque(o.Name, o.Body, "PUBLICATION", "", o.SrcPos)
		return appendCommentOp(ops, err, "publication", "", o.Name, "", "", o.Comment, o.SrcPos)
	case *ir.Subscription:
		return createSubscription(o)
	case *ir.EventTrigger:
		ops, err := createOpaque(o.Name, o.Body, "EVENT TRIGGER", "", o.SrcPos)
		return appendCommentOp(ops, err, "event_trigger", "", o.Name, "", "", o.Comment, o.SrcPos)
	case *ir.Collation:
		ops, err := createOpaque(o.QualifiedName(), o.Body, "COLLATION", o.Schema, o.SrcPos)
		return appendCommentOp(ops, err, "collation", o.Schema, o.Name, "", "", o.Comment, o.SrcPos)
	case *ir.Operator:
		ops, err := createOpaque(o.QualifiedName(), o.Body, "OPERATOR", o.Schema, o.SrcPos)
		return appendCommentOp(ops, err, "operator", o.Schema, o.Name, ir.OperandsKey(o.LeftType, o.RightType), "", o.Comment, o.SrcPos)
	case *ir.OperatorClass:
		ops, err := createOpaque(o.QualifiedName(), o.Body, "OPERATOR CLASS", o.Schema, o.SrcPos)
		return appendCommentOp(ops, err, "operator_class", o.Schema, o.Name, "", o.AccessMethod, o.Comment, o.SrcPos)
	case *ir.OperatorFamily:
		ops, err := createOpaque(o.QualifiedName(), o.Body, "OPERATOR FAMILY", o.Schema, o.SrcPos)
		ops, err = appendCommentOp(ops, err, "operator_family", o.Schema, o.Name, "", o.AccessMethod, o.Comment, o.SrcPos)
		if err != nil {
			return ops, err
		}
		famIdent := qualIdent(o.Schema, o.Name)
		for _, m := range o.Members {
			ops = append(ops, safeOp(opFamilyAddSQL(famIdent, o.AccessMethod, m), o.SrcPos))
		}
		return ops, nil
	case *ir.Cast:
		ops, err := createOpaque(o.QualifiedName(), o.Body, "CAST", "", o.SrcPos)
		return appendCommentOp(ops, err, "cast", "", o.QualifiedName(), "", "", o.Comment, o.SrcPos)
	case *ir.StatisticsObject:
		ops, err := createOpaque(o.QualifiedName(), o.Body, "STATISTICS", o.Schema, o.SrcPos)
		return appendCommentOp(ops, err, "statistics", o.Schema, o.Name, "", "", o.Comment, o.SrcPos)
	case *ir.TSConfig:
		ops, err := createOpaque(o.QualifiedName(), o.Body, "TEXT SEARCH CONFIGURATION", o.Schema, o.SrcPos)
		ops, err = appendCommentOp(ops, err, "ts_config", o.Schema, o.Name, "", "", o.Comment, o.SrcPos)
		if err != nil {
			return ops, err
		}
		for _, m := range o.Mappings {
			ops = append(ops, safeOp(tsMappingAlterSQL(qualIdent(o.Schema, o.Name), m), o.SrcPos))
		}
		return ops, nil
	case *ir.TSDict:
		ops, err := createOpaque(o.QualifiedName(), o.Body, "TEXT SEARCH DICTIONARY", o.Schema, o.SrcPos)
		return appendCommentOp(ops, err, "ts_dict", o.Schema, o.Name, "", "", o.Comment, o.SrcPos)
	case *ir.TSParser:
		ops, err := createOpaque(o.QualifiedName(), o.Body, "TEXT SEARCH PARSER", o.Schema, o.SrcPos)
		return appendCommentOp(ops, err, "ts_parser", o.Schema, o.Name, "", "", o.Comment, o.SrcPos)
	case *ir.TSTemplate:
		ops, err := createOpaque(o.QualifiedName(), o.Body, "TEXT SEARCH TEMPLATE", o.Schema, o.SrcPos)
		return appendCommentOp(ops, err, "ts_template", o.Schema, o.Name, "", "", o.Comment, o.SrcPos)
	case *ir.DefaultPrivileges:
		return createDefaultPrivileges(o), nil
	case *ir.VirtualType:
		// Virtual types exist in the snapshot for downstream consumers but
		// generate no SQL — they have no backing PostgreSQL object.
		return nil, nil
	}
	return nil, nil
}

// createOpaque emits a CREATE statement from a pre-built Body SQL string.
// Returns an error if Body is empty — the builder failed to capture the source SQL,
// which would otherwise produce a silent no-op migration.
//
// schema is the object's own declared/inferred schema for kinds that are
// genuinely schema-scoped in PostgreSQL (COLLATION, OPERATOR, OPERATOR
// CLASS/FAMILY, STATISTICS, the 4 TEXT SEARCH kinds) — pass "" for kinds
// that aren't (TABLESPACE, FOREIGN DATA WRAPPER, SERVER, PUBLICATION, EVENT
// TRIGGER are cluster/database-level; CAST has no schema concept at all,
// identified purely by its source/target type pair).
//
// Body is Part1 deparsed as written in source — it only carries a
// schema-qualified object name if the user happened to write one
// explicitly; DPG's normal schema inference (directory placement, an
// enclosing SCHEMA { } block) is never baked into this raw text the way
// createTable/createView etc. explicitly qualify their own identifiers.
// Confirmed live: a STATISTICS object declared under a non-public schema
// context landed in `public` instead — PostgreSQL resolves an unqualified
// CREATE target through search_path, and the opaque Body's own unqualified
// name says nothing about DPG's tracked Schema. Fixed the same way pg_dump
// itself handles this (SET search_path before each unqualified opaque
// statement) rather than attempting to string-rewrite 9 differently-shaped
// CREATE statements to inject a qualified name — safe because these ops
// already run inside the migration's single transaction, and SET LOCAL's
// scope ends at that transaction's COMMIT, so it can never leak into a
// later, unrelated migration. ", public" is appended as a fallback so an
// unqualified reference elsewhere in the same body (e.g. STATISTICS' FROM
// table) still resolves normally when that referent lives in public.
func createOpaque(name, body, kind, schema string, pos pipeline.SourcePos) ([]pipeline.DiffOp, error) {
	if body == "" {
		return nil, fmt.Errorf("%s %s: body not captured; define it explicitly in a .dpg source file", kind, name)
	}
	sql := body + ";"
	if schema != "" && schema != "public" {
		sql = fmt.Sprintf("SET LOCAL search_path = %s, public;\n%s", quoteIdent(schema), sql)
	}
	// CREATE TABLESPACE cannot run inside a transaction block (confirmed
	// live: "ERROR: CREATE TABLESPACE cannot run inside a transaction
	// block") — unlike every other opaque kind this function handles (FDW,
	// SERVER, EVENT TRIGGER, COLLATION, etc.), which all run fine
	// transactionally. kind arrives both uppercase (createObject's literal
	// "TABLESPACE") and lowercase snake_case (diffOpaqueIR's snap.Kind
	// "tablespace"), hence the case-insensitive compare.
	if strings.EqualFold(kind, "tablespace") {
		return []pipeline.DiffOp{manualOp(sql, pos)}, nil
	}
	return []pipeline.DiffOp{safeOp(sql, pos)}, nil
}

// commentOnOpaqueSQL renders "COMMENT ON <kind-specific identity> IS
// <lit-or-NULL>;" for the 14 opaque kinds that carry a Comment field
// (tablespace, fdw, server, event_trigger, collation, operator,
// operator_class, operator_family, cast, statistics, and all 4 TS kinds) —
// found live-testing a demo project: PostgreSQL genuinely supports
// COMMENT ON for every one of these (confirmed via \h COMMENT against a
// real server), but the blockparser's generic { COMMENT '...'; } was
// silently discarded for 9 of them (no field existed at all) and captured-
// but-never-emitted for the other 5 — dpg plan reported "-- (no changes)"
// with no error and no effect either way.
//
// kind uses the same lowercase snake_case vocabulary as dropObject/
// snapshot.SnapOpaque.Kind (not createOpaque's own uppercase display kind),
// so callers already holding a SnapOpaque (the diff/update path) can pass
// its Kind/Schema/Name/Args/Using straight through with no translation.
// Reuses the exact same per-kind identity shapes dropObject already relies
// on, so a comment always targets the identical object dropObject would —
// operator/operator class/operator family/cast all need their special
// (non-plain-identifier) COMMENT ON syntax, confirmed against \h COMMENT.
// Returns "" for an unrecognized kind.
func commentOnOpaqueSQL(kind, schema, name, args, using string, comment *string) string {
	var ident string
	switch kind {
	case "tablespace":
		ident = "TABLESPACE " + quoteIdent(name)
	case "fdw":
		ident = "FOREIGN DATA WRAPPER " + quoteIdent(name)
	case "server":
		ident = "SERVER " + quoteIdent(name)
	case "publication":
		ident = "PUBLICATION " + quoteIdent(name)
	case "event_trigger":
		ident = "EVENT TRIGGER " + quoteIdent(name)
	case "collation":
		ident = "COLLATION " + qualIdent(schema, name)
	case "operator":
		ident = "OPERATOR " + qualOperatorIdent(schema, name) + "(" + args + ")"
	case "operator_class":
		ident = "OPERATOR CLASS " + qualIdent(schema, name) + " USING " + accessMethodOrDefault(using)
	case "operator_family":
		ident = "OPERATOR FAMILY " + qualIdent(schema, name) + " USING " + accessMethodOrDefault(using)
	case "cast":
		parts := strings.SplitN(name, "->", 2)
		if len(parts) != 2 {
			return ""
		}
		ident = fmt.Sprintf("CAST (%s AS %s)", parts[0], parts[1])
	case "statistics":
		ident = "STATISTICS " + qualIdent(schema, name)
	case "ts_config":
		ident = "TEXT SEARCH CONFIGURATION " + qualIdent(schema, name)
	case "ts_dict":
		ident = "TEXT SEARCH DICTIONARY " + qualIdent(schema, name)
	case "ts_parser":
		ident = "TEXT SEARCH PARSER " + qualIdent(schema, name)
	case "ts_template":
		ident = "TEXT SEARCH TEMPLATE " + qualIdent(schema, name)
	default:
		return ""
	}
	val := "NULL"
	if comment != nil {
		val = quoteLit(*comment)
	}
	return fmt.Sprintf("COMMENT ON %s IS %s;", ident, val)
}

// appendCommentOp appends a COMMENT ON op to ops (the result of a preceding
// createOpaque call) when comment is set, passing err through unchanged —
// lets each createObject call site stay a single return statement:
// return appendCommentOp(createOpaque(...), kind, schema, name, args, using, comment, pos)
// wouldn't type-check (createOpaque's 2 return values can't feed more
// params in the same call), so callers destructure first; this only
// centralizes the "skip on error or nil comment" check itself.
func appendCommentOp(ops []pipeline.DiffOp, err error, kind, schema, name, args, using string, comment *string, pos pipeline.SourcePos) ([]pipeline.DiffOp, error) {
	if err != nil || comment == nil {
		return ops, err
	}
	if sql := commentOnOpaqueSQL(kind, schema, name, args, using, comment); sql != "" {
		ops = append(ops, safeOp(sql, pos))
	}
	return ops, nil
}

// userMappingCreateOp is the DiffOp for a CREATE USER MAPPING statement
// whose OPTIONS clause may embed a secret reference (Secret resolution,
// Phase 5) — e.g. OPTIONS (user 'app', password '{{vault:secret/fdw/db#pw}}').
// Unlike SUBSCRIPTION CONNECTION (§13.2/§6bb-§6dd) there's no single known
// clause to isolate: FDW OPTIONS keys are provider-specific, not fixed by
// DPG, so ExecSQL runs pipeline.ResolveTemplate over the ENTIRE deparsed
// body — a no-op everywhere there's no {{...}}, and it substitutes each
// placeholder in place regardless of which OPTIONS key it's under, with no
// regex/positional parsing needed at all. Same redaction contract as
// subscriptionCreateOp/roleCreateOp: SQL() never changes meaning, only
// ExecSQL (called only inside the executor's exec loop) resolves.
type userMappingCreateOp struct {
	*op
	body string
}

func (o *userMappingCreateOp) ExecSQL(resolver pipeline.SecretResolver) (string, error) {
	resolved, err := pipeline.ResolveTemplate(o.body, resolver)
	if err != nil {
		return "", err
	}
	return resolved + ";", nil
}

var _ pipeline.SecretBearingOp = (*userMappingCreateOp)(nil)

func createUserMapping(o *ir.UserMapping) ([]pipeline.DiffOp, error) {
	if o.Body == "" {
		return nil, fmt.Errorf("USER MAPPING %s: body not captured; define it explicitly in a .dpg source file", o.QualifiedName())
	}
	return []pipeline.DiffOp{&userMappingCreateOp{op: safeOp(o.Body+";", o.SrcPos), body: o.Body}}, nil
}

// diffUserMapping mirrors diffOpaqueIR's shape (offline body-hash compare,
// structured DROP+CREATE on a real change) but can't reuse it directly: its
// CREATE side must produce a userMappingCreateOp, not a generic createOpaque
// op, so any {{...}} reference in OPTIONS is resolved only immediately
// before execution, never during diff.
// diffUserMapping implements RFC §14.10's structured diffing: "any change
// to the mapping is a full DROP USER MAPPING + CREATE USER MAPPING, not a
// targeted ALTER USER MAPPING" is the RFC's own explicit, deliberately-
// corrected semantics, so this is a single "did the non-sensitive OPTIONS
// change" comparison, in place of the previous offline-only body-hash
// compare (o.Reconstructed==true — always false for a desired/source-side
// object — meant the *snap* side going Reconstructed on any live path
// forced snap.BodyHash to "", so this never fired against live catalog
// drift). Password-like OPTIONS keys are excluded from the comparison
// entirely (toComparableOptions' excludeSensitive=true): the live side
// never carries the real value, only a fixed redaction placeholder (see
// ir.UserMapping.Options' doc comment), so comparing it would show
// permanent, spurious drift on every plan.
func diffUserMapping(o *ir.UserMapping, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	if o.Body == "" {
		return nil, nil
	}
	// Offline path: snap.BodyHash is only ever populated when the snap
	// side is NOT Reconstructed (see sourceBodyHash) — i.e. both sides
	// came from source, so a real, comparable password/secret-reference
	// value exists on both. Preserve the original full-body hash compare
	// here unchanged: it's the only way to detect a declared {{secret-uri}}
	// reference change (e.g. rotating to a different vault path) at all,
	// and password-like keys must stay fully in scope for it.
	if snap.BodyHash != "" {
		newHash := hashText(o.Body)
		if newHash == snap.BodyHash {
			return nil, nil
		}
		ops := dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
		createOps, err := createUserMapping(o)
		if err != nil {
			return nil, err
		}
		return append(ops, createOps...), nil
	}
	// Live path (snap.BodyHash == "", i.e. Reconstructed): the live side
	// can never expose a real, comparable password value (see
	// ir.UserMapping.Options' doc comment) — RFC §14.10's structured
	// diffing input, per-field OPTIONS comparison excluding password-like
	// keys entirely, closes G-live for the non-sensitive subset. Detecting
	// a live-only password/secret-reference change remains genuinely
	// inherent (same PostgreSQL-imposed limit RFC §24 already documents
	// for Subscription's subconninfo), not a gap this can close.
	if !snap.OptionsStructured {
		// Stale snapshot predating this structured field — same
		// self-healing pattern as diffFDW/diffForeignServer. UserMapping
		// has no Comment field (real PostgreSQL has no COMMENT ON USER
		// MAPPING), so there's nothing to fall back to but the refresh op.
		return []pipeline.DiffOp{safeOp(fmt.Sprintf("-- refresh snapshot metadata for user mapping %s", o.QualifiedName()), o.SrcPos)}, nil
	}
	if optionsEqual(toComparableOptions(o.Options, true), snap.UserMappingOptions) {
		return nil, nil
	}
	ops := dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
	createOps, err := createUserMapping(o)
	if err != nil {
		return nil, err
	}
	return append(ops, createOps...), nil
}

// subscriptionConnectionLit matches CREATE SUBSCRIPTION's CONNECTION clause
// literal in pg_query's deparsed output — verified empirically:
// "CONNECTION '<connstr>'", connstr single-quote-escaped by doubling,
// exactly once per statement, since CONNECTION is a required, unique clause
// in this grammar (never appears elsewhere in a CREATE SUBSCRIPTION).
var subscriptionConnectionLit = regexp.MustCompile(`(?s)\bCONNECTION\s+'(?:[^']|'')*'`)

// substituteConnection replaces body's CONNECTION '...' clause with a
// freshly quoted resolved value. Returns an error if the clause can't be
// found — would mean pg_query's deparse output changed shape; safer to fail
// loudly than silently execute a statement with the wrong, unresolved
// CONNECTION value.
func substituteConnection(body, resolved string) (string, error) {
	loc := subscriptionConnectionLit.FindStringIndex(body)
	if loc == nil {
		return "", fmt.Errorf("could not locate CONNECTION clause in deparsed SUBSCRIPTION SQL")
	}
	return body[:loc[0]] + "CONNECTION " + quoteLit(resolved) + body[loc[1]:], nil
}

// subscriptionCreateOp is the DiffOp for a CREATE SUBSCRIPTION statement
// whose CONNECTION clause may hold an unresolved secret reference (RFC
// §13.2). SQL() always returns the placeholder/reference form (embedded
// op.sql, unchanged) — used for plan output, migration-file archival, and
// error messages, so a resolved secret is never persisted or logged.
// ExecSQL resolves the native CONNECTION literal via pipeline.ResolveTemplate
// (a no-op if it contains no {{...}} at all) and substitutes it into a fresh
// copy of the SQL, used only for the immediate execution call — never for
// anything that gets displayed, hashed, or wrapped into an error.
type subscriptionCreateOp struct {
	*op
	connInfo string
}

func (o *subscriptionCreateOp) ExecSQL(resolver pipeline.SecretResolver) (string, error) {
	resolved, err := pipeline.ResolveTemplate(o.connInfo, resolver)
	if err != nil {
		return "", err
	}
	return substituteConnection(o.sql, resolved)
}

var _ pipeline.SecretBearingOp = (*subscriptionCreateOp)(nil)

// createSubscription emits a CREATE SUBSCRIPTION statement as a
// subscriptionCreateOp rather than the generic createOpaque path — every
// other opaque kind has nothing secret-bearing in its Body, but a
// Subscription's CONNECTION clause may embed a secret reference that must
// only ever be resolved immediately before execution (see
// subscriptionCreateOp's doc comment). Also emits COMMENT ON SUBSCRIPTION
// when set — Comment isn't part of Body (it lives in the { } block), so it
// needs its own statement, same shape as every other Comment-bearing kind.
//
// Built via manualOp, the same non-transactional precedent already used for
// CREATE INDEX CONCURRENTLY: PostgreSQL's CREATE SUBSCRIPTION defaults to
// WITH (create_slot = true), which errors "cannot run inside a transaction
// block" — confirmed live (this was a real, pre-existing bug predating
// secret-reference support entirely; createOpaque's generic path used
// safeOp, transactional, for every opaque kind including this one, and
// nothing had ever live-tested a fresh CREATE SUBSCRIPTION before). Always
// non-transactional regardless of whether a given declaration happens to
// set create_slot = false — Subscription's WITH-options aren't structurally
// parsed (still fully opaque beyond CONNECTION), and running outside a
// transaction is always safe even when not strictly required, unlike the
// reverse.
func createSubscription(o *ir.Subscription) ([]pipeline.DiffOp, error) {
	if o.Body == "" {
		return nil, fmt.Errorf("SUBSCRIPTION %s: body not captured; define it explicitly in a .dpg source file", o.Name)
	}
	ops := []pipeline.DiffOp{&subscriptionCreateOp{
		op:       manualOp(o.Body+";", o.SrcPos),
		connInfo: o.ConnInfo,
	}}
	if o.Comment != nil {
		// manualOp (non-transactional), not safeOp: emit buckets ops purely
		// by Transactional() into two separate lists, and the executor runs
		// the WHOLE transactional block before any non-transactional op —
		// confirmed live. A transactional COMMENT here would run BEFORE the
		// non-transactional CREATE above, erroring "subscription does not
		// exist" (the subscription doesn't exist yet). Non-transactional
		// keeps it in CREATE's own bucket, in the correct relative order.
		// diffSubscription's standalone comment-only-change path has no such
		// ordering hazard (the subscription already exists there) and stays
		// transactional.
		ops = append(ops, manualOp(
			fmt.Sprintf("COMMENT ON SUBSCRIPTION %s IS %s;", quoteIdent(o.Name), quoteLit(*o.Comment)),
			o.SrcPos,
		))
	}
	return ops, nil
}

// diffTablespace implements RFC §14.7's structured diffing (Location is the
// only property that decides DROP+CREATE — LOCATION cannot be changed after
// creation in real PostgreSQL) in place of diffOpaqueIR's generic
// Reconstructed-gated body-hash compare, which went silently unset (via
// Reconstructed) on every live path — verify/plan --live never detected a
// live-catalog-only LOCATION change. Comment is still diffed independently,
// same as every other opaque-tier kind.
func diffTablespace(o *ir.Tablespace, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	pos := o.SrcPos
	// Stale snapshot predating this structured field: Go's zero value ""
	// for TablespaceLocation, even though a real tablespace always has a
	// non-empty LOCATION (required by CREATE TABLESPACE's grammar) — same
	// self-healing guard pattern as diffType's DOMAIN branch
	// (snap.DomainBaseType == ""). Skip structural comparison and fall back
	// to Comment-only, emitting a harmless refresh op if nothing else
	// changed so the snapshot self-heals on the very next apply instead of
	// staying stale forever.
	if snap.TablespaceLocation == "" {
		var ops []pipeline.DiffOp
		if !ptrEq(o.Comment, snap.Comment) {
			if sql := commentOnOpaqueSQL("tablespace", "", o.Name, "", "", o.Comment); sql != "" {
				ops = append(ops, safeOp(sql, pos))
			}
		}
		if len(ops) == 0 {
			ops = append(ops, safeOp(fmt.Sprintf("-- refresh snapshot metadata for tablespace %s", quoteIdent(o.Name)), pos))
		}
		return ops, nil
	}
	if o.Location != snap.TablespaceLocation {
		ops := dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
		createOps, err := createOpaque(o.Name, o.Body, "TABLESPACE", "", pos)
		if err != nil {
			return nil, err
		}
		ops = append(ops, createOps...)
		return appendCommentOp(ops, nil, "tablespace", "", o.Name, "", "", o.Comment, pos)
	}
	if !ptrEq(o.Comment, snap.Comment) {
		if sql := commentOnOpaqueSQL("tablespace", "", o.Name, "", "", o.Comment); sql != "" {
			return []pipeline.DiffOp{safeOp(sql, pos)}, nil
		}
	}
	return nil, nil
}

// diffCast implements RFC §14.5's structured diffing: PostgreSQL provides no
// ALTER CAST at all, so any change to Method, Function, or Context decides
// DROP+CREATE, in place of diffOpaqueIR's generic Reconstructed-gated
// body-hash compare (silently unset on every live path — see
// diffTablespace's doc comment for the same root cause). Function is
// compared via qualifyFuncForCompare (same helper diffTriggers already
// uses): introspection always returns a schema-qualified name, while
// hand-written source commonly leaves it unqualified relying on the default
// "public" schema.
func diffCast(o *ir.Cast, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	pos := o.SrcPos
	name := o.QualifiedName()
	// Stale snapshot predating these structured fields: CastMethod is
	// always one of "f"/"i"/"b" for a real cast, never the Go zero value —
	// same self-healing guard pattern as diffTablespace.
	if snap.CastMethod == "" {
		var ops []pipeline.DiffOp
		if !ptrEq(o.Comment, snap.Comment) {
			if sql := commentOnOpaqueSQL("cast", "", name, "", "", o.Comment); sql != "" {
				ops = append(ops, safeOp(sql, pos))
			}
		}
		if len(ops) == 0 {
			ops = append(ops, safeOp(fmt.Sprintf("-- refresh snapshot metadata for cast %s", name), pos))
		}
		return ops, nil
	}
	if o.Method != snap.CastMethod || o.Context != snap.CastContext ||
		qualifyFuncForCompare(o.Function) != qualifyFuncForCompare(snap.CastFunction) {
		ops := dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
		createOps, err := createOpaque(name, o.Body, "CAST", "", pos)
		if err != nil {
			return nil, err
		}
		ops = append(ops, createOps...)
		return appendCommentOp(ops, nil, "cast", "", name, "", "", o.Comment, pos)
	}
	if !ptrEq(o.Comment, snap.Comment) {
		if sql := commentOnOpaqueSQL("cast", "", name, "", "", o.Comment); sql != "" {
			return []pipeline.DiffOp{safeOp(sql, pos)}, nil
		}
	}
	return nil, nil
}

// diffEventTrigger implements RFC §14.1's structured diffing: PostgreSQL has
// no ALTER EVENT TRIGGER for Event/Tags/Function (only ENABLE/DISABLE/
// OWNER TO/RENAME TO, none modeled here), so any change to those three
// decides DROP+CREATE — RFC §14.1 classifies this SAFE ("no data
// involved"), unlike every other opaque-tier DROP+CREATE, which is why
// dropObject's "event_trigger" case already uses safeOp instead of
// destructiveOp. Replaces diffOpaqueIR's generic Reconstructed-gated
// body-hash compare (silently unset on every live path — see
// diffTablespace's doc comment for the same root cause). Tags are compared
// as a set (stringSetEqual): PostgreSQL stores evttags as an unordered
// array, and WHEN TAG IN (...)'s list order carries no meaning.
func diffEventTrigger(o *ir.EventTrigger, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	pos := o.SrcPos
	// Stale snapshot predating these structured fields: EventTriggerEvent
	// is always non-empty for a real event trigger (required by CREATE
	// EVENT TRIGGER's grammar), never the Go zero value — same
	// self-healing guard pattern as diffTablespace/diffCast.
	if snap.EventTriggerEvent == "" {
		var ops []pipeline.DiffOp
		if !ptrEq(o.Comment, snap.Comment) {
			if sql := commentOnOpaqueSQL("event_trigger", "", o.Name, "", "", o.Comment); sql != "" {
				ops = append(ops, safeOp(sql, pos))
			}
		}
		if len(ops) == 0 {
			ops = append(ops, safeOp(fmt.Sprintf("-- refresh snapshot metadata for event trigger %s", quoteIdent(o.Name)), pos))
		}
		return ops, nil
	}
	if o.Event != snap.EventTriggerEvent ||
		qualifyFuncForCompare(o.Function) != qualifyFuncForCompare(snap.EventTriggerFunction) ||
		!stringSetEqual(o.Tags, snap.EventTriggerTags) {
		ops := dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
		createOps, err := createOpaque(o.Name, o.Body, "EVENT TRIGGER", "", pos)
		if err != nil {
			return nil, err
		}
		ops = append(ops, createOps...)
		return appendCommentOp(ops, nil, "event_trigger", "", o.Name, "", "", o.Comment, pos)
	}
	if !ptrEq(o.Comment, snap.Comment) {
		if sql := commentOnOpaqueSQL("event_trigger", "", o.Name, "", "", o.Comment); sql != "" {
			return []pipeline.DiffOp{safeOp(sql, pos)}, nil
		}
	}
	return nil, nil
}

// userMappingPasswordKeys mirrors internal/introspect's and
// internal/snapshot's own copies of the same list (which itself mirrors
// internal/linter's passwordColNames) — kept as a local duplicate rather
// than a cross-package import, following this codebase's established
// pattern for this exact recurring 5-string need.
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

// toComparableOptions converts a desired-side Options list to the snapshot
// comparison shape, optionally excluding password-like keys — used for
// UserMapping (excludeSensitive=true, since the live side never carries a
// real password value to compare against, only a fixed redaction
// placeholder) and reused as a plain passthrough (excludeSensitive=false)
// for FDW/ForeignServer, which have no sensitive-key concept.
func toComparableOptions(opts []pipeline.StorageParam, excludeSensitive bool) []snapshot.SnapOptionKV {
	var out []snapshot.SnapOptionKV
	for _, o := range opts {
		if excludeSensitive && isUserMappingPasswordKey(o.Key) {
			continue
		}
		out = append(out, snapshot.SnapOptionKV{Key: o.Key, Value: o.Value})
	}
	return out
}

// optionsEqual compares two OPTIONS lists as sets (order-independent, since
// PostgreSQL's own OPTIONS have no meaningful order).
func optionsEqual(a, b []snapshot.SnapOptionKV) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]string, len(a))
	for _, kv := range a {
		m[kv.Key] = kv.Value
	}
	for _, kv := range b {
		v, ok := m[kv.Key]
		if !ok || v != kv.Value {
			return false
		}
	}
	return true
}

// alterOptionsSQL builds an ALTER ... OPTIONS (ADD/SET/DROP ...) clause's
// entry list comparing desired against live — confirmed live against a
// real PostgreSQL 17 server: OPTIONS keys need no identifier quoting (even
// reserved words like "user" parse fine unquoted; formatOptions elsewhere
// in this codebase already relies on the same). Returns "" when nothing
// changed.
func alterOptionsSQL(desired, live []snapshot.SnapOptionKV) string {
	liveByKey := make(map[string]string, len(live))
	for _, kv := range live {
		liveByKey[kv.Key] = kv.Value
	}
	desiredByKey := make(map[string]string, len(desired))
	for _, kv := range desired {
		desiredByKey[kv.Key] = kv.Value
	}
	var parts []string
	for _, kv := range desired {
		if lv, ok := liveByKey[kv.Key]; !ok {
			parts = append(parts, fmt.Sprintf("ADD %s %s", kv.Key, quoteLit(kv.Value)))
		} else if lv != kv.Value {
			parts = append(parts, fmt.Sprintf("SET %s %s", kv.Key, quoteLit(kv.Value)))
		}
	}
	for _, kv := range live {
		if _, ok := desiredByKey[kv.Key]; !ok {
			parts = append(parts, fmt.Sprintf("DROP %s", kv.Key))
		}
	}
	return strings.Join(parts, ", ")
}

// diffCollation implements RFC §14.2's structured diffing: "any property
// change requires DROP COLLATION + CREATE COLLATION" (real PostgreSQL's
// ALTER COLLATION supports only REFRESH VERSION/OWNER TO/RENAME TO/SET
// SCHEMA, none of Provider/Collate/Ctype/ICULocale/Deterministic), so this
// is a single "did anything change" comparison, same shape as diffFDW.
// Comparing the resolved Collate/Ctype/ICULocale fields directly (rather
// than the LOCALE-vs-LC_COLLATE/LC_CTYPE shorthand text) is what actually
// closes the gap — see ir.Collation's doc comment. Replaces diffOpaqueIR's
// generic Reconstructed-gated body-hash compare (silently unset on every
// live path).
// diffStatisticsObject implements RFC §14.6's structured diffing table:
// real PostgreSQL's ALTER STATISTICS supports only OWNER TO/RENAME TO/
// SET SCHEMA/SET STATISTICS (confirmed live via `\h ALTER STATISTICS`
// against a real PostgreSQL 17 server), so a Table, Kinds, or Columns
// change decides DROP+CREATE (RFC's "Column list or kinds changed" row,
// DESTRUCTIVE), while a StatisticsTarget-only change gets a real, targeted
// ALTER STATISTICS ... SET STATISTICS (RFC: SAFE). Table is compared via
// qualifyTableForCompare (same helper Publication's Tables use): a
// statistics object's FROM table may be left unqualified in source,
// relying on the default "public" schema, while introspection always
// returns a fully schema-qualified name. Replaces diffOpaqueIR's generic
// Reconstructed-gated body-hash compare (silently unset on every live
// path).
func diffStatisticsObject(o *ir.StatisticsObject, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	pos := o.SrcPos
	ident := qualIdent(o.Schema, o.Name)
	if !snap.StatisticsStructured {
		// Stale snapshot predating these structured fields — same
		// self-healing pattern as diffCollation/diffFDW. An empty
		// Kinds/Columns list is not itself a reliable "unpopulated"
		// signal (a freshly-populated object could legitimately have
		// neither yet, e.g. mid-edit), hence the explicit sentinel.
		var ops []pipeline.DiffOp
		if !ptrEq(o.Comment, snap.Comment) {
			if sql := commentOnOpaqueSQL("statistics", o.Schema, o.Name, "", "", o.Comment); sql != "" {
				ops = append(ops, safeOp(sql, pos))
			}
		}
		if len(ops) == 0 {
			ops = append(ops, safeOp(fmt.Sprintf("-- refresh snapshot metadata for statistics %s", ident), pos))
		}
		return ops, nil
	}
	tableChanged := qualifyTableForCompare(o.Table) != qualifyTableForCompare(snap.StatisticsTable)
	kindsChanged := !stringSetEqual(o.Kinds, snap.StatisticsKinds)
	columnsChanged := !stringSetEqual(o.Columns, snap.StatisticsColumns)
	if tableChanged || kindsChanged || columnsChanged {
		ops := dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
		createOps, err := createOpaque(o.QualifiedName(), o.Body, "STATISTICS", o.Schema, pos)
		if err != nil {
			return nil, err
		}
		ops = append(ops, createOps...)
		return appendCommentOp(ops, nil, "statistics", o.Schema, o.Name, "", "", o.Comment, pos)
	}
	var ops []pipeline.DiffOp
	if !intPtrEq(o.StatisticsTarget, snap.StatisticsTarget) {
		if o.StatisticsTarget != nil {
			ops = append(ops, safeOp(fmt.Sprintf("ALTER STATISTICS %s SET STATISTICS %d;", ident, *o.StatisticsTarget), pos))
		} else {
			ops = append(ops, safeOp(fmt.Sprintf("ALTER STATISTICS %s SET STATISTICS DEFAULT;", ident), pos))
		}
	}
	if !ptrEq(o.Comment, snap.Comment) {
		if sql := commentOnOpaqueSQL("statistics", o.Schema, o.Name, "", "", o.Comment); sql != "" {
			ops = append(ops, safeOp(sql, pos))
		}
	}
	return ops, nil
}

func diffCollation(o *ir.Collation, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	pos := o.SrcPos
	ident := qualIdent(o.Schema, o.Name)
	if !snap.CollationStructured {
		var ops []pipeline.DiffOp
		if !ptrEq(o.Comment, snap.Comment) {
			if sql := commentOnOpaqueSQL("collation", o.Schema, o.Name, "", "", o.Comment); sql != "" {
				ops = append(ops, safeOp(sql, pos))
			}
		}
		if len(ops) == 0 {
			ops = append(ops, safeOp(fmt.Sprintf("-- refresh snapshot metadata for collation %s", ident), pos))
		}
		return ops, nil
	}
	if o.Provider != snap.CollationProvider ||
		!ptrEq(o.Collate, snap.CollationCollate) || !ptrEq(o.Ctype, snap.CollationCtype) || !ptrEq(o.ICULocale, snap.CollationICULocale) ||
		o.Deterministic != snap.CollationDeterministic {
		ops := dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
		createOps, err := createOpaque(o.QualifiedName(), o.Body, "COLLATION", o.Schema, pos)
		if err != nil {
			return nil, err
		}
		ops = append(ops, createOps...)
		return appendCommentOp(ops, nil, "collation", o.Schema, o.Name, "", "", o.Comment, pos)
	}
	if !ptrEq(o.Comment, snap.Comment) {
		if sql := commentOnOpaqueSQL("collation", o.Schema, o.Name, "", "", o.Comment); sql != "" {
			return []pipeline.DiffOp{safeOp(sql, pos)}, nil
		}
	}
	return nil, nil
}

// diffFDW implements RFC §14.8's structured diffing: "any change to a FDW
// requires drop + recreate" is the RFC's own explicit semantics (no
// ALTER FOREIGN DATA WRAPPER path modeled, even though real PostgreSQL has
// one — a deliberate DPG simplification), so this is a single "did
// anything change" comparison across Handler/Validator/Options, in place
// of diffOpaqueIR's generic Reconstructed-gated body-hash compare (silently
// unset on every live path — see diffTablespace's doc comment for the same
// root cause).
func diffFDW(o *ir.ForeignDataWrapper, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	pos := o.SrcPos
	// Stale snapshot predating these structured fields: unlike Tablespace/
	// Cast/EventTrigger, no single field is guaranteed non-empty on a real
	// FDW (a bare FOREIGN DATA WRAPPER with no HANDLER/VALIDATOR/OPTIONS
	// is valid), so OptionsStructured is an explicit sentinel instead.
	if !snap.OptionsStructured {
		var ops []pipeline.DiffOp
		if !ptrEq(o.Comment, snap.Comment) {
			if sql := commentOnOpaqueSQL("fdw", "", o.Name, "", "", o.Comment); sql != "" {
				ops = append(ops, safeOp(sql, pos))
			}
		}
		if len(ops) == 0 {
			ops = append(ops, safeOp(fmt.Sprintf("-- refresh snapshot metadata for foreign data wrapper %s", quoteIdent(o.Name)), pos))
		}
		return ops, nil
	}
	if qualifyFuncForCompare(o.Handler) != qualifyFuncForCompare(snap.FDWHandler) ||
		qualifyFuncForCompare(o.Validator) != qualifyFuncForCompare(snap.FDWValidator) ||
		!optionsEqual(toComparableOptions(o.Options, false), snap.FDWOptions) {
		ops := dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
		createOps, err := createOpaque(o.Name, o.Body, "FOREIGN DATA WRAPPER", "", pos)
		if err != nil {
			return nil, err
		}
		ops = append(ops, createOps...)
		return appendCommentOp(ops, nil, "fdw", "", o.Name, "", "", o.Comment, pos)
	}
	if !ptrEq(o.Comment, snap.Comment) {
		if sql := commentOnOpaqueSQL("fdw", "", o.Name, "", "", o.Comment); sql != "" {
			return []pipeline.DiffOp{safeOp(sql, pos)}, nil
		}
	}
	return nil, nil
}

// diffForeignServer implements RFC §14.9's structured diffing table: a
// FDW-wrapper change decides DROP+CREATE (DESTRUCTIVE) — same for a TYPE
// change, since real PostgreSQL's ALTER SERVER has no TYPE clause
// (confirmed via `\h ALTER SERVER` against a live PostgreSQL 17 server:
// only VERSION and OPTIONS are alterable in place) — while a VERSION or
// OPTIONS change gets a real, targeted ALTER SERVER (SAFE, per the RFC's
// own "OPTIONS changed" row; VERSION follows the identical reasoning since
// PostgreSQL supports altering it the same way). Replaces diffOpaqueIR's
// generic Reconstructed-gated body-hash compare (silently unset on every
// live path).
func diffForeignServer(o *ir.ForeignServer, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	pos := o.SrcPos
	ident := quoteIdent(o.Name)
	if !snap.OptionsStructured {
		var ops []pipeline.DiffOp
		if !ptrEq(o.Comment, snap.Comment) {
			if sql := commentOnOpaqueSQL("server", "", o.Name, "", "", o.Comment); sql != "" {
				ops = append(ops, safeOp(sql, pos))
			}
		}
		if len(ops) == 0 {
			ops = append(ops, safeOp(fmt.Sprintf("-- refresh snapshot metadata for server %s", ident), pos))
		}
		return ops, nil
	}
	if o.FDWName != snap.ServerFDWName || !ptrEq(o.Type, snap.ServerType) {
		ops := dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
		createOps, err := createOpaque(o.Name, o.Body, "SERVER", "", pos)
		if err != nil {
			return nil, err
		}
		ops = append(ops, createOps...)
		return appendCommentOp(ops, nil, "server", "", o.Name, "", "", o.Comment, pos)
	}
	var ops []pipeline.DiffOp
	desiredOptions := toComparableOptions(o.Options, false)
	versionChanged := !ptrEq(o.Version, snap.ServerVersion)
	optionsChanged := !optionsEqual(desiredOptions, snap.ServerOptions)
	if versionChanged || optionsChanged {
		var sb strings.Builder
		fmt.Fprintf(&sb, "ALTER SERVER %s", ident)
		if versionChanged && o.Version != nil {
			fmt.Fprintf(&sb, " VERSION %s", quoteLit(*o.Version))
		}
		if optionsChanged {
			fmt.Fprintf(&sb, " OPTIONS (%s)", alterOptionsSQL(desiredOptions, snap.ServerOptions))
		}
		sb.WriteString(";")
		ops = append(ops, safeOp(sb.String(), pos))
	}
	if !ptrEq(o.Comment, snap.Comment) {
		if sql := commentOnOpaqueSQL("server", "", o.Name, "", "", o.Comment); sql != "" {
			ops = append(ops, safeOp(sql, pos))
		}
	}
	return ops, nil
}

// qualifyTableForCompare mirrors qualifyFuncForCompare (used for Cast/
// EventTrigger's Function fields): a Publication's FOR TABLE entry may be
// left unqualified in source, relying on the default "public" schema,
// while introspection always returns a fully schema-qualified name.
func qualifyTableForCompare(s string) string {
	if strings.Contains(s, ".") {
		return s
	}
	return "public." + s
}

// publicationTableKeys converts Tables to "schema.name"/"name" strings
// matching toSnapObject's own PublicationTables format (see convert.go) —
// raw, un-defaulted, for storage-shape consistency; qualifyTableForCompare
// normalizes at comparison time instead, same layering as
// qualifyFuncForCompare.
func publicationTableKeys(tables []ir.PublicationTableRef) []string {
	out := make([]string, len(tables))
	for i, t := range tables {
		if t.Schema != "" {
			out[i] = t.Schema + "." + t.Name
		} else {
			out[i] = t.Name
		}
	}
	return out
}

// publishClause renders a Publication's WITH (publish = ...) value —
// matches introspect.publishOption's own "insert, update"-style format for
// consistency, though byte-identical output isn't required here (this
// feeds a freshly-built ALTER statement, not a reconstruction compare).
func publishClause(o *ir.Publication) string {
	var ops []string
	if o.Insert {
		ops = append(ops, "insert")
	}
	if o.Update {
		ops = append(ops, "update")
	}
	if o.Delete {
		ops = append(ops, "delete")
	}
	if o.Truncate {
		ops = append(ops, "truncate")
	}
	return strings.Join(ops, ", ")
}

// diffPublication implements RFC §13.1's structured diffing table: a
// FOR ALL TABLES publication can never be converted to/from an explicit
// table list via ALTER (confirmed live against a real PostgreSQL 17
// server: "Tables cannot be added to or dropped from FOR ALL TABLES
// publications") — an AllTables change decides DROP+CREATE (DESTRUCTIVE,
// matching dropObject's existing "publication" case and RFC §13.1's
// "Publication removed" row). A Tables or WITH (publish = ...) change
// each get their own real, targeted ALTER PUBLICATION (RFC §13.1: both
// rows SAFE) — a genuine new capability, not just detection. Replaces
// diffOpaqueIR's generic Reconstructed-gated body-hash compare (silently
// unset on every live path). FOR TABLES IN SCHEMA targets are a
// pre-existing, separate gap not modeled here (see ir.Publication.
// AllTables' doc comment) — unaffected by this change either way.
func diffPublication(o *ir.Publication, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	pos := o.SrcPos
	ident := quoteIdent(o.Name)
	if !snap.PublicationStructured {
		// Stale snapshot predating these structured fields — same
		// self-healing pattern as diffFDW/diffForeignServer.
		var ops []pipeline.DiffOp
		if !ptrEq(o.Comment, snap.Comment) {
			if sql := commentOnOpaqueSQL("publication", "", o.Name, "", "", o.Comment); sql != "" {
				ops = append(ops, safeOp(sql, pos))
			}
		}
		if len(ops) == 0 {
			ops = append(ops, safeOp(fmt.Sprintf("-- refresh snapshot metadata for publication %s", ident), pos))
		}
		return ops, nil
	}
	if o.AllTables != snap.PublicationAllTables {
		ops := dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
		createOps, err := createOpaque(o.Name, o.Body, "PUBLICATION", "", pos)
		if err != nil {
			return nil, err
		}
		ops = append(ops, createOps...)
		return appendCommentOp(ops, nil, "publication", "", o.Name, "", "", o.Comment, pos)
	}
	var ops []pipeline.DiffOp
	if !o.AllTables {
		desiredKeys := publicationTableKeys(o.Tables)
		normDesired := make([]string, len(desiredKeys))
		for i, k := range desiredKeys {
			normDesired[i] = qualifyTableForCompare(k)
		}
		normLive := make([]string, len(snap.PublicationTables))
		for i, k := range snap.PublicationTables {
			normLive[i] = qualifyTableForCompare(k)
		}
		if !stringSetEqual(normDesired, normLive) {
			if o.HasFilteredTables || snap.PublicationHasFilteredTables {
				// A column-list/WHERE filter exists on at least one table
				// entry, on at least one side — PublicationTableRef can't
				// represent either, so a targeted SET TABLE here would
				// silently rebuild the table list without it (see
				// ir.Publication.HasFilteredTables' doc comment). Fall back
				// to a full DROP+CREATE, which always regenerates from the
				// complete, correct Body.
				ops = dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
				createOps, err := createOpaque(o.Name, o.Body, "PUBLICATION", "", pos)
				if err != nil {
					return nil, err
				}
				ops = append(ops, createOps...)
				return appendCommentOp(ops, nil, "publication", "", o.Name, "", "", o.Comment, pos)
			}
			idents := make([]string, len(o.Tables))
			for i, t := range o.Tables {
				idents[i] = qualIdent(t.Schema, t.Name)
			}
			ops = append(ops, safeOp(fmt.Sprintf("ALTER PUBLICATION %s SET TABLE %s;", ident, strings.Join(idents, ", ")), pos))
		}
	}
	if o.Insert != snap.PublicationInsert || o.Update != snap.PublicationUpdate ||
		o.Delete != snap.PublicationDelete || o.Truncate != snap.PublicationTruncate {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER PUBLICATION %s SET (publish = %s);", ident, quoteLit(publishClause(o))), pos))
	}
	if !ptrEq(o.Comment, snap.Comment) {
		if sql := commentOnOpaqueSQL("publication", "", o.Name, "", "", o.Comment); sql != "" {
			ops = append(ops, safeOp(sql, pos))
		}
	}
	return ops, nil
}

// diffSubscription mirrors diffOpaqueIR's shape (offline body-hash compare,
// structured DROP+CREATE on a real change) but can't reuse it directly: its
// CREATE side must produce a subscriptionCreateOp, not a generic createOpaque
// op, and Comment is diffed at the field level (like every other
// Comment-bearing kind) rather than folded into the body hash — a
// comment-only edit doesn't need the subscription dropped and recreated.
func diffSubscription(o *ir.Subscription, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	if o.Body != "" {
		newHash := hashText(o.Body)
		if snap.BodyHash != "" && newHash != snap.BodyHash {
			ops := dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
			createOps, err := createSubscription(o)
			if err != nil {
				return nil, err
			}
			return append(ops, createOps...), nil
		}
	}
	var ops []pipeline.DiffOp
	if !ptrEq(o.Comment, snap.Comment) {
		if o.Comment != nil {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON SUBSCRIPTION %s IS %s;", quoteIdent(o.Name), quoteLit(*o.Comment)), o.SrcPos))
		} else {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON SUBSCRIPTION %s IS NULL;", quoteIdent(o.Name)), o.SrcPos))
		}
	}
	return ops, nil
}

func buildProcedureSignature(o *ir.Procedure) string {
	return fmt.Sprintf("%s(%s)", qualIdent(o.Schema, o.Name), ir.ArgsKey(o.Args))
}

func createProcedure(o *ir.Procedure) []pipeline.DiffOp {
	ops := []pipeline.DiffOp{safeOp(ir.RenderCreateProcedureSQL(o), o.SrcPos)}
	sig := buildProcedureSignature(o)
	if o.Comment != nil {
		ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON PROCEDURE %s IS %s;", sig, quoteLit(*o.Comment)), o.SrcPos))
	}
	for _, g := range o.Grants {
		sql := fmt.Sprintf("GRANT %s ON PROCEDURE %s TO %s", privStr(g.Privileges), sig, roleList(g.Roles))
		if g.WithGrant {
			sql += " WITH GRANT OPTION"
		}
		ops = append(ops, safeOp(sql+";", o.SrcPos))
	}
	for _, r := range o.Revocations {
		ops = append(ops, explicitRevokeOp(r, "PROCEDURE "+sig, o.SrcPos))
	}
	return ops
}

func buildAggregateSignature(o *ir.Aggregate) string {
	return fmt.Sprintf("%s(%s)", qualIdent(o.Schema, o.Name), ir.ArgsKey(o.Args))
}

func createAggregate(o *ir.Aggregate) ([]pipeline.DiffOp, error) {
	if o.Body == "" {
		return nil, fmt.Errorf("aggregate %s: body not captured; define it explicitly in a .dpg source file", o.QualifiedName())
	}
	sig := buildAggregateSignature(o)
	ops := []pipeline.DiffOp{safeOp(o.Body+";", o.SrcPos)}
	if o.Comment != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("COMMENT ON AGGREGATE %s IS %s;", sig, quoteLit(*o.Comment)),
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
	return ops, nil
}

func diffAggregate(o *ir.Aggregate, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	sig := buildAggregateSignature(o)
	pos := o.SrcPos

	var newHash string
	if o.Body != "" {
		sum := sha256.Sum256([]byte(strings.TrimSpace(o.Body)))
		newHash = fmt.Sprintf("%x", sum)
	}
	// Skip body comparison when either side has no hash: the live snapshot
	// (introspected) cannot reconstruct the aggregate body, so we only diff
	// body when both sides have a hash (offline plan against committed snapshot).
	bodyChanged := newHash != "" && snap.BodyHash != "" && newHash != snap.BodyHash

	if bodyChanged {
		ops := []pipeline.DiffOp{
			destructiveOp(fmt.Sprintf("DROP AGGREGATE IF EXISTS %s;", sig), pos),
			safeOp(o.Body+";", pos),
		}
		if o.Comment != nil {
			ops = append(ops, safeOp(
				fmt.Sprintf("COMMENT ON AGGREGATE %s IS %s;", sig, quoteLit(*o.Comment)),
				pos,
			))
		}
		ops = append(ops, diffGrantSet(nil, o.Grants, "FUNCTION "+sig, pos)...)
		return ops, nil
	}

	var ops []pipeline.DiffOp
	if !ptrEq(o.Comment, snap.Comment) {
		if o.Comment != nil {
			ops = append(ops, safeOp(
				fmt.Sprintf("COMMENT ON AGGREGATE %s IS %s;", sig, quoteLit(*o.Comment)),
				pos,
			))
		} else {
			ops = append(ops, safeOp(
				fmt.Sprintf("COMMENT ON AGGREGATE %s IS NULL;", sig),
				pos,
			))
		}
	}
	ops = append(ops, diffGrantSet(snap.Grants, o.Grants, "FUNCTION "+sig, pos)...)
	return ops, nil
}

func createDefaultPrivileges(o *ir.DefaultPrivileges) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	pos := o.SrcPos
	prefix := buildDefaultPrivPrefix(o.ForRole, o.InSchema)
	for _, g := range o.Grants {
		sql := fmt.Sprintf("%s GRANT %s ON %s TO %s",
			prefix, privStr(g.Privileges), o.ObjectType, roleList(g.Roles))
		if g.WithGrant {
			sql += " WITH GRANT OPTION"
		}
		ops = append(ops, safeOp(sql+";", pos))
	}
	for _, r := range o.Revocations {
		cascade := ""
		if r.Cascade {
			cascade = " CASCADE"
		}
		sql := fmt.Sprintf("%s REVOKE %s ON %s FROM %s%s",
			prefix, privStr(r.Privileges), o.ObjectType, roleList(r.Roles), cascade)
		ops = append(ops, cautionOp(sql+";", pos))
	}
	return ops
}

// diffDefaultPrivileges diffs a DEFAULT PRIVILEGES declaration against its snapshot.
// Grants are diffed structurally; revocations are re-emitted whenever their set changes.
func diffDefaultPrivileges(o *ir.DefaultPrivileges, snap *snapshot.SnapDefaultPrivileges) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	pos := o.SrcPos

	prefix := buildDefaultPrivPrefix(o.ForRole, o.InSchema)

	// Diff grants.
	snapGrantsByKey := make(map[string]snapshot.SnapGrant, len(snap.Grants))
	for _, g := range snap.Grants {
		snapGrantsByKey[grantKey(g.Privileges, g.Roles, g.WithGrant)] = g
	}
	desiredGrantsByKey := make(map[string]ir.Grant, len(o.Grants))
	for _, g := range o.Grants {
		desiredGrantsByKey[grantKey(g.Privileges, g.Roles, g.WithGrant)] = g
	}

	for k, sg := range snapGrantsByKey {
		if _, ok := desiredGrantsByKey[k]; !ok {
			ops = append(ops, cautionOp(
				fmt.Sprintf("%s REVOKE %s ON %s FROM %s;",
					prefix, privStr(sg.Privileges), o.ObjectType, roleList(sg.Roles)),
				pos,
			))
		}
	}
	for k, g := range desiredGrantsByKey {
		if _, ok := snapGrantsByKey[k]; !ok {
			sql := fmt.Sprintf("%s GRANT %s ON %s TO %s",
				prefix, privStr(g.Privileges), o.ObjectType, roleList(g.Roles))
			if g.WithGrant {
				sql += " WITH GRANT OPTION"
			}
			ops = append(ops, safeOp(sql+";", pos))
		}
	}

	// Diff explicit revocations.
	snapRevsByKey := make(map[string]snapshot.SnapGrant, len(snap.Revocations))
	for _, r := range snap.Revocations {
		snapRevsByKey[grantKey(r.Privileges, r.Roles, false)] = r
	}
	desiredRevsByKey := make(map[string]ir.Revocation, len(o.Revocations))
	for _, r := range o.Revocations {
		desiredRevsByKey[grantKey(r.Privileges, r.Roles, false)] = r
	}

	for k, sr := range snapRevsByKey {
		if _, ok := desiredRevsByKey[k]; !ok {
			// Revocation removed from desired: re-grant to restore.
			ops = append(ops, safeOp(
				fmt.Sprintf("%s GRANT %s ON %s TO %s;",
					prefix, privStr(sr.Privileges), o.ObjectType, roleList(sr.Roles)),
				pos,
			))
		}
	}
	for k, r := range desiredRevsByKey {
		if _, ok := snapRevsByKey[k]; !ok {
			cascade := ""
			if r.Cascade {
				cascade = " CASCADE"
			}
			ops = append(ops, cautionOp(
				fmt.Sprintf("%s REVOKE %s ON %s FROM %s%s;",
					prefix, privStr(r.Privileges), o.ObjectType, roleList(r.Roles), cascade),
				pos,
			))
		}
	}

	return ops
}

// buildDefaultPrivPrefix builds the "ALTER DEFAULT PRIVILEGES [FOR ROLE x] [IN SCHEMA y]" prefix.
func buildDefaultPrivPrefix(forRole, inSchema *string) string {
	var b strings.Builder
	b.WriteString("ALTER DEFAULT PRIVILEGES")
	if forRole != nil {
		b.WriteString(" FOR ROLE ")
		b.WriteString(quoteIdent(*forRole))
	}
	if inSchema != nil {
		b.WriteString(" IN SCHEMA ")
		b.WriteString(quoteIdent(*inSchema))
	}
	return b.String()
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
	schemaIdent := quoteIdent(o.Name)
	for _, g := range o.Grants {
		sql := fmt.Sprintf("GRANT %s ON SCHEMA %s TO %s", privStr(g.Privileges), schemaIdent, roleList(g.Roles))
		if g.WithGrant {
			sql += " WITH GRANT OPTION"
		}
		ops = append(ops, safeOp(sql+";", o.SrcPos))
	}
	for _, r := range o.Revocations {
		ops = append(ops, explicitRevokeOp(r, "SCHEMA "+schemaIdent, o.SrcPos))
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

func createTable(o *ir.Table, vtypes map[string]string) []pipeline.DiffOp {
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

	// Classify constraints: single-column PK/UNIQUE/FK and column-promoted CHECK
	// are inlined into their column; everything else stays table-level.
	// A column may accumulate multiple inline clauses (e.g. UNIQUE + REFERENCES
	// + CHECK), so inlineFor holds a slice ordered by declaration.
	// pkColSet tracks all PK columns so we can suppress redundant NOT NULL
	// (PRIMARY KEY already implies NOT NULL in PostgreSQL).
	type inlineKW struct {
		name    string // CONSTRAINT name, may be ""
		keyword string // e.g. "PRIMARY KEY", "UNIQUE", "REFERENCES ...", "CHECK (...)"
	}
	inlineFor := make(map[string][]inlineKW) // colName → ordered inline clauses
	pkColSet := make(map[string]bool)        // any PK column (single- or multi-)
	skipIdx := make(map[int]bool)            // constraint index to omit at table level

	for i, cst := range o.Constraints {
		cols := localConstraintCols(cst.Expr)
		switch cst.Type {
		case "PRIMARY KEY":
			for _, c := range cols {
				pkColSet[c] = true
			}
			if len(cols) == 1 {
				inlineFor[cols[0]] = append(inlineFor[cols[0]], inlineKW{name: cst.Name, keyword: "PRIMARY KEY"})
				skipIdx[i] = true
			}
		case "UNIQUE":
			if len(cols) == 1 {
				// Skip if column already carries PRIMARY KEY — UNIQUE is redundant.
				hasPK := false
				for _, kw := range inlineFor[cols[0]] {
					if kw.keyword == "PRIMARY KEY" {
						hasPK = true
						break
					}
				}
				if !hasPK {
					kw := "UNIQUE"
					if strings.Contains(strings.ToUpper(cst.Expr), "NULLS NOT DISTINCT") {
						kw = "UNIQUE NULLS NOT DISTINCT"
					}
					inlineFor[cols[0]] = append(inlineFor[cols[0]], inlineKW{name: cst.Name, keyword: kw})
					skipIdx[i] = true
				}
			}
		case "FOREIGN KEY":
			// Single-column FK: strip "FOREIGN KEY ("col") " prefix, keep "REFERENCES ...".
			// The REFERENCES suffix includes ON DELETE/UPDATE actions and DEFERRABLE.
			if len(cols) == 1 {
				upper := strings.ToUpper(cst.Expr)
				if refIdx := strings.Index(upper, "REFERENCES"); refIdx > 0 {
					inlineFor[cols[0]] = append(inlineFor[cols[0]], inlineKW{name: cst.Name, keyword: cst.Expr[refIdx:]})
					skipIdx[i] = true
				}
			}
		case "CHECK":
			// Inline CHECK when Columns is populated — that means the constraint was
			// promoted from a column definition (buildColumn sets Columns=[colname]).
			// Table-level CHECK constraints (Columns empty) stay table-level because
			// we cannot safely infer which column they belong to from the expression.
			if len(cst.Columns) == 1 {
				inlineFor[cst.Columns[0]] = append(inlineFor[cst.Columns[0]], inlineKW{name: cst.Name, keyword: cst.Expr})
				skipIdx[i] = true
			}
		}
	}

	for i, col := range o.Columns {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n    ")
		b.WriteString(quoteIdent(col.Name))
		b.WriteString(" ")
		if col.Serial != nil {
			// Emit the literal SERIAL/BIGSERIAL/SMALLSERIAL keyword and let
			// PostgreSQL's own server-side macro-expansion create the
			// backing sequence, default, ownership, and implicit NOT NULL —
			// rather than DPG hand-reconstructing that expansion itself.
			b.WriteString(*col.Serial)
		} else {
			b.WriteString(resolveColType(col.Type, vtypes))
		}
		// Suppress NOT NULL for PK columns — PRIMARY KEY already implies it.
		// Also suppress for SERIAL — PG's own macro-expansion adds it.
		if col.NotNull && !pkColSet[col.Name] && col.Serial == nil {
			b.WriteString(" NOT NULL")
		}
		if col.Default != nil && col.Serial == nil {
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
		for _, spec := range inlineFor[col.Name] {
			if spec.name != "" {
				b.WriteString(" CONSTRAINT ")
				b.WriteString(quoteIdent(spec.name))
			}
			b.WriteString(" ")
			b.WriteString(spec.keyword)
		}
	}
	for i, cst := range o.Constraints {
		if skipIdx[i] {
			continue
		}
		b.WriteString(",\n    ")
		if cst.Name != "" {
			b.WriteString("CONSTRAINT ")
			b.WriteString(quoteIdent(cst.Name))
			b.WriteString(" ")
		}
		b.WriteString(cst.Expr)
	}
	b.WriteString("\n)")
	if o.PartitionBy != nil && !o.Foreign {
		b.WriteString(" PARTITION BY ")
		b.WriteString(o.PartitionBy.Strategy)
		b.WriteString(" (")
		b.WriteString(strings.Join(o.PartitionBy.Columns, ", "))
		b.WriteString(")")
	}
	if o.Foreign {
		// Real PostgreSQL grammar: CREATE FOREIGN TABLE name (cols)
		// SERVER server_name [OPTIONS (...)]. Without this, the emitted
		// statement was missing SERVER entirely — not just incomplete, an
		// outright syntax error, since SERVER is mandatory for a foreign table.
		if o.ForeignServer != nil {
			b.WriteString(" SERVER ")
			b.WriteString(quoteIdent(*o.ForeignServer))
		}
		if len(o.ForeignOptions) > 0 {
			b.WriteString(" OPTIONS (")
			for i, p := range o.ForeignOptions {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(p.Key)
				b.WriteString(" ")
				b.WriteString(quoteLit(p.Value))
			}
			b.WriteString(")")
		}
	} else if o.Tablespace != nil {
		// TABLESPACE is real PostgreSQL's final CREATE TABLE clause, and
		// meaningless for a foreign table (it stores no local data).
		b.WriteString(" TABLESPACE ")
		b.WriteString(quoteIdent(*o.Tablespace))
	}
	b.WriteString(";")

	var ops []pipeline.DiffOp
	ops = append(ops, safeOp(b.String(), o.SrcPos))
	for _, p := range o.Partitions {
		ops = append(ops, createPartitionOps(o.Schema, qualIdent(o.Schema, o.Name), p)...)
	}

	if o.Owner != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("ALTER TABLE %s OWNER TO %s;", qualIdent(o.Schema, o.Name), quoteIdent(*o.Owner)),
			o.SrcPos,
		))
	}
	if txt := effectiveComment(o.Comment, o.Deprecated); txt != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("COMMENT ON TABLE %s IS %s;", qualIdent(o.Schema, o.Name), quoteLit(*txt)),
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
		// Always non-concurrent, regardless of idx.Concurrently or the
		// project's concurrent_indexes default: this table doesn't exist yet,
		// so its indexes are created in the SAME transactional migration as
		// the CREATE TABLE itself — and PostgreSQL rejects CREATE INDEX
		// CONCURRENTLY inside a transaction block. See createIndex's doc
		// comment; diffIndexes is the path where the default/explicit
		// CONCURRENTLY actually applies (adding an index to a table that
		// already exists).
		ops = append(ops, createIndex(o.Schema, o.Name, idx, false)...)
	}
	for _, pol := range o.Policies {
		ops = append(ops, createPolicy(o.Schema, o.Name, pol)...)
	}
	for _, trg := range o.Triggers {
		ops = append(ops, createTrigger(o.Schema, o.Name, trg)...)
	}
	tblIdent := qualIdent(o.Schema, o.Name)
	for _, g := range o.Grants {
		ops = append(ops, tableGrantOp(g, tblIdent, o.SrcPos))
	}
	for _, r := range o.Revocations {
		ops = append(ops, explicitRevokeOp(r, "TABLE "+tblIdent, o.SrcPos))
	}
	for _, col := range o.Columns {
		for _, g := range col.Grants {
			ops = append(ops, colGrantOp(g, tblIdent, col.Name, col.SrcPos))
		}
		for _, r := range col.Revocations {
			ops = append(ops, colExplicitRevokeOp(r, tblIdent, col.Name, col.SrcPos))
		}
	}
	return ops
}

// createIndex builds the CREATE INDEX statement for idx. concurrent is the
// caller-resolved effective value — NOT idx.Concurrently directly — since an
// index created alongside its own brand-new table is always forced to false
// regardless of idx.Concurrently: PostgreSQL rejects CREATE INDEX
// CONCURRENTLY inside a transaction block, and a new table's indexes are
// always emitted transactionally with it (see createTable). An index added
// to an already-existing table (see diffIndexes) passes idx.Concurrently
// straight through — CONCURRENTLY only ever fires when the source wrote it
// explicitly; there is no project-wide default.
func createIndex(schema, table string, idx *ir.Index, concurrent bool) []pipeline.DiffOp {
	var b strings.Builder
	b.WriteString("CREATE ")
	if idx.Unique {
		b.WriteString("UNIQUE ")
	}
	b.WriteString("INDEX ")
	if concurrent {
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
	b.WriteString(")")
	// Covering (INCLUDE) columns and the partial-index predicate must be emitted
	// or the created index silently differs from the declared one.
	if len(idx.Include) > 0 {
		b.WriteString(" INCLUDE (")
		for i, c := range idx.Include {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(quoteIdent(c))
		}
		b.WriteString(")")
	}
	if idx.NullsNotDistinct {
		b.WriteString(" NULLS NOT DISTINCT")
	}
	if len(idx.With) > 0 {
		b.WriteString(" WITH (")
		for i, p := range idx.With {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(p.Key)
			b.WriteString("=")
			b.WriteString(p.Value)
		}
		b.WriteString(")")
	}
	if idx.Tablespace != nil {
		b.WriteString(" TABLESPACE ")
		b.WriteString(quoteIdent(*idx.Tablespace))
	}
	if idx.Where != nil && *idx.Where != "" {
		b.WriteString(" WHERE ")
		b.WriteString(*idx.Where)
	}
	b.WriteString(";")

	if concurrent {
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
		b.WriteString(roleList(pol.Roles))
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
	sql := fmt.Sprintf("GRANT %s ON TABLE %s TO %s", privs, tblIdent, roleList(g.Roles))
	if g.WithGrant {
		sql += " WITH GRANT OPTION"
	}
	sql += ";"
	return safeOp(sql, pos)
}

func colGrantOp(g ir.Grant, tbl, col string, pos pipeline.SourcePos) pipeline.DiffOp {
	colIdent := quoteIdent(col)
	var privParts []string
	if len(g.Privileges) == 0 {
		privParts = []string{"ALL (" + colIdent + ")"}
	} else {
		for _, p := range g.Privileges {
			privParts = append(privParts, p+" ("+colIdent+")")
		}
	}
	sql := "GRANT " + strings.Join(privParts, ", ") + " ON TABLE " + tbl + " TO " + roleList(g.Roles)
	if g.WithGrant {
		sql += " WITH GRANT OPTION"
	}
	return safeOp(sql+";", pos)
}

// explicitRevokeOp emits the REVOKE for an explicit REVOCATION directive
// (RFC §11.3) — distinct from the additive-model's implicit revoke (see
// colRevokeOp) that fires when a GRANT declaration is simply removed. Explicit
// revocations are CAUTION (not SAFE): they can break access that something
// else depends on. PG's REVOKE grammar has no IF EXISTS clause, so there's
// no guard to add regardless. onClause is the SQL object specifier after ON,
// e.g. "TABLE \"public\".\"users\"" or "FUNCTION \"public\".\"f\"(integer)".
func explicitRevokeOp(r ir.Revocation, onClause string, pos pipeline.SourcePos) *op {
	var privs string
	if len(r.Privileges) == 0 {
		privs = "ALL"
	} else {
		privs = strings.Join(r.Privileges, ", ")
	}
	sql := fmt.Sprintf("REVOKE %s ON %s FROM %s", privs, onClause, roleList(r.Roles))
	if r.Cascade {
		sql += " CASCADE"
	}
	sql += ";"
	return cautionOp(sql, pos)
}

// colExplicitRevokeOp is explicitRevokeOp's column-level counterpart,
// mirroring colGrantOp's column-parenthesized privilege syntax.
func colExplicitRevokeOp(r ir.Revocation, tbl, col string, pos pipeline.SourcePos) pipeline.DiffOp {
	colIdent := quoteIdent(col)
	var privParts []string
	if len(r.Privileges) == 0 {
		privParts = []string{"ALL (" + colIdent + ")"}
	} else {
		for _, p := range r.Privileges {
			privParts = append(privParts, p+" ("+colIdent+")")
		}
	}
	sql := "REVOKE " + strings.Join(privParts, ", ") + " ON TABLE " + tbl + " FROM " + roleList(r.Roles)
	if r.Cascade {
		sql += " CASCADE"
	}
	return cautionOp(sql+";", pos)
}

func colRevokeOp(sg snapshot.SnapGrant, tbl, col string, pos pipeline.SourcePos) pipeline.DiffOp {
	colIdent := quoteIdent(col)
	var privParts []string
	if len(sg.Privileges) == 0 {
		privParts = []string{"ALL (" + colIdent + ")"}
	} else {
		for _, p := range sg.Privileges {
			privParts = append(privParts, p+" ("+colIdent+")")
		}
	}
	return safeOp(
		"REVOKE "+strings.Join(privParts, ", ")+" ON TABLE "+tbl+" FROM "+roleList(sg.Roles)+";",
		pos,
	)
}

func diffColGrantSet(tbl, col string, snapGrants []snapshot.SnapGrant, desiredGrants []ir.Grant, pos pipeline.SourcePos) []pipeline.DiffOp {
	snapKeys := make(map[string]snapshot.SnapGrant, len(snapGrants))
	for _, g := range snapGrants {
		snapKeys[grantKey(g.Privileges, g.Roles, g.WithGrant)] = g
	}
	desiredKeys := make(map[string]ir.Grant, len(desiredGrants))
	for _, g := range desiredGrants {
		desiredKeys[grantKey(g.Privileges, g.Roles, g.WithGrant)] = g
	}
	var ops []pipeline.DiffOp
	for k, sg := range snapKeys {
		if _, ok := desiredKeys[k]; !ok {
			ops = append(ops, colRevokeOp(sg, tbl, col, pos))
		}
	}
	for k, g := range desiredKeys {
		if _, ok := snapKeys[k]; !ok {
			ops = append(ops, colGrantOp(g, tbl, col, pos))
		}
	}
	return ops
}

// diffColRevocationSet is diffRevocationSet's column-level counterpart,
// mirroring diffColGrantSet's structure for explicit REVOCATIONS on a column.
func diffColRevocationSet(tbl, col string, snapRevs []snapshot.SnapGrant, desiredRevs []ir.Revocation, pos pipeline.SourcePos) []pipeline.DiffOp {
	snapKeys := make(map[string]snapshot.SnapGrant, len(snapRevs))
	for _, r := range snapRevs {
		snapKeys[grantKey(r.Privileges, r.Roles, false)] = r
	}
	desiredKeys := make(map[string]ir.Revocation, len(desiredRevs))
	for _, r := range desiredRevs {
		desiredKeys[grantKey(r.Privileges, r.Roles, false)] = r
	}
	var ops []pipeline.DiffOp
	for k, sr := range snapKeys {
		if _, ok := desiredKeys[k]; !ok {
			// Revocation removed from desired: re-grant to restore.
			ops = append(ops, colGrantOp(ir.Grant{Privileges: sr.Privileges, Roles: sr.Roles}, tbl, col, pos))
		}
	}
	for k, r := range desiredKeys {
		if _, ok := snapKeys[k]; !ok {
			ops = append(ops, colExplicitRevokeOp(r, tbl, col, pos))
		}
	}
	return ops
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
	if o.Owner != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("ALTER %s %s OWNER TO %s;", viewKind, qualIdent(o.Schema, o.Name), quoteIdent(*o.Owner)),
			o.SrcPos,
		))
	}
	if txt := effectiveComment(o.Comment, o.Deprecated); txt != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("COMMENT ON %s %s IS %s;", viewKind, qualIdent(o.Schema, o.Name), quoteLit(*txt)),
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
	for _, r := range o.Revocations {
		ops = append(ops, explicitRevokeOp(r, "TABLE "+viewIdent, o.SrcPos))
	}
	for _, idx := range o.Indexes {
		// Always non-concurrent — this view doesn't exist yet, so its
		// indexes are created in the same transactional migration as the
		// CREATE MATERIALIZED VIEW itself, matching createTable's identical
		// reasoning for a brand-new table's indexes.
		ops = append(ops, createIndex(o.Schema, o.Name, idx, false)...)
	}
	return ops
}

func createFunction(o *ir.Function) []pipeline.DiffOp {
	ops := []pipeline.DiffOp{safeOp(buildFunctionSQL(o), o.SrcPos)}
	sig := buildFuncSignature(o)
	if txt := effectiveComment(o.Comment, o.Deprecated); txt != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("COMMENT ON FUNCTION %s IS %s;", sig, quoteLit(*txt)),
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
	for _, r := range o.Revocations {
		ops = append(ops, explicitRevokeOp(r, "FUNCTION "+sig, o.SrcPos))
	}
	return ops
}

func buildFuncSignature(o *ir.Function) string {
	return fmt.Sprintf("%s(%s)", qualIdent(o.Schema, o.Name), ir.ArgsKey(o.Args))
}

func buildFunctionSQL(o *ir.Function) string {
	return ir.RenderCreateFunctionSQL(o)
}

func createType(o *ir.Type, vtypes map[string]string) []pipeline.DiffOp {
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
	case "COMPOSITE":
		ops = append(ops, safeOp(buildCompositeTypeSQL(o, vtypes), o.SrcPos))
	case "DOMAIN":
		ops = append(ops, safeOp(buildDomainSQL(o), o.SrcPos))
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

func buildCompositeTypeSQL(o *ir.Type, vtypes map[string]string) string {
	var b strings.Builder
	b.WriteString("CREATE TYPE ")
	b.WriteString(qualIdent(o.Schema, o.Name))
	b.WriteString(" AS (")
	for i, attr := range o.CompositeAttrs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quoteIdent(attr.Name))
		b.WriteString(" ")
		b.WriteString(resolveColType(attr.Type, vtypes))
	}
	b.WriteString(");")
	return b.String()
}

// buildDomainSQL builds a full CREATE DOMAIN statement from ir.Type's
// structured domain fields — never from o.Body, which (for a domain
// declared via RFC §5.4's block syntax) may only ever have captured the
// bare "name AS basetype" from Part 1, missing any DEFAULT/CONSTRAINT/NOT
// NULL the user put in the { } block instead. Building from the structured
// fields is correct regardless of which syntax (real PG's own inline form,
// the RFC's block form, or a mix) the user wrote.
func buildDomainSQL(o *ir.Type) string {
	var b strings.Builder
	b.WriteString("CREATE DOMAIN ")
	b.WriteString(qualIdent(o.Schema, o.Name))
	b.WriteString(" AS ")
	b.WriteString(o.DomainBaseType.String())
	if o.DomainDefault != nil {
		b.WriteString(" DEFAULT ")
		b.WriteString(*o.DomainDefault)
	}
	if o.DomainNotNull {
		b.WriteString(" NOT NULL")
	}
	for _, cst := range o.DomainConstraints {
		b.WriteString(" ")
		if cst.Name != "" {
			b.WriteString("CONSTRAINT ")
			b.WriteString(quoteIdent(cst.Name))
			b.WriteString(" ")
		}
		b.WriteString(cst.Expr)
	}
	b.WriteString(";")
	return b.String()
}

func createSequence(o *ir.Sequence) []pipeline.DiffOp {
	ident := qualIdent(o.Schema, o.Name)
	var b strings.Builder
	b.WriteString("CREATE SEQUENCE IF NOT EXISTS ")
	b.WriteString(ident)
	writeSeqParams(&b, o)
	b.WriteString(";")
	ops := []pipeline.DiffOp{safeOp(b.String(), o.SrcPos)}
	if o.Owner != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("ALTER SEQUENCE %s OWNER TO %s;", ident, quoteIdent(*o.Owner)),
			o.SrcPos,
		))
	}
	if o.Comment != nil {
		ops = append(ops, safeOp(
			fmt.Sprintf("COMMENT ON SEQUENCE %s IS %s;", ident, quoteLit(*o.Comment)),
			o.SrcPos,
		))
	}
	return ops
}

// writeSeqParams appends explicit sequence parameters to b for any non-nil fields.
func writeSeqParams(b *strings.Builder, o *ir.Sequence) {
	if o.IncrementBy != nil {
		fmt.Fprintf(b, " INCREMENT BY %d", *o.IncrementBy)
	}
	if o.MinValue != nil {
		fmt.Fprintf(b, " MINVALUE %d", *o.MinValue)
	}
	if o.MaxValue != nil {
		fmt.Fprintf(b, " MAXVALUE %d", *o.MaxValue)
	}
	if o.StartValue != nil {
		fmt.Fprintf(b, " START WITH %d", *o.StartValue)
	}
	if o.Cache != nil {
		fmt.Fprintf(b, " CACHE %d", *o.Cache)
	}
	if o.Cycle != nil && *o.Cycle {
		b.WriteString(" CYCLE")
	}
}

// writeRoleBoolOpt writes " ON" or " OFF" (PostgreSQL's toggle-pair keywords)
// to b if v is non-nil, nothing otherwise — mirrors RFC §11.1's "any option
// a declaration omits is simply not managed by DPG" rule at the SQL-building
// level.
func writeRoleBoolOpt(b *strings.Builder, v *bool, on, off string) {
	if v == nil {
		return
	}
	if *v {
		b.WriteString(" " + on)
	} else {
		b.WriteString(" " + off)
	}
}

// roleAttrClause builds the "<opt1> <opt2> ..." portion of a CREATE
// ROLE/ALTER ROLE statement for every non-PASSWORD, non-membership
// attribute the declaration sets (RFC §11.1) — valid in both statement
// forms, unlike IN ROLE/ROLE/ADMIN (create-time only) and unlike PASSWORD
// (kept separate so callers can pass either the raw declared text or an
// apply-time-resolved value; nil password omits the clause entirely).
func roleAttrClause(o *ir.Role, password *string) string {
	var b strings.Builder
	writeRoleBoolOpt(&b, o.CanLogin, "LOGIN", "NOLOGIN")
	writeRoleBoolOpt(&b, o.Superuser, "SUPERUSER", "NOSUPERUSER")
	writeRoleBoolOpt(&b, o.CreateDB, "CREATEDB", "NOCREATEDB")
	writeRoleBoolOpt(&b, o.CreateRole, "CREATEROLE", "NOCREATEROLE")
	writeRoleBoolOpt(&b, o.Inherit, "INHERIT", "NOINHERIT")
	writeRoleBoolOpt(&b, o.IsReplication, "REPLICATION", "NOREPLICATION")
	writeRoleBoolOpt(&b, o.BypassRLS, "BYPASSRLS", "NOBYPASSRLS")
	if o.ConnectionLimit != nil {
		fmt.Fprintf(&b, " CONNECTION LIMIT %d", *o.ConnectionLimit)
	}
	if password != nil {
		b.WriteString(" PASSWORD " + quoteLit(*password))
	}
	if o.ValidUntil != nil {
		b.WriteString(" VALID UNTIL " + quoteLit(*o.ValidUntil))
	}
	return b.String()
}

// buildCreateRoleSQL builds a full CREATE ROLE statement, including the
// create-time-only IN ROLE/ROLE/ADMIN membership clauses (RFC §11.1) —
// PostgreSQL's ALTER ROLE has no membership clause at all, so this shape
// (attrs + membership in one CREATE) is only ever used at creation time;
// later membership changes are diffed as GRANT/REVOKE (see
// diffRoleMembership). password is passed separately from o.Password so
// callers can build either the display form (raw declared text, may
// contain {{...}}) or the apply-time exec form (resolved value).
func buildCreateRoleSQL(o *ir.Role, password *string) string {
	var b strings.Builder
	b.WriteString("CREATE ROLE ")
	b.WriteString(quoteIdent(o.Name))
	b.WriteString(roleAttrClause(o, password))
	if len(o.InRole) > 0 {
		b.WriteString(" IN ROLE " + roleList(o.InRole))
	}
	if len(o.RoleMembers) > 0 {
		b.WriteString(" ROLE " + roleList(o.RoleMembers))
	}
	if len(o.AdminRoles) > 0 {
		b.WriteString(" ADMIN " + roleList(o.AdminRoles))
	}
	b.WriteString(";")
	return b.String()
}

// roleCreateOp is the DiffOp for a CREATE ROLE statement whose PASSWORD
// option may hold an unresolved secret reference (RFC §11.1). SQL() always
// returns the placeholder/declared form — used for plan output,
// migration-file archival, and error messages, so a resolved password is
// never persisted or logged. ExecSQL rebuilds the statement with PASSWORD's
// resolved value; unlike Subscription's subscriptionCreateOp, this doesn't
// need regex substitution into deparsed text — Role is built from
// structured fields on both the display and exec paths, so ExecSQL just
// calls the same builder with a different password string.
type roleCreateOp struct {
	*op
	role *ir.Role
}

func (o *roleCreateOp) ExecSQL(resolver pipeline.SecretResolver) (string, error) {
	resolved, err := pipeline.ResolveTemplate(*o.role.Password, resolver)
	if err != nil {
		return "", err
	}
	return buildCreateRoleSQL(o.role, &resolved), nil
}

var _ pipeline.SecretBearingOp = (*roleCreateOp)(nil)

func createRole(o *ir.Role) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	displaySQL := buildCreateRoleSQL(o, o.Password)
	if o.Password != nil {
		ops = append(ops, &roleCreateOp{op: safeOp(displaySQL, o.SrcPos), role: o})
	} else {
		ops = append(ops, safeOp(displaySQL, o.SrcPos))
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

func diffObject(desired pipeline.IRObject, snap *snapshot.SnapObject, fullSnap *pipeline.Snapshot, vtypes map[string]string) ([]pipeline.DiffOp, error) {
	switch o := desired.(type) {
	case *ir.Extension:
		if snap.Extension == nil {
			return nil, nil
		}
		return diffExtension(o, snap.Extension), nil
	case *ir.Sequence:
		if snap.Sequence == nil {
			return nil, nil
		}
		return diffSequence(o, snap.Sequence), nil
	case *ir.Role:
		if snap.Role == nil {
			return nil, nil
		}
		return diffRole(o, snap.Role), nil
	case *ir.Schema:
		if snap.Schema == nil {
			return nil, nil
		}
		return diffSchema(o, snap.Schema), nil
	case *ir.Table:
		if snap.Table == nil {
			return nil, nil
		}
		return diffTable(o, snap.Table, fullSnap, vtypes)
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
		return diffType(o, snap.Type, fullSnap, vtypes)
	case *ir.Procedure:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffProcedure(o, snap.Opaque)
	case *ir.Aggregate:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffAggregate(o, snap.Opaque)
	case *ir.Tablespace:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffTablespace(o, snap.Opaque)
	case *ir.ForeignDataWrapper:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffFDW(o, snap.Opaque)
	case *ir.ForeignServer:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffForeignServer(o, snap.Opaque)
	case *ir.UserMapping:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffUserMapping(o, snap.Opaque)
	case *ir.Publication:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffPublication(o, snap.Opaque)
	case *ir.Subscription:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffSubscription(o, snap.Opaque)
	case *ir.EventTrigger:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffEventTrigger(o, snap.Opaque)
	case *ir.Collation:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffCollation(o, snap.Opaque)
	case *ir.Operator:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, o.Reconstructed, o.Comment, snap.Opaque, o.SrcPos)
	case *ir.OperatorClass:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, o.Reconstructed, o.Comment, snap.Opaque, o.SrcPos)
	case *ir.OperatorFamily:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOperatorFamily(o, snap.Opaque)
	case *ir.Cast:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffCast(o, snap.Opaque)
	case *ir.StatisticsObject:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffStatisticsObject(o, snap.Opaque)
	case *ir.TSConfig:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffTSConfig(o, snap.Opaque)
	case *ir.TSDict:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, o.Reconstructed, o.Comment, snap.Opaque, o.SrcPos)
	case *ir.TSParser:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, o.Reconstructed, o.Comment, snap.Opaque, o.SrcPos)
	case *ir.TSTemplate:
		if snap.Opaque == nil {
			return nil, nil
		}
		return diffOpaqueIR(o.QualifiedName(), o.Body, o.Reconstructed, o.Comment, snap.Opaque, o.SrcPos)
	case *ir.DefaultPrivileges:
		if snap.DefaultPrivileges == nil {
			return nil, nil
		}
		return diffDefaultPrivileges(o, snap.DefaultPrivileges), nil
	case *ir.VirtualType:
		// Virtual types are DPG-only annotations; no SQL is generated on change.
		return nil, nil
	}
	return nil, nil
}

// accessMethodOrDefault returns the recorded index access method for an
// operator class/family, falling back to "btree" for snapshots written before
// the access method was tracked (the field is absent in legacy snapshot JSON).
func accessMethodOrDefault(am string) string {
	if am == "" {
		return "btree"
	}
	return am
}

// tsMappingAlterSQL builds "ALTER TEXT SEARCH CONFIGURATION cfg ALTER MAPPING
// FOR tok1, tok2 WITH dict1, dict2;" — confirmed live against postgres:17
// that ALTER MAPPING FOR is an upsert (adds the mapping if none exists yet
// for that token type, replaces it if one does), so it's always the right
// verb regardless of whether the config was COPY'd from an existing one
// (which pre-populates default mappings) or created from scratch — no need
// to distinguish ADD vs ALTER, matching the RFC's own diffing-semantics
// table (§12.1), which only ever uses ALTER MAPPING FOR.
func tsMappingAlterSQL(configIdent string, m pipeline.TSMappingDef) string {
	dicts := make([]string, len(m.Dictionaries))
	for i, d := range m.Dictionaries {
		dicts[i] = d.String()
	}
	return tsMappingAlterSQLStrings(configIdent, m.TokenTypes, dicts)
}

func tsMappingAlterSQLStrings(configIdent string, tokenTypes, dictionaries []string) string {
	return fmt.Sprintf("ALTER TEXT SEARCH CONFIGURATION %s ALTER MAPPING FOR %s WITH %s;",
		configIdent, strings.Join(tokenTypes, ", "), strings.Join(dictionaries, ", "))
}

// tsMappingDropSQL builds "ALTER TEXT SEARCH CONFIGURATION cfg DROP MAPPING
// FOR tok;" for a token type whose mapping was removed from source.
func tsMappingDropSQL(configIdent, tokenType string) string {
	return fmt.Sprintf("ALTER TEXT SEARCH CONFIGURATION %s DROP MAPPING FOR %s;", configIdent, tokenType)
}

// opFamilyAddSQL/opFamilyDropSQL render a single-member "ALTER OPERATOR
// FAMILY ... ADD/DROP ..." statement (RFC §14.4) — one statement per
// member, not batched, so each DiffOp carries its own safety classification
// and can be individually reviewed or skipped, matching how every other
// per-element diff in this file (constraints, columns, grants) is emitted.
func opFamilyAddSQL(famIdent, am string, m pipeline.OpFamilyMember) string {
	return fmt.Sprintf("ALTER OPERATOR FAMILY %s USING %s ADD %s;", famIdent, am, m.AddClause())
}

func opFamilyDropSQL(famIdent, am string, m snapshot.SnapOpFamilyMember) string {
	kind := "OPERATOR"
	if m.IsFunction {
		kind = "FUNCTION"
	}
	return fmt.Sprintf("ALTER OPERATOR FAMILY %s USING %s DROP %s %d (%s, %s);",
		famIdent, am, kind, m.Number, m.LeftType, m.RightType)
}

// opFamilyMemberSnapKey is snapshot.SnapOpFamilyMember's counterpart to
// pipeline.OpFamilyMember.Key() — must stay byte-identical to it (same
// kind/number/op_type components) so a desired-side and snapshot-side
// member at the same catalog slot compare as the same map key.
func opFamilyMemberSnapKey(m snapshot.SnapOpFamilyMember) string {
	kind := "OPERATOR"
	if m.IsFunction {
		kind = "FUNCTION"
	}
	return kind + "|" + strconv.Itoa(m.Number) + "|" + m.LeftType + "|" + m.RightType
}

// qualifyOpFamilyOperandForCompare normalizes a loose member's operand
// identity (RFC §14.4) for comparison purposes only — mirroring
// qualifyFuncForCompare's own reasoning (introspection always returns a
// fully schema-qualified name; hand-written source commonly doesn't) and,
// critically, its symmetric application: called on BOTH the desired and
// snapshot side before comparing, so a pure-offline diff (both sides
// unqualified) still matches, not just the live-catalog path. Function
// members and sort-family targets default like every other DPG object
// reference (the family's own declaring schema, matching graph.go's
// defaultSchema for the identical reference); operator members default to
// pg_catalog instead — an unqualified operator symbol overwhelmingly means
// a built-in, unlike a function or family reference.
func qualifyOpFamilyOperandForCompare(isOperator bool, famSchema, schema, name string) string {
	if schema != "" {
		return schema + "." + name
	}
	if isOperator {
		return "pg_catalog." + name
	}
	return famSchema + "." + name
}

// opFamilyMemberEqual compares the "payload" of a same-slot member pair —
// the parts Key() deliberately excludes (see its doc comment): operator/
// function identity, FOR ORDER BY, and (for FUNCTION) the argument list.
func opFamilyMemberEqual(famSchema string, d pipeline.OpFamilyMember, s snapshot.SnapOpFamilyMember) bool {
	dName := qualifyOpFamilyOperandForCompare(!d.IsFunction, famSchema, d.Name.Schema, d.Name.Name)
	sName := qualifyOpFamilyOperandForCompare(!s.IsFunction, famSchema, s.NameSchema, s.Name)
	if dName != sName {
		return false
	}
	if d.OrderBy != s.OrderBy {
		return false
	}
	if d.OrderBy {
		dSort := qualifyOpFamilyOperandForCompare(false, famSchema, d.SortFamily.Schema, d.SortFamily.Name)
		sSort := qualifyOpFamilyOperandForCompare(false, famSchema, s.SortFamilySchema, s.SortFamilyName)
		if dSort != sSort {
			return false
		}
	}
	return slices.Equal(d.FuncArgs, s.FuncArgs)
}

// diffOpFamilyMembers diffs an operator family's loose members (RFC §14.4)
// at the per-slot level (Key(): kind+number+op_types — see
// pipeline.OpFamilyMember.Key's doc comment: two members at the same slot
// are the same catalog row changing shape, not independent members).
// structured is the same stale-snapshot guard pattern as
// OptionsStructured/StatisticsStructured elsewhere in this file: a snapshot
// written before this feature existed has OpFamilyMembersStructured ==
// false, so this returns nil rather than proposing an ADD for a member
// PostgreSQL may already have — that ADD would genuinely error live (there
// is no "ADD ... IF NOT EXISTS"), so silence is the only safe default until
// the next apply/verify populates the sentinel. Keys are iterated in sorted
// order (unlike diffTSConfigMappings's plain map iteration) so plan output
// is stable across repeated runs.
func diffOpFamilyMembers(famSchema, famIdent, am string, desired []pipeline.OpFamilyMember, snap []snapshot.SnapOpFamilyMember, structured bool, pos pipeline.SourcePos) []pipeline.DiffOp {
	if !structured {
		return nil
	}
	desiredByKey := make(map[string]pipeline.OpFamilyMember, len(desired))
	for _, m := range desired {
		desiredByKey[m.Key()] = m
	}
	snapByKey := make(map[string]snapshot.SnapOpFamilyMember, len(snap))
	for _, m := range snap {
		snapByKey[opFamilyMemberSnapKey(m)] = m
	}

	keys := make(map[string]bool, len(desiredByKey)+len(snapByKey))
	for k := range desiredByKey {
		keys[k] = true
	}
	for k := range snapByKey {
		keys[k] = true
	}
	sortedKeys := make([]string, 0, len(keys))
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	var ops []pipeline.DiffOp
	for _, key := range sortedKeys {
		d, inDesired := desiredByKey[key]
		s, inSnap := snapByKey[key]
		switch {
		case inDesired && !inSnap:
			ops = append(ops, safeOp(opFamilyAddSQL(famIdent, am, d), pos))
		case !inDesired && inSnap:
			ops = append(ops, destructiveOp(opFamilyDropSQL(famIdent, am, s), pos))
		case inDesired && inSnap && !opFamilyMemberEqual(famSchema, d, s):
			ops = append(ops, destructiveOp(opFamilyDropSQL(famIdent, am, s), pos))
			ops = append(ops, safeOp(opFamilyAddSQL(famIdent, am, d), pos))
		}
	}
	return ops
}

// diffTSConfigMappings diffs MAPPING FOR entries at the per-token-type level
// (not per declared entry): a real PG mapping is keyed by individual token
// type, and DPG's "MAPPING FOR word, hword WITH ..." grouping is just a
// source-side convenience for declaring several token types at once — two
// entries covering the same token types with different groupings must NOT
// show as spurious drift, so both sides are flattened to tokenType->
// dictionaries before comparing (mirrors how introspection naturally reads
// pg_ts_config_map, one row per token type).
func diffTSConfigMappings(configIdent string, desired []pipeline.TSMappingDef, snap []snapshot.SnapTSMapping, pos pipeline.SourcePos) []pipeline.DiffOp {
	desiredByToken := make(map[string][]string)
	for _, m := range desired {
		dicts := make([]string, len(m.Dictionaries))
		for i, d := range m.Dictionaries {
			dicts[i] = d.String()
		}
		for _, tt := range m.TokenTypes {
			desiredByToken[tt] = dicts
		}
	}
	snapByToken := make(map[string][]string)
	for _, m := range snap {
		for _, tt := range m.TokenTypes {
			snapByToken[tt] = m.Dictionaries
		}
	}

	var ops []pipeline.DiffOp
	for tt, dicts := range desiredByToken {
		if !slices.Equal(snapByToken[tt], dicts) {
			ops = append(ops, safeOp(tsMappingAlterSQLStrings(configIdent, []string{tt}, dicts), pos))
		}
	}
	for tt := range snapByToken {
		if _, ok := desiredByToken[tt]; !ok {
			ops = append(ops, safeOp(tsMappingDropSQL(configIdent, tt), pos))
		}
	}
	return ops
}

// diffOpaqueIR checks whether an opaque object's body hash has changed and, if
// so, emits a structured DROP (via dropObject, reusing the exact statement
// already used when the object is removed outright) followed by a CREATE from
// the new body. The body comparison is only meaningful when BOTH sides are
// source-derived deparses: it is skipped when the desired object's body is a
// catalog reconstruction (reconstructed == true, e.g. verify introspects the
// desired side) or when the snapshot side carries no hash (a reconstructed
// baseline, e.g. plan --live). Offline plan/apply (source vs source) compare
// normally and detect genuine edits.
//
// comment is diffed independently of Body — a comment-only change never
// needs the DROP+CREATE a body change requires, so it's checked even when
// the body branch above doesn't fire (unchanged body, reconstructed, or no
// baseline hash yet). Uses snap's own Schema/Name/Args/Using (already the
// exact per-object identity dropObject/commentOnOpaqueSQL need) rather than
// requiring a second set of identity params from every call site. Runs on
// the live/reconstructed path too, unlike Body: an exact comment string has
// no canonical-form-vs-hand-written ambiguity the way a reconstructed body
// does, so comparing it is always reliable.
func diffOpaqueIR(name, body string, reconstructed bool, comment *string, snap *snapshot.SnapOpaque, pos pipeline.SourcePos) ([]pipeline.DiffOp, error) {
	if body != "" && !reconstructed {
		sum := sha256.Sum256([]byte(strings.TrimSpace(body)))
		newHash := fmt.Sprintf("%x", sum)
		if snap.BodyHash != "" && newHash != snap.BodyHash {
			ops := dropObject(&snapshot.SnapObject{Kind: snap.Kind, Opaque: snap})
			createOps, err := createOpaque(name, body, snap.Kind, snap.Schema, pos)
			if err != nil {
				return nil, err
			}
			ops = append(ops, createOps...)
			return appendCommentOp(ops, nil, snap.Kind, snap.Schema, snap.Name, snap.Args, snap.Using, comment, pos)
		}
	}
	if !ptrEq(comment, snap.Comment) {
		if sql := commentOnOpaqueSQL(snap.Kind, snap.Schema, snap.Name, snap.Args, snap.Using, comment); sql != "" {
			return []pipeline.DiffOp{safeOp(sql, pos)}, nil
		}
	}
	return nil, nil
}

// diffTSConfig is diffOpaqueIR's TSConfig-specific wrapper: MAPPING FOR
// entries (RFC §12.1) are a TSConfig-only concern diffOpaqueIR knows nothing
// about. When the base diff triggers a full DROP+CREATE (body/PARSER
// changed — diffOpaqueIR calls createOpaque directly, not createObject, so
// none of o.Mappings gets applied automatically), every declared mapping is
// unconditionally re-applied (ALTER MAPPING FOR is confirmed live to be an
// upsert, so this is always correct against a config that was just
// recreated from scratch) instead of being diffed against the old
// snapshot's mappings, which no longer describe anything real. Otherwise
// mappings are diffed at the per-token-type level (diffTSConfigMappings).
func diffTSConfig(o *ir.TSConfig, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	ops, err := diffOpaqueIR(o.QualifiedName(), o.Body, o.Reconstructed, o.Comment, snap, o.SrcPos)
	if err != nil {
		return nil, err
	}
	configIdent := qualIdent(o.Schema, o.Name)
	for _, op := range ops {
		if op.Safety() == pipeline.Destructive {
			for _, m := range o.Mappings {
				ops = append(ops, safeOp(tsMappingAlterSQL(configIdent, m), o.SrcPos))
			}
			return ops, nil
		}
	}
	ops = append(ops, diffTSConfigMappings(configIdent, o.Mappings, snap.Mappings, o.SrcPos)...)
	return ops, nil
}

// diffOperatorFamily is diffOpaqueIR's OperatorFamily-specific wrapper,
// cloning diffTSConfig's own DROP+CREATE-vs-incremental-diff structure
// exactly: loose members (RFC §14.4) are a family-only concern diffOpaqueIR
// knows nothing about. When the base diff triggers a full DROP+CREATE
// (body/AccessMethod changed — diffOpaqueIR calls createOpaque directly,
// not createObject, so none of o.Members gets applied automatically), every
// declared member is unconditionally re-added: the just-recreated family
// has none of the old snapshot's members anymore, so diffing against them
// would be comparing against nothing real. Otherwise members are diffed
// incrementally (diffOpFamilyMembers) — unlike an operator class's
// AS-list, PostgreSQL genuinely supports incremental
// ALTER OPERATOR FAMILY ... ADD/DROP for family-level members, so there is
// no need to force a full drop+recreate here the way OperatorClass does.
func diffOperatorFamily(o *ir.OperatorFamily, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	ops, err := diffOpaqueIR(o.QualifiedName(), o.Body, o.Reconstructed, o.Comment, snap, o.SrcPos)
	if err != nil {
		return nil, err
	}
	famIdent := qualIdent(o.Schema, o.Name)
	for _, op := range ops {
		if op.Safety() == pipeline.Destructive {
			for _, m := range o.Members {
				ops = append(ops, safeOp(opFamilyAddSQL(famIdent, o.AccessMethod, m), o.SrcPos))
			}
			return ops, nil
		}
	}
	ops = append(ops, diffOpFamilyMembers(o.Schema, famIdent, o.AccessMethod, o.Members, snap.OpFamilyMembers, snap.OpFamilyMembersStructured, o.SrcPos)...)
	return ops, nil
}

func diffProcedure(o *ir.Procedure, snap *snapshot.SnapOpaque) ([]pipeline.DiffOp, error) {
	sig := buildProcedureSignature(o)
	pos := o.SrcPos

	// Body changed: re-create via CREATE OR REPLACE (includes comment and grants).
	if o.BodyHash != "" && snap.BodyHash != "" && o.BodyHash != snap.BodyHash {
		return createProcedure(o), nil
	}

	var ops []pipeline.DiffOp
	if !ptrEq(o.Comment, snap.Comment) {
		if o.Comment != nil {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON PROCEDURE %s IS %s;", sig, quoteLit(*o.Comment)), pos))
		} else {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON PROCEDURE %s IS NULL;", sig), pos))
		}
	}
	ops = append(ops, diffGrantSet(snap.Grants, o.Grants, "PROCEDURE "+sig, pos)...)
	ops = append(ops, diffRevocationSet(snap.Revocations, o.Revocations, "PROCEDURE "+sig, pos)...)
	return ops, nil
}

func diffExtension(o *ir.Extension, snap *snapshot.SnapExtension) []pipeline.DiffOp {
	pos := o.SrcPos
	if !ptrEq(o.Version, snap.Version) && o.Version != nil {
		return []pipeline.DiffOp{safeOp(
			fmt.Sprintf("ALTER EXTENSION %s UPDATE TO %s;", quoteIdent(o.Name), quoteLit(*o.Version)),
			pos,
		)}
	}
	return nil
}

func diffSequence(o *ir.Sequence, snap *snapshot.SnapSequence) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	pos := o.SrcPos
	ident := qualIdent(o.Schema, o.Name)

	// Check if any explicitly-specified sequence params differ from the snapshot.
	// Only compare params that the user set (non-nil in desired IR).
	paramsChanged := (o.IncrementBy != nil && !int64PtrEq(o.IncrementBy, snap.IncrementBy)) ||
		(o.MinValue != nil && !int64PtrEq(o.MinValue, snap.MinValue)) ||
		(o.MaxValue != nil && !int64PtrEq(o.MaxValue, snap.MaxValue)) ||
		(o.StartValue != nil && !int64PtrEq(o.StartValue, snap.StartValue)) ||
		(o.Cache != nil && !int64PtrEq(o.Cache, snap.Cache)) ||
		(o.Cycle != nil && *o.Cycle != snap.Cycle)
	if paramsChanged {
		var b strings.Builder
		b.WriteString("ALTER SEQUENCE ")
		b.WriteString(ident)
		writeSeqParams(&b, o)
		b.WriteString(";")
		ops = append(ops, safeOp(b.String(), pos))
	}

	if !ptrEq(o.Owner, snap.Owner) && o.Owner != nil {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER SEQUENCE %s OWNER TO %s;", ident, quoteIdent(*o.Owner)), pos))
	}

	if !ptrEq(o.Comment, snap.Comment) {
		if o.Comment != nil {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON SEQUENCE %s IS %s;", ident, quoteLit(*o.Comment)), pos))
		} else {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON SEQUENCE %s IS NULL;", ident), pos))
		}
	}
	return ops
}

func int64PtrEq(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// desiredFloatChanged reports whether an explicitly-declared desired value
// (e.g. Function.Attrs.Cost/Rows) differs from the snapshot's. A nil
// desired means the source doesn't mention the attribute at all — that
// must never itself trigger drift, since the snapshot side always carries
// a concrete value (PostgreSQL's own catalog default, once introspected)
// even when nothing was ever declared. Only a genuinely explicit,
// different desired value counts as a change.
func desiredFloatChanged(desired, snap *float64) bool {
	if desired == nil {
		return false
	}
	return snap == nil || *desired != *snap
}

// parallelChanged compares a function's PARALLEL setting, treating an empty
// snapshot value as "unknown" (a pre-upgrade snapshot.json that predates this
// field) rather than a real "UNSAFE": desired is always concrete (the IR
// builder defaults unspecified source to "UNSAFE"), but snap can be "" purely
// from JSON's zero-value-on-missing-key behavior, which must never itself be
// read as a genuine difference.
func parallelChanged(desired, snap string) bool {
	if snap == "" {
		return false
	}
	return desired != snap
}

func boolPtrEq(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func intPtrEq(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// roleAlterPasswordOp is the DiffOp for a standalone ALTER ROLE ... PASSWORD
// statement (an existing role's password changed, RFC §11.1) — same
// SecretBearingOp contract as roleCreateOp/subscriptionCreateOp: SQL() is
// always the placeholder/declared form, ExecSQL resolves it fresh for the
// one execution call only.
type roleAlterPasswordOp struct {
	*op
	name     string
	password string
}

func (o *roleAlterPasswordOp) ExecSQL(resolver pipeline.SecretResolver) (string, error) {
	resolved, err := pipeline.ResolveTemplate(o.password, resolver)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("ALTER ROLE %s PASSWORD %s;", quoteIdent(o.name), quoteLit(resolved)), nil
}

var _ pipeline.SecretBearingOp = (*roleAlterPasswordOp)(nil)

// stringSetDiff returns the elements present in desired but not current
// (added) and present in current but not desired (removed).
func stringSetDiff(desired, current []string) (added, removed []string) {
	curSet := make(map[string]bool, len(current))
	for _, s := range current {
		curSet[s] = true
	}
	desSet := make(map[string]bool, len(desired))
	for _, s := range desired {
		desSet[s] = true
		if !curSet[s] {
			added = append(added, s)
		}
	}
	for _, s := range current {
		if !desSet[s] {
			removed = append(removed, s)
		}
	}
	return added, removed
}

// diffRoleMembership diffs IN ROLE/ROLE/ADMIN (RFC §11.1) as GRANT/REVOKE —
// PostgreSQL's ALTER ROLE has no membership clause at all (confirmed via
// pg_query: addroleto/rolemembers/adminmembers are CreateRoleStmt-only
// options), so an existing role's membership can only ever change via
// GRANT/REVOKE, the same mechanism PostgreSQL itself uses post-creation.
// Each of the three fields is only compared when declared (non-nil) —
// undeclared means "not managed by DPG for this role", same convention as
// every other optional Role field.
func diffRoleMembership(o *ir.Role, snap *snapshot.SnapRole, pos pipeline.SourcePos) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	ident := quoteIdent(o.Name)

	if o.InRole != nil {
		added, removed := stringSetDiff(o.InRole, snap.InRole)
		for _, r := range added {
			ops = append(ops, safeOp(fmt.Sprintf("GRANT %s TO %s;", quoteIdent(r), ident), pos))
		}
		for _, r := range removed {
			ops = append(ops, cautionOp(fmt.Sprintf("REVOKE %s FROM %s;", quoteIdent(r), ident), pos))
		}
	}
	if o.RoleMembers != nil {
		added, removed := stringSetDiff(o.RoleMembers, snap.RoleMembers)
		for _, m := range added {
			ops = append(ops, safeOp(fmt.Sprintf("GRANT %s TO %s;", ident, quoteIdent(m)), pos))
		}
		for _, m := range removed {
			ops = append(ops, cautionOp(fmt.Sprintf("REVOKE %s FROM %s;", ident, quoteIdent(m)), pos))
		}
	}
	if o.AdminRoles != nil {
		added, removed := stringSetDiff(o.AdminRoles, snap.AdminRoles)
		for _, m := range added {
			ops = append(ops, safeOp(fmt.Sprintf("GRANT %s TO %s WITH ADMIN OPTION;", ident, quoteIdent(m)), pos))
		}
		for _, m := range removed {
			ops = append(ops, cautionOp(fmt.Sprintf("REVOKE %s FROM %s;", ident, quoteIdent(m)), pos))
		}
	}
	return ops
}

func diffRole(o *ir.Role, snap *snapshot.SnapRole) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	pos := o.SrcPos
	ident := quoteIdent(o.Name)

	// Every non-PASSWORD, non-membership attribute the declaration manages,
	// batched into one ALTER ROLE (mirrors diffSequence's identical
	// batching of its own optional params into one ALTER SEQUENCE).
	attrsChanged := (o.CanLogin != nil && !boolPtrEq(o.CanLogin, snap.CanLogin)) ||
		(o.Superuser != nil && !boolPtrEq(o.Superuser, snap.Superuser)) ||
		(o.CreateDB != nil && !boolPtrEq(o.CreateDB, snap.CreateDB)) ||
		(o.CreateRole != nil && !boolPtrEq(o.CreateRole, snap.CreateRole)) ||
		(o.Inherit != nil && !boolPtrEq(o.Inherit, snap.Inherit)) ||
		(o.IsReplication != nil && !boolPtrEq(o.IsReplication, snap.IsReplication)) ||
		(o.BypassRLS != nil && !boolPtrEq(o.BypassRLS, snap.BypassRLS)) ||
		(o.ConnectionLimit != nil && !intPtrEq(o.ConnectionLimit, snap.ConnectionLimit)) ||
		(o.ValidUntil != nil && !ptrEq(o.ValidUntil, snap.ValidUntil))
	if attrsChanged {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER ROLE %s%s;", ident, roleAttrClause(o, nil)), pos))
	}

	// PASSWORD is never compared as raw text — only its hash (of the
	// declared text, never the resolved value) is ever stored, so this is
	// the only way to detect a change offline (RFC §11.1's "Password drift
	// detection").
	if o.Password != nil && hashText(*o.Password) != snap.PasswordHash {
		ops = append(ops, &roleAlterPasswordOp{
			op:       cautionOp(fmt.Sprintf("ALTER ROLE %s PASSWORD %s;", ident, quoteLit(*o.Password)), pos),
			name:     o.Name,
			password: *o.Password,
		})
	}

	if !ptrEq(o.Comment, snap.Comment) {
		if o.Comment != nil {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON ROLE %s IS %s;", ident, quoteLit(*o.Comment)), pos))
		} else {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON ROLE %s IS NULL;", ident), pos))
		}
	}

	ops = append(ops, diffRoleMembership(o, snap, pos)...)
	return ops
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
	schemaIdent := quoteIdent(o.Name)
	ops = append(ops, diffGrantSet(snap.Grants, o.Grants, "SCHEMA "+schemaIdent, pos)...)
	ops = append(ops, diffRevocationSet(snap.Revocations, o.Revocations, "SCHEMA "+schemaIdent, pos)...)
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

	if desiredTxt, snapTxt := effectiveComment(o.Comment, o.Deprecated), effectiveComment(snap.Comment, snap.Deprecated); !ptrEq(desiredTxt, snapTxt) {
		if desiredTxt != nil {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON %s %s IS %s;", viewKind, tbl, quoteLit(*desiredTxt)), pos))
		} else {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON %s %s IS NULL;", viewKind, tbl), pos))
		}
	}

	if !ptrEq(o.Owner, snap.Owner) && o.Owner != nil {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER %s %s OWNER TO %s;", viewKind, tbl, quoteIdent(*o.Owner)), pos))
	}

	ops = append(ops, diffGrantSet(snap.Grants, o.Grants, "TABLE "+tbl, pos)...)
	ops = append(ops, diffRevocationSet(snap.Revocations, o.Revocations, "TABLE "+tbl, pos)...)
	ops = append(ops, diffViewIndexes(o.Schema, o.Name, o.Indexes, snap.Indexes)...)
	return ops
}

func diffFunction(o *ir.Function, snap *snapshot.SnapFunction) []pipeline.DiffOp {
	var ops []pipeline.DiffOp
	pos := o.SrcPos
	sig := buildFuncSignature(o)

	// A return-type change (including toggling SETOF, or a RETURNS TABLE(...)
	// column-list edit) needs DROP + CREATE, not CREATE OR REPLACE: confirmed
	// live against postgres:17 that PG rejects an in-place return-type change
	// outright ("cannot change return type of existing function", hinting to
	// DROP FUNCTION first) — the same DROP-required class of change diffView
	// already handles for a materialized view's query. This also closes a
	// genuinely separate, pre-existing gap found while wiring this up:
	// ReturnType was never compared here at all before, so ANY return-type-
	// only change (SETOF or otherwise) silently went undetected.
	//
	// ReturnTable is compared separately from ReturnType/ReturnsSet: a RETURNS
	// TABLE(...) function's ReturnType is always "record"/SetOf=true no matter
	// what its column list is (that's how PostgreSQL's own catalog represents
	// it — prorettype is genuinely 'record' regardless), so two functions with
	// different TABLE column lists would look identical to that check alone.
	returnTable := ir.FormatTableColumns(ir.FuncTableColumns(o.Args))
	if o.ReturnType.String() != snap.ReturnType || o.ReturnType.SetOf != snap.ReturnsSet || returnTable != snap.ReturnTable {
		ops = append(ops, destructiveOp(fmt.Sprintf("DROP FUNCTION IF EXISTS %s;", sig), pos))
		ops = append(ops, createFunction(o)...)
		return ops
	}

	// Re-create if: desired has a hash and it differs from the snapshot (including
	// "" when the snapshot predates body-hash tracking), or language/volatility/
	// parallel changed, or an EXPLICITLY declared Cost/Rows differs. Cost/Rows
	// use desiredFloatChanged rather than a plain !=: an unspecified COST/ROWS
	// in source (o.Attrs.Cost == nil) must never trigger a recreate on its own,
	// the same "only act when the desired side actually declares it" rule
	// already established for column STORAGE — otherwise every function that
	// has never mentioned COST/ROWS would show permanent drift the moment
	// introspection (or a prior apply) records PostgreSQL's own concrete
	// default value for it.
	//
	// Parallel needs the same "don't trust an absent value" treatment, but from
	// the opposite side: unlike Cost/Rows (a *float64, nil on the DESIRED side
	// means "not declared"), Parallel is a plain string that the IR builder
	// ALWAYS sets to a concrete default ("UNSAFE") even when source never
	// mentions PARALLEL — so o.Attrs.Parallel is never "". snap.Parallel CAN be
	// "" though: any snapshot.json written before this field existed has no
	// "parallel" key at all, so JSON unmarshalling leaves it at the Go zero
	// value. A bare != would then compare "UNSAFE" against "" for every single
	// function in every pre-existing project and spuriously recreate all of
	// them on the first plan/apply after upgrading. parallelChanged treats an
	// empty snapshot value as "unknown, don't diff yet" — self-healing after
	// that first apply records a real value.
	if (o.BodyHash != "" && o.BodyHash != snap.BodyHash) ||
		o.Attrs.Language != snap.Language || o.Attrs.Volatility != snap.Volatility ||
		parallelChanged(o.Attrs.Parallel, snap.Parallel) ||
		desiredFloatChanged(o.Attrs.Cost, snap.Cost) || desiredFloatChanged(o.Attrs.Rows, snap.Rows) {
		ops = append(ops, safeOp(buildFunctionSQL(o), pos))
	}
	if desiredTxt, snapTxt := effectiveComment(o.Comment, o.Deprecated), effectiveComment(snap.Comment, snap.Deprecated); !ptrEq(desiredTxt, snapTxt) {
		if desiredTxt != nil {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON FUNCTION %s IS %s;", sig, quoteLit(*desiredTxt)), pos))
		} else {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON FUNCTION %s IS NULL;", sig), pos))
		}
	}
	ops = append(ops, diffGrantSet(snap.Grants, o.Grants, "FUNCTION "+sig, pos)...)
	ops = append(ops, diffRevocationSet(snap.Revocations, o.Revocations, "FUNCTION "+sig, pos)...)
	return ops
}

func diffType(o *ir.Type, snap *snapshot.SnapType, fullSnap *pipeline.Snapshot, vtypes map[string]string) ([]pipeline.DiffOp, error) {
	var ops []pipeline.DiffOp
	pos := o.SrcPos
	typeIdent := qualIdent(o.Schema, o.Name)

	if o.Variant == "COMPOSITE" && snap.Variant == "COMPOSITE" {
		if compositeAttrsChanged(o.CompositeAttrs, snap.CompositeAttrs) {
			// PG has no in-place ALTER TYPE … ALTER ATTRIBUTE for type changes;
			// DROP + recreate is required.
			ops = append(ops,
				destructiveOp(fmt.Sprintf("DROP TYPE IF EXISTS %s;", typeIdent), pos),
			)
			ops = append(ops, safeOp(buildCompositeTypeSQL(o, vtypes), pos))
		}
		if !ptrEq(o.Comment, snap.Comment) {
			if o.Comment != nil {
				ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON TYPE %s IS %s;", typeIdent, quoteLit(*o.Comment)), pos))
			} else {
				ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON TYPE %s IS NULL;", typeIdent), pos))
			}
		}
		return ops, nil
	}

	if o.Variant == "DOMAIN" && snap.Variant == "DOMAIN" {
		// RFC §5.4: unlike RANGE/BASE (hash-diffed opaque bodies, DROP+CREATE
		// on any change), a domain's properties are each diffed and altered
		// individually — DEFAULT/NOT NULL/constraint changes never require
		// recreating the domain, only a base-type change does. Found live-
		// testing a demo project: diffType had no case for DOMAIN at all, so
		// none of this was ever diffed — only the generic COMMENT check
		// (shared by every variant, below) ever fired for an already-applied
		// domain.
		// Same "reconstructed/stale snapshot, no comparison possible yet"
		// guard as RANGE/BASE below: a snapshot written before this
		// structured-field tracking existed has snap.DomainBaseType == ""
		// (Go zero value, never populated) even though the domain is real
		// and unchanged. Found live-testing a demo project's pre-existing
		// email_address domain: without this guard, every already-applied
		// domain looked like a base-type change on the very first plan
		// after upgrading, proposing a destructive DROP DOMAIN CASCADE
		// that would have cascade-dropped any column using it. A domain
		// always has a non-empty base type, so an empty snap value can
		// only mean "not yet populated" — skip structural diffing entirely
		// and fall through to the COMMENT check, which still works since
		// Comment was already tracked before this change.
		if snap.DomainBaseType == "" {
			if !ptrEq(o.Comment, snap.Comment) {
				if o.Comment != nil {
					ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON DOMAIN %s IS %s;", typeIdent, quoteLit(*o.Comment)), pos))
				} else {
					ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON DOMAIN %s IS NULL;", typeIdent), pos))
				}
			}
			// Same PROTECTED-field precedent as diffTable: if nothing else
			// changed, ops would be empty and apply's len(ops)==0
			// short-circuit skips Populate, so snap.DomainBaseType would
			// stay "" forever and this branch would never self-heal. A
			// harmless comment op forces the snapshot refresh on the very
			// next apply.
			if len(ops) == 0 {
				ops = append(ops, safeOp(fmt.Sprintf("-- refresh snapshot metadata for domain %s", typeIdent), pos))
			}
			return ops, nil
		}
		if o.DomainBaseType.String() != snap.DomainBaseType {
			// PG has no ALTER DOMAIN ... TYPE; the base type is fixed at
			// creation. RFC §5.4 explicitly requires DROP DOMAIN CASCADE.
			ops = append(ops, destructiveOp(fmt.Sprintf("DROP DOMAIN IF EXISTS %s CASCADE;", typeIdent), pos))
			ops = append(ops, createType(o, vtypes)...)
			return ops, nil
		}
		if !ptrEq(o.DomainDefault, snap.DomainDefault) {
			if o.DomainDefault != nil {
				ops = append(ops, safeOp(fmt.Sprintf("ALTER DOMAIN %s SET DEFAULT %s;", typeIdent, *o.DomainDefault), pos))
			} else {
				ops = append(ops, safeOp(fmt.Sprintf("ALTER DOMAIN %s DROP DEFAULT;", typeIdent), pos))
			}
		}
		if o.DomainNotNull != snap.DomainNotNull {
			if o.DomainNotNull {
				// Existing rows may already violate it — PG validates on SET,
				// unlike DROP which can never fail.
				ops = append(ops, cautionOp(fmt.Sprintf("ALTER DOMAIN %s SET NOT NULL;", typeIdent), pos))
			} else {
				ops = append(ops, safeOp(fmt.Sprintf("ALTER DOMAIN %s DROP NOT NULL;", typeIdent), pos))
			}
		}
		// Constraints are matched by name; an unnamed CHECK (Name == "") is
		// left undiffed — same "genuinely hard, no reliable identity to
		// match on" class as unnamed table constraints before that was
		// separately solved with PG's full auto-naming algorithm, out of
		// scope here since the RFC's own worked example always names them.
		snapByName := make(map[string]snapshot.SnapConstraint, len(snap.DomainConstraints))
		for _, c := range snap.DomainConstraints {
			if c.Name != "" {
				snapByName[c.Name] = c
			}
		}
		desiredByName := make(map[string]*ir.Constraint, len(o.DomainConstraints))
		for _, c := range o.DomainConstraints {
			if c.Name != "" {
				desiredByName[c.Name] = c
			}
		}
		for name := range snapByName {
			if _, ok := desiredByName[name]; !ok {
				ops = append(ops, safeOp(fmt.Sprintf("ALTER DOMAIN %s DROP CONSTRAINT %s;", typeIdent, quoteIdent(name)), pos))
			}
		}
		for name, c := range desiredByName {
			if sc, existed := snapByName[name]; !existed || sc.Expr != c.Expr {
				if existed {
					// Same name, different expression: PG has no ALTER
					// DOMAIN ... ALTER CONSTRAINT for the check expression
					// itself, so replace it via drop + add.
					ops = append(ops, safeOp(fmt.Sprintf("ALTER DOMAIN %s DROP CONSTRAINT %s;", typeIdent, quoteIdent(name)), pos))
				}
				ops = append(ops, cautionOp(fmt.Sprintf("ALTER DOMAIN %s ADD CONSTRAINT %s %s;", typeIdent, quoteIdent(name), c.Expr), pos))
			}
		}
		if !ptrEq(o.Comment, snap.Comment) {
			if o.Comment != nil {
				ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON DOMAIN %s IS %s;", typeIdent, quoteLit(*o.Comment)), pos))
			} else {
				ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON DOMAIN %s IS NULL;", typeIdent), pos))
			}
		}
		return ops, nil
	}

	if (o.Variant == "RANGE" || o.Variant == "BASE") && snap.Variant == o.Variant {
		// RFC §5.3/§5.5: any change to a RANGE type's options, or to a BASE
		// type at all, requires DROP + CREATE (RANGE explicitly says CASCADE;
		// BASE's RFC text doesn't, so none is added). Found live-testing a
		// demo project: this whole branch was simply missing — diffType had
		// no case for RANGE or BASE at all, so an already-applied one whose
		// Body changed was a silent no-op forever, only the COMMENT (below)
		// was ever diffed. Same body-hash-with-reconstructed-guard pattern as
		// diffOpaqueIR (Publication/Collation/...): a reconstructed
		// (introspected) body isn't byte-identical to hand-written source, so
		// hashing it would report spurious drift — snap.BodyHash is "" for a
		// reconstructed snapshot entry, and desiredHash is "" here for the
		// same reason, both treated as "no comparison possible yet."
		desiredHash := ""
		if o.Body != "" && !o.Reconstructed {
			sum := sha256.Sum256([]byte(strings.TrimSpace(o.Body)))
			desiredHash = fmt.Sprintf("%x", sum)
		}
		if desiredHash != "" && snap.BodyHash != "" && desiredHash != snap.BodyHash {
			cascade := ""
			if o.Variant == "RANGE" {
				cascade = " CASCADE"
			}
			ops = append(ops, destructiveOp(fmt.Sprintf("DROP TYPE IF EXISTS %s%s;", typeIdent, cascade), pos))
			ops = append(ops, createType(o, vtypes)...)
			return ops, nil
		}
		if !ptrEq(o.Comment, snap.Comment) {
			if o.Comment != nil {
				ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON TYPE %s IS %s;", typeIdent, quoteLit(*o.Comment)), pos))
			} else {
				ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON TYPE %s IS NULL;", typeIdent), pos))
			}
		}
		return ops, nil
	}

	if o.Variant == "ENUM" && snap.Variant == "ENUM" {
		snapVals := make(map[string]bool, len(snap.Values))
		for _, v := range snap.Values {
			snapVals[v] = true
		}
		desiredVals := make(map[string]bool, len(o.EnumValues))
		for _, v := range o.EnumValues {
			desiredVals[v] = true
		}

		// Values removed from the enum require the MIGRATE REMOVE procedure.
		var removedCount int
		for v := range snapVals {
			if !desiredVals[v] {
				removedCount++
			}
		}
		if removedCount > 0 {
			return diffEnumRemove(o, snap, fullSnap)
		}

		// ALTER TYPE ADD VALUE cannot run inside a transaction in PG < 16.
		for _, v := range o.EnumValues {
			if !snapVals[v] {
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
	return ops, nil
}

// diffEnumRemove implements the 7-step MIGRATE REMOVE procedure for enums.
// It creates a shadow type, runs the user-supplied DML, verifies no rows
// carry removed values, alters affected columns, drops the old type, and
// renames the shadow type back to the original name.
func diffEnumRemove(o *ir.Type, snap *snapshot.SnapType, fullSnap *pipeline.Snapshot) ([]pipeline.DiffOp, error) {
	pos := o.SrcPos
	typeIdent := qualIdent(o.Schema, o.Name)
	shadowIdent := qualIdent(o.Schema, o.Name+"__dpg_new")

	if o.MigrateRemove == nil {
		return nil, pipeline.Errorf(pos,
			"enum %s has removed values but no MIGRATE REMOVE block; add a MIGRATE REMOVE block with DML to migrate existing rows before removing",
			typeIdent)
	}

	desiredSet := make(map[string]bool, len(o.EnumValues))
	for _, v := range o.EnumValues {
		desiredSet[v] = true
	}
	var removed []string
	for _, v := range snap.Values {
		if !desiredSet[v] {
			removed = append(removed, v)
		}
	}

	// Find all table columns in the snapshot that reference this enum type.
	type affectedCol struct{ tableSchema, tableName, colName string }
	var affected []affectedCol
	for _, raw := range fullSnap.Objects {
		var so snapshot.SnapObject
		if err := json.Unmarshal(raw, &so); err != nil || so.Table == nil {
			continue
		}
		for _, col := range so.Table.Columns {
			if enumTypeMatch(col.Type, o.Schema, o.Name) {
				affected = append(affected, affectedCol{so.Table.Schema, so.Table.Name, col.Name})
			}
		}
	}

	var ops []pipeline.DiffOp

	// Step 1: create shadow type with the new (reduced) value set.
	vals := make([]string, len(o.EnumValues))
	for i, v := range o.EnumValues {
		vals[i] = quoteLit(v)
	}
	ops = append(ops, safeOp(
		fmt.Sprintf("CREATE TYPE %s AS ENUM (%s);", shadowIdent, strings.Join(vals, ", ")),
		pos,
	))

	// Step 2: execute migration DML (user-supplied).
	if dml := strings.TrimSpace(o.MigrateRemove.SQL.Text); dml != "" {
		ops = append(ops, destructiveOp(dml, pos))
	}

	// Step 3: verify no rows still carry removed values.
	removedLits := make([]string, len(removed))
	for i, v := range removed {
		removedLits[i] = quoteLit(v)
	}
	for _, ac := range affected {
		tblIdent := qualIdent(ac.tableSchema, ac.tableName)
		colIdent := quoteIdent(ac.colName)
		ops = append(ops, safeOp(fmt.Sprintf(
			"DO $$ DECLARE _cnt bigint; BEGIN\n"+
				"  SELECT count(*) INTO _cnt FROM %s WHERE %s::text = ANY(ARRAY[%s]);\n"+
				"  IF _cnt > 0 THEN RAISE EXCEPTION 'MIGRATE REMOVE: %% row(s) in %s.%s still carry a removed %s value', _cnt; END IF;\n"+
				"END; $$;",
			tblIdent, colIdent, strings.Join(removedLits, ", "),
			tblIdent, colIdent, typeIdent,
		), pos))
	}

	// Step 4: alter affected columns to use the shadow type.
	for _, ac := range affected {
		tblIdent := qualIdent(ac.tableSchema, ac.tableName)
		colIdent := quoteIdent(ac.colName)
		ops = append(ops, destructiveOp(fmt.Sprintf(
			"ALTER TABLE %s ALTER COLUMN %s TYPE %s USING %s::text::%s;",
			tblIdent, colIdent, shadowIdent, colIdent, shadowIdent,
		), pos))
	}

	// Step 5: drop the old type.
	ops = append(ops, destructiveOp(fmt.Sprintf("DROP TYPE %s;", typeIdent), pos))

	// Step 6: rename shadow to the original name.
	ops = append(ops, safeOp(fmt.Sprintf("ALTER TYPE %s RENAME TO %s;", shadowIdent, quoteIdent(o.Name)), pos))

	// Re-apply comment after the rename.
	if o.Comment != nil {
		ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON TYPE %s IS %s;", typeIdent, quoteLit(*o.Comment)), pos))
	}

	// Step 7: cleanup instruction for manual rollback.
	ops = append(ops, manualOp(fmt.Sprintf("-- On failure run: DROP TYPE IF EXISTS %s;", shadowIdent), pos))

	return ops, nil
}

// enumTypeMatch reports whether colType refers to the enum identified by
// (schema, name). It handles both qualified and unqualified forms, and
// single-dimension arrays.
func enumTypeMatch(colType, schema, name string) bool {
	qn := schema + "." + name
	return colType == qn || colType == name ||
		colType == qn+"[]" || colType == name+"[]"
}

func diffTable(o *ir.Table, snap *snapshot.SnapTable, fullSnap *pipeline.Snapshot, vtypes map[string]string) ([]pipeline.DiffOp, error) {
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

	// A foreign table's SERVER cannot be changed via ALTER FOREIGN TABLE —
	// real PostgreSQL has no such clause — so it requires DROP + CREATE,
	// same as diffView's handling of a materialized view's query change.
	if o.Foreign && !ptrEq(o.ForeignServer, snap.ForeignServer) {
		ops = append(ops, destructiveOp(fmt.Sprintf("DROP FOREIGN TABLE IF EXISTS %s;", tbl), pos))
		ops = append(ops, createTable(o, vtypes)...)
		return ops, nil
	}
	if o.Foreign {
		ops = append(ops, diffForeignOptions(tbl, o.ForeignOptions, snap.ForeignOptions, pos)...)
	}

	if !ptrEq(o.Owner, snap.Owner) && o.Owner != nil {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER TABLE %s OWNER TO %s;", tbl, quoteIdent(*o.Owner)), pos))
	}
	if !ptrEq(o.Tablespace, snap.Tablespace) && o.Tablespace != nil {
		ops = append(ops, safeOp(fmt.Sprintf("ALTER TABLE %s SET TABLESPACE %s;", tbl, quoteIdent(*o.Tablespace)), pos))
	}
	// PROTECTED has no PG DDL equivalent — it's pure DPG-side bookkeeping
	// (see the Pass-2 deletion check's DPG-E022 guard) — but it must still
	// appear as an op, or a PROTECTED-only removal produces zero DiffOps and
	// apply's len(ops)==0 short-circuit means the snapshot's stale
	// Protected=true is never cleared, permanently blocking the very
	// "remove PROTECTED first" workflow RFC §7.11 documents.
	if o.Protected != snap.Protected {
		ops = append(ops, safeOp(fmt.Sprintf("-- PROTECTED %s on %s", map[bool]string{true: "enabled", false: "removed"}[o.Protected], tbl), pos))
	}
	if desiredTxt, snapTxt := effectiveComment(o.Comment, o.Deprecated), effectiveComment(snap.Comment, snap.Deprecated); !ptrEq(desiredTxt, snapTxt) {
		if desiredTxt != nil {
			ops = append(ops, safeOp(fmt.Sprintf("COMMENT ON TABLE %s IS %s;", tbl, quoteLit(*desiredTxt)), pos))
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

	colOps, renamedCols, droppedCols, err := diffColumns(tbl, o, snap, vtypes)
	if err != nil {
		return nil, err
	}
	ops = append(ops, colOps...)
	ops = append(ops, diffConstraints(tbl, o, snap, fullSnap, pos, renamedCols, droppedCols)...)
	ops = append(ops, diffIndexes(o.Schema, o.Name, o, snap, renamedCols, droppedCols)...)
	ops = append(ops, diffPolicies(o.Schema, o.Name, o, snap)...)
	ops = append(ops, diffTriggers(o.Schema, o.Name, o, snap)...)
	ops = append(ops, diffTableInherits(tbl, o, snap, pos)...)
	ops = append(ops, diffGrantSet(snap.Grants, o.Grants, "TABLE "+tbl, pos)...)
	ops = append(ops, diffRevocationSet(snap.Revocations, o.Revocations, "TABLE "+tbl, pos)...)
	ops = append(ops, diffPartitions(tbl, o, snap, pos)...)
	return ops, nil
}

// diffForeignOptions computes ALTER FOREIGN TABLE ... OPTIONS (ADD/SET/DROP
// ...) for a changed FOREIGN TABLE OPTIONS clause. Unlike a SERVER change,
// real PostgreSQL supports altering OPTIONS in place — no drop+recreate
// needed. snapFlat is the snapshot's flattened "key=value, key=value" form
// (see flattenParams); desired is the live IR's ordered param list.
func diffForeignOptions(tbl string, desired []pipeline.StorageParam, snapFlat string, pos pipeline.SourcePos) []pipeline.DiffOp {
	snapMap := make(map[string]string)
	for _, kv := range strings.Split(snapFlat, ", ") {
		if kv == "" {
			continue
		}
		if k, v, ok := strings.Cut(kv, "="); ok {
			snapMap[k] = v
		}
	}

	desiredKeys := make(map[string]bool, len(desired))
	var actions []string
	for _, p := range desired {
		desiredKeys[p.Key] = true
		old, existed := snapMap[p.Key]
		switch {
		case !existed:
			actions = append(actions, fmt.Sprintf("ADD %s %s", p.Key, quoteLit(p.Value)))
		case old != p.Value:
			actions = append(actions, fmt.Sprintf("SET %s %s", p.Key, quoteLit(p.Value)))
		}
	}
	// Iterate snapFlat's own order (not map order) for deterministic DROP output.
	for _, kv := range strings.Split(snapFlat, ", ") {
		if kv == "" {
			continue
		}
		k, _, _ := strings.Cut(kv, "=")
		if !desiredKeys[k] {
			actions = append(actions, fmt.Sprintf("DROP %s", k))
		}
	}
	if len(actions) == 0 {
		return nil
	}
	return []pipeline.DiffOp{safeOp(
		fmt.Sprintf("ALTER FOREIGN TABLE %s OPTIONS (%s);", tbl, strings.Join(actions, ", ")),
		pos,
	)}
}

// createPartitionOps emits "CREATE TABLE child PARTITION OF parent
// FOR VALUES ...;" for p, recursing into any sub-partitions (RFC §7.13). A
// sub-partitioned child gets its own trailing "PARTITION BY strategy (cols)"
// clause — real PostgreSQL allows this directly on a PARTITION OF statement —
// then each of its sub-partitions is created as PARTITION OF the child.
//
// p.Bounds already carries the full clause ("FOR VALUES FROM (...) TO
// (...)"/"FOR VALUES IN (...)"/"FOR VALUES WITH (...)"/"DEFAULT") — both the
// parser (raw text after the partition name) and introspection (pg_get_expr
// on relpartbound) capture it that way, so prepending "FOR VALUES" here
// would double it up (and be outright wrong for a DEFAULT partition, which
// has no FOR VALUES at all).
func createPartitionOps(schema string, parent string, p *ir.Partition) []pipeline.DiffOp {
	childTbl := qualIdent(schema, p.Name)
	stmt := fmt.Sprintf("CREATE TABLE %s PARTITION OF %s %s", childTbl, parent, p.Bounds)
	if p.PartitionBy != nil {
		stmt += fmt.Sprintf(" PARTITION BY %s (%s)", p.PartitionBy.Strategy, strings.Join(p.PartitionBy.Columns, ", "))
	}
	stmt += ";"

	ops := []pipeline.DiffOp{safeOp(stmt, p.SrcPos)}
	for _, sub := range p.Partitions {
		ops = append(ops, createPartitionOps(schema, childTbl, sub)...)
	}
	return ops
}

func diffPartitions(tbl string, o *ir.Table, snap *snapshot.SnapTable, pos pipeline.SourcePos) []pipeline.DiffOp {
	var ops []pipeline.DiffOp

	// Partition strategy change cannot be done in-place.
	desiredPB := ""
	if o.PartitionBy != nil {
		desiredPB = o.PartitionBy.Strategy + " (" + strings.Join(o.PartitionBy.Columns, ", ") + ")"
	}
	if snap.PartitionBy != desiredPB {
		ops = append(ops, manualOp(
			fmt.Sprintf("-- PARTITION BY changed on %s; table must be recreated to alter the partition strategy", tbl),
			pos,
		))
		return ops
	}

	ops = append(ops, diffPartitionList(o.Schema, tbl, o.Partitions, snap.Partitions, pos)...)
	return ops
}

// diffPartitionList diffs one level of partition entries (desired vs.
// snapshot), recursing into each matched pair's sub-partitions
// (RFC §7.13). parent is the qualified name of the owning table (or, one
// level down, the qualified name of the parent partition).
func diffPartitionList(schema, parent string, desired []*ir.Partition, snap []snapshot.SnapPartition, pos pipeline.SourcePos) []pipeline.DiffOp {
	var ops []pipeline.DiffOp

	snapMap := make(map[string]snapshot.SnapPartition, len(snap))
	for _, sp := range snap {
		snapMap[sp.Name] = sp
	}
	desiredSet := make(map[string]bool, len(desired))
	for _, p := range desired {
		desiredSet[p.Name] = true
	}

	for _, p := range desired {
		sp, exists := snapMap[p.Name]
		partTbl := qualIdent(schema, p.Name)
		desiredPB := ""
		if p.PartitionBy != nil {
			desiredPB = p.PartitionBy.Strategy + " (" + strings.Join(p.PartitionBy.Columns, ", ") + ")"
		}
		switch {
		case !exists:
			ops = append(ops, createPartitionOps(schema, parent, p)...)
		case sp.Bound != p.Bounds:
			// PG cannot alter partition bounds; requires DROP + CREATE.
			ops = append(ops, destructiveOp(fmt.Sprintf("DROP TABLE %s;", partTbl), p.SrcPos))
			ops = append(ops, createPartitionOps(schema, parent, p)...)
		case sp.PartitionBy != desiredPB:
			// A sub-partition's own PARTITION BY strategy cannot be altered
			// in place either.
			ops = append(ops, manualOp(
				fmt.Sprintf("-- PARTITION BY changed on %s; table must be recreated to alter the partition strategy", partTbl),
				p.SrcPos,
			))
		default:
			ops = append(ops, diffPartitionList(schema, partTbl, p.Partitions, sp.Partitions, p.SrcPos)...)
		}
	}
	for _, sp := range snap {
		if !desiredSet[sp.Name] {
			ops = append(ops, destructiveOp(
				fmt.Sprintf("DROP TABLE %s;", qualIdent(sp.Schema, sp.Name)),
				pos,
			))
		}
	}
	return ops
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
func diffColumns(tbl string, o *ir.Table, snap *snapshot.SnapTable, vtypes map[string]string) ([]pipeline.DiffOp, map[string]string, map[string]bool, error) {
	var ops []pipeline.DiffOp

	// PostgreSQL PRIMARY KEY implies NOT NULL. Snapshots written before this
	// inference was applied may have not_null=false for PK columns; normalise
	// them here so we don't emit a spurious SET NOT NULL on every plan run.
	pkCols := make(map[string]bool)
	for _, sc := range snap.Constraints {
		if sc.Type == "PRIMARY KEY" {
			for _, col := range localConstraintCols(sc.Expr) {
				pkCols[col] = true
			}
		}
	}
	for i := range snap.Columns {
		if pkCols[snap.Columns[i].Name] {
			snap.Columns[i].NotNull = true
		}
	}

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
			if col.Serial != nil {
				fmt.Fprintf(&b, "ALTER TABLE %s ADD COLUMN %s %s", tbl, quoteIdent(col.Name), *col.Serial)
			} else {
				fmt.Fprintf(&b, "ALTER TABLE %s ADD COLUMN %s %s", tbl, quoteIdent(col.Name), resolveColType(col.Type, vtypes))
			}
			if col.NotNull && col.Serial == nil {
				b.WriteString(" NOT NULL")
			}
			if col.Default != nil && col.Serial == nil {
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
			b.WriteString(";")
			// NOT NULL without a volatile default risks failing on existing
			// rows. SERIAL is exempt — PostgreSQL auto-populates it via the
			// sequence default, same exemption Identity/Generated already get.
			safety := pipeline.Safe
			if col.NotNull && col.Default == nil && col.Identity == nil && col.Generated == nil && col.Serial == nil {
				safety = pipeline.Caution
			}
			ops = append(ops, &op{sql: b.String(), safety: safety, pos: col.SrcPos, txn: true})
			if txt := effectiveComment(col.Comment, col.Deprecated); txt != nil {
				ops = append(ops, safeOp(
					fmt.Sprintf("COMMENT ON COLUMN %s.%s IS %s;", tbl, quoteIdent(col.Name), quoteLit(*txt)),
					col.SrcPos,
				))
			}
			for _, g := range col.Grants {
				ops = append(ops, colGrantOp(g, tbl, col.Name, col.SrcPos))
			}
			for _, r := range col.Revocations {
				ops = append(ops, colExplicitRevokeOp(r, tbl, col.Name, col.SrcPos))
			}
			continue
		}

		// Alter existing column.
		resolvedType := resolveColType(col.Type, vtypes)
		if col.Serial != nil && isLegacySerialTypeName(sc.Type) {
			// Pre-upgrade snapshot stored the literal, un-normalized
			// "serial"/"bigserial"/"smallserial" type name (this project's
			// own prior representation, before SERIAL got a distinct IR
			// marker) — the live column hasn't actually changed shape, only
			// DPG's own internal representation of it has. Treat as no
			// drift; the snapshot self-heals to the new normalized shape
			// ("integer"+Serial marker etc.) on this run's save, the same
			// way a plpgsql BodyHash-algorithm upgrade self-heals (see
			// internal/ir/typeutil.go's canonicalizePlpgsqlBody).
		} else if sc.Type == legacyTypeNameBeforeFix(resolvedType) {
			// Pre-2026-08-17 snapshot stored the un-normalized short
			// spelling ("timestamp"/"time"/"varbit") for a type
			// PGCatalogName now maps to its full canonical form — see
			// legacyTypeNameBeforeFix's doc comment. The live column hasn't
			// actually changed, only DPG's own rendering of it has. Treat
			// as no drift; self-heals to the new spelling on this run's
			// save, same pattern as the SERIAL case just above.
		} else if resolvedType != sc.Type {
			using := ""
			// RFC §7.2/§17.2 "Column type change diffing": a type change is
			// CAUTION when the user supplies an explicit USING expression
			// (their own safe conversion), OR when no USING is given but
			// PostgreSQL itself has an implicit cast (pg_cast.castcontext =
			// 'i') between the old and new base types — RFC §17.2's own
			// "ALTER TABLE ALTER COLUMN TYPE (implicit cast) -> CAUTION" row
			// — OR the change is a same-base-type modifier (length/
			// precision/scale) widening PostgreSQL applies with no data
			// loss, RFC §7.2's own primary example (VARCHAR(10) ->
			// VARCHAR(20)) — see typmod_widening.go for the full per-type-
			// family rules, each verified live, not assumed. Anything else
			// (no USING, no implicit cast, no provable widening) stays the
			// safe, conservative DESTRUCTIVE default. Found live-testing a
			// demo project: this was once hardcoded destructiveOp
			// unconditionally, so even a correctly-supplied USING clause
			// got treated as potential data loss requiring
			// --allow-destructive.
			safety := pipeline.Destructive
			switch {
			case col.Using != nil:
				using = " USING " + *col.Using
				safety = pipeline.Caution
			case hasImplicitCast(sc.Type, resolvedType):
				safety = pipeline.Caution
			case typmodWideningSafe(sc.Type, resolvedType):
				safety = pipeline.Caution
			}
			ops = append(ops, &op{
				sql:    fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s%s;", tbl, quoteIdent(col.Name), resolvedType, using),
				safety: safety,
				pos:    col.SrcPos,
				txn:    true,
			})
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
		if desiredTxt, snapTxt := effectiveComment(col.Comment, col.Deprecated), effectiveComment(sc.Comment, sc.Deprecated); !ptrEq(desiredTxt, snapTxt) {
			if desiredTxt != nil {
				ops = append(ops, safeOp(
					fmt.Sprintf("COMMENT ON COLUMN %s.%s IS %s;", tbl, quoteIdent(col.Name), quoteLit(*desiredTxt)),
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
		ops = append(ops, diffColGrantSet(tbl, col.Name, sc.Grants, col.Grants, col.SrcPos)...)
		ops = append(ops, diffColRevocationSet(tbl, col.Name, sc.Revocations, col.Revocations, col.SrcPos)...)
	}

	return ops, renamedFrom, droppedCols, nil
}

func diffConstraints(tbl string, o *ir.Table, snap *snapshot.SnapTable, fullSnap *pipeline.Snapshot, pos pipeline.SourcePos, renamedCols map[string]string, droppedCols map[string]bool) []pipeline.DiffOp {
	var ops []pipeline.DiffOp

	// Inline constraints (e.g. `id BIGINT PRIMARY KEY`) have no user-supplied
	// name, so matching by name alone would treat them as new on every run.
	// An unnamed desired constraint is matched against the snapshot side two
	// ways:
	//   1. A structural signature (type + normalized expression) — matches
	//      an unnamed snapshot-side constraint. This is the offline
	//      plan/apply path: the persisted snapshot.json preserves an
	//      original unnamed declaration's empty name verbatim, so both sides
	//      key identically.
	//   2. PostgreSQL's own auto-naming algorithm, reconstructed (see
	//      pgAutoConstraintName) — matches a REAL generated name. This is
	//      the verify/plan --live path: live introspection's
	//      pg_constraint.conname is NEVER empty, since PG always assigns a
	//      name (e.g. "t_pkey") even when the user wrote none, so without
	//      this an unnamed inline PK/UNIQUE/FK/CHECK/EXCLUDE produced a
	//      self-inconsistent DROP+ADD pair on every single run. Covers
	//      PRIMARY KEY/UNIQUE/FOREIGN KEY/CHECK/EXCLUDE, including EXCLUDE
	//      elements that are expressions rather than plain columns — see
	//      predictName's "excl" case, which reconstructs PostgreSQL's own
	//      "expr"/"expr1" literal-fallback naming for any element shape its
	//      syntax-only PredictedName rules don't otherwise cover, so this
	//      strategy applies uniformly with no narrower EXCLUDE carve-out.
	// Note both matching strategies are identity-only, never full-definition
	// equality — this is a pre-existing property of this function (a named
	// constraint whose definition changes while keeping the same name was
	// already invisible to it before this fix) that now also applies to an
	// unnamed PRIMARY KEY specifically: PG's generated PK name never encodes
	// columns (a table has only one PK), so an unnamed PK moved to a
	// different column on both sides can't be distinguished from unchanged.
	// Genuine removal, or a change of constraint TYPE, are still caught
	// correctly either way, since those change which map entry (if any) a
	// snapshot constraint matches.
	// The snapshot expression still references pre-rename column names, so
	// apply the rename map first — otherwise a plain RENAMED FROM would
	// surface as a spurious drop+recreate of every constraint touching the
	// renamed column.
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

	// The collision universe pgAutoConstraintName needs: every constraint
	// name already used by another table in this schema (schemaConstraintNames),
	// PLUS — for PRIMARY KEY/UNIQUE specifically, since those are index-backed —
	// every other RELATION name in the schema (schemaRelationNames), matching
	// PostgreSQL's own ChooseRelationName, which checks pg_class, not just
	// pg_constraint. FOREIGN KEY and CHECK aren't index-backed (their default
	// names go through ChooseConstraintName, which only ever checks
	// pg_constraint — see heap.c/pg_constraint.c), so they only get the
	// narrower constraint-name set (see schemaRelationNames' doc comment).
	// The current table's own names are excluded from both — those are
	// exactly the pre-existing assignments a recomputed name is trying to
	// reproduce, not competitors to avoid. Each unnamed constraint gets an
	// independent copy so that multiple unnamed constraints on the same
	// table don't influence one another's predicted name (PG's actual
	// within-batch collision order for that pathological case can't be
	// reconstructed after the fact).
	otherTableNames := schemaConstraintNames(fullSnap, o.Schema, o.Name)
	otherRelationNames := schemaRelationNames(fullSnap, o.Schema, o.Name)
	// predictName returns PostgreSQL's predicted auto-generated name for an
	// unnamed constraint, and whether a prediction was possible at all —
	// distinct from CHECK's cols==nil case (still a real, valid PG-generated
	// name, "tab_check" with no name2). The only case predictName cannot
	// predict is a malformed EXCLUDE with zero elements (see the "excl" case
	// below) — every real element shape, including a bare uncast expression,
	// is now predictable.
	predictName := func(c *ir.Constraint, label string) (string, bool) {
		existing := maps.Clone(otherTableNames)
		if label == "pkey" || label == "key" || label == "excl" {
			// EXCLUDE is index-backed (ChooseIndexName's exclusionOpNames
			// branch calls ChooseRelationName, the same pg_class-scanning
			// path PRIMARY KEY/UNIQUE use) — confirmed live: a plain table
			// coincidentally named like an EXCLUDE's predicted name forces
			// PostgreSQL to fall back to a "1"-suffixed name, exactly the
			// PRIMARY KEY/UNIQUE relation-name-collision case already
			// guarded for those two.
			maps.Copy(existing, otherRelationNames)
		}
		var cols []string
		switch label {
		case "check":
			// CHECK's name2 is the single referenced column, not the full
			// set c.Columns carries for other purposes (createTable's
			// inline-rendering marker) — see CheckColumn's doc comment.
			if c.CheckColumn != nil {
				cols = []string{*c.CheckColumn}
			}
		case "excl":
			// EXCLUDE's name2 is built from each element's own name, in
			// order, joined by "_" (ChooseIndexNameAddition — confirmed
			// live: EXCLUDE USING gist (room WITH =, during WITH &&) on
			// table "bookings" generates "bookings_room_during_excl"). A
			// plain column contributes its own name; any OTHER element
			// shape's contribution is Exclude.Elements[i].PredictedName,
			// populated by figureIndexColname (internal/ir/builder.go) — a
			// port of PostgreSQL's real FigureColnameInternal, confirmed
			// live to run BEFORE expression analysis (no catalog/OID
			// resolution involved), covering a bare function call (its own
			// name, never descending into arguments), NULLIF(a,b) (the
			// literal "nullif"), and a type cast (recursing into its
			// argument, falling back to the cast's own target type name
			// only when the argument isn't itself a column or function
			// call). A shape none of those rules cover — a bare, uncast
			// operator expression, e.g. "a + b" — leaves PredictedName
			// empty at the IR layer (see its doc comment), but this is
			// NOT the end of PostgreSQL's own algorithm: ChooseIndexColumnNames
			// (indexcmds.c) falls back to the literal string "expr" for any
			// element whose indexcolname AND simple-column name are both
			// unset — previously misdiagnosed in this codebase as
			// "unpredictable" and left as a false/false return, corrected
			// after live-verifying against PG 17 that
			// EXCLUDE USING gist ((a + b) WITH =) really does generate
			// "<table>_expr_excl", and two such elements on one constraint
			// dedup to "expr"/"expr1" as expected (dedupIndexColNames below
			// already handles that generically). This makes EXCLUDE naming
			// fully offline-predictable for every element shape; there is
			// no remaining case that needs a live catalog connection.
			if c.Exclude == nil || len(c.Exclude.Elements) == 0 {
				return "", false
			}
			var raw []string
			for _, el := range c.Exclude.Elements {
				switch {
				case el.Column != "":
					raw = append(raw, el.Column)
				case el.PredictedName != "":
					raw = append(raw, el.PredictedName)
				default:
					raw = append(raw, "expr")
				}
			}
			// PostgreSQL also deduplicates same-derived-name elements
			// within the one index (ChooseIndexColumnNames — confirmed
			// live: two lower(...) elements on different columns produce
			// "lower" and "lower1", joined as name2 "lower_lower1", not a
			// name2 with two identical "lower" components).
			cols = dedupIndexColNames(raw)
		default:
			cols = c.Columns
		}
		return pgAutoConstraintName(o.Name, cols, label, existing), true
	}

	desiredByKey := make(map[string]*ir.Constraint, len(o.Constraints))
	for _, c := range o.Constraints {
		desiredByKey[key(c.Name, c.Type, c.Expr)] = c
		if c.Name == "" {
			if label, ok := pgConstraintNameLabel(c.Type); ok {
				if predicted, predOK := predictName(c, label); predOK {
					desiredByKey["n:"+predicted] = c
				}
			}
		}
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
		if sc, exists := snapByKey[key(c.Name, c.Type, c.Expr)]; exists {
			ops = append(ops, validateConstraintOp(tbl, sc, c)...)
			continue
		}
		if c.Name == "" {
			if label, ok := pgConstraintNameLabel(c.Type); ok {
				if predicted, predOK := predictName(c, label); predOK {
					if sc, exists := snapByKey["n:"+predicted]; exists {
						ops = append(ops, validateConstraintOp(tbl, sc, c)...)
						continue
					}
				}
			}
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

// validateConstraintOp emits ALTER TABLE ... VALIDATE CONSTRAINT ... when a
// constraint that was previously added NOT VALID has had NOT VALID removed
// from source — the second half of the RFC §7.3 NOT VALID lifecycle
// (ADD CONSTRAINT ... NOT VALID, then later VALIDATE CONSTRAINT once the
// author is ready to scan existing rows). sc.Name (the snapshot's recorded
// name), not c.Name, is used as the target: every PostgreSQL constraint has
// a real catalog name even when DPG's source never spelled one out, and sc
// only reaches here via a key that already matched an auto-predicted name.
// The reverse transition (NotValid: false → true) has no PostgreSQL
// equivalent — an already-validated constraint can't be marked NOT VALID
// again — so it's silently a no-op, same as any other unrepresentable state.
func validateConstraintOp(tbl string, sc *snapshot.SnapConstraint, c *ir.Constraint) []pipeline.DiffOp {
	if !sc.NotValid || c.NotValid || sc.Name == "" {
		return nil
	}
	return []pipeline.DiffOp{cautionOp(
		fmt.Sprintf("ALTER TABLE %s VALIDATE CONSTRAINT %s;", tbl, quoteIdent(sc.Name)),
		c.Pos,
	)}
}

// pgConstraintNameLabel returns the label PostgreSQL's auto-naming
// algorithm uses for a constraint type, and whether pgAutoConstraintName can
// reconstruct a name for it at all. For EXCLUDE this is necessary but not
// sufficient: a per-instance check in diffConstraints' predictName further
// restricts prediction to an EXCLUDE whose every element resolves to SOME
// name (a plain column, or the result of figureIndexColname for anything
// else) — see the comment there for exactly what that covers.
func pgConstraintNameLabel(typ string) (label string, ok bool) {
	switch typ {
	case "PRIMARY KEY":
		return "pkey", true
	case "UNIQUE":
		return "key", true
	case "FOREIGN KEY":
		return "fkey", true
	case "CHECK":
		return "check", true
	case "EXCLUDE":
		return "excl", true
	default:
		return "", false
	}
}

// pgAutoConstraintName replicates PostgreSQL's algorithm for generating a
// constraint name when the user supplies none (ChooseConstraintName /
// ChooseRelationName / makeObjectName, in src/backend/catalog/
// pg_constraint.c and src/backend/commands/indexcmds.c /tablecmds.c), so
// that verify/plan --live can recognize a live catalog's auto-generated
// name as the SAME constraint an inline unnamed declaration refers to.
//
// label is "pkey"/"key"/"fkey"/"check"/"excl" (see pgConstraintNameLabel);
// cols is ignored for "pkey" (PG's primary key name never depends on
// columns — a table can only have one), and is joined with "_" for the
// others (for "fkey" this must be the LOCAL/referencing columns, matching
// ChooseForeignKeyConstraintNameAddition, not the referenced table's; for
// "check" the caller must pass either a single-element slice — when the
// expression references exactly one distinct column — or nil, matching
// heap.c's pull_var_clause-based name2 selection, not the constraint's full
// column set; for "excl" the caller must pass every element's column name
// in source order, matching ChooseIndexNameAddition — never called at all
// when any element is an expression, see predictName's "excl" case).
// existingNames is mutated: each name this function returns is added to it,
// so a second call sharing the same map correctly avoids colliding with the
// first — callers that want independent (non-batch) predictions, as
// diffConstraints does, must pass a fresh copy per call.
func pgAutoConstraintName(table string, cols []string, label string, existingNames map[string]bool) string {
	var name2 string
	if label != "pkey" {
		name2 = strings.Join(cols, "_")
	}
	for pass := 0; ; pass++ {
		modlabel := label
		if pass > 0 {
			modlabel = fmt.Sprintf("%s%d", label, pass)
		}
		name := makeObjectName(table, name2, modlabel)
		if !existingNames[name] {
			existingNames[name] = true
			return name
		}
	}
}

// makeObjectName ports PostgreSQL's makeObjectName (src/backend/commands/
// indexcmds.c): build "name1_name2_label" (name2 omitted when empty),
// truncating name1/name2 — never label, which the caller retries with
// instead on collision — to fit within NAMEDATALEN-1 (63) bytes. Truncation
// shortens whichever of name1/name2 is currently longer, one byte at a
// time, then rounds each cut down to the nearest complete UTF-8 rune
// boundary (PG's pg_mbcliplen), so a multi-byte identifier is never split
// mid-character.
func makeObjectName(name1, name2, label string) string {
	const nameDataLen = 64 // PostgreSQL's NAMEDATALEN
	overhead := len(label) + 1
	if name2 != "" {
		overhead++
	}
	avail := nameDataLen - 1 - overhead

	n1, n2 := len(name1), len(name2)
	for n1+n2 > avail {
		if n1 > n2 {
			n1--
		} else {
			n2--
		}
	}
	n1 = mbClipLen(name1, n1)
	if name2 != "" {
		n2 = mbClipLen(name2, n2)
	}

	var b strings.Builder
	b.WriteString(name1[:n1])
	if name2 != "" {
		b.WriteByte('_')
		b.WriteString(name2[:n2])
	}
	b.WriteByte('_')
	b.WriteString(label)
	return b.String()
}

// mbClipLen returns the largest n <= limit such that s[:n] does not split a
// UTF-8 rune, mirroring PostgreSQL's pg_mbcliplen.
func mbClipLen(s string, limit int) int {
	if limit >= len(s) {
		return len(s)
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return limit
}

// dedupIndexColNames replicates PostgreSQL's ChooseIndexColumnNames: when
// two index elements independently derive the SAME name (e.g. two EXCLUDE
// elements that are both a call to the same function, on different
// columns), the second (and any further) conflicting one gets a numeric
// suffix — 1, 2, ... — appended, truncating the ORIGINAL name (never the
// suffix) down to a complete UTF-8 rune boundary if needed to fit
// NAMEDATALEN-1 bytes total. Confirmed live: EXCLUDE ((lower(a)) WITH =,
// (lower(b)) WITH =) generates the per-element names "lower"/"lower1"
// (joined by pgAutoConstraintName's caller into name2 "lower_lower1"), not
// two identical "lower" components. Each name is checked against every
// PRIOR (already-deduped) name in the same call, matching PG's own
// grow-the-result-list-as-you-go comparison.
func dedupIndexColNames(names []string) []string {
	const nameDataLen = 64 // PostgreSQL's NAMEDATALEN
	result := make([]string, 0, len(names))
	for _, origname := range names {
		curname := origname
		for i := 1; slices.Contains(result, curname); i++ {
			suffix := strconv.Itoa(i)
			n := mbClipLen(origname, nameDataLen-1-len(suffix))
			curname = origname[:n] + suffix
		}
		result = append(result, curname)
	}
	return result
}

// schemaConstraintNames collects every constraint name already in use by
// any OTHER table in the given schema, per fullSnap — the collision
// universe pgAutoConstraintName needs, since PostgreSQL's auto-naming
// avoids clashing with any constraint name in the same namespace (schema),
// not just the same table. excludeTable is left out deliberately: see the
// comment at diffConstraints's call site.
func schemaConstraintNames(fullSnap *pipeline.Snapshot, schema, excludeTable string) map[string]bool {
	names := make(map[string]bool)
	if fullSnap == nil {
		return names
	}
	for _, raw := range fullSnap.Objects {
		var so snapshot.SnapObject
		if err := json.Unmarshal(raw, &so); err != nil || so.Table == nil {
			continue
		}
		if so.Table.Schema != schema || so.Table.Name == excludeTable {
			continue
		}
		for _, c := range so.Table.Constraints {
			if c.Name != "" {
				names[c.Name] = true
			}
		}
	}
	return names
}

// schemaRelationNames collects every relation name (table, view, sequence,
// and index) already in use in the given schema, per fullSnap. This is the
// BROADER collision universe PostgreSQL's ChooseRelationName actually
// checks — a pg_class scan, not just a pg_constraint one — for PRIMARY
// KEY/UNIQUE/EXCLUDE specifically: those constraint types are backed by an
// index, so the auto-generated name must be unique among ALL relations in
// the namespace (a plain table happening to be named "orders_pkey" would
// force PG to fall back to "orders_pkey1" for an unnamed PK on "orders",
// even though no OTHER constraint is named "orders_pkey" — confirmed the
// same holds for EXCLUDE live: ChooseIndexName's exclusionOpNames branch
// calls ChooseRelationName too, exactly like the PRIMARY KEY branch).
// FOREIGN KEY and CHECK are NOT index-backed (their default names go
// through ChooseConstraintName, which only ever checks pg_constraint, never
// pg_class — see ChooseConstraintName in pg_constraint.c and heap.c's CHECK
// naming), so callers must not add this set for "fkey"/"check".
// excludeTable's own index names are left out — those are exactly the
// pre-existing backing-index assignment a recomputed name is trying to
// reproduce, not a competitor to avoid.
func schemaRelationNames(fullSnap *pipeline.Snapshot, schema, excludeTable string) map[string]bool {
	names := make(map[string]bool)
	if fullSnap == nil {
		return names
	}
	for _, raw := range fullSnap.Objects {
		var so snapshot.SnapObject
		if err := json.Unmarshal(raw, &so); err != nil {
			continue
		}
		switch {
		case so.Table != nil && so.Table.Schema == schema:
			if so.Table.Name != excludeTable {
				names[so.Table.Name] = true
			}
			if so.Table.Name != excludeTable {
				for _, idx := range so.Table.Indexes {
					names[idx.Name] = true
				}
			}
		case so.View != nil && so.View.Schema == schema:
			names[so.View.Name] = true
		case so.Sequence != nil && so.Sequence.Schema == schema:
			names[so.Sequence.Name] = true
		}
	}
	return names
}

// diffViewIndexes is diffIndexes' sibling for a materialized view's INDICES
// (RFC §8.2). Simpler than the table version: a view has no COLUMN block, so
// there's no RENAMED FROM / DROP COLUMN cascade to translate index
// definitions through — indexes are matched by name and compared as-is.
func diffViewIndexes(schema, view string, desired []*ir.Index, snapIdx []snapshot.SnapIndex) []pipeline.DiffOp {
	var ops []pipeline.DiffOp

	snapByName := make(map[string]*snapshot.SnapIndex, len(snapIdx))
	for i := range snapIdx {
		snapByName[snapIdx[i].Name] = &snapIdx[i]
	}
	desiredByName := make(map[string]*ir.Index, len(desired))
	for _, idx := range desired {
		desiredByName[idx.Name] = idx
	}

	for _, si := range snapIdx {
		if _, ok := desiredByName[si.Name]; !ok {
			ops = append(ops, cautionOp(
				fmt.Sprintf("DROP INDEX IF EXISTS %s;", quoteIdent(si.Name)),
				pipeline.SourcePos{},
			))
		}
	}
	for _, idx := range desired {
		si, exists := snapByName[idx.Name]
		if !exists {
			ops = append(ops, createIndex(schema, view, idx, idx.Concurrently)...)
			continue
		}
		if snapshot.ToSnapIndex(idx) != *si {
			ops = append(ops, cautionOp(
				fmt.Sprintf("DROP INDEX IF EXISTS %s;", quoteIdent(idx.Name)),
				idx.Pos,
			))
			ops = append(ops, createIndex(schema, view, idx, idx.Concurrently)...)
		}
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
		// idx.Concurrently is only ever true when the source wrote
		// CONCURRENTLY explicitly on this index (a bare presence keyword,
		// same as real PostgreSQL — there is no project-wide default and no
		// boolean form). An index added to an EXISTING table (as opposed to
		// one created alongside its own brand-new table — see createTable)
		// honors that explicit request directly.
		concurrent := idx.Concurrently

		si, exists := snapByName[idx.Name]
		if !exists {
			ops = append(ops, createIndex(schema, table, idx, concurrent)...)
			continue
		}
		// A same-named index must still be compared: PG has no generic ALTER
		// for an index's structural definition, so any change to method,
		// uniqueness, columns (incl. sort order/NULLS placement), WHERE,
		// INCLUDE, WITH, or NULLS NOT DISTINCT requires DROP + recreate.
		// Without this, an edited index that keeps its name was a silent
		// no-op on plan/apply.
		//
		// The snapshot's Columns/Include/Where still reference the OLD
		// column name after a RENAMED FROM — translate them before
		// comparing, or every index on a renamed column spuriously compares
		// unequal and gets dropped + recreated for no reason (real
		// PostgreSQL's ALTER TABLE RENAME COLUMN keeps every dependent
		// index transparently, no rebuild needed at all — confirmed live).
		// Mirrors diffConstraints' identical translateConstraintExpr step.
		translatedSnap := *si
		translatedSnap.Columns = translateIndexColumnList(si.Columns, renamedCols)
		translatedSnap.Include = translateIndexColumnList(si.Include, renamedCols)
		translatedSnap.Where = replaceQuotedIdents(si.Where, renamedCols)
		if snapshot.ToSnapIndex(idx) != translatedSnap {
			ops = append(ops, cautionOp(
				fmt.Sprintf("DROP INDEX IF EXISTS %s;", quoteIdent(idx.Name)),
				idx.Pos,
			))
			ops = append(ops, createIndex(schema, table, idx, concurrent)...)
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
	case strings.HasPrefix(upper, "CHECK"), strings.HasPrefix(upper, "EXCLUDE"):
		// Unlike PRIMARY KEY/UNIQUE/FOREIGN KEY (which restrict translation to
		// the first paren group, to avoid touching FOREIGN KEY's unrelated
		// "REFERENCES othertable (othercol)" group), EXCLUDE has no such
		// second, different-table group to protect against: its element list
		// AND its optional WHERE clause both reference only this table's own
		// columns, same as CHECK. Translating the whole string catches a
		// renamed column wherever it appears (element list, an
		// expression-based element, or WHERE) — quoted column names always
		// match the quoteIdent form renderExclude emits.
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
	for part := range strings.SplitSeq(inside, ",") {
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
// (a comma-separated list of column names or `(expression)` entries, each
// optionally suffixed with ASC/DESC and/or NULLS FIRST/LAST — see
// snapshot.ToSnapIndex) and returns the resulting plain column names.
// Expression entries are returned as empty strings so they don't accidentally
// appear "dropped".
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
		// Strip an optional " DESC"/" ASC"/" NULLS FIRST"/" NULLS LAST" suffix,
		// mirroring localConstraintCols's identical treatment for constraints.
		if sp := strings.IndexAny(p, " \t"); sp != -1 {
			p = p[:sp]
		}
		if newName, ok := renamedCols[p]; ok {
			p = newName
		}
		out = append(out, p)
	}
	return out
}

// translateIndexColumnList rewrites a SnapIndex-style comma-separated
// column/Include list (bare names, optionally suffixed with ASC/DESC/NULLS
// …, or a parenthesized "(expr)" entry — see snapshot.ToSnapIndex) so a
// renamed column's OLD name compares equal to the desired side's NEW name.
// Unlike translateIndexCols (which only extracts base names for the
// column-dropped check), this reconstructs the full entry text, suffix and
// all, for direct SnapIndex-to-SnapIndex comparison.
func translateIndexColumnList(list string, renamedCols map[string]string) string {
	if list == "" || len(renamedCols) == 0 {
		return list
	}
	parts := strings.Split(list, ", ")
	for i, p := range parts {
		if strings.HasPrefix(p, "(") {
			parts[i] = replaceQuotedIdents(p, renamedCols)
			continue
		}
		name, rest, hasSuffix := strings.Cut(p, " ")
		if newName, ok := renamedCols[name]; ok {
			name = newName
		}
		if hasSuffix {
			parts[i] = name + " " + rest
		} else {
			parts[i] = name
		}
	}
	return strings.Join(parts, ", ")
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

// replaceQuotedIdents substitutes a renamed column's references in s, both
// quoted ("old" → "new") and bare (old → new, at a word boundary — no
// partial-identifier match, so renaming "a" never touches "cat"). The bare
// form matters because nodeToText/pg_query.Deparse — used to render CHECK
// expressions and EXCLUDE's WHERE clause/expression-based elements — only
// quotes an identifier that actually needs it (confirmed live: Deparse
// renders a plain lowercase column reference unquoted), unlike the
// hand-built, always-quoted column lists PRIMARY KEY/UNIQUE/FOREIGN KEY's
// Expr uses. Matching case-sensitively is protects against clobbering a SQL
// keyword: PostgreSQL folds an unquoted identifier to lowercase at
// declaration time, so a renamed column's key here is always lowercase,
// while Deparse always renders keywords (AND, OR, ASC, DESC, NULL, ...) in
// uppercase — confirmed live — so the two can never collide case-sensitively.
//
// Both substitutions are applied only OUTSIDE single-quoted SQL string
// literals (splitSQLStringLiterals) — an independent verification pass
// caught this as a real gap: a string literal's contents are delimited by
// `'`, a non-word character, so an unqualified `\bold\b` regex would
// otherwise treat 'old' *the string value* exactly like the bare identifier
// old (e.g. CHECK (status <> 'room') with column "room" being renamed would
// have mangled the unrelated literal 'room', producing a spurious
// unchanged-constraint drop+recreate — the exact class of noise this whole
// rename-translation feature exists to eliminate, reintroduced through a
// different path). The same literal-skip is applied to the quoted-ident
// substitution too, for the same reason, even though a real-world collision
// there would need a double-quoted substring INSIDE a single-quoted
// literal — pathological, but the fix is the same either way.
func replaceQuotedIdents(s string, renamedCols map[string]string) string {
	segs := splitSQLStringLiterals(s)
	for i, seg := range segs {
		if seg.isLiteral {
			continue
		}
		for old, newName := range renamedCols {
			seg.text = strings.ReplaceAll(seg.text, `"`+old+`"`, `"`+newName+`"`)
			seg.text = wordBoundaryRegexp(old).ReplaceAllString(seg.text, newName)
		}
		segs[i] = seg
	}
	var b strings.Builder
	for _, seg := range segs {
		b.WriteString(seg.text)
	}
	return b.String()
}

// wordBoundaryRegexp returns a compiled regexp matching ident as a whole
// word (bounded by non-identifier characters, including string start/end).
func wordBoundaryRegexp(ident string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(ident) + `\b`)
}

// sqlSegment is one maximal run of text from splitSQLStringLiterals, tagged
// as either inside or outside a single-quoted SQL string literal.
type sqlSegment struct {
	text      string
	isLiteral bool
}

// splitSQLStringLiterals splits s into alternating literal/non-literal runs,
// so a caller can transform identifiers in the code portions without ever
// touching the contents of a string literal. Handles SQL's doubled-quote
// escape (” inside a literal means a literal single quote, not the end of
// the string) — confirmed this is the real PostgreSQL escaping rule, not
// backslash-escaping, which pg_query.Deparse's own output also uses (e.g.
// 'it”s'), so a naive "split on every apostrophe" would incorrectly end
// the literal early on that case.
func splitSQLStringLiterals(s string) []sqlSegment {
	var segs []sqlSegment
	i := 0
	for i < len(s) {
		start := i
		for i < len(s) && s[i] != '\'' {
			i++
		}
		if i > start {
			segs = append(segs, sqlSegment{text: s[start:i]})
		}
		if i >= len(s) {
			break
		}
		// s[i] == '\'': scan the literal, treating '' as an escaped quote.
		litStart := i
		i++
		for i < len(s) {
			if s[i] == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i += 2
					continue
				}
				i++
				break
			}
			i++
		}
		segs = append(segs, sqlSegment{text: s[litStart:i], isLiteral: true})
	}
	return segs
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
			qualifyFuncForCompare(trg.Function) != qualifyFuncForCompare(existing.Function) {
			ops = append(ops, safeOp(
				fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;", quoteIdent(trg.Name), tblIdent),
				trg.Pos,
			))
			ops = append(ops, createTrigger(schema, table, trg)...)
		}
	}
	return ops
}

// qualifyFuncForCompare normalizes a trigger's EXECUTE FUNCTION reference for
// comparison purposes only (never for rendering — createTrigger still emits
// whatever the user wrote). Introspection always returns a fully schema-
// qualified name (funcSchema + "." + funcName, from a live pg_proc join),
// while hand-written source commonly references a function unqualified,
// relying on the default "public" schema — the same convention DPG's own
// objects get via applySchemaContext. Comparing raw strings treated every
// unqualified trigger function as changed on every verify/plan --live.
func qualifyFuncForCompare(f string) string {
	if strings.Contains(f, ".") {
		return f
	}
	return "public." + f
}

var _ pipeline.Differ = (*Differ)(nil)
