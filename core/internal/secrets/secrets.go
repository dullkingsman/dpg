// Package secrets implements pipeline.SecretResolver. It resolves secret URIs
// to plaintext values at connection time — used for `dpg.toml`'s cluster
// `link` field ("a secrets-provider URI resolved at connection time", RFC
// §3.3), and, as of the operator-family-era secret-resolution work, for
// structured secret-reference fields elsewhere in the IR.
//
// The package default (registered under pipeline.KeySecretResolver) is a
// ChainResolver (see chain.go) with EnvResolver registered for the "env"
// scheme. Additional schemes (vault, aws-sm, gcp-sm, azure-kv) register
// themselves into the same ChainResolver from their own subpackages.
//
// Supported URI schemes:
//   - env:VAR_NAME → os.Getenv("VAR_NAME")
//
// `link` is a TOML *key* name, never a URI *scheme* — a value like
// `link:env:VAR` is not meaningful and is rejected the same as any other
// unrecognized scheme (see ChainResolver).
package secrets

import (
	"fmt"
	"os"
	"strings"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	chain := NewChain()
	chain.Register("env", New())
	pipeline.Default.Register(pipeline.KeySecretResolver, chain)
}

// EnvResolver implements pipeline.SecretResolver for the "env" scheme.
type EnvResolver struct{}

// New returns an EnvResolver.
func New() *EnvResolver { return &EnvResolver{} }

// Resolve resolves an "env:VAR_NAME" URI to os.Getenv("VAR_NAME"). Any other
// input is an error — EnvResolver only ever handles its own scheme; routing
// unrecognized schemes to the right resolver (or a clear error) is
// ChainResolver's job, not something each individual resolver should guess
// at by falling back to "treat it as a literal."
func (r *EnvResolver) Resolve(uri string) (string, error) {
	varName, ok := strings.CutPrefix(uri, "env:")
	if !ok {
		return "", fmt.Errorf("secrets: %q is not an env: URI", uri)
	}
	if varName == "" {
		return "", fmt.Errorf("secrets: env: URI missing variable name")
	}
	val, ok := os.LookupEnv(varName)
	if !ok {
		return "", fmt.Errorf("secrets: environment variable %q is not set", varName)
	}
	return val, nil
}

var _ pipeline.SecretResolver = (*EnvResolver)(nil)
