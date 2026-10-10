package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"lending-app/backend/internal/domain/entities"
	"lending-app/backend/internal/identity"
	"lending-app/backend/internal/repositories"
	"lending-app/backend/pkg/crypto"
	"strings"
	"time"

	"github.com/google/uuid"
)

// KYCService orchestrates document submission → provider check → admin-ready
// state. Provider match moves the document to verified and the user to
// submitted (awaiting human approval); mismatch rejects both with a reason
// the admin can override. Swap the provider in main.go for production.
type KYCService struct {
	kyc  *repositories.KYCRepository
	user *repositories.UserRepository
	prov identity.Provider
	enc  *crypto.EncryptionService
}

func NewKYCService(kyc *repositories.KYCRepository, user *repositories.UserRepository, prov identity.Provider, enc *crypto.EncryptionService) *KYCService {
	return &KYCService{kyc: kyc, user: user, prov: prov, enc: enc}
}

// Sentinel errors: the handler maps these to safe client messages.
// Anything else is infrastructure failure → generic 500.
var (
	ErrKYCValidation = errors.New("kyc validation failed")
	ErrKYCMismatch   = errors.New("identity mismatch")
	ErrKYCConflict   = errors.New("kyc conflict")
	ErrKYCProvider   = errors.New("verification provider unavailable")
)

type KYCSubmitRequest struct {
	DocumentType     string `json:"documentType"`
	DocumentNumber   string `json:"documentNumber"`
	IssuingAuthority string `json:"issuingAuthority"`
	ExpiryDate       string `json:"expiryDate"`
	FilePath         string `json:"filePath"`
}

func (s *KYCService) Submit(ctx context.Context, userID uuid.UUID, req *KYCSubmitRequest) (*entities.KYCDocument, error) {
	docType := strings.TrimSpace(req.DocumentType)
	if !entities.ValidKYCDocumentTypes[docType] {
		return nil, fmt.Errorf("%w: invalid document type", ErrKYCValidation)
	}
	number := strings.TrimSpace(req.DocumentNumber)
	if len(number) == 0 || len(number) > 64 {
		return nil, fmt.Errorf("%w: document number required (max 64 characters)", ErrKYCValidation)
	}
	if docType == "bvn" && !validBVNShape(number) {
		return nil, fmt.Errorf("%w: invalid BVN (must be 11 digits)", ErrKYCValidation)
	}
	if docType == "nin" && !validNINShape(number) {
		return nil, fmt.Errorf("%w: invalid NIN (must be 11 digits)", ErrKYCValidation)
	}
	if len(req.IssuingAuthority) > 120 {
		return nil, fmt.Errorf("%w: issuing authority too long", ErrKYCValidation)
	}
	if len(req.FilePath) > 500 {
		return nil, fmt.Errorf("%w: file path too long", ErrKYCValidation)
	}
	var expiry *time.Time
	if strings.TrimSpace(req.ExpiryDate) != "" {
		t, err := time.Parse("2006-01-02", strings.TrimSpace(req.ExpiryDate))
		if err != nil {
			return nil, fmt.Errorf("%w: invalid expiry date (use YYYY-MM-DD)", ErrKYCValidation)
		}
		expiry = &t
	}

	user, err := s.user.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	// One active review per type: resubmission allowed after reject/expire.
	if existing, err := s.kyc.GetByUserAndType(ctx, userID, docType); err == nil {
		if existing.Status == "pending" || existing.Status == "submitted" {
			return nil, fmt.Errorf("%w: a review for this document type is already in progress", ErrKYCConflict)
		}
	} else if !errors.Is(err, repositories.ErrNotFound) {
		return nil, err
	}

	// Cross-account reuse: the same national ID on two accounts is a fraud
	// signal — caught via the deterministic HMAC blind index (ciphertext
	// itself is randomized and cannot dedupe). Scoped to active reviews of
	// the same type; rejected/expired rows don't block resubmission.
	docHMAC := s.enc.HMAC(strings.ToLower(number))
	if dup, err := s.kyc.GetActiveByDocHMAC(ctx, docHMAC, docType); err == nil {
		if dup.UserID != userID {
			return nil, fmt.Errorf("%w: this document is already registered to another account", ErrKYCConflict)
		}
		return nil, fmt.Errorf("%w: this document is already verified on your account", ErrKYCConflict)
	} else if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return nil, err
	}

	encNumber, err := s.enc.Encrypt(number)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	doc := &entities.KYCDocument{
		ID:               uuid.New(),
		UserID:           userID,
		DocumentType:     docType,
		DocumentNumber:   encNumber,
		DocumentHMAC:     docHMAC,
		IssuingAuthority: strings.TrimSpace(req.IssuingAuthority),
		ExpiryDate:       expiry,
		FilePath:         strings.TrimSpace(req.FilePath),
		Status:           "submitted",
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// Registry check for national IDs; other documents go straight to
	// human review (submitted).
	if docType == "bvn" || docType == "nin" {
		res, err := s.verifyRegistry(ctx, docType, number, user)
		if err != nil {
			return nil, fmt.Errorf("%w, try again", ErrKYCProvider)
		}
		raw, _ := json.Marshal(res)
		doc.ProviderRef = res.Ref
		doc.ProviderResponse = raw
		if !res.Match {
			doc.Status = "rejected"
			doc.RejectionReason = res.Reason
			if err := s.kyc.Create(ctx, doc); err != nil {
				return nil, err
			}
			_ = s.user.UpdateKYCStatus(ctx, userID, "rejected")
			doc.DocumentNumber = number // respond with plaintext, store ciphertext
			return doc, fmt.Errorf("%w: %s", ErrKYCMismatch, res.Reason)
		}
	}
	if err := s.kyc.Create(ctx, doc); err != nil {
		return nil, err
	}
	doc.DocumentNumber = number // respond with plaintext, store ciphertext
	// Verified document (or non-registry doc) → ready for admin approval.
	if user.KYCStatus == "pending" || user.KYCStatus == "rejected" || user.KYCStatus == "expired" {
		_ = s.user.UpdateKYCStatus(ctx, userID, "submitted")
	}
	return doc, nil
}

// verifyRegistry calls the provider with a bounded timeout and records
// whether the submitted number matches the user's profile number.
func (s *KYCService) verifyRegistry(ctx context.Context, docType, number string, user *entities.User) (identity.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	first, last := user.FirstName.Plain, user.LastName.Plain
	var res identity.Result
	var err error
	if docType == "bvn" {
		res, err = s.prov.VerifyBVN(ctx, number, first, last)
	} else {
		res, err = s.prov.VerifyNIN(ctx, number, first, last)
	}
	if err != nil {
		return res, errors.New("verification provider unavailable, try again")
	}
	profileNum := user.BVN.Plain
	if docType == "nin" {
		profileNum = user.NIN.Plain
	}
	res.ProfileMatch = profileNum == "" || profileNum == number
	return res, nil
}

func validBVNShape(s string) bool {
	if len(s) != 11 {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func validNINShape(s string) bool { return validBVNShape(s) }

func (s *KYCService) ListMine(ctx context.Context, userID uuid.UUID) ([]*entities.KYCDocument, error) {
	docs, err := s.kyc.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Decrypt numbers for the owner; fail loud on tamper (never blank).
	for _, d := range docs {
		if d.DocumentNumber == "" {
			continue
		}
		plain, err := s.enc.Decrypt(d.DocumentNumber)
		if err != nil {
			return nil, err
		}
		d.DocumentNumber = plain
	}
	return docs, nil
}
