package repositories

import (
	"context"
	"database/sql"
)

// txKey is the private context key used to carry an *sql.Tx.
type txKey struct{}

// WithTx returns a context that carries the given transaction. Any repository
// call made with this context will execute against the tx instead of the DB
// pool, enabling atomic multi-step operations.
func WithTx(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// txFromCtx extracts an *sql.Tx from ctx, if present.
func txFromCtx(ctx context.Context) (*sql.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(*sql.Tx)
	return tx, ok
}

// execer is the minimal interface satisfied by both *sql.DB and *sql.Tx.
// Every repository method that must be tx-aware should go through this.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// runner returns the tx bound to ctx if one exists, else the DB pool.
// It's a free function so every repository can share it without embedding.
func runner(ctx context.Context, db *sql.DB) execer {
	if tx, ok := txFromCtx(ctx); ok {
		return tx
	}
	return db
}
