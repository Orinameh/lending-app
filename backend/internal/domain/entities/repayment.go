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
