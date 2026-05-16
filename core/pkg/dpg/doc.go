// Package dpg is the stable public API for the DPG (Declarative PG) compiler.
//
// It exposes the types and functions that external tools — language servers,
// editor plugins, CI integrations, custom linters — need to compile .dpg source
// files, run the built-in linter, diff against a snapshot, and discover project
// structure, without importing internal packages directly.
//
// # Minimal usage
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
//
// # Extension points
//
// Register a custom linter that augments the built-in rules:
//
//	type myLinter struct{}
//
//	func (l *myLinter) Lint(objects []dpg.IRObject, cfg dpg.LinterConfig) ([]dpg.LintDiagnostic, error) {
//	    var diags []dpg.LintDiagnostic
//	    for _, obj := range objects {
//	        if t, ok := obj.(*dpg.Table); ok && t.Comment == nil {
//	            diags = append(diags, dpg.LintDiagnostic{
//	                Pos:     t.SrcPos,
//	                Rule:    "require-table-comment",
//	                Message: t.QualifiedName() + " has no COMMENT",
//	            })
//	        }
//	    }
//	    return diags, nil
//	}
//
//	// Chain with the built-in linter so both sets of rules run.
//	builtin, _ := dpg.ResolveLinter(dpg.Default)
//	chained := dpg.NewChainLinter(builtin, &myLinter{})
//
//	// Or replace the built-in linter entirely.
//	dpg.Default.Register(dpg.KeyLinter, &myLinter{})
//
// The same Register pattern works for Differ, Emitter, and SecretResolver.
// See examples/plugin/ for a runnable example.
package dpg
