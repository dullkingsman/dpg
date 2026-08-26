// Package azurekv implements a pipeline.SecretResolver for the "azure-kv"
// scheme, reading from Azure Key Vault.
//
// Authentication is entirely ambient, via DefaultAzureCredential (env vars
// AZURE_CLIENT_ID/AZURE_TENANT_ID/AZURE_CLIENT_SECRET, a managed identity,
// the Azure CLI's cached login, ...) — see azidentity.NewDefaultAzureCredential,
// the same credential chain every other Azure SDK client uses by default.
// DPG does not reimplement or wrap Azure's own credential resolution.
// Constructing the resolver never fails just because Azure isn't configured,
// matching every other resolver's "safe to register unconditionally"
// contract.
package azurekv

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"

	"github.com/thec1oud/dpg/internal/secrets"
)

// resolveTimeout bounds a single secret lookup — see vault's identical
// constant for why: pipeline.SecretResolver's Resolve(uri string) signature
// carries no context, so this is the only way to avoid a hung `dpg`
// invocation if Azure is configured but unreachable.
const resolveTimeout = 30 * time.Second

// secretGetter is the minimal seam over *azsecrets.Client, letting tests
// inject a fake without live Azure credentials.
type secretGetter interface {
	GetSecret(ctx context.Context, name string, version string, options *azsecrets.GetSecretOptions) (azsecrets.GetSecretResponse, error)
}

// newClient constructs a secretGetter for the given vault, using
// DefaultAzureCredential for ambient auth. Swappable in tests via
// Resolver.newClient.
func newClient(vaultName string) (secretGetter, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("secrets/azurekv: constructing credential: %w", err)
	}
	vaultURL := fmt.Sprintf("https://%s.vault.azure.net/", vaultName)
	client, err := azsecrets.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("secrets/azurekv: constructing client: %w", err)
	}
	return client, nil
}

// Resolver implements pipeline.SecretResolver for the "azure-kv" scheme.
//
// URI shape: azure-kv:<vault-name>/<secret-name>[/<version>][#<json-field>]
//   - vault-name: the Key Vault's name (used to build
//     https://<vault-name>.vault.azure.net/, Azure's standard vault URL
//     convention).
//   - secret-name: the secret's name within that vault.
//   - version: the secret version. Omitted (or empty) means the latest
//     version — Azure's own API convention for an empty version string.
//   - json-field: optional. Like aws-sm:/gcp-sm:, Key Vault secrets are a
//     single opaque string with no inherent key-value structure, so
//     omitting the field returns the raw value as-is; if given, the value
//     is parsed as JSON and that key extracted.
//
// Examples: azure-kv:my-vault/db-password
//
//	azure-kv:my-vault/db-password/abc123
//	azure-kv:my-vault/db#password (value is JSON: {"password": "..."})
type Resolver struct {
	// newClient is swappable in tests; defaults to the package-level
	// newClient, which talks to real Azure Key Vault.
	newClient func(vaultName string) (secretGetter, error)
}

// New returns a Resolver using DefaultAzureCredential.
func New() *Resolver {
	return &Resolver{newClient: newClient}
}

func (r *Resolver) Resolve(uri string) (string, error) {
	rest, ok := strings.CutPrefix(uri, "azure-kv:")
	if !ok {
		return "", fmt.Errorf("secrets/azurekv: %q is not an azure-kv: URI", uri)
	}
	pathPart, field, _ := strings.Cut(rest, "#")

	parts := strings.Split(pathPart, "/")
	var vaultName, secretName, version string
	switch len(parts) {
	case 2:
		vaultName, secretName = parts[0], parts[1]
	case 3:
		vaultName, secretName, version = parts[0], parts[1], parts[2]
	default:
		return "", fmt.Errorf("secrets/azurekv: %q must have the shape azure-kv:<vault-name>/<secret-name>[/<version>]", uri)
	}
	if vaultName == "" || secretName == "" {
		return "", fmt.Errorf("secrets/azurekv: %q must have the shape azure-kv:<vault-name>/<secret-name>[/<version>]", uri)
	}

	client, err := r.newClient(vaultName)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()

	resp, err := client.GetSecret(ctx, secretName, version, nil)
	if err != nil {
		return "", fmt.Errorf("secrets/azurekv: reading %s/%s: %w", vaultName, secretName, err)
	}
	if resp.Value == nil {
		return "", fmt.Errorf("secrets/azurekv: secret %s/%s has no value", vaultName, secretName)
	}
	value := *resp.Value

	if field == "" {
		return value, nil
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(value), &fields); err != nil {
		return "", fmt.Errorf("secrets/azurekv: secret %s/%s is not valid JSON, cannot extract field %q: %w", vaultName, secretName, field, err)
	}
	v, ok := fields[field]
	if !ok {
		return "", fmt.Errorf("secrets/azurekv: secret %s/%s has no field %q", vaultName, secretName, field)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("secrets/azurekv: secret %s/%s field %q is not a string", vaultName, secretName, field)
	}
	return s, nil
}

func init() {
	secrets.Default().Register("azure-kv", New())
}
