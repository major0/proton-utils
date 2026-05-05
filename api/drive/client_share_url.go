package drive

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/ProtonMail/gopenpgp/v2/crypto"
)

// ListShareURLs returns all ShareURLs for a share.
func (c *Client) ListShareURLs(ctx context.Context, shareID string) ([]ShareURL, error) {
	path := fmt.Sprintf("/drive/shares/%s/urls", shareID)
	var resp ShareURLsResponse
	if err := c.Session.DoJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("drive.ListShareURLs %s: %w", shareID, err)
	}
	return resp.ShareURLs, nil
}

// CreateShareURL creates a ShareURL with a generated password.
// Returns the plaintext password and the created ShareURL.
func (c *Client) CreateShareURL(ctx context.Context, share *Share) (string, *ShareURL, error) {
	shareID := share.Metadata().ShareID

	// Guard: check if URL already exists.
	existing, err := c.ListShareURLs(ctx, shareID)
	if err != nil {
		return "", nil, fmt.Errorf("drive.CreateShareURL %s: list: %w", shareID, err)
	}
	if len(existing) > 0 {
		return "", nil, ErrShareURLExists
	}

	// Generate 32-char random password.
	randBytes, err := crypto.RandomToken(32)
	if err != nil {
		return "", nil, fmt.Errorf("drive.CreateShareURL %s: random: %w", shareID, err)
	}
	password := base64.RawURLEncoding.EncodeToString(randBytes)[:32]

	// Get address keyring for decryption/signing and public keyring for encryption.
	addrID := share.ProtonShare().AddressID
	addrKR, ok := c.AddressKeyRing(addrID)
	if !ok {
		return "", nil, fmt.Errorf("drive.CreateShareURL %s: address keyring not found", shareID)
	}

	// Fetch public keys for encryption (addrKR is private-only, can't encrypt-to).
	creatorEmail := share.Metadata().Creator
	pubKeys, _, err := c.Session.Client.GetPublicKeys(ctx, creatorEmail)
	if err != nil {
		return "", nil, fmt.Errorf("drive.CreateShareURL %s: fetch public keys: %w", shareID, err)
	}
	pubKR, err := pubKeys.GetKeyRing()
	if err != nil {
		return "", nil, fmt.Errorf("drive.CreateShareURL %s: build public keyring: %w", shareID, err)
	}

	// Encrypt password with address public key.
	encPassword, err := encryptShareURLPassword(password, pubKR)
	if err != nil {
		return "", nil, fmt.Errorf("drive.CreateShareURL %s: %w", shareID, err)
	}

	// Generate salt and passphrase from password.
	salt, passphrase, err := generateKeySaltAndPassphrase(password)
	if err != nil {
		return "", nil, fmt.Errorf("drive.CreateShareURL %s: %w", shareID, err)
	}

	// Get share session key and encrypt with passphrase.
	sessionKey, err := c.shareSessionKey(share, addrKR)
	if err != nil {
		return "", nil, fmt.Errorf("drive.CreateShareURL %s: %w", shareID, err)
	}

	sharePassphraseKeyPacket, err := encryptShareSessionKey(sessionKey, passphrase)
	if err != nil {
		return "", nil, fmt.Errorf("drive.CreateShareURL %s: %w", shareID, err)
	}

	// Generate SRP verifier.
	srpResult, err := generateSRPVerifier(ctx, c, password)
	if err != nil {
		return "", nil, fmt.Errorf("drive.CreateShareURL %s: %w", shareID, err)
	}

	// Build payload.
	payload := CreateShareURLPayload{
		Flags:                    2, // GeneratedPasswordIncluded
		Permissions:              4, // viewer
		MaxAccesses:              0, // unlimited
		CreatorEmail:             creatorEmail,
		ExpirationDuration:       nil,
		SharePassphraseKeyPacket: sharePassphraseKeyPacket,
		SharePasswordSalt:        salt,
		Password:                 encPassword,
		SRPModulusID:             srpResult.ModulusID,
		SRPVerifier:              srpResult.Verifier,
		URLPasswordSalt:          srpResult.Salt,
	}

	// POST to create the ShareURL.
	apiPath := fmt.Sprintf("/drive/shares/%s/urls", shareID)
	var resp struct {
		Code     int      `json:"Code"`
		ShareURL ShareURL `json:"ShareURL"`
	}
	if err := c.Session.DoJSON(ctx, "POST", apiPath, payload, &resp); err != nil {
		return "", nil, fmt.Errorf("drive.CreateShareURL %s: %w", shareID, err)
	}

	return password, &resp.ShareURL, nil
}

// DeleteShareURL deletes a ShareURL.
func (c *Client) DeleteShareURL(ctx context.Context, shareID, shareURLID string) error {
	path := fmt.Sprintf("/drive/shares/%s/urls/%s", shareID, shareURLID)
	if err := c.Session.DoJSON(ctx, "DELETE", path, nil, nil); err != nil {
		return fmt.Errorf("drive.DeleteShareURL %s/%s: %w", shareID, shareURLID, err)
	}
	return nil
}

