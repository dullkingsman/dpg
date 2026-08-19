package executor

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

// ownerRoleRefRe matches the quoted role identifier in "SET ROLE "x";" (the
// creation-time impersonation wrapper, RFC §11.5) and "... OWNER TO "x";"
// (reassigning an existing object's owner) — both require the connecting
// role to be a member of the target role in real PostgreSQL, so both are
// pre-flight-checked identically.
var ownerRoleRefRe = regexp.MustCompile(`(?:SET ROLE|OWNER TO) ("(?:[^"]|"")+")`)

// unquoteIdent reverses quoteIdent's escaping (internal/diff) — a
// double-quoted SQL identifier like "foo""bar" back to the literal foo"bar.
func unquoteIdent(quoted string) string {
	inner := strings.TrimSuffix(strings.TrimPrefix(quoted, `"`), `"`)
	return strings.ReplaceAll(inner, `""`, `"`)
}

// DistinctOwnerRoles scans ops for every role referenced via SET ROLE or
// OWNER TO and returns the distinct role names, in first-seen order.
func DistinctOwnerRoles(ops []pipeline.DiffOp) []string {
	seen := map[string]bool{}
	var roles []string
	for _, op := range ops {
		for _, m := range ownerRoleRefRe.FindAllStringSubmatch(op.SQL(), -1) {
			role := unquoteIdent(m[1])
			if !seen[role] {
				seen[role] = true
				roles = append(roles, role)
			}
		}
	}
	return roles
}

// ValidateOwnerMembership implements RFC §11.5's pre-flight membership check:
// before any DDL in the migration executes, the connecting role must be a
// member of every role a SET ROLE/OWNER TO statement in ops will target.
// Checking this upfront turns a bare PostgreSQL "permission denied to set
// role" — surfacing mid-migration (and, for the transactional block,
// mid-transaction) on whichever object happens to hit it first — into one
// DPG-E036 error naming every missing membership before any statement runs.
func ValidateOwnerMembership(ctx context.Context, conn pipeline.Querier, ops []pipeline.DiffOp) error {
	roles := DistinctOwnerRoles(ops)
	if len(roles) == 0 {
		return nil
	}
	var missing []string
	for _, role := range roles {
		member, err := hasRole(ctx, conn, role)
		if err != nil {
			return fmt.Errorf("executor: checking role membership for %q: %w", role, err)
		}
		if !member {
			missing = append(missing, role)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("DPG-E036: connecting role is not a member of the declared OWNER role(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

func hasRole(ctx context.Context, conn pipeline.Querier, role string) (bool, error) {
	rows, err := conn.QueryRows(ctx, `SELECT pg_has_role(current_user, $1, 'MEMBER')`, role)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var member bool
	if rows.Next() {
		if err := rows.Scan(&member); err != nil {
			return false, err
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return member, nil
}
