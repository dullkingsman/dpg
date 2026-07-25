package introspect

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

// firstNormalObjectID is PostgreSQL's FirstNormalObjectId. Catalog rows with a
// lower OID are built-in system objects (e.g. the hundreds of pg_catalog casts
// and collations) that DPG must never dump or diff.
const firstNormalObjectID = 16384

// notExtensionOwned is a SQL predicate that excludes catalog rows owned by an
// extension (pg_depend deptype 'e'). Such objects are managed by CREATE
// EXTENSION — introspecting them as standalone objects would make `dump` emit
// them and `plan --live` propose dropping them. catalog is the row's system
// catalog regclass (e.g. "pg_cast") and aliasOID references that row's oid.
func notExtensionOwned(catalog, aliasOID string) string {
	return fmt.Sprintf(
		"NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = '%s'::regclass AND d.objid = %s AND d.deptype = 'e')",
		catalog, aliasOID)
}

// ── DDL reconstruction helpers ────────────────────────────────────────────────

// canonicalDDL runs a reconstructed CREATE statement through pg_query
// parse→deparse so its text matches, byte-for-byte, the compiler's rawSQL()
// output for the same statement. The compiler stores every opaque object's Body
// as pg_query.Deparse of the parsed source, and the snapshot persists only
// sha256(TrimSpace(Body)); canonicalisation is what keeps an introspected Body's
// hash equal to the compiled one, so `dump`/`verify`/`plan --live` stay drift-free.
// On any parse/deparse error it returns the trimmed input unchanged.
func canonicalDDL(sql string) string {
	sql = strings.TrimSpace(sql)
	res, err := pg_query.Parse(sql)
	if err != nil || len(res.Stmts) == 0 {
		return sql
	}
	out, err := pg_query.Deparse(res)
	if err != nil {
		return sql
	}
	return out
}

// quoteIdent double-quotes an identifier for safe interpolation. canonicalDDL
// removes quoting that PostgreSQL considers unnecessary, so over-quoting here is
// harmless.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// qualIdentQ renders an optionally schema-qualified identifier.
func qualIdentQ(schema, name string) string {
	if schema == "" {
		return quoteIdent(name)
	}
	return quoteIdent(schema) + "." + quoteIdent(name)
}

