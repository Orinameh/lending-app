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

type LoanRepository struct {
	db *sql.DB
}

func NewLoanRepository(db *sql.DB) *LoanRepository {
	return &LoanRepository{db: db}
}

func (r *LoanRepository) Create(ctx context.Context, l *entities.Loan) error {
	const q = `
        INSERT INTO loans (
            id, user_id, amount, interest_rate, effective_apr, term_months, purpose, status,
            currency, idempotency_key,
            application_date, approval_date, disbursement_date, maturity_date,
            monthly_payment, total_payment, total_interest, risk_score,
            credit_decision, created_at, updated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
    `
	_, err := runner(ctx, r.db).ExecContext(ctx, q,
		l.ID, l.UserID, l.Amount.String(), l.InterestRate.String(), l.EffectiveAPR.String(), l.TermMonths,
		l.Purpose, l.Status, l.Currency, nullIfEmpty(l.IdempotencyKey),
		l.ApplicationDate, l.ApprovalDate, l.DisbursementDate,
		l.MaturityDate, l.MonthlyPayment.String(), l.TotalPayment.String(),
		l.TotalInterest.String(), l.RiskScore, l.CreditDecision,
		l.CreatedAt, l.UpdatedAt,
	)
	return err
}

func nullIfEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

const loanCols = `
    id, user_id, amount, interest_rate, effective_apr, term_months, purpose, status,
    currency, idempotency_key,
    application_date, approval_date, disbursement_date, maturity_date,
    monthly_payment, total_payment, total_interest, risk_score,
    credit_decision, created_at, updated_at, deleted_at
`

func (r *LoanRepository) scan(row interface{ Scan(...interface{}) error }) (*entities.Loan, error) {
	var l entities.Loan
	var amount, rate, apr, monthly, total, interest string
	var effAPR sql.NullString
	var idemKey sql.NullString
	var approvalDate, disbDate, deletedAt sql.NullTime
	err := row.Scan(
		&l.ID, &l.UserID, &amount, &rate, &apr, &l.TermMonths, &l.Purpose, &l.Status,
		&l.Currency, &idemKey,
		&l.ApplicationDate, &approvalDate, &disbDate, &l.MaturityDate,
		&monthly, &total, &interest, &l.RiskScore, &l.CreditDecision,
		&l.CreatedAt, &l.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return nil, err
	}
	parse := func(name, s string) (decimal.Decimal, error) {
		d, err := decimal.NewFromString(s)
		if err != nil {
			return decimal.Zero, fmt.Errorf("parse loan %s: %w", name, err)
		}
		return d, nil
	}
	if l.Amount, err = parse("amount", amount); err != nil {
		return nil, err
	}
	if l.InterestRate, err = parse("interest_rate", rate); err != nil {
		return nil, err
	}
	if apr != "" {
		if l.EffectiveAPR, err = parse("effective_apr", apr); err != nil {
			return nil, err
		}
	}
	_ = effAPR
	if l.MonthlyPayment, err = parse("monthly_payment", monthly); err != nil {
		return nil, err
	}
	if l.TotalPayment, err = parse("total_payment", total); err != nil {
		return nil, err
	}
	if l.TotalInterest, err = parse("total_interest", interest); err != nil {
		return nil, err
	}
	if idemKey.Valid {
		l.IdempotencyKey = idemKey.String
	}
	if approvalDate.Valid {
		l.ApprovalDate = &approvalDate.Time
	}
	if disbDate.Valid {
		l.DisbursementDate = &disbDate.Time
	}
	if deletedAt.Valid {
		l.DeletedAt = &deletedAt.Time
	}
	return &l, nil
}

