// Package introspect implements pipeline.Introspector. It reads the live
// PostgreSQL catalog (PG 14+) and returns IRObjects equivalent to what the
// compiler would produce from .dpg source files.
package introspect

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	pipeline.Default.Register(pipeline.KeyIntrospector, New())
}

// CatalogIntrospector implements pipeline.Introspector.
type CatalogIntrospector struct{}

// New returns a CatalogIntrospector.
func New() *CatalogIntrospector { return &CatalogIntrospector{} }

// Introspect reads the live PG catalog and returns schema objects as IRObjects.
func (ci *CatalogIntrospector) Introspect(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	var all []pipeline.IRObject

	schemas, err := introspectSchemas(ctx, conn)
	if err != nil {
		return nil, err
	}
	if err := introspectSchemaGrants(ctx, conn, schemas); err != nil {
		return nil, err
	}
	if err := introspectSchemaSecurityLabels(ctx, conn, schemas); err != nil {
		return nil, err
	}
	all = append(all, schemas...)

	extensions, err := introspectExtensions(ctx, conn)
	if err != nil {
		return nil, err
	}
	all = append(all, extensions...)

	tables, err := introspectTables(ctx, conn)
	if err != nil {
		return nil, err
	}
	all = append(all, tables...)

	views, err := introspectViews(ctx, conn)
	if err != nil {
		return nil, err
	}
	all = append(all, views...)

	if err := introspectViewIndexes(ctx, conn, views); err != nil {
		return nil, err
	}

	functions, err := introspectFunctions(ctx, conn)
	if err != nil {
		return nil, err
	}
	all = append(all, functions...)

	types, err := introspectTypes(ctx, conn)
	if err != nil {
		return nil, err
	}
	all = append(all, types...)

	sequences, err := introspectSequences(ctx, conn)
	if err != nil {
		return nil, err
	}
	all = append(all, sequences...)

	roles, err := introspectRoles(ctx, conn)
	if err != nil {
		return nil, err
	}
	all = append(all, roles...)

	aggregates, err := introspectAggregates(ctx, conn)
	if err != nil {
		return nil, err
	}
	all = append(all, aggregates...)

	defaultPrivs, err := introspectDefaultPrivileges(ctx, conn)
	if err != nil {
		return nil, err
	}
	all = append(all, defaultPrivs...)

	// Reliable-tier opaque objects: reconstructed CREATE DDL, canonicalised
	// through pg_query so the body hash matches the compiler's. See opaque.go.
	for _, step := range []func(context.Context, pipeline.Querier) ([]pipeline.IRObject, error){
		introspectCollations,
		introspectCasts,
		introspectStatistics,
		introspectEventTriggers,
		introspectForeignDataWrappers,
		introspectForeignServers,
		introspectUserMappings,
		introspectPublications,
		introspectSubscriptions,
		introspectTablespaces,
		introspectOperators,
		introspectTSParsers,
		introspectTSTemplates,
		introspectTSDicts,
		introspectTSConfigs,
		introspectOperatorFamilies,
		introspectOperatorClasses,
	} {
		objs, err := step(ctx, conn)
		if err != nil {
			return nil, err
		}
		all = append(all, objs...)
	}

	return all, nil
}

// ── aggregates ────────────────────────────────────────────────────────────────

func introspectAggregates(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT n.nspname, p.proname,
       pg_get_function_identity_arguments(p.oid) AS args,
       pg_catalog.oidvectortypes(p.proargtypes)   AS arg_types,
       obj_description(p.oid, 'pg_proc') AS comment,
       a.aggtransfn::text                AS sfunc,
       a.aggtranstype::regtype::text     AS stype,
       a.agginitval                      AS initcond,
       NULLIF(a.aggfinalfn::text, '-')   AS finalfunc,
       NULLIF(a.aggcombinefn::text, '-') AS combinefunc,
       NULLIF(a.aggserialfn::text, '-')  AS serialfunc
FROM   pg_proc p
JOIN   pg_namespace n ON n.oid = p.pronamespace
JOIN   pg_aggregate a ON a.aggfnoid = p.oid
WHERE  p.prokind = 'a'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
AND    NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
ORDER  BY n.nspname, p.proname, args`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect aggregates: %w", err)
	}
	defer rs.Close()

	aggIdx := make(map[string]*ir.Aggregate)
	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, args, argTypes, sfunc, stype string
		var comment, initcond, finalfunc, combinefunc, serialfunc *string
		if err := rs.Scan(&schema, &name, &args, &argTypes, &comment,
			&sfunc, &stype, &initcond, &finalfunc, &combinefunc, &serialfunc); err != nil {
			return nil, err
		}
		agg := &ir.Aggregate{
			Schema:  schema,
			Name:    name,
			Comment: comment,
		}
		// Use argTypes (type-only, from oidvectortypes) so QualifiedName matches
		// ir.ArgsKey(). Keep args (with parameter names) for the grants index key.
		if argTypes != "" {
			for a := range strings.SplitSeq(argTypes, ", ") {
				agg.Args = append(agg.Args, ir.FuncArg{Type: ir.TypeRef{Name: strings.TrimSpace(a)}})
			}
		}
		agg.Options = append(agg.Options, pipeline.StorageParam{Key: "sfunc", Value: sfunc})
		agg.Options = append(agg.Options, pipeline.StorageParam{Key: "stype", Value: stype})
		if initcond != nil {
			agg.Options = append(agg.Options, pipeline.StorageParam{Key: "initcond", Value: quoteLit(*initcond)})
		}
		if finalfunc != nil {
			agg.Options = append(agg.Options, pipeline.StorageParam{Key: "finalfunc", Value: *finalfunc})
		}
		if combinefunc != nil {
			agg.Options = append(agg.Options, pipeline.StorageParam{Key: "combinefunc", Value: *combinefunc})
		}
		if serialfunc != nil {
			agg.Options = append(agg.Options, pipeline.StorageParam{Key: "serialfunc", Value: *serialfunc})
		}
		optParts := make([]string, len(agg.Options))
		for i, p := range agg.Options {
			optParts[i] = p.Key + " = " + p.Value
		}
		argParts := make([]string, len(agg.Args))
		for i, a := range agg.Args {
			argParts[i] = a.Type.String()
		}
		agg.Body = fmt.Sprintf("CREATE AGGREGATE %s (%s) (%s)",
			qualIdentQ(schema, name), strings.Join(argParts, ", "), strings.Join(optParts, ", "))

		aggIdx[schema+"."+name+"("+args+")"] = agg
		out = append(out, agg)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	rs.Close()

	if err := introspectAggregateGrants(ctx, conn, aggIdx); err != nil {
		return nil, err
	}
	if err := introspectAggregateSecurityLabels(ctx, conn, aggIdx); err != nil {
		return nil, err
	}
	return out, nil
}

// functionLikeACLMaterializedKeys returns the set of "schema.name(args)" keys
// (args = pg_get_function_identity_arguments, matching every prokind-scoped
// grants query's own idx key format) for functions/procedures/aggregates
// (selected by prokind: 'f'/'p'/'a') whose pg_proc.proacl has been
// materialized (is NOT NULL) — i.e. at least one explicit GRANT/REVOKE has
// ever touched the object, as opposed to one still carrying Postgres's pure
// implicit-default ACL (proacl IS NULL, PUBLIC's default EXECUTE applies,
// nothing to declare).
//
// This distinction is invisible to aclexplode(proacl) alone: an object whose
// PUBLIC EXECUTE was never granted (proacl IS NULL) and one whose PUBLIC
// EXECUTE was explicitly revoked (proacl IS NOT NULL, no PUBLIC row) both
// produce zero PUBLIC rows from aclexplode. Each *Grants function below uses
// this to synthesize an explicit "REVOKE EXECUTE FROM PUBLIC" Revocation for
// the latter case only — without it, dump→reapply silently restores PUBLIC's
// implicit default on every explicitly-locked-down function-like object,
// undoing a real security decision (see RFC audit item C.4).
func functionLikeACLMaterializedKeys(ctx context.Context, conn pipeline.Querier, prokind string) (map[string]bool, error) {
	const q = `
SELECT n.nspname, p.proname, pg_get_function_identity_arguments(p.oid) AS args
FROM   pg_proc p
JOIN   pg_namespace n ON n.oid = p.pronamespace
WHERE  p.prokind = $1
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
AND    p.proacl IS NOT NULL`

	rs, err := conn.QueryRows(ctx, q, prokind)
	if err != nil {
		return nil, fmt.Errorf("introspect function-like ACL materialization: %w", err)
	}
	defer rs.Close()

	out := make(map[string]bool)
	for rs.Next() {
		var schema, name, args string
		if err := rs.Scan(&schema, &name, &args); err != nil {
			return nil, err
		}
		out[schema+"."+name+"("+args+")"] = true
	}
	return out, rs.Err()
}

func introspectAggregateGrants(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Aggregate) error {
	const q = `
SELECT n.nspname, p.proname,
       pg_get_function_identity_arguments(p.oid) AS args,
       CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END AS grantee,
       a.privilege_type, a.is_grantable
FROM   pg_proc p
JOIN   pg_namespace n ON n.oid = p.pronamespace,
       LATERAL aclexplode(p.proacl) a
WHERE  p.prokind = 'a'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, p.proname, args, grantee, a.privilege_type`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect aggregate grants: %w", err)
	}
	defer rs.Close()

	type grantKey struct{ schema, name, args, grantee string }
	type grantEntry struct {
		privs     []string
		grantable bool
	}
	grants := make(map[grantKey]*grantEntry)
	var order []grantKey

	for rs.Next() {
		var schema, name, args, grantee, priv string
		var grantable bool
		if err := rs.Scan(&schema, &name, &args, &grantee, &priv, &grantable); err != nil {
			return err
		}
		k := grantKey{schema, name, args, grantee}
		e, ok := grants[k]
		if !ok {
			e = &grantEntry{}
			grants[k] = e
			order = append(order, k)
		}
		e.privs = append(e.privs, priv)
		if grantable {
			e.grantable = true
		}
	}
	if err := rs.Err(); err != nil {
		return err
	}

	for _, k := range order {
		agg, ok := idx[k.schema+"."+k.name+"("+k.args+")"]
		if !ok {
			continue
		}
		e := grants[k]
		agg.Grants = append(agg.Grants, ir.Grant{
			Privileges: e.privs,
			Roles:      []string{k.grantee},
			WithGrant:  e.grantable,
		})
	}

	materialized, err := functionLikeACLMaterializedKeys(ctx, conn, "a")
	if err != nil {
		return err
	}
	for key, agg := range idx {
		if !materialized[key] || hasPublicGrant(agg.Grants) {
			continue
		}
		agg.Revocations = append(agg.Revocations, ir.Revocation{
			Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"},
		})
	}
	return nil
}

// hasPublicGrant reports whether any grant in grants names the PUBLIC
// pseudo-role — used by *Grants functions to decide whether a materialized
// ACL's missing PUBLIC row means "explicitly revoked" (see
// functionLikeACLMaterializedKeys's doc comment).
func hasPublicGrant(grants []ir.Grant) bool {
	for _, g := range grants {
		if slices.Contains(g.Roles, "PUBLIC") {
			return true
		}
	}
	return false
}

// ── default privileges ──────────────────────────────────────────────────────

// defaclObjTypeNames maps pg_default_acl.defaclobjtype's single-char code to
// the ON <type> keyword real PostgreSQL's own ALTER DEFAULT PRIVILEGES
// grammar uses (confirmed live via \h ALTER DEFAULT PRIVILEGES: TABLES,
// SEQUENCES, FUNCTIONS, TYPES, SCHEMAS).
var defaclObjTypeNames = map[string]string{
	"r": "TABLES",
	"S": "SEQUENCES",
	"f": "FUNCTIONS",
	"T": "TYPES",
	"n": "SCHEMAS",
}

// introspectDefaultPrivileges reads pg_default_acl, one *ir.DefaultPrivileges
// per (role, schema, object type) tuple — matching the catalog's own model,
// and Builder.BuildDefaultPrivileges's identical split for the compiled-
// source side (see ir.DefaultPrivileges.QualifiedName). defaclnamespace == 0
// means "no schema restriction" (a database-wide default), represented as a
// nil InSchema, matching how the compiler leaves it unset when no "IN
// SCHEMA" clause was declared.
func introspectDefaultPrivileges(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT r.rolname AS for_role, n.nspname AS in_schema, d.defaclobjtype::text,
       CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END AS grantee,
       a.privilege_type, a.is_grantable
FROM   pg_default_acl d
JOIN   pg_roles r ON r.oid = d.defaclrole
LEFT   JOIN pg_namespace n ON n.oid = NULLIF(d.defaclnamespace, 0),
       LATERAL aclexplode(d.defaclacl) a
WHERE  n.nspname IS NULL OR n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
ORDER  BY r.rolname, n.nspname, d.defaclobjtype, grantee, a.privilege_type`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect default privileges: %w", err)
	}
	defer rs.Close()

	type dpKey struct{ forRole, inSchema, objType string }
	type grantKey struct {
		dpKey
		grantee string
	}
	type grantEntry struct {
		privs     []string
		grantable bool
	}
	grants := make(map[grantKey]*grantEntry)
	var grantOrder []grantKey
	dpSeen := make(map[dpKey]bool)
	var dpOrder []dpKey

	for rs.Next() {
		var forRole, objType, grantee, priv string
		var inSchema *string
		var grantable bool
		if err := rs.Scan(&forRole, &inSchema, &objType, &grantee, &priv, &grantable); err != nil {
			return nil, err
		}
		schemaVal := ""
		if inSchema != nil {
			schemaVal = *inSchema
		}
		dk := dpKey{forRole: forRole, inSchema: schemaVal, objType: objType}
		if !dpSeen[dk] {
			dpSeen[dk] = true
			dpOrder = append(dpOrder, dk)
		}
		gk := grantKey{dpKey: dk, grantee: grantee}
		e, ok := grants[gk]
		if !ok {
			e = &grantEntry{}
			grants[gk] = e
			grantOrder = append(grantOrder, gk)
		}
		e.privs = append(e.privs, priv)
		if grantable {
			e.grantable = true
		}
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}

	dpIdx := make(map[dpKey]*ir.DefaultPrivileges, len(dpOrder))
	out := make([]pipeline.IRObject, 0, len(dpOrder))
	for _, dk := range dpOrder {
		objType := defaclObjTypeNames[dk.objType]
		if objType == "" {
			objType = dk.objType // unrecognized code: pass through raw rather than silently drop
		}
		role := dk.forRole
		dp := &ir.DefaultPrivileges{ForRole: &role, ObjectType: objType}
		if dk.inSchema != "" {
			schema := dk.inSchema
			dp.InSchema = &schema
		}
		dpIdx[dk] = dp
		out = append(out, dp)
	}
	for _, gk := range grantOrder {
		dp := dpIdx[gk.dpKey]
		e := grants[gk]
		dp.Grants = append(dp.Grants, ir.Grant{
			Privileges: e.privs,
			Roles:      []string{gk.grantee},
			WithGrant:  e.grantable,
		})
	}
	return out, nil
}

