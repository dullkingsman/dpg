// Runs the end-to-end smoke scenario as a standard Go test.
// Skipped with -short (e.g. in unit-only CI jobs) because it builds and
// spawns a real dpg-lsp subprocess.
//
//	go test ./cmd/lsp-smoke/          # full smoke run
//	go test -short ./cmd/lsp-smoke/   # skipped
package main

import (
	"testing"
)

func TestSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping smoke test in short mode")
	}
	if err := run(); err != nil {
		t.Fatalf("smoke test failed: %v", err)
	}
}
