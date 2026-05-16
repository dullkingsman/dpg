// Package introspect implements pipeline.Introspector. It reads the live
// PostgreSQL catalog (PG 14+) and returns IRObjects equivalent to what the
// compiler would produce from .dpg source files.
package introspect

import (
	"context"
	"fmt"
	"regexp"
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

	return all, nil
}

// ── aggregates ────────────────────────────────────────────────────────────────

func introspectAggregates(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT n.nspname, p.proname,
       pg_get_function_identity_arguments(p.oid) AS args,
       pg_catalog.oidvectortypes(p.proargtypes)   AS arg_types,
       obj_description(p.oid, 'pg_proc') AS comment
FROM   pg_proc p
JOIN   pg_namespace n ON n.oid = p.pronamespace
WHERE  p.prokind = 'a'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, p.proname, args`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect aggregates: %w", err)
	}
	defer rs.Close()

	aggIdx := make(map[string]*ir.Aggregate)
	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, args, argTypes string
		var comment *string
		if err := rs.Scan(&schema, &name, &args, &argTypes, &comment); err != nil {
			return nil, err
		}
		agg := &ir.Aggregate{
			Schema:  schema,
			Name:    name,
			Comment: comment,
			// Body is intentionally empty: we cannot reconstruct DDL from catalog.
			// diffAggregate skips the body check when Body == "".
		}
		// Use argTypes (type-only, from oidvectortypes) so QualifiedName matches
		// ir.ArgsKey(). Keep args (with parameter names) for the grants index key.
		if argTypes != "" {
			for a := range strings.SplitSeq(argTypes, ", ") {
				agg.Args = append(agg.Args, ir.FuncArg{Type: ir.TypeRef{Name: strings.TrimSpace(a)}})
			}
		}
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
	return out, nil
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
	return nil
}

// ── schemas ───────────────────────────────────────────────────────────────────

func introspectSchemas(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT n.nspname,
       r.rolname AS owner,
       obj_description(n.oid, 'pg_namespace') AS comment
FROM   pg_namespace n
JOIN   pg_roles r ON r.oid = n.nspowner
WHERE  n.nspname NOT LIKE 'pg_%'
AND    n.nspname NOT IN ('information_schema', 'public')
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

// ── extensions ────────────────────────────────────────────────────────────────

func introspectExtensions(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT e.extname,
       n.nspname AS schema,
       e.extversion
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
		if err := rs.Scan(&name, &schema, &version); err != nil {
			return nil, err
		}
		out = append(out, &ir.Extension{Name: name, Schema: &schema, Version: &version})
	}
	return out, rs.Err()
}

// ── tables ────────────────────────────────────────────────────────────────────

func introspectTables(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT c.relname, n.nspname, c.relpersistence::text,
       r.rolname AS owner,
       obj_description(c.oid, 'pg_class') AS comment,
       c.relrowsecurity, c.relforcerowsecurity
FROM   pg_class c
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_roles r     ON r.oid = c.relowner
WHERE  c.relkind IN ('r', 'p')
AND    NOT c.relispartition
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, c.relname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect tables: %w", err)
	}
	defer rs.Close()

	var tables []*ir.Table
	tableIdx := map[string]*ir.Table{}

	for rs.Next() {
		var name, schema, persistence, owner string
		var comment *string
		var rlsEnabled, rlsForced bool
		if err := rs.Scan(&name, &schema, &persistence, &owner, &comment, &rlsEnabled, &rlsForced); err != nil {
			return nil, err
		}
		t := &ir.Table{
			Schema:     schema,
			Name:       name,
			Unlogged:   persistence == "u",
			Owner:      &owner,
			Comment:    comment,
			RLSEnabled: rlsEnabled,
			RLSForced:  rlsForced,
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
       END AS storage
FROM   pg_attribute a
JOIN   pg_class c     ON c.oid = a.attrelid
JOIN   pg_namespace n ON n.oid = c.relnamespace
LEFT   JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE  a.attnum > 0
AND    NOT a.attisdropped
AND    c.relkind = 'r'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, c.relname, a.attnum`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect columns: %w", err)
	}
	defer rs.Close()

	for rs.Next() {
		var schema, table, name, dataType string
		var notNull bool
		var identityKind, generatedKind, def, comment, compression, storage *string
		var stats *int
		if err := rs.Scan(&schema, &table, &name, &dataType, &notNull, &identityKind, &generatedKind, &def, &comment, &stats, &compression, &storage); err != nil {
			return err
		}
		t, ok := idx[schema+"."+table]
		if !ok {
			continue
		}
		col := &ir.Column{
			Name:        name,
			Type:        ir.TypeRef{Name: dataType},
			NotNull:     notNull,
			Comment:     comment,
			Compression: compression,
			Storage:     storage,
		}
		switch {
		case identityKind != nil && *identityKind == "a":
			col.Identity = &ir.Identity{Always: true}
		case identityKind != nil && *identityKind == "d":
			col.Identity = &ir.Identity{Always: false}
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
       END AS single_col
FROM   pg_constraint con
JOIN   pg_class c     ON c.oid = con.conrelid
JOIN   pg_namespace n ON n.oid = c.relnamespace
WHERE  c.relkind = 'r'
AND    con.contype != 'n'
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
		var singleCol *string
		if err := rs.Scan(&schema, &table, &name, &typ, &expr, &notValid, &deferrable, &singleCol); err != nil {
			return err
		}
		t, ok := idx[schema+"."+table]
		if !ok {
			continue
		}
		cst := &ir.Constraint{
			Name:       name,
			Type:       typ,
			Expr:       expr,
			NotValid:   notValid,
			Deferrable: deferrable,
		}
		if singleCol != nil {
			cst.Columns = []string{*singleCol}
		}
		t.Constraints = append(t.Constraints, cst)
	}
	return rs.Err()
}

