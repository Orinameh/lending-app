package repositories

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type RefreshToken struct {
	JTI       string
	UserID    uuid.UUID
	ExpiresAt time.Time
	RevokedAt *time.Time
	ReplacedBy *string
}

type RefreshTokenRepository struct {
	db *sql.DB
}

func NewRefreshTokenRepository(db *sql.DB) *RefreshTokenRepository {
	return &RefreshTokenRepository{db: db}
}

func (r *RefreshTokenRepository) Create(ctx context.Context, jti string, userID uuid.UUID, expires time.Time) error {
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`INSERT INTO refresh_tokens (jti, user_id, expires_at) VALUES ($1,$2,$3)`,
		jti, userID, expires)
	return err
}

func (r *RefreshTokenRepository) Get(ctx context.Context, jti string) (*RefreshToken, error) {
	var t RefreshToken
	var revoked sql.NullTime
	var replaced sql.NullString
	err := runner(ctx, r.db).QueryRowContext(ctx,
		`SELECT jti, user_id, expires_at, revoked_at, replaced_by FROM refresh_tokens WHERE jti=$1`,
		jti).Scan(&t.JTI, &t.UserID, &t.ExpiresAt, &revoked, &replaced)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if revoked.Valid {
		t.RevokedAt = &revoked.Time
	}
	if replaced.Valid {
		t.ReplacedBy = &replaced.String
	}
	return &t, nil
}

// Rotate marks jti revoked and links it to its replacement.
func (r *RefreshTokenRepository) Rotate(ctx context.Context, oldJTI, newJTI string) error {
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE refresh_tokens SET revoked_at=NOW(), replaced_by=$2 WHERE jti=$1 AND revoked_at IS NULL`,
		oldJTI, newJTI)
	return err
}

// RevokeAllUser revokes every active refresh token for a user (theft response / logout-all).
func (r *RefreshTokenRepository) RevokeAllUser(ctx context.Context, userID uuid.UUID) error {
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE refresh_tokens SET revoked_at=NOW() WHERE user_id=$1 AND revoked_at IS NULL`,
		userID)
	return err
}

// PurgeExpired removes expired rows (housekeeping; audit_logs remain append-only).
func (r *RefreshTokenRepository) PurgeExpired(ctx context.Context, before time.Time) (int64, error) {
	res, err := runner(ctx, r.db).ExecContext(ctx,
		`DELETE FROM refresh_tokens WHERE expires_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
