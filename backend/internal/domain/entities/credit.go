package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type CreditReport struct {
	ID                 uuid.UUID       `json:"id"`
	UserID             uuid.UUID       `json:"userId"`
	Score              int             `json:"score"`
	Rating             string          `json:"rating"`
	TotalDebt          decimal.Decimal `json:"totalDebt"`
	AvailableCredit    decimal.Decimal `json:"availableCredit"`
	CreditUtilization  decimal.Decimal `json:"creditUtilization"`
	PaymentHistory     decimal.Decimal `json:"paymentHistory"`
	CreditAgeMonths    int             `json:"creditAgeMonths"`
	NumAccounts        int             `json:"numAccounts"`
	HardInquiries      int             `json:"hardInquiries"`
	DelinquentAccounts int             `json:"delinquentAccounts"`
	ReportDate         time.Time       `json:"reportDate"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
}
