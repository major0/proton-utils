package client

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api/drive"
)

// FileHandle holds the resolved state needed to populate a CopyEndpoint
// for a Proton Drive file. Returned by CreateFile (for destinations)
// and OpenFile (for sources).
type FileHandle struct {
	Link       *drive.Link
	Share      *drive.Share
	LinkID     string // file link ID (from CreateFileRes or Link.LinkID)
	RevisionID string
	Blocks     []proton.Block     // populated by OpenFile (source)
	SessionKey *crypto.SessionKey // for encrypt (dest) or decrypt (source)
	FileSize   int64              // populated by OpenFile (source)
	ModTime    time.Time          // populated by OpenFile from XAttr (zero if unavailable)

	// Upload-side fields populated by CreateFile.
	NodeKR           *crypto.KeyRing // node keyring for encrypt signatures + XAttr
	AddrKR           *crypto.KeyRing // address keyring for signing
	ShareID          string          // share ID for verification data endpoint
	VolumeID         string          // volume ID for block upload requests
	AddressID        string          // address ID for block upload requests
	SigAddr          string          // signature address for UpdateRevision
	VerificationCode []byte          // raw verification code for block tokens
}

// CreateFile creates a file draft in Proton Drive and returns a
// FileHandle with the RevisionID and SessionKey needed for upload.
// The caller uses these to populate a CopyEndpoint destination.
func (c *Client) CreateFile(ctx context.Context, share *drive.Share, parentLink *drive.Link, name string) (*FileHandle, error) {
	mimeType := detectMIMEType(name)

	parentKR, err := parentLink.KeyRing()
	if err != nil {
		return nil, fmt.Errorf("drive.CreateFile: parent keyring: %w", err)
	}

	addrKR, err := c.addrKRForLink(parentLink)
	if err != nil {
		return nil, fmt.Errorf("drive.CreateFile: address keyring: %w", err)
	}

	sigAddr, err := c.signatureAddress(parentLink)
	if err != nil {
		return nil, fmt.Errorf("drive.CreateFile: signature address: %w", err)
	}

	nodeKey, encPassphrase, passphraseSig, err := generateNodeKeys(parentKR, addrKR)
	if err != nil {
		return nil, fmt.Errorf("drive.CreateFile: generate node keys: %w", err)
	}

	req := proton.CreateFileReq{
		ParentLinkID:            parentLink.ProtonLink().LinkID,
		MIMEType:                mimeType,
		NodeKey:                 nodeKey,
		NodePassphrase:          encPassphrase,
		NodePassphraseSignature: passphraseSig,
		SignatureAddress:        sigAddr,
	}

	nodeKR, err := unlockKeyRing(parentKR, addrKR, nodeKey, encPassphrase, passphraseSig)
	if err != nil {
		return nil, fmt.Errorf("drive.CreateFile: unlock node keyring: %w", err)
	}

	sessionKey, err := req.SetContentKeyPacketAndSignature(nodeKR)
	if err != nil {
		return nil, fmt.Errorf("drive.CreateFile: content key packet: %w", err)
	}

	if err := req.SetName(name, addrKR, parentKR); err != nil {
		return nil, fmt.Errorf("drive.CreateFile: encrypt name: %w", err)
	}

	hashKey, err := parentLink.ProtonLink().GetHashKeyFromParent(parentKR, addrKR)
	if err != nil {
		return nil, fmt.Errorf("drive.CreateFile: hash key: %w", err)
	}
	if err := req.SetHash(name, hashKey); err != nil {
		return nil, fmt.Errorf("drive.CreateFile: name hash: %w", err)
	}

	shareID := share.ProtonShare().ShareID
	res, err := c.Session.Client.CreateFile(ctx, shareID, req)
	if err != nil {
		return nil, fmt.Errorf("drive.CreateFile: %w", err)
	}

	// Fetch verification code for block upload tokens.
	vd, err := c.Session.Client.GetVerificationData(ctx, shareID, res.ID, res.RevisionID)
	if err != nil {
		return nil, fmt.Errorf("drive.CreateFile: verification data: %w", err)
	}
	verifyCode, err := base64.StdEncoding.DecodeString(vd.VerificationCode)
	if err != nil {
		return nil, fmt.Errorf("drive.CreateFile: decode verification code: %w", err)
	}

	return &FileHandle{
		Link:             parentLink,
		Share:            share,
		LinkID:           res.ID,
		RevisionID:       res.RevisionID,
		SessionKey:       sessionKey,
		NodeKR:           nodeKR,
		AddrKR:           addrKR,
		ShareID:          shareID,
		VolumeID:         share.ProtonShare().VolumeID,
		AddressID:        share.ProtonShare().AddressID,
		SigAddr:          sigAddr,
		VerificationCode: verifyCode,
	}, nil
}

// OpenFile prepares a Proton Drive file for reading by fetching the
// revision block list and deriving the session key. Returns a
// FileHandle with the info needed to populate a CopyEndpoint source.
func (c *Client) OpenFile(ctx context.Context, link *drive.Link) (*FileHandle, error) {
	if link.Type() != proton.LinkTypeFile {
		return nil, fmt.Errorf("drive.OpenFile: %s: not a file", link.LinkID())
	}

	pLink := link.ProtonLink()
	if pLink.FileProperties == nil {
		return nil, fmt.Errorf("drive.OpenFile: %s: no file properties", link.LinkID())
	}

	shareID := link.Share().ProtonShare().ShareID
	revisionID := pLink.FileProperties.ActiveRevision.ID

	revision, err := c.Session.Client.GetRevisionAllBlocks(ctx, shareID, link.LinkID(), revisionID)
	if err != nil {
		return nil, fmt.Errorf("drive.OpenFile: %s: get revision: %w", link.LinkID(), err)
	}

	nodeKR, err := link.KeyRing()
	if err != nil {
		return nil, fmt.Errorf("drive.OpenFile: %s: keyring: %w", link.LinkID(), err)
	}

	sessionKey, err := pLink.GetSessionKey(nodeKR)
	if err != nil {
		return nil, fmt.Errorf("drive.OpenFile: %s: session key: %w", link.LinkID(), err)
	}

	// Extract mtime from revision XAttr if available.
	var modTime time.Time
	addrKR, err := c.addrKRForLink(link)
	if err == nil {
		xattr, xErr := revision.GetDecXAttrString(addrKR, nodeKR)
		if xErr == nil && xattr != nil {
			if xattr.ModificationTime != "" {
				if mt, tErr := time.Parse(time.RFC3339, xattr.ModificationTime); tErr == nil {
					modTime = mt
				}
			}
		}
	}

	fileSize := pLink.FileProperties.ActiveRevision.Size

	return &FileHandle{
		Link:       link,
		Share:      link.Share(),
		LinkID:     link.LinkID(),
		RevisionID: revisionID,
		Blocks:     revision.Blocks,
		SessionKey: sessionKey,
		FileSize:   fileSize,
		ModTime:    modTime,
	}, nil
}
