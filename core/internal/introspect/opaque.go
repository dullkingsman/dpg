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
       shobj_description(oid, 'pg_tablespace')    AS comment
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
       n.nspname, p.proname,
       obj_description(e.oid, 'pg_event_trigger') AS comment
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
		var comment *string
		if err := rs.Scan(&name, &event, &tags, &fnSchema, &fnName, &comment); err != nil {
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
		out = append(out, &ir.EventTrigger{Name: name, Body: canonicalDDL(sb.String()), Comment: comment, Reconstructed: true})
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
       pg_get_function_identity_arguments(p.oid) AS fn_args,
       obj_description(c.oid, 'pg_cast') AS comment
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
		var fnSchema, fnName, fnArgs, comment *string
		if err := rs.Scan(&src, &tgt, &context8, &method, &fnSchema, &fnName, &fnArgs, &comment); err != nil {
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
			Comment:       comment,
			Reconstructed: true,
		})
	}
	return out, rs.Err()
}

// ── publications ──────────────────────────────────────────────────────────────

func introspectPublications(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	q := `
SELECT p.oid, p.pubname, p.puballtables,
       p.pubinsert, p.pubupdate, p.pubdelete, p.pubtruncate,
       obj_description(p.oid, 'pg_publication') AS comment
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
		comment                         *string
	}
	var rows []pubRow
	for rs.Next() {
		var r pubRow
		if err := rs.Scan(&r.oid, &r.name, &r.allTables, &r.ins, &r.upd, &r.del, &r.trunc, &r.comment); err != nil {
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
		out = append(out, &ir.Publication{Name: r.name, Body: canonicalDDL(sb.String()), Comment: r.comment, Reconstructed: true})
	}
	return out, rs.Err()
}

// ── subscriptions ────────────────────────────────────────────────────────────

// subscriptionConnInfoPlaceholder replaces the live CONNECTION string on
// introspection: pg_subscription.subconninfo has no default grant to PUBLIC
// (confirmed live — every other pg_subscription column keeps its default
// grant), and even a privileged caller who CAN read it would have no way to
// map the resolved value back to whatever {{secret-uri}} the original
// CONNECTION clause held, if any — same inherent limitation already
// documented for UserMapping OPTIONS (§14.10/§6ff). subconninfo is never
// selected at all, by design, not merely omitted on error.
//
// Must be syntactically valid libpq keyword/value conninfo syntax, not just
// any string: PostgreSQL parses CONNECTION's value as conninfo at CREATE
// SUBSCRIPTION time even with WITH (connect = false) — confirmed live, a
// bare comment-text placeholder errors "invalid connection string syntax"
// outright, connect = false only skips the network dial, not parsing. The
// warning itself lives inside the (quoted, libpq-legal) password field's
// value so it still reads clearly in a dump.
const subscriptionConnInfoPlaceholder = "host=REDACTED port=0 dbname=REDACTED user=REDACTED " +
	"password='live value redacted -- re-declare CONNECTION manually before enabling'"

// introspectSubscriptions reads every Subscription attribute except
// subconninfo (see subscriptionConnInfoPlaceholder). Reconstructed: true
// excludes Body from the drift comparison entirely (sourceBodyHash returns ""
// for a reconstructed body), so the always-different placeholder CONNECTION
// never causes a spurious DROP+CREATE loop; introspecting at all is what
// closes the actual bug this fixes — without it, plan --live has no entry
// for an already-applied subscription and proposes a spurious re-CREATE that
// then errors on apply (§6z).
//
// The reconstructed WITH clause always forces connect = false, create_slot =
// false, and enabled = false, regardless of the live subscription's actual
// state: subscriptionConnInfoPlaceholder is never a real conninfo, and
// PostgreSQL's own default (connect = true) would have CREATE SUBSCRIPTION
// try to dial that literal placeholder string and error outright — confirmed
// live, not assumed. Forcing all three keeps the reconstructed body valid,
// re-executable SQL (the same guarantee every other reliable-tier kind's
// Body already carries, proven by TestRenderOpaqueObjectsCompile), at the
// cost of the reconstruction never reflecting a currently-enabled live
// subscription's true state — acceptable, since the placeholder's own text
// already tells the reader this is an inert skeleton to hand-edit, not a
// live clone.
//
// pg_subscription is a shared, cluster-wide catalog (tablespace pg_global) —
// querying it from any database returns EVERY database's subscriptions, not
// just the current one (confirmed live), so subdbid must be filtered
// explicitly against the connected database's own oid; nothing else in this
// file needs that pattern, since every other reliable-tier catalog is
// already database-local.
//
// Column availability varies by PostgreSQL version (confirmed against the
// 14/15/16/17 pg_subscription catalog docs, not assumed from memory):
// subtwophasestate/subdisableonerr (15+), subpasswordrequired/subrunasowner
// (16+), subfailover (17+) don't exist on older servers. Each is
// select-guarded by serverVersionNum, substituting that option's own
// documented default (confirmed live against a fresh subscription, not the
// docs alone — the current docs page's prose default for `streaming` does
// not match live PG17 behavior) so it's simply omitted from the
// reconstructed WITH clause on an older server, identical to an unset
// option. subskiplsn is deliberately not read at all: it's one-shot admin
// state (ALTER SUBSCRIPTION ... SKIP), not part of a subscription's
// declarative definition, with no WITH-option equivalent.
func introspectSubscriptions(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	v := serverVersionNum(ctx, conn)
	twoPhaseCol, disableOnErrCol := `'d'::"char"`, "false"
	if v >= 150000 {
		twoPhaseCol, disableOnErrCol = "s.subtwophasestate", "s.subdisableonerr"
	}
	pwReqCol, runAsOwnerCol, originCol := "true", "false", "'any'::text"
	if v >= 160000 {
		pwReqCol, runAsOwnerCol, originCol = "s.subpasswordrequired", "s.subrunasowner", "COALESCE(s.suborigin, 'any')"
	}
	failoverCol := "false"
	if v >= 170000 {
		failoverCol = "s.subfailover"
	}

	// subenabled itself is never selected: the reconstructed body always
	// forces enabled = false (see the withOpts comment below), so the live
	// enabled/disabled state has nothing to feed into.
	q := fmt.Sprintf(`
SELECT s.subname, s.subbinary, s.substream, %s, %s,
       %s, %s, %s, s.subslotname, s.subsynccommit, s.subpublications, %s,
       obj_description(s.oid, 'pg_subscription') AS comment
FROM   pg_subscription s
WHERE  s.subdbid = (SELECT oid FROM pg_database WHERE datname = current_database())
ORDER  BY s.subname`, twoPhaseCol, disableOnErrCol, pwReqCol, runAsOwnerCol, failoverCol, originCol)

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("introspect subscriptions: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var name string
		var binary, disableOnErr, pwReq, runAsOwner, failover bool
		var stream, twoPhase byte
		var slotName *string
		var syncCommit, origin string
		var publications []string
		var comment *string
		if err := rs.Scan(&name, &binary, &stream, &twoPhase,
			&disableOnErr, &pwReq, &runAsOwner, &failover, &slotName, &syncCommit,
			&publications, &origin, &comment); err != nil {
			return nil, err
		}

		quotedPubs := make([]string, len(publications))
		for i, p := range publications {
			quotedPubs[i] = quoteIdent(p)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "CREATE SUBSCRIPTION %s CONNECTION %s PUBLICATION %s",
			quoteIdent(name), quoteLit(subscriptionConnInfoPlaceholder), strings.Join(quotedPubs, ", "))

		// connect/create_slot/enabled are always forced false, regardless of
		// the live subscription's actual state: subscriptionConnInfoPlaceholder
		// is never a real conninfo, so the PostgreSQL default (connect = true)
		// would have CREATE SUBSCRIPTION try to dial that literal placeholder
		// string and fail outright — confirmed live. This keeps the
		// reconstructed body valid, re-executable SQL (same guarantee every
		// other reliable-tier kind's Body already has), at the cost of never
		// reflecting the live enabled state; the placeholder's own text
		// ("re-declare manually") already tells the reader this is an inert
		// skeleton, not a live clone.
		withOpts := []string{"connect = false", "create_slot = false", "enabled = false"}
		if binary {
			withOpts = append(withOpts, "binary = true")
		}
		switch stream {
		case 't':
			withOpts = append(withOpts, "streaming = on")
		case 'p':
			withOpts = append(withOpts, "streaming = parallel")
		}
		if twoPhase != 'd' {
			withOpts = append(withOpts, "two_phase = true")
		}
		if disableOnErr {
			withOpts = append(withOpts, "disable_on_error = true")
		}
		if !pwReq {
			withOpts = append(withOpts, "password_required = false")
		}
		if runAsOwner {
			withOpts = append(withOpts, "run_as_owner = true")
		}
		if failover {
			withOpts = append(withOpts, "failover = true")
		}
		if origin != "any" {
			withOpts = append(withOpts, "origin = "+quoteLit(origin))
		}
		if slotName == nil {
			withOpts = append(withOpts, "slot_name = NONE")
		} else if *slotName != name {
			withOpts = append(withOpts, "slot_name = "+quoteLit(*slotName))
		}
		if syncCommit != "off" {
			withOpts = append(withOpts, "synchronous_commit = "+quoteLit(syncCommit))
		}
		if len(withOpts) > 0 {
			fmt.Fprintf(&sb, " WITH (%s)", strings.Join(withOpts, ", "))
		}

		out = append(out, &ir.Subscription{
			Name: name, ConnInfo: subscriptionConnInfoPlaceholder,
			Body: canonicalDDL(sb.String()), Comment: comment, Reconstructed: true,
		})
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
       c.collcollate, c.collctype, c.collisdeterministic, ` + icuLocaleCol + `,
       obj_description(c.oid, 'pg_collation') AS comment
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
		var collate, ctype, icuLocale, comment *string
		var deterministic bool
		if err := rs.Scan(&schema, &name, &provider, &collate, &ctype, &deterministic, &icuLocale, &comment); err != nil {
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
		out = append(out, &ir.Collation{Schema: schema, Name: name, Body: canonicalDDL(body), Comment: comment, Reconstructed: true})
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
SELECT n.nspname, s.stxname, pg_get_statisticsobjdef(s.oid),
       obj_description(s.oid, 'pg_statistic_ext') AS comment
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
		var comment *string
		if err := rs.Scan(&schema, &name, &def, &comment); err != nil {
			return nil, err
		}
		out = append(out, &ir.StatisticsObject{Schema: schema, Name: name, Body: canonicalDDL(def), Comment: comment, Reconstructed: true})
	}
	return out, rs.Err()
}

// ── operators ─────────────────────────────────────────────────────────────────

func introspectOperators(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	// oprcom/oprnegate reference other pg_operator rows by OID; a forward
	// reference (COMMUTATOR pointing at an operator created later) is legal —
	// PostgreSQL creates a shell and links it — so ordering is not a concern.
	q := `
SELECT n.nspname, o.oprname,
       NULLIF(o.oprleft, 0)::regtype::text  AS leftarg,
       NULLIF(o.oprright, 0)::regtype::text AS rightarg,
       o.oprcode::regproc::text             AS fn,
       cn.nspname AS com_schema, co.oprname AS com_name,
       nn.nspname AS neg_schema, no.oprname AS neg_name,
       NULLIF(o.oprrest, 0)::regproc::text   AS restrict_fn,
       NULLIF(o.oprjoin, 0)::regproc::text   AS join_fn,
       o.oprcanmerge, o.oprcanhash,
       obj_description(o.oid, 'pg_operator') AS comment
FROM   pg_operator o
JOIN   pg_namespace n  ON n.oid = o.oprnamespace
LEFT   JOIN pg_operator co ON co.oid = o.oprcom
LEFT   JOIN pg_namespace cn ON cn.oid = co.oprnamespace
LEFT   JOIN pg_operator no ON no.oid = o.oprnegate
LEFT   JOIN pg_namespace nn ON nn.oid = no.oprnamespace
WHERE  o.oid >= $1
AND    n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
AND    ` + notExtensionOwned("pg_operator", "o.oid") + `
ORDER  BY n.nspname, o.oprname`

	rs, err := conn.QueryRows(ctx, q, firstNormalObjectID)
	if err != nil {
		return nil, fmt.Errorf("introspect operators: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, fn string
		var leftArg, rightArg, comSchema, comName, negSchema, negName, restrictFn, joinFn, comment *string
		var canMerge, canHash bool
		if err := rs.Scan(&schema, &name, &leftArg, &rightArg, &fn,
			&comSchema, &comName, &negSchema, &negName, &restrictFn, &joinFn,
			&canMerge, &canHash, &comment); err != nil {
			return nil, err
		}
		var parts []string
		parts = append(parts, "FUNCTION = "+fn)
		if leftArg != nil {
			parts = append(parts, "LEFTARG = "+*leftArg)
		}
		if rightArg != nil {
			parts = append(parts, "RIGHTARG = "+*rightArg)
		}
		if comName != nil {
			parts = append(parts, "COMMUTATOR = OPERATOR("+operatorRef(deref(comSchema), *comName)+")")
		}
		if negName != nil {
			parts = append(parts, "NEGATOR = OPERATOR("+operatorRef(deref(negSchema), *negName)+")")
		}
		if restrictFn != nil {
			parts = append(parts, "RESTRICT = "+*restrictFn)
		}
		if joinFn != nil {
			parts = append(parts, "JOIN = "+*joinFn)
		}
		if canHash {
			parts = append(parts, "HASHES")
		}
		if canMerge {
			parts = append(parts, "MERGES")
		}
		body := fmt.Sprintf("CREATE OPERATOR %s (%s)", operatorRef(schema, name), strings.Join(parts, ", "))
		op := &ir.Operator{Schema: schema, Name: name, Body: canonicalDDL(body), Comment: comment, Reconstructed: true}
		if leftArg != nil {
			t := ir.TypeRef{Name: *leftArg}
			op.LeftType = &t
		}
		if rightArg != nil {
			t := ir.TypeRef{Name: *rightArg}
			op.RightType = &t
		}
		out = append(out, op)
	}
	return out, rs.Err()
}

// operatorRef renders an optionally schema-qualified operator name. The operator
// symbol itself (e.g. "===", "@>") is never quoted — quoting is only applied to
// the schema identifier.
func operatorRef(schema, name string) string {
	if schema == "" {
		return name
	}
	return quoteIdent(schema) + "." + name
}

// ── text search: parsers ──────────────────────────────────────────────────────

func introspectTSParsers(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	q := `
SELECT n.nspname, p.prsname,
       p.prsstart::regproc::text    AS start_fn,
       p.prstoken::regproc::text    AS token_fn,
       p.prsend::regproc::text      AS end_fn,
       p.prslextype::regproc::text  AS lextype_fn,
       NULLIF(p.prsheadline, 0)::regproc::text AS headline_fn,
       obj_description(p.oid, 'pg_ts_parser') AS comment
FROM   pg_ts_parser p
JOIN   pg_namespace n ON n.oid = p.prsnamespace
WHERE  p.oid >= $1
AND    n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
AND    ` + notExtensionOwned("pg_ts_parser", "p.oid") + `
ORDER  BY n.nspname, p.prsname`

	rs, err := conn.QueryRows(ctx, q, firstNormalObjectID)
	if err != nil {
		return nil, fmt.Errorf("introspect text search parsers: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, startFn, tokenFn, endFn, lextypeFn string
		var headlineFn, comment *string
		if err := rs.Scan(&schema, &name, &startFn, &tokenFn, &endFn, &lextypeFn, &headlineFn, &comment); err != nil {
			return nil, err
		}
		parts := []string{
			"START = " + startFn,
			"GETTOKEN = " + tokenFn,
			"END = " + endFn,
			"LEXTYPES = " + lextypeFn,
		}
		if headlineFn != nil {
			parts = append(parts, "HEADLINE = "+*headlineFn)
		}
		body := fmt.Sprintf("CREATE TEXT SEARCH PARSER %s (%s)", qualIdentQ(schema, name), strings.Join(parts, ", "))
		out = append(out, &ir.TSParser{Schema: schema, Name: name, Body: canonicalDDL(body), Comment: comment, Reconstructed: true})
	}
	return out, rs.Err()
}

// ── text search: templates ────────────────────────────────────────────────────

func introspectTSTemplates(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	q := `
SELECT n.nspname, t.tmplname,
       NULLIF(t.tmplinit, 0)::regproc::text AS init_fn,
       t.tmpllexize::regproc::text          AS lexize_fn,
       obj_description(t.oid, 'pg_ts_template') AS comment
FROM   pg_ts_template t
JOIN   pg_namespace n ON n.oid = t.tmplnamespace
WHERE  t.oid >= $1
AND    n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
AND    ` + notExtensionOwned("pg_ts_template", "t.oid") + `
ORDER  BY n.nspname, t.tmplname`

	rs, err := conn.QueryRows(ctx, q, firstNormalObjectID)
	if err != nil {
		return nil, fmt.Errorf("introspect text search templates: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, lexizeFn string
		var initFn, comment *string
		if err := rs.Scan(&schema, &name, &initFn, &lexizeFn, &comment); err != nil {
			return nil, err
		}
		var parts []string
		if initFn != nil {
			parts = append(parts, "INIT = "+*initFn)
		}
		parts = append(parts, "LEXIZE = "+lexizeFn)
		body := fmt.Sprintf("CREATE TEXT SEARCH TEMPLATE %s (%s)", qualIdentQ(schema, name), strings.Join(parts, ", "))
		out = append(out, &ir.TSTemplate{Schema: schema, Name: name, Body: canonicalDDL(body), Comment: comment, Reconstructed: true})
	}
	return out, rs.Err()
}

// ── text search: dictionaries ─────────────────────────────────────────────────

func introspectTSDicts(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	// dictinitoption is stored as the already-rendered option list, e.g.
	// "stopwords = 'english', language = 'english'"; it is appended verbatim.
	// The outer alias must not be "d": notExtensionOwned's subquery aliases
	// pg_depend as "d", and a matching outer alias would be shadowed.
	q := `
SELECT n.nspname, dict.dictname,
       tn.nspname AS tmpl_schema, t.tmplname AS tmpl_name,
       dict.dictinitoption,
       obj_description(dict.oid, 'pg_ts_dict') AS comment
FROM   pg_ts_dict dict
JOIN   pg_namespace n  ON n.oid = dict.dictnamespace
JOIN   pg_ts_template t ON t.oid = dict.dicttemplate
JOIN   pg_namespace tn ON tn.oid = t.tmplnamespace
WHERE  dict.oid >= $1
AND    n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
AND    ` + notExtensionOwned("pg_ts_dict", "dict.oid") + `
ORDER  BY n.nspname, dict.dictname`

	rs, err := conn.QueryRows(ctx, q, firstNormalObjectID)
	if err != nil {
		return nil, fmt.Errorf("introspect text search dictionaries: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, tmplSchema, tmplName string
		var initOption, comment *string
		if err := rs.Scan(&schema, &name, &tmplSchema, &tmplName, &initOption, &comment); err != nil {
			return nil, err
		}
		parts := []string{"TEMPLATE = " + qualIdentQ(tmplSchema, tmplName)}
		if initOption != nil && strings.TrimSpace(*initOption) != "" {
			parts = append(parts, strings.TrimSpace(*initOption))
		}
		body := fmt.Sprintf("CREATE TEXT SEARCH DICTIONARY %s (%s)", qualIdentQ(schema, name), strings.Join(parts, ", "))
		out = append(out, &ir.TSDict{
			Schema: schema, Name: name,
			TemplateSchema: tmplSchema, TemplateName: tmplName,
			Body: canonicalDDL(body), Comment: comment, Reconstructed: true,
		})
	}
	return out, rs.Err()
}

// ── text search: configurations ───────────────────────────────────────────────

func introspectTSConfigs(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	// The reconstructed body is the bare CREATE with its PARSER; token→dictionary
	// mappings (pg_ts_config_map) are a separate IR concern (TSConfig.Mappings)
	// and are not folded into the body.
	q := `
SELECT n.nspname, c.cfgname,
       pn.nspname AS parser_schema, p.prsname AS parser_name,
       obj_description(c.oid, 'pg_ts_config') AS comment
FROM   pg_ts_config c
JOIN   pg_namespace n  ON n.oid = c.cfgnamespace
JOIN   pg_ts_parser p  ON p.oid = c.cfgparser
JOIN   pg_namespace pn ON pn.oid = p.prsnamespace
WHERE  c.oid >= $1
AND    n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
AND    ` + notExtensionOwned("pg_ts_config", "c.oid") + `
ORDER  BY n.nspname, c.cfgname`

	rs, err := conn.QueryRows(ctx, q, firstNormalObjectID)
	if err != nil {
		return nil, fmt.Errorf("introspect text search configurations: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	idx := make(map[string]*ir.TSConfig)
	for rs.Next() {
		var schema, name, parserSchema, parserName string
		var comment *string
		if err := rs.Scan(&schema, &name, &parserSchema, &parserName, &comment); err != nil {
			return nil, err
		}
		body := fmt.Sprintf("CREATE TEXT SEARCH CONFIGURATION %s (PARSER = %s)",
			qualIdentQ(schema, name), qualIdentQ(parserSchema, parserName))
		tc := &ir.TSConfig{
			Schema: schema, Name: name,
			ParserSchema: parserSchema, ParserName: parserName,
			Body: canonicalDDL(body), Comment: comment, Reconstructed: true,
		}
		idx[schema+"."+name] = tc
		out = append(out, tc)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	rs.Close()

	if err := introspectTSConfigMappings(ctx, conn, idx); err != nil {
		return nil, err
	}
	return out, nil
}

// introspectTSConfigMappings populates Mappings (RFC §12.1) for every
// TSConfig in idx from pg_ts_config_map — previously never queried at all
// (a comment here even claimed mappings were "a separate IR concern",
// implying somewhere else handled it, but nothing did: TSConfig.Mappings
// was always empty from introspection). maptokentype is an integer that
// only resolves to a name (e.g. "word", "hword") via ts_token_type(parser
// oid) — there's no direct catalog column with the readable alias. Token
// types are grouped into one MAPPING FOR entry per identical, ordered
// dictionary chain (matching how a human naturally writes it and how a
// live config's own \dF+ output groups them), not emitted one row per
// token type.
func introspectTSConfigMappings(ctx context.Context, conn pipeline.Querier, idx map[string]*ir.TSConfig) error {
	if len(idx) == 0 {
		return nil
	}
	const q = `
SELECT n.nspname, c.cfgname, tt.alias, dn.nspname AS dict_schema, d.dictname, m.mapseqno
FROM   pg_ts_config_map m
JOIN   pg_ts_config c        ON c.oid = m.mapcfg
JOIN   pg_namespace n        ON n.oid = c.cfgnamespace
JOIN   pg_ts_dict d          ON d.oid = m.mapdict
JOIN   pg_namespace dn       ON dn.oid = d.dictnamespace
JOIN   ts_token_type(c.cfgparser) tt ON tt.tokid = m.maptokentype
WHERE  n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
ORDER  BY n.nspname, c.cfgname, tt.alias, m.mapseqno`

	rs, err := conn.QueryRows(ctx, q)
	if err != nil {
		return fmt.Errorf("introspect text search config mappings: %w", err)
	}
	defer rs.Close()

	// Per-config, per-token-type ordered dictionary chain, built up across
	// rows (mapseqno order) before being grouped into MAPPING FOR entries.
	type key struct{ schema, name string }
	chains := make(map[key]map[string][]pipeline.Identifier)
	var order []key
	tokenOrder := make(map[key][]string)
	for rs.Next() {
		var schema, name, tokenAlias, dictSchema, dictName string
		var mapseqno int
		if err := rs.Scan(&schema, &name, &tokenAlias, &dictSchema, &dictName, &mapseqno); err != nil {
			return err
		}
		k := key{schema, name}
		if _, ok := idx[schema+"."+name]; !ok {
			continue
		}
		if chains[k] == nil {
			chains[k] = make(map[string][]pipeline.Identifier)
			order = append(order, k)
		}
		if _, ok := chains[k][tokenAlias]; !ok {
			tokenOrder[k] = append(tokenOrder[k], tokenAlias)
		}
		dictID := pipeline.Identifier{Name: dictName}
		if dictSchema != "pg_catalog" {
			dictID.Schema = dictSchema
		}
		chains[k][tokenAlias] = append(chains[k][tokenAlias], dictID)
	}
	if err := rs.Err(); err != nil {
		return err
	}

	for _, k := range order {
		tc := idx[k.schema+"."+k.name]
		byChain := make(map[string][]string) // chain signature -> token types, in first-seen order
		var chainOrder []string
		chainDicts := make(map[string][]pipeline.Identifier)
		for _, tok := range tokenOrder[k] {
			dicts := chains[k][tok]
			sig := ""
			for _, d := range dicts {
				sig += d.String() + ","
			}
			if _, ok := byChain[sig]; !ok {
				chainOrder = append(chainOrder, sig)
				chainDicts[sig] = dicts
			}
			byChain[sig] = append(byChain[sig], tok)
		}
		for _, sig := range chainOrder {
			tc.Mappings = append(tc.Mappings, pipeline.TSMappingDef{
				TokenTypes:   byChain[sig],
				Dictionaries: chainDicts[sig],
			})
		}
	}
	return nil
}

// ── operator families ─────────────────────────────────────────────────────────

func introspectOperatorFamilies(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	// Every operator family is introspected as a standalone object — including
	// one PostgreSQL auto-created for an unqualified CREATE OPERATOR CLASS —
	// mirroring pg_dump's own model exactly (confirmed live against postgres:17:
	// `pg_dump -s` always emits a separate CREATE OPERATOR FAMILY for every
	// family, auto-created or not, and always gives every class an explicit
	// FAMILY clause; see introspectOperatorClasses).
	//
	// A prior version of this function tried to skip families it inferred were
	// auto-created (matching a class's own name+schema), to avoid a spurious
	// CREATE OPERATOR FAMILY that would conflict with PG's auto-creation on
	// apply. That heuristic was unreliable BY DESIGN, not just in a rare edge
	// case: confirmed live that the opclass→opfamily pg_depend row is deptype
	// 'a' (DEPENDENCY_AUTO) whether the family was truly auto-created OR
	// explicitly created and then attached via an explicit FAMILY clause of the
	// same name — there is no pg_depend signal that distinguishes the two, so
	// name-matching necessarily misclassified the second, legal case (an
	// explicit family — possibly enriched via ALTER OPERATOR FAMILY ADD, or
	// shared by several classes — silently dropped from the dump). Once every
	// class is given an explicit FAMILY clause unconditionally (see
	// introspectOperatorClasses) and every family is introspected
	// unconditionally, the ambiguity is moot: PostgreSQL itself no longer needs
	// to guess, since the class always names its family explicitly, matching
	// exactly how pg_dump avoids the same ambiguity.
	q := `
SELECT n.nspname, f.opfname, am.amname,
       obj_description(f.oid, 'pg_opfamily') AS comment
FROM   pg_opfamily f
JOIN   pg_namespace n ON n.oid = f.opfnamespace
JOIN   pg_am am       ON am.oid = f.opfmethod
WHERE  f.oid >= $1
AND    n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
AND    ` + notExtensionOwned("pg_opfamily", "f.oid") + `
ORDER  BY n.nspname, f.opfname`

	rs, err := conn.QueryRows(ctx, q, firstNormalObjectID)
	if err != nil {
		return nil, fmt.Errorf("introspect operator families: %w", err)
	}
	defer rs.Close()

	var out []pipeline.IRObject
	for rs.Next() {
		var schema, name, am string
		var comment *string
		if err := rs.Scan(&schema, &name, &am, &comment); err != nil {
			return nil, err
		}
		body := fmt.Sprintf("CREATE OPERATOR FAMILY %s USING %s", qualIdentQ(schema, name), quoteIdent(am))
		out = append(out, &ir.OperatorFamily{
			Schema: schema, Name: name, AccessMethod: am,
			Body: canonicalDDL(body), Comment: comment, Reconstructed: true,
		})
	}
	return out, rs.Err()
}

// ── operator classes ──────────────────────────────────────────────────────────

func introspectOperatorClasses(ctx context.Context, conn pipeline.Querier) ([]pipeline.IRObject, error) {
	q := `
SELECT c.oid, n.nspname, c.opcname, am.amname,
       c.opcintype::regtype::text AS intype,
       c.opcdefault,
       NULLIF(c.opckeytype, 0)::regtype::text AS storagetype,
       fn.nspname AS fam_schema, f.opfname AS fam_name,
       obj_description(c.oid, 'pg_opclass') AS comment
FROM   pg_opclass c
JOIN   pg_namespace n ON n.oid = c.opcnamespace
JOIN   pg_am am       ON am.oid = c.opcmethod
JOIN   pg_opfamily f  ON f.oid = c.opcfamily
JOIN   pg_namespace fn ON fn.oid = f.opfnamespace
WHERE  c.oid >= $1
AND    n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
AND    ` + notExtensionOwned("pg_opclass", "c.oid") + `
ORDER  BY n.nspname, c.opcname`

	rs, err := conn.QueryRows(ctx, q, firstNormalObjectID)
	if err != nil {
		return nil, fmt.Errorf("introspect operator classes: %w", err)
	}

	type opcRow struct {
		oid                                      uint32
		schema, name, am, intype                 string
		isDefault                                bool
		storageType, famSchema, famName, comment *string
	}
	var rows []opcRow
	for rs.Next() {
		var r opcRow
		if err := rs.Scan(&r.oid, &r.schema, &r.name, &r.am, &r.intype, &r.isDefault,
			&r.storageType, &r.famSchema, &r.famName, &r.comment); err != nil {
			rs.Close()
			return nil, err
		}
		rows = append(rows, r)
	}
	if err := rs.Err(); err != nil {
		rs.Close()
		return nil, err
	}
	rs.Close()

	var out []pipeline.IRObject
	for _, r := range rows {
		members, err := opClassMembers(ctx, conn, r.oid)
		if err != nil {
			return nil, err
		}
		if r.storageType != nil {
			members = append(members, "STORAGE "+*r.storageType)
		}
		if len(members) == 0 {
			// An operator class with no reconstructable members cannot be emitted
			// as valid DDL; skip rather than produce an empty AS clause.
			continue
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "CREATE OPERATOR CLASS %s", qualIdentQ(r.schema, r.name))
		if r.isDefault {
			sb.WriteString(" DEFAULT")
		}
		fmt.Fprintf(&sb, " FOR TYPE %s USING %s", r.intype, quoteIdent(r.am))
		// Always name the family explicitly, matching pg_dump's own model exactly
		// (confirmed live: pg_dump -s does this even for a family PostgreSQL
		// auto-created via an unqualified CREATE OPERATOR CLASS). r.famName/
		// r.famSchema come from an INNER JOIN on pg_opclass.opcfamily, a NOT NULL
		// column — every class belongs to exactly one family, so these are always
		// populated; the pointer type only mirrors storageType's genuinely-nullable
		// shape. This removes the prior same-name-as-class "implicit" special case
		// entirely: see introspectOperatorFamilies for why that heuristic was
		// unreliable by construction (deptype 'a' cannot distinguish an
		// auto-created family from an explicit one sharing the class's name), not
		// merely wrong in a rare case.
		famSchema, famName := deref(r.famSchema), deref(r.famName)
		fmt.Fprintf(&sb, " FAMILY %s", qualIdentQ(famSchema, famName))
		fmt.Fprintf(&sb, " AS %s", strings.Join(members, ", "))
		out = append(out, &ir.OperatorClass{
			Schema: r.schema, Name: r.name, AccessMethod: r.am,
			FamilySchema: famSchema, FamilyName: famName,
			Body: canonicalDDL(sb.String()), Comment: r.comment, Reconstructed: true,
		})
	}
	return out, nil
}

// opClassMembers returns the rendered OPERATOR and FUNCTION member clauses that
// belong to the operator class identified by opcOID. Membership is determined by
// an internal pg_depend link from the pg_amop/pg_amproc row to the opclass, which
// is exactly the set a CREATE OPERATOR CLASS ... AS clause established (members
// later added at family scope via ALTER depend on the family, not the class).
func opClassMembers(ctx context.Context, conn pipeline.Querier, opcOID uint32) ([]string, error) {
	var members []string

	const opQ = `
SELECT ao.amopstrategy,
       opn.nspname AS opr_schema, op.oprname AS opr_name,
       ao.amoplefttype::regtype::text  AS lt,
       ao.amoprighttype::regtype::text AS rt,
       ao.amoppurpose::text,
       (SELECT sfn.nspname || '.' || sf.opfname
        FROM   pg_opfamily sf
        JOIN   pg_namespace sfn ON sfn.oid = sf.opfnamespace
        WHERE  sf.oid = ao.amopsortfamily) AS sort_family
FROM   pg_amop ao
JOIN   pg_depend d  ON d.classid = 'pg_amop'::regclass AND d.objid = ao.oid
                   AND d.refclassid = 'pg_opclass'::regclass AND d.refobjid = $1
                   AND d.deptype = 'i'
JOIN   pg_operator op  ON op.oid = ao.amopopr
JOIN   pg_namespace opn ON opn.oid = op.oprnamespace
ORDER  BY ao.amopstrategy, ao.amoplefttype, ao.amoprighttype`

	rs, err := conn.QueryRows(ctx, opQ, opcOID)
	if err != nil {
		return nil, fmt.Errorf("introspect operator class operators: %w", err)
	}
	for rs.Next() {
		var strategy int
		var oprSchema, oprName, lt, rt string
		var purpose string
		var sortFamily *string
		if err := rs.Scan(&strategy, &oprSchema, &oprName, &lt, &rt, &purpose, &sortFamily); err != nil {
			rs.Close()
			return nil, err
		}
		clause := fmt.Sprintf("OPERATOR %d %s (%s, %s)", strategy, operatorRef(oprSchema, oprName), lt, rt)
		if purpose == "o" && sortFamily != nil {
			clause += " FOR ORDER BY " + *sortFamily
		}
		members = append(members, clause)
	}
	if err := rs.Err(); err != nil {
		rs.Close()
		return nil, err
	}
	rs.Close()

	const fnQ = `
SELECT ap.amprocnum,
       ap.amproc::regprocedure::text  AS fn,
       ap.amproclefttype::regtype::text  AS lt,
       ap.amprocrighttype::regtype::text AS rt
FROM   pg_amproc ap
JOIN   pg_depend d ON d.classid = 'pg_amproc'::regclass AND d.objid = ap.oid
                  AND d.refclassid = 'pg_opclass'::regclass AND d.refobjid = $1
                  AND d.deptype = 'i'
ORDER  BY ap.amprocnum, ap.amproclefttype, ap.amprocrighttype`

	rs2, err := conn.QueryRows(ctx, fnQ, opcOID)
	if err != nil {
		return nil, fmt.Errorf("introspect operator class functions: %w", err)
	}
	for rs2.Next() {
		var support int
		var fn, lt, rt string
		if err := rs2.Scan(&support, &fn, &lt, &rt); err != nil {
			rs2.Close()
			return nil, err
		}
		members = append(members, fmt.Sprintf("FUNCTION %d (%s, %s) %s", support, lt, rt, fn))
	}
	if err := rs2.Err(); err != nil {
		rs2.Close()
		return nil, err
	}
	rs2.Close()

	return members, nil
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
