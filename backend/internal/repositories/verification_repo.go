package repositories

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrAlreadyConsumed reports a single-use secret lost a consume race:
// another request used it first. Callers map it to expired/invalid.
var ErrAlreadyConsumed = errors.New("already consumed")

// VerificationRepository stores single-use email tokens and phone OTPs.
// Only hashes are persisted; raw tokens/codes live in transit only.
type VerificationRepository struct {
	db *sql.DB
}

func NewVerificationRepository(db *sql.DB) *VerificationRepository {
	return &VerificationRepository{db: db}
}

type EmailVerification struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

func (r *VerificationRepository) CreateEmailToken(ctx context.Context, id, userID uuid.UUID, tokenHash string, expires time.Time) error {
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`INSERT INTO email_verifications (id, user_id, token_hash, expires_at) VALUES ($1,$2,$3,$4)`,
		id, userID, tokenHash, expires)
	return err
}

func (r *VerificationRepository) GetValidEmailToken(ctx context.Context, tokenHash string, now time.Time) (*EmailVerification, error) {
	var v EmailVerification
	var used sql.NullTime
	err := runner(ctx, r.db).QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, expires_at, used_at FROM email_verifications
         WHERE token_hash=$1 AND expires_at > $2 AND used_at IS NULL`,
		tokenHash, now).Scan(&v.ID, &v.UserID, &v.TokenHash, &v.ExpiresAt, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if used.Valid {
		v.UsedAt = &used.Time
	}
	return &v, nil
}

func (r *VerificationRepository) MarkEmailUsed(ctx context.Context, id uuid.UUID) error {
	// Atomic consume: exactly one concurrent request wins; the loser gets
	// ErrAlreadyConsumed instead of double-spending the token.
	res, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE email_verifications SET used_at=NOW() WHERE id=$1 AND used_at IS NULL`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrAlreadyConsumed
	}
	return nil
}

type PhoneOTP struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	CodeHash    string
	ExpiresAt   time.Time
	Attempts    int
	MaxAttempts int
	UsedAt      *time.Time
}

func (r *VerificationRepository) CreatePhoneOTP(ctx context.Context, id, userID uuid.UUID, codeHash string, expires time.Time) error {
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`INSERT INTO phone_otps (id, user_id, code_hash, expires_at) VALUES ($1,$2,$3,$4)`,
		id, userID, codeHash, expires)
	return err
}

// LatestValidOTP returns the newest unused, unexpired OTP for the user.
func (r *VerificationRepository) LatestValidOTP(ctx context.Context, userID uuid.UUID, now time.Time) (*PhoneOTP, error) {
	var o PhoneOTP
	var used sql.NullTime
	err := runner(ctx, r.db).QueryRowContext(ctx,
		`SELECT id, user_id, code_hash, expires_at, attempts, max_attempts, used_at
         FROM phone_otps WHERE user_id=$1 AND expires_at > $2 AND used_at IS NULL
         ORDER BY created_at DESC LIMIT 1`,
		userID, now).Scan(&o.ID, &o.UserID, &o.CodeHash, &o.ExpiresAt, &o.Attempts, &o.MaxAttempts, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if used.Valid {
		o.UsedAt = &used.Time
	}
	return &o, nil
}

func (r *VerificationRepository) BumpOTPAttempts(ctx context.Context, id uuid.UUID) error {
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE phone_otps SET attempts = attempts + 1 WHERE id=$1`, id)
	return err
}

func (r *VerificationRepository) MarkOTPUsed(ctx context.Context, id uuid.UUID) error {
	res, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE phone_otps SET used_at=NOW() WHERE id=$1 AND used_at IS NULL`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrAlreadyConsumed
	}
	return nil
}

// RecentOTPCount bounds resend abuse (SMS costs money): how many OTPs were
// issued to this user since `since`.
func (r *VerificationRepository) RecentOTPCount(ctx context.Context, userID uuid.UUID, since time.Time) (int, error) {
	var n int
	err := runner(ctx, r.db).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM phone_otps WHERE user_id=$1 AND created_at > $2`,
		userID, since).Scan(&n)
	return n, err
}
