package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/dullkingsman/dpg/internal/pipeline"
)

// NameMapsConfig holds the parsed [namemaps] configuration at any config level.
// Global maps tool name to rule for all object types (from direct key-value
// pairs in [namemaps]). ByType maps object-type name to (tool → rule), from
// [namemaps.<type>] subsections (e.g. [namemaps.column]).
// Only rule keywords are permitted at the config level; literal names may only
// be specified in block-level NAME MAP directives.
type NameMapsConfig struct {
	Global map[string]string
	ByType map[string]map[string]string
}

// UnmarshalTOML implements toml.Unmarshaler so that a mixed [namemaps] table
// (string values for global rules + subtables for per-type rules) decodes
// into the structured NameMapsConfig.
func (n *NameMapsConfig) UnmarshalTOML(data interface{}) error {
	m, ok := data.(map[string]interface{})
	if !ok {
		return nil
	}
	for k, v := range m {
		switch val := v.(type) {
		case string:
			rule := strings.ToUpper(val)
			if !pipeline.ValidNameMapRules[rule] {
				return fmt.Errorf("[namemaps]: unknown rule %q for tool %q", val, k)
			}
			if n.Global == nil {
				n.Global = make(map[string]string)
			}
			n.Global[k] = rule
		case map[string]interface{}:
			typeMap := make(map[string]string)
			for tool, ruleVal := range val {
				r, ok := ruleVal.(string)
				if !ok {
					return fmt.Errorf("[namemaps.%s]: expected string rule for tool %q", k, tool)
				}
				rule := strings.ToUpper(r)
				if !pipeline.ValidNameMapRules[rule] {
					return fmt.Errorf("[namemaps.%s]: unknown rule %q for tool %q", k, r, tool)
				}
				typeMap[tool] = rule
			}
			if n.ByType == nil {
				n.ByType = make(map[string]map[string]string)
			}
			n.ByType[k] = typeMap
		}
	}
	return nil
}

// RootConfig represents the contents of dpg.toml at the project root.
type RootConfig struct {
	Compiler   CompilerConfig   `toml:"compiler"`
	Linter     LinterConfig     `toml:"linter"`
	Fmt        FmtConfig        `toml:"fmt"`
	Snapshots  SnapshotsConfig  `toml:"snapshots"`
	Migrations MigrationsConfig `toml:"migrations"`
	NameMaps   NameMapsConfig   `toml:"namemaps"`
}

// FmtConfig holds formatter settings (dpg fmt).
type FmtConfig struct {
	// IndentSize is the number of spaces per indent level. Default: 4.
	IndentSize int `toml:"indent"`
	// KeywordCase controls keyword casing: "upper" (default) or "lower".
	KeywordCase string `toml:"keyword_case"`
}

// CompilerConfig holds compiler-wide defaults.
type CompilerConfig struct {
	// DefaultDropBehavior controls whether DROPs cascade or restrict.
	// Valid values: "restrict" (default), "cascade".
	DefaultDropBehavior string `toml:"default_drop_behavior"`
	// ConcurrentIndexes controls whether new indexes on existing tables are
	// created with CONCURRENTLY by default. Default: true.
	ConcurrentIndexes bool `toml:"concurrent_indexes"`
}

// LinterConfig holds the linter rule settings.
type LinterConfig struct {
	WarnOnDeprecated          bool `toml:"warn_on_deprecated"`
	RequireColumnComments     bool `toml:"require_column_comments"`
	ForbidHardcodedPasswords  bool `toml:"forbid_hardcoded_passwords"`
	MaxColumnsPerTable        int  `toml:"max_columns_per_table"`
	WarnOnScalarMergeConflict bool `toml:"warn_on_scalar_merge_conflict"`
}

// SnapshotsConfig controls snapshot file locations.
type SnapshotsConfig struct {
	// Directory is the path (relative to the project root) where snapshot
	// JSON files are stored. Default: ".dpg/snapshots".
	Directory string `toml:"directory"`
}

// MigrationsConfig controls where applied migration SQL files are archived.
type MigrationsConfig struct {
	// Directory is the path (relative to the project root) where applied
	// migration SQL files are written. Default: ".dpg/migrations".
	// Set to "" to disable migration file archiving.
	Directory string `toml:"directory"`
}

