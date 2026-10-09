package entities

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type CollectionStatus string

const (
	CollectionStatusActive     CollectionStatus = "active"
	CollectionStatusResolved   CollectionStatus = "resolved"
	CollectionStatusLegal      CollectionStatus = "legal"
	CollectionStatusWrittenOff CollectionStatus = "written_off"
)

type Collection struct {
	ID                uuid.UUID        `json:"id"`
	LoanID            uuid.UUID        `json:"loanId"`
	UserID            uuid.UUID        `json:"userId"`
	AmountOutstanding decimal.Decimal  `json:"amountOutstanding"`
	DaysPastDue       int              `json:"daysPastDue"`
	Status            CollectionStatus `json:"status"`
	AssignedTo        *uuid.UUID       `json:"assignedTo,omitempty"`
	LastContactDate   *time.Time       `json:"lastContactDate,omitempty"`
	NextContactDate   *time.Time       `json:"nextContactDate,omitempty"`
	Notes             string           `json:"notes"`
	CreatedAt         time.Time        `json:"createdAt"`
	UpdatedAt         time.Time        `json:"updatedAt"`
}
