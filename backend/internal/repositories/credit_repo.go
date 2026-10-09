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

type CreditRepository struct {
	db *sql.DB
}

func NewCreditRepository(db *sql.DB) *CreditRepository {
	return &CreditRepository{db: db}
}

func (r *CreditRepository) Create(ctx context.Context, cr *entities.CreditReport) error {
	const q = `
        INSERT INTO credit_reports (
            id, user_id, score, rating, total_debt, available_credit,
            credit_utilization, payment_history, credit_age_months,
            num_accounts, hard_inquiries, delinquent_accounts,
            report_date, created_at, updated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
    `
	_, err := runner(ctx, r.db).ExecContext(ctx, q,
		cr.ID, cr.UserID, cr.Score, cr.Rating,
		cr.TotalDebt.String(), cr.AvailableCredit.String(),
		cr.CreditUtilization.String(), cr.PaymentHistory.String(),
		cr.CreditAgeMonths, cr.NumAccounts, cr.HardInquiries,
		cr.DelinquentAccounts, cr.ReportDate, cr.CreatedAt, cr.UpdatedAt,
	)
	return err
}

func parseCreditDecimal(name, s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, fmt.Errorf("parse credit %s: %w", name, err)
	}
	return d, nil
}

func (r *CreditRepository) GetLatestByUserID(ctx context.Context, userID uuid.UUID) (*entities.CreditReport, error) {
	const q = `
        SELECT id, user_id, score, rating, total_debt, available_credit,
               credit_utilization, payment_history, credit_age_months,
               num_accounts, hard_inquiries, delinquent_accounts,
               report_date, created_at, updated_at
        FROM credit_reports WHERE user_id=$1
        ORDER BY report_date DESC LIMIT 1
    `
	var cr entities.CreditReport
	var totalDebt, availCredit, util, hist string
	err := runner(ctx, r.db).QueryRowContext(ctx, q, userID).Scan(
		&cr.ID, &cr.UserID, &cr.Score, &cr.Rating,
		&totalDebt, &availCredit, &util, &hist,
		&cr.CreditAgeMonths, &cr.NumAccounts, &cr.HardInquiries,
		&cr.DelinquentAccounts, &cr.ReportDate, &cr.CreatedAt, &cr.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var err2 error
	if cr.TotalDebt, err2 = parseCreditDecimal("total_debt", totalDebt); err2 != nil {
		return nil, err2
	}
	if cr.AvailableCredit, err2 = parseCreditDecimal("available_credit", availCredit); err2 != nil {
		return nil, err2
	}
	if cr.CreditUtilization, err2 = parseCreditDecimal("credit_utilization", util); err2 != nil {
		return nil, err2
	}
	if cr.PaymentHistory, err2 = parseCreditDecimal("payment_history", hist); err2 != nil {
		return nil, err2
	}
	return &cr, nil
}

func (r *CreditRepository) GetHistory(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.CreditReport, error) {
	rows, err := runner(ctx, r.db).QueryContext(ctx, `
        SELECT id, user_id, score, rating, total_debt, available_credit,
               credit_utilization, payment_history, credit_age_months,
               num_accounts, hard_inquiries, delinquent_accounts,
               report_date, created_at, updated_at
        FROM credit_reports WHERE user_id=$1
        ORDER BY report_date DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*entities.CreditReport
	for rows.Next() {
		var cr entities.CreditReport
		var totalDebt, availCredit, util, hist string
		if err := rows.Scan(
			&cr.ID, &cr.UserID, &cr.Score, &cr.Rating,
			&totalDebt, &availCredit, &util, &hist,
			&cr.CreditAgeMonths, &cr.NumAccounts, &cr.HardInquiries,
			&cr.DelinquentAccounts, &cr.ReportDate, &cr.CreatedAt, &cr.UpdatedAt,
		); err != nil {
			return nil, err
		}
		var err2 error
		if cr.TotalDebt, err2 = parseCreditDecimal("total_debt", totalDebt); err2 != nil {
			return nil, err2
		}
		if cr.AvailableCredit, err2 = parseCreditDecimal("available_credit", availCredit); err2 != nil {
			return nil, err2
		}
		if cr.CreditUtilization, err2 = parseCreditDecimal("credit_utilization", util); err2 != nil {
			return nil, err2
		}
		if cr.PaymentHistory, err2 = parseCreditDecimal("payment_history", hist); err2 != nil {
			return nil, err2
		}
		out = append(out, &cr)
	}
	return out, nil
}

func (r *CreditRepository) MarkStale(ctx context.Context, userID uuid.UUID) error {
	// No-op for now — could set a flag to force refresh
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE credit_reports SET updated_at=$1 WHERE user_id=$2`,
		time.Now().UTC(), userID)
	return err
}
