package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"lending-app/backend/internal/domain/entities"
	"lending-app/backend/internal/repositories"
	"lending-app/backend/pkg/auth"
	"lending-app/backend/pkg/validator"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountDisabled    = errors.New("account is disabled")
)

type AuthService struct {
	userRepo    *repositories.UserRepository
	refreshRepo *repositories.RefreshTokenRepository
	resetRepo   *repositories.PasswordResetRepository
	jwtService  *auth.JWTService
}

func NewAuthService(
	userRepo *repositories.UserRepository,
	refreshRepo *repositories.RefreshTokenRepository,
	resetRepo *repositories.PasswordResetRepository,
	jwt *auth.JWTService,
) *AuthService {
	return &AuthService{userRepo: userRepo, refreshRepo: refreshRepo, resetRepo: resetRepo, jwtService: jwt}
}

type RegisterRequest struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	FirstName      string `json:"firstName"`
	LastName       string `json:"lastName"`
	Phone          string `json:"phone"`
	DateOfBirth    string `json:"dateOfBirth"`
	BVN            string `json:"bvn"`
	NIN            string `json:"nin"`
	EmploymentType string `json:"employmentType"`
	AnnualIncome   string `json:"annualIncome"`
	Country        string `json:"country"`
	Currency       string `json:"currency"`
}

func (s *AuthService) Register(ctx context.Context, req *RegisterRequest) (*entities.User, error) {
	email := repositories.NormalizeEmail(req.Email)
	if !validator.ValidateEmail(email) {
		return nil, errors.New("invalid email format")
	}
	if !validator.ValidatePhone(strings.TrimSpace(req.Phone)) {
		return nil, errors.New("invalid phone format (use Nigerian format)")
	}
	if strings.TrimSpace(req.BVN) != "" && !validator.ValidateBVN(strings.TrimSpace(req.BVN)) {
		return nil, errors.New("invalid BVN (must be 11 digits)")
	}
	if strings.TrimSpace(req.NIN) != "" && !validator.ValidateNIN(strings.TrimSpace(req.NIN)) {
		return nil, errors.New("invalid NIN (must be 11 digits)")
	}
	if err := validator.ValidatePassword(req.Password); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.FirstName) == "" || strings.TrimSpace(req.LastName) == "" {
		return nil, errors.New("first and last name are required")
	}
	if len(req.FirstName) > 100 || len(req.LastName) > 100 {
		return nil, errors.New("name too long (max 100 characters)")
	}

	employment := strings.TrimSpace(req.EmploymentType)
	if employment == "" {
		employment = "other"
	}
	if !entities.ValidEmploymentTypes[employment] {
		return nil, errors.New("invalid employment type")
	}
	var annualIncome decimal.Decimal
	if strings.TrimSpace(req.AnnualIncome) != "" {
		d, err := decimal.NewFromString(strings.TrimSpace(req.AnnualIncome))
		if err != nil || d.IsNegative() {
			return nil, errors.New("invalid annual income")
		}
		if d.GreaterThan(decimal.NewFromInt(1_000_000_000)) {
			return nil, errors.New("annual income out of range")
		}
		annualIncome = d
	}

	dob, err := time.Parse("2006-01-02", strings.TrimSpace(req.DateOfBirth))
	if err != nil {
		return nil, errors.New("invalid date of birth (use YYYY-MM-DD)")
	}
	now := time.Now().UTC()
	age := now.Year() - dob.Year()
	if now.YearDay() < dob.YearDay() {
		age--
	}
	if dob.After(now) {
		return nil, errors.New("date of birth cannot be in the future")
	}
	if age < 18 {
		return nil, errors.New("must be at least 18 years old")
	}
	if age > 120 {
		return nil, errors.New("invalid date of birth")
	}

	// Generic path: probe then create. Duplicate emails get a generic error
	// surfaced by the handler to avoid enumeration (see handler).
	existing, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, repositories.ErrNotFound) {
		return nil, err
	}
	if existing != nil {
		return nil, errors.New("unable to complete registration")
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	country := strings.ToUpper(strings.TrimSpace(req.Country))
	if country == "" {
		country = "NG"
	}
	if len(country) != 2 {
		return nil, errors.New("invalid country code")
	}
	currency := strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency == "" {
		currency = "NGN"
	}
	if len(currency) != 3 {
		return nil, errors.New("invalid currency code")
	}

	user := &entities.User{
		ID:             uuid.New(),
		Email:          entities.EncryptedString{Plain: email},
		PasswordHash:   string(hashed),
		FirstName:      entities.EncryptedString{Plain: strings.TrimSpace(req.FirstName)},
		LastName:       entities.EncryptedString{Plain: strings.TrimSpace(req.LastName)},
		Phone:          entities.EncryptedString{Plain: strings.TrimSpace(req.Phone)},
		BVN:            entities.EncryptedString{Plain: strings.TrimSpace(req.BVN)},
		NIN:            entities.EncryptedString{Plain: strings.TrimSpace(req.NIN)},
		DateOfBirth:    dob.UTC(),
		Country:        country,
		Currency:       currency,
		EmploymentType: employment,
		AnnualIncome:   annualIncome,
		Role:           "user",
		KYCStatus:      "pending",
		IsActive:       true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token        string                `json:"token"`
	RefreshToken string                `json:"refreshToken"`
	ExpiresIn    int64                 `json:"expiresIn"`
	User         entities.UserProfile  `json:"user"`
}

func (s *AuthService) Login(ctx context.Context, req *LoginRequest) (*LoginResponse, error) {
	user, err := s.userRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		// Generic: do not reveal whether the email exists.
		return nil, ErrInvalidCredentials
	}
	if !user.IsActive {
		return nil, ErrAccountDisabled
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, ErrInvalidCredentials
	}

	token, err := s.jwtService.GenerateToken(user.ID, user.Role)
	if err != nil {
		return nil, err
	}
	jti, refresh, err := s.jwtService.GenerateRefreshToken(user.ID)
	if err != nil {
		return nil, err
	}
	if err := s.refreshRepo.Create(ctx, jti, user.ID, time.Now().Add(auth.RefreshTokenTTL)); err != nil {
		return nil, err
	}
	return &LoginResponse{Token: token, RefreshToken: refresh, ExpiresIn: int64(auth.AccessTokenTTL.Seconds()), User: user.Profile()}, nil
}

