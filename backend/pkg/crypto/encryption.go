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
	"strings"
	"sync"
)

var (
	instance *EncryptionService
	once     sync.Once
	initErr  error
)

// Key rotation design.
//
// Ciphertext envelope: "v<keyID>:<base64(nonce|ciphertext)>", where keyID is
// 8 hex chars of SHA-256("enc-key-id:"+key). The ID is BOUND TO THE KEY
// MATERIAL, not to a slot: the same key keeps the same ID whether it is
// primary or previous, so dropping the previous key after re-encryption can
// never orphan rows (a positional v1/v2 scheme would renumber the survivor
// and brick the rotation window — do not "simplify" this back).
//
//   - Decrypt matches the envelope ID against configured keys by identity.
//     Unknown IDs and legacy unprefixed values (tried primary, then previous)
//     fail CLOSED via GCM authentication — never wrong plaintext.
//   - Reads serve from both keys; writes use primary.
//
// Runbook:
//  1. Generate new key (make key). Deploy with new ENCRYPTION_KEY and old key
//     in ENCRYPTION_KEY_PREVIOUS. Reads serve from both; writes use primary.
//  2. Run re-encryption: `api -reencrypt` (dry-run first with -dry-run).
//     Rewrites every PII field to the primary key ID + refreshes HMACs.
//  3. Deploy with ENCRYPTION_KEY_PREVIOUS removed. Done.
//
// Never log key material — only key IDs and row counts.
type versionedKey struct {
	id  string
	gcm cipher.AEAD
}

// keyID binds a stable identifier to key material.
func keyID(key []byte) string {
	sum := sha256.Sum256(append([]byte("enc-key-id:"), key...))
	return fmt.Sprintf("%x", sum[:4])
}

type EncryptionService struct {
	primary  versionedKey
	previous *versionedKey

	hmacKey     []byte
	hmacPrevKey []byte
	hasHMACPrev bool
}

func GetEncryptionService() (*EncryptionService, error) {
	once.Do(func() {
		instance, initErr = NewEncryptionService()
	})
	return instance, initErr
}

func decodeKey(name, raw string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", name, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s must be 32 bytes (got %d)", name, len(key))
	}
	return key, nil
}

func mustGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}
	return gcm, nil
}

func NewEncryptionService() (*EncryptionService, error) {
	rawCurrent := os.Getenv("ENCRYPTION_KEY")
	if rawCurrent == "" {
		return nil, errors.New("ENCRYPTION_KEY environment variable required")
	}
	current, err := decodeKey("ENCRYPTION_KEY", rawCurrent)
	if err != nil {
		return nil, err
	}
	svc := &EncryptionService{}
	if rawPrev := os.Getenv("ENCRYPTION_KEY_PREVIOUS"); rawPrev != "" {
		prev, err := decodeKey("ENCRYPTION_KEY_PREVIOUS", rawPrev)
		if err != nil {
			return nil, err
		}
		if subtle.ConstantTimeCompare(current, prev) == 1 {
			return nil, errors.New("ENCRYPTION_KEY_PREVIOUS must differ from ENCRYPTION_KEY")
		}
		prevGCM, err := mustGCM(prev)
		if err != nil {
			return nil, err
		}
		svc.previous = &versionedKey{id: keyID(prev), gcm: prevGCM}
		currGCM, err := mustGCM(current)
		if err != nil {
			return nil, err
		}
		svc.primary = versionedKey{id: keyID(current), gcm: currGCM}
	} else {
		currGCM, err := mustGCM(current)
		if err != nil {
			return nil, err
		}
		svc.primary = versionedKey{id: keyID(current), gcm: currGCM}
	}

	svc.hmacKey = loadHMACKey(os.Getenv("HMAC_KEY"), current)
	if rawHMACPrev := os.Getenv("HMAC_KEY_PREVIOUS"); rawHMACPrev != "" {
		k, err := decodeKey("HMAC_KEY_PREVIOUS", rawHMACPrev)
		if err != nil {
			return nil, err
		}
		svc.hmacPrevKey = k
		svc.hasHMACPrev = true
	}
	return svc, nil
}

