package services

import (
	"context"
	"errors"
	"lending-app/backend/internal/domain/entities"
	"lending-app/backend/internal/repositories"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type CreditService struct {
	creditRepo    *repositories.CreditRepository
	userRepo      *repositories.UserRepository
	loanRepo      *repositories.LoanRepository
	repaymentRepo *repositories.RepaymentRepository
}

func NewCreditService(
	cr *repositories.CreditRepository,
	ur *repositories.UserRepository,
	lr *repositories.LoanRepository,
	rr *repositories.RepaymentRepository,
) *CreditService {
	return &CreditService{creditRepo: cr, userRepo: ur, loanRepo: lr, repaymentRepo: rr}
}

func (s *CreditService) GetLatestOrGenerate(ctx context.Context, userID uuid.UUID) (*entities.CreditReport, error) {
	cr, err := s.creditRepo.GetLatestByUserID(ctx, userID)
	if err == nil {
		// Report is fresh (less than 30 days old) → return
		if time.Since(cr.ReportDate) < 30*24*time.Hour {
			return cr, nil
		}
	} else if !errors.Is(err, repositories.ErrNotFound) {
		return nil, err
	}
	return s.GenerateReport(ctx, userID)
}

// GenerateReport builds an internal credit snapshot derived ENTIRELY from the
// app's own data: profile/KYC, loan book, repayment performance, and tenure.
// There is no randomness: identical underlying data yields an identical
// report, and re-generating without new activity returns the existing row
// instead of inserting a duplicate.
//
// PRODUCTION NOTE: this remains an internal behavioral score, not a bureau
// score. Before real lending, integrate a licensed bureau / open-banking
// provider and persist the bureau reference, raw payload hash, and consent
// record. All ratio scales below are PERCENT (0-100) to match calculateRisk.
func (s *CreditService) GenerateReport(ctx context.Context, userID uuid.UUID) (*entities.CreditReport, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	loans, _, err := s.loanRepo.GetUserLoans(ctx, userID, 1000, 0)
	if err != nil {
		return nil, err
	}

	var (
		totalDisbursed  = decimal.Zero
		outstanding     = decimal.Zero
		totalDue        int64
		onTime          int64
		delinquentLoans = map[uuid.UUID]bool{}
		recentApps      int
	)
	twelveMonthsAgo := now.AddDate(-1, 0, 0)

	for _, loan := range loans {
		if loan.ApplicationDate.After(twelveMonthsAgo) {
			recentApps++
		}
		// Only funded loans contribute balances/performance.
		if loan.Status != entities.LoanStatusDisbursed &&
			loan.Status != entities.LoanStatusApproved &&
			loan.Status != entities.LoanStatusRepaid &&
			loan.Status != entities.LoanStatusDefaulted {
			continue
		}
		if loan.DisbursementDate != nil {
			totalDisbursed = totalDisbursed.Add(loan.Amount)
		} else if loan.Status == entities.LoanStatusRepaid {
			totalDisbursed = totalDisbursed.Add(loan.Amount)
		}
		if loan.Status == entities.LoanStatusDefaulted {
			delinquentLoans[loan.ID] = true
		}
		sched, err := s.repaymentRepo.GetByLoanID(ctx, loan.ID)
		if err != nil {
			return nil, err
		}
		for _, p := range sched {
			if p.DueDate.After(now) {
				continue // not yet due — no performance signal
			}
			totalDue++
			if p.Status == entities.RepaymentStatusPaid {
				if p.PaymentDate != nil && !p.PaymentDate.After(p.DueDate) {
					onTime++
				} else if p.PaymentDate == nil {
					onTime++
				}
				// Paid late counts as missed for on-time purposes.
			} else {
				// Unpaid past-due installment → delinquent loan.
				delinquentLoans[loan.ID] = true
			}
			outstanding = outstanding.Add(p.BalanceDue())
		}
	}

	// Payment history % (on-time share of due installments; 100 when none due).
	paymentHistory := decimal.NewFromInt(100)
	if totalDue > 0 {
		paymentHistory = decimal.NewFromInt(onTime).Mul(decimal.NewFromInt(100)).
			Div(decimal.NewFromInt(totalDue)).Round(2)
	}

	// Utilization % = outstanding / capacity, capped at 100.
	capacity := user.AnnualIncome
	if capacity.IsZero() {
		capacity = totalDisbursed
	}
	utilization := decimal.Zero
	if !capacity.IsZero() && capacity.IsPositive() {
		utilization = outstanding.Mul(decimal.NewFromInt(100)).Div(capacity).Round(2)
		if utilization.GreaterThan(decimal.NewFromInt(100)) {
			utilization = decimal.NewFromInt(100)
		}
		if utilization.IsNegative() {
			utilization = decimal.Zero
		}
	}

	// Available credit = capacity headroom, floored at zero.
	availableCredit := capacity.Sub(outstanding)
	if availableCredit.IsNegative() {
		availableCredit = decimal.Zero
	}
	availableCredit = availableCredit.Round(2)

	// Tenure in whole months from account creation.
	tenureMonths := monthsBetween(user.CreatedAt, now)

	candidate := &entities.CreditReport{
		ID:                 uuid.New(),
		UserID:             userID,
		TotalDebt:          outstanding.Round(2),
		AvailableCredit:    availableCredit,
		CreditUtilization:  utilization,
		PaymentHistory:     paymentHistory,
		CreditAgeMonths:    tenureMonths,
		NumAccounts:        len(loans),
		HardInquiries:      recentApps,
		DelinquentAccounts: len(delinquentLoans),
		ReportDate:         now,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	candidate.Score = deriveScore(user, candidate)
	candidate.Rating = ratingFor(candidate.Score)

	// No new activity since the latest report → return it, don't spam history.
	if latest, err := s.creditRepo.GetLatestByUserID(ctx, userID); err == nil {
		if sameSnapshot(latest, candidate) {
			return latest, nil
		}
	} else if !errors.Is(err, repositories.ErrNotFound) {
		return nil, err
	}

	if err := s.creditRepo.Create(ctx, candidate); err != nil {
		return nil, err
	}
	return candidate, nil
}

// deriveScore is a documented, deterministic weighted model over real inputs.
func deriveScore(user *entities.User, cr *entities.CreditReport) int {
	score := 650

	// Payment history (±60): ±2 points per percent off 90.
	ph := cr.PaymentHistory.InexactFloat64()
	adj := int((ph - 90) * 2)
	if adj > 60 {
		adj = 60
	}
	if adj < -60 {
		adj = -60
	}
	score += adj

	// Utilization: low usage rewards, high usage penalizes.
	switch u := cr.CreditUtilization.InexactFloat64(); {
	case cr.TotalDebt.IsZero():
		// No debt: neutral (thin file handled via tenure/KYC below).
	case u <= 20:
		score += 30
	case u <= 40:
		score += 10
	case u <= 60:
		score -= 20
	default:
		score -= 50
	}

	// Delinquency: -60 per delinquent loan, capped at -180.
	if d := cr.DelinquentAccounts; d > 0 {
		pen := d * 60
		if pen > 180 {
			pen = 180
		}
		score -= pen
	}

	// Inquiries: -10 per application beyond the first in 12 months, cap -40.
	if extra := cr.HardInquiries - 1; extra > 0 {
		pen := extra * 10
		if pen > 40 {
			pen = 40
		}
		score -= pen
	}

	// Tenure: +2 per 6 months, capped at +30.
	if bonus := (cr.CreditAgeMonths / 6) * 2; bonus > 0 {
		if bonus > 30 {
			bonus = 30
		}
		score += bonus
	}

	// KYC + employment stability.
	switch user.KYCStatus {
	case "verified":
		score += 20
	case "rejected":
		score -= 30
	default:
		score -= 10
	}
	switch user.EmploymentType {
	case "employed":
		score += 15
	case "self_employed":
		score += 5
	case "student", "unemployed":
		score -= 15
	case "retired":
		score -= 5
	}

	if score < 300 {
		score = 300
	}
	if score > 850 {
		score = 850
	}
	return score
}

// sameSnapshot reports whether candidate carries no new information versus
// the latest stored report (IDs/dates excluded).
func sameSnapshot(latest, candidate *entities.CreditReport) bool {
	return latest.Score == candidate.Score &&
		latest.Rating == candidate.Rating &&
		latest.TotalDebt.Equal(candidate.TotalDebt) &&
		latest.AvailableCredit.Equal(candidate.AvailableCredit) &&
		latest.CreditUtilization.Equal(candidate.CreditUtilization) &&
		latest.PaymentHistory.Equal(candidate.PaymentHistory) &&
		latest.CreditAgeMonths == candidate.CreditAgeMonths &&
		latest.NumAccounts == candidate.NumAccounts &&
		latest.HardInquiries == candidate.HardInquiries &&
		latest.DelinquentAccounts == candidate.DelinquentAccounts
}

func monthsBetween(from, to time.Time) int {
	y1, m1, _ := from.Date()
	y2, m2, _ := to.Date()
	n := (y2-y1)*12 + int(m2-m1)
	if n < 0 {
		return 0
	}
	return n
}

func (s *CreditService) GetHistory(ctx context.Context, userID uuid.UUID, limit int) ([]*entities.CreditReport, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return s.creditRepo.GetHistory(ctx, userID, limit)
}

func ratingFor(score int) string {
	switch {
	case score >= 800:
		return "Exceptional"
	case score >= 740:
		return "Very Good"
	case score >= 670:
		return "Good"
	case score >= 580:
		return "Fair"
	default:
		return "Poor"
	}
}
