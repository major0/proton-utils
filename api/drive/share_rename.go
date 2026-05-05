package drive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
)

// RenameLinkPayload is the request body for PUT /drive/shares/{shareID}/links/{linkID}/rename.
type RenameLinkPayload struct {
	Name               string `json:"Name"`               // Encrypted new name (PGP message)
	Hash               string `json:"Hash"`               // For root links: random 64 hex chars. For child links: HMAC lookup hash.
	NameSignatureEmail string `json:"NameSignatureEmail"` // Email of the signing address key
	OriginalHash       string `json:"OriginalHash"`       // The link's current Hash value — used for conflict detection
}

// ValidateShareName checks that a share name is valid for use as a root link name.
// Returns an error if the name is empty, contains path separators, or exceeds 255 bytes.
func ValidateShareName(name string) error {
	if name == "" {
		return fmt.Errorf("share name must not be empty")
	}
	if strings.Contains(name, "/") {
		return fmt.Errorf("share name must not contain path separators")
	}
	if len(name) > 255 {
		return fmt.Errorf("share name must not exceed 255 bytes (got %d)", len(name))
	}
	return nil
}

// ShareRename renames a share's root link. Uses the share's private key
// as parent key and a random hex hash (not a lookup hash), matching the
// WebClients behavior for root link renames.
func (c *Client) ShareRename(ctx context.Context, share *Share, newName string) error {
	// Guard: only standard shares can be renamed.
	if share.ProtonShare().Type != proton.ShareTypeStandard {
		return ErrNotStandardShare
	}

	// Validate the new name locally.
	if err := ValidateShareName(newName); err != nil {
		return fmt.Errorf("drive.ShareRename: %w", err)
	}

	shareID := share.Metadata().ShareID
	linkID := share.ProtonShare().LinkID

	// Get the share's keyring (acts as "parent" key for root links).
	shareKR := share.KeyRingValue()
	if shareKR == nil {
		return fmt.Errorf("drive.ShareRename %s: share keyring is nil", shareID)
	}

	// Get the address keyring for signing.
	addrID := share.ProtonShare().AddressID
	addrKR, ok := c.AddressKeyRing(addrID)
	if !ok {
		return fmt.Errorf("drive.ShareRename %s: address keyring not found for %s", shareID, addrID)
	}

	// Encrypt the new name with the share keyring (parent key) and sign
	// with the address keyring. This matches the go-proton-api
	// getEncryptedName pattern: nodeKR.Encrypt(plaintext, addrKR).
	plainName := crypto.NewPlainMessageFromString(newName)
	encMsg, err := shareKR.Encrypt(plainName, addrKR)
	if err != nil {
		return fmt.Errorf("drive.ShareRename %s: encrypt name: %w", shareID, err)
	}

	encNameArmored, err := encMsg.GetArmored()
	if err != nil {
		return fmt.Errorf("drive.ShareRename %s: armor name: %w", shareID, err)
	}

	// Generate random 64-char hex hash (32 random bytes → hex).
	// Root links have no parent hash key, so the WebClients use
	// getRandomString(64) instead of a lookup hash.
	hashBytes := make([]byte, 32)
	if _, err := rand.Read(hashBytes); err != nil {
		return fmt.Errorf("drive.ShareRename %s: random hash: %w", shareID, err)
	}
	hash := hex.EncodeToString(hashBytes)

	// Get the signature email (share creator's address email).
	sigEmail := share.Metadata().Creator

	// Get the original hash from the root link for conflict detection.
	originalHash := share.Link.ProtonLink().Hash

	// Build the rename payload.
	payload := RenameLinkPayload{
		Name:               encNameArmored,
		Hash:               hash,
		NameSignatureEmail: sigEmail,
		OriginalHash:       originalHash,
	}

	// PUT to rename the root link.
	path := fmt.Sprintf("/drive/shares/%s/links/%s/rename", shareID, linkID)
	if err := c.Session.DoJSON(ctx, "PUT", path, payload, nil); err != nil {
		return fmt.Errorf("drive.ShareRename %s: %w", shareID, err)
	}

	return nil
}
