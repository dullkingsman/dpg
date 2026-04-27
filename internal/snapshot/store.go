// Package snapshot implements pipeline.SnapshotStore. It reads and writes the
// committed JSON snapshot format from RFC §4.2 as JSON files in the project's
// .dpg/snapshots/ directory.
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	pipeline.Default.Register(pipeline.KeySnapshotStore, &FileStore{Dir: ".dpg/snapshots"})
}

const Version = "0.1.0"

// FileStore implements pipeline.SnapshotStore using JSON files on disk.
type FileStore struct {
	// Dir is the directory where snapshot files are stored.
	// Each snapshot is written as <cluster>.<database>.json.
	Dir string
}

// Load implements pipeline.SnapshotStore. It reads and JSON-decodes the snapshot
// for the given cluster and database. Returns a zero-valued Snapshot if the file
// does not exist (first run / no prior apply).
func (s *FileStore) Load(cluster, database string) (*pipeline.Snapshot, error) {
	path := s.path(cluster, database)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &pipeline.Snapshot{}, nil
		}
		return nil, fmt.Errorf("snapshot: reading %s: %w", path, err)
	}

	var snap pipeline.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("snapshot: parsing %s: %w", path, err)
	}
	return &snap, nil
}

// Save implements pipeline.SnapshotStore. It writes the snapshot as pretty-printed
// JSON. The parent directory is created if it does not exist.
func (s *FileStore) Save(cluster, database string, snap *pipeline.Snapshot) error {
	// Stamp version and apply time.
	snap.DPGVersion = Version
	snap.AppliedAt = time.Now().UTC().Format(time.RFC3339)
	snap.Cluster = cluster
	snap.Database = database

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("snapshot: serialising: %w", err)
	}
	data = append(data, '\n')

	path := s.path(cluster, database)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("snapshot: creating directory %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("snapshot: writing %s: %w", path, err)
	}
	return nil
}

func (s *FileStore) path(cluster, database string) string {
	return filepath.Join(s.Dir, cluster+"."+database+".json")
}

// Ensure FileStore implements pipeline.SnapshotStore.
var _ pipeline.SnapshotStore = (*FileStore)(nil)
