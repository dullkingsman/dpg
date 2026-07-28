package awssm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type fakeClient struct {
	secretString *string
	err          error
	gotSecretID  string
}

func (f *fakeClient) GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	if params.SecretId != nil {
		f.gotSecretID = *params.SecretId
	}
	if f.err != nil {
		return nil, f.err
	}
	return &secretsmanager.GetSecretValueOutput{SecretString: f.secretString}, nil
}

func strPtr(s string) *string { return &s }

func newTestResolver(c secretGetter) *Resolver {
	r := New()
	r.newClient = func() (secretGetter, error) { return c, nil }
	return r
}

func TestResolvePlainSecretNoField(t *testing.T) {
	c := &fakeClient{secretString: strPtr("s3cr3t")}
	r := newTestResolver(c)

	got, err := r.Resolve("aws-sm:myapp/prod/db")
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cr3t" {
		t.Errorf("got %q, want s3cr3t", got)
	}
	if c.gotSecretID != "myapp/prod/db" {
		t.Errorf("GetSecretValue called with SecretId %q, want myapp/prod/db (secret IDs are not path-split, unlike vault:)", c.gotSecretID)
	}
}

func TestResolveJSONFieldExtraction(t *testing.T) {
	c := &fakeClient{secretString: strPtr(`{"username":"app","password":"s3cr3t"}`)}
	r := newTestResolver(c)

	got, err := r.Resolve("aws-sm:myapp/prod/db#password")
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cr3t" {
		t.Errorf("got %q, want s3cr3t", got)
	}
}

func TestResolveFieldOnNonJSONSecretErrors(t *testing.T) {
	c := &fakeClient{secretString: strPtr("not-json")}
	r := newTestResolver(c)

	if _, err := r.Resolve("aws-sm:myapp/db#password"); err == nil {
		t.Fatal("expected an error extracting a field from a non-JSON secret")
	}
}

// TestResolveUnknownJSONFieldErrors checks the error message specifically
// (not just err != nil): a missing key produces a Go zero value (nil) from
// a plain map lookup, which would ALSO fail the not-a-string type assertion
// below it — so an imprecise test could pass for the wrong reason (masking
// whether the explicit "no field" check does anything at all). Confirmed by
// deliberately breaking the ok-check during development and observing this
// exact test still reported an error, just the wrong one.
func TestResolveUnknownJSONFieldErrors(t *testing.T) {
	c := &fakeClient{secretString: strPtr(`{"username":"app"}`)}
	r := newTestResolver(c)

	_, err := r.Resolve("aws-sm:myapp/db#password")
	if err == nil {
		t.Fatal("expected an error for a field not present in the JSON secret")
	}
	if !strings.Contains(err.Error(), "no field") {
		t.Errorf("expected the missing-field error specifically, got: %v", err)
	}
}

func TestResolveNonStringJSONFieldErrors(t *testing.T) {
	c := &fakeClient{secretString: strPtr(`{"port":5432}`)}
	r := newTestResolver(c)

	if _, err := r.Resolve("aws-sm:myapp/db#port"); err == nil {
		t.Fatal("expected an error for a non-string JSON field value")
	}
}

func TestResolveMissingSecretIDErrors(t *testing.T) {
	r := New()
	if _, err := r.Resolve("aws-sm:"); err == nil {
		t.Fatal("expected an error for an empty secret ID")
	}
}

func TestResolveNonAWSSMURIErrors(t *testing.T) {
	r := New()
	if _, err := r.Resolve("env:FOO"); err == nil {
		t.Fatal("expected an error for a non-aws-sm: URI")
	}
}

func TestResolveNoSecretStringErrors(t *testing.T) {
	c := &fakeClient{secretString: nil}
	r := newTestResolver(c)

	_, err := r.Resolve("aws-sm:myapp/db")
	if err == nil {
		t.Fatal("expected an error for a secret with no SecretString (e.g. binary-only)")
	}
	if !strings.Contains(err.Error(), "SecretString") {
		t.Errorf("expected error to mention SecretString, got: %v", err)
	}
}

func TestResolvePropagatesClientError(t *testing.T) {
	wantErr := errors.New("access denied")
	c := &fakeClient{err: wantErr}
	r := newTestResolver(c)

	_, err := r.Resolve("aws-sm:myapp/db")
	if !errors.Is(err, wantErr) {
		t.Errorf("expected the underlying client error to propagate, got: %v", err)
	}
}
