package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type LoanStatus string

const (
	LoanStatusPending     LoanStatus = "pending"
	LoanStatusUnderReview LoanStatus = "under_review"
	LoanStatusApproved    LoanStatus = "approved"
	LoanStatusDisbursed   LoanStatus = "disbursed"
	LoanStatusRepaid      LoanStatus = "repaid"
	LoanStatusDefaulted   LoanStatus = "defaulted"
	LoanStatusDeclined    LoanStatus = "declined"
)

type Loan struct {
	ID               uuid.UUID       `json:"id"`
	UserID           uuid.UUID       `json:"userId"`
	Amount           decimal.Decimal `json:"amount"`
	InterestRate     decimal.Decimal `json:"interestRate"` // nominal % per annum
	EffectiveAPR     decimal.Decimal `json:"effectiveApr"` // fee-inclusive % per annum (disclosure)
	TermMonths       int             `json:"termMonths"`
	Purpose          string          `json:"purpose"`
	Status           LoanStatus      `json:"status"`
	Currency         string          `json:"currency"`
	IdempotencyKey   string          `json:"-"`
	ApplicationDate  time.Time       `json:"applicationDate"`
	ApprovalDate     *time.Time      `json:"approvalDate,omitempty"`
	DisbursementDate *time.Time      `json:"disbursementDate,omitempty"`
	MaturityDate     time.Time       `json:"maturityDate"`
	MonthlyPayment   decimal.Decimal `json:"monthlyPayment"`
	TotalPayment     decimal.Decimal `json:"totalPayment"`
	TotalInterest    decimal.Decimal `json:"totalInterest"`
	RiskScore        int             `json:"riskScore"`
	CreditDecision   string          `json:"creditDecision"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
	DeletedAt        *time.Time      `json:"deletedAt,omitempty"`
}

// Credit decisions are distinct from loan lifecycle states.
const (
	CreditApproved      = "approved"
	CreditPendingReview = "pending_review"
	CreditDeclined      = "declined"
)

// ValidLoanTransitions enforces the lifecycle state machine.
var ValidLoanTransitions = map[LoanStatus][]LoanStatus{
	LoanStatusPending:     {LoanStatusUnderReview, LoanStatusApproved, LoanStatusDeclined},
	LoanStatusUnderReview: {LoanStatusApproved, LoanStatusDeclined},
	LoanStatusApproved:    {LoanStatusDisbursed, LoanStatusDeclined},
	LoanStatusDisbursed:   {LoanStatusRepaid, LoanStatusDefaulted},
	LoanStatusRepaid:      {},
	LoanStatusDefaulted:   {LoanStatusRepaid},
	LoanStatusDeclined:    {},
}

func CanTransitionLoan(from, to LoanStatus) bool {
	for _, s := range ValidLoanTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}
