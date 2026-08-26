//go:build integration

package introspect_test

import (
	"context"
	"testing"

	"github.com/thec1oud/dpg/internal/executor"
	"github.com/thec1oud/dpg/internal/introspect"
	"github.com/thec1oud/dpg/internal/ir"
	"github.com/thec1oud/dpg/internal/pipeline"
	"github.com/thec1oud/dpg/internal/testpg"
)

// TestIntrospectSecurityLabels is the live-catalog guard for RFC Section 14.11
// across every kind PostgreSQL's real SECURITY LABEL statement supports:
// previously no introspect function for this existed at all, so a live
// label was always silently invisible to `dpg dump`/`plan --live`.
//
// Labels are written directly into pg_seclabel/pg_shseclabel via INSERT
// rather than the real SECURITY LABEL statement: PostgreSQL refuses to
// execute SECURITY LABEL at all unless a label provider is loaded via
// shared_preload_libraries (confirmed live against a real dummy-provider
// build — see the dummy_seclabel contrib module), which the plain
// postgres:17 image this suite's testpg container uses does not have.
// Direct INSERT is a faithful substitute for testing the READ path
// specifically (confirmed live: a superuser can INSERT into either catalog
// with no provider loaded at all — nothing in Postgres enforces "a real
// provider set this row" at the storage layer, only the SECURITY LABEL
// statement's own execution path does), which is exactly what this test
// exercises — the SQL this package generates to WRITE a label (RFC Section 14.11's
// other half) is covered separately and directly against a real loaded
// provider by internal/diff's SecurityLabel test suite plus this feature's
// original live verification (see project memory).
//
// The two-catalog split itself is the single easiest thing to get wrong
// here (found live while building this): ROLE, TABLESPACE, and
// SUBSCRIPTION are cluster-wide/shared catalogs (pg_authid, pg_tablespace,
// pg_subscription — confirmed via pg_class.relisshared, not assumed from
// their per-database-looking CREATE syntax) and store SECURITY LABEL in
// pg_shseclabel, not pg_seclabel like every other kind here.
func TestIntrospectSecurityLabels(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	ddl := []string{
		`CREATE SCHEMA myschema`,
		`CREATE TABLE myschema.mytab (id integer, name text)`,
		`CREATE VIEW myschema.myview AS SELECT id FROM myschema.mytab`,
		`CREATE MATERIALIZED VIEW myschema.mymatview AS SELECT id FROM myschema.mytab`,
		`CREATE FUNCTION myschema.myfunc(a integer) RETURNS integer LANGUAGE sql AS 'SELECT a'`,
		`CREATE PROCEDURE myschema.myproc(a integer) LANGUAGE sql AS 'SELECT a'`,
		`CREATE AGGREGATE myschema.myagg (integer) (SFUNC = int4pl, STYPE = integer, INITCOND = '0')`,
		`CREATE DOMAIN myschema.mydom AS integer`,
		`CREATE TYPE myschema.myenum AS ENUM ('a','b')`,
		`CREATE SEQUENCE myschema.myseq`,
		`CREATE ROLE mysecrole`,
		`CREATE FUNCTION myschema.myevtfunc() RETURNS event_trigger LANGUAGE plpgsql AS $$ BEGIN END; $$`,
		`CREATE EVENT TRIGGER myevt ON ddl_command_start EXECUTE FUNCTION myschema.myevtfunc()`,
		`CREATE PUBLICATION mypub FOR TABLE myschema.mytab`,
	}
	for _, stmt := range ddl {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	// pg_seclabel: per-database kinds. objsubid is the column's attnum for
	// the COLUMN row (2 == "name", the table's 2nd attribute), 0 for
	// everything else.
	perDatabase := []string{
		`INSERT INTO pg_seclabel VALUES ('myschema'::regnamespace, 'pg_namespace'::regclass, 0, 'dummy', 'classified')`,
		`INSERT INTO pg_seclabel VALUES ('myschema.mytab'::regclass, 'pg_class'::regclass, 0, 'dummy', 'classified')`,
		`INSERT INTO pg_seclabel VALUES ('myschema.mytab'::regclass, 'pg_class'::regclass, 2, 'dummy', 'secret')`,
		`INSERT INTO pg_seclabel VALUES ('myschema.myview'::regclass, 'pg_class'::regclass, 0, 'dummy', 'classified')`,
		`INSERT INTO pg_seclabel VALUES ('myschema.mymatview'::regclass, 'pg_class'::regclass, 0, 'dummy', 'classified')`,
		`INSERT INTO pg_seclabel VALUES ('myschema.myfunc(integer)'::regprocedure, 'pg_proc'::regclass, 0, 'dummy', 'classified')`,
		`INSERT INTO pg_seclabel VALUES ('myschema.myproc(integer)'::regprocedure, 'pg_proc'::regclass, 0, 'dummy', 'classified')`,
		`INSERT INTO pg_seclabel VALUES ('myschema.myagg(integer)'::regprocedure, 'pg_proc'::regclass, 0, 'dummy', 'classified')`,
		`INSERT INTO pg_seclabel VALUES ('myschema.mydom'::regtype, 'pg_type'::regclass, 0, 'dummy', 'classified')`,
		`INSERT INTO pg_seclabel VALUES ('myschema.myenum'::regtype, 'pg_type'::regclass, 0, 'dummy', 'classified')`,
		`INSERT INTO pg_seclabel VALUES ('myschema.myseq'::regclass, 'pg_class'::regclass, 0, 'dummy', 'classified')`,
		`INSERT INTO pg_seclabel SELECT oid, 'pg_event_trigger'::regclass, 0, 'dummy', 'classified' FROM pg_event_trigger WHERE evtname = 'myevt'`,
		`INSERT INTO pg_seclabel SELECT oid, 'pg_publication'::regclass, 0, 'dummy', 'classified' FROM pg_publication WHERE pubname = 'mypub'`,
	}
	for _, stmt := range perDatabase {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	// pg_shseclabel: the two shared/cluster-wide kinds this test covers
	// (subscription needs a publisher connection this suite doesn't set up,
	// so it's left to the differ-layer tests + original live verification).
	shared := []string{
		`INSERT INTO pg_shseclabel VALUES ('mysecrole'::regrole, 'pg_authid'::regclass, 'dummy', 'classified')`,
	}
	for _, stmt := range shared {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	ci := introspect.New()
	objects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	wantOne := func(t *testing.T, kind string, labels []pipeline.SecurityLabel, wantLabel string) {
		t.Helper()
		if len(labels) != 1 || labels[0].Provider != "dummy" || labels[0].Label != wantLabel {
			t.Errorf("%s: SecurityLabels = %+v, want [{dummy %s}]", kind, labels, wantLabel)
		}
	}

	found := map[string]bool{}
	for _, o := range objects {
		switch v := o.(type) {
		case *ir.Schema:
			if v.Name == "myschema" {
				found["schema"] = true
				wantOne(t, "schema", v.SecurityLabels, "classified")
			}
		case *ir.Table:
			if v.Schema == "myschema" && v.Name == "mytab" {
				found["table"] = true
				wantOne(t, "table", v.SecurityLabels, "classified")
				for _, c := range v.Columns {
					if c.Name == "name" {
						found["column"] = true
						wantOne(t, "column", c.SecurityLabels, "secret")
					}
				}
			}
		case *ir.View:
			if v.Schema == "myschema" && v.Name == "myview" && !v.Materialized {
				found["view"] = true
				wantOne(t, "view", v.SecurityLabels, "classified")
			}
			if v.Schema == "myschema" && v.Name == "mymatview" && v.Materialized {
				found["matview"] = true
				wantOne(t, "matview", v.SecurityLabels, "classified")
			}
		case *ir.Function:
			if v.Schema == "myschema" && v.Name == "myfunc" {
				found["function"] = true
				wantOne(t, "function", v.SecurityLabels, "classified")
			}
		case *ir.Procedure:
			if v.Schema == "myschema" && v.Name == "myproc" {
				found["procedure"] = true
				wantOne(t, "procedure", v.SecurityLabels, "classified")
			}
		case *ir.Aggregate:
			if v.Schema == "myschema" && v.Name == "myagg" {
				found["aggregate"] = true
				wantOne(t, "aggregate", v.SecurityLabels, "classified")
			}
		case *ir.Type:
			if v.Schema == "myschema" && v.Name == "mydom" && v.Variant == "DOMAIN" {
				found["domain"] = true
				wantOne(t, "domain", v.SecurityLabels, "classified")
			}
			if v.Schema == "myschema" && v.Name == "myenum" && v.Variant == "ENUM" {
				found["enum_type"] = true
				wantOne(t, "enum_type", v.SecurityLabels, "classified")
			}
		case *ir.Sequence:
			if v.Schema == "myschema" && v.Name == "myseq" {
				found["sequence"] = true
				wantOne(t, "sequence", v.SecurityLabels, "classified")
			}
		case *ir.Role:
			if v.Name == "mysecrole" {
				found["role"] = true
				wantOne(t, "role", v.SecurityLabels, "classified")
			}
		case *ir.EventTrigger:
			if v.Name == "myevt" {
				found["event_trigger"] = true
				wantOne(t, "event_trigger", v.SecurityLabels, "classified")
			}
		case *ir.Publication:
			if v.Name == "mypub" {
				found["publication"] = true
				wantOne(t, "publication", v.SecurityLabels, "classified")
			}
		}
	}

	want := []string{"schema", "table", "column", "view", "matview", "function",
		"procedure", "aggregate", "domain", "enum_type", "sequence", "role",
		"event_trigger", "publication"}
	for _, w := range want {
		if !found[w] {
			t.Errorf("kind %q: object not found among introspected results", w)
		}
	}
}

// TestIntrospectTablespaceSecurityLabel is TABLESPACE's own dedicated test,
// separate from the main suite above: CREATE TABLESPACE requires a real,
// already-existing filesystem directory the container must provide (see
// testpg.StartWithContainer's doc comment on the same requirement
// elsewhere in this package), which the shared myschema fixture doesn't
// set up.
func TestIntrospectTablespaceSecurityLabel(t *testing.T) {
	connStr, container := testpg.StartWithContainer(t)
	ctx := context.Background()

	const tsDir = "/var/lib/postgresql/dpg_seclabel_ts"
	if code, _, err := container.Exec(ctx, []string{"mkdir", "-p", tsDir}); err != nil || code != 0 {
		t.Fatalf("mkdir tablespace dir in container: code=%d err=%v", code, err)
	}
	if code, _, err := container.Exec(ctx, []string{"chown", "postgres:postgres", tsDir}); err != nil || code != 0 {
		t.Fatalf("chown tablespace dir in container: code=%d err=%v", code, err)
	}

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	ddl := []string{
		`CREATE TABLESPACE myts LOCATION '` + tsDir + `'`,
		`INSERT INTO pg_shseclabel SELECT oid, 'pg_tablespace'::regclass, 'dummy', 'classified' FROM pg_tablespace WHERE spcname = 'myts'`,
	}
	for _, stmt := range ddl {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	ci := introspect.New()
	objects, err := ci.Introspect(ctx, conn)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	var found *ir.Tablespace
	for _, o := range objects {
		if ts, ok := o.(*ir.Tablespace); ok && ts.Name == "myts" {
			found = ts
		}
	}
	if found == nil {
		t.Fatal("tablespace myts not found among introspected results")
	}
	if len(found.SecurityLabels) != 1 || found.SecurityLabels[0].Provider != "dummy" || found.SecurityLabels[0].Label != "classified" {
		t.Errorf("SecurityLabels = %+v, want [{dummy classified}]", found.SecurityLabels)
	}
}
