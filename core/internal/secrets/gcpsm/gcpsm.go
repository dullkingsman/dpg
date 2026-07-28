// Package gcpsm implements a pipeline.SecretResolver for the "gcp-sm"
// scheme, reading from Google Cloud Secret Manager.
//
// Authentication is entirely ambient, via Application Default Credentials
// (GOOGLE_APPLICATION_CREDENTIALS, gcloud's cached user credentials, or the
// GCE/GKE/Cloud Run metadata server) — see secretmanager.NewClient, which
// resolves ADC the same way every other GCP client library does. DPG does
// not reimplement or wrap GCP's own credential resolution. Constructing the
// resolver never fails just because GCP isn't configured, matching every
// other resolver's "safe to register unconditionally" contract — confirmed
// live that NewClient does not validate credentials or dial eagerly.
package gcpsm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	gax "github.com/googleapis/gax-go/v2"

	"github.com/dullkingsman/dpg/internal/secrets"
)

// resolveTimeout bounds a single secret lookup — see vault's identical
// constant for why: pipeline.SecretResolver's Resolve(uri string) signature
// carries no context, so this is the only way to avoid a hung `dpg`
// invocation if GCP is configured but unreachable.
const resolveTimeout = 30 * time.Second

// secretAccessor is the minimal seam over *secretmanager.Client, letting
// tests inject a fake without live GCP credentials.
type secretAccessor interface {
	AccessSecretVersion(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error)
}

// newClient constructs a secretAccessor using Application Default
// Credentials. Swappable in tests via Resolver.newClient.
func newClient(ctx context.Context) (secretAccessor, error) {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("secrets/gcpsm: constructing client: %w", err)
	}
	return client, nil
}

// Resolver implements pipeline.SecretResolver for the "gcp-sm" scheme.
//
// URI shape: gcp-sm:<project>/<secret-id>[/<version>][#<json-field>]
//   - project: the GCP project ID.
//   - secret-id: the secret's name within that project.
//   - version: the secret version, defaulting to "latest" (GCP's own alias
//     for the newest enabled version) if omitted.
//   - json-field: optional. Like aws-sm:, GCP Secret Manager secrets are a
//     single opaque byte payload with no inherent key-value structure, so
//     omitting the field returns the raw payload as a UTF-8 string; if
//     given, the payload is parsed as JSON and that key extracted.
//
// Examples: gcp-sm:my-project/db-password
//
//	gcp-sm:my-project/db-password/3
//	gcp-sm:my-project/db#password (payload is JSON: {"password": "..."})
type Resolver struct {
	// newClient is swappable in tests; defaults to the package-level
	// newClient, which talks to real GCP Secret Manager.
	newClient func(ctx context.Context) (secretAccessor, error)
}

// New returns a Resolver using Application Default Credentials.
func New() *Resolver {
	return &Resolver{newClient: newClient}
}

func (r *Resolver) Resolve(uri string) (string, error) {
	rest, ok := strings.CutPrefix(uri, "gcp-sm:")
	if !ok {
		return "", fmt.Errorf("secrets/gcpsm: %q is not a gcp-sm: URI", uri)
	}
	pathPart, field, _ := strings.Cut(rest, "#")

	parts := strings.Split(pathPart, "/")
	var project, secretID, version string
	switch len(parts) {
	case 2:
		project, secretID, version = parts[0], parts[1], "latest"
	case 3:
		project, secretID, version = parts[0], parts[1], parts[2]
	default:
		return "", fmt.Errorf("secrets/gcpsm: %q must have the shape gcp-sm:<project>/<secret-id>[/<version>]", uri)
	}
	if project == "" || secretID == "" || version == "" {
		return "", fmt.Errorf("secrets/gcpsm: %q must have the shape gcp-sm:<project>/<secret-id>[/<version>]", uri)
	}
	resourceName := fmt.Sprintf("projects/%s/secrets/%s/versions/%s", project, secretID, version)

	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()

	client, err := r.newClient(ctx)
	if err != nil {
		return "", err
	}

	resp, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: resourceName})
	if err != nil {
		return "", fmt.Errorf("secrets/gcpsm: reading %s: %w", resourceName, err)
	}
	if resp.Payload == nil {
		return "", fmt.Errorf("secrets/gcpsm: secret %s has no payload", resourceName)
	}
	value := string(resp.Payload.Data)

	if field == "" {
		return value, nil
	}

	var fields map[string]any
	if err := json.Unmarshal(resp.Payload.Data, &fields); err != nil {
		return "", fmt.Errorf("secrets/gcpsm: secret %s is not valid JSON, cannot extract field %q: %w", resourceName, field, err)
	}
	v, ok := fields[field]
	if !ok {
		return "", fmt.Errorf("secrets/gcpsm: secret %s has no field %q", resourceName, field)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("secrets/gcpsm: secret %s field %q is not a string", resourceName, field)
	}
	return s, nil
}

func init() {
	secrets.Default().Register("gcp-sm", New())
}