// quoteLit single-quotes a string literal.
func quoteLit(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// formatOptions renders a catalog options array (text[] of "key=value" entries,
// as stored in pg_foreign_data_wrapper.fdwoptions, pg_foreign_server.srvoptions,
// and pg_user_mappings.umoptions) into an ` OPTIONS (key 'value', …)` clause.
// Returns "" when there are no options.
func formatOptions(opts []string) string {
	var parts []string
	for _, o := range opts {
		k, v, found := strings.Cut(o, "=")
		if !found {
			continue
		}
		parts = append(parts, k+" "+quoteLit(v))
	}
	if len(parts) == 0 {
		return ""
	}
	return " OPTIONS (" + strings.Join(parts, ", ") + ")"
}

// ── tablespaces ───────────────────────────────────────────────────────────────

func introspectTablespaces(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	const q = `
SELECT spcname,
       pg_tablespace_location(oid)               AS location,
       obj_description(oid, 'pg_tablespace')      AS comment
FROM   pg_tablespace
WHERE  spcname NOT IN ('pg_default', 'pg_global')
ORDER  BY spcname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect tablespaces: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var name, location string
		var comment *string
		if err := rs.Scan(&name, &location, &comment); err != nil {
			return nil, err
		}
		body := fmt.Sprintf("CREATE TABLESPACE %s LOCATION %s", quoteIdent(name), quoteLit(location))
		out = append(out, &ir.Tablespace{Name: name, Body: canonicalDDL(body), Comment: comment, Reconstructed: true})
	}
	return out, rs.Err()
}

// ── foreign data wrappers ─────────────────────────────────────────────────────

func introspectForeignDataWrappers(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	q := `
SELECT f.fdwname,
       hn.nspname, h.proname,
       vn.nspname, v.proname,
       f.fdwoptions,
       obj_description(f.oid, 'pg_foreign_data_wrapper') AS comment
FROM   pg_foreign_data_wrapper f
LEFT   JOIN pg_proc h      ON h.oid = f.fdwhandler
LEFT   JOIN pg_namespace hn ON hn.oid = h.pronamespace
LEFT   JOIN pg_proc v      ON v.oid = f.fdwvalidator
LEFT   JOIN pg_namespace vn ON vn.oid = v.pronamespace
WHERE  f.oid >= $1
AND    ` + notExtensionOwned("pg_foreign_data_wrapper", "f.oid") + `
ORDER  BY f.fdwname`

	rs, err := conn.QueryRows(ctx, q, firstNormalObjectID)
	if err != nil {
		return nil, fmt.Errorf("introspect foreign data wrappers: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var name string
		var hSchema, hName, vSchema, vName *string
		var options []string
		var comment *string
		if err := rs.Scan(&name, &hSchema, &hName, &vSchema, &vName, &options, &comment); err != nil {
			return nil, err
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "CREATE FOREIGN DATA WRAPPER %s", quoteIdent(name))
		// Emit HANDLER/VALIDATOR only when present. Their absence is the default,
		// so omitting them (rather than writing NO HANDLER/NO VALIDATOR) keeps the
		// reconstruction's canonical deparse identical to a minimal source form.
		if hName != nil {
			fmt.Fprintf(&sb, " HANDLER %s", qualIdentQ(deref(hSchema), *hName))
		}
		if vName != nil {
			fmt.Fprintf(&sb, " VALIDATOR %s", qualIdentQ(deref(vSchema), *vName))
		}
		sb.WriteString(formatOptions(options))
		out = append(out, &ir.ForeignDataWrapper{Name: name, Body: canonicalDDL(sb.String()), Comment: comment, Reconstructed: true})
	}
	return out, rs.Err()
}

// ── foreign servers ───────────────────────────────────────────────────────────

func introspectForeignServers(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	q := `
SELECT s.srvname, f.fdwname, s.srvtype, s.srvversion, s.srvoptions,
       obj_description(s.oid, 'pg_foreign_server') AS comment
FROM   pg_foreign_server s
JOIN   pg_foreign_data_wrapper f ON f.oid = s.srvfdw
WHERE  ` + notExtensionOwned("pg_foreign_server", "s.oid") + `
ORDER  BY s.srvname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect foreign servers: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var name, fdw string
		var srvType, srvVersion *string
		var options []string
		var comment *string
		if err := rs.Scan(&name, &fdw, &srvType, &srvVersion, &options, &comment); err != nil {
			return nil, err
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "CREATE SERVER %s", quoteIdent(name))
		if srvType != nil && *srvType != "" {
			fmt.Fprintf(&sb, " TYPE %s", quoteLit(*srvType))
		}
		if srvVersion != nil && *srvVersion != "" {
			fmt.Fprintf(&sb, " VERSION %s", quoteLit(*srvVersion))
		}
		fmt.Fprintf(&sb, " FOREIGN DATA WRAPPER %s", quoteIdent(fdw))
		sb.WriteString(formatOptions(options))
		out = append(out, &ir.ForeignServer{Name: name, Body: canonicalDDL(sb.String()), Comment: comment, Reconstructed: true})
	}
	return out, rs.Err()
}

// ── user mappings ─────────────────────────────────────────────────────────────

func introspectUserMappings(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	// pg_user_mappings redacts umoptions to NULL unless the caller owns the
	// mapping or is a superuser; a redacted mapping is emitted without OPTIONS.
	const q = `
SELECT um.usename, um.srvname, um.umoptions
FROM   pg_user_mappings um
JOIN   pg_foreign_server s ON s.srvname = um.srvname
ORDER  BY um.srvname, um.usename`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect user mappings: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var user *string // NULL == mapping FOR PUBLIC
		var server string
		var options []string
		if err := rs.Scan(&user, &server, &options); err != nil {
			return nil, err
		}
		// pg_user_mappings.usename is the literal "public" for a FOR PUBLIC
		// mapping (never a real role — "public" is reserved). Treat it as the
		// PUBLIC case with an empty User, matching what the compiler records.
		forClause := "PUBLIC"
		irUser := ""
		if user != nil && *user != "public" {
			forClause = quoteIdent(*user)
			irUser = *user
		}
		body := fmt.Sprintf("CREATE USER MAPPING FOR %s SERVER %s%s",
			forClause, quoteIdent(server), formatOptions(options))
		out = append(out, &ir.UserMapping{User: irUser, Server: server, Body: canonicalDDL(body), Reconstructed: true})
	}
	return out, rs.Err()
}

