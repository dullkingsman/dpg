// Package vault implements a pipeline.SecretResolver for the "vault" scheme,
// reading from a HashiCorp Vault KV version 2 secrets engine — the modern
// default (what `vault server -dev` mounts, and what current Vault docs
// recommend for new deployments). KV version 1 and non-KV secrets engines
// (dynamic database credentials, the AWS engine, etc.) are out of scope:
// each has materially different read semantics, and DPG only ever needs a
// static "read this value" lookup, the same shape env: already provides.
//
// Authentication is entirely ambient, the same convention the `vault` CLI
// itself uses: VAULT_ADDR and VAULT_TOKEN environment variables (see
// hashicorp/vault/api's own DefaultConfig/NewClient — DPG does not
// reimplement or wrap Vault's own auth resolution). Constructing the
// resolver never makes a network call and never fails just because Vault
// isn't configured — that matches every other resolver's contract of "safe
// to register unconditionally, only errors when actually used" — so a
// project that never references a vault: URI is completely unaffected by
// this package being linked in.
package vault

import (
	"context"
	"fmt"
	"strings"
	"time"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/thec1oud/dpg/internal/secrets"
)

// resolveTimeout bounds a single secret lookup. pipeline.SecretResolver's
// Resolve(uri string) signature carries no context, so this is the only way
// to avoid a hung `dpg` invocation if Vault is configured but unreachable —
// a short-lived CLI command should never block indefinitely on a network
// call the caller has no way to cancel.
const resolveTimeout = 30 * time.Second

// kvGetter is the minimal seam over *vaultapi.KVv2, letting tests inject a
// fake without a live Vault server. *vaultapi.KVv2's own Get method already
// matches this shape, so no adapter type is needed.
type kvGetter interface {
	Get(ctx context.Context, secretPath string) (*vaultapi.KVSecret, error)
}

// newKV constructs a kvGetter for the given KV v2 mount from ambient
// VAULT_ADDR/VAULT_TOKEN. Swappable in tests via Resolver.newKV.
func newKV(mount string) (kvGetter, error) {
	client, err := vaultapi.NewClient(vaultapi.DefaultConfig())
	if err != nil {
		return nil, fmt.Errorf("secrets/vault: constructing client: %w", err)
	}
	return client.KVv2(mount), nil
}

// Resolver implements pipeline.SecretResolver for the "vault" scheme.
//
// URI shape: vault:<mount>/<path>#<field>
//   - mount: the KV v2 engine's mount path (e.g. "secret")
//   - path: the secret's path within that mount (e.g. "myapp/db")
//   - field: REQUIRED — the key to read from the secret's data map. Vault
//     secrets are always key-value maps, never a bare string, so unlike
//     env:/aws-sm:/gcp-sm:/azure-kv: there is no unambiguous "whole value"
//     reading to fall back to; auto-selecting a lone key was considered and
//     rejected as a latent footgun (a second key added to the secret later,
//     without ever touching the DPG source, would silently turn a working
//     reference ambiguous at a confusing time).
//
// Example: vault:secret/myapp/db#password
type Resolver struct {
	// newKV is swappable in tests; defaults to the package-level newKV,
	// which talks to a real Vault server.
	newKV func(mount string) (kvGetter, error)
}

// New returns a Resolver using ambient Vault configuration.
func New() *Resolver {
	return &Resolver{newKV: newKV}
}

func (r *Resolver) Resolve(uri string) (string, error) {
	rest, ok := strings.CutPrefix(uri, "vault:")
	if !ok {
		return "", fmt.Errorf("secrets/vault: %q is not a vault: URI", uri)
	}

	mountAndPath, field, hasField := strings.Cut(rest, "#")
	if !hasField || field == "" {
		return "", fmt.Errorf(
			"secrets/vault: %q is missing a #field — Vault secrets are always key-value maps, "+
				"so the field to read must be named explicitly (e.g. vault:secret/myapp/db#password)", uri)
	}
	mount, path, ok := strings.Cut(mountAndPath, "/")
	if !ok || mount == "" || path == "" {
		return "", fmt.Errorf("secrets/vault: %q must have the shape vault:<mount>/<path>#<field>", uri)
	}

	kv, err := r.newKV(mount)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()

	secret, err := kv.Get(ctx, path)
	if err != nil {
		return "", fmt.Errorf("secrets/vault: reading %s/%s: %w", mount, path, err)
	}
	if secret == nil || secret.Data == nil {
		return "", fmt.Errorf("secrets/vault: secret %s/%s not found", mount, path)
	}

	v, ok := secret.Data[field]
	if !ok {
		return "", fmt.Errorf("secrets/vault: secret %s/%s has no field %q", mount, path, field)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("secrets/vault: secret %s/%s field %q is not a string", mount, path, field)
	}
	return s, nil
}

func init() {
	secrets.Default().Register("vault", New())
}
