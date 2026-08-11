package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/dullkingsman/dpg/internal/project"
)

// loadEnv loads environment variables from an .env file for commands that
// connect to a live database. It is non-fatal: if the file does not exist,
// variables already present in the process environment are used as-is.
//
// Path resolution order:
//  1. envFilePath if non-empty (from --env flag)
//  2. <project-root>/.env
//
// Existing environment variables are never overwritten (process env wins).
// Callers already gate when this runs (apply/verify/dump unconditionally;
// plan only under --live) — offline-only commands (plan without --live,
// diff, portability) never call this at all. A prior version additionally
// gated internally on "at least one cluster uses a link: connection
// string", which was wrong: a project using a plain url= connection can
// still reference {{env:VAR}} elsewhere (ROLE PASSWORD, SUBSCRIPTION
// CONNECTION, USER MAPPING OPTIONS — the secret-reference mechanism is
// independent of how the cluster itself connects), and that project's .env
// was silently never read, forcing --env or manual export every time even
// though the file was present and correctly named. Confirmed live: a demo
// project with url= and a ROLE PASSWORD '{{env:...}}' failed to resolve
// with .env present until --env was passed explicitly.
func loadEnv(proj *project.Project, envFilePath string) {
	path := envFilePath
	if path == "" {
		path = filepath.Join(proj.RootDir, ".env")
	}
	_ = parseEnvFile(path) // non-fatal; missing file is fine
}

// parseEnvFile reads KEY=VALUE pairs from path and sets any that are not
// already present in the process environment. Supports:
//   - Blank lines and # comments (ignored)
//   - Optional leading "export " prefix
//   - Values wrapped in single or double quotes (quotes are stripped)
func parseEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])

		if len(val) >= 2 {
			q := val[0]
			if (q == '"' || q == '\'') && val[len(val)-1] == q {
				val = val[1 : len(val)-1]
			}
		}

		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
	return sc.Err()
}
