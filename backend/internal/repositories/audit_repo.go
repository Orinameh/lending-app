package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"lending-app/backend/internal/domain/entities"

	"github.com/google/uuid"
)

type AuditRepository struct {
	db *sql.DB
}

func NewAuditRepository(db *sql.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

func (r *AuditRepository) Create(ctx context.Context, l *entities.AuditLog) error {
	var details []byte
	if l.Details != nil {
		details = []byte(l.Details)
	}
	const q = `
        INSERT INTO audit_logs (
            id, user_id, actor_id, action, resource_type, resource_id, details,
            ip_address, user_agent, request_id, created_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
    `
	_, err := runner(ctx, r.db).ExecContext(ctx, q,
		l.ID, l.UserID, l.ActorID, l.Action, l.ResourceType, l.ResourceID,
		details, l.IPAddress, l.UserAgent, nullIfEmpty(l.RequestID), l.CreatedAt)
	return err
}

func (r *AuditRepository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.AuditLog, error) {
	rows, err := runner(ctx, r.db).QueryContext(ctx, `
        SELECT id, user_id, actor_id, action, resource_type, resource_id, details,
               ip_address, user_agent, request_id, created_at
        FROM audit_logs WHERE user_id=$1
        ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanList(rows)
}

func (r *AuditRepository) List(ctx context.Context, action string, limit, offset int) ([]*entities.AuditLog, error) {
	var rows *sql.Rows
	var err error
	if action != "" {
		rows, err = runner(ctx, r.db).QueryContext(ctx, `
            SELECT id, user_id, actor_id, action, resource_type, resource_id, details,
                   ip_address, user_agent, request_id, created_at
            FROM audit_logs WHERE action=$1
            ORDER BY created_at DESC LIMIT $2 OFFSET $3`, action, limit, offset)
	} else {
		rows, err = runner(ctx, r.db).QueryContext(ctx, `
            SELECT id, user_id, actor_id, action, resource_type, resource_id, details,
                   ip_address, user_agent, request_id, created_at
            FROM audit_logs
            ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanList(rows)
}

func (r *AuditRepository) scanList(rows *sql.Rows) ([]*entities.AuditLog, error) {
	var out []*entities.AuditLog
	for rows.Next() {
		var l entities.AuditLog
		var userID, actorID, resourceID uuid.NullUUID
		var requestID sql.NullString
		var details []byte
		if err := rows.Scan(
			&l.ID, &userID, &actorID, &l.Action, &l.ResourceType, &resourceID,
			&details, &l.IPAddress, &l.UserAgent, &requestID, &l.CreatedAt,
		); err != nil {
			return nil, err
		}
		if userID.Valid {
			l.UserID = &userID.UUID
		}
		if actorID.Valid {
			l.ActorID = &actorID.UUID
		}
		if resourceID.Valid {
			l.ResourceID = &resourceID.UUID
		}
		if requestID.Valid {
			l.RequestID = requestID.String
		}
		if len(details) > 0 {
			l.Details = json.RawMessage(details)
		}
		out = append(out, &l)
	}
	return out, nil
}

// NOTE: audit_logs is append-only (DB trigger rejects UPDATE/DELETE).
// Retention must be implemented as archival to cold storage with legal-hold
// support, never as DELETE. There is intentionally no Prune/Delete method.
