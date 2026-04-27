// Package secrets implements pipeline.SecretResolver. It resolves secret URIs
// to plaintext values at connection time.
//
// Supported URI schemes:
//   - env:VAR_NAME   → os.Getenv("VAR_NAME")
//   - link:...       → stub; returns an error until vault support is added
package secrets

import (
	"fmt"
	"os"
	"strings"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	pipeline.Default.Register(pipeline.KeySecretResolver, New())
}

// EnvResolver implements pipeline.SecretResolver.
type EnvResolver struct{}

// New returns an EnvResolver.
func New() *EnvResolver { return &EnvResolver{} }

// Resolve resolves a secret URI to its plaintext value.
func (r *EnvResolver) Resolve(uri string) (string, error) {
	switch {
	case strings.HasPrefix(uri, "env:"):
		varName := strings.TrimPrefix(uri, "env:")
		if varName == "" {
			return "", fmt.Errorf("secrets: env: URI missing variable name")
		}
		val, ok := os.LookupEnv(varName)
		if !ok {
			return "", fmt.Errorf("secrets: environment variable %q is not set", varName)
		}
		return val, nil

	case strings.HasPrefix(uri, "link:"):
		return "", fmt.Errorf("secrets: link: URIs require vault integration (not yet implemented)")

	default:
		// Plain value: return as-is (not a URI).
		return uri, nil
	}
}

var _ pipeline.SecretResolver = (*EnvResolver)(nil)
