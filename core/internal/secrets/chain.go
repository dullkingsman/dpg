package secrets

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

// ChainResolver dispatches a secret URI to whichever resolver is registered
// for its scheme (the part before the first ':'), erroring clearly if the
// scheme is unrecognized. This is the RFC's promised "ChainResolver" (D.5).
//
// Despite the name, it is deliberately NOT a fallback chain that tries
// multiple resolvers per URI in sequence: PostgreSQL secret schemes are
// unambiguous by construction (a URI's own prefix says exactly which
// backend must handle it), so there is never a genuine "which one applies"
// question to resolve by trying candidates. A try-in-sequence design is
// also how the original bug happened in EnvResolver: a resolver that didn't
// recognize a scheme fell through to "treat as a plain value" instead of
// correctly saying "not mine, try something else" — silently returning
// nonsense (e.g. a `vault:...` URI in a project with no vault resolver
// configured, silently used as a literal, unparseable connection string)
// instead of a clear, actionable error. A scheme-keyed map makes that
// failure mode structurally impossible: every URI either matches a
// registered scheme or is rejected immediately, before ever reaching a
// database connection attempt.
type ChainResolver struct {
	resolvers map[string]pipeline.SecretResolver
}

// NewChain returns an empty ChainResolver. Register schemes with Register.
func NewChain() *ChainResolver {
	return &ChainResolver{resolvers: make(map[string]pipeline.SecretResolver)}
}

// Register adds resolver as the handler for scheme (without the trailing
// colon, e.g. "env", "vault"). Registering the same scheme twice overwrites
// the previous registration — later registrations win, matching the
// last-registration-wins convention pipeline.Registry itself already uses.
func (c *ChainResolver) Register(scheme string, resolver pipeline.SecretResolver) {
	c.resolvers[scheme] = resolver
}

// Resolve extracts uri's scheme and delegates to its registered resolver,
// passing the full original uri (including the scheme prefix) unchanged —
// every registered resolver is responsible for its own prefix handling,
// the same contract EnvResolver already had before this type existed, so
// each resolver stays independently constructible and testable without
// requiring a ChainResolver at all.
func (c *ChainResolver) Resolve(uri string) (string, error) {
	scheme, ok := uriScheme(uri)
	if !ok {
		return "", fmt.Errorf(
			"secrets: %q is not a secret URI (expected a scheme like \"env:VAR\") — "+
				"if this is meant to be a literal connection string, use \"url\" instead of \"link\"", uri)
	}
	r, ok := c.resolvers[scheme]
	if !ok {
		return "", fmt.Errorf("secrets: unrecognized scheme %q (known: %s)", scheme, strings.Join(c.knownSchemes(), ", "))
	}
	return r.Resolve(uri)
}

func (c *ChainResolver) knownSchemes() []string {
	schemes := make([]string, 0, len(c.resolvers))
	for s := range c.resolvers {
		schemes = append(schemes, s+":")
	}
	sort.Strings(schemes)
	return schemes
}

// uriScheme extracts the scheme prefix (the substring before the first ':')
// from uri. Returns ok=false if uri has no ':' at all — a bare value can
// never be a secret URI, regardless of what schemes are registered.
func uriScheme(uri string) (scheme string, ok bool) {
	idx := strings.IndexByte(uri, ':')
	if idx <= 0 {
		return "", false
	}
	return uri[:idx], true
}

var _ pipeline.SecretResolver = (*ChainResolver)(nil)