// ── schemas ───────────────────────────────────────────────────────────────────

// introspectSchemas reads every managed schema, including "public". Unlike
// "information_schema"/"pg_%" (genuinely PostgreSQL-internal, never user-
// managed), "public" is a real, commonly-granted-on schema that used to be
// hard-excluded here — making its actual Owner/Comment/Grants state
// completely invisible to `dpg dump` and `plan --live`/`verify` drift
// detection (confirmed live: a `GRANT USAGE ON SCHEMA public` was silently
// dropped by dump and never flagged as drift — RFC audit item C.2). The
// differ's own diffing/dump-render logic for ir.Schema was already correct
// once given a snapshot to compare against; only introspection's read side
// was excluding it. The corresponding "never propose DROP SCHEMA public"
// lifecycle guard lives in differ.go's Pass 2 (schemas/tables/etc. absent
// from desired but present in snap) — "public" always exists in PostgreSQL
// and is never dropped, the same reasoning compiler.go already documents
// for why it's skipped from directory-based synthetic schema declarations.
func introspectSchemas(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT n.nspname,
       r.rolname AS owner,
       obj_description(n.oid, 'pg_namespace') AS comment
FROM   pg_namespace n
JOIN   pg_roles r ON r.oid = n.nspowner
WHERE  n.nspname NOT LIKE 'pg_%'
AND    n.nspname != 'information_schema'
ORDER  BY n.nspname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect schemas: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var name, owner string
		var comment *string
		if err := rs.Scan(&name, &owner, &comment); err != nil {
			return nil, err
		}
		out = append(out, &ir.Schema{Name: name, Owner: &owner, Comment: comment})
	}
	return out, rs.Err()
}

// introspectSchemaGrants populates Schema.Grants for every schema in objs
// using aclexplode on pg_namespace.nspacl, mirroring introspectTableGrants.
// Includes "public" — see introspectSchemas' doc comment (RFC audit item
// C.2); the grantee CASE's 'PUBLIC' pseudo-role string is unrelated to and
// not to be confused with the schema literally named "public".
func introspectSchemaGrants(ctx context.Context, conn pipeline.Querier, objs []pipeline.IRObject) error {
	idx := make(map[string]*ir.Schema, len(objs))
	for _, o := range objs {
		if s, ok := o.(*ir.Schema); ok {
			idx[s.Name] = s
		}
	}
	if len(idx) == 0 {
		return nil
	}

	// aclexplode(NULL) returns 0 rows on PG14+, so no COALESCE needed.
	const q = `
SELECT n.nspname,
       CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END AS grantee,
       a.privilege_type, a.is_grantable
FROM   pg_namespace n,
       LATERAL aclexplode(n.nspacl) a
WHERE  n.nspname NOT LIKE 'pg_%'
AND    n.nspname != 'information_schema'
ORDER  BY n.nspname, grantee, a.privilege_type`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect schema grants: %w", err)
	}
	defer rs.Close()

	type grantKey struct{ schema, grantee string }
	type grantEntry struct {
		privs     []string
		grantable bool
	}
	grants := make(map[grantKey]*grantEntry)
	var order []grantKey

	for rs.Next() {
		var schema, grantee, priv string
		var grantable bool
		if err := rs.Scan(&schema, &grantee, &priv, &grantable); err != nil {
			return err
		}
		k := grantKey{schema, grantee}
		e, ok := grants[k]
		if !ok {
			e = &grantEntry{}
			grants[k] = e
			order = append(order, k)
		}
		e.privs = append(e.privs, priv)
		if grantable {
			e.grantable = true
		}
	}
	if err := rs.Err(); err != nil {
		return err
	}

	for _, k := range order {
		s, ok := idx[k.schema]
		if !ok {
			continue
		}
		e := grants[k]
		s.Grants = append(s.Grants, ir.Grant{
			Privileges: e.privs,
			Roles:      []string{k.grantee},
			WithGrant:  e.grantable,
		})
	}
	return nil
}

// ── extensions ────────────────────────────────────────────────────────────────

func introspectExtensions(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT e.extname,
       n.nspname AS schema,
       e.extversion,
       obj_description(e.oid, 'pg_extension') AS comment
FROM   pg_extension e
JOIN   pg_namespace n ON n.oid = e.extnamespace
WHERE  n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER  BY e.extname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect extensions: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var name, schema, version string
		var comment *string
		if err := rs.Scan(&name, &schema, &version, &comment); err != nil {
			return nil, err
		}
		out = append(out, &ir.Extension{Name: name, Schema: &schema, Version: &version, Comment: comment})
	}
	return out, rs.Err()
}

// ── tables ────────────────────────────────────────────────────────────────────

// replicaIdentityFromCatalog maps pg_class.relreplident ('d'=default,
// 'f'=full, 'n'=nothing, 'i'=index) plus the index name (only populated
// server-side when relreplident == 'i') to ir.ReplicaIdentity. "DEFAULT" is
// PostgreSQL's own default and left as the zero-value Mode, matching how a
// source declaration that omits the directive builds the same zero value —
// so an unmodified table never shows spurious drift after introspection.
func replicaIdentityFromCatalog(replident string, indexName *string) ir.ReplicaIdentity {
	switch replident {
	case "f":
		return ir.ReplicaIdentity{Mode: "FULL"}
	case "n":
		return ir.ReplicaIdentity{Mode: "NOTHING"}
	case "i":
		name := ""
		if indexName != nil {
			name = *indexName
		}
		return ir.ReplicaIdentity{Mode: "INDEX", IndexName: name}
	default:
		return ir.ReplicaIdentity{}
	}
}

func introspectTables(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT c.relname, n.nspname, c.relpersistence::text, c.relkind::text,
       r.rolname AS owner,
       obj_description(c.oid, 'pg_class') AS comment,
       c.relrowsecurity, c.relforcerowsecurity,
       fs.srvname, ft.ftoptions,
       ts.spcname AS tablespace,
       c.relreplident::text,
       (SELECT ic.relname FROM pg_index pi JOIN pg_class ic ON ic.oid = pi.indexrelid
          WHERE pi.indrelid = c.oid AND pi.indisreplident) AS replident_index,
       (SELECT ic2.relname FROM pg_index pi2 JOIN pg_class ic2 ON ic2.oid = pi2.indexrelid
          WHERE pi2.indrelid = c.oid AND pi2.indisclustered) AS cluster_index
FROM   pg_class c
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_roles r     ON r.oid = c.relowner
LEFT   JOIN pg_foreign_table ft  ON ft.ftrelid = c.oid
LEFT   JOIN pg_foreign_server fs ON fs.oid = ft.ftserver
LEFT   JOIN pg_tablespace ts     ON ts.oid = c.reltablespace
WHERE  c.relkind IN ('r', 'p', 'f')
AND    NOT c.relispartition
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
AND    NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
ORDER  BY n.nspname, c.relname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect tables: %w", err)
	}
	defer rs.Close()

	var tables []*ir.Table
	tableIdx := map[string]*ir.Table{}

	for rs.Next() {
		var name, schema, persistence, relkind, owner, replident string
		var comment, server, tablespace, replidentIndex, clusterIndex *string
		var rlsEnabled, rlsForced bool
		var ftoptions []string
		if err := rs.Scan(&name, &schema, &persistence, &relkind, &owner, &comment, &rlsEnabled, &rlsForced, &server, &ftoptions, &tablespace, &replident, &replidentIndex, &clusterIndex); err != nil {
			return nil, err
		}
		t := &ir.Table{
			Schema:          schema,
			Name:            name,
			Unlogged:        persistence == "u",
			Foreign:         relkind == "f",
			Owner:           &owner,
			Comment:         comment,
			RLSEnabled:      rlsEnabled,
			RLSForced:       rlsForced,
			Tablespace:      tablespace,
			ReplicaIdentity: replicaIdentityFromCatalog(replident, replidentIndex),
			ClusterOn:       clusterIndex,
		}
		if t.Foreign {
			t.ForeignServer = server
			for _, kv := range ftoptions {
				if k, v, ok := strings.Cut(kv, "="); ok {
					t.ForeignOptions = append(t.ForeignOptions, pipeline.StorageParam{Key: k, Value: v})
				}
			}
		}
		tables = append(tables, t)
		tableIdx[schema+"."+name] = t
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	rs.Close()

	if err := introspectColumns(ctx, conn, tableIdx); err != nil {
		return nil, err
	}
	if err := introspectConstraints(ctx, conn, tableIdx); err != nil {
		return nil, err
	}
	if err := introspectIndexes(ctx, conn, tableIdx); err != nil {
		return nil, err
	}
	if err := introspectPolicies(ctx, conn, tableIdx); err != nil {
		return nil, err
	}
	if err := introspectTriggers(ctx, conn, tableIdx); err != nil {
		return nil, err
	}
	if err := introspectPartitions(ctx, conn, tableIdx); err != nil {
		return nil, err
	}
	if err := introspectTableInherits(ctx, conn, tableIdx); err != nil {
		return nil, err
	}
	if err := introspectTableSecurityLabels(ctx, conn, tableIdx); err != nil {
		return nil, err
	}
	if err := introspectColumnSecurityLabels(ctx, conn, tableIdx); err != nil {
		return nil, err
	}
	if err := introspectTableGrants(ctx, conn, tableIdx); err != nil {
		return nil, err
	}
	if err := introspectColumnGrants(ctx, conn, tableIdx); err != nil {
		return nil, err
	}

	out := make([]pipeline.IRObject, len(tables))
	for i, t := range tables {
		out[i] = t
	}
	return out, nil
}

// introspectColumns populates Table.Columns. relkind matches both 'r'
// (ordinary table) and 'p' (partitioned parent) — a partitioned table's own
// columns live in pg_attribute the same as an ordinary table's, but omitting
// 'p' here silently left every partitioned table's Columns empty.
// serialMarkerFromWidth maps a format_type() result to the canonical
// ir.Column.Serial marker spelling for a SERIAL-owned column, mirroring
// internal/ir/typeutil.go's serialUnderlyingType in reverse (that function
// maps a source-declared SERIAL name to its underlying type; this one maps
// the underlying type read back from a live catalog to the marker).
func serialMarkerFromWidth(dataType string) (string, bool) {
	switch dataType {
	case "smallint":
		return "SMALLSERIAL", true
	case "integer":
		return "SERIAL", true
	case "bigint":
		return "BIGSERIAL", true
	default:
		return "", false
	}
}

func introspectColumns(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Table) error {
	const q = `
SELECT n.nspname, c.relname,
       a.attname,
       pg_catalog.format_type(a.atttypid, a.atttypmod) AS data_type,
       a.attnotnull,
       NULLIF(a.attidentity::text, '') AS identity_kind,
       NULLIF(a.attgenerated::text, '') AS generated_kind,
       pg_get_expr(d.adbin, d.adrelid) AS col_default,
       col_description(a.attrelid, a.attnum) AS comment,
       a.attstattarget,
       CASE a.attcompression WHEN '' THEN NULL ELSE a.attcompression::text END AS compression,
       CASE a.attstorage
           WHEN 'p' THEN 'PLAIN' WHEN 'e' THEN 'EXTERNAL'
           WHEN 'm' THEN 'MAIN'  WHEN 'x' THEN 'EXTENDED'
           ELSE NULL
       END AS storage,
       a.attstorage = t.typstorage AS storage_is_type_default,
       EXISTS (
           SELECT 1 FROM pg_depend dep
           JOIN pg_class sc ON sc.oid = dep.objid AND sc.relkind = 'S'
           WHERE dep.deptype = 'a'
             AND dep.classid = 'pg_class'::regclass
             AND dep.refclassid = 'pg_class'::regclass
             AND dep.refobjid = a.attrelid
             AND dep.refobjsubid = a.attnum
       ) AS serial_owned
FROM   pg_attribute a
JOIN   pg_class c     ON c.oid = a.attrelid
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_type t      ON t.oid = a.atttypid
LEFT   JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE  a.attnum > 0
AND    NOT a.attisdropped
-- attislocal excludes columns present only via classic table INHERITS
-- (not partitioning, already excluded above) — Table.Columns mirrors what
-- a DPG declaration itself lists, not what a parent contributes implicitly.
AND    a.attislocal
AND    c.relkind IN ('r', 'p', 'f')
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, c.relname, a.attnum`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect columns: %w", err)
	}
	defer rs.Close()

	for rs.Next() {
		var schema, table, name, dataType string
		var notNull, storageIsDefault, serialOwned bool
		var identityKind, generatedKind, def, comment, compression, storage *string
		var stats *int
		if err := rs.Scan(&schema, &table, &name, &dataType, &notNull, &identityKind, &generatedKind, &def, &comment, &stats, &compression, &storage, &storageIsDefault, &serialOwned); err != nil {
			return err
		}
		t, ok := idx[schema+"."+table]
		if !ok {
			continue
		}
		col := &ir.Column{
			Name:                 name,
			Type:                 ir.TypeRef{Name: dataType},
			NotNull:              notNull,
			Comment:              comment,
			Compression:          compression,
			Storage:              storage,
			StorageIsTypeDefault: storageIsDefault,
		}
		switch {
		case identityKind != nil && *identityKind == "a":
			col.Identity = &ir.Identity{Always: true}
		case identityKind != nil && *identityKind == "d":
			col.Identity = &ir.Identity{Always: false}
		case serialOwned && def != nil:
			// dataType is already the real underlying type (format_type()
			// on atttypid never returns "serial" — that's a source-syntax
			// pseudo-type, PostgreSQL's own catalog never stores it), so no
			// Type translation is needed, only picking the Serial marker
			// off the underlying width. Default stays nil, mirroring the
			// Identity branches above — a column whose default is a
			// deptype-'a'-owned-sequence nextval() is DPG's SERIAL shape,
			// never rendered/diffed as an ordinary Default expression.
			if marker, ok := serialMarkerFromWidth(dataType); ok {
				col.Serial = &marker
			} else if def != nil {
				// A hand-rolled owned-sequence-plus-nextval-default on a
				// non-integer column is legal PG but not SERIAL sugar —
				// falls through to plain Default handling, same as before
				// this change.
				stripped := stripStringLiteralCasts(*def)
				col.Default = &stripped
			}
		case generatedKind != nil && *generatedKind == "s" && def != nil:
			col.Generated = &ir.Generated{Expr: *def, Stored: true}
		default:
			if def != nil {
				stripped := stripStringLiteralCasts(*def)
				col.Default = &stripped
			}
		}
		if stats != nil && *stats > 0 {
			col.Statistics = stats
		}
		t.Columns = append(t.Columns, col)
	}
	return rs.Err()
}

