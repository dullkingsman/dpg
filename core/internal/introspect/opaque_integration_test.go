//go:build integration

package introspect_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/introspect"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// setupOpaque connects to a fresh container, executes the given DDL statements
// in order, and returns the introspected objects.
func setupOpaque(t *testing.T, ddl ...string) []pipeline.IRObject {
	t.Helper()
	connStr := testpg.Start(t)
	ctx := context.Background()
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	for _, stmt := range ddl {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	objects, err := introspect.New().Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	return objects
}

// TestIntrospectOperatorClassAlwaysHasExplicitFamily guards the operator-family
// general fix: a class created WITHOUT an explicit FAMILY clause (the common
// case — PostgreSQL auto-creates a same-named family) must now be introspected
// with the family as its own standalone *ir.OperatorFamily object AND the
// class's reconstructed body must carry an explicit FAMILY clause naming it —
// mirroring pg_dump's own model exactly (confirmed live via `pg_dump -s`
// before writing this fix: it always dumps a separate CREATE OPERATOR FAMILY,
// even for an auto-created one, and always gives the class an explicit
// FAMILY). This is the inverse of the old, removed behavior (which skipped
// same-name families and omitted the class's FAMILY clause) — see CHANGELOG
// for why that old heuristic was unreliable by construction, not just wrong
// in a rare case.
func TestIntrospectOperatorClassAlwaysHasExplicitFamily(t *testing.T) {
	objects := setupOpaque(t, `CREATE OPERATOR CLASS rt_auto_opc FOR TYPE integer USING btree AS
        OPERATOR 3 =, FUNCTION 1 btint4cmp(integer, integer)`)

	var fam *ir.OperatorFamily
	var cls *ir.OperatorClass
	for _, o := range objects {
		if f, ok := o.(*ir.OperatorFamily); ok && f.Name == "rt_auto_opc" {
			fam = f
		}
		if c, ok := o.(*ir.OperatorClass); ok && c.Name == "rt_auto_opc" {
			cls = c
		}
	}
	if fam == nil {
		t.Fatal("auto-created family rt_auto_opc was NOT introspected as a standalone object (expected it to be, now)")
	}
	if fam.AccessMethod != "btree" {
		t.Errorf("family AccessMethod: got %q, want btree", fam.AccessMethod)
	}
	if cls == nil {
		t.Fatal("operator class rt_auto_opc was not introspected")
	}
	if !strings.Contains(strings.ToUpper(cls.Body), "FAMILY") {
		t.Errorf("class body does not name its family explicitly: %q", cls.Body)
	}
	if cls.FamilySchema != "public" || cls.FamilyName != "rt_auto_opc" {
		t.Errorf("class Family fields: got schema=%q name=%q, want schema=public name=rt_auto_opc", cls.FamilySchema, cls.FamilyName)
	}
}

// TestIntrospectOperatorFamilyExplicitSharingClassName is the direct
// regression guard for the ORIGINAL misclassification bug this fix closes: an
// EXPLICIT, separately-created family that happens to share its attached
// class's name (confirmed live that PostgreSQL's opclass→opfamily pg_depend
// row is deptype 'a' — DEPENDENCY_AUTO — for this case too, identical to a
// genuinely auto-created family, so no catalog signal alone could ever
// distinguish them). Before this fix, the old same-name heuristic silently
// dropped this family from the dump. Now, since every family is introspected
// unconditionally, this case needs no special handling at all — it "just
// works" the same as any other family.
func TestIntrospectOperatorFamilyExplicitSharingClassName(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE OPERATOR FAMILY same_name_ops USING btree`,
		`CREATE OPERATOR CLASS same_name_ops FOR TYPE integer USING btree FAMILY same_name_ops AS
            OPERATOR 3 =, FUNCTION 1 btint4cmp(integer, integer)`,
	)

	var fam *ir.OperatorFamily
	var cls *ir.OperatorClass
	for _, o := range objects {
		if f, ok := o.(*ir.OperatorFamily); ok && f.Name == "same_name_ops" {
			fam = f
		}
		if c, ok := o.(*ir.OperatorClass); ok && c.Name == "same_name_ops" {
			cls = c
		}
	}
	if fam == nil {
		t.Fatal("explicit family same_name_ops (sharing its class's name) was not introspected — the exact bug this fix closes")
	}
	if cls == nil {
		t.Fatal("operator class same_name_ops was not introspected")
	}
	if cls.FamilySchema != "public" || cls.FamilyName != "same_name_ops" {
		t.Errorf("class Family fields: got schema=%q name=%q, want schema=public name=same_name_ops", cls.FamilySchema, cls.FamilyName)
	}
}

// TestIntrospectTSConfigParserFields guards ParserSchema/ParserName wiring —
// needed for the new config→parser dependency edge in graph.go.
func TestIntrospectTSConfigParserFields(t *testing.T) {
	objects := setupOpaque(t, `CREATE TEXT SEARCH CONFIGURATION public.rt_cfg (PARSER = pg_catalog."default")`)
	var found *ir.TSConfig
	for _, o := range objects {
		if c, ok := o.(*ir.TSConfig); ok && c.Name == "rt_cfg" {
			found = c
		}
	}
	if found == nil {
		t.Fatal("TS config public.rt_cfg not found")
	}
	if found.ParserSchema != "pg_catalog" || found.ParserName != "default" {
		t.Errorf("Parser fields: got schema=%q name=%q, want schema=pg_catalog name=default", found.ParserSchema, found.ParserName)
	}
}

// TestIntrospectTSDictTemplateFields guards TemplateSchema/TemplateName
// wiring — needed for the new dict→template dependency edge in graph.go.
func TestIntrospectTSDictTemplateFields(t *testing.T) {
	objects := setupOpaque(t, `CREATE TEXT SEARCH DICTIONARY public.rt_dict (TEMPLATE = pg_catalog.simple)`)
	var found *ir.TSDict
	for _, o := range objects {
		if d, ok := o.(*ir.TSDict); ok && d.Name == "rt_dict" {
			found = d
		}
	}
	if found == nil {
		t.Fatal("TS dictionary public.rt_dict not found")
	}
	if found.TemplateSchema != "pg_catalog" || found.TemplateName != "simple" {
		t.Errorf("Template fields: got schema=%q name=%q, want schema=pg_catalog name=simple", found.TemplateSchema, found.TemplateName)
	}
}

func TestIntrospectCollation(t *testing.T) {
	objects := setupOpaque(t, `CREATE COLLATION public.mycoll (LOCALE = 'C')`)
	var found *ir.Collation
	for _, obj := range objects {
		if c, ok := obj.(*ir.Collation); ok && c.Name == "mycoll" && c.Schema == "public" {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("collation public.mycoll not found")
	}
	if !strings.Contains(found.Body, "CREATE COLLATION") || !strings.Contains(found.Body, "mycoll") {
		t.Errorf("unexpected collation body: %q", found.Body)
	}
}

func TestIntrospectCast(t *testing.T) {
	// A user-defined cast between a custom enum and integer (no built-in exists).
	objects := setupOpaque(t,
		`CREATE TYPE public.rgb AS ENUM ('r', 'g', 'b')`,
		`CREATE FUNCTION public.rgb_to_int(public.rgb) RETURNS integer AS 'SELECT 1' LANGUAGE sql IMMUTABLE`,
		`CREATE CAST (public.rgb AS integer) WITH FUNCTION public.rgb_to_int(public.rgb) AS ASSIGNMENT`)
	var found *ir.Cast
	for _, obj := range objects {
		if c, ok := obj.(*ir.Cast); ok && c.TargetType.Name == "integer" && strings.Contains(c.SourceType.Name, "rgb") {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("cast (rgb AS integer) not found")
	}
	if !strings.Contains(found.Body, "CREATE CAST") || !strings.Contains(found.Body, "rgb_to_int") {
		t.Errorf("unexpected cast body: %q", found.Body)
	}
}

func TestIntrospectStatistics(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE TABLE public.st (a int, b int)`,
		`CREATE STATISTICS public.st_stats (dependencies) ON a, b FROM public.st`)
	var found *ir.StatisticsObject
	for _, obj := range objects {
		if s, ok := obj.(*ir.StatisticsObject); ok && s.Name == "st_stats" {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatal("statistics public.st_stats not found")
	}
	if !strings.Contains(found.Body, "CREATE STATISTICS") {
		t.Errorf("unexpected statistics body: %q", found.Body)
	}
}

func TestIntrospectEventTrigger(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE FUNCTION public.log_ddl() RETURNS event_trigger AS 'BEGIN END' LANGUAGE plpgsql`,
		`CREATE EVENT TRIGGER my_et ON ddl_command_start EXECUTE FUNCTION public.log_ddl()`)
	var found *ir.EventTrigger
	for _, obj := range objects {
		if e, ok := obj.(*ir.EventTrigger); ok && e.Name == "my_et" {
			found = e
			break
		}
	}
	if found == nil {
		t.Fatal("event trigger my_et not found")
	}
	if !strings.Contains(found.Body, "CREATE EVENT TRIGGER") || !strings.Contains(found.Body, "ddl_command_start") {
		t.Errorf("unexpected event trigger body: %q", found.Body)
	}
}

func TestIntrospectForeignInfra(t *testing.T) {
	objects := setupOpaque(t,
		`CREATE FOREIGN DATA WRAPPER dummy_fdw`,
		`CREATE SERVER dummy_srv FOREIGN DATA WRAPPER dummy_fdw OPTIONS (host 'localhost')`,
		`CREATE USER MAPPING FOR PUBLIC SERVER dummy_srv OPTIONS ("user" 'alice')`)

	var fdw *ir.ForeignDataWrapper
	var srv *ir.ForeignServer
	var um *ir.UserMapping
	for _, obj := range objects {
		switch o := obj.(type) {
		case *ir.ForeignDataWrapper:
			if o.Name == "dummy_fdw" {
				fdw = o
			}
		case *ir.ForeignServer:
			if o.Name == "dummy_srv" {
				srv = o
			}
		case *ir.UserMapping:
			if o.Server == "dummy_srv" {
				um = o
			}
		}
	}
	if fdw == nil || !strings.Contains(fdw.Body, "CREATE FOREIGN DATA WRAPPER") {
		t.Errorf("foreign data wrapper not introspected: %+v", fdw)
	}
	if srv == nil || !strings.Contains(srv.Body, "dummy_fdw") {
		t.Errorf("foreign server not introspected: %+v", srv)
	}
	if um == nil || !strings.Contains(um.Body, "CREATE USER MAPPING") {
		t.Errorf("user mapping not introspected: %+v", um)
	}
	if um != nil && um.User != "" {
		t.Errorf("FOR PUBLIC user mapping should have empty User, got %q", um.User)
	}
}

func TestIntrospectPublication(t *testing.T) {
	objects := setupOpaque(t, `CREATE PUBLICATION my_pub FOR ALL TABLES`)
	var found *ir.Publication
	for _, obj := range objects {
		if p, ok := obj.(*ir.Publication); ok && p.Name == "my_pub" {
			found = p
			break
		}
	}
	if found == nil {
		t.Fatal("publication my_pub not found")
	}
	if !strings.Contains(found.Body, "FOR ALL TABLES") {
		t.Errorf("unexpected publication body: %q", found.Body)
	}
}

// TestIntrospectPublicationMixedTargets guards the single-FOR grammar: a
// publication over both a table AND a schema must reconstruct to one FOR clause.
// The buggy form (FOR TABLE … FOR TABLES IN SCHEMA …) is a syntax error that
// canonicalDDL would silently pass through, so re-executing the body catches it.
func TestIntrospectPublicationMixedTargets(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	for _, stmt := range []string{
		`CREATE SCHEMA s1`,
		`CREATE TABLE public.t1 (id int)`,
		`CREATE PUBLICATION mix_pub FOR TABLE public.t1, TABLES IN SCHEMA s1`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	objects, err := introspect.New().Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var found *ir.Publication
	for _, obj := range objects {
		if p, ok := obj.(*ir.Publication); ok && p.Name == "mix_pub" {
			found = p
			break
		}
	}
	if found == nil {
		t.Fatal("publication mix_pub not found")
	}
	// The reconstructed body must be valid SQL: drop and re-create it.
	if _, err := conn.Exec(ctx, `DROP PUBLICATION mix_pub`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := conn.Exec(ctx, found.Body); err != nil {
		t.Fatalf("reconstructed publication body is invalid SQL: %v\nbody: %s", err, found.Body)
	}
}

// TestIntrospectCollationFiltersSystem guards the namespace filter: initdb
// imports hundreds of system locale collations into pg_catalog with normal
// OIDs. A database with no user collations must yield zero introspected ones.
func TestIntrospectCollationFiltersSystem(t *testing.T) {
	objects := setupOpaque(t) // no user objects created
	for _, obj := range objects {
		if c, ok := obj.(*ir.Collation); ok {
			t.Errorf("system collation leaked into introspection: %s.%s", c.Schema, c.Name)
		}
	}
}

// TestIntrospectSubscription guards §6z/§6ff's closing piece: introspection
// must reconstruct every Subscription attribute except subconninfo (never
// selected — see subscriptionConnInfoPlaceholder's doc comment), non-default
// WITH options included, while never leaking the real connection string
// (which, unlike every other reliable-tier kind's Body, holds a real
// credential in the live catalog) anywhere into the reconstructed Body.
// connect = false avoids needing a real reachable publisher: this test
// proves introspection reads the row correctly, not that replication works
// (TestSubscriptionConnectionSecretRoundtrip in integration/ already proves
// a real replication connection separately).
func TestIntrospectSubscription(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.StartLogical(t)
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	for _, stmt := range []string{
		`CREATE PUBLICATION pub_a FOR ALL TABLES`,
		`CREATE SUBSCRIPTION my_sub CONNECTION ` +
			`'host=127.0.0.1 port=5432 dbname=dpgtest user=dpg password=supersecret' ` +
			`PUBLICATION pub_a WITH (connect = false, create_slot = false, ` +
			`binary = true, streaming = parallel, disable_on_error = true, ` +
			`password_required = false, synchronous_commit = remote_apply, ` +
			`slot_name = my_custom_slot)`,
		`COMMENT ON SUBSCRIPTION my_sub IS 'covers §6ff'`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	objects, err := introspect.New().Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	var found *ir.Subscription
	for _, obj := range objects {
		if s, ok := obj.(*ir.Subscription); ok && s.Name == "my_sub" {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatal("subscription my_sub not found")
	}
	if !found.Reconstructed {
		t.Error("expected Reconstructed = true")
	}
	if strings.Contains(found.Body, "supersecret") || strings.Contains(found.Body, "127.0.0.1") {
		t.Fatalf("introspected Body leaked the live connection string: %q", found.Body)
	}
	if found.ConnInfo != "" && strings.Contains(found.ConnInfo, "supersecret") {
		t.Fatalf("ConnInfo leaked the live connection string: %q", found.ConnInfo)
	}
	for _, want := range []string{
		// connect/create_slot/enabled are always forced false — the placeholder
		// CONNECTION is never dialable, see introspectSubscriptions' doc comment.
		"connect = false", "create_slot = false", "enabled = false",
		// canonicalDDL quotes "binary" (reserved word in this grammar context) —
		// not a bug, just how pg_query's own deparse renders it.
		`"binary" = true`, "streaming = parallel", "disable_on_error = true",
		"password_required = false", "synchronous_commit = 'remote_apply'",
		"slot_name = 'my_custom_slot'",
	} {
		if !strings.Contains(found.Body, want) {
			t.Errorf("expected Body to contain %q, got: %s", want, found.Body)
		}
	}
	if found.Comment == nil || *found.Comment != "covers §6ff" {
		t.Errorf("expected comment %q, got %v", "covers §6ff", found.Comment)
	}

	// The reconstructed body must be valid, re-executable SQL. Detach the
	// slot first: create_slot = false above means no physical replication
	// slot actually exists on a (nonexistent, connect = false) publisher for
	// PostgreSQL to drop, and a bare DROP SUBSCRIPTION always tries.
	if _, err := conn.Exec(ctx, `ALTER SUBSCRIPTION my_sub SET (slot_name = NONE)`); err != nil {
		t.Fatalf("detach slot before drop: %v", err)
	}
	if _, err := conn.Exec(ctx, `DROP SUBSCRIPTION my_sub`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := conn.Exec(ctx, found.Body); err != nil {
		t.Fatalf("reconstructed subscription body is invalid SQL: %v\nbody: %s", err, found.Body)
	}
}

// TestIntrospectSubscriptionCrossDatabaseIsolation guards the pg_subscription
// shared-catalog filter: pg_subscription lives in pg_global (confirmed
// live — every row from every database in the cluster is visible from any
// single database's connection, unlike every other reliable-tier catalog
// here, which is already database-local), so introspectSubscriptions MUST
// filter by subdbid explicitly. Without that filter, introspecting database
// A would leak database B's subscriptions into A's dump/plan --live.
func TestIntrospectSubscriptionCrossDatabaseIsolation(t *testing.T) {
	ctx := context.Background()
	connStr := testpg.StartLogical(t)
	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `CREATE DATABASE other_db`); err != nil {
		t.Fatalf("create other_db: %v", err)
	}
	otherConnStr := strings.Replace(connStr, "/dpgtest", "/other_db", 1)
	otherConn, err := executor.Connect(ctx, otherConnStr)
	if err != nil {
		t.Fatalf("connect other_db: %v", err)
	}
	defer otherConn.Close(ctx)
	for _, stmt := range []string{
		`CREATE PUBLICATION pub_other FOR ALL TABLES`,
		`CREATE SUBSCRIPTION other_sub CONNECTION 'host=127.0.0.1 port=5432 dbname=other_db user=dpg password=x' ` +
			`PUBLICATION pub_other WITH (connect = false, create_slot = false)`,
	} {
		if _, err := otherConn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q against other_db: %v", stmt, err)
		}
	}

	objects, err := introspect.New().Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect dpgtest: %v", err)
	}
	for _, obj := range objects {
		if s, ok := obj.(*ir.Subscription); ok {
			t.Errorf("other_db's subscription %q leaked into dpgtest's introspection", s.Name)
		}
	}
}
