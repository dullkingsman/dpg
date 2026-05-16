package secrets

import (
	"os"
	"testing"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

func TestResolvePlainValue(t *testing.T) {
	r := New()
	got, err := r.Resolve("postgres://localhost/mydb")
	if err != nil {
		t.Fatal(err)
	}
	if got != "postgres://localhost/mydb" {
		t.Errorf("expected plain value returned as-is, got %q", got)
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

func TestResolveLinkPlain(t *testing.T) {
	r := New()
	got, err := r.Resolve("link:postgres://localhost/mydb")
	if err != nil {
		t.Fatal(err)
	}
	if got != "postgres://localhost/mydb" {
		t.Errorf("expected plain value via link:, got %q", got)
	}
}

func TestResolveLinkEnv(t *testing.T) {
	r := New()
	t.Setenv("DPG_LINK_TARGET", "s3cr3t")
	got, err := r.Resolve("link:env:DPG_LINK_TARGET")
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cr3t" {
		t.Errorf("expected s3cr3t via link:env:, got %q", got)
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
}
