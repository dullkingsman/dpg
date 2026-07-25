package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/format"
	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func TestObjectSchema(t *testing.T) {
	cases := []struct {
		obj  pipeline.IRObject
		want string
	}{
		{&ir.Table{Schema: "public"}, "public"},
		{&ir.View{Schema: "reports"}, "reports"},
		{&ir.Function{Schema: "util"}, "util"},
		{&ir.Procedure{Schema: "util"}, "util"},
		{&ir.Aggregate{Schema: "stats"}, "stats"},
		{&ir.Type{Schema: "domain"}, "domain"},
		{&ir.Sequence{Schema: "public"}, "public"},
		{&ir.Role{}, ""},
		{&ir.Schema{}, ""},
	}
	for _, tc := range cases {
		got := objectSchema(tc.obj)
		if got != tc.want {
			t.Errorf("objectSchema(%T) = %q, want %q", tc.obj, got, tc.want)
		}
	}
}

// TestRenderOpaqueObjectsCompile guards the no-verb mandate: dump renders
// introspected opaque objects (whose Body is a canonical "CREATE …" statement)
// as DPG declarations, which MUST NOT begin with CREATE and MUST re-compile.
func TestRenderOpaqueObjectsCompile(t *testing.T) {
	fmtOpts := format.Options{IndentSize: 4, KeywordCase: "upper"}

	objs := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "t", Columns: []*ir.Column{
			{Name: "a", Type: ir.TypeRef{Name: "int4"}},
			{Name: "b", Type: ir.TypeRef{Name: "int4"}},
		}},
		&ir.ForeignDataWrapper{Name: "dummy_fdw", Body: "CREATE FOREIGN DATA WRAPPER dummy_fdw"},
		&ir.ForeignServer{Name: "dummy_srv", Body: "CREATE SERVER dummy_srv FOREIGN DATA WRAPPER dummy_fdw"},
		&ir.UserMapping{Server: "dummy_srv", Body: "CREATE USER MAPPING FOR PUBLIC SERVER dummy_srv"},
		&ir.Publication{Name: "my_pub", Body: "CREATE PUBLICATION my_pub FOR ALL TABLES"},
		&ir.EventTrigger{Name: "et", Body: "CREATE EVENT TRIGGER et ON ddl_command_start EXECUTE FUNCTION f()"},
		&ir.Collation{Schema: "public", Name: "my_coll", Body: "CREATE COLLATION public.my_coll (locale = 'C')"},
		&ir.Cast{SourceType: ir.TypeRef{Name: "int4"}, TargetType: ir.TypeRef{Name: "bool"},
			Body: "CREATE CAST (integer AS boolean) WITHOUT FUNCTION"},
		&ir.StatisticsObject{Schema: "public", Name: "st",
			Body: "CREATE STATISTICS public.st (dependencies) ON a, b FROM public.t"},
		&ir.Tablespace{Name: "ts", Body: "CREATE TABLESPACE ts LOCATION '/tmp/x'"},
	}

	var b strings.Builder
	for _, o := range objs {
		renderObjectDPG(&b, o, fmtOpts)
	}
	rendered := b.String()

	// The no-verb mandate: no declaration may begin with CREATE.
	for line := range strings.SplitSeq(rendered, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "CREATE ") {
			t.Errorf("rendered declaration begins with CREATE (violates no-verb mandate): %q", line)
		}
	}

	dir := t.TempDir()
	f := filepath.Join(dir, "objects.dpg")
	if err := os.WriteFile(f, []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("dumped .dpg failed to recompile: %v\n---\n%s", err, rendered)
	}

	// Every opaque object must survive the round-trip.
	wantKinds := map[string]bool{
		"*ir.ForeignDataWrapper": false, "*ir.ForeignServer": false, "*ir.UserMapping": false,
		"*ir.Publication": false, "*ir.EventTrigger": false, "*ir.Collation": false,
		"*ir.Cast": false, "*ir.StatisticsObject": false, "*ir.Tablespace": false,
	}
	for _, o := range compiled {
		k := reflect.TypeOf(o).String()
		if _, ok := wantKinds[k]; ok {
			wantKinds[k] = true
		}
	}
	for k, seen := range wantKinds {
		if !seen {
			t.Errorf("kind %s missing after recompile", k)
		}
	}

	// Every opaque object must carry a Body after recompile — an empty one aborts
	// createOpaque ("body not captured") and would break plan/apply for the whole
	// database. The differ's create path is the direct guard: running it against
	// an empty snapshot exercises createOpaque for each object.
	if _, err := diff.New().Diff(compiled, &pipeline.Snapshot{}); err != nil {
		t.Fatalf("diffing dumped opaque objects failed (empty body?): %v", err)
	}
}

func TestIsClusterScoped(t *testing.T) {
	cluster := []pipeline.IRObject{&ir.Role{Name: "r"}, &ir.Tablespace{Name: "ts"}}
	database := []pipeline.IRObject{
		&ir.ForeignDataWrapper{Name: "f"}, &ir.ForeignServer{Name: "s"},
		&ir.UserMapping{Server: "s"}, &ir.Publication{Name: "p"},
		&ir.EventTrigger{Name: "e"}, &ir.Cast{}, &ir.Collation{Name: "c"},
		&ir.Table{Schema: "public", Name: "t"},
	}
	for _, o := range cluster {
		if !isClusterScoped(o) {
			t.Errorf("%T should be cluster-scoped", o)
		}
	}
	for _, o := range database {
		if isClusterScoped(o) {
			t.Errorf("%T should NOT be cluster-scoped", o)
		}
	}
	// excludeClusterScoped keeps exactly the database objects.
	kept := excludeClusterScoped(append(append([]pipeline.IRObject{}, cluster...), database...))
	if len(kept) != len(database) {
		t.Errorf("excludeClusterScoped kept %d, want %d", len(kept), len(database))
	}
}