// ── event triggers ────────────────────────────────────────────────────────────

func introspectEventTriggers(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	q := `
SELECT e.evtname, e.evtevent, e.evttags,
       n.nspname, p.proname
FROM   pg_event_trigger e
JOIN   pg_proc p      ON p.oid = e.evtfoid
JOIN   pg_namespace n ON n.oid = p.pronamespace
WHERE  ` + notExtensionOwned("pg_event_trigger", "e.oid") + `
ORDER  BY e.evtname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect event triggers: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var name, event, fnSchema, fnName string
		var tags []string
		if err := rs.Scan(&name, &event, &tags, &fnSchema, &fnName); err != nil {
			return nil, err
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "CREATE EVENT TRIGGER %s ON %s", quoteIdent(name), event)
		if len(tags) > 0 {
			quoted := make([]string, len(tags))
			for i, t := range tags {
				quoted[i] = quoteLit(t)
			}
			fmt.Fprintf(&sb, " WHEN TAG IN (%s)", strings.Join(quoted, ", "))
		}
		fmt.Fprintf(&sb, " EXECUTE FUNCTION %s()", qualIdentQ(fnSchema, fnName))
		out = append(out, &ir.EventTrigger{Name: name, Body: canonicalDDL(sb.String()), Reconstructed: true})
	}
	return out, rs.Err()
}

// ── casts ─────────────────────────────────────────────────────────────────────

func introspectCasts(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	q := `
SELECT format_type(c.castsource, NULL) AS src,
       format_type(c.casttarget, NULL) AS tgt,
       c.castcontext::text, c.castmethod::text,
       n.nspname, p.proname,
       pg_get_function_identity_arguments(p.oid) AS fn_args
FROM   pg_cast c
LEFT   JOIN pg_proc p      ON p.oid = c.castfunc
LEFT   JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE  c.oid >= $1
AND    ` + notExtensionOwned("pg_cast", "c.oid") + `
ORDER  BY src, tgt`

	rs, err := conn.QueryRows(ctx, q, firstNormalObjectID)
	if err != nil {
		return nil, fmt.Errorf("introspect casts: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var src, tgt, context8, method string
		var fnSchema, fnName, fnArgs *string
		if err := rs.Scan(&src, &tgt, &context8, &method, &fnSchema, &fnName, &fnArgs); err != nil {
			return nil, err
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "CREATE CAST (%s AS %s)", src, tgt)
		switch method {
		case "f": // via function
			args := ""
			if fnArgs != nil {
				args = *fnArgs
			}
			fmt.Fprintf(&sb, " WITH FUNCTION %s(%s)", qualIdentQ(deref(fnSchema), deref(fnName)), args)
		case "i": // via I/O conversion
			sb.WriteString(" WITH INOUT")
		default: // 'b' — binary-coercible
			sb.WriteString(" WITHOUT FUNCTION")
		}
		switch context8 {
		case "a":
			sb.WriteString(" AS ASSIGNMENT")
		case "i":
			sb.WriteString(" AS IMPLICIT")
		}
		out = append(out, &ir.Cast{
			SourceType:    ir.TypeRef{Name: src},
			TargetType:    ir.TypeRef{Name: tgt},
			Body:          canonicalDDL(sb.String()),
			Reconstructed: true,
		})
	}
	return out, rs.Err()
}

// ── publications ──────────────────────────────────────────────────────────────

func introspectPublications(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	q := `
SELECT p.oid, p.pubname, p.puballtables,
       p.pubinsert, p.pubupdate, p.pubdelete, p.pubtruncate
FROM   pg_publication p
WHERE  ` + notExtensionOwned("pg_publication", "p.oid") + `
ORDER  BY p.pubname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect publications: %w", err)
	}
	defer rs.Close()

	type pubRow struct {
		oid                             uint32
		name                            string
		allTables, ins, upd, del, trunc bool
	}
	var rows []pubRow
	for rs.Next() {
		var r pubRow
		if err := rs.Scan(&r.oid, &r.name, &r.allTables, &r.ins, &r.upd, &r.del, &r.trunc); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	rs.Close()

	var out []pipeline.IRObject
	for _, r := range rows {
		var sb strings.Builder
		fmt.Fprintf(&sb, "CREATE PUBLICATION %s", quoteIdent(r.name))
		if r.allTables {
			sb.WriteString(" FOR ALL TABLES")
		} else {
			targets, err := publicationTargets(ctx, conn, r.oid)
			if err != nil {
				return nil, err
			}
			if targets != "" {
				fmt.Fprintf(&sb, " %s", targets)
			}
		}
		if opt, ok := publishOption(r.ins, r.upd, r.del, r.trunc); ok {
			fmt.Fprintf(&sb, " WITH (publish = %s)", quoteLit(opt))
		}
		out = append(out, &ir.Publication{Name: r.name, Body: canonicalDDL(sb.String()), Reconstructed: true})
	}
	return out, rs.Err()
}

