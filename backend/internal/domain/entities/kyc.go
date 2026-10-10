package entities

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type KYCDocument struct {
	ID               uuid.UUID       `json:"id"`
	UserID           uuid.UUID       `json:"userId"`
	DocumentType     string          `json:"documentType"`
	DocumentNumber   string          `json:"documentNumber,omitempty"`
	DocumentHMAC     string          `json:"-"`
	IssuingAuthority string          `json:"issuingAuthority,omitempty"`
	ExpiryDate       *time.Time      `json:"expiryDate,omitempty"`
	FilePath         string          `json:"filePath,omitempty"`
	Status           string          `json:"status"`
	RejectionReason  string          `json:"rejectionReason,omitempty"`
	VerifiedBy       *uuid.UUID      `json:"verifiedBy,omitempty"`
	VerifiedAt       *time.Time      `json:"verifiedAt,omitempty"`
	ProviderRef      string          `json:"providerRef,omitempty"`
	ProviderResponse json.RawMessage `json:"providerResponse,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

var ValidKYCDocumentTypes = map[string]bool{
	"bvn": true, "nin": true, "passport": true, "drivers_license": true,
	"voters_card": true, "utility_bill": true, "bank_statement": true, "other": true,
}

var ValidKYCDocumentStatuses = map[string]bool{
	"pending": true, "submitted": true, "verified": true,
	"rejected": true, "expired": true,
}
