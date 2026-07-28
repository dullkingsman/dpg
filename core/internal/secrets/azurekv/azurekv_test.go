package azurekv

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

type fakeClient struct {
	value      *string
	err        error
	gotName    string
	gotVersion string
}

func (f *fakeClient) GetSecret(ctx context.Context, name string, version string, options *azsecrets.GetSecretOptions) (azsecrets.GetSecretResponse, error) {
	f.gotName = name
	f.gotVersion = version
	if f.err != nil {
		return azsecrets.GetSecretResponse{}, f.err
	}
	return azsecrets.GetSecretResponse{Secret: azsecrets.Secret{Value: f.value}}, nil
}

func strPtr(s string) *string { return &s }

func newTestResolver(vaultName string, c secretGetter) *Resolver {
	r := New()
	r.newClient = func(gotVault string) (secretGetter, error) {
		if gotVault != vaultName {
			return nil, errors.New("unexpected vault name")
		}
		return c, nil
	}
	return r
}

func TestResolvePlainSecretNoVersionNoField(t *testing.T) {
	c := &fakeClient{value: strPtr("s3cr3t")}
	r := newTestResolver("my-vault", c)

	got, err := r.Resolve("azure-kv:my-vault/db-password")
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cr3t" {
		t.Errorf("got %q, want s3cr3t", got)
	}
	if c.gotName != "db-password" {
		t.Errorf("GetSecret called with name %q, want db-password", c.gotName)
	}
	if c.gotVersion != "" {
		t.Errorf("GetSecret called with version %q, want empty (latest)", c.gotVersion)
	}
}

func TestResolveExplicitVersion(t *testing.T) {
	c := &fakeClient{value: strPtr("s3cr3t")}
	r := newTestResolver("my-vault", c)

	if _, err := r.Resolve("azure-kv:my-vault/db-password/abc123"); err != nil {
		t.Fatal(err)
	}
	if c.gotVersion != "abc123" {
		t.Errorf("GetSecret called with version %q, want abc123", c.gotVersion)
	}
}

func TestResolveJSONFieldExtraction(t *testing.T) {
	c := &fakeClient{value: strPtr(`{"username":"app","password":"s3cr3t"}`)}
	r := newTestResolver("my-vault", c)

	got, err := r.Resolve("azure-kv:my-vault/db#password")
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cr3t" {
		t.Errorf("got %q, want s3cr3t", got)
	}
}

func TestResolveMalformedURIErrors(t *testing.T) {
	r := New()
	cases := []string{
		"azure-kv:onlyvault",
		"azure-kv:/db-password",
		"azure-kv:my-vault/",
	}
	for _, uri := range cases {
		if _, err := r.Resolve(uri); err == nil {
			t.Errorf("Resolve(%q): expected an error", uri)
		}
	}
}

func TestResolveNonAzureKVURIErrors(t *testing.T) {
	r := New()
	if _, err := r.Resolve("env:FOO"); err == nil {
		t.Fatal("expected an error for a non-azure-kv: URI")
	}
}

func TestResolveFieldOnNonJSONSecretErrors(t *testing.T) {
	c := &fakeClient{value: strPtr("not-json")}
	r := newTestResolver("my-vault", c)

	if _, err := r.Resolve("azure-kv:my-vault/db#password"); err == nil {
		t.Fatal("expected an error extracting a field from a non-JSON secret")
	}
}

func TestResolveNonStringJSONFieldErrors(t *testing.T) {
	c := &fakeClient{value: strPtr(`{"port":5432}`)}
	r := newTestResolver("my-vault", c)

	if _, err := r.Resolve("azure-kv:my-vault/db#port"); err == nil {
		t.Fatal("expected an error for a non-string JSON field value")
	}
}

func TestResolveNilValueErrors(t *testing.T) {
	c := &fakeClient{value: nil}
	r := newTestResolver("my-vault", c)

	if _, err := r.Resolve("azure-kv:my-vault/db-password"); err == nil {
		t.Fatal("expected an error for a secret with a nil value")
	}
}

func TestResolveUnknownJSONFieldErrors(t *testing.T) {
	c := &fakeClient{value: strPtr(`{"username":"app"}`)}
	r := newTestResolver("my-vault", c)

	_, err := r.Resolve("azure-kv:my-vault/db#password")
	if err == nil {
		t.Fatal("expected an error for a field not present in the JSON secret")
	}
}

func TestResolvePropagatesClientError(t *testing.T) {
	wantErr := errors.New("forbidden")
	c := &fakeClient{err: wantErr}
	r := newTestResolver("my-vault", c)

	_, err := r.Resolve("azure-kv:my-vault/db-password")
	if !errors.Is(err, wantErr) {
		t.Errorf("expected the underlying client error to propagate, got: %v", err)
	}
}