func (r *LoanRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.Loan, error) {
	q := `SELECT ` + loanCols + ` FROM loans WHERE id=$1 AND deleted_at IS NULL`
	l, err := r.scan(runner(ctx, r.db).QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return l, err
}

func (r *LoanRepository) GetByIdempotencyKey(ctx context.Context, key string) (*entities.Loan, error) {
	if key == "" {
		return nil, ErrNotFound
	}
	q := `SELECT ` + loanCols + ` FROM loans WHERE idempotency_key=$1 AND deleted_at IS NULL`
	l, err := r.scan(runner(ctx, r.db).QueryRowContext(ctx, q, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return l, err
}

func (r *LoanRepository) GetUserLoans(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*entities.Loan, int, error) {
	q := runner(ctx, r.db)
	var total int
	if err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM loans WHERE user_id=$1 AND deleted_at IS NULL`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := q.QueryContext(ctx,
		`SELECT `+loanCols+` FROM loans WHERE user_id=$1 AND deleted_at IS NULL
         ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*entities.Loan
	for rows.Next() {
		l, err := r.scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, nil
}

func (r *LoanRepository) List(ctx context.Context, status string, limit, offset int) ([]*entities.Loan, int, error) {
	q := runner(ctx, r.db)
	var total int
	var err error
	if status != "" {
		err = q.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM loans WHERE status=$1 AND deleted_at IS NULL`, status).Scan(&total)
	} else {
		err = q.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM loans WHERE deleted_at IS NULL`).Scan(&total)
	}
	if err != nil {
		return nil, 0, err
	}

	var rows *sql.Rows
	if status != "" {
		rows, err = q.QueryContext(ctx,
			`SELECT `+loanCols+` FROM loans WHERE status=$1 AND deleted_at IS NULL
             ORDER BY created_at DESC LIMIT $2 OFFSET $3`, status, limit, offset)
	} else {
		rows, err = q.QueryContext(ctx,
			`SELECT `+loanCols+` FROM loans WHERE deleted_at IS NULL
             ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*entities.Loan
	for rows.Next() {
		l, err := r.scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, nil
}

func (r *LoanRepository) Update(ctx context.Context, l *entities.Loan) error {
	const q = `
        UPDATE loans SET status=$1, approval_date=$2, disbursement_date=$3,
            maturity_date=$4, monthly_payment=$5, total_payment=$6, total_interest=$7,
            effective_apr=$8, risk_score=$9, credit_decision=$10, updated_at=$11
        WHERE id=$12 AND deleted_at IS NULL
    `
	_, err := runner(ctx, r.db).ExecContext(ctx, q,
		l.Status, l.ApprovalDate, l.DisbursementDate, l.MaturityDate,
		l.MonthlyPayment.String(), l.TotalPayment.String(), l.TotalInterest.String(),
		l.EffectiveAPR.String(), l.RiskScore,
		l.CreditDecision, time.Now().UTC(), l.ID)
	return err
}

// UpdateStatusConditional performs an optimistic state transition:
// UPDATE ... WHERE id=$ AND status=$expected. Returns ErrNotFound if the
// row is missing or already moved (concurrent transition).
func (r *LoanRepository) UpdateStatusConditional(ctx context.Context, id uuid.UUID, from, to entities.LoanStatus) error {
	res, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE loans SET status=$1, updated_at=$2 WHERE id=$3 AND status=$4 AND deleted_at IS NULL`,
		to, time.Now().UTC(), id, from)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *LoanRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.LoanStatus) error {
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE loans SET status=$1, updated_at=$2 WHERE id=$3 AND deleted_at IS NULL`,
		status, time.Now().UTC(), id)
	return err
}

// GetOverdueLoans is used by the collections service
func (r *LoanRepository) GetOverdueLoans(ctx context.Context, limit int) ([]*entities.Loan, error) {
	rows, err := runner(ctx, r.db).QueryContext(ctx,
		`SELECT `+loanCols+` FROM loans
         WHERE status IN ('disbursed','approved') AND maturity_date < $1 AND deleted_at IS NULL
         ORDER BY maturity_date ASC LIMIT $2`,
		time.Now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*entities.Loan
	for rows.Next() {
		l, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// GetOverdueInstallments returns disbursed/approved loans having at least one
// unpaid installment past due — the correct DPD grain (per-installment).
func (r *LoanRepository) GetOverdueInstallments(ctx context.Context, now time.Time, limit int) ([]*entities.Loan, error) {
	rows, err := runner(ctx, r.db).QueryContext(ctx,
		`SELECT DISTINCT l.id, l.user_id, l.amount, l.interest_rate, l.effective_apr,
         l.term_months, l.purpose, l.status, l.currency, l.idempotency_key,
         l.application_date, l.approval_date, l.disbursement_date, l.maturity_date,
         l.monthly_payment, l.total_payment, l.total_interest, l.risk_score,
         l.credit_decision, l.created_at, l.updated_at, l.deleted_at
         FROM loans l JOIN repayments p ON p.loan_id = l.id
         WHERE l.status IN ('disbursed','approved') AND l.deleted_at IS NULL
           AND p.status IN ('pending','partial','overdue') AND p.due_date < $1
         ORDER BY l.id LIMIT $2`,
		now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*entities.Loan
	for rows.Next() {
		l, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}
