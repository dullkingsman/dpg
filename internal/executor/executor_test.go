package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

// ── mock Conn / Tx ────────────────────────────────────────────────────────────

type mockTx struct {
	executed  []string
	commitErr error
	rollback  bool
}

func (t *mockTx) Exec(_ context.Context, sql string, _ ...any) (int64, error) {
	t.executed = append(t.executed, sql)
	return 0, nil
}
func (t *mockTx) Commit(_ context.Context) error   { return t.commitErr }
func (t *mockTx) Rollback(_ context.Context) error { t.rollback = true; return nil }

type mockConn struct {
	txn       *mockTx
	nonTxnSQL []string
	beginErr  error
}

func (c *mockConn) Exec(_ context.Context, sql string, _ ...any) (int64, error) {
	c.nonTxnSQL = append(c.nonTxnSQL, sql)
	return 0, nil
}
func (c *mockConn) Begin(_ context.Context) (pipeline.Tx, error) {
	if c.beginErr != nil {
		return nil, c.beginErr
	}
	c.txn = &mockTx{}
	return c.txn, nil
}
func (c *mockConn) Close(_ context.Context) error { return nil }

// ── test ops ──────────────────────────────────────────────────────────────────

type testOp struct {
	sql string
	txn bool
}

func (o testOp) SQL() string             { return o.sql }
func (o testOp) Safety() pipeline.Safety { return pipeline.Safe }
func (o testOp) Transactional() bool     { return o.txn }
func (o testOp) Pos() pipeline.SourcePos { return pipeline.SourcePos{} }

// ── tests ─────────────────────────────────────────────────────────────────────

func TestApplyTransactional(t *testing.T) {
	conn := &mockConn{}
	e := New()
	m := pipeline.Migration{
		Transactional: []pipeline.DiffOp{testOp{"CREATE TABLE t (id int);", true}},
	}
	if err := e.Apply(context.Background(), m, conn); err != nil {
		t.Fatal(err)
	}
	if conn.txn == nil {
		t.Fatal("expected transaction to be started")
	}
	if len(conn.txn.executed) != 1 || conn.txn.executed[0] != "CREATE TABLE t (id int);" {
		t.Errorf("unexpected executed SQL: %v", conn.txn.executed)
	}
	if len(conn.nonTxnSQL) != 0 {
		t.Errorf("transactional op leaked into non-txn path: %v", conn.nonTxnSQL)
	}
}

func TestApplyNonTransactional(t *testing.T) {
	conn := &mockConn{}
	e := New()
	m := pipeline.Migration{
		NonTransactional: []pipeline.DiffOp{testOp{"ALTER TYPE status ADD VALUE 'archived';", false}},
	}
	if err := e.Apply(context.Background(), m, conn); err != nil {
		t.Fatal(err)
	}
	if conn.txn != nil {
		t.Fatal("expected no transaction for non-transactional ops")
	}
	if len(conn.nonTxnSQL) != 1 {
		t.Fatalf("expected 1 non-txn SQL, got %d", len(conn.nonTxnSQL))
	}
}

func TestApplyEmpty(t *testing.T) {
	conn := &mockConn{}
	e := New()
	if err := e.Apply(context.Background(), pipeline.Migration{}, conn); err != nil {
		t.Fatal(err)
	}
	if conn.txn != nil {
		t.Fatal("expected no transaction for empty migration")
	}
}

func TestApplyBeginError(t *testing.T) {
	conn := &mockConn{beginErr: errors.New("connection refused")}
	e := New()
	m := pipeline.Migration{
		Transactional: []pipeline.DiffOp{testOp{"CREATE TABLE t (id int);", true}},
	}
	if err := e.Apply(context.Background(), m, conn); err == nil {
		t.Fatal("expected error from Begin failure")
	}
}

func TestApplyRollbackOnExecError(t *testing.T) {
	conn := &mockConn{}
	// Use real Begin but have a tx that errors on exec.
	failTx := &mockTx{}
	conn.txn = failTx

	// Wrap a conn that uses our pre-built failing tx.
	type failConn struct{ mockConn }
	fc := &failConn{}
	fc.txn = &mockTx{}

	// Use a conn that returns an exec-failing tx.
	errConn := &errExecConn{}
	e := New()
	m := pipeline.Migration{
		Transactional: []pipeline.DiffOp{testOp{"BAD SQL", true}},
	}
	if err := e.Apply(context.Background(), m, errConn); err == nil {
		t.Fatal("expected error from exec failure")
	}
	if !errConn.txn.rollback {
		t.Error("expected rollback to be called on exec failure")
	}
}

type errExecConn struct {
	txn *errExecTx
}

func (c *errExecConn) Exec(_ context.Context, sql string, _ ...any) (int64, error) { return 0, nil }
func (c *errExecConn) Begin(_ context.Context) (pipeline.Tx, error) {
	c.txn = &errExecTx{}
	return c.txn, nil
}
func (c *errExecConn) Close(_ context.Context) error { return nil }

type errExecTx struct{ rollback bool }

func (t *errExecTx) Exec(_ context.Context, _ string, _ ...any) (int64, error) {
	return 0, errors.New("syntax error")
}
func (t *errExecTx) Commit(_ context.Context) error   { return nil }
func (t *errExecTx) Rollback(_ context.Context) error { t.rollback = true; return nil }
