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

type RepaymentRepository struct {
	db *sql.DB
}

func NewRepaymentRepository(db *sql.DB) *RepaymentRepository {
	return &RepaymentRepository{db: db}
}

func (r *RepaymentRepository) Create(ctx context.Context, p *entities.Repayment) error {
	const q = `
        INSERT INTO repayments (
            id, loan_id, amount, principal_amount, interest_amount,
            amount_paid, principal_paid, interest_paid, fee_paid,
            payment_date, due_date, status, payment_method, transaction_id,
            late_fee, created_at, updated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
    `
	_, err := runner(ctx, r.db).ExecContext(ctx, q,
		p.ID, p.LoanID, p.Amount.String(), p.PrincipalAmount.String(),
		p.InterestAmount.String(), p.AmountPaid.String(), p.PrincipalPaid.String(),
		p.InterestPaid.String(), p.FeePaid.String(),
		p.PaymentDate, p.DueDate, p.Status,
		nullStr(p.PaymentMethod), nullStr(p.TransactionID), p.LateFee.String(),
		p.CreatedAt, p.UpdatedAt,
	)
	return err
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

const repaymentCols = `
    id, loan_id, amount, principal_amount, interest_amount,
    amount_paid, principal_paid, interest_paid, fee_paid,
    payment_date, due_date, status, payment_method, transaction_id,
    late_fee, created_at, updated_at
`

func (r *RepaymentRepository) scan(row interface{ Scan(...interface{}) error }) (*entities.Repayment, error) {
	var p entities.Repayment
	var amount, principal, interest, amtPaid, princPaid, intPaid, feePaid, lateFee string
	var payDate sql.NullTime
	var payMethod, txnID sql.NullString
	err := row.Scan(
		&p.ID, &p.LoanID, &amount, &principal, &interest,
		&amtPaid, &princPaid, &intPaid, &feePaid,
		&payDate, &p.DueDate, &p.Status, &payMethod, &txnID,
		&lateFee, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	parse := func(name, s string) (decimal.Decimal, error) {
		d, err := decimal.NewFromString(s)
		if err != nil {
			return decimal.Zero, fmt.Errorf("parse repayment %s: %w", name, err)
		}
		return d, nil
	}
	if p.Amount, err = parse("amount", amount); err != nil {
		return nil, err
	}
	if p.PrincipalAmount, err = parse("principal_amount", principal); err != nil {
		return nil, err
	}
	if p.InterestAmount, err = parse("interest_amount", interest); err != nil {
		return nil, err
	}
	if p.AmountPaid, err = parse("amount_paid", amtPaid); err != nil {
		return nil, err
	}
	if p.PrincipalPaid, err = parse("principal_paid", princPaid); err != nil {
		return nil, err
	}
	if p.InterestPaid, err = parse("interest_paid", intPaid); err != nil {
		return nil, err
	}
	if p.FeePaid, err = parse("fee_paid", feePaid); err != nil {
		return nil, err
	}
	if p.LateFee, err = parse("late_fee", lateFee); err != nil {
		return nil, err
	}
	if payDate.Valid {
		p.PaymentDate = &payDate.Time
	}
	if payMethod.Valid {
		p.PaymentMethod = payMethod.String
	}
	if txnID.Valid {
		p.TransactionID = txnID.String
	}
	return &p, nil
}

func (r *RepaymentRepository) GetByLoanID(ctx context.Context, loanID uuid.UUID) ([]*entities.Repayment, error) {
	rows, err := runner(ctx, r.db).QueryContext(ctx,
		`SELECT `+repaymentCols+` FROM repayments WHERE loan_id=$1 ORDER BY due_date ASC`, loanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*entities.Repayment
	for rows.Next() {
		p, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// GetByLoanIDForUpdate locks the schedule rows for a repayment transaction.
func (r *RepaymentRepository) GetByLoanIDForUpdate(ctx context.Context, loanID uuid.UUID) ([]*entities.Repayment, error) {
	rows, err := runner(ctx, r.db).QueryContext(ctx,
		`SELECT `+repaymentCols+` FROM repayments WHERE loan_id=$1 ORDER BY due_date ASC FOR UPDATE`, loanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*entities.Repayment
	for rows.Next() {
		p, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *RepaymentRepository) ExistsTransaction(ctx context.Context, txnID string) (bool, error) {
	if txnID == "" {
		return false, nil
	}
	var n int
	err := runner(ctx, r.db).QueryRowContext(ctx,
		`SELECT COUNT(*) FROM repayments WHERE transaction_id=$1`, txnID).Scan(&n)
	return n > 0, err
}

func (r *RepaymentRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.Repayment, error) {
	p, err := r.scan(runner(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+repaymentCols+` FROM repayments WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (r *RepaymentRepository) Update(ctx context.Context, p *entities.Repayment) error {
	_, err := runner(ctx, r.db).ExecContext(ctx, `
        UPDATE repayments SET status=$1, payment_date=$2, payment_method=$3,
            transaction_id=$4, late_fee=$5, amount_paid=$6, principal_paid=$7,
            interest_paid=$8, fee_paid=$9, updated_at=$10 WHERE id=$11`,
		p.Status, p.PaymentDate, nullStr(p.PaymentMethod), nullStr(p.TransactionID),
		p.LateFee.String(), p.AmountPaid.String(), p.PrincipalPaid.String(),
		p.InterestPaid.String(), p.FeePaid.String(), time.Now().UTC(), p.ID)
	return err
}
