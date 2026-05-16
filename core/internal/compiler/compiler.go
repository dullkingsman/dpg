// Package compiler orchestrates the DPG pipeline stages from source files
// through to a sorted []pipeline.IRObject ready for diffing.
package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

// Compile reads all source files, runs them through every pipeline stage up to
// dependency resolution, and returns a sorted slice of fully-resolved IRObjects.
//
// dbDir is the database source root directory. Files located under
// dbDir/schemas/<name>/... have their schema context inferred from the directory
// name when no explicit SCHEMA block is present. A SCHEMA block inside the
// schemas/ hierarchy is a compile error.
func Compile(files []string, dbDir string, reg *pipeline.Registry) ([]pipeline.IRObject, error) {
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
	// Track unique directory-inferred schemas so we can inject synthetic declarations.
	dirSchemas := map[string]pipeline.SourcePos{}

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

		dirSchema := inferSchemaFromPath(dbDir, path)
		for i := range raws {
			if dirSchema != "" && raws[i].Kind == pipeline.KindSchema {
				diags = append(diags, pipeline.Errorf(raws[i].Pos,
					"SCHEMA blocks are not allowed inside the schemas/ directory hierarchy; "+
						"the schema is inferred from the directory name"))
				continue
			}
			if raws[i].Schema == "" {
				raws[i].Schema = dirSchema
			}
		}

		if dirSchema != "" {
			if _, seen := dirSchemas[dirSchema]; !seen {
				pos := pipeline.SourcePos{File: path, Line: 1, Col: 1}
				dirSchemas[dirSchema] = pos
			}
		}

		rawObjects = append(rawObjects, raws...)
	}

	// Inject one synthetic SCHEMA declaration per directory-inferred schema.
	// This ensures schemas that exist only as directories appear in the desired
	// state, so the differ never generates a spurious DROP SCHEMA.
	// The synthetic raw object goes through the normal pipeline (Reconstruct →
	// "CREATE SCHEMA <name>" → pg_query → IR builder); the merger deduplicates
	// it with any explicit SCHEMA block for the same name.
	// "public" is skipped: it always exists in PostgreSQL and is never managed.
	for name, pos := range dirSchemas {
		if name == "public" {
			continue
		}
		rawObjects = append(rawObjects, pipeline.RawObject{
			Kind:  pipeline.KindSchema,
			Part1: name,
			Pos:   pos,
		})
	}

	if diags.HasErrors() {
		return nil, diags
	}

	// Stages 2–3: Parse Part1 (PG SQL) + Part2 ({ } block) and build IR.
	var irObjects []pipeline.IRObject
	for _, raw := range rawObjects {
		pgResult, pgErr := pgParser.Parse(raw.Kind, raw.Part1, raw.Pos)
		pgResult.SchemaContext = raw.Schema
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

// inferSchemaFromPath returns the schema name inferred from the file's position
// under dbDir/schemas/<name>/..., or "" if the file is not in that structure.
func inferSchemaFromPath(dbDir, filePath string) string {
	if dbDir == "" {
		return ""
	}
	rel, err := filepath.Rel(dbDir, filePath)
	if err != nil {
		return ""
	}
	// Use forward-slash segments on all platforms.
	parts := strings.SplitN(filepath.ToSlash(rel), "/", 4)
	if len(parts) >= 3 && parts[0] == "schemas" && parts[1] != "" {
		return parts[1]
	}
	return ""
}
