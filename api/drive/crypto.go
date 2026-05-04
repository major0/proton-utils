package drive

import (
	"encoding/base64"
	"fmt"

	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/ProtonMail/gopenpgp/v2/helper"
	"github.com/major0/proton-cli/api"
)

// generateNodeKeys creates a new node key pair for a Drive link. The
// passphrase is encrypted with parentKR and signed with addrKR.
// Returns (armoredKey, encryptedPassphrase, passphraseSignature).
func generateNodeKeys(parentKR, addrKR *crypto.KeyRing) (string, string, string, error) {
	passphrase, err := crypto.RandomToken(32)
	if err != nil {
		return "", "", "", err
	}

	passphraseB64 := base64.StdEncoding.EncodeToString(passphrase)

	key, err := helper.GenerateKey("Drive key", "noreply@protonmail.com", []byte(passphraseB64), "x25519", 0)
	if err != nil {
		return "", "", "", err
	}

	plainPassphrase := crypto.NewPlainMessage([]byte(passphraseB64))

	enc, err := parentKR.Encrypt(plainPassphrase, nil)
	if err != nil {
		return "", "", "", err
	}

	encArm, err := enc.GetArmored()
	if err != nil {
		return "", "", "", err
	}

	sig, err := addrKR.SignDetached(plainPassphrase)
	if err != nil {
		return "", "", "", err
	}

	sigArm, err := sig.GetArmored()
	if err != nil {
		return "", "", "", err
	}

	return key, encArm, sigArm, nil
}

// unlockKeyRing decrypts a node key using the parent keyring and the
// encrypted passphrase. The signature is verified against addrKR.
func unlockKeyRing(parentKR, addrKR *crypto.KeyRing, key, passphrase, passphraseSig string) (*crypto.KeyRing, error) {
	enc, err := crypto.NewPGPMessageFromArmored(passphrase)
	if err != nil {
		return nil, err
	}

	dec, err := parentKR.Decrypt(enc, nil, crypto.GetUnixTime())
	if err != nil {
		return nil, err
	}

	sig, err := crypto.NewPGPSignatureFromArmored(passphraseSig)
	if err != nil {
		return nil, err
	}

	if err := addrKR.VerifyDetached(dec, sig, crypto.GetUnixTime()); err != nil {
		return nil, err
	}

	lockedKey, err := crypto.NewKeyFromArmored(key)
	if err != nil {
		return nil, err
	}

	unlockedKey, err := lockedKey.Unlock(dec.GetBinary())
	if err != nil {
		return nil, err
	}

	return crypto.NewKeyRing(unlockedKey)
}

// addrKRForLink returns the address keyring for the link's signature email.
// Returns an error if no matching keyring is found.
func (c *Client) addrKRForLink(l *Link) (*crypto.KeyRing, error) {
	if addr, ok := c.addresses[l.ProtonLink().SignatureEmail]; ok {
		if kr, ok := c.addressKeyRings[addr.ID]; ok {
			return kr, nil
		}
	}
	return nil, fmt.Errorf("addrKRForLink %s: %w", l.ProtonLink().SignatureEmail, api.ErrKeyNotFound)
}

// signatureAddress returns the signature email address for the link.
// Returns an error if no address is available.
func (c *Client) signatureAddress(l *Link) (string, error) {
	if l.ProtonLink().SignatureEmail != "" {
		return l.ProtonLink().SignatureEmail, nil
	}
	return "", fmt.Errorf("signatureAddress %s: %w", l.ProtonLink().LinkID, api.ErrKeyNotFound)
}
