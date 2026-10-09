package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 30 * 24 * time.Hour
	Issuer          = "lending-app"
)

type JWTService struct {
	secret []byte
}

func NewJWTService(secret string) *JWTService {
	return &JWTService{secret: []byte(secret)}
}

type Claims struct {
	UserID uuid.UUID `json:"userId"`
	Role   string    `json:"role"`
	jwt.RegisteredClaims
}

func (j *JWTService) GenerateToken(userID uuid.UUID, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    Issuer,
			Audience:  jwt.ClaimStrings{"lending-app-clients"},
			Subject:   userID.String(),
			ID:        uuid.NewString(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secret)
}

func (j *JWTService) GenerateRefreshToken(userID uuid.UUID) (jti string, token string, err error) {
	now := time.Now()
	jti = uuid.NewString()
	claims := jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(now.Add(RefreshTokenTTL)),
		IssuedAt:  jwt.NewNumericDate(now),
		NotBefore: jwt.NewNumericDate(now),
		Issuer:    Issuer,
		Subject:   userID.String(),
		ID:        jti,
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := t.SignedString(j.secret)
	if err != nil {
		return "", "", err
	}
	return jti, signed, nil
}

func checkMethod(t *jwt.Token) (interface{}, error) {
	return nil, errors.New("invalid signing method")
}

func (j *JWTService) keyFunc(t *jwt.Token) (interface{}, error) {
	if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
		return nil, errors.New("invalid signing method")
	}
	return j.secret, nil
}

func (j *JWTService) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, j.keyFunc)
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}
	if claims.Issuer != Issuer {
		return nil, errors.New("invalid issuer")
	}
	return claims, nil
}

func (j *JWTService) ValidateRefreshToken(tokenString string) (userID uuid.UUID, jti string, err error) {
	token, err := jwt.ParseWithClaims(tokenString, &jwt.RegisteredClaims{}, j.keyFunc)
	if err != nil {
		return uuid.Nil, "", err
	}
	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid {
		return uuid.Nil, "", errors.New("invalid token")
	}
	if claims.Issuer != Issuer {
		return uuid.Nil, "", errors.New("invalid issuer")
	}
	if claims.ID == "" {
		return uuid.Nil, "", errors.New("missing jti")
	}
	uid, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, "", errors.New("invalid subject")
	}
	_ = checkMethod
	return uid, claims.ID, nil
}
