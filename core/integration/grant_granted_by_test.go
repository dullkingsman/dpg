//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dullkingsman/dpg/internal/compiler"
	"github.com/dullkingsman/dpg/internal/diff"
	"github.com/dullkingsman/dpg/internal/emit"
	"github.com/dullkingsman/dpg/internal/executor"
	"github.com/dullkingsman/dpg/internal/pipeline"
	"github.com/dullkingsman/dpg/internal/testpg"
)

// TestRoundtripGrantGrantedBy guards RFC audit item #90 (Section 7.10/11.3):
// GRANTED BY role-spec on a table-level GRANTS/REVOCATIONS entry. Real
// PostgreSQL restricts the effective grantor to current_user regardless of
// who is named (confirmed live via a direct psql probe before writing this
// test: even a superuser gets "grantor must be current user" naming any
// role other than the one it's actually connected as) — so CURRENT_USER is
// the only role-spec value guaranteed to apply successfully in any
// environment, and is what this test exercises. Proves: (a) a declared
// GRANT ... GRANTED BY CURRENT_USER actually applies without error and the
// live ACL entry's grantor is the connecting role, (b) a second plan
// against the same declaration is a genuine no-op, and (c) an explicit
// REVOCATIONS entry with GRANTED BY CURRENT_USER actually revokes it live.
func TestRoundtripGrantGrantedBy(t *testing.T) {
	connStr := testpg.Start(t)
	ctx := context.Background()

	differ := diff.New()
	emitter := emit.New()
	applyExec := executor.New()
	store := newMemStore()

	conn, err := executor.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	dir := t.TempDir()
	f := filepath.Join(dir, "schema.dpg")

	grantorOf := func(grantee string) (string, bool) {
		t.Helper()
		rows, err := conn.QueryRows(ctx,
			`SELECT acl.grantor::regrole::text FROM pg_class c, pg_namespace n,
			 LATERAL aclexplode(c.relacl) acl
			 WHERE c.relnamespace = n.oid AND n.nspname = 'public' AND c.relname = 'orders'
			 AND acl.grantee = $1::regrole AND acl.privilege_type = 'SELECT'`, grantee)
		if err != nil {
			t.Fatalf("query grantor: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			return "", false
		}
		var v string
		_ = rows.Scan(&v)
		return v, true
	}

	// v1: declare the grant with GRANTED BY CURRENT_USER.
	v1 := `ROLE reader NOLOGIN;

TABLE orders (id INT) {
    GRANTS { SELECT TO reader GRANTED BY CURRENT_USER; }
}`
	if err := os.WriteFile(f, []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	connectingRole, err := conn.QueryRows(ctx, `SELECT current_user`)
	if err != nil {
		t.Fatalf("query current_user: %v", err)
	}
	var connRole string
	if !connectingRole.Next() {
		t.Fatal("current_user returned no row")
	}
	_ = connectingRole.Scan(&connRole)
	connectingRole.Close()

	grantor, ok := grantorOf("reader")
	if !ok {
		t.Fatal("reader has no live SELECT grant on orders after initial apply — GRANT never applied")
	}
	if grantor != connRole {
		t.Fatalf("grantor: got %q, want %q (the connecting role) — GRANTED BY CURRENT_USER did not resolve correctly", grantor, connRole)
	}

	// The offline dpg plan path (desired vs. the stored snapshot of last-
	// declared state, not live introspection) must already be a no-op for
	// the unchanged declaration. Deliberately not checking live-
	// introspection drift here (e.g. via assertNoLiveDrift): doing so
	// independently surfaced a real, pre-existing, unrelated gap —
	// introspectTableGrants has no grantor<>grantee filter (unlike
	// introspectColumnGrants), so the object owner's own privileges, which
	// PostgreSQL materializes into a real aclitem row the moment any
	// explicit GRANT touches the table (confirmed live via direct psql),
	// get read back as an "extra" live grant and diffGrantSet's live-
	// comparison path proposes revoking it — contradicting RFC Section
	// 11.2's own explicit "does NOT report extra grants present in the
	// live catalog but absent from DPG source" text. Flagged separately;
	// out of scope for GRANTED BY itself.
	desired, _, err := compiler.Compile([]string{f}, dir, pipeline.Default)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	base, _ := store.Load("test", "dpgtest")
	replanOps, err := differ.Diff(desired, base)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(replanOps) != 0 {
		t.Errorf("expected no ops replanning the unchanged declaration, got: %v", replanOps)
	}

	// v2: an explicit REVOCATIONS entry with GRANTED BY CURRENT_USER must
	// actually revoke it live.
	v2 := `ROLE reader NOLOGIN;

TABLE orders (id INT) {
    REVOCATIONS { SELECT FROM reader GRANTED BY CURRENT_USER; }
}`
	if err := os.WriteFile(f, []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	applyFixture(t, ctx, conn, []string{f}, dir, differ, emitter, applyExec, store)

	if _, ok := grantorOf("reader"); ok {
		t.Fatal("reader still has a live SELECT grant on orders after an explicit REVOCATIONS entry with GRANTED BY — REVOKE never applied")
	}
}
