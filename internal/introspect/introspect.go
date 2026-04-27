// Package introspect implements pipeline.Introspector. It reads the live
// PostgreSQL catalog (PG 14+) and returns IRObjects equivalent to what the
// compiler would produce from .dpg source files.
package introspect

import (
	"context"
	"fmt"
	"strings"

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

	return all, nil
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

// ── extensions ────────────────────────────────────────────────────────────────

func introspectExtensions(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT e.extname,
       n.nspname AS schema,
       e.extversion
FROM   pg_extension e
JOIN   pg_namespace n ON n.oid = e.extnamespace
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
SELECT c.relname, n.nspname, c.relpersistence,
       r.rolname AS owner,
       obj_description(c.oid, 'pg_class') AS comment,
       c.relrowsecurity, c.relforcerowsecurity
FROM   pg_class c
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_roles r     ON r.oid = c.relowner
WHERE  c.relkind = 'r'
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
       pg_get_expr(d.adbin, d.adrelid) AS col_default,
       col_description(a.attrelid, a.attnum) AS comment,
       a.attstattarget,
       CASE a.attcompression WHEN '' THEN NULL ELSE a.attcompression END AS compression,
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
		var def, comment, compression, storage *string
		var stats int
		if err := rs.Scan(&schema, &table, &name, &dataType, &notNull, &def, &comment, &stats, &compression, &storage); err != nil {
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
			Default:     def,
			Comment:     comment,
			Compression: compression,
			Storage:     storage,
		}
		if stats > 0 {
			col.Statistics = &stats
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
       con.condeferrable AS deferrable
FROM   pg_constraint con
JOIN   pg_class c     ON c.oid = con.conrelid
JOIN   pg_namespace n ON n.oid = c.relnamespace
WHERE  c.relkind = 'r'
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
		if err := rs.Scan(&schema, &table, &name, &typ, &expr, &notValid, &deferrable); err != nil {
			return err
		}
		t, ok := idx[schema+"."+table]
		if !ok {
			continue
		}
		t.Constraints = append(t.Constraints, &ir.Constraint{
			Name:       name,
			Type:       typ,
			Expr:       expr,
			NotValid:   notValid,
			Deferrable: deferrable,
		})
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
WHERE  NOT ix.indisprimary
AND    NOT ix.indisexclusion
AND    c.relkind = 'r'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
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
			Name:   name,
			Unique: unique,
			Method: method,
			Where:  where,
		})
	}
	return rs.Err()
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
		for _, part := range strings.Split(rawEvents, " OR ") {
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
       c.relkind = 'm' AS materialized
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

	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, owner, query string
		var comment *string
		var materialized bool
		if err := rs.Scan(&schema, &name, &owner, &query, &comment, &materialized); err != nil {
			return nil, err
		}
		out = append(out, &ir.View{
			Schema:       schema,
			Name:         name,
			Owner:        &owner,
			Query:        strings.TrimSpace(query),
			Comment:      comment,
			Materialized: materialized,
		})
	}
	return out, rs.Err()
}

// ── functions ─────────────────────────────────────────────────────────────────

func introspectFunctions(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT n.nspname, p.proname,
       pg_get_function_identity_arguments(p.oid) AS args,
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
       p.prokind
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

	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, args, retType, lang, volatility string
		var secDef, strict bool
		var comment *string
		var prokind string
		if err := rs.Scan(&schema, &name, &args, &retType, &lang, &volatility, &secDef, &strict, &comment, &prokind); err != nil {
			return nil, err
		}
		if prokind == "p" {
			// Procedure
			proc := &ir.Procedure{
				Schema:  schema,
				Name:    name,
				Comment: comment,
				Attrs: ir.FuncAttrs{
					Language: lang,
				},
			}
			if args != "" {
				for _, a := range strings.Split(args, ", ") {
					proc.Args = append(proc.Args, ir.FuncArg{Type: ir.TypeRef{Name: strings.TrimSpace(a)}})
				}
			}
			out = append(out, proc)
		} else {
			fn := &ir.Function{
				Schema:     schema,
				Name:       name,
				ReturnType: ir.TypeRef{Name: retType},
				Comment:    comment,
				Attrs: ir.FuncAttrs{
					Language:    lang,
					Volatility:  volatility,
					SecurityDef: secDef,
					Strict:      strict,
				},
			}
			if args != "" {
				for _, a := range strings.Split(args, ", ") {
					fn.Args = append(fn.Args, ir.FuncArg{Type: ir.TypeRef{Name: strings.TrimSpace(a)}})
				}
			}
			out = append(out, fn)
		}
	}
	return out, rs.Err()
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
	return out, nil
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
       obj_description(c.oid, 'pg_class') AS comment
FROM   pg_class c
JOIN   pg_namespace n ON n.oid = c.relnamespace
JOIN   pg_roles r     ON r.oid = c.relowner
WHERE  c.relkind = 'S'
AND    n.nspname NOT IN ('pg_catalog','information_schema','pg_toast')
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
		if err := rs.Scan(&schema, &name, &owner, &comment); err != nil {
			return nil, err
		}
		out = append(out, &ir.Sequence{Schema: schema, Name: name, Owner: &owner, Comment: comment})
	}
	return out, rs.Err()
}

func introspectRoles(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT r.rolname,
       obj_description(r.oid, 'pg_authid') AS comment
FROM   pg_roles r
WHERE  r.rolname NOT LIKE 'pg_%'
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
