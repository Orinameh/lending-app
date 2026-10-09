package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

var (
	instance *EncryptionService
	once     sync.Once
	initErr  error
)

type EncryptionService struct {
	aesGCM cipher.AEAD
	hmacKey []byte
}

func GetEncryptionService() (*EncryptionService, error) {
	once.Do(func() {
		instance, initErr = NewEncryptionService()
	})
	return instance, initErr
}

func NewEncryptionService() (*EncryptionService, error) {
	keyBase64 := os.Getenv("ENCRYPTION_KEY")
	if keyBase64 == "" {
		return nil, errors.New("ENCRYPTION_KEY environment variable required")
	}
	key, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes (got %d)", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}
	hmacKey := loadHMACKey(key)
	return &EncryptionService{aesGCM: aesGCM, hmacKey: hmacKey}, nil
}

// loadHMACKey prefers HMAC_KEY (base64 32B) and falls back to the encryption
// key with domain separation. A separate HMAC key is strongly recommended.
func loadHMACKey(encKey []byte) []byte {
	if raw := os.Getenv("HMAC_KEY"); raw != "" {
		if k, err := base64.StdEncoding.DecodeString(raw); err == nil && len(k) == 32 {
			return k
		}
	}
	h := sha256.Sum256(append([]byte("hmac:"), encKey...))
	out := make([]byte, 32)
	copy(out, h[:])
	return out
}

// HMAC returns a hex HMAC-SHA256 used for indexed lookups of encrypted PII
// (e.g. email_hmac). The plaintext is normalized by the caller.
func (e *EncryptionService) HMAC(plaintext string) string {
	m := hmac.New(sha256.New, e.hmacKey)
	_, _ = m.Write([]byte(plaintext))
	return fmt.Sprintf("%x", m.Sum(nil))
}

// EqualHMAC compares two hex HMACs in constant time.
func EqualHMAC(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (e *EncryptionService) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, e.aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	ciphertext := e.aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (e *EncryptionService) Decrypt(encryptedData string) (string, error) {
	if encryptedData == "" {
		return "", nil
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return "", fmt.Errorf("failed to decode: %w", err)
	}
	nonceSize := e.aesGCM.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	nonce := ciphertext[:nonceSize]
	ciphertext = ciphertext[nonceSize:]
	plaintext, err := e.aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}
	return string(plaintext), nil
}

func (e *EncryptionService) HealthCheck() error {
	test := "encryption-health-check"
	enc, err := e.Encrypt(test)
	if err != nil {
		return err
	}
	dec, err := e.Decrypt(enc)
	if err != nil {
		return err
	}
	if dec != test {
		return errors.New("encryption/decryption mismatch")
	}
	return nil
}
