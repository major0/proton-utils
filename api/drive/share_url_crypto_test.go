package drive

import (
	"bytes"
	"testing"

	"pgregory.net/rapid"
)

// srpTestModulus is a known-good signed modulus from go-srp test vectors.
// Used for SRP verifier determinism tests.
const testSRPModulus = "-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\nW2z5HBi8RvsfYzZTS7qBaUxxPhsfHJFZpu3Kd6s1JafNrCCH9rfvPLrfuqocxWPgWDH2R8neK7PkNvjxto9TStuY5z7jAzWRvFWN9cQhAKkdWgy0JY6ywVn22+HFpF4cYesHrqFIKUPDMSSIlWjBVmEJZ/MusD44ZT29xcPrOqeZvwtCffKtGAIjLYPZIEbZKnDM1Dm3q2K/xS5h+xdhjnndhsrkwm9U9oyA2wxzSXFL+pdfj2fOdRwuR5nW0J2NFrq3kJjkRmpO/Genq1UW+TEknIWAb6VzJJJA244K/H8cnSx2+nSNZO3bbo6Ys228ruV9A8m6DhxmS+bihN3ttQ==\n-----BEGIN PGP SIGNATURE-----\nVersion: ProtonMail\nComment: https://protonmail.com\n\nwl4EARYIABAFAlwB1j0JEDUFhcTpUY8mAAD8CgEAnsFnF4cF0uSHKkXa1GIa\nGO86yMV4zDZEZcDSJo0fgr8A/AlupGN9EdHlsrZLmTA1vhIx+rOgxdEff28N\nkvNM7qIK\n=q6vu\n-----END PGP SIGNATURE-----"

// TestShareURLPasswordRoundTrip_Property verifies that for any random 32-char
// password, encrypting with the address public key and decrypting with the
// address private key produces the identical original password.
//
// **Property 1: ShareURL password round-trip**
// **Validates: Requirements 1a.3, 2.4**
func TestShareURLPasswordRoundTrip_Property(t *testing.T) {
	// Key generation is expensive — generate once outside the property loop.
	addrKR := genKeyRing(t, "shareurl-addr")

	rapid.Check(t, func(t *rapid.T) {
		// Generate a random 32-character password (alphanumeric).
		password := rapid.StringMatching(`[a-zA-Z0-9]{32}`).Draw(t, "password")

		// Encrypt with address public key.
		encrypted, err := encryptShareURLPassword(password, addrKR)
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}

		// Decrypt with address private key.
		decrypted, err := decryptShareURLPassword(encrypted, addrKR)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}

		// Assert round-trip equality.
		if decrypted != password {
			t.Fatalf("round-trip mismatch: got %q, want %q", decrypted, password)
		}
	})
}

// TestSRPVerifierDeterminism_Property verifies that for a fixed modulus and
// fixed salt, generating an SRP verifier with the same password always
// produces identical verifier bytes.
//
// **Property 2: SRP verifier determinism**
// **Validates: Requirements 1a.5**
func TestSRPVerifierDeterminism_Property(t *testing.T) {
	// Fixed salt (10 bytes, matching the production pattern).
	fixedSalt := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a}

	rapid.Check(t, func(t *rapid.T) {
		// Generate a random password (1-64 chars, alphanumeric).
		password := rapid.StringMatching(`[a-zA-Z0-9]{1,64}`).Draw(t, "password")

		// Compute verifier twice with same inputs.
		verifier1, err := computeSRPVerifier(password, testSRPModulus, fixedSalt)
		if err != nil {
			t.Fatalf("first verifier: %v", err)
		}

		verifier2, err := computeSRPVerifier(password, testSRPModulus, fixedSalt)
		if err != nil {
			t.Fatalf("second verifier: %v", err)
		}

		// Assert determinism: same inputs → same output.
		if !bytes.Equal(verifier1, verifier2) {
			t.Fatalf("verifier not deterministic for password %q", password)
		}
	})
}
