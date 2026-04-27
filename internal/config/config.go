package config

import (
	"fmt"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// RootConfig represents the contents of dpg.toml at the project root.
type RootConfig struct {
	Compiler  CompilerConfig  `toml:"compiler"`
	Linter    LinterConfig    `toml:"linter"`
	Snapshots SnapshotsConfig `toml:"snapshots"`
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
		Snapshots: SnapshotsConfig{
			Directory: ".dpg/snapshots",
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

// ClusterConfig represents a <cluster-name>.dpg.toml file.
type ClusterConfig struct {
	Cluster ClusterDef `toml:"cluster"`
}

// ClusterDef holds the cluster topology and options.
type ClusterDef struct {
	Name string `toml:"name"`
	// ClusterObjectsDir is the subdirectory within the cluster directory
	// that holds cluster-level objects (roles, tablespaces, FDWs).
	// Default: "cluster". This name is reserved — no database may share it.
	ClusterObjectsDir string         `toml:"cluster_objects_dir"`
	Nodes             []NodeDef      `toml:"nodes"`
	Options           ClusterOptions `toml:"options"`
}

// NodeDef describes a single PostgreSQL node in the cluster.
type NodeDef struct {
	Name string `toml:"name"`
	// URL is an inline connection string. Mutually exclusive with Link.
	URL string `toml:"url"`
	// Link is a secrets-provider URI (e.g. "vault://prod/pg-primary").
	// Resolved at connection time by the SecretResolver. Mutually exclusive with URL.
	Link string `toml:"link"`
	// Role is either "primary" (writable; target of dpg apply) or "replica"
	// (read-only; used by dpg verify). Exactly one node must be "primary".
	Role string `toml:"role"`
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

// LoadCluster loads and parses a <cluster-name>.dpg.toml file at path.
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
	primaryCount := 0
	for _, n := range c.Nodes {
		switch n.Role {
		case "primary":
			primaryCount++
		case "replica":
		default:
			return fmt.Errorf("%s: node %q: role must be \"primary\" or \"replica\", got %q",
				path, n.Name, n.Role)
		}
		if n.URL != "" && n.Link != "" {
			return fmt.Errorf("%s: node %q: url and link are mutually exclusive", path, n.Name)
		}
		if n.URL == "" && n.Link == "" {
			return fmt.Errorf("%s: node %q: one of url or link is required", path, n.Name)
		}
	}
	if len(c.Nodes) > 0 && primaryCount != 1 {
		return fmt.Errorf("%s: exactly one node must have role = \"primary\", found %d", path, primaryCount)
	}
	return nil
}

// DatabaseConfig represents a <db-name>.dpg.toml file.
type DatabaseConfig struct {
	Database DatabaseDef `toml:"database"`
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

// LoadDatabase loads and parses a <db-name>.dpg.toml file at path.
func LoadDatabase(path string) (DatabaseConfig, error) {
	cfg := DefaultDatabaseConfig()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return DatabaseConfig{}, fmt.Errorf("loading %s: %w", path, err)
	}
	return cfg, nil
}
