package services

import (
	"context"
	"database/sql"
	"errors"
	"lending-app/backend/internal/domain/entities"
	"lending-app/backend/internal/repositories"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type LoanService struct {
	db            *sql.DB
	loanRepo      *repositories.LoanRepository
	creditRepo    *repositories.CreditRepository
	repaymentRepo *repositories.RepaymentRepository
}

func NewLoanService(
	db *sql.DB,
	loanRepo *repositories.LoanRepository,
	creditRepo *repositories.CreditRepository,
	repaymentRepo *repositories.RepaymentRepository,
) *LoanService {
	return &LoanService{db: db, loanRepo: loanRepo, creditRepo: creditRepo, repaymentRepo: repaymentRepo}
}

type LoanApplicationRequest struct {
	Amount          decimal.Decimal `json:"amount"`
	TermMonths      int             `json:"termMonths"`
	Purpose         string          `json:"purpose"`
	MonthlyIncome   decimal.Decimal `json:"monthlyIncome"`
	MonthlyExpenses decimal.Decimal `json:"monthlyExpenses"`
	AnnualIncome    decimal.Decimal `json:"annualIncome"`
	IdempotencyKey  string          `json:"idempotencyKey"`
}

var (
	ErrLoanValidation = errors.New("loan validation failed")
	ErrLoanLimit      = errors.New("loan limit exceeded")
)

// PricedRate is the single server-side pricing control. Borrowers must NOT
// self-price (previous InterestRate request field removed). Tier by risk band.
func PricedRate(riskScore int) decimal.Decimal {
	switch {
	case riskScore >= 740:
		return decimal.NewFromFloat(12.0)
	case riskScore >= 670:
		return decimal.NewFromFloat(18.0)
	case riskScore >= 580:
		return decimal.NewFromFloat(24.0)
	default:
		return decimal.NewFromFloat(30.0)
	}
}

func (s *LoanService) ApplyForLoan(ctx context.Context, userID uuid.UUID, req *LoanApplicationRequest) (*entities.Loan, error) {
	if err := validateLoanRequest(req); err != nil {
		return nil, err
	}

	// Idempotency: same key returns the original loan (safe retry/double-click).
	if strings.TrimSpace(req.IdempotencyKey) != "" {
		if existing, err := s.loanRepo.GetByIdempotencyKey(ctx, strings.TrimSpace(req.IdempotencyKey)); err == nil {
			if existing.UserID != userID {
				return nil, errors.New("idempotency key conflict")
			}
			return existing, nil
		} else if !errors.Is(err, repositories.ErrNotFound) {
			return nil, err
		}
	}

	creditReport, err := s.creditRepo.GetLatestByUserID(ctx, userID)
	if err != nil {
		return nil, errors.New("credit report required before applying")
	}

	riskScore, decision := calculateRisk(creditReport, req)
	rate := PricedRate(riskScore)
	monthly, total, interest := calculateLoanTerms(req.Amount, rate, req.TermMonths)
	effectiveAPR := effectiveAnnualRate(rate)

	now := time.Now().UTC()
	loan := &entities.Loan{
		ID:              uuid.New(),
		UserID:          userID,
		Amount:          req.Amount,
		InterestRate:    rate,
		EffectiveAPR:    effectiveAPR,
		TermMonths:      req.TermMonths,
		Purpose:         strings.TrimSpace(req.Purpose),
		Status:          entities.LoanStatusPending,
		Currency:        "NGN",
		IdempotencyKey:  strings.TrimSpace(req.IdempotencyKey),
		ApplicationDate: now,
		// Maturity is provisional until disbursement; recalculated on Disburse.
		MaturityDate:   now.AddDate(0, req.TermMonths, 0),
		MonthlyPayment: monthly,
		TotalPayment:   total,
		TotalInterest:  interest,
		RiskScore:      riskScore,
		CreditDecision: decision,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	txCtx, cancel := repositories.WithTxTimeout(ctx)
	defer cancel()
	var out *entities.Loan
	// ACID + concurrency: serializable tx with automatic retry on 40001/40P01.
	err = repositories.Transact(txCtx, s.db, func(txCtx context.Context) error {
		if err := s.loanRepo.Create(txCtx, loan); err != nil {
			return err
		}

		switch decision {
		case entities.CreditApproved:
			loan.Status = entities.LoanStatusApproved
			loan.ApprovalDate = &now
			if err := s.loanRepo.Update(txCtx, loan); err != nil {
				return err
			}
			// Schedule is created at DISBURSEMENT, not approval (no pre-funding interest).
		case entities.CreditPendingReview:
			loan.Status = entities.LoanStatusUnderReview
			if err := s.loanRepo.Update(txCtx, loan); err != nil {
				return err
			}
		default:
			loan.Status = entities.LoanStatusDeclined
			if err := s.loanRepo.Update(txCtx, loan); err != nil {
				return err
			}
		}
		out = loan
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Disburse moves approved → disbursed, anchors maturity + schedule at funding.
// ACID: serializable + retried; serialized per-loan via advisory xact lock so
// concurrent disburse calls cannot double-create the schedule.
func (s *LoanService) Disburse(ctx context.Context, loanID uuid.UUID) (*entities.Loan, error) {
	txCtx, cancel := repositories.WithTxTimeout(ctx)
	defer cancel()
	var out *entities.Loan
	err := repositories.Transact(txCtx, s.db, func(txCtx context.Context) error {
		if err := repositories.LockTx(txCtx, repositories.LoanLockKey(loanID)); err != nil {
			return err
		}
		loan, err := s.loanRepo.GetByID(txCtx, loanID)
		if err != nil {
			return err
		}
		if loan.Status != entities.LoanStatusApproved {
			return errors.New("only approved loans can be disbursed")
		}
		// Idempotent: schedule already exists (prior attempt committed).
		if existing, err := s.repaymentRepo.GetByLoanID(txCtx, loanID); err != nil {
			return err
		} else if len(existing) > 0 {
			out = loan
			return nil
		}
		now := time.Now().UTC()
		loan.Status = entities.LoanStatusDisbursed
		loan.DisbursementDate = &now
		loan.MaturityDate = now.AddDate(0, loan.TermMonths, 0)
		loan.UpdatedAt = now

		monthly, total, interest := calculateLoanTerms(loan.Amount, loan.InterestRate, loan.TermMonths)
		loan.MonthlyPayment, loan.TotalPayment, loan.TotalInterest = monthly, total, interest
		loan.EffectiveAPR = effectiveAnnualRate(loan.InterestRate)

		if err := s.loanRepo.Update(txCtx, loan); err != nil {
			return err
		}
		if err := s.createSchedule(txCtx, loan, now); err != nil {
			return err
		}
		if err := s.reconcileTotals(txCtx, loan); err != nil {
			return err
		}
		out = loan
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// reconcileTotals sets TotalPayment/TotalInterest to the SUM of schedule rows.
func (s *LoanService) reconcileTotals(ctx context.Context, loan *entities.Loan) error {
	rows, err := s.repaymentRepo.GetByLoanID(ctx, loan.ID)
	if err != nil {
		return err
	}
	total := decimal.Zero
	for _, p := range rows {
		total = total.Add(p.Amount)
	}
	loan.TotalPayment = total.Round(2)
	loan.TotalInterest = total.Sub(loan.Amount).Round(2)
	return s.loanRepo.Update(ctx, loan)
}

// effectiveAnnualRate converts nominal APR → effective annual (1+r)^12-1.
func effectiveAnnualRate(nominalAPR decimal.Decimal) decimal.Decimal {
	if nominalAPR.IsZero() {
		return decimal.Zero
	}
	monthly := nominalAPR.Div(decimal.NewFromInt(1200))
	one := decimal.NewFromInt(1)
	acc := decimal.NewFromInt(1)
	for i := 0; i < 12; i++ {
		acc = acc.Mul(one.Add(monthly))
	}
	return acc.Sub(one).Mul(decimal.NewFromInt(100)).Round(4)
}

func validateLoanRequest(req *LoanApplicationRequest) error {
	if req.Amount.LessThan(decimal.NewFromInt(1000)) {
		return errors.New("minimum loan amount is 1,000")
	}
	if req.Amount.GreaterThan(decimal.NewFromInt(10_000_000)) {
		return errors.New("maximum loan amount is 10,000,000")
	}
	if req.TermMonths < 1 || req.TermMonths > 60 {
		return errors.New("loan term must be between 1 and 60 months")
	}
	if len(req.Purpose) > 500 {
		return errors.New("purpose too long (max 500 characters)")
	}
	if req.MonthlyIncome.IsNegative() || req.MonthlyExpenses.IsNegative() {
		return errors.New("income and expenses must be non-negative")
	}
	if !req.MonthlyIncome.IsZero() && req.MonthlyExpenses.GreaterThan(req.MonthlyIncome.Mul(decimal.NewFromInt(10))) {
		return errors.New("expenses implausible relative to income")
	}
	return nil
}

// calculateRisk uses PERCENT scales: CreditUtilization 0-100, PaymentHistory 0-100.
func calculateRisk(cr *entities.CreditReport, req *LoanApplicationRequest) (int, string) {
	score := 700
	decision := entities.CreditApproved

	if cr.Score < 500 {
		score -= 100
		decision = entities.CreditDeclined
	} else if cr.Score < 600 {
		score -= 70
		decision = entities.CreditPendingReview
	} else if cr.Score < 700 {
		score -= 40
		if decision == entities.CreditApproved {
			decision = entities.CreditPendingReview
		}
	}

	if req.MonthlyIncome.GreaterThan(decimal.Zero) {
		dti := req.MonthlyExpenses.Div(req.MonthlyIncome).Mul(decimal.NewFromInt(100))
		if dti.GreaterThan(decimal.NewFromInt(40)) {
			score -= 50
			if decision == entities.CreditApproved {
				decision = entities.CreditPendingReview
			}
		}
		if dti.GreaterThan(decimal.NewFromInt(70)) {
			score -= 40
			decision = entities.CreditDeclined
		}
	}

	if req.AnnualIncome.GreaterThan(decimal.Zero) {
		ratio := req.Amount.Div(req.AnnualIncome).Mul(decimal.NewFromInt(100))
		if ratio.GreaterThan(decimal.NewFromInt(50)) {
			score -= 30
		}
		if ratio.GreaterThan(decimal.NewFromInt(80)) {
			score -= 40
			decision = entities.CreditDeclined
		}
	}

	if cr.CreditUtilization.GreaterThan(decimal.NewFromInt(30)) {
		score -= 20
	}
	if cr.CreditUtilization.GreaterThan(decimal.NewFromInt(50)) {
		score -= 30
	}

	if cr.PaymentHistory.LessThan(decimal.NewFromInt(95)) {
		score -= 30
		if cr.PaymentHistory.LessThan(decimal.NewFromInt(80)) {
			score -= 40
			decision = entities.CreditDeclined
		}
	}

	if cr.DelinquentAccounts > 2 {
		score -= 50
		decision = entities.CreditDeclined
	}
	if cr.HardInquiries > 5 {
		score -= 20
		if decision == entities.CreditApproved {
			decision = entities.CreditPendingReview
		}
	}

	if score < 300 {
		score = 300
	}
	if score > 850 {
		score = 850
	}
	return score, decision
}

// calculateLoanTerms uses exact decimal arithmetic and rounds half-up to 2dp.
func calculateLoanTerms(amount, annualRate decimal.Decimal, months int) (monthly, total, interest decimal.Decimal) {
	if months <= 0 {
		return decimal.Zero, decimal.Zero, decimal.Zero
	}
	if annualRate.IsZero() {
		monthly = amount.Div(decimal.NewFromInt(int64(months))).Round(2)
	} else {
		monthlyRate := annualRate.Div(decimal.NewFromInt(1200)) // r/12
		onePlusR := decimal.NewFromInt(1).Add(monthlyRate)
		pow := powDec(onePlusR, months)
		numerator := amount.Mul(monthlyRate).Mul(pow)
		denominator := pow.Sub(decimal.NewFromInt(1))
		monthly = numerator.Div(denominator).Round(2)
	}
	total = monthly.Mul(decimal.NewFromInt(int64(months)))
	interest = total.Sub(amount)
	return
}

func powDec(base decimal.Decimal, exp int) decimal.Decimal {
	r := decimal.NewFromInt(1)
	for i := 0; i < exp; i++ {
		r = r.Mul(base)
	}
	return r
}

func (s *LoanService) createSchedule(ctx context.Context, loan *entities.Loan, start time.Time) error {
	balance := loan.Amount
	monthlyRate := loan.InterestRate.Div(decimal.NewFromInt(1200))

	for i := 0; i < loan.TermMonths; i++ {
		interestAmt := balance.Mul(monthlyRate).Round(2)
		principalAmt := loan.MonthlyPayment.Sub(interestAmt)
		if i == loan.TermMonths-1 {
			// last installment absorbs rounding residue
			principalAmt = balance
			interestAmt = loan.MonthlyPayment.Sub(principalAmt)
			if interestAmt.IsNegative() {
				interestAmt = decimal.Zero
			}
		}

		p := &entities.Repayment{
			ID:              uuid.New(),
			LoanID:          loan.ID,
			Amount:          principalAmt.Add(interestAmt),
			PrincipalAmount: principalAmt,
			InterestAmount:  interestAmt,
			DueDate:         start.AddDate(0, i+1, 0),
			Status:          entities.RepaymentStatusPending,
			LateFee:         decimal.Zero,
			CreatedAt:       start,
			UpdatedAt:       start,
		}
		if err := s.repaymentRepo.Create(ctx, p); err != nil {
			return err
		}
		balance = balance.Sub(principalAmt)
	}
	return nil
}

func (s *LoanService) GetUserLoans(ctx context.Context, userID uuid.UUID, page, pageSize int) ([]*entities.Loan, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	return s.loanRepo.GetUserLoans(ctx, userID, pageSize, (page-1)*pageSize)
}

func (s *LoanService) GetLoanDetails(ctx context.Context, loanID uuid.UUID) (*entities.Loan, error) {
	return s.loanRepo.GetByID(ctx, loanID)
}

func (s *LoanService) ListAll(ctx context.Context, status string, page, pageSize int) ([]*entities.Loan, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	if status != "" {
		valid := map[string]bool{
			"pending": true, "under_review": true, "approved": true,
			"disbursed": true, "repaid": true, "defaulted": true, "declined": true,
		}
		if !valid[status] {
			return nil, 0, errors.New("invalid status filter")
		}
	}
	return s.loanRepo.List(ctx, status, pageSize, (page-1)*pageSize)
}

// UpdateStatus enforces the lifecycle state machine.
func (s *LoanService) UpdateStatus(ctx context.Context, loanID uuid.UUID, status entities.LoanStatus) error {
	loan, err := s.loanRepo.GetByID(ctx, loanID)
	if err != nil {
		return err
	}
	if loan.Status == status {
		return nil
	}
	if !entities.CanTransitionLoan(loan.Status, status) {
		return errors.New("invalid loan status transition")
	}
	if status == entities.LoanStatusDisbursed {
		_, err = s.Disburse(ctx, loanID)
		return err
	}
	return s.loanRepo.UpdateStatusConditional(ctx, loanID, loan.Status, status)
}
