package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"lending-app/backend/internal/repositories"
	"lending-app/backend/pkg/crypto"
	"math/big"
	"time"

	"github.com/google/uuid"
)

var (
	ErrVerificationExpired = errors.New("verification expired or invalid")
	ErrOTPAttemptsSpent    = errors.New("too many attempts, request a new code")
	ErrOTPResendLimit      = errors.New("too many codes requested, try again later")
	ErrAlreadyVerified     = errors.New("already verified")
)

// VerificationService owns email tokens and phone OTPs. Only hashes are
// stored; raw values exist in transit only (and, in non-production, are
// returned once so devs can complete the flow without an SMS/email vendor).
//
// Hash strength is matched to secret strength: email tokens carry 256 bits
// (plain SHA-256 is fine); 6-digit OTPs are HMACed under a server secret so
// a DB read alone cannot offline-crack live codes within their 10-min TTL.
type VerificationService struct {
	repo *repositories.VerificationRepository
	user *repositories.UserRepository
	enc  *crypto.EncryptionService
}

func NewVerificationService(repo *repositories.VerificationRepository, user *repositories.UserRepository, enc *crypto.EncryptionService) *VerificationService {
	return &VerificationService{repo: repo, user: user, enc: enc}
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *VerificationService) otpHash(code string) string {
	return s.enc.HMAC("otp-v1:" + code)
}

// RequestEmailVerification issues a single-use 24h token. Previous unused
// tokens remain valid until expiry (harmless); the newest is communicated.
func (s *VerificationService) RequestEmailVerification(ctx context.Context, userID uuid.UUID) (rawToken string, err error) {
	u, err := s.user.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if u.EmailVerified {
		return "", ErrAlreadyVerified
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	rawToken = base64.RawURLEncoding.EncodeToString(b[:])
	if err := s.repo.CreateEmailToken(ctx, uuid.New(), userID, hashToken(rawToken), time.Now().Add(24*time.Hour)); err != nil {
		return "", err
	}
	return rawToken, nil
}

func (s *VerificationService) VerifyEmail(ctx context.Context, rawToken string) (uuid.UUID, error) {
	if rawToken == "" {
		return uuid.Nil, ErrVerificationExpired
	}
	rec, err := s.repo.GetValidEmailToken(ctx, hashToken(rawToken), time.Now())
	if err != nil {
		return uuid.Nil, ErrVerificationExpired
	}
	if err := s.repo.MarkEmailUsed(ctx, rec.ID); err != nil {
		// Lost the consume race (or token used between read and mark).
		return uuid.Nil, ErrVerificationExpired
	}
	if err := s.user.SetEmailVerified(ctx, rec.UserID, true); err != nil {
		return uuid.Nil, err
	}
	return rec.UserID, nil
}

// RequestPhoneOTP issues a 6-digit code (10 min TTL, 5 attempts). Resends
// are capped at 5/hour (SMS costs money and enables harassment).
func (s *VerificationService) RequestPhoneOTP(ctx context.Context, userID uuid.UUID) (code string, err error) {
	u, err := s.user.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if u.PhoneVerified {
		return "", ErrAlreadyVerified
	}
	n, err := s.repo.RecentOTPCount(ctx, userID, time.Now().Add(-1*time.Hour))
	if err != nil {
		return "", err
	}
	if n >= 5 {
		return "", ErrOTPResendLimit
	}
	// 6-digit code in [100000, 999999] from crypto/rand.
	dr, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return "", err
	}
	code = fmt.Sprintf("%d", 100000+dr.Int64())
	if err := s.repo.CreatePhoneOTP(ctx, uuid.New(), userID, s.otpHash(code), time.Now().Add(10*time.Minute)); err != nil {
		return "", err
	}
	return code, nil
}

func (s *VerificationService) VerifyPhoneOTP(ctx context.Context, userID uuid.UUID, code string) error {
	if len(code) != 6 {
		return ErrVerificationExpired
	}
	otp, err := s.repo.LatestValidOTP(ctx, userID, time.Now())
	if err != nil {
		return ErrVerificationExpired
	}
	if otp.Attempts >= otp.MaxAttempts {
		return ErrOTPAttemptsSpent
	}
	if !crypto.EqualHMAC(s.otpHash(code), otp.CodeHash) {
		_ = s.repo.BumpOTPAttempts(ctx, otp.ID)
		if otp.Attempts+1 >= otp.MaxAttempts {
			return ErrOTPAttemptsSpent
		}
		return ErrVerificationExpired
	}
	if err := s.repo.MarkOTPUsed(ctx, otp.ID); err != nil {
		return ErrVerificationExpired
	}
	return s.user.SetPhoneVerified(ctx, userID, true)
}
