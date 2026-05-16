// Package executor implements pipeline.ApplyExecutor using pgx.
package executor

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	pipeline.Default.Register(pipeline.KeyApplyExecutor, New())
}

// PgxExecutor implements pipeline.ApplyExecutor over a pgx connection.
type PgxExecutor struct{}

// New returns a PgxExecutor.
func New() *PgxExecutor { return &PgxExecutor{} }

// Apply executes a Migration. Transactional ops run inside BEGIN/COMMIT;
// non-transactional ops run individually outside any transaction.
func (e *PgxExecutor) Apply(ctx context.Context, m pipeline.Migration, conn pipeline.Conn) error {
	// Transactional block.
	if len(m.Transactional) > 0 {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("executor: begin transaction: %w", err)
		}
		for _, op := range m.Transactional {
			if _, err := tx.Exec(ctx, op.SQL()); err != nil {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("executor: %s: %w", op.SQL(), err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("executor: commit: %w", err)
		}
	}

	// Non-transactional steps (e.g. CREATE INDEX CONCURRENTLY).
	for _, op := range m.NonTransactional {
		if _, err := conn.Exec(ctx, op.SQL()); err != nil {
			return fmt.Errorf("executor: %s: %w", op.SQL(), err)
		}
	}
	return nil
}

var _ pipeline.ApplyExecutor = (*PgxExecutor)(nil)

// ── pgxConn wraps *pgx.Conn to satisfy pipeline.Conn ─────────────────────────

// PgxConn wraps a *pgx.Conn to implement pipeline.Conn.
type PgxConn struct {
	conn *pgx.Conn
}

// Connect opens a pgx connection to connStr and returns a PgxConn.
func Connect(ctx context.Context, connStr string) (*PgxConn, error) {
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		return nil, fmt.Errorf("executor: connect: %w", err)
	}
	return &PgxConn{conn: conn}, nil
}

func (c *PgxConn) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	ct, err := c.conn.Exec(ctx, sql, args...)
	return ct.RowsAffected(), err
}

func (c *PgxConn) Begin(ctx context.Context) (pipeline.Tx, error) {
	tx, err := c.conn.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &pgxTx{tx: tx}, nil
}

func (c *PgxConn) Close(ctx context.Context) error {
	return c.conn.Close(ctx)
}

// QueryRows implements pipeline.Querier. It returns rows from a query.
func (c *PgxConn) QueryRows(ctx context.Context, sql string, args ...any) (pipeline.Rows, error) {
	rows, err := c.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRows{rows: rows}, nil
}

// pgxRows wraps pgx.Rows to implement pipeline.Rows.
type pgxRows struct {
	rows interface {
		Next() bool
		Scan(dest ...any) error
		Err() error
		Close()
	}
}

func (r *pgxRows) Next() bool          { return r.rows.Next() }
func (r *pgxRows) Scan(d ...any) error { return r.rows.Scan(d...) }
func (r *pgxRows) Err() error          { return r.rows.Err() }
func (r *pgxRows) Close()              { r.rows.Close() }

var _ pipeline.Querier = (*PgxConn)(nil)

// pgxTx wraps pgx.Tx to implement pipeline.Tx.
type pgxTx struct {
	tx pgx.Tx
}

func (t *pgxTx) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	ct, err := t.tx.Exec(ctx, sql, args...)
	return ct.RowsAffected(), err
}

func (t *pgxTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t *pgxTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }

var _ pipeline.Conn = (*PgxConn)(nil)
var _ pipeline.Tx = (*pgxTx)(nil)
