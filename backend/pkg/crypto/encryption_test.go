package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b[:])
}

func TestSingleKeyEnvelope(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", testKey(t))
	t.Setenv("ENCRYPTION_KEY_PREVIOUS", "")
	svc, err := NewEncryptionService()
	if err != nil {
		t.Fatal(err)
	}
	if svc.PrimaryVersion() == "" || svc.HasPrevious() {
		t.Fatal("want key id, no previous")
	}
	enc, err := svc.Encrypt("ada@test.ng")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "v"+svc.PrimaryVersion()+":") {
		t.Fatalf("want v<id>: envelope, got %q", enc)
	}
	dec, err := svc.Decrypt(enc)
	if err != nil || dec != "ada@test.ng" {
		t.Fatalf("roundtrip = %q, %v", dec, err)
	}
	if VersionOf(enc) != svc.PrimaryVersion() || VersionOf("") != "" || VersionOf("garbage") != "" {
		t.Fatal("VersionOf mismatch")
	}
}

// TestRotationSurvivesPreviousDrop is the regression test for positional
// versioning: key IDs are bound to key material, so dropping the previous
// key after re-encryption must not orphan rotation-window rows.
func TestRotationSurvivesPreviousDrop(t *testing.T) {
	keyA := testKey(t)
	keyB := testKey(t)

	t.Setenv("ENCRYPTION_KEY", keyA)
	t.Setenv("ENCRYPTION_KEY_PREVIOUS", "")
	svcA, err := NewEncryptionService()
	if err != nil {
		t.Fatal(err)
	}
	idA := svcA.PrimaryVersion()
	oldCipher, err := svcA.Encrypt("sensitive")
	if err != nil {
		t.Fatal(err)
	}

	// Rotate: B primary, A previous. IDs must be stable per key material.
	t.Setenv("ENCRYPTION_KEY", keyB)
	t.Setenv("ENCRYPTION_KEY_PREVIOUS", keyA)
	svcB, err := NewEncryptionService()
	if err != nil {
		t.Fatal(err)
	}
	idB := svcB.PrimaryVersion()
	if idA == idB {
		t.Fatal("distinct keys must have distinct ids")
	}
	if dec, err := svcB.Decrypt(oldCipher); err != nil || dec != "sensitive" {
		t.Fatalf("old cipher unreadable after rotation: %q %v", dec, err)
	}
	enc, err := svcB.Encrypt("sensitive")
	if err != nil {
		t.Fatal(err)
	}
	if VersionOf(enc) != idB {
		t.Fatalf("new writes must carry primary id, got %q", enc)
	}

	// Drop previous WITHOUT re-encrypting: old (A) rows fail loudly...
	t.Setenv("ENCRYPTION_KEY", keyB)
	t.Setenv("ENCRYPTION_KEY_PREVIOUS", "")
	svcC, err := NewEncryptionService()
	if err != nil {
		t.Fatal(err)
	}
	if svcC.PrimaryVersion() != idB {
		t.Fatal("same key must keep the same id across deploys")
	}
	if _, err := svcC.Decrypt(oldCipher); err == nil {
		t.Fatal("un-rekeyed A row must fail after previous is dropped")
	}
	// ...but rows re-encrypted during the window stay readable. This is the
	// exact scenario the rekey job produces.
	if dec, err := svcC.Decrypt(enc); err != nil || dec != "sensitive" {
		t.Fatalf("rekeyed row must survive previous drop: %q %v", dec, err)
	}
}

func TestLegacyUnprefixedFallback(t *testing.T) {
	keyA := testKey(t)
	keyB := testKey(t)

	// Simulate a pre-versioning row: raw base64(nonce|ciphertext).
	t.Setenv("ENCRYPTION_KEY", keyA)
	t.Setenv("ENCRYPTION_KEY_PREVIOUS", "")
	svcA, _ := NewEncryptionService()
	enc, _ := svcA.Encrypt("legacy-row")
	legacy := strings.TrimPrefix(enc, "v"+svcA.PrimaryVersion()+":")

	t.Setenv("ENCRYPTION_KEY", keyB)
	t.Setenv("ENCRYPTION_KEY_PREVIOUS", keyA)
	svcB, _ := NewEncryptionService()
	if dec, err := svcB.Decrypt(legacy); err != nil || dec != "legacy-row" {
		t.Fatalf("legacy fallback failed: %q %v", dec, err)
	}
}

func TestUnknownVersionRejected(t *testing.T) {
	t.Setenv("ENCRYPTION_KEY", testKey(t))
	t.Setenv("ENCRYPTION_KEY_PREVIOUS", "")
	svc, _ := NewEncryptionService()
	if _, err := svc.Decrypt("vdeadbeef:AAAAAAAAAAAAAAAA"); err == nil {
		t.Fatal("unknown key id must be rejected")
	}
	if _, err := svc.Decrypt("v1"); err == nil {
		t.Fatal("malformed envelope must be rejected")
	}
}

func TestHMACPreviousFallback(t *testing.T) {
	keyA, keyB := testKey(t), testKey(t)
	t.Setenv("ENCRYPTION_KEY", testKey(t))
	t.Setenv("HMAC_KEY", keyA)
	t.Setenv("HMAC_KEY_PREVIOUS", "")
	svcA, _ := NewEncryptionService()
	sumA := svcA.HMAC("ada@test.ng")
	if _, ok := svcA.HMACPrevious("ada@test.ng"); ok {
		t.Fatal("no previous expected")
	}

	t.Setenv("HMAC_KEY", keyB)
	t.Setenv("HMAC_KEY_PREVIOUS", keyA)
	svcB, _ := NewEncryptionService()
	if svcB.HMAC("ada@test.ng") == sumA {
		t.Fatal("primary HMAC must differ after rotation")
	}
	prev, ok := svcB.HMACPrevious("ada@test.ng")
	if !ok || !EqualHMAC(prev, sumA) {
		t.Fatal("previous HMAC must match pre-rotation value")
	}
}

func TestIdenticalPreviousRejected(t *testing.T) {
	k := testKey(t)
	t.Setenv("ENCRYPTION_KEY", k)
	t.Setenv("ENCRYPTION_KEY_PREVIOUS", k)
	if _, err := NewEncryptionService(); err == nil {
		t.Fatal("identical previous key must be rejected")
	}
}
