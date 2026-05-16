package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SaveMigration writes sql to dir/<cluster>/<database>/<timestamp>.sql.
// The timestamp is the UTC apply time formatted as 20060102T150405Z.
// Returns the path of the written file.
// If dir is empty the call is a no-op (migration archiving is disabled).
func SaveMigration(dir, cluster, database, sql string) (string, error) {
	if dir == "" {
		return "", nil
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	dest := filepath.Join(dir, cluster, database, ts+".sql")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("migrations: create directory: %w", err)
	}
	if err := os.WriteFile(dest, []byte(sql), 0o644); err != nil {
		return "", fmt.Errorf("migrations: write %s: %w", dest, err)
	}
	return dest, nil
}