// loadHMACKey prefers an explicit HMAC key (base64 32B) and falls back to the
// encryption key with domain separation. A separate HMAC key is strongly
// recommended.
func loadHMACKey(explicit string, encKey []byte) []byte {
	if explicit != "" {
		if k, err := base64.StdEncoding.DecodeString(explicit); err == nil && len(k) == 32 {
			return k
		}
	}
	h := sha256.Sum256(append([]byte("hmac:"), encKey...))
	out := make([]byte, 32)
	copy(out, h[:])
	return out
}

// PrimaryVersion is the key ID used for new writes.
func (e *EncryptionService) PrimaryVersion() string { return e.primary.id }

// HasPrevious reports whether a previous key is configured (rotation window).
func (e *EncryptionService) HasPrevious() bool { return e.previous != nil }

// HasHMACPrevious reports whether a previous HMAC key is configured.
func (e *EncryptionService) HasHMACPrevious() bool { return e.hasHMACPrev }

// VersionOf returns the envelope key ID of stored ciphertext ("" = legacy).
func VersionOf(stored string) string {
	if !strings.HasPrefix(stored, "v") {
		return ""
	}
	if i := strings.Index(stored, ":"); i > 1 {
		id := stored[1:i]
		if len(id) == 8 && isHex(id) {
			return id
		}
	}
	return ""
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// HMAC returns a hex HMAC-SHA256 under the PRIMARY HMAC key, used for indexed
// lookups of encrypted PII (e.g. email_hmac). The plaintext is normalized by
// the caller.
func (e *EncryptionService) HMAC(plaintext string) string {
	return hmacHex(e.hmacKey, plaintext)
}

// HMACPrevious returns the HMAC under the previous key (rotation window).
// ok is false when no previous HMAC key is configured.
func (e *EncryptionService) HMACPrevious(plaintext string) (sum string, ok bool) {
	if !e.hasHMACPrev {
		return "", false
	}
	return hmacHex(e.hmacPrevKey, plaintext), true
}

func hmacHex(key []byte, plaintext string) string {
	m := hmac.New(sha256.New, key)
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
	gcm := e.primary.gcm
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return fmt.Sprintf("v%s:%s", e.primary.id, base64.StdEncoding.EncodeToString(ciphertext)), nil
}

func (e *EncryptionService) openWith(gcm cipher.AEAD, raw string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", fmt.Errorf("failed to decode: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	nonce := ciphertext[:nonceSize]
	ciphertext = ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}
	return string(plaintext), nil
}

func (e *EncryptionService) Decrypt(encryptedData string) (string, error) {
	if encryptedData == "" {
		return "", nil
	}
	// Versioned envelope: match the key ID by identity.
	if id := VersionOf(encryptedData); id != "" {
		rest := encryptedData[len(id)+2:]
		switch {
		case subtle.ConstantTimeCompare([]byte(id), []byte(e.primary.id)) == 1:
			return e.openWith(e.primary.gcm, rest)
		case e.previous != nil && subtle.ConstantTimeCompare([]byte(id), []byte(e.previous.id)) == 1:
			return e.openWith(e.previous.gcm, rest)
		default:
			return "", fmt.Errorf("unknown key id v%s", id)
		}
	}
	if strings.HasPrefix(encryptedData, "v") {
		return "", errors.New("malformed ciphertext envelope")
	}
	// Legacy unprefixed value: try primary, then previous.
	if s, err := e.openWith(e.primary.gcm, encryptedData); err == nil {
		return s, nil
	} else if e.previous != nil {
		if s, err2 := e.openWith(e.previous.gcm, encryptedData); err2 == nil {
			return s, nil
		}
		return "", err
	} else {
		return "", err
	}
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
