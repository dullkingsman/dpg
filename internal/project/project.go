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

// DPGDir returns the absolute path to the project's .dpg working directory.
func (p *Project) DPGDir() string {
	return filepath.Join(p.RootDir, ".dpg")
}

// SnapshotDir returns the absolute path to the snapshot directory.
func (p *Project) SnapshotDir() string {
	return filepath.Join(p.RootDir, p.RootConfig.Snapshots.Directory)
}

// MigrationsDir returns the absolute path to the migrations archive directory.
// Returns "" when migration archiving is disabled (directory = "").
func (p *Project) MigrationsDir() string {
	if p.RootConfig.Migrations.Directory == "" {
		return ""
	}
	return filepath.Join(p.RootDir, p.RootConfig.Migrations.Directory)
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

// ConnectionString returns the raw URL or Link value from cluster config.
// Callers that support secrets (apply, verify, dump) must check whether the
// value is a link URI and resolve it via the SecretResolver before connecting.
func (c *Cluster) ConnectionString() string {
	return c.Config.Cluster.ConnectionURL()
}

// IsLink reports whether the connection string is a secrets-provider URI
// (i.e. the Link field is set rather than URL).
func (c *Cluster) IsLink() bool {
	return c.Config.Cluster.Link != "" && c.Config.Cluster.URL == ""
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

// findRoot walks up from dir looking for a dpg.toml that is the project root
// config (i.e. contains [compiler], [linter], or [snapshots] — not [cluster]
// or [database], which identify cluster/database-level configs).
func findRoot(dir string) (string, error) {
	current := filepath.Clean(dir)
	for {
		candidate := filepath.Join(current, "dpg.toml")
		if _, err := os.Stat(candidate); err == nil && isRootConfig(candidate) {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no project root dpg.toml found in %s or any parent directory", dir)
		}
		current = parent
	}
}

// isRootConfig reports whether the dpg.toml at path is a project root config
// rather than a cluster or database config. Root configs must not contain a
// [cluster] or [database] section.
func isRootConfig(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t == "[cluster]" || strings.HasPrefix(t, "[cluster.") || t == "[database]" {
			return false
		}
	}
	return true
}

// discoverClusters scans immediate subdirectories of rootDir. Any subdirectory
// containing a dpg.toml is treated as a cluster directory.
func discoverClusters(rootDir string) ([]*Cluster, error) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return nil, fmt.Errorf("reading project root %s: %w", rootDir, err)
	}

	var clusters []*Cluster
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		clusterDir := filepath.Join(rootDir, entry.Name())
		cfgPath := filepath.Join(clusterDir, "dpg.toml")
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			continue
		}
		cluster, err := loadCluster(clusterDir, cfgPath)
		if err != nil {
			return nil, err
		}
		clusters = append(clusters, cluster)
	}
	return clusters, nil
}

// loadCluster loads a single cluster from its dpg.toml inside clusterDir.
func loadCluster(clusterDir, cfgPath string) (*Cluster, error) {
	cfg, err := config.LoadCluster(cfgPath)
	if err != nil {
		return nil, err
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

// discoverDatabases scans immediate subdirectories of clusterDir. Any
// subdirectory that contains a dpg.toml and is not the cluster objects
// directory is treated as a database directory.
func discoverDatabases(clusterDir, reservedDir string) ([]*Database, error) {
	entries, err := os.ReadDir(clusterDir)
	if err != nil {
		return nil, fmt.Errorf("reading cluster directory %s: %w", clusterDir, err)
	}

	var databases []*Database
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if entry.Name() == reservedDir {
			continue // cluster-level objects directory — not a database
		}
		dbDir := filepath.Join(clusterDir, entry.Name())
		cfgPath := filepath.Join(dbDir, "dpg.toml")
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			continue
		}
		db, err := loadDatabase(dbDir, cfgPath)
		if err != nil {
			return nil, err
		}
		databases = append(databases, db)
	}
	return databases, nil
}

// loadDatabase loads a single database from its dpg.toml inside dbDir.
func loadDatabase(dbDir, cfgPath string) (*Database, error) {
	cfg, err := config.LoadDatabase(cfgPath)
	if err != nil {
		return nil, err
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
