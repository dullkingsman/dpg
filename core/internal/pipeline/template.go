package pipeline

import (
	"fmt"
	"regexp"
	"strings"
)

// templateRef matches a {{<secret-uri>}} placeholder. The inner content is
// restricted to "no braces" rather than any specific URI shape — validating
// the URI itself is SecretResolver's job (a clear "unrecognized scheme" or
// similar error), not the template scanner's.
var templateRef = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// ResolveTemplate scans s for {{<secret-uri>}} placeholders and replaces
// each with resolver.Resolve(<secret-uri>), leaving all other text
// untouched. Used to embed a secret reference inside an otherwise-literal
// string (e.g. a SUBSCRIPTION CONNECTION conninfo, or a future FDW/UserMapping
// option string) without requiring the whole value to be a reference.
//
// s with no {{...}} occurrences at all is returned unchanged, without
// calling resolver — a plain literal never touches the resolver. On the
// first placeholder that fails to resolve, ResolveTemplate returns an error
// naming it; no partially-substituted string is ever returned.
func ResolveTemplate(s string, resolver SecretResolver) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	var firstErr error
	result := templateRef.ReplaceAllStringFunc(s, func(match string) string {
		if firstErr != nil {
			return match
		}
		ref := templateRef.FindStringSubmatch(match)[1]
		val, err := resolver.Resolve(ref)
		if err != nil {
			firstErr = fmt.Errorf("resolving {{%s}}: %w", ref, err)
			return match
		}
		return val
	})
	if firstErr != nil {
		return "", firstErr
	}
	return result, nil
}