func introspectIndexes(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Table) error {
	const q = `
SELECT n.nspname, c.relname,
       i.relname AS idx_name,
       ix.indisunique,
       am.amname AS method,
       pg_get_indexdef(ix.indexrelid) AS idx_def
FROM   pg_index ix
JOIN   pg_class c  ON c.oid = ix.indrelid
JOIN   pg_class i  ON i.oid = ix.indexrelid
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_am am    ON am.oid = i.relam
WHERE  c.relkind = 'r'
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
		if err := rs.Scan(&schema, &table, &name, &unique, &method, &def); err != nil {
			return err
		}
		t, ok := idx[schema+"."+table]
		if !ok {
			continue
		}
		var where *string
		if i := strings.Index(strings.ToUpper(def), " WHERE "); i >= 0 {
			w := strings.TrimSpace(def[i+7:])
			where = &w
		}
		t.Indexes = append(t.Indexes, &ir.Index{
			Name:    name,
			Unique:  unique,
			Method:  method,
			Columns: parseIndexDef(def),
			Where:   where,
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
		col.Expr = &pipeline.RawExpr{Text: s}
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
       pg_get_expr(p.polwithcheck, p.polrelid) AS check_expr
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
		var using, check *string
		if err := rs.Scan(&schema, &table, &name, &cmd, &permissive, &using, &check); err != nil {
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
		})
	}
	return rs.Err()
}

func introspectTriggers(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Table) error {
	const q = `
SELECT n.nspname, c.relname,
       t.tgname,
       CASE WHEN (t.tgtype & 2) != 0 THEN 'BEFORE' ELSE 'AFTER' END AS when,
       CASE WHEN (t.tgtype & 1) != 0 THEN 'ROW' ELSE 'STATEMENT' END AS for_each,
       CASE WHEN (t.tgtype & 4)  != 0 THEN 'INSERT' ELSE '' END ||
       CASE WHEN (t.tgtype & 8)  != 0 THEN ' OR DELETE' ELSE '' END ||
       CASE WHEN (t.tgtype & 16) != 0 THEN ' OR UPDATE' ELSE '' END ||
       CASE WHEN (t.tgtype & 32) != 0 THEN ' OR TRUNCATE' ELSE '' END AS events,
       p.proname AS func_name,
       pn.nspname AS func_schema
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
		var schema, table, name, when, forEach, events, funcName, funcSchema string
		if err := rs.Scan(&schema, &table, &name, &when, &forEach, &events, &funcName, &funcSchema); err != nil {
			return err
		}
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
			Name:     name,
			When:     when,
			Events:   cleanEvents,
			ForEach:  forEach,
			Function: fn,
		})
	}
	return rs.Err()
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

	if err := introspectViewGrants(ctx, conn, viewIdx); err != nil {
		return nil, err
	}
	return out, nil
}

