package secrets

import (
	"os"
	"testing"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

// TestResolveNonEnvURIErrors guards EnvResolver's strict contract: it only
// ever handles "env:" URIs. Passthrough for anything else was the original
// bug (see chain.go's doc comment) — a resolver silently treating an
// unrecognized value as a literal, rather than saying "not mine."
func TestResolveNonEnvURIErrors(t *testing.T) {
	r := New()
	if _, err := r.Resolve("postgres://localhost/mydb"); err == nil {
		t.Fatal("expected an error for a non-env: URI, got none")
	}
}

func TestResolveEnvSet(t *testing.T) {
	r := New()
	t.Setenv("DPG_TEST_SECRET", "s3cr3t")
	got, err := r.Resolve("env:DPG_TEST_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cr3t" {
		t.Errorf("expected s3cr3t, got %q", got)
	}
}

func TestResolveEnvMissing(t *testing.T) {
	r := New()
	os.Unsetenv("DPG_TEST_MISSING_VAR")
	_, err := r.Resolve("env:DPG_TEST_MISSING_VAR")
	if err == nil {
		t.Fatal("expected error for unset env var")
	}
}

func TestResolveEnvEmptyName(t *testing.T) {
	r := New()
	_, err := r.Resolve("env:")
	if err == nil {
		t.Fatal("expected error for empty env var name")
	}
}

// TestResolveLinkPrefixIsNotAScheme guards against reviving the removed
// link:-as-URI-prefix behavior: `link` is a dpg.toml *key* name (RFC Section 3.3),
// never a URI scheme, so a literal "link:" prefix inside a value is just an
// ordinary unrecognized scheme like any other typo, not special-cased.
func TestResolveLinkPrefixIsNotAScheme(t *testing.T) {
	r := New()
	if _, err := r.Resolve("link:env:DPG_LINK_TARGET"); err == nil {
		t.Fatal(`expected an error for "link:..." — link: is not a URI scheme`)
	}
}

func TestResolveRegistration(t *testing.T) {
	r, ok := pipeline.Resolve[pipeline.SecretResolver](pipeline.Default, pipeline.KeySecretResolver)
	if !ok {
		t.Fatal("SecretResolver not registered")
	}
	if r == nil {
		t.Fatal("registered SecretResolver is nil")
	}
	// The default registration is a ChainResolver with "env" wired in — not
	// a bare EnvResolver — so it must resolve env: URIs (delegated) but
	// reject unregistered schemes with a clear error (see chain_test.go for
	// the direct ChainResolver-level coverage of that).
	if _, ok := r.(*ChainResolver); !ok {
		t.Fatalf("expected the default SecretResolver to be a *ChainResolver, got %T", r)
	}
}
