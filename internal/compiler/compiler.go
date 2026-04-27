// Package compiler orchestrates the DPG pipeline stages from source files
// through to a sorted []pipeline.IRObject ready for diffing.
package compiler

import (
	"fmt"
	"os"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

// Compile reads all source files, runs them through every pipeline stage up to
// dependency resolution, and returns a sorted slice of fully-resolved IRObjects.
// All pipeline stage implementations are resolved from the provided registry.
func Compile(files []string, reg *pipeline.Registry) ([]pipeline.IRObject, error) {
	tokenizer, err := pipeline.MustResolve[pipeline.Tokenizer](reg, pipeline.KeyTokenizer)
	if err != nil {
		return nil, err
	}
	pgParser, err := pipeline.MustResolve[pipeline.PGSQLParser](reg, pipeline.KeyPGSQLParser)
	if err != nil {
		return nil, err
	}
	blockParser, err := pipeline.MustResolve[pipeline.BlockParser](reg, pipeline.KeyBlockParser)
	if err != nil {
		return nil, err
	}
	irBuilder, err := pipeline.MustResolve[pipeline.IRBuilder](reg, pipeline.KeyIRBuilder)
	if err != nil {
		return nil, err
	}
	merger, err := pipeline.MustResolve[pipeline.Merger](reg, pipeline.KeyMerger)
	if err != nil {
		return nil, err
	}
	resolver, err := pipeline.MustResolve[pipeline.DependencyResolver](reg, pipeline.KeyDependencyResolver)
	if err != nil {
		return nil, err
	}

	var rawObjects []pipeline.RawObject
	var diags pipeline.Diagnostics

	// Stage 1: Tokenize all source files.
	for _, path := range files {
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("compiler: reading %s: %w", path, readErr)
		}
		raws, scanErr := tokenizer.Scan(path, src)
		if scanErr != nil {
			if d, ok := scanErr.(pipeline.Diagnostics); ok {
				diags = append(diags, d...)
				continue
			}
			return nil, fmt.Errorf("compiler: scanning %s: %w", path, scanErr)
		}
		rawObjects = append(rawObjects, raws...)
	}
	if diags.HasErrors() {
		return nil, diags
	}

	// Stages 2–3: Parse Part1 (PG SQL) + Part2 ({ } block) and build IR.
	var irObjects []pipeline.IRObject
	for _, raw := range rawObjects {
		pgResult, pgErr := pgParser.Parse(raw.Kind, raw.Part1, raw.Pos)
		if pgErr != nil {
			if ce, ok := pgErr.(*pipeline.CompilerError); ok {
				diags = append(diags, ce)
				continue
			}
			return nil, pgErr
		}

		blockAST, blockErr := blockParser.Parse(raw.Kind, raw.Part2, raw.Pos)
		if blockErr != nil {
			if ce, ok := blockErr.(*pipeline.CompilerError); ok {
				diags = append(diags, ce)
				continue
			}
			return nil, blockErr
		}

		obj, buildErr := irBuilder.Build(pgResult, blockAST)
		if buildErr != nil {
			if ce, ok := buildErr.(*pipeline.CompilerError); ok {
				diags = append(diags, ce)
				continue
			}
			return nil, buildErr
		}
		irObjects = append(irObjects, obj)
	}
	if diags.HasErrors() {
		return nil, diags
	}

	// Stage 4: Merge same-name declarations across files.
	merged, mergeErr := merger.Merge(irObjects)
	if mergeErr != nil {
		return nil, mergeErr
	}

	// Stage 5: Topological sort with FK / type dependency resolution.
	sorted, sortErr := resolver.Sort(merged)
	if sortErr != nil {
		return nil, sortErr
	}

	return sorted, nil
}
