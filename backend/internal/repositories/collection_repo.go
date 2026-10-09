package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"lending-app/backend/internal/domain/entities"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type CollectionRepository struct {
	db *sql.DB
}

func NewCollectionRepository(db *sql.DB) *CollectionRepository {
	return &CollectionRepository{db: db}
}

func (r *CollectionRepository) Create(ctx context.Context, c *entities.Collection) error {
	const q = `
        INSERT INTO collections (
            id, loan_id, user_id, amount_outstanding, days_past_due,
            status, assigned_to, last_contact_date, next_contact_date,
            notes, created_at, updated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
    `
	_, err := runner(ctx, r.db).ExecContext(ctx, q,
		c.ID, c.LoanID, c.UserID, c.AmountOutstanding.String(),
		c.DaysPastDue, c.Status, c.AssignedTo, c.LastContactDate,
		c.NextContactDate, c.Notes, c.CreatedAt, c.UpdatedAt,
	)
	return err
}

const collectionCols = `
    id, loan_id, user_id, amount_outstanding, days_past_due,
    status, assigned_to, last_contact_date, next_contact_date,
    notes, created_at, updated_at
`

func (r *CollectionRepository) scan(row interface{ Scan(...interface{}) error }) (*entities.Collection, error) {
	var c entities.Collection
	var amount string
	var assignedTo uuid.NullUUID
	var lastContact, nextContact sql.NullTime
	err := row.Scan(
		&c.ID, &c.LoanID, &c.UserID, &amount, &c.DaysPastDue,
		&c.Status, &assignedTo, &lastContact, &nextContact,
		&c.Notes, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	c.AmountOutstanding, err = decimal.NewFromString(amount)
	if err != nil {
		return nil, fmt.Errorf("parse collection amount_outstanding: %w", err)
	}
	if assignedTo.Valid {
		c.AssignedTo = &assignedTo.UUID
	}
	if lastContact.Valid {
		c.LastContactDate = &lastContact.Time
	}
	if nextContact.Valid {
		c.NextContactDate = &nextContact.Time
	}
	return &c, nil
}

func (r *CollectionRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.Collection, error) {
	c, err := r.scan(runner(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+collectionCols+` FROM collections WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (r *CollectionRepository) List(ctx context.Context, userID *uuid.UUID, status string, limit, offset int) ([]*entities.Collection, int, error) {
	where := "WHERE 1=1"
	args := []any{}
	idx := 1
	if userID != nil {
		where += " AND user_id=$" + itoa(idx)
		args = append(args, *userID)
		idx++
	}
	if status != "" {
		where += " AND status=$" + itoa(idx)
		args = append(args, status)
		idx++
	}

	q := runner(ctx, r.db)

	var total int
	if err := q.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM collections "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := q.QueryContext(ctx,
		"SELECT "+collectionCols+" FROM collections "+where+
			" ORDER BY days_past_due DESC LIMIT $"+itoa(idx)+" OFFSET $"+itoa(idx+1),
		args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*entities.Collection
	for rows.Next() {
		c, err := r.scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, nil
}

func (r *CollectionRepository) GetActiveByLoanID(ctx context.Context, loanID uuid.UUID) (*entities.Collection, error) {
	c, err := r.scan(runner(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+collectionCols+` FROM collections WHERE loan_id=$1 AND status='active'`, loanID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (r *CollectionRepository) Update(ctx context.Context, c *entities.Collection) error {
	_, err := runner(ctx, r.db).ExecContext(ctx, `
        UPDATE collections SET amount_outstanding=$1, days_past_due=$2,
            status=$3, assigned_to=$4, last_contact_date=$5,
            next_contact_date=$6, notes=$7, updated_at=$8
        WHERE id=$9`,
		c.AmountOutstanding.String(), c.DaysPastDue, c.Status,
		c.AssignedTo, c.LastContactDate, c.NextContactDate, c.Notes,
		time.Now().UTC(), c.ID)
	return err
}

// small helper to avoid importing strconv in this file
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