// publicationTargets builds the FOR TABLE / FOR TABLES IN SCHEMA clauses for a
// non-FOR-ALL-TABLES publication.
func publicationTargets(ctx context.Context, conn pipeline.Querier, pubid uint32) (string, error) {
	tables, err := scanQualNames(ctx, conn, `
SELECT n.nspname, c.relname
FROM   pg_publication_rel pr
JOIN   pg_class c     ON c.oid = pr.prrelid
JOIN   pg_namespace n ON n.oid = c.relnamespace
WHERE  pr.prpubid = $1
ORDER  BY n.nspname, c.relname`, pubid)
	if err != nil {
		return "", err
	}
	schemas, err := scanNames(ctx, conn, `
SELECT n.nspname
FROM   pg_publication_namespace pn
JOIN   pg_namespace n ON n.oid = pn.pnnspid
WHERE  pn.pnpubid = $1
ORDER  BY n.nspname`, pubid)
	if err != nil {
		return "", err
	}
	// A publication takes ONE FOR clause with a comma-separated list of
	// publication objects: FOR TABLE a, b, TABLES IN SCHEMA s1, TABLES IN SCHEMA s2.
	// Repeating FOR (FOR TABLE … FOR TABLES IN SCHEMA …) is a syntax error.
	var objects []string
	if len(tables) > 0 {
		objects = append(objects, "TABLE "+strings.Join(tables, ", "))
	}
	for _, s := range schemas {
		objects = append(objects, "TABLES IN SCHEMA "+quoteIdent(s))
	}
	if len(objects) == 0 {
		return "", nil
	}
	return "FOR " + strings.Join(objects, ", "), nil
}

// publishOption returns the publish= value (e.g. "insert, update") and whether a
// WITH (publish = …) clause is needed. It reports false for the default
// (all four operations enabled), so no clause is emitted; for any other set —
// including the degenerate all-disabled case (value "") — it reports true.
func publishOption(ins, upd, del, trunc bool) (string, bool) {
	if ins && upd && del && trunc {
		return "", false
	}
	var ops []string
	if ins {
		ops = append(ops, "insert")
	}
	if upd {
		ops = append(ops, "update")
	}
	if del {
		ops = append(ops, "delete")
	}
	if trunc {
		ops = append(ops, "truncate")
	}
	return strings.Join(ops, ", "), true
}

// ── collations ────────────────────────────────────────────────────────────────

