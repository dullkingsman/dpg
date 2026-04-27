package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dullkingsman/dpg/internal/config"
)

// Project is the fully-resolved DPG project, rooted at a directory containing dpg.toml.
type Project struct {
	RootDir    string
	RootConfig config.RootConfig
	Clusters   []*Cluster
}

// SnapshotDir returns the absolute path to the snapshot directory.
func (p *Project) SnapshotDir() string {
	return filepath.Join(p.RootDir, p.RootConfig.Snapshots.Directory)
}

// Cluster represents a single PostgreSQL cluster within the project.
type Cluster struct {
	// Dir is the absolute path to the cluster directory.
	Dir    string
	Config config.ClusterConfig
	// ObjectsDir is the absolute path to the cluster-level objects directory
	// (roles, tablespaces, FDWs). May not exist yet.
	ObjectsDir string
	Databases  []*Database
}

// Name returns the cluster name from config.
func (c *Cluster) Name() string { return c.Config.Cluster.Name }

// PrimaryNode returns the primary node definition, or nil if none is configured.
func (c *Cluster) PrimaryNode() *config.NodeDef {
	for i := range c.Config.Cluster.Nodes {
		if c.Config.Cluster.Nodes[i].Role == "primary" {
			return &c.Config.Cluster.Nodes[i]
		}
	}
	return nil
}

// Database represents a single PostgreSQL database within a cluster.
type Database struct {
	// Dir is the absolute path to the database source directory.
	Dir    string
	Config config.DatabaseConfig
	// SourceFiles is the ordered list of absolute paths to all .dpg files
	// found recursively within Dir.
	SourceFiles []string
}

// Name returns the database name from config.
func (d *Database) Name() string { return d.Config.Database.Name }

// Discover walks up from startDir until it finds a dpg.toml, then builds
// and returns the full Project. Returns an error if no dpg.toml is found or
// if the project structure is invalid.
func Discover(startDir string) (*Project, error) {
	rootDir, err := findRoot(startDir)
	if err != nil {
		return nil, err
	}

	rootCfg, err := config.LoadRoot(rootDir)
	if err != nil {
		return nil, err
	}

	clusters, err := discoverClusters(rootDir)
	if err != nil {
		return nil, err
	}

	return &Project{
		RootDir:    rootDir,
		RootConfig: rootCfg,
		Clusters:   clusters,
	}, nil
}

// findRoot walks up from dir looking for a dpg.toml file.
func findRoot(dir string) (string, error) {
	current := filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(current, "dpg.toml")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no dpg.toml found in %s or any parent directory", dir)
		}
		current = parent
	}
}

// discoverClusters finds all *.dpg.toml files in rootDir and builds a Cluster for each.
func discoverClusters(rootDir string) ([]*Cluster, error) {
	pattern := filepath.Join(rootDir, "*.dpg.toml")
	tomlPaths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("globbing cluster configs in %s: %w", rootDir, err)
	}

	var clusters []*Cluster
	for _, tomlPath := range tomlPaths {
		cluster, err := loadCluster(rootDir, tomlPath)
		if err != nil {
			return nil, err
		}
		clusters = append(clusters, cluster)
	}
	return clusters, nil
}

// loadCluster loads a single cluster from its *.dpg.toml file.
func loadCluster(rootDir, tomlPath string) (*Cluster, error) {
	cfg, err := config.LoadCluster(tomlPath)
	if err != nil {
		return nil, err
	}

	// The cluster directory has the same stem as the .dpg.toml file.
	// e.g. production.dpg.toml → production/
	stem := clusterStem(tomlPath)
	clusterDir := filepath.Join(rootDir, stem)

	if info, err := os.Stat(clusterDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("cluster directory %s not found (expected alongside %s)",
			clusterDir, filepath.Base(tomlPath))
	}

	objectsDir := filepath.Join(clusterDir, cfg.Cluster.ClusterObjectsDir)

	databases, err := discoverDatabases(clusterDir, cfg.Cluster.ClusterObjectsDir)
	if err != nil {
		return nil, fmt.Errorf("cluster %q: %w", cfg.Cluster.Name, err)
	}

	return &Cluster{
		Dir:        clusterDir,
		Config:     cfg,
		ObjectsDir: objectsDir,
		Databases:  databases,
	}, nil
}

// discoverDatabases finds all *.dpg.toml files in clusterDir and builds a Database for each.
func discoverDatabases(clusterDir, reservedDir string) ([]*Database, error) {
	pattern := filepath.Join(clusterDir, "*.dpg.toml")
	tomlPaths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("globbing database configs in %s: %w", clusterDir, err)
	}

	var databases []*Database
	for _, tomlPath := range tomlPaths {
		stem := clusterStem(tomlPath) // reuse: same logic, strips ".dpg.toml"

		// Validate: no database may share the cluster objects dir name.
		if stem == reservedDir {
			return nil, fmt.Errorf(
				"database name %q conflicts with cluster_objects_dir %q in %s",
				stem, reservedDir, tomlPath)
		}

		db, err := loadDatabase(clusterDir, tomlPath, stem)
		if err != nil {
			return nil, err
		}
		databases = append(databases, db)
	}
	return databases, nil
}

// loadDatabase loads a single database from its *.dpg.toml file.
func loadDatabase(clusterDir, tomlPath, stem string) (*Database, error) {
	cfg, err := config.LoadDatabase(tomlPath)
	if err != nil {
		return nil, err
	}

	dbDir := filepath.Join(clusterDir, stem)
	if info, err := os.Stat(dbDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("database directory %s not found (expected alongside %s)",
			dbDir, filepath.Base(tomlPath))
	}

	sourceFiles, err := collectSourceFiles(dbDir)
	if err != nil {
		return nil, fmt.Errorf("database %q: collecting source files: %w", cfg.Database.Name, err)
	}

	return &Database{
		Dir:         dbDir,
		Config:      cfg,
		SourceFiles: sourceFiles,
	}, nil
}

// collectSourceFiles recursively finds all .dpg files under dir, sorted by path.
func collectSourceFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".dpg") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// clusterStem strips the ".dpg.toml" suffix from a file path and returns the base name.
// e.g. "/path/to/production.dpg.toml" → "production"
func clusterStem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, ".dpg.toml")
}
