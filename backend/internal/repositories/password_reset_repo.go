package repositories

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PasswordReset struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

type PasswordResetRepository struct {
	db *pgxpool.Pool
}

func NewPasswordResetRepository(db *pgxpool.Pool) *PasswordResetRepository {
	return &PasswordResetRepository{db: db}
}

func (r *PasswordResetRepository) Create(ctx context.Context, id, userID uuid.UUID, tokenHash string, expires time.Time) error {
	_, err := querier(ctx, r.db).Exec(ctx,
		`INSERT INTO password_resets (id, user_id, token_hash, expires_at) VALUES ($1,$2,$3,$4)`,
		id, userID, tokenHash, expires)
	return err
}

func (r *PasswordResetRepository) GetValid(ctx context.Context, tokenHash string, now time.Time) (*PasswordReset, error) {
	var p PasswordReset
	var used sql.NullTime
	err := querier(ctx, r.db).QueryRow(ctx,
		`SELECT id, user_id, token_hash, expires_at, used_at FROM password_resets
         WHERE token_hash=$1 AND expires_at > $2 AND used_at IS NULL`,
		tokenHash, now).Scan(&p.ID, &p.UserID, &p.TokenHash, &p.ExpiresAt, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if used.Valid {
		p.UsedAt = &used.Time
	}
	return &p, nil
}

func (r *PasswordResetRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	_, err := querier(ctx, r.db).Exec(ctx,
		`UPDATE password_resets SET used_at=NOW() WHERE id=$1 AND used_at IS NULL`, id)
	return err
}