func introspectCollations(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	// initdb imports the system locale collations into pg_catalog with normal
	// OIDs, so the namespace filter (not just oid >= FirstNormalObjectId) is
	// what keeps introspection to user-declared collations.
	//
	// The ICU locale column moved across releases: colllocale (PG17+),
	// colliculocale (PG15–16); on older releases the locale lives in
	// collcollate. Select the right column for the live server version.
	icuLocaleCol := "NULL::text"
	switch v := serverVersionNum(ctx, conn); {
	case v >= 170000:
		icuLocaleCol = "c.colllocale"
	case v >= 150000:
		icuLocaleCol = "c.colliculocale"
	}
	q := `
SELECT n.nspname, c.collname, c.collprovider,
       c.collcollate, c.collctype, c.collisdeterministic, ` + icuLocaleCol + `
FROM   pg_collation c
JOIN   pg_namespace n ON n.oid = c.collnamespace
WHERE  c.oid >= $1
AND    n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
AND    ` + notExtensionOwned("pg_collation", "c.oid") + `
ORDER  BY n.nspname, c.collname`

	rs, err := conn.QueryRows(ctx, q, firstNormalObjectID)
	if err != nil {
		return nil, fmt.Errorf("introspect collations: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name string
		var provider byte
		var collate, ctype, icuLocale *string
		var deterministic bool
		if err := rs.Scan(&schema, &name, &provider, &collate, &ctype, &deterministic, &icuLocale); err != nil {
			return nil, err
		}
		var attrs []string
		switch provider {
		case 'i': // icu — the locale is in icuLocale (or collcollate on old servers)
			loc := firstNonEmpty(icuLocale, collate)
			if loc == "" {
				// Cannot reconstruct a valid ICU collation; skip rather than
				// emit DDL that would fail to apply (LOCALE is required).
				continue
			}
			attrs = append(attrs, "PROVIDER = icu", "LOCALE = "+quoteLit(loc))
		default: // 'c' (libc) / 'd' (database default) / 'b' (builtin)
			if collate != nil && ctype != nil && *collate == *ctype {
				attrs = append(attrs, "LOCALE = "+quoteLit(*collate))
			} else {
				if collate != nil {
					attrs = append(attrs, "LC_COLLATE = "+quoteLit(*collate))
				}
				if ctype != nil {
					attrs = append(attrs, "LC_CTYPE = "+quoteLit(*ctype))
				}
			}
		}
		if !deterministic {
			attrs = append(attrs, "DETERMINISTIC = false")
		}
		if len(attrs) == 0 {
			continue // nothing reconstructable; skip rather than emit invalid DDL
		}
		body := fmt.Sprintf("CREATE COLLATION %s (%s)", qualIdentQ(schema, name), strings.Join(attrs, ", "))
		out = append(out, &ir.Collation{Schema: schema, Name: name, Body: canonicalDDL(body), Reconstructed: true})
	}
	return out, rs.Err()
}

// serverVersionNum returns the live server's server_version_num (e.g. 170004),
// or 0 if it cannot be read.
func serverVersionNum(ctx context.Context, conn pipeline.Querier) int {
	rs, err := conn.QueryRows(ctx, "SHOW server_version_num")
	if err != nil {
		return 0
	}
	defer rs.Close()
	if rs.Next() {
		var v string
		if err := rs.Scan(&v); err == nil {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
	}
	return 0
}

// firstNonEmpty returns the first non-nil, non-empty pointed-to string, or "".
func firstNonEmpty(ptrs ...*string) string {
	for _, p := range ptrs {
		if p != nil && *p != "" {
			return *p
		}
	}
	return ""
}

// ── extended statistics ───────────────────────────────────────────────────────

func introspectStatistics(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	// pg_get_statisticsobjdef reconstructs the full CREATE STATISTICS DDL,
	// including the kinds list, column/expression list, and source table.
	q := `
SELECT n.nspname, s.stxname, pg_get_statisticsobjdef(s.oid)
FROM   pg_statistic_ext s
JOIN   pg_namespace n ON n.oid = s.stxnamespace
WHERE  ` + notExtensionOwned("pg_statistic_ext", "s.oid") + `
ORDER  BY n.nspname, s.stxname`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect statistics: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, def string
		if err := rs.Scan(&schema, &name, &def); err != nil {
			return nil, err
		}
		out = append(out, &ir.StatisticsObject{Schema: schema, Name: name, Body: canonicalDDL(def), Reconstructed: true})
	}
	return out, rs.Err()
}

// ── shared row helpers ────────────────────────────────────────────────────────

// scanNames runs a single-column query and returns the values.
func scanNames(ctx context.Context, conn pipeline.Querier, q string, args ...any) ([]string, error) {
	rs, err := conn.QueryRows(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var s string
		if err := rs.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rs.Err()
}

// scanQualNames runs a (schema, name) query and returns quoted, qualified names.
func scanQualNames(ctx context.Context, conn pipeline.Querier, q string, args ...any) ([]string, error) {
	rs, err := conn.QueryRows(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var schema, name string
		if err := rs.Scan(&schema, &name); err != nil {
			return nil, err
		}
		out = append(out, qualIdentQ(schema, name))
	}
	return out, rs.Err()
}

// deref returns the pointed-to string or "" for a nil pointer.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
