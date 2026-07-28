//go:build integration

package vault

import (
	"context"
	"os"
	"testing"

	vaultapi "github.com/hashicorp/vault/api"
)

// TestResolveLiveVault exercises the real hashicorp/vault/api client against
// a live Vault dev-mode server, confirmed reachable via VAULT_ADDR/VAULT_TOKEN
// (the same ambient env vars the `vault` CLI itself uses — nothing
// DPG-specific). Unlike this package's other tests (which fake kvGetter),
// this proves the actual wiring — New()'s real client construction, the
// vaultapi.KVv2 helper's request/response shape, ambient auth — works
// end-to-end, not just that Resolver's own logic is correct in isolation.
//
// Skips (not fails) if VAULT_ADDR isn't set, so this doesn't break `go test
// -tags integration` runs on machines without a Vault dev server up — unlike
// postgres:17, there's no existing testcontainers wiring for Vault in this
// repo yet, so this test relies on one already being reachable rather than
// spinning one up itself.
func TestResolveLiveVault(t *testing.T) {
	if os.Getenv("VAULT_ADDR") == "" {
		t.Skip("VAULT_ADDR not set; skipping live Vault test (see file comment)")
	}

	seedLiveSecret(t)

	r := New()
	got, err := r.Resolve("vault:secret/dpg-live-test/db#password")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "live-s3cr3t" {
		t.Errorf("got %q, want live-s3cr3t", got)
	}

	if _, err := r.Resolve("vault:secret/dpg-live-test/db#nonexistent"); err == nil {
		t.Error("expected an error for a field that doesn't exist in the live secret")
	}
}

// seedLiveSecret writes a known secret using the same SDK/ambient-auth path
// the resolver itself uses (not an external `vault` CLI binary, which may
// not be installed on the machine running this test).
func seedLiveSecret(t *testing.T) {
	t.Helper()
	client, err := vaultapi.NewClient(vaultapi.DefaultConfig())
	if err != nil {
		t.Fatalf("constructing vault client to seed test data: %v", err)
	}
	_, err = client.KVv2("secret").Put(context.Background(), "dpg-live-test/db", map[string]any{
		"password": "live-s3cr3t",
	})
	if err != nil {
		t.Fatalf("seeding live secret: %v", err)
	}
}
