package client

import (
	"context"
	"fmt"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api/drive"
)

// Move moves a link (file or folder) to a new parent directory with a new name.
// The node passphrase is re-encrypted from the old parent's keyring to the new
// parent's keyring.
func (c *Client) Move(ctx context.Context, share *drive.Share, link *drive.Link, newParent *drive.Link, newName string) error {
	if newParent.Type() != proton.LinkTypeFolder {
		return drive.ErrNotAFolder
	}

	newParentKR, err := newParent.KeyRing()
	if err != nil {
		return fmt.Errorf("move: new parent keyring: %w", err)
	}

	addrKR, err := c.addrKRForLink(link)
	if err != nil {
		return fmt.Errorf("move: %w", err)
	}

	sigAddr, err := c.signatureAddress(link)
	if err != nil {
		return fmt.Errorf("move: %w", err)
	}

	req := proton.MoveLinkReq{
		ParentLinkID:            newParent.ProtonLink().LinkID,
		OriginalHash:            link.ProtonLink().Hash,
		NodePassphraseSignature: link.ProtonLink().NodePassphraseSignature,
		SignatureAddress:        sigAddr,
	}

	if err := req.SetName(newName, addrKR, newParentKR); err != nil {
		return fmt.Errorf("move: encrypting name: %w", err)
	}

	hashKey, err := newParent.ProtonLink().GetHashKeyFromParent(newParentKR, addrKR)
	if err != nil {
		return fmt.Errorf("move: hash key: %w", err)
	}
	if err := req.SetHash(newName, hashKey); err != nil {
		return fmt.Errorf("move: hash: %w", err)
	}

	// Re-encrypt the node passphrase from old parent to new parent.
	// Replicate Link.getParentKeyRing() logic: if no parent, use share keyring.
	var oldParentKR *crypto.KeyRing
	if link.ParentLink() != nil {
		oldParentKR, err = link.ParentLink().KeyRing()
	} else {
		oldParentKR = link.Share().KeyRingValue()
		if oldParentKR == nil {
			err = fmt.Errorf("move: share keyring is nil")
		}
	}
	if err != nil {
		return fmt.Errorf("move: old parent keyring: %w", err)
	}

	newPassphrase, err := reencryptKeyPacket(oldParentKR, newParentKR, link.ProtonLink().NodePassphrase)
	if err != nil {
		return fmt.Errorf("move: re-encrypting passphrase: %w", err)
	}
	req.NodePassphrase = newPassphrase

	if err := c.Session.Client.MoveLink(ctx, share.ProtonShare().ShareID, link.ProtonLink().LinkID, req); err != nil {
		return err
	}

	// Invalidate affected Link Table entries. The moved link, old
	// parent, and new parent all have stale children/metadata.
	c.deleteLink(link.ProtonLink().LinkID)
	if link.ParentLink() != nil {
		c.deleteLink(link.ParentLink().ProtonLink().LinkID)
	}
	c.deleteLink(newParent.ProtonLink().LinkID)

	return nil
}

// Rename renames a link in place (same parent directory).
func (c *Client) Rename(ctx context.Context, share *drive.Share, link *drive.Link, newName string) error {
	if link.ParentLink() == nil {
		return fmt.Errorf("rename: cannot rename share root")
	}
	return c.Move(ctx, share, link, link.ParentLink(), newName)
}

// reencryptKeyPacket re-encrypts an armored PGP message from srcKR to dstKR.
func reencryptKeyPacket(srcKR, dstKR *crypto.KeyRing, passphrase string) (string, error) {
	oldSplit, err := crypto.NewPGPSplitMessageFromArmored(passphrase)
	if err != nil {
		return "", err
	}

	sessionKey, err := srcKR.DecryptSessionKey(oldSplit.KeyPacket)
	if err != nil {
		return "", err
	}

	newKeyPacket, err := dstKR.EncryptSessionKey(sessionKey)
	if err != nil {
		return "", err
	}

	newSplit := crypto.NewPGPSplitMessage(newKeyPacket, oldSplit.DataPacket)
	return newSplit.GetArmored()
}
