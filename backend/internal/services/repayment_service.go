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

var (
	lateFeeRate = decimal.NewFromFloat(0.05)
	lateFeeCap  = decimal.NewFromInt(5000)
)

type RepaymentService struct {
	db            *sql.DB
	repaymentRepo *repositories.RepaymentRepository
	loanRepo      *repositories.LoanRepository
}

func NewRepaymentService(db *sql.DB, rr *repositories.RepaymentRepository, lr *repositories.LoanRepository) *RepaymentService {
	return &RepaymentService{db: db, repaymentRepo: rr, loanRepo: lr}
}

type RepaymentRequest struct {
	Amount        decimal.Decimal `json:"amount"`
	PaymentMethod string          `json:"paymentMethod"`
	TransactionID string          `json:"transactionId"`
}

type RepaymentResult struct {
	Applied      decimal.Decimal `json:"applied"`
	Overpayment  decimal.Decimal `json:"overpayment"`
	Installments int             `json:"installments"`
}

func (s *RepaymentService) MakeRepayment(ctx context.Context, userID, loanID uuid.UUID, req *RepaymentRequest) (*RepaymentResult, error) {
	if req.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, errors.New("amount must be greater than zero")
	}
	if strings.TrimSpace(req.PaymentMethod) == "" {
		return nil, errors.New("payment method is required")
	}
	if len(req.PaymentMethod) > 50 {
		return nil, errors.New("payment method too long")
	}
	txn := strings.TrimSpace(req.TransactionID)
	if txn == "" {
		return nil, errors.New("transaction id is required for idempotency")
	}
	if len(txn) > 100 {
		return nil, errors.New("transaction id too long")
	}

	loan, err := s.loanRepo.GetByID(ctx, loanID)
	if err != nil {
		return nil, err
	}
	if loan.UserID != userID {
		return nil, errors.New("forbidden")
	}
	if loan.Status == entities.LoanStatusRepaid {
		return nil, errors.New("loan already fully repaid")
	}
	if loan.Status != entities.LoanStatusDisbursed && loan.Status != entities.LoanStatusApproved {
		return nil, errors.New("loan is not in a repayable state")
	}

	// Idempotency: same transaction id must never double-apply.
	if exists, err := s.repaymentRepo.ExistsTransaction(ctx, txn); err != nil {
		return nil, err
	} else if exists {
		return nil, errors.New("duplicate transaction id")
	}

	// ACID + concurrency: serializable tx with retry on 40001/40P01,
	// serialized per-loan via advisory xact lock + SELECT ... FOR UPDATE.
	txCtx, cancel := repositories.WithTxTimeout(ctx)
	defer cancel()
	var result *RepaymentResult
	err = repositories.Transact(txCtx, s.db, func(txCtx context.Context) error {
		if err := repositories.LockTx(txCtx, repositories.LoanLockKey(loanID)); err != nil {
			return err
		}
		// Re-check idempotency INSIDE the tx (TOCTOU safe).
		if exists, err := s.repaymentRepo.ExistsTransaction(txCtx, txn); err != nil {
			return err
		} else if exists {
			return errors.New("duplicate transaction id")
		}
		payments, err := s.repaymentRepo.GetByLoanIDForUpdate(txCtx, loanID)
		if err != nil {
			return err
		}

		remaining := req.Amount
		applied := decimal.Zero
		touched := 0
		now := time.Now().UTC()

		for _, p := range payments {
			if remaining.IsZero() || remaining.IsNegative() {
				break
			}
			if p.Status == entities.RepaymentStatusPaid {
				continue
			}
			due := p.BalanceDue()
			if due.IsZero() {
				continue
			}
			pay := remaining
			if pay.GreaterThan(due) {
				pay = due
			}
			// Allocation order: late fee → interest → principal.
			feeDue := p.LateFee.Sub(p.FeePaid)
			if feeDue.IsNegative() {
				feeDue = decimal.Zero
			}
			feePay := decimal.Min(pay, feeDue)
			p.FeePaid = p.FeePaid.Add(feePay)
			pay = pay.Sub(feePay)

			intDue := p.InterestAmount.Sub(p.InterestPaid)
			if intDue.IsNegative() {
				intDue = decimal.Zero
			}
			intPay := decimal.Min(pay, intDue)
			p.InterestPaid = p.InterestPaid.Add(intPay)
			pay = pay.Sub(intPay)

			princDue := p.PrincipalAmount.Sub(p.PrincipalPaid)
			if princDue.IsNegative() {
				princDue = decimal.Zero
			}
			princPay := decimal.Min(pay, princDue)
			p.PrincipalPaid = p.PrincipalPaid.Add(princPay)
			pay = pay.Sub(princPay)

			consumed := feePay.Add(intPay).Add(princPay)
			p.AmountPaid = p.AmountPaid.Add(consumed)
			remaining = remaining.Sub(consumed)
			applied = applied.Add(consumed)

			if p.BalanceDue().IsZero() {
				p.Status = entities.RepaymentStatusPaid
			} else {
				p.Status = entities.RepaymentStatusPartial
			}
			p.PaymentDate = &now
			p.PaymentMethod = strings.TrimSpace(req.PaymentMethod)
			p.TransactionID = txn
			if err := s.repaymentRepo.Update(txCtx, p); err != nil {
				return err
			}
			touched++
		}

		if applied.IsZero() {
			return errors.New("no pending repayments to apply this amount to")
		}

		// Full payoff check (locked rows re-read).
		payments, err = s.repaymentRepo.GetByLoanID(txCtx, loanID)
		if err != nil {
			return err
		}
		allPaid := true
		for _, p := range payments {
			if p.Status != entities.RepaymentStatusPaid {
				allPaid = false
				break
			}
		}
		if allPaid {
			if err := s.loanRepo.UpdateStatus(txCtx, loanID, entities.LoanStatusRepaid); err != nil {
				return err
			}
		}
		result = &RepaymentResult{Applied: applied, Overpayment: remaining, Installments: touched}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// AccrueLateFees assesses a one-time 5% fee (capped at 5,000) on installments
// past due with no fee yet. Called by the collections scan.
func (s *RepaymentService) AccrueLateFees(ctx context.Context, loanID uuid.UUID, now time.Time) (int, error) {
	payments, err := s.repaymentRepo.GetByLoanID(ctx, loanID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range payments {
		if p.Status == entities.RepaymentStatusPaid {
			continue
		}
		if !p.DueDate.Before(now) {
			continue
		}
		if !p.LateFee.IsZero() {
			continue
		}
		fee := p.Amount.Mul(lateFeeRate).Round(2)
		if fee.GreaterThan(lateFeeCap) {
			fee = lateFeeCap
		}
		p.LateFee = fee
		if p.Status == entities.RepaymentStatusPending {
			p.Status = entities.RepaymentStatusOverdue
		}
		if err := s.repaymentRepo.Update(ctx, p); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (s *RepaymentService) GetSchedule(ctx context.Context, userID, loanID uuid.UUID) ([]*entities.Repayment, error) {
	loan, err := s.loanRepo.GetByID(ctx, loanID)
	if err != nil {
		return nil, err
	}
	if loan.UserID != userID {
		return nil, errors.New("forbidden")
	}
	return s.repaymentRepo.GetByLoanID(ctx, loanID)
}
