package lumo

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"

	pgpcrypto "github.com/ProtonMail/gopenpgp/v2/crypto"
)

// masterKeyFields holds the cached master key state. These fields are
// added to Client via embedding to keep the main struct clean.
type masterKeyFields struct {
	masterKey     []byte
	masterKeyOnce sync.Once
	masterKeyErr  error
}

// GetMasterKey returns the unwrapped master key, fetching and caching
// it on first call. Subsequent calls return the cached result.
func (c *Client) GetMasterKey(ctx context.Context) ([]byte, error) {
	c.masterKeyOnce.Do(func() {
		c.masterKey, c.masterKeyErr = c.fetchMasterKey(ctx)
	})
	if c.masterKeyErr != nil {
		return nil, c.masterKeyErr
	}
	// Return a copy to prevent callers from mutating the cached key.
	out := make([]byte, len(c.masterKey))
	copy(out, c.masterKey)
	return out, nil
}

// fetchMasterKey fetches master keys from the API, selects the best one,
// and PGP-decrypts it. If no keys exist, it creates a new one.
func (c *Client) fetchMasterKey(ctx context.Context) ([]byte, error) {
	var resp ListMasterKeysResponse
	err := c.Session.DoJSON(ctx, "GET", c.url("/lumo/v1/masterkeys"), nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("lumo: get master keys: %w", err)
	}

	if resp.Eligibility != EligibilityEligible && len(resp.MasterKeys) == 0 {
		return nil, fmt.Errorf("lumo: get master keys: %w", ErrNotEligible)
	}

	if len(resp.MasterKeys) == 0 {
		return c.createMasterKey(ctx)
	}

	best, err := SelectBestMasterKey(resp.MasterKeys)
	if err != nil {
		return nil, fmt.Errorf("lumo: select master key: %w", err)
	}

	return c.unwrapMasterKey(best.MasterKey)
}

// unwrapMasterKey PGP-decrypts a master key using the session's user keyring.
// The key may be ASCII-armored or base64-encoded binary PGP.
func (c *Client) unwrapMasterKey(key string) ([]byte, error) {
	var msg *pgpcrypto.PGPMessage

	if strings.HasPrefix(key, "-----BEGIN") {
		var err error
		msg, err = pgpcrypto.NewPGPMessageFromArmored(key)
		if err != nil {
			return nil, fmt.Errorf("lumo: parse master key: %w", err)
		}
	} else {
		raw, err := base64.StdEncoding.DecodeString(key)
		if err != nil {
			return nil, fmt.Errorf("lumo: decode master key: %w", err)
		}
		msg = pgpcrypto.NewPGPMessage(raw)
	}

	plain, err := c.Session.UserKeyRing.Decrypt(msg, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("lumo: decrypt master key: %w", err)
	}

	return plain.GetBinary(), nil
}

// createMasterKey generates a new 32-byte AES-KW key, PGP-encrypts it
// with the user's keyring, POSTs it to the API, and returns the raw bytes.
func (c *Client) createMasterKey(ctx context.Context) ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("lumo: generate master key: %w", err)
	}

	// PGP-encrypt the raw key bytes and base64-encode the binary output
	// to match the API's format (base64 binary PGP, not ASCII armor).
	plainMsg := pgpcrypto.NewPlainMessage(key)
	encMsg, err := c.Session.UserKeyRing.Encrypt(plainMsg, nil)
	if err != nil {
		return nil, fmt.Errorf("lumo: encrypt master key: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(encMsg.GetBinary())

	req := CreateMasterKeyReq{MasterKey: encoded}
	err = c.Session.DoJSON(ctx, "POST", c.url("/lumo/v1/masterkeys"), req, nil)
	if err != nil {
		return nil, fmt.Errorf("lumo: create master key: %w", err)
	}

	return key, nil
}

// GenerateSpaceKey generates a new 32-byte AES-GCM key for a space.
func GenerateSpaceKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("lumo: generate space key: %w", err)
	}
	return key, nil
}

// GenerateTag generates a new random UUID v4 tag.
func GenerateTag() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("lumo: generate tag: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
