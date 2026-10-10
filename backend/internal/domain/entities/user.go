package entities

import (
	"database/sql/driver"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// EncryptedString is a database field that stores PII encrypted at rest.
// The stored value in the DB is base64(AES-GCM-ciphertext).
// Application-layer code accesses via .Data (ciphertext) or .Plain (cleartext).
type EncryptedString struct {
	// Data holds the ciphertext as stored/read from the DB.
	Data string
	// Plain holds decrypted plaintext (populated by repository layer).
	Plain string
}

// Scan implements sql.Scanner
func (e *EncryptedString) Scan(value interface{}) error {
	switch v := value.(type) {
	case nil:
		e.Data = ""
	case string:
		e.Data = v
	case []byte:
		e.Data = string(v)
	}
	return nil
}

// Value implements driver.Valuer
func (e EncryptedString) Value() (driver.Value, error) {
	if e.Data == "" {
		return nil, nil
	}
	return e.Data, nil
}

func (e EncryptedString) String() string { return e.Plain }

type User struct {
	ID             uuid.UUID       `json:"id"`
	Email          EncryptedString `json:"-"`
	PasswordHash   string          `json:"-"`
	FirstName      EncryptedString `json:"-"`
	LastName       EncryptedString `json:"-"`
	Phone          EncryptedString `json:"-"`
	BVN            EncryptedString `json:"-"`
	NIN            EncryptedString `json:"-"`
	Address        EncryptedString `json:"-"`
	City           EncryptedString `json:"-"`
	State          EncryptedString `json:"-"`
	DateOfBirth    time.Time       `json:"dateOfBirth"`
	Country        string          `json:"country"`
	Currency       string          `json:"currency"`
	EmploymentType string          `json:"employmentType"`
	AnnualIncome   decimal.Decimal `json:"annualIncome"`
	Role           string          `json:"role"`
	EmailVerified  bool            `json:"emailVerified"`
	EmailVerifiedAt *time.Time     `json:"emailVerifiedAt,omitempty"`
	PhoneVerified  bool            `json:"phoneVerified"`
	PhoneVerifiedAt *time.Time     `json:"phoneVerifiedAt,omitempty"`
	KYCStatus      string          `json:"kycStatus"`
	IsActive       bool            `json:"isActive"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	DeletedAt      *time.Time      `json:"deletedAt,omitempty"`
}

// Public profile fields safe to return to the owning user (no ciphertext).
type UserProfile struct {
	ID             uuid.UUID `json:"id"`
	Email          string    `json:"email"`
	FirstName      string    `json:"firstName"`
	LastName       string    `json:"lastName"`
	Phone          string    `json:"phone"`
	DateOfBirth    time.Time `json:"dateOfBirth"`
	Country        string    `json:"country"`
	Currency       string    `json:"currency"`
	EmploymentType string    `json:"employmentType"`
	AnnualIncome   string    `json:"annualIncome"`
	Role           string    `json:"role"`
	EmailVerified  bool      `json:"emailVerified"`
	EmailVerifiedAt *time.Time `json:"emailVerifiedAt,omitempty"`
	PhoneVerified  bool      `json:"phoneVerified"`
	PhoneVerifiedAt *time.Time `json:"phoneVerifiedAt,omitempty"`
	KYCStatus      string    `json:"kycStatus"`
	IsActive       bool      `json:"isActive"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func (u *User) Profile() UserProfile {
	return UserProfile{
		ID: u.ID, Email: u.Email.Plain,
		FirstName: u.FirstName.Plain, LastName: u.LastName.Plain,
		Phone: u.Phone.Plain, DateOfBirth: u.DateOfBirth,
		Country: u.Country, Currency: u.Currency,
		EmploymentType: u.EmploymentType, AnnualIncome: u.AnnualIncome.String(),
	Role: u.Role, EmailVerified: u.EmailVerified, PhoneVerified: u.PhoneVerified,
		EmailVerifiedAt: u.EmailVerifiedAt, PhoneVerifiedAt: u.PhoneVerifiedAt,
		KYCStatus: u.KYCStatus, IsActive: u.IsActive,
		CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
	}
}

var ValidRoles = map[string]bool{"user": true, "admin": true, "agent": true}

var ValidEmploymentTypes = map[string]bool{
	"employed": true, "self_employed": true, "unemployed": true,
	"student": true, "retired": true, "other": true,
}

var ValidKYCStatuses = map[string]bool{
	"pending": true, "submitted": true, "verified": true,
	"rejected": true, "expired": true,
}