// introspectConstraints populates Table.Constraints. See introspectColumns
// for why relkind must include 'p' (partitioned parent) alongside 'r'.
func introspectConstraints(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Table) error {
	const q = `
SELECT n.nspname, c.relname,
       con.conname,
       CASE con.contype
           WHEN 'p' THEN 'PRIMARY KEY' WHEN 'u' THEN 'UNIQUE'
           WHEN 'c' THEN 'CHECK'       WHEN 'f' THEN 'FOREIGN KEY'
           WHEN 'x' THEN 'EXCLUDE'     ELSE con.contype::text
       END AS con_type,
       pg_get_constraintdef(con.oid) AS def,
       NOT con.convalidated AS not_valid,
       con.condeferrable AS deferrable,
       CASE WHEN con.contype IN ('p','u','f') AND array_length(con.conkey, 1) = 1
            THEN (SELECT a.attname FROM pg_attribute a
                  WHERE  a.attrelid = con.conrelid AND a.attnum = con.conkey[1])
            ELSE NULL
       END AS single_col,
       obj_description(con.oid, 'pg_constraint') AS comment
FROM   pg_constraint con
JOIN   pg_class c     ON c.oid = con.conrelid
JOIN   pg_namespace n ON n.oid = c.relnamespace
WHERE  c.relkind IN ('r', 'p', 'f')
AND    con.contype != 'n'
-- conislocal excludes a CHECK constraint present on a child only because it's
-- inherited from a parent under classic INHERITS (PRIMARY KEY/UNIQUE/FOREIGN
-- KEY/EXCLUDE never propagate to children this way, so this only ever
-- filters CHECK rows) — mirrors introspectColumns' attislocal filter above,
-- same reasoning.
AND    con.conislocal
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, c.relname, con.conname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect constraints: %w", err)
	}
	defer rs.Close()

	for rs.Next() {
		var schema, table, name, typ, expr string
		var notValid, deferrable bool
		var singleCol, comment *string
		if err := rs.Scan(&schema, &table, &name, &typ, &expr, &notValid, &deferrable, &singleCol, &comment); err != nil {
			return err
		}
		t, ok := idx[schema+"."+table]
		if !ok {
			continue
		}
		// pg_get_constraintdef bakes a trailing "NOT VALID" into def itself
		// for an unvalidated constraint — strip it so Expr always represents
		// the bare definition regardless of source, matching parseConstraint
		// (blockparser), which already strips the same suffix from
		// hand-written source into this same NotValid field. Without this,
		// a comment-bearing NOT VALID constraint sourced from live
		// introspection rendered "NOT VALID NOT VALID" wherever a caller
		// (e.g. dump) appends NOT VALID itself based on NotValid.
		if notValid {
			expr = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(expr), "NOT VALID"))
		}
		cst := &ir.Constraint{
			Name:       name,
			Type:       typ,
			Expr:       expr,
			NotValid:   notValid,
			Deferrable: deferrable,
			Comment:    comment,
		}
		if singleCol != nil {
			cst.Columns = []string{*singleCol}
		}
		t.Constraints = append(t.Constraints, cst)
	}
	return rs.Err()
}

// introspectIndexes populates Table.Indexes. See introspectColumns for why
// relkind must include 'p' (partitioned parent) alongside 'r' — an index
// created directly on a partitioned table is itself a real pg_index entry
// with indrelid pointing at the relkind='p' parent, not just at its children.
func introspectIndexes(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Table) error {
	const q = `
SELECT n.nspname, c.relname,
       i.relname AS idx_name,
       ix.indisunique,
       am.amname AS method,
       pg_get_indexdef(ix.indexrelid) AS idx_def,
       its.spcname AS tablespace,
       obj_description(i.oid, 'pg_class') AS comment
FROM   pg_index ix
JOIN   pg_class c  ON c.oid = ix.indrelid
JOIN   pg_class i  ON i.oid = ix.indexrelid
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_am am    ON am.oid = i.relam
LEFT   JOIN pg_tablespace its ON its.oid = i.reltablespace
WHERE  c.relkind IN ('r', 'p')
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
AND    NOT EXISTS (
           SELECT 1 FROM pg_constraint con
           WHERE  con.conindid = ix.indexrelid
       )
ORDER  BY n.nspname, c.relname, i.relname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect indexes: %w", err)
	}
	defer rs.Close()

	for rs.Next() {
		var schema, table, name, method, def string
		var unique bool
		var tablespace, comment *string
		if err := rs.Scan(&schema, &table, &name, &unique, &method, &def, &tablespace, &comment); err != nil {
			return err
		}
		t, ok := idx[schema+"."+table]
		if !ok {
			continue
		}
		var where *string
		if i := strings.Index(strings.ToUpper(def), " WHERE "); i >= 0 {
			// Strip PG-added ::typename casts (e.g. from a WHERE clause
			// comparing against a typed column) so this matches hand-written
			// source and doesn't show as spurious drift — same treatment as
			// column defaults below.
			w := stripStringLiteralCasts(strings.TrimSpace(def[i+7:]))
			where = &w
		}
		t.Indexes = append(t.Indexes, &ir.Index{
			Name:             name,
			Unique:           unique,
			Method:           method,
			Columns:          parseIndexDef(def),
			Include:          parseIndexInclude(def),
			NullsNotDistinct: strings.Contains(strings.ToUpper(def), "NULLS NOT DISTINCT"),
			With:             parseIndexWith(def),
			Where:            where,
			Tablespace:       tablespace,
			Comment:          comment,
		})
	}
	return rs.Err()
}

// introspectViewIndexes is introspectIndexes' sibling for a materialized
// view's indexes (RFC §8.2) — real PostgreSQL only supports indexes on a
// materialized view or a table, never a plain view, so this only ever
// matches relkind = 'm'.
func introspectViewIndexes(ctx context.Context, conn pipeline.Querier, views []pipeline.IRObject) error {
	idx := make(map[string]*ir.View, len(views))
	for _, obj := range views {
		if v, ok := obj.(*ir.View); ok && v.Materialized {
			idx[v.Schema+"."+v.Name] = v
		}
	}
	if len(idx) == 0 {
		return nil
	}

	const q = `
SELECT n.nspname, c.relname,
       i.relname AS idx_name,
       ix.indisunique,
       am.amname AS method,
       pg_get_indexdef(ix.indexrelid) AS idx_def,
       its.spcname AS tablespace,
       obj_description(i.oid, 'pg_class') AS comment
FROM   pg_index ix
JOIN   pg_class c  ON c.oid = ix.indrelid
JOIN   pg_class i  ON i.oid = ix.indexrelid
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_am am    ON am.oid = i.relam
LEFT   JOIN pg_tablespace its ON its.oid = i.reltablespace
WHERE  c.relkind = 'm'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, c.relname, i.relname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect view indexes: %w", err)
	}
	defer rs.Close()

	for rs.Next() {
		var schema, view, name, method, def string
		var unique bool
		var tablespace, comment *string
		if err := rs.Scan(&schema, &view, &name, &unique, &method, &def, &tablespace, &comment); err != nil {
			return err
		}
		v, ok := idx[schema+"."+view]
		if !ok {
			continue
		}
		var where *string
		if i := strings.Index(strings.ToUpper(def), " WHERE "); i >= 0 {
			w := stripStringLiteralCasts(strings.TrimSpace(def[i+7:]))
			where = &w
		}
		v.Indexes = append(v.Indexes, &ir.Index{
			Name:             name,
			Unique:           unique,
			Method:           method,
			Columns:          parseIndexDef(def),
			Include:          parseIndexInclude(def),
			NullsNotDistinct: strings.Contains(strings.ToUpper(def), "NULLS NOT DISTINCT"),
			With:             parseIndexWith(def),
			Where:            where,
			Tablespace:       tablespace,
			Comment:          comment,
		})
	}
	return rs.Err()
}

// parseIndexDef extracts the column list from a pg_get_indexdef result.
// Format: CREATE [UNIQUE] INDEX name ON schema.table USING method (col_exprs) [WHERE pred]
func parseIndexDef(def string) []pipeline.IndexColumn {
	upper := strings.ToUpper(def)
	usingIdx := strings.Index(upper, " USING ")
	if usingIdx < 0 {
		return nil
	}
	// Skip method name to find the opening '('
	rest := def[usingIdx+7:]
	parenIdx := strings.IndexByte(rest, '(')
	if parenIdx < 0 {
		return nil
	}
	rest = rest[parenIdx+1:]

	// Find the matching closing ')'
	depth := 1
	end := -1
	for i, ch := range rest {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i
				goto found
			}
		}
	}
	return nil
found:
	return splitIndexColumns(rest[:end])
}

// parseIndexInclude extracts the covering (INCLUDE) column names from a
// pg_get_indexdef string, e.g. "… (a) INCLUDE (b, c) WHERE …" → ["b", "c"].
func parseIndexInclude(def string) []string {
	upper := strings.ToUpper(def)
	i := strings.Index(upper, " INCLUDE (")
	if i < 0 {
		return nil
	}
	rest := def[i+len(" INCLUDE ("):]
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		return nil
	}
	var cols []string
	for _, c := range strings.Split(rest[:end], ",") {
		c = strings.Trim(strings.TrimSpace(c), `"`)
		if c != "" {
			cols = append(cols, c)
		}
	}
	return cols
}

// parseIndexWith extracts WITH (...) storage parameters from a
// pg_get_indexdef string, e.g. "… (a) WITH (fillfactor='70') WHERE …".
func parseIndexWith(def string) []pipeline.StorageParam {
	upper := strings.ToUpper(def)
	i := strings.Index(upper, " WITH (")
	if i < 0 {
		return nil
	}
	rest := def[i+len(" WITH ("):]
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		return nil
	}
	var params []pipeline.StorageParam
	for _, part := range strings.Split(rest[:end], ",") {
		part = strings.TrimSpace(part)
		if kv := strings.SplitN(part, "=", 2); len(kv) == 2 {
			params = append(params, pipeline.StorageParam{
				Key:   strings.TrimSpace(kv[0]),
				Value: strings.TrimSpace(kv[1]),
			})
		}
	}
	return params
}

func splitIndexColumns(s string) []pipeline.IndexColumn {
	var cols []pipeline.IndexColumn
	var cur strings.Builder
	depth := 0
	flush := func() {
		if p := strings.TrimSpace(cur.String()); p != "" {
			cols = append(cols, parseIndexColumn(p))
		}
		cur.Reset()
	}
	for _, ch := range s {
		switch ch {
		case '(':
			depth++
			cur.WriteRune(ch)
		case ')':
			depth--
			cur.WriteRune(ch)
		case ',':
			if depth == 0 {
				flush()
			} else {
				cur.WriteRune(ch)
			}
		default:
			cur.WriteRune(ch)
		}
	}
	flush()
	return cols
}

func parseIndexColumn(s string) pipeline.IndexColumn {
	col := pipeline.IndexColumn{}
	upper := strings.ToUpper(s)

	if strings.HasSuffix(upper, " NULLS LAST") {
		col.Nulls = "LAST"
		s = strings.TrimSpace(s[:len(s)-len(" NULLS LAST")])
		upper = strings.ToUpper(s)
	} else if strings.HasSuffix(upper, " NULLS FIRST") {
		col.Nulls = "FIRST"
		s = strings.TrimSpace(s[:len(s)-len(" NULLS FIRST")])
		upper = strings.ToUpper(s)
	}

	if strings.HasSuffix(upper, " DESC") {
		col.SortOrder = "DESC"
		s = strings.TrimSpace(s[:len(s)-len(" DESC")])
	} else if strings.HasSuffix(upper, " ASC") {
		col.SortOrder = "ASC"
		s = strings.TrimSpace(s[:len(s)-len(" ASC")])
	}

	if strings.ContainsRune(s, '(') {
		// Strip PG-added ::typename casts (e.g. to_tsvector('english'::regconfig, e)
		// vs hand-written to_tsvector('english', e)) so an unchanged expression
		// index doesn't show as spurious drift against a live catalog.
		col.Expr = &pipeline.RawExpr{Text: stripStringLiteralCasts(s)}
	} else {
		col.Name = strings.Trim(s, `"`)
	}
	return col
}

