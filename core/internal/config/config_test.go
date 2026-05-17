package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dullkingsman/dpg/internal/config"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// ── RootConfig defaults ───────────────────────────────────────────────────────

func TestDefaultRootConfig(t *testing.T) {
	cfg := config.DefaultRootConfig()
	if cfg.Compiler.DefaultDropBehavior != "restrict" {
		t.Errorf("DefaultDropBehavior: got %q", cfg.Compiler.DefaultDropBehavior)
	}
	if !cfg.Compiler.ConcurrentIndexes {
		t.Error("ConcurrentIndexes: expected true")
	}
	if !cfg.Linter.WarnOnDeprecated {
		t.Error("WarnOnDeprecated: expected true")
	}
	if cfg.Linter.MaxColumnsPerTable != 50 {
		t.Errorf("MaxColumnsPerTable: got %d", cfg.Linter.MaxColumnsPerTable)
	}
	if cfg.Fmt.IndentSize != 4 {
		t.Errorf("Fmt.IndentSize: got %d", cfg.Fmt.IndentSize)
	}
	if cfg.Fmt.KeywordCase != "upper" {
		t.Errorf("Fmt.KeywordCase: got %q", cfg.Fmt.KeywordCase)
	}
	if cfg.Snapshots.Directory != ".dpg/snapshots" {
		t.Errorf("Snapshots.Directory: got %q", cfg.Snapshots.Directory)
	}
	if cfg.Migrations.Directory != ".dpg/migrations" {
		t.Errorf("Migrations.Directory: got %q", cfg.Migrations.Directory)
	}
}

// ── LoadRoot ──────────────────────────────────────────────────────────────────

func TestLoadRoot_MinimalFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dpg.toml", `
[compiler]
default_drop_behavior = "cascade"
`)
	cfg, err := config.LoadRoot(dir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	if cfg.Compiler.DefaultDropBehavior != "cascade" {
		t.Errorf("DefaultDropBehavior: got %q", cfg.Compiler.DefaultDropBehavior)
	}
	// Unset fields should retain defaults.
	if cfg.Fmt.IndentSize != 4 {
		t.Errorf("Fmt.IndentSize should default to 4, got %d", cfg.Fmt.IndentSize)
	}
}

func TestLoadRoot_FmtSection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dpg.toml", `
[compiler]
default_drop_behavior = "restrict"

[fmt]
indent = 2
keyword_case = "lower"
`)
	cfg, err := config.LoadRoot(dir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	if cfg.Fmt.IndentSize != 2 {
		t.Errorf("Fmt.IndentSize: got %d", cfg.Fmt.IndentSize)
	}
	if cfg.Fmt.KeywordCase != "lower" {
		t.Errorf("Fmt.KeywordCase: got %q", cfg.Fmt.KeywordCase)
	}
}

func TestLoadRoot_InvalidDropBehavior(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dpg.toml", `
[compiler]
default_drop_behavior = "explode"
`)
	_, err := config.LoadRoot(dir)
	if err == nil {
		t.Fatal("expected error for invalid default_drop_behavior")
	}
}

func TestLoadRoot_MissingFile(t *testing.T) {
	_, err := config.LoadRoot(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing dpg.toml")
	}
}

// ── LoadCluster ───────────────────────────────────────────────────────────────

func TestLoadCluster_Basic(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "dpg.toml", `
[cluster]
name = "prod"
url = "postgres://localhost/prod"
`)
	cfg, err := config.LoadCluster(path)
	if err != nil {
		t.Fatalf("LoadCluster: %v", err)
	}
	if cfg.Cluster.Name != "prod" {
		t.Errorf("Name: got %q", cfg.Cluster.Name)
	}
	if cfg.Cluster.URL != "postgres://localhost/prod" {
		t.Errorf("URL: got %q", cfg.Cluster.URL)
	}
	// ClusterObjectsDir should default to "cluster".
	if cfg.Cluster.ClusterObjectsDir != "cluster" {
		t.Errorf("ClusterObjectsDir: got %q", cfg.Cluster.ClusterObjectsDir)
	}
}

func TestLoadCluster_MutuallyExclusiveURLAndLink(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "dpg.toml", `
[cluster]
name = "prod"
url  = "postgres://localhost/prod"
link = "env:PROD_URL"
`)
	_, err := config.LoadCluster(path)
	if err == nil {
		t.Fatal("expected error: url and link are mutually exclusive")
	}
}

func TestLoadCluster_ConnectionURL_PrefersURL(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "dpg.toml", `
[cluster]
name = "x"
url  = "postgres://localhost/x"
`)
	cfg, _ := config.LoadCluster(path)
	if cfg.Cluster.ConnectionURL() != "postgres://localhost/x" {
		t.Errorf("ConnectionURL(): got %q", cfg.Cluster.ConnectionURL())
	}
}

func TestLoadCluster_ConnectionURL_FallsBackToLink(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "dpg.toml", `
[cluster]
name = "x"
link = "env:X_URL"
`)
	cfg, _ := config.LoadCluster(path)
	if cfg.Cluster.ConnectionURL() != "env:X_URL" {
		t.Errorf("ConnectionURL(): got %q", cfg.Cluster.ConnectionURL())
	}
}

// ── LoadDatabase ──────────────────────────────────────────────────────────────

func TestLoadDatabase_Basic(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "dpg.toml", `
[database]
name = "mydb"
default_schema = "app"
`)
	cfg, err := config.LoadDatabase(path)
	if err != nil {
		t.Fatalf("LoadDatabase: %v", err)
	}
	if cfg.Database.Name != "mydb" {
		t.Errorf("Name: got %q", cfg.Database.Name)
	}
	if cfg.Database.DefaultSchema != "app" {
		t.Errorf("DefaultSchema: got %q", cfg.Database.DefaultSchema)
	}
}

// ── NameMapsConfig ────────────────────────────────────────────────────────────

func TestNameMaps_GlobalRules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dpg.toml", `
[compiler]
default_drop_behavior = "restrict"

