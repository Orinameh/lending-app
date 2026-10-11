package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lending-app/backend/pkg/retry"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is implemented by *pgxpool.Pool and pgx.Tx, so every repository
// method runs unchanged inside or outside a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// txKey is the private context key carrying a pgx.Tx.
type txKey struct{}

// WithTx returns a context that carries the given transaction. Any
// repository call made with this context executes against the tx instead of
// the pool, enabling atomic multi-step operations.
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// txFromCtx extracts a pgx.Tx from ctx, if present.
func txFromCtx(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}

// querier returns the tx bound to ctx if one exists, else the pool.
// It's a free function so every repository can share it without embedding.
func querier(ctx context.Context, pool *pgxpool.Pool) Querier {
	if tx, ok := txFromCtx(ctx); ok {
		return tx
	}
	return pool
}

// Connect returns a pgxpool tuned for payment workloads.
// Pool sizing: MaxConns should be modest. Postgres context-switching
// hurts more than helps beyond (CPU cores × 2) + effective_spindles.
// For a 4-core API replica, 20 is appropriate.
func Connect(ctx context.Context, dsn string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

// WithTx runs fn inside a transaction with automatic commit/rollback.
// Read Committed is sufficient because correctness depends on explicit
// row locks (SELECT ... FOR UPDATE, advisory xact locks), not on snapshot
// isolation.
func WithTxRunner(ctx context.Context, pool *pgxpool.Pool, fn func(Querier) error) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Transact runs fn inside a ReadCommitted transaction bound to a derived
// context, with automatic retry on deadlocks/serialization failures.
// ACID: all multi-write operations (loan apply, disburse, repayment)
// MUST go through this — never pool.BeginTx directly.
func Transact(ctx context.Context, pool *pgxpool.Pool, fn func(txCtx context.Context) error) error {
	opt := retry.DefaultOptions()
	opt.MaxAttempts = 5
	return retry.Do(ctx, opt, func(ctx context.Context) error {
		return WithTxRunner(ctx, pool, func(q Querier) error {
			tx, ok := q.(pgx.Tx)
			if !ok {
				return fmt.Errorf("db: transact without tx")
			}
			return fn(WithTx(ctx, tx))
		})
	})
}

// IsCheckViolation reports whether err is a PostgreSQL CHECK constraint
// violation (SQLSTATE 23514). Used to translate the database's
// last-line-of-defense rejection into a domain error.
func IsCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

// IsUniqueViolation reports whether err is a unique constraint violation
// (SQLSTATE 23505). Used to translate races (idempotency keys, HMAC blind
// indexes) into idempotent/conflict domain behavior instead of 500s.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

const (
	// DefaultQueryTimeout bounds repository work so a slow DB can never
	// wedge an HTTP worker forever.
	DefaultQueryTimeout = 8 * time.Second
	// TxTimeout bounds whole transactions (repayments, disbursement).
	TxTimeout = 20 * time.Second
)

// WithTimeout derives a ceiling-bounded context for repository work.
func WithTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, DefaultQueryTimeout)
}

// WithTxTimeout derives the transaction ceiling.
func WithTxTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, TxTimeout)
}

// Advisory lock keys (int64 namespace — must be unique per job).
const (
	LockMigrations  int64 = 0x1e4d1_0001
	LockOverdueScan int64 = 0x1e4d1_0002
	LockCreditGen   int64 = 0x1e4d1_0003
	LockRekey       int64 = 0x1e4d1_0004
)

// TryAdvisoryLock takes a session-level pg advisory lock without blocking.
// Holds a pooled connection until unlock is called — keep the critical
// section short. Returns (true, unlock, nil) on success; (false, nil, nil)
// if another replica/holder owns it (caller should skip, not queue).
func TryAdvisoryLock(ctx context.Context, pool *pgxpool.Pool, key int64) (bool, func(), error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("advisory lock acquire: %w", err)
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		conn.Release()
		return false, nil, fmt.Errorf("advisory lock: %w", err)
	}
	if !got {
		conn.Release()
		return false, nil, nil
	}
	unlock := func() {
		defer conn.Release()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, key)
	}
	return true, unlock, nil
}

// LockTx takes a transaction-scoped advisory lock using the tx bound to
// ctx (no-op when ctx carries no tx). Xact locks release automatically on
// commit/rollback — impossible to leak. Ideal for serializing hot rows
// (same-loan concurrent repays) without holding pool connections.
func LockTx(ctx context.Context, key int64) error {
	tx, ok := txFromCtx(ctx)
	if !ok {
		return nil
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key)
	return err
}

// LoanLockKey derives a deterministic advisory key per loan UUID for
// xact-scoped serialization of concurrent repay/disburse on the same loan.
func LoanLockKey(id [16]byte) int64 {
	var v int64
	for i := 0; i < 8; i++ {
		v = v<<8 | int64(id[i])
	}
	return v
}