// DefaultRootConfig returns a RootConfig populated with the RFC defaults.
func DefaultRootConfig() RootConfig {
	return RootConfig{
		Compiler: CompilerConfig{
			DefaultDropBehavior: "restrict",
			ConcurrentIndexes:   true,
		},
		Linter: LinterConfig{
			WarnOnDeprecated:          true,
			RequireColumnComments:     false,
			ForbidHardcodedPasswords:  true,
			MaxColumnsPerTable:        50,
			WarnOnScalarMergeConflict: true,
		},
		Fmt: FmtConfig{
			IndentSize:  4,
			KeywordCase: "upper",
		},
		Snapshots: SnapshotsConfig{
			Directory: ".dpg/snapshots",
		},
		Migrations: MigrationsConfig{
			Directory: ".dpg/migrations",
		},
	}
}

// LoadRoot loads and parses dpg.toml from dir.
// Missing optional fields default to DefaultRootConfig values.
func LoadRoot(dir string) (RootConfig, error) {
	cfg := DefaultRootConfig()
	path := filepath.Join(dir, "dpg.toml")
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return RootConfig{}, fmt.Errorf("loading %s: %w", path, err)
	}
	if err := cfg.Compiler.validate(); err != nil {
		return RootConfig{}, fmt.Errorf("%s: [compiler]: %w", path, err)
	}
	return cfg, nil
}

func (c CompilerConfig) validate() error {
	switch c.DefaultDropBehavior {
	case "restrict", "cascade":
		return nil
	default:
		return fmt.Errorf("default_drop_behavior must be \"restrict\" or \"cascade\", got %q", c.DefaultDropBehavior)
	}
}

// ClusterConfig represents the dpg.toml file inside a cluster directory.
type ClusterConfig struct {
	Cluster  ClusterDef     `toml:"cluster"`
	NameMaps NameMapsConfig `toml:"namemaps"`
}

// ClusterDef holds the cluster connection and options.
type ClusterDef struct {
	Name string `toml:"name"`
	// ClusterObjectsDir is the subdirectory within the cluster directory
	// that holds cluster-level objects (roles, tablespaces, FDWs).
	// Default: "cluster". This name is reserved — no database may share it.
	ClusterObjectsDir string `toml:"cluster_objects_dir"`
	// URL is an inline PostgreSQL connection string for the primary node.
	// Mutually exclusive with Link. May be omitted for offline-only usage.
	URL string `toml:"url"`
	// Link is a secrets-provider URI (e.g. "env:PRIMARY_DB_URL") resolved at
	// connection time. Mutually exclusive with URL.
	Link    string         `toml:"link"`
	Options ClusterOptions `toml:"options"`
}

// ConnectionURL returns the effective connection string, preferring URL over Link.
// Callers that need secret resolution should check Link first.
func (c ClusterDef) ConnectionURL() string {
	if c.URL != "" {
		return c.URL
	}
	return c.Link
}

// ClusterOptions holds per-cluster behavioural options.
type ClusterOptions struct {
	// SnapshotOnApply writes an updated snapshot after every successful apply.
	SnapshotOnApply bool `toml:"snapshot_on_apply"`
}

// DefaultClusterConfig returns a ClusterConfig with sensible defaults.
func DefaultClusterConfig() ClusterConfig {
	return ClusterConfig{
		Cluster: ClusterDef{
			ClusterObjectsDir: "cluster",
		},
	}
}

// LoadCluster loads and parses the dpg.toml inside a cluster directory.
func LoadCluster(path string) (ClusterConfig, error) {
	cfg := DefaultClusterConfig()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return ClusterConfig{}, fmt.Errorf("loading %s: %w", path, err)
	}
	if err := cfg.Cluster.validate(path); err != nil {
		return ClusterConfig{}, err
	}
	return cfg, nil
}

func (c ClusterDef) validate(path string) error {
	if c.URL != "" && c.Link != "" {
		return fmt.Errorf("%s: url and link are mutually exclusive", path)
	}
	return nil
}

// DatabaseConfig represents the dpg.toml file inside a database directory.
type DatabaseConfig struct {
	Database DatabaseDef    `toml:"database"`
	NameMaps NameMapsConfig `toml:"namemaps"`
}

// DatabaseDef holds per-database settings.
type DatabaseDef struct {
	Name          string `toml:"name"`
	DefaultSchema string `toml:"default_schema"`
}

// DefaultDatabaseConfig returns a DatabaseConfig with sensible defaults.
func DefaultDatabaseConfig() DatabaseConfig {
	return DatabaseConfig{
		Database: DatabaseDef{
			DefaultSchema: "public",
		},
	}
}

// LoadDatabase loads and parses the dpg.toml inside a database directory.
func LoadDatabase(path string) (DatabaseConfig, error) {
	cfg := DefaultDatabaseConfig()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return DatabaseConfig{}, fmt.Errorf("loading %s: %w", path, err)
	}
	return cfg, nil
}