// ── functions ─────────────────────────────────────────────────────────────────

func introspectFunctions(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT n.nspname, p.proname,
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
       obj_description(p.oid, 'pg_proc') AS comment,
       p.prokind::text,
       p.prosrc
FROM   pg_proc p
JOIN   pg_namespace n ON n.oid = p.pronamespace
JOIN   pg_language  l ON l.oid = p.prolang
WHERE  p.prokind IN ('f', 'p')
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY n.nspname, p.proname, args`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect functions: %w", err)
	}
	defer rs.Close()

	funcIdx := make(map[string]*ir.Function)
	procIdx := make(map[string]*ir.Procedure)
	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, args, argTypes, retType, lang, volatility string
		var secDef, strict bool
		var comment *string
		var prokind, prosrc string
		if err := rs.Scan(&schema, &name, &args, &argTypes, &retType, &lang, &volatility, &secDef, &strict, &comment, &prokind, &prosrc); err != nil {
			return nil, err
		}
		if prokind == "p" {
			proc := &ir.Procedure{
				Schema:   schema,
				Name:     name,
				Comment:  comment,
				BodyHash: ir.HashBody(prosrc),
				Attrs: ir.FuncAttrs{
					Language: lang,
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
			out = append(out, proc)
		} else {
			fn := &ir.Function{
				Schema:     schema,
				Name:       name,
				ReturnType: ir.TypeRef{Name: retType},
				Comment:    comment,
				BodyHash:   ir.HashBody(prosrc),
				Attrs: ir.FuncAttrs{
					Language:    lang,
					Volatility:  volatility,
					SecurityDef: secDef,
					Strict:      strict,
				},
			}
			// Use argTypes (type-only) so QualifiedName matches argsKey() in IR builder.
			// Keep args (with parameter names) for the grants index key only.
			if argTypes != "" {
				for a := range strings.SplitSeq(argTypes, ", ") {
					fn.Args = append(fn.Args, ir.FuncArg{Type: ir.TypeRef{Name: strings.TrimSpace(a)}})
				}
			}
			funcIdx[schema+"."+name+"("+args+")"] = fn
			out = append(out, fn)
		}
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	rs.Close()

	if err := introspectFunctionGrants(ctx, conn, funcIdx); err != nil {
		return nil, err
	}
	if err := introspectProcedureGrants(ctx, conn, procIdx); err != nil {
		return nil, err
	}
	return out, nil
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
       obj_description(t.oid, 'pg_type') AS comment
FROM   pg_type t
JOIN   pg_namespace n ON n.oid = t.typnamespace
WHERE  t.typtype IN ('e','c','r','d')
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
AND    (t.typtype != 'c' OR NOT EXISTS (
    SELECT 1 FROM pg_class c WHERE c.oid = t.typrelid AND c.relkind != 'c'
))
ORDER  BY n.nspname, t.typname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect types: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, variant string
		var comment *string
		if err := rs.Scan(&schema, &name, &variant, &comment); err != nil {
			return nil, err
		}
		out = append(out, &ir.Type{Schema: schema, Name: name, Variant: variant, Comment: comment})
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
	return out, nil
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

func introspectDomainBodies(ctx context.Context, conn pipeline.Querier, types []pipeline.IRObject) error {
	const q = `
SELECT n.nspname, t.typname,
       pg_catalog.format_type(t.typbasetype, t.typtypmod) AS base_type,
       t.typnotnull,
       t.typdefault,
       (SELECT string_agg(
                   CASE WHEN con.conname != '' THEN 'CONSTRAINT ' || quote_ident(con.conname) || ' ' ELSE '' END
                   || pg_get_constraintdef(con.oid),
                   ' '
                   ORDER BY con.conname)
        FROM   pg_constraint con
        WHERE  con.contypid = t.oid AND con.contype = 'c') AS checks
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
		var defaultVal, checks *string
		if err := rs.Scan(&schema, &name, &baseType, &notNull, &defaultVal, &checks); err != nil {
			return err
		}
		t, ok := domainIdx[schema+"."+name]
		if !ok {
			continue
		}
		var body strings.Builder
		body.WriteString(baseType)
		if notNull {
			body.WriteString(" NOT NULL")
		}
		if defaultVal != nil {
			body.WriteString(" DEFAULT ")
			body.WriteString(*defaultVal)
		}
		if checks != nil {
			body.WriteString(" ")
			body.WriteString(*checks)
		}
		t.Body = body.String()
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
       s.seqincrement, s.seqmin, s.seqmax, s.seqstart, s.seqcache, s.seqcycle
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
           AND    d.deptype IN ('a', 'i')
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
		if err := rs.Scan(&schema, &name, &owner, &comment, &increment, &min, &max, &start, &cache, &cycle); err != nil {
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
			Cycle:       cycle,
		}
		out = append(out, seq)
	}
	return out, rs.Err()
}