func introspectPolicies(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Table) error {
	const q = `
SELECT n.nspname, c.relname,
       p.polname,
       CASE p.polcmd WHEN 'r' THEN 'SELECT' WHEN 'a' THEN 'INSERT'
                     WHEN 'w' THEN 'UPDATE'  WHEN 'd' THEN 'DELETE'
                     ELSE 'ALL' END AS cmd,
       p.polpermissive,
       pg_get_expr(p.polqual, p.polrelid) AS using_expr,
       pg_get_expr(p.polwithcheck, p.polrelid) AS check_expr,
       obj_description(p.oid, 'pg_policy') AS comment,
       (SELECT array_agg(COALESCE(pr.rolname, 'PUBLIC') ORDER BY COALESCE(pr.rolname, 'PUBLIC'))
          FROM unnest(p.polroles) AS role_oid
          LEFT JOIN pg_roles pr ON pr.oid = role_oid) AS roles
FROM   pg_policy p
JOIN   pg_class c     ON c.oid = p.polrelid
JOIN   pg_namespace n ON n.oid = c.relnamespace
WHERE  n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, c.relname, p.polname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect policies: %w", err)
	}
	defer rs.Close()

	for rs.Next() {
		var schema, table, name, cmd string
		var permissive bool
		var using, check, comment *string
		var roles []string
		if err := rs.Scan(&schema, &table, &name, &cmd, &permissive, &using, &check, &comment, &roles); err != nil {
			return err
		}
		t, ok := idx[schema+"."+table]
		if !ok {
			continue
		}
		t.Policies = append(t.Policies, &ir.Policy{
			Name:       name,
			Command:    cmd,
			Permissive: permissive,
			Using:      using,
			WithCheck:  check,
			Roles:      roles,
			Comment:    comment,
		})
	}
	return rs.Err()
}

// introspectTriggers populates Table.Triggers. The events column uses
// array_to_string (which skips NULL elements) rather than string
// concatenation with a hardcoded " OR " prefix on every event after the
// first bit position — that unconditional prefix left a dangling "OR " on
// any trigger whose ONLY event isn't INSERT (e.g. an UPDATE-only trigger
// introspected as "OR UPDATE" instead of "UPDATE"), which never round-
// tripped back to the same event list.
func introspectTriggers(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Table) error {
	const q = `
SELECT n.nspname, c.relname,
       t.tgname,
       CASE WHEN (t.tgtype & 2) != 0 THEN 'BEFORE' ELSE 'AFTER' END AS when,
       CASE WHEN (t.tgtype & 1) != 0 THEN 'ROW' ELSE 'STATEMENT' END AS for_each,
       array_to_string(ARRAY[
           CASE WHEN (t.tgtype & 4)  != 0 THEN 'INSERT' END,
           CASE WHEN (t.tgtype & 8)  != 0 THEN 'DELETE' END,
           CASE WHEN (t.tgtype & 16) != 0 THEN 'UPDATE' END,
           CASE WHEN (t.tgtype & 32) != 0 THEN 'TRUNCATE' END
       ], ' OR ') AS events,
       p.proname AS func_name,
       pn.nspname AS func_schema,
       pg_get_triggerdef(t.oid, true) AS triggerdef,
       obj_description(t.oid, 'pg_trigger') AS comment,
       -- RFC §7.9 (audit item #2): pg_trigger.tgoldtable/tgnewtable hold
       -- the REFERENCING OLD/NEW TABLE AS transition-table names directly
       -- (NULL when REFERENCING wasn't used for that side) — no deparse
       -- needed, unlike the WHEN condition below.
       t.tgoldtable AS old_transition_name,
       t.tgnewtable AS new_transition_name,
       -- RFC audit item #1: pg_trigger.tgattr is the "UPDATE OF col, ..."
       -- column list as an int2vector of attnums (empty when the trigger
       -- has no OF clause at all) — unnest WITH ORDINALITY preserves the
       -- declared column order.
       (SELECT array_agg(a.attname ORDER BY u.ord)
          FROM unnest(t.tgattr) WITH ORDINALITY AS u(attnum, ord)
          JOIN pg_attribute a ON a.attrelid = t.tgrelid AND a.attnum = u.attnum
       ) AS update_of_columns,
       -- Section 9.1's [NO] DEPENDS ON EXTENSION, reused for triggers
       -- (Section 7.9, audit item #75) — deptype='x' is PostgreSQL's own
       -- auto-drop-dependency-on-an-extension marker, confirmed live via
       -- the identical mechanism introspectFunctionDependsOnExtension
       -- already uses for Function/Procedure.
       (SELECT array_agg(e.extname ORDER BY e.extname)
          FROM pg_depend d
          JOIN pg_extension e ON e.oid = d.refobjid
          WHERE d.classid = 'pg_trigger'::regclass AND d.objid = t.oid AND d.deptype = 'x'
       ) AS depends_on_extensions,
       -- trigger-enable-state (Section 7.9, audit item #56): tgenabled
       -- 'O' (origin, the default) means ENABLED; 'D' DISABLED; 'R'
       -- ENABLE REPLICA; 'A' ENABLE ALWAYS.
       t.tgenabled::text
FROM   pg_trigger t
JOIN   pg_class c     ON c.oid = t.tgrelid
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_proc p      ON p.oid = t.tgfoid
JOIN   pg_namespace pn ON pn.oid = p.pronamespace
WHERE  NOT t.tgisinternal
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, c.relname, t.tgname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect triggers: %w", err)
	}
	defer rs.Close()

	for rs.Next() {
		var schema, table, name, when, forEach, events, funcName, funcSchema, triggerDef, tgenabled string
		var comment *string
		var updateOfColumns, dependsOnExtensions []string
		var oldTransitionName, newTransitionName *string
		if err := rs.Scan(&schema, &table, &name, &when, &forEach, &events, &funcName, &funcSchema, &triggerDef, &comment, &oldTransitionName, &newTransitionName, &updateOfColumns, &dependsOnExtensions, &tgenabled); err != nil {
			return err
		}
		var enableState string
		switch tgenabled {
		case "D":
			enableState = "DISABLED"
		case "R":
			enableState = "ENABLE REPLICA"
		case "A":
			enableState = "ENABLE ALWAYS"
		}
		condition := extractTriggerWhenCondition(triggerDef)
		t, ok := idx[schema+"."+table]
		if !ok {
			continue
		}
		fn := funcSchema + "." + funcName
		// Parse events string: remove leading " OR " and split
		rawEvents := strings.TrimSpace(events)
		var cleanEvents []string
		for part := range strings.SplitSeq(rawEvents, " OR ") {
			part = strings.TrimSpace(part)
			if part != "" {
				cleanEvents = append(cleanEvents, part)
			}
		}
		t.Triggers = append(t.Triggers, &ir.Trigger{
			Name:              name,
			When:              when,
			Events:            cleanEvents,
			ForEach:           forEach,
			UpdateOfColumns:   updateOfColumns,
			OldTransitionName: oldTransitionName,
			NewTransitionName: newTransitionName,
			Function:            fn,
			Condition:           condition,
			Comment:             comment,
			EnableState:         enableState,
			DependsOnExtensions: dependsOnExtensions,
		})
	}
	return rs.Err()
}

// extractTriggerWhenCondition pulls a trigger's WHEN (...) condition out of
// pg_get_triggerdef's full deparsed CREATE TRIGGER text. There is no direct
// SQL-callable equivalent of pg_get_expr for a trigger qual: tgqual's Var
// nodes resolve against the trigger's implicit OLD/NEW pseudo-relations, and
// plain pg_get_expr(tgqual, tgrelid) errors with "expression contains
// variables of more than one relation" for any condition that references
// NEW or OLD (confirmed live — the common case for an UPDATE trigger's WHEN
// clause). pg_get_triggerdef deparses the whole statement correctly because
// it builds the OLD/NEW range-table context internally; this walks its
// output the same way introspectConstraints already relies on
// pg_get_constraintdef's text for a value the catalog doesn't expose more
// directly.
func extractTriggerWhenCondition(def string) *string {
	const marker = " WHEN ("
	idx := strings.Index(def, marker)
	if idx == -1 {
		return nil
	}
	start := idx + len(marker) - 1 // index of the opening '('
	depth := 0
	for i := start; i < len(def); i++ {
		switch def[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				cond := strings.TrimSpace(def[start+1 : i])
				return &cond
			}
		}
	}
	return nil
}

// ── views ─────────────────────────────────────────────────────────────────────

func introspectViews(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT n.nspname, c.relname,
       r.rolname AS owner,
       pg_get_viewdef(c.oid, true) AS query,
       obj_description(c.oid, 'pg_class') AS comment,
       c.relkind = 'm' AS materialized,
       NOT c.relispopulated AS with_no_data
FROM   pg_class c
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_roles r     ON r.oid = c.relowner
WHERE  c.relkind IN ('v', 'm')
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
AND    NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
ORDER  BY n.nspname, c.relname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect views: %w", err)
	}
	defer rs.Close()

	viewIdx := make(map[string]*ir.View)
	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, owner, query string
		var comment *string
		var materialized, withNoData bool
		if err := rs.Scan(&schema, &name, &owner, &query, &comment, &materialized, &withNoData); err != nil {
			return nil, err
		}
		q := normalizeViewQuery(query)
		v := &ir.View{
			Schema:       schema,
			Name:         name,
			Owner:        &owner,
			Query:        q,
			Comment:      comment,
			Materialized: materialized,
			Recursive:    strings.HasPrefix(q, "WITH RECURSIVE"),
			WithNoData:   materialized && withNoData,
		}
		viewIdx[schema+"."+name] = v
		out = append(out, v)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	rs.Close()

	if err := introspectViewSecurityLabels(ctx, conn, viewIdx); err != nil {
		return nil, err
	}
	if err := introspectViewGrants(ctx, conn, viewIdx); err != nil {
		return nil, err
	}
	return out, nil
}

// ── functions ─────────────────────────────────────────────────────────────────

func introspectFunctions(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT n.nspname, p.proname,
       p.oid::bigint AS oid,
       pg_get_function_identity_arguments(p.oid) AS args,
       pg_catalog.oidvectortypes(p.proargtypes)   AS arg_types,
       pg_catalog.format_type(p.prorettype, NULL) AS return_type,
       l.lanname AS language,
       CASE p.provolatile
           WHEN 'i' THEN 'IMMUTABLE'
           WHEN 's' THEN 'STABLE'
           ELSE 'VOLATILE'
       END,
       p.prosecdef,
       p.proisstrict,
       CASE p.proparallel
           WHEN 'r' THEN 'RESTRICTED'
           WHEN 's' THEN 'SAFE'
           ELSE 'UNSAFE'
       END,
       p.procost,
       p.prorows,
       p.proretset,
       l.lanname IN ('c', 'internal') AS is_c_or_internal,
       obj_description(p.oid, 'pg_proc') AS comment,
       p.prokind::text,
       p.prosrc
FROM   pg_proc p
JOIN   pg_namespace n ON n.oid = p.pronamespace
JOIN   pg_language  l ON l.oid = p.prolang
WHERE  p.prokind IN ('f', 'p')
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
AND    NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
ORDER  BY n.nspname, p.proname, args`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect functions: %w", err)
	}
	defer rs.Close()

	funcIdx := make(map[string]*ir.Function)
	procIdx := make(map[string]*ir.Procedure)
	fnByOID := make(map[int64]*ir.Function)
	procByOID := make(map[int64]*ir.Procedure)
	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, args, argTypes, retType, lang, volatility string
		var oid int64
		var secDef, strict bool
		var parallel string
		var cost, rows float64
		var retset, isCOrInternal bool
		var comment *string
		var prokind, prosrc string
		if err := rs.Scan(&schema, &name, &oid, &args, &argTypes, &retType, &lang, &volatility, &secDef, &strict,
			&parallel, &cost, &rows, &retset, &isCOrInternal, &comment, &prokind, &prosrc); err != nil {
			return nil, err
		}
		if prokind == "p" {
			proc := &ir.Procedure{
				Schema:  schema,
				Name:    name,
				Comment: comment,
				Attrs: ir.FuncAttrs{
					Language: lang,
					Body:     prosrc,
				},
			}
			// Use argTypes (type-only, from oidvectortypes) to build Args so that
			// the QualifiedName matches argsKey() in the IR builder (which also uses
			// type-only). Keep args (full identity args with parameter names) for the
			// grants index key, which mirrors pg_get_function_identity_arguments.
			if argTypes != "" {
				for a := range strings.SplitSeq(argTypes, ", ") {
					proc.Args = append(proc.Args, ir.FuncArg{Type: ir.TypeRef{Name: strings.TrimSpace(a)}})
				}
			}
			procIdx[schema+"."+name+"("+args+")"] = proc
			procByOID[oid] = proc
			out = append(out, proc)
		} else {
			// procost/prorows are NOT NULL in pg_proc — every function has a
			// concrete value, PG's own default among them (1 for C/internal
			// language, 100 otherwise for cost; 0 for a scalar function, 1000
			// for a set-returning one, for rows). Only surface a value when
			// it genuinely differs from that computed default — otherwise
			// every ordinary function would render a noisy "COST 100" on
			// dump, the same suppress-when-default problem already solved
			// for column STORAGE (ir.Column.StorageIsTypeDefault).
			defaultCost := 100.0
			if isCOrInternal {
				defaultCost = 1
			}
			defaultRows := 0.0
			if retset {
				defaultRows = 1000
			}
			attrs := ir.FuncAttrs{
				Language:    lang,
				Volatility:  volatility,
				SecurityDef: secDef,
				Strict:      strict,
				Parallel:    parallel,
				Body:        prosrc,
			}
			if cost != defaultCost {
				c := cost
				attrs.Cost = &c
			}
			if rows != defaultRows {
				r := rows
				attrs.Rows = &r
			}
			fn := &ir.Function{
				Schema:     schema,
				Name:       name,
				ReturnType: ir.TypeRef{Name: retType, SetOf: retset},
				Comment:    comment,
				Attrs:      attrs,
			}
			// Use argTypes (type-only) so QualifiedName matches argsKey() in IR builder.
			// Keep args (with parameter names) for the grants index key only.
			if argTypes != "" {
				for a := range strings.SplitSeq(argTypes, ", ") {
					fn.Args = append(fn.Args, ir.FuncArg{Type: ir.TypeRef{Name: strings.TrimSpace(a)}})
				}
			}
			funcIdx[schema+"."+name+"("+args+")"] = fn
			fnByOID[oid] = fn
			out = append(out, fn)
		}
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	rs.Close()

	if err := introspectFunctionArgs(ctx, conn, fnByOID, procByOID); err != nil {
		return nil, err
	}

	// Body hashes are computed here, after argument names/modes are final,
	// not inline in the row-scan loop above: ir.HashFunctionBody's plpgsql
	// canonicalisation compiles a reconstructed CREATE FUNCTION/PROCEDURE
	// shim through the real PL/pgSQL compiler, which resolves the
	// function's own parameter names against its declared argument list
	// (e.g. "a := a + 1"). A shim built before introspectFunctionArgs has
	// run would have empty/wrong names for the plain-IN-only common case,
	// so the plpgsql compile would frequently fail and silently fall back
	// to raw hashing — defeating the fix for exactly the case it exists
	// for.
	for _, fn := range funcIdx {
		fn.BodyHash = ir.HashFunctionBody(fn.Attrs.Language, fn.Attrs.Body, ir.RenderCreateFunctionSQL(fn))
	}
	for _, proc := range procIdx {
		proc.BodyHash = ir.HashFunctionBody(proc.Attrs.Language, proc.Attrs.Body, ir.RenderCreateProcedureSQL(proc))
	}

	if err := introspectFunctionSecurityLabels(ctx, conn, funcIdx); err != nil {
		return nil, err
	}
	if err := introspectProcedureSecurityLabels(ctx, conn, procIdx); err != nil {
		return nil, err
	}
	if err := introspectFunctionGrants(ctx, conn, funcIdx); err != nil {
		return nil, err
	}
	if err := introspectProcedureGrants(ctx, conn, procIdx); err != nil {
		return nil, err
	}
	if err := introspectFunctionDependsOnExtension(ctx, conn, fnByOID, procByOID); err != nil {
		return nil, err
	}
	return out, nil
}

// introspectFunctionDependsOnExtension populates DependsOnExtensions
// (Section 9.1) for every function/procedure from pg_depend's deptype='x'
// rows — PostgreSQL's own auto-drop-dependency-on-an-extension marker,
// confirmed live (distinct from deptype='e', true extension membership,
// which introspectFunctions' own WHERE clause already excludes entirely).
func introspectFunctionDependsOnExtension(ctx context.Context, conn pipeline.Querier, fnByOID map[int64]*ir.Function, procByOID map[int64]*ir.Procedure) error {
	const q = `
SELECT d.objid::bigint, e.extname
FROM   pg_depend d
JOIN   pg_extension e ON e.oid = d.refobjid
WHERE  d.classid = 'pg_proc'::regclass
AND    d.deptype = 'x'
ORDER  BY d.objid, e.extname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect function DEPENDS ON EXTENSION: %w", err)
	}
	defer rs.Close()

	for rs.Next() {
		var oid int64
		var ext string
		if err := rs.Scan(&oid, &ext); err != nil {
			return err
		}
		if fn, ok := fnByOID[oid]; ok {
			fn.DependsOnExtensions = append(fn.DependsOnExtensions, ext)
		}
		if proc, ok := procByOID[oid]; ok {
			proc.DependsOnExtensions = append(proc.DependsOnExtensions, ext)
		}
	}
	return rs.Err()
}

