package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

func TestDistinctOwnerRoles(t *testing.T) {
	ops := []pipeline.DiffOp{
		testOp{sql: `SET ROLE "app_admin";`},
		testOp{sql: `CREATE TABLE "public"."widgets" (id bigint);`},
		testOp{sql: `RESET ROLE;`},
		testOp{sql: `ALTER SEQUENCE "public"."seq_id" OWNER TO "app_admin";`},
		testOp{sql: `ALTER TABLE "public"."other" OWNER TO "report""er";`},
		testOp{sql: `COMMENT ON TABLE "public"."widgets" IS 'x';`},
	}
	got := DistinctOwnerRoles(ops)
	want := []string{"app_admin", `report"er`}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestDistinctOwnerRolesEmpty(t *testing.T) {
	ops := []pipeline.DiffOp{testOp{sql: `CREATE TABLE "public"."widgets" (id bigint);`}}
	if got := DistinctOwnerRoles(ops); len(got) != 0 {
		t.Fatalf("expected no roles, got %v", got)
	}
}

// mockQuerier implements pipeline.Querier for ValidateOwnerMembership tests.
// members maps role name -> pg_has_role result.
type mockQuerier struct {
	members map[string]bool
	queried []string
}

func (m *mockQuerier) Exec(context.Context, string, ...any) (int64, error) { return 0, nil }
func (m *mockQuerier) Begin(context.Context) (pipeline.Tx, error)          { return nil, nil }
func (m *mockQuerier) Close(context.Context) error                        { return nil }
func (m *mockQuerier) QueryRows(_ context.Context, sql string, args ...any) (pipeline.Rows, error) {
	role, _ := args[0].(string)
	m.queried = append(m.queried, role)
	return &mockRoleRows{member: m.members[role]}, nil
}

// mockRoleRows yields exactly one boolean row, mirroring
// "SELECT pg_has_role(current_user, $1, 'MEMBER')".
type mockRoleRows struct {
	member bool
	done   bool
}

func (r *mockRoleRows) Next() bool {
	if r.done {
		return false
	}
	r.done = true
	return true
}
func (r *mockRoleRows) Scan(dest ...any) error {
	*dest[0].(*bool) = r.member
	return nil
}
func (r *mockRoleRows) Err() error { return nil }
func (r *mockRoleRows) Close()     {}

func TestValidateOwnerMembership_AllMembers(t *testing.T) {
	q := &mockQuerier{members: map[string]bool{"app_admin": true}}
	ops := []pipeline.DiffOp{
		testOp{sql: `SET ROLE "app_admin";`},
		testOp{sql: `CREATE TABLE "public"."widgets" (id bigint);`},
		testOp{sql: `RESET ROLE;`},
	}
	if err := ValidateOwnerMembership(context.Background(), q, ops); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOwnerMembership_MissingMembership(t *testing.T) {
	q := &mockQuerier{members: map[string]bool{"app_admin": false, "report_role": true}}
	ops := []pipeline.DiffOp{
		testOp{sql: `SET ROLE "app_admin";`},
		testOp{sql: `CREATE TABLE "public"."widgets" (id bigint);`},
		testOp{sql: `RESET ROLE;`},
		testOp{sql: `ALTER SEQUENCE "public"."seq_id" OWNER TO "report_role";`},
	}
	err := ValidateOwnerMembership(context.Background(), q, ops)
	if err == nil {
		t.Fatal("expected an error for missing membership, got nil")
	}
	if !strings.Contains(err.Error(), "DPG-E036") || !strings.Contains(err.Error(), "app_admin") {
		t.Errorf("expected DPG-E036 naming app_admin, got: %v", err)
	}
	if strings.Contains(err.Error(), "report_role") {
		t.Errorf("expected report_role (a real member) NOT to be listed as missing, got: %v", err)
	}
}

func TestValidateOwnerMembership_NoOwners(t *testing.T) {
	q := &mockQuerier{members: map[string]bool{}}
	ops := []pipeline.DiffOp{testOp{sql: `CREATE TABLE "public"."widgets" (id bigint);`}}
	if err := ValidateOwnerMembership(context.Background(), q, ops); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(q.queried) != 0 {
		t.Errorf("expected no pg_has_role queries when no OWNER is declared, got: %v", q.queried)
	}
}
