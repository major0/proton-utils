package lumo

import (
	"crypto/aes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	// HKDFSalt is the base64-encoded salt for space key derivation.
	// Source: WebClients crypto/index.ts SPACE_KEY_DERIVATION_SALT
	HKDFSalt = "Xd6V94/+5BmLAfc67xIBZcjsBPimm9/j02kHPI7Vsuc="

	// HKDFInfo is the context string for space DEK derivation.
	// Source: WebClients crypto/index.ts SPACE_DEK_CONTEXT
	HKDFInfo = "dek.space.lumo"

	// EligibilityEligible is the Eligibility value for eligible accounts.
	EligibilityEligible = 1
)

// WrapSpaceKey wraps a 32-byte space key with a 32-byte master key
// using AES Key Wrap (RFC 3394).
func WrapSpaceKey(masterKey, spaceKey []byte) ([]byte, error) {
	if len(masterKey) != aesKeySize {
		return nil, fmt.Errorf("lumo: master key must be %d bytes", aesKeySize)
	}
	if len(spaceKey) != aesKeySize {
		return nil, fmt.Errorf("lumo: space key must be %d bytes", aesKeySize)
	}
	return aesKeyWrap(masterKey, spaceKey)
}

// UnwrapSpaceKey unwraps an AES-KW wrapped space key using the master key.
func UnwrapSpaceKey(masterKey, wrappedKey []byte) ([]byte, error) {
	if len(masterKey) != aesKeySize {
		return nil, fmt.Errorf("lumo: master key must be %d bytes", aesKeySize)
	}
	plaintext, err := aesKeyUnwrap(masterKey, wrappedKey)
	if err != nil {
		return nil, fmt.Errorf("lumo: unwrap space key: %w", ErrDecryptionFailed)
	}
	return plaintext, nil
}

// DeriveDataEncryptionKey derives a 32-byte DEK from a space key using
// HKDF-SHA256 with the fixed salt and info string.
func DeriveDataEncryptionKey(spaceKey []byte) ([]byte, error) {
	if len(spaceKey) != aesKeySize {
		return nil, fmt.Errorf("lumo: space key must be %d bytes", aesKeySize)
	}
	salt, err := base64.StdEncoding.DecodeString(HKDFSalt)
	if err != nil {
		return nil, fmt.Errorf("lumo: decode HKDF salt: %w", err)
	}
	r := hkdf.New(sha256.New, spaceKey, salt, []byte(HKDFInfo))
	dek := make([]byte, aesKeySize)
	if _, err := io.ReadFull(r, dek); err != nil {
		return nil, fmt.Errorf("lumo: HKDF derive: %w", err)
	}
	return dek, nil
}

// EncryptString encrypts plaintext with AES-GCM using the DEK and AD,
// returning a base64-encoded string (nonce || ciphertext || tag).
func EncryptString(plaintext string, dek []byte, ad string) (string, error) {
	return encryptAESGCM([]byte(plaintext), dek, []byte(ad))
}

// DecryptString decrypts a base64-encoded AES-GCM ciphertext using the
// DEK and AD, returning the plaintext string.
func DecryptString(ciphertext string, dek []byte, ad string) (string, error) {
	plaintext, err := decryptAESGCM(ciphertext, dek, []byte(ad))
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// SpaceAD returns the deterministic associated data string for a space.
// Output matches json-stable-stringify with alphabetically sorted keys.
func SpaceAD(spaceTag string) string {
	return `{"app":"lumo","id":"` + jsonEscape(spaceTag) + `","type":"space"}`
}

// ConversationAD returns the deterministic associated data string for a
// conversation.
func ConversationAD(convTag, spaceTag string) string {
	return `{"app":"lumo","id":"` + jsonEscape(convTag) +
		`","spaceId":"` + jsonEscape(spaceTag) +
		`","type":"conversation"}`
}

// MessageAD returns the deterministic associated data string for a message.
// Keys are alphabetically sorted to match json-stable-stringify output.
// The parentId key is omitted entirely when parentID is empty, matching
// the web client's behavior for root messages (undefined → key omitted).
func MessageAD(msgTag, role, parentID, convTag string) string {
	s := `{"app":"lumo","conversationId":"` + jsonEscape(convTag) +
		`","id":"` + jsonEscape(msgTag) + `"`
	if parentID != "" {
		s += `,"parentId":"` + jsonEscape(parentID) + `"`
	}
	s += `,"role":"` + jsonEscape(role) +
		`","type":"message"}`
	return s
}

// jsonEscape escapes special JSON characters in a string value.
// Only handles the characters that could appear in UUIDs and role strings.
func jsonEscape(s string) string {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\\', '\n', '\r', '\t':
			return jsonEscapeSlow(s)
		}
	}
	return s
}