// UpdateShareURLPassword changes the password on an existing ShareURL.
// If password is empty, disables the password (URL remains active but unprotected).
func (c *Client) UpdateShareURLPassword(ctx context.Context, share *Share, shareURL *ShareURL, password string) error {
	shareID := share.Metadata().ShareID

	// Guard: must have an existing URL.
	if shareURL == nil {
		return ErrNoShareURL
	}

	// Empty password = disable.
	if password == "" {
		payload := passwordDisablePayload()
		path := fmt.Sprintf("/drive/shares/%s/urls/%s", shareID, shareURL.ShareURLID)
		if err := c.Session.DoJSON(ctx, "PUT", path, payload, nil); err != nil {
			return fmt.Errorf("drive.UpdateShareURLPassword %s: %w", shareID, err)
		}
		return nil
	}

	// Non-empty password: full crypto pipeline.
	addrID := share.ProtonShare().AddressID
	addrKR, ok := c.AddressKeyRing(addrID)
	if !ok {
		return fmt.Errorf("drive.UpdateShareURLPassword %s: address keyring not found", shareID)
	}

	// Fetch public keys for encryption (addrKR is private-only, can't encrypt-to).
	creatorEmail := share.Metadata().Creator
	pubKeys, _, err := c.Session.Client.GetPublicKeys(ctx, creatorEmail)
	if err != nil {
		return fmt.Errorf("drive.UpdateShareURLPassword %s: fetch public keys: %w", shareID, err)
	}
	pubKR, err := pubKeys.GetKeyRing()
	if err != nil {
		return fmt.Errorf("drive.UpdateShareURLPassword %s: build public keyring: %w", shareID, err)
	}

	encPassword, err := encryptShareURLPassword(password, pubKR)
	if err != nil {
		return fmt.Errorf("drive.UpdateShareURLPassword %s: %w", shareID, err)
	}

	salt, passphrase, err := generateKeySaltAndPassphrase(password)
	if err != nil {
		return fmt.Errorf("drive.UpdateShareURLPassword %s: %w", shareID, err)
	}

	// Re-derive session key.
	sessionKey, err := c.shareSessionKey(share, addrKR)
	if err != nil {
		return fmt.Errorf("drive.UpdateShareURLPassword %s: %w", shareID, err)
	}

	sharePassphraseKeyPacket, err := encryptShareSessionKey(sessionKey, passphrase)
	if err != nil {
		return fmt.Errorf("drive.UpdateShareURLPassword %s: %w", shareID, err)
	}

	srpResult, err := generateSRPVerifier(ctx, c, password)
	if err != nil {
		return fmt.Errorf("drive.UpdateShareURLPassword %s: %w", shareID, err)
	}

	payload := UpdateShareURLPayload{
		Flags:                    2, // GeneratedPasswordIncluded
		Permissions:              4, // viewer
		MaxAccesses:              0, // unlimited
		SharePassphraseKeyPacket: sharePassphraseKeyPacket,
		SharePasswordSalt:        salt,
		Password:                 encPassword,
		SRPModulusID:             srpResult.ModulusID,
		SRPVerifier:              srpResult.Verifier,
		URLPasswordSalt:          srpResult.Salt,
	}

	path := fmt.Sprintf("/drive/shares/%s/urls/%s", shareID, shareURL.ShareURLID)
	if err := c.Session.DoJSON(ctx, "PUT", path, payload, nil); err != nil {
		return fmt.Errorf("drive.UpdateShareURLPassword %s: %w", shareID, err)
	}
	return nil
}

// DecryptShareURLPassword decrypts the Password field of a ShareURL
// using the address private key.
func (c *Client) DecryptShareURLPassword(_ context.Context, share *Share, shareURL *ShareURL) (string, error) {
	if shareURL == nil || shareURL.Password == "" {
		return "", ErrNoShareURL
	}

	addrID := share.ProtonShare().AddressID
	addrKR, ok := c.AddressKeyRing(addrID)
	if !ok {
		return "", fmt.Errorf("drive.DecryptShareURLPassword: address keyring not found for %s", addrID)
	}

	password, err := decryptShareURLPassword(shareURL.Password, addrKR)
	if err != nil {
		return "", fmt.Errorf("drive.DecryptShareURLPassword %s: %w", share.Metadata().ShareID, err)
	}
	return password, nil
}

// shareSessionKey extracts the session key from the share's encrypted
// passphrase. The share passphrase is a PGP message encrypted to the
// address keyring — we split it into key packet + data and decrypt the
// key packet to obtain the session key.
func (c *Client) shareSessionKey(share *Share, addrKR *crypto.KeyRing) (*crypto.SessionKey, error) {
	sharePassphrase := share.ProtonShare().Passphrase
	encMsg, err := crypto.NewPGPMessageFromArmored(sharePassphrase)
	if err != nil {
		return nil, fmt.Errorf("parse share passphrase: %w", err)
	}

	splitMsg, err := encMsg.SeparateKeyAndData(len(encMsg.GetBinary()), 0)
	if err != nil {
		return nil, fmt.Errorf("split share passphrase: %w", err)
	}

	sessionKey, err := addrKR.DecryptSessionKey(splitMsg.GetBinaryKeyPacket())
	if err != nil {
		// Fallback: try with share keyring.
		shareKR := share.KeyRingValue()
		if shareKR != nil {
			sessionKey, err = shareKR.DecryptSessionKey(splitMsg.GetBinaryKeyPacket())
		}
		if err != nil {
			return nil, fmt.Errorf("decrypt session key: %w", err)
		}
	}

	return sessionKey, nil
}