// introspectFunctionArgs fixes up Args (name, mode, type) for every function
// AND procedure, superseding the type-only Args the main introspectFunctions
// query builds from oidvectortypes(proargtypes)/oidvectortypes(proargtypes)
// (function/procedure branches respectively) — which, like
// pg_get_function_identity_arguments, only ever reports IN/INOUT/VARIADIC
// argument TYPES, with no name or mode information at all, and never
// touches procedures' Args in any capacity.
//
// Before this fix, only functions with at least one non-plain-IN parameter
// (proargmodes IS NOT NULL) got real names/modes, via a narrower predecessor
// of this function; the overwhelming common case — plain IN-only functions,
// and every procedure regardless of its argument modes — had empty Name on
// every arg. That meant: a plain function's dumped/rendered signature always
// omitted parameter names (e.g. "add_ints(integer, integer)" instead of
// "add_ints(a integer, b integer)"), and — the reason this function was
// generalized rather than left as-is — HashFunctionBody's plpgsql
// canonicalisation needs an argument-accurate CREATE FUNCTION/PROCEDURE shim
// to feed the real PL/pgSQL compiler, which resolves the body's own
// parameter references (e.g. "a := a + 1") against the declared argument
// list at compile time; a shim with empty names would fail to compile for
// exactly the common case this whole feature targets.
//
// Uses generate_subscripts over COALESCE(proallargtypes, proargtypes::oid[])
// so it naturally covers both the "has a non-default mode" case
// (proallargtypes/proargmodes populated) and the plain-IN-only case
// (proallargtypes NULL, proargmodes NULL — COALESCE(proargmodes[i], 'i')
// supplies the default mode per PostgreSQL's own documented invariant that
// proargmodes, when non-NULL, has exactly the same length as
// proallargtypes). A function/procedure with zero arguments naturally
// produces zero subscript rows, leaving Args nil, same as before.
func introspectFunctionArgs(ctx context.Context, conn pipeline.Querier, fnByOID map[int64]*ir.Function, procByOID map[int64]*ir.Procedure) error {
	// alltypes is computed via a LATERAL subquery rather than inline
	// COALESCE(proallargtypes, proargtypes::oid[]) repeated at every use
	// site: proargtypes is an oidvector, PostgreSQL's special 0-based-lower-
	// bound array type (unlike every ordinary 1-based array), and a bare
	// ::oid[] cast preserves that 0-based numbering rather than
	// renormalizing it — confirmed live, this silently misaligned every
	// plain-IN-only function's argument names by one position before this
	// fix (name[i] read against a 0-based type array's index i landed on
	// the wrong argument). ARRAY(SELECT unnest(...)) is the standard
	// PostgreSQL idiom to renumber a 0-based array to an ordinary 1-based
	// one, matching proargnames/proargmodes's native 1-based indexing.
	const q = `
SELECT p.oid::bigint, p.prokind::text,
       COALESCE(p.proargmodes[i], 'i')::text AS mode,
       p.proargnames[i] AS name,
       pg_catalog.format_type(t.alltypes[i], NULL) AS type
FROM   pg_proc p
JOIN   pg_namespace n ON n.oid = p.pronamespace,
       LATERAL (SELECT COALESCE(p.proallargtypes, ARRAY(SELECT unnest(p.proargtypes))) AS alltypes) t,
       LATERAL generate_subscripts(t.alltypes, 1) AS i
WHERE  p.prokind IN ('f','p')
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY p.oid, i`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect function/procedure args: %w", err)
	}
	defer rs.Close()

	argsByOID := make(map[int64][]ir.FuncArg)
	kindByOID := make(map[int64]string)
	var order []int64
	for rs.Next() {
		var oid int64
		var prokind, mode, typ string
		var name *string
		if err := rs.Scan(&oid, &prokind, &mode, &name, &typ); err != nil {
			return err
		}
		if _, ok := argsByOID[oid]; !ok {
			order = append(order, oid)
			kindByOID[oid] = prokind
		}
		argName := ""
		if name != nil {
			argName = *name
		}
		argMode := "IN"
		switch mode {
		case "o":
			argMode = "OUT"
		case "b":
			argMode = "INOUT"
		case "v":
			argMode = "VARIADIC"
		case "t":
			argMode = "TABLE"
		}
		argsByOID[oid] = append(argsByOID[oid], ir.FuncArg{Name: argName, Mode: argMode, Type: ir.TypeRef{Name: typ}})
	}
	if err := rs.Err(); err != nil {
		return err
	}

	for _, oid := range order {
		if kindByOID[oid] == "p" {
			if proc, ok := procByOID[oid]; ok {
				proc.Args = argsByOID[oid]
			}
			continue
		}
		if fn, ok := fnByOID[oid]; ok {
			fn.Args = argsByOID[oid]
		}
	}
	return nil
}

// ── types ─────────────────────────────────────────────────────────────────────

func introspectTypes(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT n.nspname, t.typname,
       CASE t.typtype
           WHEN 'e' THEN 'ENUM'      WHEN 'c' THEN 'COMPOSITE'
           WHEN 'r' THEN 'RANGE'     WHEN 'd' THEN 'DOMAIN'
           WHEN 'b' THEN 'BASE'      ELSE 'UNKNOWN'
       END AS variant,
       obj_description(t.oid, 'pg_type') AS comment,
       pg_get_userbyid(t.typowner) AS owner
FROM   pg_type t
JOIN   pg_namespace n ON n.oid = t.typnamespace
WHERE  t.typtype IN ('e','c','r','d','b')
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
AND    (t.typtype != 'c' OR NOT EXISTS (
    SELECT 1 FROM pg_class c WHERE c.oid = t.typrelid AND c.relkind != 'c'
))
-- PostgreSQL auto-creates an array shadow type (typcategory 'A', e.g.
-- "_mytype") for every base type — pg_type.typarray on the ELEMENT type
-- points forward to it, the same canonical test psql/format_type use, so
-- exclude any type that's the target of some other type's typarray.
AND    NOT EXISTS (SELECT 1 FROM pg_type base WHERE base.typarray = t.oid)
AND    NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_type'::regclass AND d.objid = t.oid AND d.deptype = 'e')
ORDER  BY n.nspname, t.typname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect types: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, variant, owner string
		var comment *string
		if err := rs.Scan(&schema, &name, &variant, &comment, &owner); err != nil {
			return nil, err
		}
		out = append(out, &ir.Type{Schema: schema, Name: name, Variant: variant, Comment: comment, Owner: &owner})
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	rs.Close()

	if err := introspectEnumValues(ctx, conn, out); err != nil {
		return nil, err
	}
	if err := introspectDomainBodies(ctx, conn, out); err != nil {
		return nil, err
	}
	if err := introspectCompositeAttrs(ctx, conn, out); err != nil {
		return nil, err
	}
	if err := introspectRangeBodies(ctx, conn, out); err != nil {
		return nil, err
	}
	if err := introspectBaseBody(ctx, conn, out); err != nil {
		return nil, err
	}
	if err := introspectTypeSecurityLabels(ctx, conn, out); err != nil {
		return nil, err
	}
	if err := introspectTypeGrants(ctx, conn, out); err != nil {
		return nil, err
	}
	return out, nil
}

// introspectTypeGrants populates Type.Grants for every type in types using
// aclexplode on pg_type.typacl (RFC audit item #3 — uniform across all 5
// variants, including DOMAIN: real PostgreSQL grants a domain exactly like
// any other type, via pg_type.typacl, confirmed live).
func introspectTypeGrants(ctx context.Context, conn pipeline.Querier, types []pipeline.IRObject) error {
	idx := make(map[string]*ir.Type, len(types))
	for _, obj := range types {
		t := obj.(*ir.Type)
		idx[t.Schema+"."+t.Name] = t
	}

	const q = `
SELECT n.nspname, t.typname,
       CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END AS grantee,
       a.privilege_type, a.is_grantable
FROM   pg_type t
JOIN   pg_namespace n ON n.oid = t.typnamespace,
       LATERAL aclexplode(t.typacl) a
WHERE  t.typtype IN ('e','c','r','d','b')
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, t.typname, grantee, a.privilege_type`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect type grants: %w", err)
	}
	defer rs.Close()

	type grantKey struct{ schema, name, grantee string }
	type grantEntry struct {
		privs     []string
		grantable bool
	}
	grants := make(map[grantKey]*grantEntry)
	var order []grantKey

	for rs.Next() {
		var schema, name, grantee, priv string
		var grantable bool
		if err := rs.Scan(&schema, &name, &grantee, &priv, &grantable); err != nil {
			return err
		}
		k := grantKey{schema, name, grantee}
		e, ok := grants[k]
		if !ok {
			e = &grantEntry{}
			grants[k] = e
			order = append(order, k)
		}
		e.privs = append(e.privs, priv)
		if grantable {
			e.grantable = true
		}
	}
	if err := rs.Err(); err != nil {
		return err
	}

	for _, k := range order {
		t, ok := idx[k.schema+"."+k.name]
		if !ok {
			continue
		}
		e := grants[k]
		t.Grants = append(t.Grants, ir.Grant{
			Privileges: e.privs,
			Roles:      []string{k.grantee},
			WithGrant:  e.grantable,
		})
	}
	return nil
}

func introspectCompositeAttrs(ctx context.Context, conn pipeline.Querier, types []pipeline.IRObject) error {
	const q = `
SELECT n.nspname, t.typname,
       a.attname,
       pg_catalog.format_type(a.atttypid, a.atttypmod) AS attr_type
FROM   pg_type t
JOIN   pg_namespace n   ON n.oid = t.typnamespace
JOIN   pg_class c       ON c.oid = t.typrelid
JOIN   pg_attribute a   ON a.attrelid = c.oid
WHERE  t.typtype = 'c'
AND    c.relkind = 'c'
AND    a.attnum > 0
AND    NOT a.attisdropped
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, t.typname, a.attnum`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect composite attrs: %w", err)
	}
	defer rs.Close()

	typeIdx := map[string]*ir.Type{}
	for _, obj := range types {
		if t, ok := obj.(*ir.Type); ok && t.Variant == "COMPOSITE" {
			typeIdx[t.Schema+"."+t.Name] = t
		}
	}
	for rs.Next() {
		var schema, name, attrName, attrType string
		if err := rs.Scan(&schema, &name, &attrName, &attrType); err != nil {
			return err
		}
		if t, ok := typeIdx[schema+"."+name]; ok {
			t.CompositeAttrs = append(t.CompositeAttrs, &ir.Column{
				Name: attrName,
				Type: ir.TypeRef{Name: attrType},
			})
		}
	}
	return rs.Err()
}

