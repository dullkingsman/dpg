// Package awssm implements a pipeline.SecretResolver for the "aws-sm" scheme,
// reading from AWS Secrets Manager.
//
// Authentication is entirely ambient, via the AWS SDK's own standard
// credential chain (environment variables, shared config/credentials files,
// an EC2/ECS/Lambda role, SSO, ...) — see config.LoadDefaultConfig. DPG does
// not reimplement or wrap AWS's own credential resolution. Constructing the
// resolver never makes a network call and never fails just because AWS
// isn't configured, matching every other resolver's "safe to register
// unconditionally" contract.
package awssm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/thec1oud/dpg/internal/secrets"
)

// resolveTimeout bounds a single secret lookup — see vault's identical
// constant for why: pipeline.SecretResolver's Resolve(uri string) signature
// carries no context, so this is the only way to avoid a hung `dpg`
// invocation if AWS is configured but unreachable.
const resolveTimeout = 30 * time.Second

// secretGetter is the minimal seam over *secretsmanager.Client, letting
// tests inject a fake without live AWS credentials.
type secretGetter interface {
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

// newClient constructs a secretGetter from the ambient AWS credential chain.
// Swappable in tests via Resolver.newClient.
func newClient() (secretGetter, error) {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("secrets/awssm: loading AWS config: %w", err)
	}
	return secretsmanager.NewFromConfig(cfg), nil
}

// Resolver implements pipeline.SecretResolver for the "aws-sm" scheme.
//
// URI shape: aws-sm:<secret-id>[#<json-field>]
//   - secret-id: the secret's ARN or name, passed through verbatim
//     (AWS secret names routinely contain '/', e.g. "myapp/prod/db", so
//     unlike vault: this is not split into path segments).
//   - json-field: optional. AWS Secrets Manager secrets are a single
//     opaque string (SecretString) with no inherent key-value structure —
//     unlike Vault, there IS an unambiguous "whole value" reading, so
//     omitting the field returns SecretString as-is. If given, SecretString
//     is parsed as JSON (a common Secrets Manager convention, e.g. what the
//     RDS "Store a new secret" console flow produces) and that key
//     extracted.
//
// Examples: aws-sm:myapp/prod/db-password
//
//	aws-sm:myapp/prod/db#password (SecretString is JSON: {"password": "..."})
type Resolver struct {
	// newClient is swappable in tests; defaults to the package-level
	// newClient, which talks to real AWS Secrets Manager.
	newClient func() (secretGetter, error)
}

// New returns a Resolver using the ambient AWS credential chain.
func New() *Resolver {
	return &Resolver{newClient: newClient}
}

func (r *Resolver) Resolve(uri string) (string, error) {
	rest, ok := strings.CutPrefix(uri, "aws-sm:")
	if !ok {
		return "", fmt.Errorf("secrets/awssm: %q is not an aws-sm: URI", uri)
	}
	secretID, field, _ := strings.Cut(rest, "#")
	if secretID == "" {
		return "", fmt.Errorf("secrets/awssm: %q is missing a secret ID", uri)
	}

	client, err := r.newClient()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()

	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &secretID})
	if err != nil {
		return "", fmt.Errorf("secrets/awssm: reading %s: %w", secretID, err)
	}
	if out.SecretString == nil {
		return "", fmt.Errorf("secrets/awssm: secret %s has no SecretString value (binary secrets are not supported)", secretID)
	}
	if field == "" {
		return *out.SecretString, nil
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(*out.SecretString), &fields); err != nil {
		return "", fmt.Errorf("secrets/awssm: secret %s is not valid JSON, cannot extract field %q: %w", secretID, field, err)
	}
	v, ok := fields[field]
	if !ok {
		return "", fmt.Errorf("secrets/awssm: secret %s has no field %q", secretID, field)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("secrets/awssm: secret %s field %q is not a string", secretID, field)
	}
	return s, nil
}

func init() {
	secrets.Default().Register("aws-sm", New())
}
