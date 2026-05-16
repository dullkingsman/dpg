package pipeline

import (
	"fmt"
	"sort"
	"sync"
)

// Well-known registry keys — one per pipeline stage interface.
const (
	KeyTokenizer           = "Tokenizer"
	KeyPGSQLParser         = "PGSQLParser"
	KeyBlockParser         = "BlockParser"
	KeyIRBuilder           = "IRBuilder"
	KeyMerger              = "Merger"
	KeyDependencyResolver  = "DependencyResolver"
	KeySnapshotStore       = "SnapshotStore"
	KeyDiffer              = "Differ"
	KeyEmitter             = "Emitter"
	KeyApplyExecutor       = "ApplyExecutor"
	KeyIntrospector        = "Introspector"
	KeyLinter              = "Linter"
	KeyPortabilityAnalyzer = "PortabilityAnalyzer"
	KeySecretResolver      = "SecretResolver"
)

// Registry holds named pipeline stage implementations. Concrete packages
// register their implementations in init() or via an explicit Register call.
// Commands resolve implementations through the registry at startup.
type Registry struct {
	mu    sync.RWMutex
	store map[string]any
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{store: make(map[string]any)}
}

// Default is the process-wide registry. Concrete implementation packages
// register into this registry from their init() functions.
var Default = NewRegistry()

// Register stores impl under key, replacing any prior value.
func (r *Registry) Register(key string, impl any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store[key] = impl
}

// Resolve returns the value stored under key and whether it was found.
func Resolve[T any](r *Registry, key string) (T, bool) {
	r.mu.RLock()
	v, ok := r.store[key]
	r.mu.RUnlock()
	if !ok {
		var zero T
		return zero, false
	}
	impl, ok := v.(T)
	return impl, ok
}

// MustResolve returns the implementation for key or returns an error if
// the key is missing or the stored value does not implement T.
func MustResolve[T any](r *Registry, key string) (T, error) {
	impl, ok := Resolve[T](r, key)
	if !ok {
		var zero T
		return zero, fmt.Errorf("registry: no implementation registered for %q", key)
	}
	return impl, nil
}

// Keys returns the sorted list of registered keys.
func (r *Registry) Keys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.store))
	for k := range r.store {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