func introspectRoles(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT r.rolname,
       obj_description(r.oid, 'pg_authid') AS comment
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
		var name string
		var comment *string
		if err := rs.Scan(&name, &comment); err != nil {
			return nil, err
		}
		out = append(out, &ir.Role{Name: name, Comment: comment})
	}
	return out, rs.Err()
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
WHERE  c.relkind IN ('r', 'p')
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
	return nil
}

// introspectColumnGrants reads column-level privileges from the information
// schema and populates each ir.Column's Grants slice.
func introspectColumnGrants(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.Table) error {
	const q = `
SELECT table_schema, table_name, column_name,
       grantee, privilege_type, is_grantable
FROM   information_schema.column_privileges
WHERE  table_schema NOT IN ('pg_catalog','information_schema','pg_toast')
AND    grantor <> grantee
ORDER  BY table_schema, table_name, column_name, grantee, privilege_type`

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
		var schema, table, col, grantee, priv, isGrantable string
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
		if isGrantable == "YES" {
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
// tables. Two queries are used: one for the partition key (pg_get_partkeydef),
// one for the child partition bounds (pg_get_expr on relpartbound).
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
	for rs.Next() {
		var schema, name, keyDef string
		if err := rs.Scan(&schema, &name, &keyDef); err != nil {
			rs.Close()
			return err
		}
		if t, ok := idx[schema+"."+name]; ok {
			t.PartitionBy = parsePartitionKey(keyDef)
		}
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
AND    pn.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
ORDER  BY pn.nspname, pc.relname, cc.relname`

	rs2, err := conn.QueryRows(ctx, childQ)
	if err != nil {
		return fmt.Errorf("introspect partition children: %w", err)
	}
	for rs2.Next() {
		var parentSchema, parentName, childSchema, childName, bound string
		if err := rs2.Scan(&parentSchema, &parentName, &childSchema, &childName, &bound); err != nil {
			rs2.Close()
			return err
		}
		if t, ok := idx[parentSchema+"."+parentName]; ok {
			t.Partitions = append(t.Partitions, &ir.Partition{
				Name:   childName,
				Bounds: bound,
			})
		}
	}
	if err := rs2.Err(); err != nil {
		return err
	}
	rs2.Close()
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
