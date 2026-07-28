package pipeline

import (
	"errors"
	"strings"
	"testing"
)

type fakeResolver struct {
	values map[string]string
	err    error
	gotRef string
}

func (f *fakeResolver) Resolve(uri string) (string, error) {
	f.gotRef = uri
	if f.err != nil {
		return "", f.err
	}
	return f.values[uri], nil
}

func TestResolveTemplateNoPlaceholderReturnsUnchanged(t *testing.T) {
	r := &fakeResolver{}
	got, err := ResolveTemplate("host=x user=y password=hunter2", r)
	if err != nil {
		t.Fatal(err)
	}
	if got != "host=x user=y password=hunter2" {
		t.Errorf("got %q, want unchanged", got)
	}
	if r.gotRef != "" {
		t.Error("resolver.Resolve was called for a string with no {{...}} placeholder")
	}
}

func TestResolveTemplateSinglePlaceholder(t *testing.T) {
	r := &fakeResolver{values: map[string]string{"vault:secret/db#pw": "s3cr3t"}}
	got, err := ResolveTemplate("host=x user=y password={{vault:secret/db#pw}}", r)
	if err != nil {
		t.Fatal(err)
	}
	want := "host=x user=y password=s3cr3t"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveTemplateWholeValuePlaceholder(t *testing.T) {
	r := &fakeResolver{values: map[string]string{"vault:secret/db#conninfo": "host=real dbname=real"}}
	got, err := ResolveTemplate("{{vault:secret/db#conninfo}}", r)
	if err != nil {
		t.Fatal(err)
	}
	if got != "host=real dbname=real" {
		t.Errorf("got %q, want the fully resolved conninfo", got)
	}
}

func TestResolveTemplateMultiplePlaceholders(t *testing.T) {
	r := &fakeResolver{values: map[string]string{
		"env:DB_USER": "app",
		"env:DB_PASS": "s3cr3t",
	}}
	got, err := ResolveTemplate("user={{env:DB_USER}} password={{env:DB_PASS}}", r)
	if err != nil {
		t.Fatal(err)
	}
	if got != "user=app password=s3cr3t" {
		t.Errorf("got %q, want both placeholders substituted", got)
	}
}

func TestResolveTemplatePropagatesResolverError(t *testing.T) {
	wantErr := errors.New("vault unreachable")
	r := &fakeResolver{err: wantErr}
	_, err := ResolveTemplate("password={{vault:secret/db#pw}}", r)
	if !errors.Is(err, wantErr) {
		t.Errorf("expected the underlying resolver error to propagate, got: %v", err)
	}
	if !strings.Contains(err.Error(), "vault:secret/db#pw") {
		t.Errorf("expected error to name the failing placeholder, got: %v", err)
	}
}

func TestResolveTemplateEmptyPlaceholderLeftLiteral(t *testing.T) {
	r := &fakeResolver{}
	got, err := ResolveTemplate("password={{}}", r)
	if err != nil {
		t.Fatal(err)
	}
	if got != "password={{}}" {
		t.Errorf("got %q, want the empty {{}} left untouched (no valid ref inside)", got)
	}
	if r.gotRef != "" {
		t.Error("resolver.Resolve was called for an empty {{}} placeholder")
	}
}