[namemaps]
default = "LOWER_SNAKE_CASE"
prisma  = "LOWER_CAMEL_CASE"
`)
	cfg, err := config.LoadRoot(dir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	if cfg.NameMaps.Global["default"] != "LOWER_SNAKE_CASE" {
		t.Errorf("Global[default]: got %q", cfg.NameMaps.Global["default"])
	}
	if cfg.NameMaps.Global["prisma"] != "LOWER_CAMEL_CASE" {
		t.Errorf("Global[prisma]: got %q", cfg.NameMaps.Global["prisma"])
	}
}

func TestNameMaps_ByTypeRules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dpg.toml", `
[compiler]
default_drop_behavior = "restrict"

[namemaps]
default = "LOWER_SNAKE_CASE"

[namemaps.column]
prisma   = "LOWER_CAMEL_CASE"
drizzle  = "LOWER_CAMEL_CASE"

[namemaps.table]
prisma = "UPPER_CAMEL_CASE"
`)
	cfg, err := config.LoadRoot(dir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	if cfg.NameMaps.Global["default"] != "LOWER_SNAKE_CASE" {
		t.Errorf("Global[default]: got %q", cfg.NameMaps.Global["default"])
	}
	colMap := cfg.NameMaps.ByType["column"]
	if colMap["prisma"] != "LOWER_CAMEL_CASE" {
		t.Errorf("ByType[column][prisma]: got %q", colMap["prisma"])
	}
	if colMap["drizzle"] != "LOWER_CAMEL_CASE" {
		t.Errorf("ByType[column][drizzle]: got %q", colMap["drizzle"])
	}
	tblMap := cfg.NameMaps.ByType["table"]
	if tblMap["prisma"] != "UPPER_CAMEL_CASE" {
		t.Errorf("ByType[table][prisma]: got %q", tblMap["prisma"])
	}
}

func TestNameMaps_InvalidRule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dpg.toml", `
[namemaps]
default = "SNAKE_LOWER"
`)
	_, err := config.LoadRoot(dir)
	if err == nil {
		t.Fatal("expected error for unknown rule SNAKE_LOWER, got nil")
	}
}

func TestNameMaps_InvalidRuleInByType(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dpg.toml", `
[namemaps.column]
prisma = "NOT_A_RULE"
`)
	_, err := config.LoadRoot(dir)
	if err == nil {
		t.Fatal("expected error for unknown rule in [namemaps.column], got nil")
	}
}

func TestNameMaps_EmptySection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dpg.toml", `
[compiler]
default_drop_behavior = "restrict"
`)
	cfg, err := config.LoadRoot(dir)
	if err != nil {
		t.Fatalf("LoadRoot: %v", err)
	}
	// NameMaps should be zero-value when no [namemaps] section is present.
	if len(cfg.NameMaps.Global) != 0 {
		t.Errorf("expected empty Global map, got %v", cfg.NameMaps.Global)
	}
	if len(cfg.NameMaps.ByType) != 0 {
		t.Errorf("expected empty ByType map, got %v", cfg.NameMaps.ByType)
	}
}

func TestNameMaps_ClusterConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "dpg.toml", `
[cluster]
name = "prod"
url  = "postgres://localhost/prod"

[namemaps]
default = "LOWER_SNAKE_CASE"

[namemaps.table]
prisma = "UPPER_CAMEL_CASE"
`)
	cfg, err := config.LoadCluster(path)
	if err != nil {
		t.Fatalf("LoadCluster: %v", err)
	}
	if cfg.NameMaps.Global["default"] != "LOWER_SNAKE_CASE" {
		t.Errorf("Global[default]: got %q", cfg.NameMaps.Global["default"])
	}
	if cfg.NameMaps.ByType["table"]["prisma"] != "UPPER_CAMEL_CASE" {
		t.Errorf("ByType[table][prisma]: got %q", cfg.NameMaps.ByType["table"]["prisma"])
	}
}

func TestNameMaps_DatabaseConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "dpg.toml", `
[database]
name = "mydb"

[namemaps.column]
default = "LOWER_CAMEL_CASE"
`)
	cfg, err := config.LoadDatabase(path)
	if err != nil {
		t.Fatalf("LoadDatabase: %v", err)
	}
	if cfg.NameMaps.ByType["column"]["default"] != "LOWER_CAMEL_CASE" {
		t.Errorf("ByType[column][default]: got %q", cfg.NameMaps.ByType["column"]["default"])
	}
}

func TestLoadDatabase_DefaultSchema(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "dpg.toml", `
[database]
name = "bare"
`)
	cfg, err := config.LoadDatabase(path)
	if err != nil {
		t.Fatalf("LoadDatabase: %v", err)
	}
	if cfg.Database.DefaultSchema != "public" {
		t.Errorf("DefaultSchema should default to 'public', got %q", cfg.Database.DefaultSchema)
	}
}
