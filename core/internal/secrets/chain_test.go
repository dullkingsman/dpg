package secrets

import (
	"errors"
	"strings"
	"testing"
)

// fakeResolver is a minimal pipeline.SecretResolver for testing dispatch
// without depending on EnvResolver's own behavior.
type fakeResolver struct {
	value string
	err   error
}

func (f fakeResolver) Resolve(uri string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.value, nil
}

func TestChainResolverDispatchesByScheme(t *testing.T) {
	c := NewChain()
	c.Register("foo", fakeResolver{value: "foo-value"})
	c.Register("bar", fakeResolver{value: "bar-value"})

	got, err := c.Resolve("foo:anything")
	if err != nil {
		t.Fatal(err)
	}
	if got != "foo-value" {
		t.Errorf("got %q, want foo-value", got)
	}

	got, err = c.Resolve("bar:anything")
	if err != nil {
		t.Fatal(err)
	}
	if got != "bar-value" {
		t.Errorf("got %q, want bar-value", got)
	}
}

// TestChainResolverPassesFullURI guards the contract that each registered
// resolver receives the full original URI (including its own scheme
// prefix), not a stripped remainder — the same contract EnvResolver already
// had before ChainResolver existed, so resolvers stay independently
// constructible and testable without a ChainResolver in the loop at all.
func TestChainResolverPassesFullURI(t *testing.T) {
	var received string
	c := NewChain()
	c.Register("foo", resolveFunc(func(uri string) (string, error) {
		received = uri
		return "ok", nil
	}))
	if _, err := c.Resolve("foo:bar:baz"); err != nil {
		t.Fatal(err)
	}
	if received != "foo:bar:baz" {
		t.Errorf("resolver received %q, want the full original URI %q", received, "foo:bar:baz")
	}
}

// TestChainResolverUnrecognizedSchemeErrors is the direct regression guard
// for the original bug: an unrecognized scheme must error clearly, not
// silently resolve to the URI itself as a literal value.
func TestChainResolverUnrecognizedSchemeErrors(t *testing.T) {
	c := NewChain()
	c.Register("env", fakeResolver{value: "unused"})

	_, err := c.Resolve("vault:secret/db")
	if err == nil {
		t.Fatal("expected an error for an unregistered scheme, got none")
	}
	if !strings.Contains(err.Error(), "vault") {
		t.Errorf("expected error to name the unrecognized scheme, got: %v", err)
	}
	if !strings.Contains(err.Error(), "env:") {
		t.Errorf("expected error to list known schemes (env:), got: %v", err)
	}
}

// TestChainResolverNoSchemeErrors guards a bare value with no ':' at all —
// this can never be a secret URI regardless of what's registered.
func TestChainResolverNoSchemeErrors(t *testing.T) {
	c := NewChain()
	c.Register("env", fakeResolver{value: "unused"})

	if _, err := c.Resolve("just-a-plain-string"); err == nil {
		t.Fatal("expected an error for a value with no scheme, got none")
	}
}

// TestChainResolverPlainConnectionStringErrors is the exact real-world
// shape of the original bug: a plain `postgresql://...` connection string
// (which itself contains a ':') mistakenly placed in a `link` field. Before
// this fix this silently "resolved" to itself; now it must error clearly
// rather than reach pgx.Connect with a nonsense value.
func TestChainResolverPlainConnectionStringErrors(t *testing.T) {
	c := NewChain()
	c.Register("env", fakeResolver{value: "unused"})

	_, err := c.Resolve("postgresql://user@host:5432/db")
	if err == nil {
		t.Fatal("expected an error for a plain connection string used as a link value, got none")
	}
}

func TestChainResolverPropagatesResolverError(t *testing.T) {
	c := NewChain()
	wantErr := errors.New("boom")
	c.Register("env", fakeResolver{err: wantErr})

	_, err := c.Resolve("env:X")
	if !errors.Is(err, wantErr) {
		t.Errorf("expected the underlying resolver's error to propagate, got: %v", err)
	}
}

// TestChainResolverReRegisterOverwrites guards the documented
// last-registration-wins convention.
func TestChainResolverReRegisterOverwrites(t *testing.T) {
	c := NewChain()
	c.Register("env", fakeResolver{value: "first"})
	c.Register("env", fakeResolver{value: "second"})

	got, err := c.Resolve("env:X")
	if err != nil {
		t.Fatal(err)
	}
	if got != "second" {
		t.Errorf("got %q, want second (later registration should win)", got)
	}
}

type resolveFunc func(uri string) (string, error)

func (f resolveFunc) Resolve(uri string) (string, error) { return f(uri) }