// GetUser — used by the auth handler's GetProfile
func (s *AuthService) GetUser(ctx context.Context, id uuid.UUID) (*entities.User, error) {
	return s.userRepo.GetByID(ctx, id)
}

func (s *AuthService) UpdateProfile(ctx context.Context, user *entities.User) error {
	user.UpdatedAt = time.Now().UTC()
	return s.userRepo.Update(ctx, user)
}

func (s *AuthService) ValidateToken(token string) (uuid.UUID, string, error) {
	claims, err := s.jwtService.ValidateToken(token)
	if err != nil {
		return uuid.Nil, "", err
	}
	return claims.UserID, claims.Role, nil
}

// ValidateTokenActive additionally enforces IsActive + current role from DB
// (prevents stale-role / disabled-account tokens living until exp).
func (s *AuthService) ValidateTokenActive(ctx context.Context, token string) (uuid.UUID, string, error) {
	claims, err := s.jwtService.ValidateToken(token)
	if err != nil {
		return uuid.Nil, "", err
	}
	user, err := s.userRepo.GetByID(ctx, claims.UserID)
	if err != nil {
		return uuid.Nil, "", errors.New("invalid token subject")
	}
	if !user.IsActive {
		return uuid.Nil, "", ErrAccountDisabled
	}
	return user.ID, user.Role, nil
}

func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string) (*LoginResponse, error) {
	userID, jti, err := s.jwtService.ValidateRefreshToken(refreshToken)
	if err != nil {
		return nil, errors.New("invalid refresh token")
	}
	stored, err := s.refreshRepo.Get(ctx, jti)
	if err != nil || stored.RevokedAt != nil || time.Now().After(stored.ExpiresAt) {
		return nil, errors.New("invalid refresh token")
	}
	if stored.UserID != userID {
		return nil, errors.New("invalid refresh token")
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, errors.New("invalid refresh token")
	}
	if !user.IsActive {
		return nil, ErrAccountDisabled
	}

	token, err := s.jwtService.GenerateToken(user.ID, user.Role)
	if err != nil {
		return nil, err
	}
	newJTI, newRefresh, err := s.jwtService.GenerateRefreshToken(user.ID)
	if err != nil {
		return nil, err
	}
	// Rotation: persist the replacement first, then revoke the old one.
	if err := s.refreshRepo.Create(ctx, newJTI, user.ID, time.Now().Add(auth.RefreshTokenTTL)); err != nil {
		return nil, err
	}
	if err := s.refreshRepo.Rotate(ctx, jti, newJTI); err != nil {
		return nil, err
	}
	return &LoginResponse{Token: token, RefreshToken: newRefresh, ExpiresIn: int64(auth.AccessTokenTTL.Seconds()), User: user.Profile()}, nil
}

// Logout revokes a single refresh token (rotation chain entry).
func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	_, jti, err := s.jwtService.ValidateRefreshToken(refreshToken)
	if err != nil {
		return nil // idempotent
	}
	_ = s.refreshRepo.Rotate(ctx, jti, "logout")
	return nil
}

// LogoutAll revokes every refresh token for the user (reuse-theft response).
func (s *AuthService) LogoutAll(ctx context.Context, userID uuid.UUID) error {
	return s.refreshRepo.RevokeAllUser(ctx, userID)
}

// RequestPasswordReset creates a single-use token (returned for email delivery
// by the caller; only the hash is stored). Always succeeds silently to the
// caller to avoid enumeration — the handler returns a generic message.
func (s *AuthService) RequestPasswordReset(ctx context.Context, email string) (rawToken string, ok bool, err error) {
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		return "", false, nil
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", false, err
	}
	rawToken = base64.RawURLEncoding.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(sum[:])
	if err := s.resetRepo.Create(ctx, uuid.New(), user.ID, tokenHash, time.Now().Add(1*time.Hour)); err != nil {
		return "", false, err
	}
	return rawToken, true, nil
}

func (s *AuthService) ResetPassword(ctx context.Context, rawToken, newPassword string) error {
	if err := validator.ValidatePassword(newPassword); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(sum[:])
	rec, err := s.resetRepo.GetValid(ctx, tokenHash, time.Now())
	if err != nil {
		return errors.New("invalid or expired reset token")
	}
	user, err := s.userRepo.GetByID(ctx, rec.UserID)
	if err != nil || !user.IsActive {
		return errors.New("invalid or expired reset token")
	}
	// Reject reuse of the current password.
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(newPassword)) == nil {
		return errors.New("new password must differ from the current password")
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.userRepo.UpdatePassword(ctx, user.ID, string(hashed)); err != nil {
		return err
	}
	if err := s.resetRepo.MarkUsed(ctx, rec.ID); err != nil {
		return err
	}
	// Invalidate all sessions after a password change.
	_ = s.refreshRepo.RevokeAllUser(ctx, user.ID)
	return nil
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

var _ = subtleEqual
