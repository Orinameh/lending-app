package repositories

import (
	"context"
	"errors"
	"fmt"
	"lending-app/backend/internal/domain/entities"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// PaymentRepository records one row per payment attempt. The UNIQUE on
// transaction_id is the atomic idempotency gate: concurrent replays race on
// the INSERT itself, so there is no check-then-act window.
type PaymentRepository struct {
	db *pgxpool.Pool
}

func NewPaymentRepository(db *pgxpool.Pool) *PaymentRepository {
	return &PaymentRepository{db: db}
}

const paymentCols = `
    id, loan_id, user_id, amount, applied, overpayment,
    payment_method, transaction_id, status, created_at, updated_at
`

func (r *PaymentRepository) scan(row interface{ Scan(...interface{}) error }) (*entities.Payment, error) {
	var p entities.Payment
	var amount, applied, over string
	err := row.Scan(
		&p.ID, &p.LoanID, &p.UserID, &amount, &applied, &over,
		&p.PaymentMethod, &p.TransactionID, &p.Status,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	parse := func(name, s string) (decimal.Decimal, error) {
		d, err := decimal.NewFromString(s)
		if err != nil {
			return decimal.Zero, fmt.Errorf("parse payment %s: %w", name, err)
		}
		return d, nil
	}
	if p.Amount, err = parse("amount", amount); err != nil {
		return nil, err
	}
	if p.Applied, err = parse("applied", applied); err != nil {
		return nil, err
	}
	if p.Overpayment, err = parse("overpayment", over); err != nil {
		return nil, err
	}
	return &p, nil
}

// Create inserts the payment attempt. A unique violation means this exact
// transaction already went through — callers map it to a duplicate error.
func (r *PaymentRepository) Create(ctx context.Context, p *entities.Payment) error {
	now := time.Now().UTC()
	p.CreatedAt, p.UpdatedAt = now, now
	_, err := querier(ctx, r.db).Exec(ctx, `
        INSERT INTO payments (
            id, loan_id, user_id, amount, applied, overpayment,
            payment_method, transaction_id, status, created_at, updated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		p.ID, p.LoanID, p.UserID, p.Amount.String(), p.Applied.String(),
		p.Overpayment.String(), p.PaymentMethod, p.TransactionID,
		p.Status, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

func (r *PaymentRepository) GetByTransactionID(ctx context.Context, txnID string) (*entities.Payment, error) {
	p, err := r.scan(querier(ctx, r.db).QueryRow(ctx,
		`SELECT `+paymentCols+` FROM payments WHERE transaction_id=$1`, txnID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (r *PaymentRepository) Settle(ctx context.Context, id uuid.UUID, applied, overpayment decimal.Decimal) error {
	_, err := querier(ctx, r.db).Exec(ctx,
		`UPDATE payments SET applied=$1, overpayment=$2, status='applied', updated_at=$3
         WHERE id=$4`,
		applied.String(), overpayment.String(), time.Now().UTC(), id)
	return err
}
