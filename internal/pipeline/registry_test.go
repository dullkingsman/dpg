package pipeline

import (
	"testing"
)

func TestRegistryRegisterAndResolve(t *testing.T) {
	r := NewRegistry()
	r.Register("mykey", "hello")

	got, ok := Resolve[string](r, "mykey")
	if !ok {
		t.Fatal("expected Resolve to find registered key")
	}
	if got != "hello" {
		t.Errorf("expected %q, got %q", "hello", got)
	}
}

func TestRegistryResolveMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := Resolve[string](r, "missing")
	if ok {
		t.Fatal("expected Resolve to return false for missing key")
	}
}

func TestRegistryResolveWrongType(t *testing.T) {
	r := NewRegistry()
	r.Register("mykey", 42)

	_, ok := Resolve[string](r, "mykey")
	if ok {
		t.Fatal("expected Resolve to return false for wrong type assertion")
	}
}

func TestMustResolve(t *testing.T) {
	r := NewRegistry()
	r.Register("k", "v")

	got, err := MustResolve[string](r, "k")
	if err != nil {
		t.Fatal(err)
	}
	if got != "v" {
		t.Errorf("expected %q, got %q", "v", got)
	}
}

func TestMustResolveMissing(t *testing.T) {
	r := NewRegistry()
	_, err := MustResolve[string](r, "absent")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestRegistryRegisterReplace(t *testing.T) {
	r := NewRegistry()
	r.Register("k", "first")
	r.Register("k", "second")

	got, ok := Resolve[string](r, "k")
	if !ok {
		t.Fatal("expected key to be found")
	}
	if got != "second" {
		t.Errorf("expected second registration to win, got %q", got)
	}
}

func TestRegistryKeys(t *testing.T) {
	r := NewRegistry()
	r.Register("b", 1)
	r.Register("a", 2)
	r.Register("c", 3)

	keys := r.Keys()
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	if keys[0] != "a" || keys[1] != "b" || keys[2] != "c" {
		t.Errorf("expected sorted keys [a b c], got %v", keys)
	}
}
