package gcpsm

import (
	"context"
	"errors"
	"testing"

	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	gax "github.com/googleapis/gax-go/v2"
)

type fakeClient struct {
	data      []byte
	noPayload bool
	err       error
	gotName   string
}

func (f *fakeClient) AccessSecretVersion(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	f.gotName = req.Name
	if f.err != nil {
		return nil, f.err
	}
	if f.noPayload {
		return &secretmanagerpb.AccessSecretVersionResponse{}, nil
	}
	return &secretmanagerpb.AccessSecretVersionResponse{
		Payload: &secretmanagerpb.SecretPayload{Data: f.data},
	}, nil
}

func newTestResolver(c secretAccessor) *Resolver {
	r := New()
	r.newClient = func(ctx context.Context) (secretAccessor, error) { return c, nil }
	return r
}

func TestResolvePlainSecretDefaultsToLatest(t *testing.T) {
	c := &fakeClient{data: []byte("s3cr3t")}
	r := newTestResolver(c)

	got, err := r.Resolve("gcp-sm:my-project/db-password")
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cr3t" {
		t.Errorf("got %q, want s3cr3t", got)
	}
	wantName := "projects/my-project/secrets/db-password/versions/latest"
	if c.gotName != wantName {
		t.Errorf("AccessSecretVersion called with Name %q, want %q", c.gotName, wantName)
	}
}

func TestResolveExplicitVersion(t *testing.T) {
	c := &fakeClient{data: []byte("s3cr3t")}
	r := newTestResolver(c)

	if _, err := r.Resolve("gcp-sm:my-project/db-password/7"); err != nil {
		t.Fatal(err)
	}
	wantName := "projects/my-project/secrets/db-password/versions/7"
	if c.gotName != wantName {
		t.Errorf("AccessSecretVersion called with Name %q, want %q", c.gotName, wantName)
	}
}

func TestResolveJSONFieldExtraction(t *testing.T) {
	c := &fakeClient{data: []byte(`{"username":"app","password":"s3cr3t"}`)}
	r := newTestResolver(c)

	got, err := r.Resolve("gcp-sm:my-project/db#password")
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
		"gcp-sm:onlyproject",
		"gcp-sm:/db-password",
		"gcp-sm:my-project/",
		"gcp-sm:a/b/c/d",
	}
	for _, uri := range cases {
		if _, err := r.Resolve(uri); err == nil {
			t.Errorf("Resolve(%q): expected an error", uri)
		}
	}
}

func TestResolveFieldOnNonJSONSecretErrors(t *testing.T) {
	c := &fakeClient{data: []byte("not-json")}
	r := newTestResolver(c)

	if _, err := r.Resolve("gcp-sm:my-project/db#password"); err == nil {
		t.Fatal("expected an error extracting a field from a non-JSON secret")
	}
}

func TestResolveNonStringJSONFieldErrors(t *testing.T) {
	c := &fakeClient{data: []byte(`{"port":5432}`)}
	r := newTestResolver(c)

	if _, err := r.Resolve("gcp-sm:my-project/db#port"); err == nil {
		t.Fatal("expected an error for a non-string JSON field value")
	}
}

func TestResolveNonGCPSMURIErrors(t *testing.T) {
	r := New()
	if _, err := r.Resolve("env:FOO"); err == nil {
		t.Fatal("expected an error for a non-gcp-sm: URI")
	}
}

func TestResolveNoPayloadErrors(t *testing.T) {
	c := &fakeClient{noPayload: true}
	r := newTestResolver(c)

	if _, err := r.Resolve("gcp-sm:my-project/db-password"); err == nil {
		t.Fatal("expected an error for a response with no payload")
	}
}

func TestResolveUnknownJSONFieldErrors(t *testing.T) {
	c := &fakeClient{data: []byte(`{"username":"app"}`)}
	r := newTestResolver(c)

	_, err := r.Resolve("gcp-sm:my-project/db#password")
	if err == nil {
		t.Fatal("expected an error for a field not present in the JSON secret")
	}
}

func TestResolvePropagatesClientError(t *testing.T) {
	wantErr := errors.New("permission denied")
	c := &fakeClient{err: wantErr}
	r := newTestResolver(c)

	_, err := r.Resolve("gcp-sm:my-project/db-password")
	if !errors.Is(err, wantErr) {
		t.Errorf("expected the underlying client error to propagate, got: %v", err)
	}
}
