package pipeline

import (
	"fmt"
	"strings"
)

// Context is an assembled set of pipeline stage implementations resolved from
// a Registry. Commands operate exclusively through a Context — they never
// import concrete implementation packages directly.
type Context struct {
	Tokenizer           Tokenizer
	PGSQLParser         PGSQLParser
	BlockParser         BlockParser
	IRBuilder           IRBuilder
	Merger              Merger
	DependencyResolver  DependencyResolver
	SnapshotStore       SnapshotStore
	Differ              Differ
	Emitter             Emitter
	ApplyExecutor       ApplyExecutor
	Introspector        Introspector
	Linter              Linter
	PortabilityAnalyzer PortabilityAnalyzer
	SecretResolver      SecretResolver
}

// ContextFromRegistry resolves all pipeline stage implementations from r and
// returns a fully populated Context. Returns an error listing every stage that
// has no registered implementation.
func ContextFromRegistry(r *Registry) (*Context, error) {
	var missing []string
	ctx := &Context{}

	if v, ok := Resolve[Tokenizer](r, KeyTokenizer); ok {
		ctx.Tokenizer = v
	} else {
		missing = append(missing, KeyTokenizer)
	}

	if v, ok := Resolve[PGSQLParser](r, KeyPGSQLParser); ok {
		ctx.PGSQLParser = v
	} else {
		missing = append(missing, KeyPGSQLParser)
	}

	if v, ok := Resolve[BlockParser](r, KeyBlockParser); ok {
		ctx.BlockParser = v
	} else {
		missing = append(missing, KeyBlockParser)
	}

	if v, ok := Resolve[IRBuilder](r, KeyIRBuilder); ok {
		ctx.IRBuilder = v
	} else {
		missing = append(missing, KeyIRBuilder)
	}

	if v, ok := Resolve[Merger](r, KeyMerger); ok {
		ctx.Merger = v
	} else {
		missing = append(missing, KeyMerger)
	}

	if v, ok := Resolve[DependencyResolver](r, KeyDependencyResolver); ok {
		ctx.DependencyResolver = v
	} else {
		missing = append(missing, KeyDependencyResolver)
	}

	if v, ok := Resolve[SnapshotStore](r, KeySnapshotStore); ok {
		ctx.SnapshotStore = v
	} else {
		missing = append(missing, KeySnapshotStore)
	}

	if v, ok := Resolve[Differ](r, KeyDiffer); ok {
		ctx.Differ = v
	} else {
		missing = append(missing, KeyDiffer)
	}

	if v, ok := Resolve[Emitter](r, KeyEmitter); ok {
		ctx.Emitter = v
	} else {
		missing = append(missing, KeyEmitter)
	}

	if v, ok := Resolve[ApplyExecutor](r, KeyApplyExecutor); ok {
		ctx.ApplyExecutor = v
	} else {
		missing = append(missing, KeyApplyExecutor)
	}

	if v, ok := Resolve[Introspector](r, KeyIntrospector); ok {
		ctx.Introspector = v
	} else {
		missing = append(missing, KeyIntrospector)
	}

	if v, ok := Resolve[Linter](r, KeyLinter); ok {
		ctx.Linter = v
	} else {
		missing = append(missing, KeyLinter)
	}

	if v, ok := Resolve[PortabilityAnalyzer](r, KeyPortabilityAnalyzer); ok {
		ctx.PortabilityAnalyzer = v
	} else {
		missing = append(missing, KeyPortabilityAnalyzer)
	}

	if v, ok := Resolve[SecretResolver](r, KeySecretResolver); ok {
		ctx.SecretResolver = v
	} else {
		missing = append(missing, KeySecretResolver)
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("pipeline: no implementation registered for: %s",
			strings.Join(missing, ", "))
	}

	return ctx, nil
}
