// Package dpg is the stable public API for the DPG (Declarative PG) compiler.
//
// It exposes the types and functions that external tools — language servers,
// editor plugins, CI integrations — need to compile .dpg source files, run
// the built-in linter, and discover project structure, without importing
// internal packages directly.
//
// Minimal usage:
//
//	proj, err := dpg.Discover(".")
//	if err != nil { ... }
//
//	db := proj.Clusters[0].Databases[0]
//	objects, err := dpg.Compile(db.SourceFiles, db.Dir)
//	if err != nil { ... }
//
//	diags, err := dpg.Lint(objects, dpg.LinterConfig{WarnOnDeprecated: true})
//	for _, d := range diags { fmt.Println(d.Pos, d.Message) }
package dpg
