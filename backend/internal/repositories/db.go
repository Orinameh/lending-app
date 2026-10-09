package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"lending-app/backend/pkg/retry"
)

const (
	// DefaultQueryTimeout bounds every repository query so a slow DB can
	// never wedge an HTTP worker forever. Callers with tighter budgets
	// derive a shorter context; this is the ceiling.
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

// Transact runs fn inside a SERIALIZABLE transaction with automatic retry on
// serialization/deadlock failures (40001/40P01). The caller's ctx carries the
// overall deadline; each attempt gets a fresh tx.
//
// ACID: all multi-write operations (loan apply, disburse, repayment,
// state transitions) MUST go through this — never BeginTx directly.
func Transact(ctx context.Context, db *sql.DB, fn func(txCtx context.Context) error) error {
	opt := retry.DefaultOptions()
	opt.MaxAttempts = 5
	return retry.Do(ctx, opt, func(ctx context.Context) error {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return err
		}
		defer tx.Rollback() //nolint:errcheck — no-op after Commit
		if err := fn(WithTx(ctx, tx)); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err // retriable 40001/deadlock handled by retry.Do
		}
		return nil
	})
}

// Advisory lock keys (int64 namespace — must be unique per job).
const (
	LockMigrations   int64 = 0x1e4d1_0001
	LockOverdueScan  int64 = 0x1e4d1_0002
	LockCreditGen    int64 = 0x1e4d1_0003
)

// TryAdvisoryLock takes a session-level pg advisory lock without blocking.
// Returns (true, unlock, nil) on success; (false, nil, nil) if another
// replica/holder owns it (caller should skip, not queue).
func TryAdvisoryLock(ctx context.Context, db *sql.DB, key int64) (bool, func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("advisory lock conn: %w", err)
	}
	var got bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		_ = conn.Close()
		return false, nil, fmt.Errorf("advisory lock: %w", err)
	}
	if !got {
		_ = conn.Close()
		return false, nil, nil
	}
	unlock := func() {
		defer conn.Close() //nolint:errcheck
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, key)
	}
	return true, unlock, nil
}

// AdvisoryXactLock takes a TRANSACTION-scoped lock inside an existing tx.
// Ideal for serializing hot rows (same-loan concurrent repays) without
// holding pool connections: SELECT pg_advisory_xact_lock($1).
func AdvisoryXactLock(ctx context.Context, tx *sql.Tx, key int64) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, key)
	return err
}

// LockTx extracts the tx bound to ctx (if any) and takes a transaction-scoped
// advisory lock on key. No-op when ctx carries no tx.
func LockTx(ctx context.Context, key int64) error {
	tx, ok := txFromCtx(ctx)
	if !ok {
		return nil
	}
	return AdvisoryXactLock(ctx, tx, key)
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
