package portability

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/ir"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func TestAnalyzeClean(t *testing.T) {
	a := New()
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "users", Columns: []*ir.Column{
			{Name: "id", Type: ir.TypeRef{Name: "integer"}},
		}},
	}
	issues, err := a.Analyze(objects)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Errorf("expected no issues, got %d", len(issues))
	}
}

func TestAnalyzeUnloggedTable(t *testing.T) {
	a := New()
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "cache", Unlogged: true},
	}
	issues, err := a.Analyze(objects)
	if err != nil {
		t.Fatal(err)
	}
	found := findConstruct(issues, "UNLOGGED TABLE")
	if !found {
		t.Fatal("expected UNLOGGED TABLE issue")
	}
}

func TestAnalyzeRLS(t *testing.T) {
	a := New()
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "docs", RLSEnabled: true},
	}
	issues, err := a.Analyze(objects)
	if err != nil {
		t.Fatal(err)
	}
	if !findConstruct(issues, "ROW LEVEL SECURITY") {
		t.Fatal("expected ROW LEVEL SECURITY issue")
	}
}

func TestAnalyzePGType(t *testing.T) {
	a := New()
	objects := []pipeline.IRObject{
		&ir.Table{Schema: "public", Name: "events", Columns: []*ir.Column{
			{Name: "payload", Type: ir.TypeRef{Name: "jsonb"}},
		}},
	}
	issues, err := a.Analyze(objects)
	if err != nil {
		t.Fatal(err)
	}
	if !findConstruct(issues, "jsonb") {
		t.Fatal("expected jsonb portability issue")
	}
}

func TestAnalyzePlPgSQL(t *testing.T) {
	a := New()
	objects := []pipeline.IRObject{
		&ir.Function{
			Schema: "public",
			Name:   "do_thing",
			Attrs:  ir.FuncAttrs{Language: "plpgsql", Body: "BEGIN END;"},
		},
	}
	issues, err := a.Analyze(objects)
	if err != nil {
		t.Fatal(err)
	}
	if !findConstruct(issues, "LANGUAGE plpgsql") {
		t.Fatal("expected LANGUAGE plpgsql issue")
	}
}

func TestAnalyzeEnum(t *testing.T) {
	a := New()
	objects := []pipeline.IRObject{
		&ir.Type{Schema: "public", Name: "mood", Variant: "ENUM", EnumValues: []string{"happy", "sad"}},
	}
	issues, err := a.Analyze(objects)
	if err != nil {
		t.Fatal(err)
	}
	if !findConstruct(issues, "CREATE TYPE AS ENUM") {
		t.Fatal("expected CREATE TYPE AS ENUM issue")
	}
}

func TestAnalyzeExtension(t *testing.T) {
	a := New()
	objects := []pipeline.IRObject{
		&ir.Extension{Name: "pgcrypto"},
	}
	issues, err := a.Analyze(objects)
	if err != nil {
		t.Fatal(err)
	}
	if !findConstruct(issues, "CREATE EXTENSION") {
		t.Fatal("expected CREATE EXTENSION issue")
	}
}

func TestAnalyzeRegistration(t *testing.T) {
	r, ok := pipeline.Resolve[pipeline.PortabilityAnalyzer](pipeline.Default, pipeline.KeyPortabilityAnalyzer)
	if !ok {
		t.Fatal("PortabilityAnalyzer not registered")
	}
	if r == nil {
		t.Fatal("registered PortabilityAnalyzer is nil")
	}
}

func findConstruct(issues []pipeline.PortabilityIssue, construct string) bool {
	for _, iss := range issues {
		if iss.Construct == construct {
			return true
		}
	}
	return false
}
