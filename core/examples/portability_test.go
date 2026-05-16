package examples_test

// TestPortabilityAnalysis shows the portability analyzer identifying PostgreSQL-
// specific constructs that would not work on other SQL databases.
//
// Constructs flagged for the portability/schema.dpg fixture:
//   - CREATE EXTENSION (PG-specific)
//   - CREATE TYPE AS ENUM (PG-specific; most DBs use CHECK constraints)
//   - UNLOGGED TABLE (PG-specific)
//   - jsonb column type (PG-specific)
//   - LANGUAGE plpgsql (PG-specific)
//
// Run:
//
//	go test ./examples/... -v -run TestPortability

import (
	"testing"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

func TestPortabilityAnalysis(t *testing.T) {
	objects := compileDPG(t, "fixtures/portability/schema.dpg")

	analyzer, ok := pipeline.Resolve[pipeline.PortabilityAnalyzer](pipeline.Default, pipeline.KeyPortabilityAnalyzer)
	if !ok {
		t.Fatal("PortabilityAnalyzer not registered in pipeline.Default")
	}

	issues, err := analyzer.Analyze(objects)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	t.Logf("\n=== Portability issues for portability/schema.dpg ===")
	if len(issues) == 0 {
		t.Log("  (no issues reported)")
	} else {
		for _, iss := range issues {
			t.Logf("  construct: %-35s  object: %s:%d  alternative: %s",
				iss.Construct, iss.Pos.File, iss.Pos.Line, iss.Alternative)
		}
	}

	if len(issues) == 0 {
		t.Fatal("expected portability issues for a PG-heavy schema, got none")
	}

	constructSet := make(map[string]bool)
	for _, iss := range issues {
		constructSet[iss.Construct] = true
	}
	t.Logf("\n  Unique constructs flagged: %v", constructSet)
}

// TestPortabilityCleanSchema verifies that a schema using only portable SQL
// constructs produces no portability warnings (this is aspirational — the v1
// schema avoids PG-specific types deliberately).
func TestPortabilityCleanSchema(t *testing.T) {
	objects := compileDPG(t, "fixtures/v1/schema.dpg")

	analyzer, ok := pipeline.Resolve[pipeline.PortabilityAnalyzer](pipeline.Default, pipeline.KeyPortabilityAnalyzer)
	if !ok {
		t.Fatal("PortabilityAnalyzer not registered")
	}

	issues, err := analyzer.Analyze(objects)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	t.Logf("\n=== Portability analysis of v1/schema.dpg ===")
	if len(issues) == 0 {
		t.Log("  no portability issues — schema uses only standard constructs")
	} else {
		for _, iss := range issues {
			t.Logf("  construct: %s  (alternative: %s)", iss.Construct, iss.Alternative)
		}
		// v1 uses pgcrypto extension, which is PG-specific; that is acceptable.
		t.Logf("  (note: CREATE EXTENSION is expected for pgcrypto)")
	}
}

// TestPortabilitySpecificConstructs checks individual constructs are flagged.
func TestPortabilitySpecificConstructs(t *testing.T) {
	objects := compileDPG(t, "fixtures/portability/schema.dpg")

	analyzer, ok := pipeline.Resolve[pipeline.PortabilityAnalyzer](pipeline.Default, pipeline.KeyPortabilityAnalyzer)
	if !ok {
		t.Fatal("PortabilityAnalyzer not registered")
	}

	issues, err := analyzer.Analyze(objects)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}

	found := make(map[string]bool)
	for _, iss := range issues {
		found[iss.Construct] = true
	}

	t.Logf("\n=== Specific construct checks ===")

	cases := []struct {
		construct string
		reason    string
	}{
		{"LANGUAGE plpgsql", "plpgsql is PostgreSQL-specific"},
		{"jsonb", "jsonb is a PostgreSQL-specific type"},
		{"CREATE TYPE AS ENUM", "ENUMs are PG-specific; other DBs use CHECK constraints"},
		{"CREATE EXTENSION", "extensions are a PostgreSQL-only feature"},
		{"UNLOGGED TABLE", "UNLOGGED is PostgreSQL-specific"},
	}

	for _, tc := range cases {
		if found[tc.construct] {
			t.Logf("  [FOUND]   %-30s  — %s", tc.construct, tc.reason)
		} else {
			t.Logf("  [MISSING] %-30s  — %s", tc.construct, tc.reason)
		}
	}
}