func jsonEscapeSlow(s string) string {
	var buf []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			buf = append(buf, '\\', '"')
		case '\\':
			buf = append(buf, '\\', '\\')
		case '\n':
			buf = append(buf, '\\', 'n')
		case '\r':
			buf = append(buf, '\\', 'r')
		case '\t':
			buf = append(buf, '\\', 't')
		default:
			buf = append(buf, s[i])
		}
	}
	return string(buf)
}

// --- AES Key Wrap (RFC 3394) ---

// xorCounter XORs the 8-byte big-endian representation of t into a.
// The uint64→byte truncation is intentional — we extract individual
// bytes from the 64-bit counter per RFC 3394 §2.2.1.
func xorCounter(a *[8]byte, t uint64) {
	a[0] ^= byte(t >> 56) //nolint:gosec // intentional truncation
	a[1] ^= byte(t >> 48) //nolint:gosec // intentional truncation
	a[2] ^= byte(t >> 40) //nolint:gosec // intentional truncation
	a[3] ^= byte(t >> 32) //nolint:gosec // intentional truncation
	a[4] ^= byte(t >> 24) //nolint:gosec // intentional truncation
	a[5] ^= byte(t >> 16) //nolint:gosec // intentional truncation
	a[6] ^= byte(t >> 8)  //nolint:gosec // intentional truncation
	a[7] ^= byte(t)       //nolint:gosec // intentional truncation
}

// aesKeyWrap implements AES Key Wrap per RFC 3394 §2.2.1.
// Input: KEK (16, 24, or 32 bytes), plaintext (multiple of 8 bytes, ≥16).
// Output: wrapped key (len(plaintext) + 8 bytes).
func aesKeyWrap(kek, plaintext []byte) ([]byte, error) {
	n := len(plaintext) / 8
	if len(plaintext)%8 != 0 || n < 2 {
		return nil, fmt.Errorf("lumo: AES-KW plaintext must be ≥16 bytes and multiple of 8")
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("lumo: AES-KW cipher: %w", err)
	}

	// Initialize: A = IV, R[1..n] = P[1..n]
	var a [8]byte
	for i := range a {
		a[i] = 0xA6
	}
	r := make([]byte, len(plaintext))
	copy(r, plaintext)

	// Wrap: for j = 0..5, for i = 1..n
	var buf [aes.BlockSize]byte
	for j := 0; j < 6; j++ {
		for i := 0; i < n; i++ {
			// B = AES(K, A || R[i])
			copy(buf[:8], a[:])
			copy(buf[8:], r[i*8:(i+1)*8])
			block.Encrypt(buf[:], buf[:])

			// A = MSB(64, B) ^ t where t = n*j + i + 1
			copy(a[:], buf[:8])
			xorCounter(&a, uint64(n*j+i+1)) //nolint:gosec // n*j+i+1 is always small and positive
			// R[i] = LSB(64, B)
			copy(r[i*8:], buf[8:])
		}
	}

	// Output: C = A || R[1] || ... || R[n]
	out := make([]byte, 8+len(r))
	copy(out[:8], a[:])
	copy(out[8:], r)
	return out, nil
}

// aesKeyUnwrap implements AES Key Unwrap per RFC 3394 §2.2.2.
// Input: KEK, ciphertext (multiple of 8 bytes, ≥24).
// Output: unwrapped key (len(ciphertext) - 8 bytes).
func aesKeyUnwrap(kek, ciphertext []byte) ([]byte, error) {
	if len(ciphertext)%8 != 0 || len(ciphertext) < 24 {
		return nil, fmt.Errorf("lumo: AES-KW ciphertext must be ≥24 bytes and multiple of 8")
	}
	n := len(ciphertext)/8 - 1

	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("lumo: AES-KW cipher: %w", err)
	}

	// Initialize: A = C[0], R[1..n] = C[1..n]
	var a [8]byte
	copy(a[:], ciphertext[:8])
	r := make([]byte, n*8)
	copy(r, ciphertext[8:])

	// Unwrap: for j = 5..0, for i = n..1
	var buf [aes.BlockSize]byte
	for j := 5; j >= 0; j-- {
		for i := n - 1; i >= 0; i-- {
			xorCounter(&a, uint64(n*j+i+1)) //nolint:gosec // n*j+i+1 is always small and positive

			// B = AES-1(K, (A ^ t) || R[i])
			copy(buf[:8], a[:])
			copy(buf[8:], r[i*8:(i+1)*8])
			block.Decrypt(buf[:], buf[:])

			// A = MSB(64, B)
			copy(a[:], buf[:8])
			// R[i] = LSB(64, B)
			copy(r[i*8:], buf[8:])
		}
	}

	// Verify IV
	for i := range a {
		if a[i] != 0xA6 {
			return nil, fmt.Errorf("lumo: AES-KW integrity check failed")
		}
	}

	return r, nil
}