// introspectDomainBodies populates each DOMAIN's structured RFC §5.4
// diffing inputs (base type, DEFAULT, NOT NULL, CHECK constraints) —
// previously all four were concatenated into a single opaque Body string
// ("basetype NOT NULL DEFAULT expr CONSTRAINT c CHECK (...) ...", the same
// shape as RANGE/BASE's genuinely-opaque bodies), even though a domain is
// NOT opaque like those two: diffType now diffs each property individually
// (see differ.go), so it needs them as separate fields, not one blob to
// hash-compare. pg_type.typdefault is already plain SQL text (unlike a
// column default, which needs pg_get_expr on a stored node tree), so no
// deparsing is needed for it.
func introspectDomainBodies(ctx context.Context, conn pipeline.Querier, types []pipeline.IRObject) error {
	const q = `
SELECT n.nspname, t.typname,
       pg_catalog.format_type(t.typbasetype, t.typtypmod) AS base_type,
       t.typnotnull,
       t.typdefault
FROM   pg_type t
JOIN   pg_namespace n ON n.oid = t.typnamespace
WHERE  t.typtype = 'd'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, t.typname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect domain bodies: %w", err)
	}
	defer rs.Close()

	domainIdx := map[string]*ir.Type{}
	for _, obj := range types {
		if t, ok := obj.(*ir.Type); ok && t.Variant == "DOMAIN" {
			domainIdx[t.Schema+"."+t.Name] = t
		}
	}

	for rs.Next() {
		var schema, name, baseType string
		var notNull bool
		var defaultVal *string
		if err := rs.Scan(&schema, &name, &baseType, &notNull, &defaultVal); err != nil {
			return err
		}
		t, ok := domainIdx[schema+"."+name]
		if !ok {
			continue
		}
		t.DomainBaseType = ir.TypeRef{Name: baseType}
		t.DomainNotNull = notNull
		t.DomainDefault = defaultVal
	}
	if err := rs.Err(); err != nil {
		return err
	}
	return introspectDomainConstraints(ctx, conn, domainIdx)
}

// introspectDomainConstraints populates DomainConstraints for every domain
// in idx from pg_constraint — a separate query (rather than introspectDomain
// Bodies' single-row string_agg this replaced) because each domain can have
// any number of named CHECK constraints, needing the same one-row-per-entry
// shape introspectEnumValues already uses for ENUM values.
func introspectDomainConstraints(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Type) error {
	if len(idx) == 0 {
		return nil
	}
	const q = `
SELECT n.nspname, t.typname, con.conname, pg_get_constraintdef(con.oid)
FROM   pg_constraint con
JOIN   pg_type t      ON t.oid = con.contypid
JOIN   pg_namespace n ON n.oid = t.typnamespace
WHERE  con.contype = 'c'
AND    t.typtype = 'd'
ORDER  BY n.nspname, t.typname, con.conname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect domain constraints: %w", err)
	}
	defer rs.Close()

	for rs.Next() {
		var schema, name, conname, condef string
		if err := rs.Scan(&schema, &name, &conname, &condef); err != nil {
			return err
		}
		t, ok := idx[schema+"."+name]
		if !ok {
			continue
		}
		t.DomainConstraints = append(t.DomainConstraints, &ir.Constraint{
			Name: conname,
			Type: "CHECK",
			Expr: condef,
		})
	}
	return rs.Err()
}

// introspectRangeBodies reads each RANGE type's pg_range row and reconstructs
// its DPG-source options text into Body — the same "trailing-clause-only"
// shape introspectDomainBodies already establishes (no "CREATE TYPE name AS
// RANGE" prefix; dump.go's renderer supplies that). Found live-testing a
// demo project: RANGE types had NO introspection at all before this — not
// even a partial/wrong reconstruction, nothing whatsoever selected SUBTYPE
// or any other pg_range column, so `dpg dump` could only emit
// "-- type X (RANGE) omitted" for every range type in the database, despite
// the RFC listing Range types as "Declared, Diffed" with no caveat that
// introspection was incomplete.
//
// SUBTYPE_OPCLASS is only rendered when it differs from the subtype's own
// default btree opclass — same "suppress when it matches the type's default"
// discipline already used for column STORAGE (§6m) — otherwise nearly every
// range type would render a redundant SUBTYPE_OPCLASS clause. COLLATION,
// CANONICAL, and SUBTYPE_DIFF are only rendered when actually set (PG
// reports 0/no-collation for the common case of a non-collatable subtype
// like numeric or a range with no custom CANONICAL/SUBTYPE_DIFF function).
func introspectRangeBodies(ctx context.Context, conn pipeline.Querier, types []pipeline.IRObject) error {
	const q = `
WITH btree_am AS (SELECT oid FROM pg_am WHERE amname = 'btree')
SELECT n.nspname, t.typname,
       pg_catalog.format_type(r.rngsubtype, NULL) AS subtype,
       CASE WHEN dflt.oid IS NULL OR dflt.oid <> r.rngsubopc
            THEN quote_ident(opc.opcname) END AS subtype_opclass,
       CASE WHEN r.rngcollation <> 0 THEN quote_ident(coll.collname) END AS collation,
       CASE WHEN r.rngcanonical <> 0 THEN quote_ident(canfn.proname) END AS canonical,
       CASE WHEN r.rngsubdiff <> 0 THEN quote_ident(difffn.proname) END AS subtype_diff
FROM   pg_range r
JOIN   pg_type t          ON t.oid = r.rngtypid
JOIN   pg_namespace n     ON n.oid = t.typnamespace
LEFT JOIN pg_opclass opc  ON opc.oid = r.rngsubopc
LEFT JOIN pg_opclass dflt ON dflt.opcintype = r.rngsubtype
                          AND dflt.opcmethod = (SELECT oid FROM btree_am)
                          AND dflt.opcdefault
LEFT JOIN pg_collation coll ON coll.oid = r.rngcollation
LEFT JOIN pg_proc canfn     ON canfn.oid = r.rngcanonical
LEFT JOIN pg_proc difffn    ON difffn.oid = r.rngsubdiff
WHERE  n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, t.typname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect range bodies: %w", err)
	}
	defer rs.Close()

	rangeIdx := map[string]*ir.Type{}
	for _, obj := range types {
		if t, ok := obj.(*ir.Type); ok && t.Variant == "RANGE" {
			rangeIdx[t.Schema+"."+t.Name] = t
		}
	}

	for rs.Next() {
		var schema, name, subtype string
		var opclass, collation, canonical, subtypeDiff *string
		if err := rs.Scan(&schema, &name, &subtype, &opclass, &collation, &canonical, &subtypeDiff); err != nil {
			return err
		}
		t, ok := rangeIdx[schema+"."+name]
		if !ok {
			continue
		}
		parts := []string{"SUBTYPE = " + subtype}
		if opclass != nil {
			parts = append(parts, "SUBTYPE_OPCLASS = "+*opclass)
		}
		if collation != nil {
			parts = append(parts, "COLLATION = "+*collation)
		}
		if canonical != nil {
			parts = append(parts, "CANONICAL = "+*canonical)
		}
		if subtypeDiff != nil {
			parts = append(parts, "SUBTYPE_DIFF = "+*subtypeDiff)
		}
		t.Body = strings.Join(parts, ", ")
		t.Reconstructed = true
	}
	return rs.Err()
}

// introspectBaseBody reconstructs a BASE type's CREATE TYPE options list
// (RFC §5.5: "INPUT = func, OUTPUT = func, ...") from pg_type — the same
// "reconstruct from catalog" pattern as introspectRangeBodies above. Diffing
// stays hash-only (RFC §5.5: any change is DESTRUCTIVE), so completeness
// here only affects how faithfully a dumped base type round-trips, not
// diffing behavior.
func introspectBaseBody(ctx context.Context, conn pipeline.Querier, types []pipeline.IRObject) error {
	const q = `
SELECT n.nspname, t.typname,
       infn.proname, outfn.proname,
       recvfn.proname, sendfn.proname,
       modinfn.proname, modoutfn.proname,
       anlfn.proname, subfn.proname,
       t.typlen, t.typbyval, t.typalign::text, t.typstorage::text,
       t.typcategory::text, t.typispreferred, t.typdelim::text,
       elem.typname AS element, t.typcollation <> 0 AS collatable
FROM   pg_type t
JOIN   pg_namespace n ON n.oid = t.typnamespace
JOIN   pg_proc infn   ON infn.oid = t.typinput
JOIN   pg_proc outfn  ON outfn.oid = t.typoutput
LEFT   JOIN pg_proc recvfn   ON recvfn.oid = t.typreceive
LEFT   JOIN pg_proc sendfn   ON sendfn.oid = t.typsend
LEFT   JOIN pg_proc modinfn  ON modinfn.oid = t.typmodin
LEFT   JOIN pg_proc modoutfn ON modoutfn.oid = t.typmodout
LEFT   JOIN pg_proc anlfn    ON anlfn.oid = t.typanalyze
LEFT   JOIN pg_proc subfn    ON subfn.oid = t.typsubscript
LEFT   JOIN pg_type elem     ON elem.oid = NULLIF(t.typelem, 0)
WHERE  t.typtype = 'b'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, t.typname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect base type bodies: %w", err)
	}
	defer rs.Close()

	baseIdx := map[string]*ir.Type{}
	for _, obj := range types {
		if t, ok := obj.(*ir.Type); ok && t.Variant == "BASE" {
			baseIdx[t.Schema+"."+t.Name] = t
		}
	}

	alignName := map[string]string{"c": "char", "s": "int2", "i": "int4", "d": "double"}
	storageName := map[string]string{"p": "plain", "e": "external", "m": "main", "x": "extended"}

	for rs.Next() {
		var schema, name, inputFn, outputFn, align, storage, category, delim string
		var recvFn, sendFn, modinFn, modoutFn, anlFn, subFn, element *string
		var typLen int32
		var byVal, preferred, collatable bool
		if err := rs.Scan(&schema, &name, &inputFn, &outputFn,
			&recvFn, &sendFn, &modinFn, &modoutFn, &anlFn, &subFn,
			&typLen, &byVal, &align, &storage,
			&category, &preferred, &delim, &element, &collatable); err != nil {
			return err
		}
		t, ok := baseIdx[schema+"."+name]
		if !ok {
			continue
		}
		parts := []string{
			"INPUT = " + quoteIdent(inputFn),
			"OUTPUT = " + quoteIdent(outputFn),
		}
		if recvFn != nil {
			parts = append(parts, "RECEIVE = "+quoteIdent(*recvFn))
		}
		if sendFn != nil {
			parts = append(parts, "SEND = "+quoteIdent(*sendFn))
		}
		if modinFn != nil {
			parts = append(parts, "TYPMOD_IN = "+quoteIdent(*modinFn))
		}
		if modoutFn != nil {
			parts = append(parts, "TYPMOD_OUT = "+quoteIdent(*modoutFn))
		}
		if anlFn != nil {
			parts = append(parts, "ANALYZE = "+quoteIdent(*anlFn))
		}
		if subFn != nil {
			parts = append(parts, "SUBSCRIPT = "+quoteIdent(*subFn))
		}
		if typLen == -1 {
			parts = append(parts, "INTERNALLENGTH = VARIABLE")
		} else if typLen > 0 {
			parts = append(parts, fmt.Sprintf("INTERNALLENGTH = %d", typLen))
		}
		if byVal {
			parts = append(parts, "PASSEDBYVALUE")
		}
		if a, ok := alignName[align]; ok {
			parts = append(parts, "ALIGNMENT = "+a)
		}
		if s, ok := storageName[storage]; ok {
			parts = append(parts, "STORAGE = "+s)
		}
		if element != nil {
			parts = append(parts, "ELEMENT = "+*element)
		}
		parts = append(parts, "DELIMITER = "+quoteLit(delim))
		parts = append(parts, "CATEGORY = "+quoteLit(category))
		if preferred {
			parts = append(parts, "PREFERRED = true")
		}
		if collatable {
			parts = append(parts, "COLLATABLE = true")
		}
		t.Body = strings.Join(parts, ", ")
		t.Reconstructed = true

		// Populate the same 7 in-place-alterable properties (Section 5.5)
		// the builder extracts from source, using identical value
		// formatting (bare lowercase function name; STORAGE's PG-code-to-
		// keyword mapping already matches typeNameToRef's rendering of a
		// bare unqualified name) so the two sides compare equal for an
		// unmodified object.
		t.BaseReceive = recvFn
		t.BaseSend = sendFn
		t.BaseTypmodIn = modinFn
		t.BaseTypmodOut = modoutFn
		t.BaseAnalyze = anlFn
		t.BaseSubscript = subFn
		if s, ok := storageName[storage]; ok {
			t.BaseStorage = &s
		}
	}
	return rs.Err()
}

func introspectEnumValues(ctx context.Context, conn pipeline.Querier, types []pipeline.IRObject) error {
	const q = `
SELECT n.nspname, t.typname, e.enumlabel
FROM   pg_enum e
JOIN   pg_type t      ON t.oid = e.enumtypid
JOIN   pg_namespace n ON n.oid = t.typnamespace
ORDER  BY n.nspname, t.typname, e.enumsortorder`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect enum values: %w", err)
	}
	defer rs.Close()

	typeIdx := map[string]*ir.Type{}
	for _, obj := range types {
		if t, ok := obj.(*ir.Type); ok && t.Variant == "ENUM" {
			typeIdx[t.Schema+"."+t.Name] = t
		}
	}
	for rs.Next() {
		var schema, name, label string
		if err := rs.Scan(&schema, &name, &label); err != nil {
			return err
		}
		if t, ok := typeIdx[schema+"."+name]; ok {
			t.EnumValues = append(t.EnumValues, label)
		}
	}
	return rs.Err()
}

