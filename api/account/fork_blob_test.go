package account

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"pgregory.net/rapid"
)

// TestForkBlobRoundTrip_Property verifies that for any valid ForkBlob (with
// arbitrary keyPassword string), encrypting with EncryptForkBlob and then
// decrypting with DecryptForkBlob using the same key produces an identical
// ForkBlob.
//
// **Validates: Requirements 2.2**
// Tag: Feature: session-fork, Property 2: Fork blob encryption round-trip
func TestForkBlobRoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		keyPassword := rapid.String().Draw(t, "keyPassword")

		original := &ForkBlob{
			Type:        "default",
			KeyPassword: keyPassword,
		}

		ciphertext, key, err := EncryptForkBlob(original)
		if err != nil {
			t.Fatalf("EncryptForkBlob: %v", err)
		}

		if len(key) != forkBlobKeySize {
			t.Fatalf("key length = %d, want %d", len(key), forkBlobKeySize)
		}

		if ciphertext == "" {
			t.Fatal("ciphertext is empty")
		}

		// Verify it's valid base64.
		decoded, err := base64.StdEncoding.DecodeString(ciphertext)
		if err != nil {
			t.Fatalf("ciphertext is not valid base64: %v", err)
		}

		// Verify nonce is prepended (at least 12 bytes + some ciphertext).
		if len(decoded) < forkBlobNonceSize+1 {
			t.Fatalf("decoded ciphertext too short: %d bytes", len(decoded))
		}

		restored, err := DecryptForkBlob(ciphertext, key)
		if err != nil {
			t.Fatalf("DecryptForkBlob: %v", err)
		}

		if restored.Type != original.Type {
			t.Fatalf("Type: got %q, want %q", restored.Type, original.Type)
		}
		if restored.KeyPassword != original.KeyPassword {
			t.Fatalf("KeyPassword: got %q, want %q", restored.KeyPassword, original.KeyPassword)
		}
	})
}

// TestEncryptForkBlobKeyUniqueness verifies that two encryptions of the same
// blob produce different keys and ciphertexts.
func TestEncryptForkBlobKeyUniqueness(t *testing.T) {
	blob := &ForkBlob{Type: "default", KeyPassword: "test-pass"}

	ct1, key1, err := EncryptForkBlob(blob)
	if err != nil {
		t.Fatalf("first encrypt: %v", err)
	}

	ct2, key2, err := EncryptForkBlob(blob)
	if err != nil {
		t.Fatalf("second encrypt: %v", err)
	}

	if ct1 == ct2 {
		t.Fatal("two encryptions produced identical ciphertext")
	}

	keyMatch := true
	for i := range key1 {
		if key1[i] != key2[i] {
			keyMatch = false
			break
		}
	}
	if keyMatch {
		t.Fatal("two encryptions produced identical keys")
	}
}

// TestDecryptForkBlobWrongKey verifies that decryption with the wrong key
// fails.
func TestDecryptForkBlobWrongKey(t *testing.T) {
	blob := &ForkBlob{Type: "default", KeyPassword: "secret"}

	ct, _, err := EncryptForkBlob(blob)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	wrongKey := make([]byte, forkBlobKeySize)
	if _, err := rand.Read(wrongKey); err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}

	_, err = DecryptForkBlob(ct, wrongKey)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

// TestDecryptForkBlobInvalidBase64 verifies that invalid base64 input is
// rejected.
func TestDecryptForkBlobInvalidBase64(t *testing.T) {
	key := make([]byte, forkBlobKeySize)
	_, err := DecryptForkBlob("not-valid-base64!!!", key)
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

// TestDecryptForkBlobTooShort verifies that a ciphertext shorter than the
// nonce size is rejected.
func TestDecryptForkBlobTooShort(t *testing.T) {
	key := make([]byte, forkBlobKeySize)
	short := base64.StdEncoding.EncodeToString([]byte("tiny"))
	_, err := DecryptForkBlob(short, key)
	if err == nil {
		t.Fatal("expected error for too-short ciphertext")
	}
}

// TestEncryptForkBlobEmptyKeyPassword verifies that an empty keyPassword
// round-trips correctly.
func TestEncryptForkBlobEmptyKeyPassword(t *testing.T) {
	blob := &ForkBlob{Type: "default", KeyPassword: ""}

	ct, key, err := EncryptForkBlob(blob)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	restored, err := DecryptForkBlob(ct, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if restored.KeyPassword != "" {
		t.Fatalf("KeyPassword = %q, want empty", restored.KeyPassword)
	}
}

// TestEncryptForkBlobUnicodeKeyPassword verifies that unicode characters in
// keyPassword round-trip correctly.
func TestEncryptForkBlobUnicodeKeyPassword(t *testing.T) {
	unicodePass := "\u043f\u0430\u0440\u043e\u043b\u044c\U0001f511\u5bc6\u7801" //nolint:gosec // G101: test data, not a real credential
	blob := &ForkBlob{Type: "default", KeyPassword: unicodePass}

	ct, key, err := EncryptForkBlob(blob)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	restored, err := DecryptForkBlob(ct, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if restored.KeyPassword != blob.KeyPassword {
		t.Fatalf("KeyPassword = %q, want %q", restored.KeyPassword, blob.KeyPassword)
	}
}
