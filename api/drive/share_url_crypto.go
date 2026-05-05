package drive

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/ProtonMail/go-srp"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
)

// authModulusResponse is the response from GET /core/v4/auth/modulus.
type authModulusResponse struct {
	Modulus   string `json:"Modulus"`
	ModulusID string `json:"ModulusID"`
}

// encryptShareURLPassword encrypts a plaintext password with the address
// public key (PGP unsigned encryption). This matches the WebClients
// encryptUnsigned helper used for ShareURL password storage.
func encryptShareURLPassword(password string, addrKR *crypto.KeyRing) (string, error) {
	plain := crypto.NewPlainMessageFromString(password)
	enc, err := addrKR.Encrypt(plain, nil)
	if err != nil {
		return "", fmt.Errorf("encrypt share URL password: %w", err)
	}
	armored, err := enc.GetArmored()
	if err != nil {
		return "", fmt.Errorf("encrypt share URL password: armor: %w", err)
	}
	return armored, nil
}

// decryptShareURLPassword decrypts the encrypted password field from a
// ShareURL using the address private key.
func decryptShareURLPassword(encPassword string, addrKR *crypto.KeyRing) (string, error) {
	enc, err := crypto.NewPGPMessageFromArmored(encPassword)
	if err != nil {
		return "", fmt.Errorf("decrypt share URL password: parse: %w", err)
	}
	dec, err := addrKR.Decrypt(enc, nil, crypto.GetUnixTime())
	if err != nil {
		return "", fmt.Errorf("decrypt share URL password: decrypt: %w", err)
	}
	return dec.GetString(), nil
}

// generateKeySaltAndPassphrase generates a random 16-byte salt and derives
// a passphrase from the password using bcrypt (matching the WebClients
// computeKeyPassword from @proton/srp). The salt is returned as base64.
//
// The derivation uses go-srp's MailboxPassword which performs:
//
//	bcrypt(password, "$2y$10$" + base64DotSlash(salt))
//
// The result is the bcrypt hash with the prefix removed (last 31 bytes),
// matching the WebClients behavior.
func generateKeySaltAndPassphrase(password string) (saltB64, passphrase string, err error) {
	// Generate 16 random bytes for the salt (matches WebClients generateKeySalt).
	saltBytes, err := crypto.RandomToken(16)
	if err != nil {
		return "", "", fmt.Errorf("generate key salt: random: %w", err)
	}
	saltB64 = base64.StdEncoding.EncodeToString(saltBytes)

	// Derive passphrase using bcrypt via go-srp's MailboxPassword.
	// MailboxPassword returns the full bcrypt hash; the WebClients
	// computeKeyPassword strips the first 29 chars (prefix + salt),
	// leaving the hash portion. MailboxPassword returns the same format.
	hashed, err := srp.MailboxPassword([]byte(password), saltBytes)
	if err != nil {
		return "", "", fmt.Errorf("generate key salt: derive passphrase: %w", err)
	}
	// Strip the bcrypt prefix "$2y$10$" + 22-char encoded salt = 29 chars.
	if len(hashed) <= 29 {
		return "", "", fmt.Errorf("generate key salt: unexpected bcrypt output length %d", len(hashed))
	}
	passphrase = string(hashed[29:])
	return saltB64, passphrase, nil
}

// encryptShareSessionKey encrypts a session key with the derived passphrase
// (symmetric password-based encryption). This matches the WebClients
// encryptSymmetricSessionKey which calls CryptoProxy.encryptSessionKey with
// passwords=[passphrase]. Returns the encrypted key packet as base64.
func encryptShareSessionKey(sessionKey *crypto.SessionKey, passphrase string) (string, error) {
	encrypted, err := crypto.EncryptSessionKeyWithPassword(sessionKey, []byte(passphrase))
	if err != nil {
		return "", fmt.Errorf("encrypt share session key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// srpVerifierResult holds the output of SRP verifier generation.
type srpVerifierResult struct {
	ModulusID string
	Salt      string // base64-encoded salt
	Verifier  string // base64-encoded verifier
}

// generateSRPVerifier generates an SRP verifier for the given password.
// It fetches a fresh modulus from the API via GET /core/v4/auth/modulus,
// generates a random 10-byte salt, and computes the verifier using go-srp.
func generateSRPVerifier(ctx context.Context, c *Client, password string) (*srpVerifierResult, error) {
	// Fetch modulus from the API.
	var modResp authModulusResponse
	if err := c.Session.DoJSON(ctx, "GET", "/core/v4/auth/modulus", nil, &modResp); err != nil {
		return nil, fmt.Errorf("generate SRP verifier: fetch modulus: %w", err)
	}

	// Generate random salt (10 bytes, matching go-proton-api pattern).
	saltBytes, err := srp.RandomBytes(10)
	if err != nil {
		return nil, fmt.Errorf("generate SRP verifier: random salt: %w", err)
	}

	// Compute verifier using NewAuthForVerifier (version 4 hash).
	auth, err := srp.NewAuthForVerifier([]byte(password), modResp.Modulus, saltBytes)
	if err != nil {
		return nil, fmt.Errorf("generate SRP verifier: new auth: %w", err)
	}

	verifierBytes, err := auth.GenerateVerifier(2048)
	if err != nil {
		return nil, fmt.Errorf("generate SRP verifier: generate: %w", err)
	}

	return &srpVerifierResult{
		ModulusID: modResp.ModulusID,
		Salt:      base64.StdEncoding.EncodeToString(saltBytes),
		Verifier:  base64.StdEncoding.EncodeToString(verifierBytes),
	}, nil
}

// computeSRPVerifier computes an SRP verifier for a given password, signed
// modulus, and raw salt. This is the pure computation without API calls,
// useful for testing determinism.
func computeSRPVerifier(password string, signedModulus string, salt []byte) ([]byte, error) {
	auth, err := srp.NewAuthForVerifier([]byte(password), signedModulus, salt)
	if err != nil {
		return nil, fmt.Errorf("compute SRP verifier: new auth: %w", err)
	}
	verifier, err := auth.GenerateVerifier(2048)
	if err != nil {
		return nil, fmt.Errorf("compute SRP verifier: generate: %w", err)
	}
	return verifier, nil
}

// passwordDisablePayload returns the crypto fields for disabling a password
// on an existing ShareURL. All crypto fields are empty and Flags is 0.
func passwordDisablePayload() UpdateShareURLPayload {
	return UpdateShareURLPayload{
		Flags:                    0,
		Permissions:              4, // viewer
		MaxAccesses:              0,
		SharePassphraseKeyPacket: "",
		SharePasswordSalt:        "",
		Password:                 "",
		SRPModulusID:             "",
		SRPVerifier:              "",
		URLPasswordSalt:          "",
	}
}