// ── sequences ─────────────────────────────────────────────────────────────────

func introspectSequences(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT n.nspname, c.relname,
       r.rolname AS owner,
       obj_description(c.oid, 'pg_class') AS comment,
       s.seqincrement, s.seqmin, s.seqmax, s.seqstart, s.seqcache, s.seqcycle,
       format_type(s.seqtypid, NULL) AS as_type
FROM   pg_class c
JOIN   pg_namespace n  ON n.oid = c.relnamespace
JOIN   pg_roles r      ON r.oid = c.relowner
JOIN   pg_sequence s   ON s.seqrelid = c.oid
WHERE  c.relkind = 'S'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
AND    NOT EXISTS (
           SELECT 1 FROM pg_depend d
           WHERE  d.classid = 'pg_class'::regclass
           AND    d.objid = c.oid
           AND    d.deptype IN ('i', 'e')
       )
-- A hand-declared "OWNED BY" sequence (RFC audit item #14) produces the
-- identical pg_depend deptype='a' row a SERIAL/bigserial column's
-- auto-generated sequence does — confirmed live, PostgreSQL's catalog
-- cannot tell the two apart by dependency shape alone. The prior blanket
-- "any deptype='a' dependency means SERIAL sugar, exclude it" filter
-- therefore also made every hand-declared OWNED BY sequence invisible to
-- introspection entirely. introspectColumns' own SERIAL detection already
-- requires the owning column to ALSO carry a nextval() default
-- (serialOwned && def != nil) — mirrored here: only exclude when the
-- owning column has a default at all, the same distinguishing signal.
AND    NOT EXISTS (
           SELECT 1 FROM pg_depend d
           JOIN   pg_attrdef ad ON ad.adrelid = d.refobjid AND ad.adnum = d.refobjsubid
           WHERE  d.classid = 'pg_class'::regclass
           AND    d.objid = c.oid
           AND    d.deptype = 'a'
           AND    d.refclassid = 'pg_class'::regclass
       )
ORDER  BY n.nspname, c.relname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect sequences: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, owner string
		var comment *string
		var increment, min, max, start, cache int64
		var cycle bool
		var asType string
		if err := rs.Scan(&schema, &name, &owner, &comment, &increment, &min, &max, &start, &cache, &cycle, &asType); err != nil {
			return nil, err
		}
		seq := &ir.Sequence{
			Schema:      schema,
			Name:        name,
			Owner:       &owner,
			Comment:     comment,
			IncrementBy: &increment,
			MinValue:    &min,
			MaxValue:    &max,
			StartValue:  &start,
			Cache:       &cache,
			Cycle:       &cycle,
		}
		if asType != "" {
			seq.AsType = &ir.TypeRef{Name: asType}
		}
		out = append(out, seq)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	if err := introspectSequenceSecurityLabels(ctx, conn, out); err != nil {
		return nil, err
	}
	seqIdx := make(map[string]*ir.Sequence, len(out))
	for _, o := range out {
		seq := o.(*ir.Sequence)
		seqIdx[seq.Schema+"."+seq.Name] = seq
	}
	if err := introspectSequenceGrants(ctx, conn, seqIdx); err != nil {
		return nil, err
	}
	if err := introspectSequenceOwnedBy(ctx, conn, seqIdx); err != nil {
		return nil, err
	}
	return out, nil
}

// introspectSequenceOwnedBy populates Sequence.OwnedBy for every sequence in
// idx by reading the auto-dependency (pg_depend deptype 'a') PostgreSQL
// itself records for a real OWNED BY relationship (RFC audit item #14) — a
// sequence with no such dependency has no owner ("NONE"'s live-catalog
// equivalent; left nil here, matching the general nil-means-unspecified
// convention, since a bare sequence and one explicitly declared
// "OWNED BY NONE" are catalog-indistinguishable).
func introspectSequenceOwnedBy(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Sequence) error {
	const q = `
SELECT sn.nspname, s.relname, tn.nspname, t.relname, a.attname
FROM   pg_depend d
JOIN   pg_class s      ON s.oid = d.objid AND s.relkind = 'S'
JOIN   pg_namespace sn ON sn.oid = s.relnamespace
JOIN   pg_class t      ON t.oid = d.refobjid
JOIN   pg_namespace tn ON tn.oid = t.relnamespace
JOIN   pg_attribute a  ON a.attrelid = t.oid AND a.attnum = d.refobjsubid
WHERE  d.classid = 'pg_class'::regclass
AND    d.refclassid = 'pg_class'::regclass
AND    d.deptype = 'a'
AND    d.refobjsubid > 0`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect sequence owned-by: %w", err)
	}
	defer rs.Close()

	for rs.Next() {
		var seqSchema, seqName, tblSchema, tblName, colName string
		if err := rs.Scan(&seqSchema, &seqName, &tblSchema, &tblName, &colName); err != nil {
			return err
		}
		seq, ok := idx[seqSchema+"."+seqName]
		if !ok {
			continue
		}
		owned := fmt.Sprintf("%s.%s.%s", tblSchema, tblName, colName)
		seq.OwnedBy = &owned
	}
	return rs.Err()
}

// introspectSequenceGrants populates Sequence.Grants for every sequence in
// idx using aclexplode on pg_class.relacl, mirroring introspectTableGrants
// (RFC audit item #24: Sequence.Grants was correctly populated by the
// builder but never referenced by createSequence/diffSequence, and there was
// no introspection path for it at all).
func introspectSequenceGrants(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Sequence) error {
	const q = `
SELECT n.nspname, c.relname,
       CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END AS grantee,
       a.privilege_type, a.is_grantable
FROM   pg_class c
JOIN   pg_namespace n ON n.oid = c.relnamespace,
       LATERAL aclexplode(c.relacl) a
WHERE  c.relkind = 'S'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, c.relname, grantee, a.privilege_type`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect sequence grants: %w", err)
	}
	defer rs.Close()

	type grantKey struct{ schema, name, grantee string }
	type grantEntry struct {
		privs     []string
		grantable bool
	}
	grants := make(map[grantKey]*grantEntry)
	var order []grantKey

	for rs.Next() {
		var schema, name, grantee, priv string
		var grantable bool
		if err := rs.Scan(&schema, &name, &grantee, &priv, &grantable); err != nil {
			return err
		}
		k := grantKey{schema, name, grantee}
		e, ok := grants[k]
		if !ok {
			e = &grantEntry{}
			grants[k] = e
			order = append(order, k)
		}
		e.privs = append(e.privs, priv)
		if grantable {
			e.grantable = true
		}
	}
	if err := rs.Err(); err != nil {
		return err
	}

	for _, k := range order {
		s, ok := idx[k.schema+"."+k.name]
		if !ok {
			continue
		}
		e := grants[k]
		s.Grants = append(s.Grants, ir.Grant{
			Privileges: e.privs,
			Roles:      []string{k.grantee},
			WithGrant:  e.grantable,
		})
	}
	return nil
}

