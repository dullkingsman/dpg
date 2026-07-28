package vault

import (
	"context"
	"errors"
	"strings"
	"testing"

	vaultapi "github.com/hashicorp/vault/api"
)

type fakeKV struct {
	secret *vaultapi.KVSecret
	err    error
	// gotPath records the path passed to Get, for call-shape assertions.
	gotPath string
}

func (f *fakeKV) Get(ctx context.Context, secretPath string) (*vaultapi.KVSecret, error) {
	f.gotPath = secretPath
	if f.err != nil {
		return nil, f.err
	}
	return f.secret, nil
}

func newTestResolver(mount string, kv kvGetter) *Resolver {
	r := New()
	r.newKV = func(gotMount string) (kvGetter, error) {
		if gotMount != mount {
			return nil, errors.New("unexpected mount")
		}
		return kv, nil
	}
	return r
}

func TestResolveExtractsField(t *testing.T) {
	kv := &fakeKV{secret: &vaultapi.KVSecret{Data: map[string]any{
		"password": "s3cr3t",
		"username": "app",
	}}}
	r := newTestResolver("secret", kv)

	got, err := r.Resolve("vault:secret/myapp/db#password")
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cr3t" {
		t.Errorf("got %q, want s3cr3t", got)
	}
	if kv.gotPath != "myapp/db" {
		t.Errorf("Get called with path %q, want myapp/db", kv.gotPath)
	}
}

func TestResolveMissingFieldRequired(t *testing.T) {
	r := New()
	_, err := r.Resolve("vault:secret/myapp/db")
	if err == nil {
		t.Fatal("expected an error for a vault: URI with no #field")
	}
	if !strings.Contains(err.Error(), "#field") {
		t.Errorf("expected error to explain the missing #field, got: %v", err)
	}
}

func TestResolveMalformedPath(t *testing.T) {
	r := New()
	cases := []string{
		"vault:#field",       // empty mount+path
		"vault:justmount#f",  // no '/' separating mount from path
		"vault:mount/#field", // empty path
	}
	for _, uri := range cases {
		if _, err := r.Resolve(uri); err == nil {
			t.Errorf("Resolve(%q): expected an error", uri)
		}
	}
}

func TestResolveNonVaultURIErrors(t *testing.T) {
	r := New()
	if _, err := r.Resolve("env:FOO"); err == nil {
		t.Fatal("expected an error for a non-vault: URI")
	}
}

func TestResolveUnknownFieldErrors(t *testing.T) {
	kv := &fakeKV{secret: &vaultapi.KVSecret{Data: map[string]any{"password": "x"}}}
	r := newTestResolver("secret", kv)

	_, err := r.Resolve("vault:secret/myapp/db#nonexistent")
	if err == nil {
		t.Fatal("expected an error for a field not present in the secret")
	}
}

func TestResolveNonStringFieldErrors(t *testing.T) {
	kv := &fakeKV{secret: &vaultapi.KVSecret{Data: map[string]any{"count": 5}}}
	r := newTestResolver("secret", kv)

	_, err := r.Resolve("vault:secret/myapp/db#count")
	if err == nil {
		t.Fatal("expected an error for a non-string field value")
	}
}

func TestResolveSecretNotFound(t *testing.T) {
	kv := &fakeKV{secret: nil}
	r := newTestResolver("secret", kv)

	_, err := r.Resolve("vault:secret/myapp/db#password")
	if err == nil {
		t.Fatal("expected an error for a nil secret (not found)")
	}
}

func TestResolvePropagatesClientError(t *testing.T) {
	wantErr := errors.New("connection refused")
	kv := &fakeKV{err: wantErr}
	r := newTestResolver("secret", kv)

	_, err := r.Resolve("vault:secret/myapp/db#password")
	if !errors.Is(err, wantErr) {
		t.Errorf("expected the underlying client error to propagate, got: %v", err)
	}
}
