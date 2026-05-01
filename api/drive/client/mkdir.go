package client

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
)

// MkDir creates a new folder under the given parent link. Returns the
// newly created Link (lazily decrypted). The parent must be a folder.
func (c *Client) MkDir(ctx context.Context, share *drive.Share, parent *drive.Link, name string) (*drive.Link, error) {
	if parent.Type() != proton.LinkTypeFolder {
		return nil, drive.ErrNotAFolder
	}

	parentKR, err := parent.KeyRing()
	if err != nil {
		return nil, fmt.Errorf("mkdir %s: parent keyring: %w", name, err)
	}

	addrKR, err := c.addrKRForLink(parent)
	if err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", name, err)
	}

	nodeKey, nodePassphraseEnc, nodePassphraseSig, err := generateNodeKeys(parentKR, addrKR)
	if err != nil {
		return nil, fmt.Errorf("mkdir %s: generating keys: %w", name, err)
	}

	sigAddr, err := c.signatureAddress(parent)
	if err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", name, err)
	}

	req := proton.CreateFolderReq{
		ParentLinkID:            parent.ProtonLink().LinkID,
		NodeKey:                 nodeKey,
		NodePassphrase:          nodePassphraseEnc,
		NodePassphraseSignature: nodePassphraseSig,
		SignatureAddress:        sigAddr,
	}

	if err := req.SetName(name, addrKR, parentKR); err != nil {
		return nil, fmt.Errorf("mkdir %s: encrypting name: %w", name, err)
	}

	hashKey, err := parent.ProtonLink().GetHashKeyFromParent(parentKR, addrKR)
	if err != nil {
		return nil, fmt.Errorf("mkdir %s: hash key: %w", name, err)
	}
	if err := req.SetHash(name, hashKey); err != nil {
		return nil, fmt.Errorf("mkdir %s: hash: %w", name, err)
	}

	newNodeKR, err := unlockKeyRing(parentKR, addrKR, nodeKey, nodePassphraseEnc, nodePassphraseSig)
	if err != nil {
		return nil, fmt.Errorf("mkdir %s: unlock keyring: %w", name, err)
	}
	if err := req.SetNodeHashKey(newNodeKR); err != nil {
		return nil, fmt.Errorf("mkdir %s: node hash key: %w", name, err)
	}

	res, err := c.Session.Client.CreateFolder(ctx, share.ProtonShare().ShareID, req)
	if err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", name, err)
	}

	// Invalidate the parent from the Link Table — its children list
	// is now stale.
	c.deleteLink(parent.ProtonLink().LinkID)

	return c.StatLink(ctx, share, parent, res.ID)
}

// MkDirAll creates a directory path, creating any missing intermediate
// directories. Like mkdir -p. Returns the final (deepest) Link.
func (c *Client) MkDirAll(ctx context.Context, share *drive.Share, root *drive.Link, path string) (*drive.Link, error) {
	path = strings.Trim(path, "/")
	if path == "" {
		return root, nil
	}

	parts := strings.Split(path, "/")
	current := root

	for _, name := range parts {
		if name == "" || name == "." {
			continue
		}

		child, err := current.Lookup(ctx, name)
		if err != nil {
			return nil, err
		}

		if child != nil {
			if child.Type() != proton.LinkTypeFolder {
				return nil, fmt.Errorf("mkdir -p: %s: %w", name, drive.ErrNotAFolder)
			}
			current = child
			continue
		}

		newDir, err := c.MkDir(ctx, share, current, name)
		if err != nil {
			if errors.Is(err, proton.ErrFolderNameExist) {
				child, findErr := current.Lookup(ctx, name)
				if findErr != nil {
					return nil, findErr
				}
				if child != nil {
					current = child
					continue
				}
			}
			return nil, err
		}

		current = newDir
	}

	return current, nil
}