// introspectRoles reads every Role attribute (RFC §11.1) except PASSWORD:
// PostgreSQL restricts pg_authid (where a role's password, hashed, actually
// lives) to superuser, and pg_roles.rolpassword itself returns the fixed
// placeholder string '********' for any non-superuser caller regardless of
// whether a password is even set — confirmed live — so there's no reliable
// non-superuser proxy for "has a password" at all, let alone its value.
// PASSWORD stays source-side-only (never introspected, never live-diffed),
// same boundary as Subscription CONNECTION (§6z/§13.2).
//
// rolvaliduntil is cast to text as introspected; PostgreSQL's own timestamp
// formatting may not byte-match a hand-written VALID UNTIL literal in every
// case (e.g. timezone/precision representation) — a known, not yet solved,
// residual spurious-drift risk for that one field specifically, same class
// of gap as several other live-vs-declared text-format mismatches already
// flagged elsewhere in this codebase.
func introspectRoles(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT r.rolname,
       shobj_description(r.oid, 'pg_authid') AS comment,
       r.rolcanlogin,
       r.rolsuper,
       r.rolcreatedb,
       r.rolcreaterole,
       r.rolinherit,
       r.rolreplication,
       r.rolbypassrls,
       r.rolconnlimit,
       r.rolvaliduntil::text,
       (SELECT array_agg(pr.rolname ORDER BY pr.rolname)
          FROM pg_auth_members am JOIN pg_roles pr ON pr.oid = am.roleid
         WHERE am.member = r.oid) AS in_role,
       (SELECT array_agg(pr.rolname ORDER BY pr.rolname)
          FROM pg_auth_members am JOIN pg_roles pr ON pr.oid = am.member
         WHERE am.roleid = r.oid AND NOT am.admin_option) AS role_members,
       (SELECT array_agg(pr.rolname ORDER BY pr.rolname)
          FROM pg_auth_members am JOIN pg_roles pr ON pr.oid = am.member
         WHERE am.roleid = r.oid AND am.admin_option) AS admin_roles
FROM   pg_roles r
WHERE  r.rolname NOT LIKE 'pg_%'
AND    r.rolname <> 'postgres'
ORDER  BY r.rolname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect roles: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		r := &ir.Role{}
		if err := rs.Scan(
			&r.Name, &r.Comment, &r.CanLogin, &r.Superuser, &r.CreateDB, &r.CreateRole,
			&r.Inherit, &r.IsReplication, &r.BypassRLS, &r.ConnectionLimit, &r.ValidUntil,
			&r.InRole, &r.RoleMembers, &r.AdminRoles,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	if err := introspectRoleSecurityLabels(ctx, conn, out); err != nil {
		return nil, err
	}
	return out, nil
}

var _ pipeline.Introspector = (*CatalogIntrospector)(nil)

// ── query normalisation ───────────────────────────────────────────────────────

// stringLiteralCastRE matches a single-quoted SQL string literal (including
// escaped ” sequences) followed by a ::typename cast.
var stringLiteralCastRE = regexp.MustCompile(`('(?:[^']|'')*')::[A-Za-z_][A-Za-z0-9_]*`)

// stripStringLiteralCasts removes PG-added ::typename casts from single-quoted
// string literals in pg_get_expr / pg_get_viewdef output.
// e.g. 'active'::status → 'active', 'foo'::character → 'foo'.
// This makes the introspected form match what users write in .dpg source files.
func stripStringLiteralCasts(s string) string {
	return stringLiteralCastRE.ReplaceAllString(s, "$1")
}

// normalizeViewQuery strips PG-added type casts from string literals and then
// canonicalises the SQL through pg_query parse→deparse so that cosmetic
// differences (extra parentheses added by pg_get_viewdef, whitespace) do not
// produce spurious drift ops.
func normalizeViewQuery(q string) string {
	q = strings.TrimSpace(q)
	q = stripStringLiteralCasts(q)
	res, err := pg_query.Parse(q)
	if err != nil || len(res.Stmts) == 0 {
		return q
	}
	out, err := pg_query.Deparse(res)
	if err != nil {
		return q
	}
	return out
}

// introspectTableInherits populates Table.Inherits for every child table in idx.
func introspectTableInherits(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Table) error {
	const q = `
SELECT cn.nspname, cc.relname, pn.nspname AS parent_schema, pc.relname AS parent_name
FROM   pg_inherits i
JOIN   pg_class cc      ON cc.oid = i.inhrelid
JOIN   pg_namespace cn  ON cn.oid = cc.relnamespace
JOIN   pg_class pc      ON pc.oid = i.inhparent
JOIN   pg_namespace pn  ON pn.oid = pc.relnamespace
WHERE  NOT cc.relispartition
AND    cn.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY cn.nspname, cc.relname, pn.nspname, pc.relname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect table inherits: %w", err)
	}
	defer rs.Close()

	for rs.Next() {
		var schema, name, parentSchema, parentName string
		if err := rs.Scan(&schema, &name, &parentSchema, &parentName); err != nil {
			return err
		}
		t, ok := idx[schema+"."+name]
		if !ok {
			continue
		}
		parent := parentSchema + "." + parentName
		t.Inherits = append(t.Inherits, parent)
	}
	return rs.Err()
}

// introspectTableGrants populates Table.Grants for every table in idx using
// aclexplode on pg_class.relacl.
func introspectTableGrants(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Table) error {
	// aclexplode(NULL) returns 0 rows on PG14+, so no COALESCE needed.
	// An empty-literal COALESCE ('{}'::aclitem[]) produces ARR_NDIM=0, which
	// PG17 rejects with "ACL arrays must be one-dimensional".
	const q = `
SELECT n.nspname, c.relname,
       CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END AS grantee,
       a.privilege_type, a.is_grantable
FROM   pg_class c
JOIN   pg_namespace n ON n.oid = c.relnamespace,
       LATERAL aclexplode(c.relacl) a
WHERE  c.relkind IN ('r', 'p', 'f')
AND    NOT c.relispartition
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, c.relname, grantee, a.privilege_type`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect table grants: %w", err)
	}
	defer rs.Close()

	type grantKey struct{ schema, name, grantee string }
	type grantEntry struct {
		privs     []string
		grantable bool
	}
	grants := make(map[grantKey]*grantEntry)
	var order []grantKey

	for rs.Next() {
		var schema, name, grantee, priv string
		var grantable bool
		if err := rs.Scan(&schema, &name, &grantee, &priv, &grantable); err != nil {
			return err
		}
		k := grantKey{schema, name, grantee}
		e, ok := grants[k]
		if !ok {
			e = &grantEntry{}
			grants[k] = e
			order = append(order, k)
		}
		e.privs = append(e.privs, priv)
		if grantable {
			e.grantable = true
		}
	}
	if err := rs.Err(); err != nil {
		return err
	}

	for _, k := range order {
		t, ok := idx[k.schema+"."+k.name]
		if !ok {
			continue
		}
		e := grants[k]
		t.Grants = append(t.Grants, ir.Grant{
			Privileges: e.privs,
			Roles:      []string{k.grantee},
			WithGrant:  e.grantable,
		})
	}
	return nil
}

// introspectViewGrants populates View.Grants for every view in idx using
// aclexplode on pg_class.relacl.
func introspectViewGrants(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.View) error {
	const q = `
SELECT n.nspname, c.relname,
       CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END AS grantee,
       a.privilege_type, a.is_grantable
FROM   pg_class c
JOIN   pg_namespace n ON n.oid = c.relnamespace,
       LATERAL aclexplode(c.relacl) a
WHERE  c.relkind IN ('v', 'm')
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, c.relname, grantee, a.privilege_type`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect view grants: %w", err)
	}
	defer rs.Close()

	type grantKey struct{ schema, name, grantee string }
	type grantEntry struct {
		privs     []string
		grantable bool
	}
	grants := make(map[grantKey]*grantEntry)
	var order []grantKey

	for rs.Next() {
		var schema, name, grantee, priv string
		var grantable bool
		if err := rs.Scan(&schema, &name, &grantee, &priv, &grantable); err != nil {
			return err
		}
		k := grantKey{schema, name, grantee}
		e, ok := grants[k]
		if !ok {
			e = &grantEntry{}
			grants[k] = e
			order = append(order, k)
		}
		e.privs = append(e.privs, priv)
		if grantable {
			e.grantable = true
		}
	}
	if err := rs.Err(); err != nil {
		return err
	}

	for _, k := range order {
		v, ok := idx[k.schema+"."+k.name]
		if !ok {
			continue
		}
		e := grants[k]
		v.Grants = append(v.Grants, ir.Grant{
			Privileges: e.privs,
			Roles:      []string{k.grantee},
			WithGrant:  e.grantable,
		})
	}
	return nil
}

// introspectFunctionGrants populates Function.Grants for every function in idx
// using aclexplode on pg_proc.proacl. The idx key is "schema.name(args)".
func introspectFunctionGrants(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Function) error {
	const q = `
SELECT n.nspname, p.proname,
       pg_get_function_identity_arguments(p.oid) AS args,
       CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END AS grantee,
       a.privilege_type, a.is_grantable
FROM   pg_proc p
JOIN   pg_namespace n ON n.oid = p.pronamespace,
       LATERAL aclexplode(p.proacl) a
WHERE  p.prokind = 'f'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, p.proname, args, grantee, a.privilege_type`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect function grants: %w", err)
	}
	defer rs.Close()

	type grantKey struct{ schema, name, args, grantee string }
	type grantEntry struct {
		privs     []string
		grantable bool
	}
	grants := make(map[grantKey]*grantEntry)
	var order []grantKey

	for rs.Next() {
		var schema, name, args, grantee, priv string
		var grantable bool
		if err := rs.Scan(&schema, &name, &args, &grantee, &priv, &grantable); err != nil {
			return err
		}
		k := grantKey{schema, name, args, grantee}
		e, ok := grants[k]
		if !ok {
			e = &grantEntry{}
			grants[k] = e
			order = append(order, k)
		}
		e.privs = append(e.privs, priv)
		if grantable {
			e.grantable = true
		}
	}
	if err := rs.Err(); err != nil {
		return err
	}

	for _, k := range order {
		fn, ok := idx[k.schema+"."+k.name+"("+k.args+")"]
		if !ok {
			continue
		}
		e := grants[k]
		fn.Grants = append(fn.Grants, ir.Grant{
			Privileges: e.privs,
			Roles:      []string{k.grantee},
			WithGrant:  e.grantable,
		})
	}

	materialized, err := functionLikeACLMaterializedKeys(ctx, conn, "f")
	if err != nil {
		return err
	}
	for key, fn := range idx {
		if !materialized[key] || hasPublicGrant(fn.Grants) {
			continue
		}
		fn.Revocations = append(fn.Revocations, ir.Revocation{
			Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"},
		})
	}
	return nil
}

func introspectProcedureGrants(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Procedure) error {
	const q = `
SELECT n.nspname, p.proname,
       pg_get_function_identity_arguments(p.oid) AS args,
       CASE WHEN a.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END AS grantee,
       a.privilege_type, a.is_grantable
FROM   pg_proc p
JOIN   pg_namespace n ON n.oid = p.pronamespace,
       LATERAL aclexplode(p.proacl) a
WHERE  p.prokind = 'p'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, p.proname, args, grantee, a.privilege_type`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect procedure grants: %w", err)
	}
	defer rs.Close()

	type grantKey struct{ schema, name, args, grantee string }
	type grantEntry struct {
		privs     []string
		grantable bool
	}
	grants := make(map[grantKey]*grantEntry)
	var order []grantKey

	for rs.Next() {
		var schema, name, args, grantee, priv string
		var grantable bool
		if err := rs.Scan(&schema, &name, &args, &grantee, &priv, &grantable); err != nil {
			return err
		}
		k := grantKey{schema, name, args, grantee}
		e, ok := grants[k]
		if !ok {
			e = &grantEntry{}
			grants[k] = e
			order = append(order, k)
		}
		e.privs = append(e.privs, priv)
		if grantable {
			e.grantable = true
		}
	}
	if err := rs.Err(); err != nil {
		return err
	}

	for _, k := range order {
		proc, ok := idx[k.schema+"."+k.name+"("+k.args+")"]
		if !ok {
			continue
		}
		e := grants[k]
		proc.Grants = append(proc.Grants, ir.Grant{
			Privileges: e.privs,
			Roles:      []string{k.grantee},
			WithGrant:  e.grantable,
		})
	}

	materialized, err := functionLikeACLMaterializedKeys(ctx, conn, "p")
	if err != nil {
		return err
	}
	for key, proc := range idx {
		if !materialized[key] || hasPublicGrant(proc.Grants) {
			continue
		}
		proc.Revocations = append(proc.Revocations, ir.Revocation{
			Privileges: []string{"EXECUTE"}, Roles: []string{"PUBLIC"},
		})
	}
	return nil
}

// introspectColumnGrants reads explicit column-level privileges from
// pg_attribute.attacl and populates each ir.Column's Grants slice.
func introspectColumnGrants(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Table) error {
	// Deliberately queries pg_attribute.attacl (via aclexplode) rather than
	// information_schema.column_privileges — found live-testing a demo
	// project: column_privileges reports every EFFECTIVE privilege a role
	// has on a column, which PostgreSQL defines to include privileges the
	// role only has because of a table-level GRANT, not just genuine
	// explicit column-level ACL entries. A table with nothing but a
	// table-level "GRANT SELECT TO app_service" (no column-level grant
	// anywhere) had that same SELECT duplicated onto attacl.Grants for
	// EVERY column via this query, even though pg_attribute.attacl itself
	// was confirmed live to be NULL for all of them — causing dump to
	// round-trip in a phantom per-column GRANT, and a subsequent plan to
	// propose spuriously revoking it. attacl only ever holds genuine
	// explicit column-level grants (confirmed live: a real "GRANT SELECT
	// (email) ON TABLE users TO app_service" shows up here; the table-level
	// grant on an unrelated table's columns does not).
	// aclexplode(NULL) returns 0 rows on PG14+ (see introspectTableGrants'
	// identical note), so a column with no explicit ACL at all contributes
	// nothing here without needing a separate NULL guard.
	const q = `
SELECT n.nspname, c.relname, a.attname,
       CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE pg_get_userbyid(acl.grantee) END AS grantee,
       acl.privilege_type, acl.is_grantable
FROM   pg_attribute a
JOIN   pg_class c ON c.oid = a.attrelid
JOIN   pg_namespace n ON n.oid = c.relnamespace,
       LATERAL aclexplode(a.attacl) acl
WHERE  a.attnum > 0 AND NOT a.attisdropped
AND    acl.grantor <> acl.grantee
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, c.relname, a.attname, grantee, acl.privilege_type`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect column grants: %w", err)
	}
	defer rs.Close()

	// Accumulate per-(table, column, grantee) privilege lists, then convert.
	type colGrantKey struct{ schema, table, col, grantee string }
	type colGrantEntry struct {
		privs     []string
		grantable bool
	}
	grants := make(map[colGrantKey]*colGrantEntry)
	var order []colGrantKey // insertion order for determinism

	for rs.Next() {
		var schema, table, col, grantee, priv string
		var isGrantable bool
		if err := rs.Scan(&schema, &table, &col, &grantee, &priv, &isGrantable); err != nil {
			return err
		}
		k := colGrantKey{schema, table, col, grantee}
		e, ok := grants[k]
		if !ok {
			e = &colGrantEntry{}
			grants[k] = e
			order = append(order, k)
		}
		e.privs = append(e.privs, priv)
		if isGrantable {
			e.grantable = true
		}
	}
	if err := rs.Err(); err != nil {
		return err
	}

	for _, k := range order {
		t, ok := idx[k.schema+"."+k.table]
		if !ok {
			continue
		}
		e := grants[k]
		g := ir.Grant{
			Privileges: e.privs,
			Roles:      []string{k.grantee},
			WithGrant:  e.grantable,
		}
		for i := range t.Columns {
			if t.Columns[i].Name == k.col {
				t.Columns[i].Grants = append(t.Columns[i].Grants, g)
				break
			}
		}
	}
	return nil
}

// introspectPartitions populates PartitionBy and Partitions on partitioned
// tables, recursing into sub-partitioned children (RFC §7.13) — a partition
// has relkind 'p' too when it's itself further partitioned, so both queries
// below deliberately cover ALL partitioned relations, not just top-level
// tables in idx. Two queries are used: one for the partition key
// (pg_get_partkeydef), one for the child partition bounds (pg_get_expr on
// relpartbound); the parent-child edges are then assembled into a tree.
func introspectPartitions(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Table) error {
	const keyQ = `
SELECT n.nspname, c.relname, pg_get_partkeydef(c.oid) AS partkeydef
FROM   pg_class c
JOIN   pg_namespace n ON n.oid = c.relnamespace
WHERE  c.relkind = 'p'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, c.relname`

	rs, err := conn.QueryRows(ctx, keyQ)
	if err != nil {
		return fmt.Errorf("introspect partition keys: %w", err)
	}
	partKeys := make(map[string]string)
	for rs.Next() {
		var schema, name, keyDef string
		if err := rs.Scan(&schema, &name, &keyDef); err != nil {
			rs.Close()
			return err
		}
		partKeys[schema+"."+name] = keyDef
	}
	if err := rs.Err(); err != nil {
		return err
	}
	rs.Close()

	const childQ = `
SELECT pn.nspname, pc.relname, cn.nspname, cc.relname,
       pg_get_expr(cc.relpartbound, cc.oid) AS bound
FROM   pg_class cc
JOIN   pg_namespace cn  ON cn.oid = cc.relnamespace
JOIN   pg_inherits i    ON i.inhrelid = cc.oid
JOIN   pg_class pc      ON pc.oid = i.inhparent
JOIN   pg_namespace pn  ON pn.oid = pc.relnamespace
WHERE  cc.relispartition
-- relispartition is also true for a child INDEX partition auto-created when
-- an index exists directly on the partitioned parent (relkind 'i'), which
-- has no partition bound (pg_get_expr returns NULL there, crashing the scan
-- into a non-nullable string below) — restrict to actual table partitions.
AND    cc.relkind IN ('r', 'p')
AND    pn.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY pn.nspname, pc.relname, cc.relname`

	rs2, err := conn.QueryRows(ctx, childQ)
	if err != nil {
		return fmt.Errorf("introspect partition children: %w", err)
	}
	childrenOf := make(map[string][]*ir.Partition)
	byKey := make(map[string]*ir.Partition) // child qualified name -> its own Partition node
	for rs2.Next() {
		var parentSchema, parentName, childSchema, childName, bound string
		if err := rs2.Scan(&parentSchema, &parentName, &childSchema, &childName, &bound); err != nil {
			rs2.Close()
			return err
		}
		parentKey := parentSchema + "." + parentName
		childKey := childSchema + "." + childName
		part := &ir.Partition{Name: childName, Bounds: bound}
		if keyDef, ok := partKeys[childKey]; ok {
			part.PartitionBy = parsePartitionKey(keyDef)
		}
		childrenOf[parentKey] = append(childrenOf[parentKey], part)
		byKey[childKey] = part
	}
	if err := rs2.Err(); err != nil {
		return err
	}
	rs2.Close()

	// Attach nested sub-partitions to their own Partition node before wiring
	// the top-level tables, since each level's .Partitions must already be
	// populated by the time its parent reads childrenOf[key].
	for childKey, part := range byKey {
		if part.PartitionBy != nil {
			part.Partitions = childrenOf[childKey]
		}
	}

	for key, t := range idx {
		if keyDef, ok := partKeys[key]; ok {
			t.PartitionBy = parsePartitionKey(keyDef)
			t.Partitions = childrenOf[key]
		}
	}
	return nil
}

// parsePartitionKey converts a pg_get_partkeydef result (e.g. "RANGE (logdate)")
// into an ir.PartitionSpec.
func parsePartitionKey(keyDef string) *ir.PartitionSpec {
	spec := &ir.PartitionSpec{}
	upper := strings.ToUpper(keyDef)
	for _, strategy := range []string{"RANGE", "LIST", "HASH"} {
		if strings.HasPrefix(upper, strategy) {
			spec.Strategy = strategy
			rest := strings.TrimSpace(keyDef[len(strategy):])
			if len(rest) >= 2 && rest[0] == '(' && rest[len(rest)-1] == ')' {
				rest = rest[1 : len(rest)-1]
			}
			for col := range strings.SplitSeq(rest, ",") {
				if col = strings.TrimSpace(col); col != "" {
					spec.Columns = append(spec.Columns, col)
				}
			}
			break
		}
	}
	return spec
}
