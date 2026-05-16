package workspace

import (
	"os"
	"path/filepath"
)

// FindProjectRoot walks up from path searching for dpg.toml.
// Exported so analysis packages can use it.
func FindProjectRoot(path string) string {
	dir := filepath.Dir(path)
	for {
		if _, err := os.Stat(filepath.Join(dir, "dpg.toml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Dir(path)
}

// ListDPGFiles returns all *.dpg files under root (up to 3 directory levels deep).
func ListDPGFiles(root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// Skip hidden dirs and vendor-like dirs
		if d.IsDir() && (d.Name() == ".dpg" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if !d.IsDir() && filepath.Ext(path) == ".dpg" {
			files = append(files, path)
		}
		return nil
	})
	return files
}
