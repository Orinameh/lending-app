package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type RepaymentStatus string

const (
	RepaymentStatusPending RepaymentStatus = "pending"
	RepaymentStatusPaid    RepaymentStatus = "paid"
	RepaymentStatusOverdue RepaymentStatus = "overdue"
	RepaymentStatusPartial RepaymentStatus = "partial"
)

type Repayment struct {
	ID              uuid.UUID       `json:"id"`
	LoanID          uuid.UUID       `json:"loanId"`
	Amount          decimal.Decimal `json:"amount"`
	PrincipalAmount decimal.Decimal `json:"principalAmount"`
	InterestAmount  decimal.Decimal `json:"interestAmount"`
	AmountPaid      decimal.Decimal `json:"amountPaid"`
	PrincipalPaid   decimal.Decimal `json:"principalPaid"`
	InterestPaid    decimal.Decimal `json:"interestPaid"`
	FeePaid         decimal.Decimal `json:"feePaid"`
	PaymentDate     *time.Time      `json:"paymentDate,omitempty"`
	DueDate         time.Time       `json:"dueDate"`
	Status          RepaymentStatus `json:"status"`
	PaymentMethod   string          `json:"paymentMethod,omitempty"`
	TransactionID   string          `json:"transactionId,omitempty"`
	LateFee         decimal.Decimal `json:"lateFee"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

// BalanceDue returns amount + lateFee - amountPaid (never negative).
func (p *Repayment) BalanceDue() decimal.Decimal {
	due := p.Amount.Add(p.LateFee).Sub(p.AmountPaid)
	if due.IsNegative() {
		return decimal.Zero
	}
	return due
}

type PaymentStatus string

const (
	PaymentStatusPending PaymentStatus = "pending"
	PaymentStatusApplied PaymentStatus = "applied"
	PaymentStatusFailed  PaymentStatus = "failed"
)

// Payment is one payment attempt against a loan. transaction_id is unique
// per PAYMENT (not per installment row — one payment may span rows).
type Payment struct {
	ID            uuid.UUID       `json:"id"`
	LoanID        uuid.UUID       `json:"loanId"`
	UserID        uuid.UUID       `json:"userId"`
	Amount        decimal.Decimal `json:"amount"`
	Applied       decimal.Decimal `json:"applied"`
	Overpayment   decimal.Decimal `json:"overpayment"`
	PaymentMethod string          `json:"paymentMethod"`
	TransactionID string          `json:"transactionId"`
	Status        PaymentStatus   `json:"status"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}
