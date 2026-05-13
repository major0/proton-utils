package drive

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
)

// ListSharesMetadata returns metadata for all shares visible to this session.
func (c *Client) ListSharesMetadata(ctx context.Context, all bool) ([]ShareMetadata, error) {
	pShares, err := c.Session.Client.ListShares(ctx, all)
	if err != nil {
		return nil, err
	}

	shares := make([]ShareMetadata, len(pShares))
	for i := range pShares {
		shares[i] = ShareMetadata(pShares[i])
	}
	return shares, nil
}

// GetShareMetadata returns the metadata for the share with the given ID.
// If metas is non-nil, searches the provided list instead of calling the API.
func (c *Client) GetShareMetadata(ctx context.Context, id string, metas []ShareMetadata) (ShareMetadata, error) {
	if metas == nil {
		var err error
		metas, err = c.ListSharesMetadata(ctx, true)
		if err != nil {
			return ShareMetadata{}, err
		}
	}

	for _, meta := range metas {
		if meta.ShareID == id {
			return meta, nil
		}
	}

	return ShareMetadata{}, nil
}

// ListShares returns all fully-resolved shares visible to this session.
func (c *Client) ListShares(ctx context.Context, all bool) ([]Share, error) {
	return c.listShares(ctx, "", all)
}

func (c *Client) listShares(ctx context.Context, volumeID string, all bool) ([]Share, error) {
	pshares, err := c.Session.Client.ListShares(ctx, all)
	if err != nil {
		return nil, err
	}

	slog.Debug("ListShares", "shares", len(pshares))
	slog.Debug("ListShares", "volumeID", volumeID)

	var mu sync.Mutex
	var wg sync.WaitGroup
	shares := make([]Share, 0, len(pshares))

	for _, s := range pshares {
		if volumeID != "" && volumeID != s.VolumeID {
			continue
		}
		shareID := s.ShareID
		c.Session.Sem.Go(&wg, func(ctx context.Context) error {
			share, err := c.GetShare(ctx, shareID)
			if err != nil {
				slog.Error("worker", "shareID", shareID, "error", err)
				return nil
			}
			mu.Lock()
			shares = append(shares, *share)
			mu.Unlock()
			return nil
		})
	}

	wg.Wait()
	return shares, nil
}

// GetShare returns the fully-resolved share with the given ID.
func (c *Client) GetShare(ctx context.Context, id string) (*Share, error) {
	pShare, err := c.Session.Client.GetShare(ctx, id)
	if err != nil {
		return nil, err
	}

	shareAddrKR, ok := c.addressKeyRings[pShare.AddressID]
	if !ok {
		return nil, fmt.Errorf("GetShare %s: address keyring not found for %s", id, pShare.AddressID)
	}

	shareKR, err := pShare.GetKeyRing(shareAddrKR)
	if err != nil {
		return nil, err
	}

	pLink, err := c.Session.Client.GetLink(ctx, pShare.ShareID, pShare.LinkID)
	if err != nil {
		return nil, err
	}

	share := NewShare(&pShare, shareKR, nil, c, pShare.VolumeID)
	link := NewLink(&pLink, nil, share, c)
	// Set the link on the share after construction to break the circular reference.
	share.Link = link

	// Insert root link into the Link Table for pointer identity.
	c.putLink(pLink.LinkID, link)

	// Apply per-share cache config (may construct objectCache).
	c.applyShareConfig(share)

	return share, nil
}

// applyShareConfig sets cache levels on a share based on the loaded config.
// Root and photos shares are always forced to disabled. Looks up the share
// by its ShareID (not decrypted name).
func (c *Client) applyShareConfig(share *Share) {
	// Root and photos shares: caching prohibited.
	st := share.ProtonShare().Type
	if st == proton.ShareTypeMain || st == ShareTypePhotos {
		share.MemoryCacheLevel = api.CacheDisabled
		share.DiskCacheLevel = api.DiskCacheDisabled
		return
	}

	if c.Config == nil {
		return
	}

	sc, ok := c.Config.Shares[share.Metadata().ShareID]
	if !ok {
		return
	}

	share.MemoryCacheLevel = sc.MemoryCache
	share.DiskCacheLevel = sc.DiskCache
}

// MainShare returns the user's main volume share.
// This is the most common share resolution in cmd/ — a named convenience
// over ResolveShareByType(ctx, proton.ShareTypeMain).
func (c *Client) MainShare(ctx context.Context) (*Share, error) {
	return c.ResolveShareByType(ctx, proton.ShareTypeMain)
}

// ResolveShareByType finds a share by its type (Main, Photos, etc.)
// without decrypting share names. Uses metadata to find the type match,
// then resolves only that share.
func (c *Client) ResolveShareByType(ctx context.Context, st proton.ShareType) (*Share, error) {
	metas, err := c.ListSharesMetadata(ctx, true)
	if err != nil {
		return nil, err
	}
	for _, meta := range metas {
		if meta.Type == st {
			return c.GetShare(ctx, meta.ShareID)
		}
	}
	return nil, ErrFileNotFound
}

// ResolveShare finds a share by name or ShareID prefix.
// Full-scans all shares: tries nameOrID as a share name first, then as a
// ShareID prefix (case-sensitive). Returns an ambiguity error if both
// interpretations match different shares, or if multiple shares match.
func (c *Client) ResolveShare(ctx context.Context, nameOrID string, all bool) (*Share, error) {
	metas, err := c.ListSharesMetadata(ctx, all)
	if err != nil {
		return nil, err
	}

	var nameMatch *Share
	var nameMatchCount int
	var idMatch *Share
	var idMatchCount int

	for _, meta := range metas {
		// Check ID prefix (cheap, no decryption needed).
		isIDMatch := strings.HasPrefix(meta.ShareID, nameOrID)

		// Always resolve the share for name check (full scan).
		share, err := c.GetShare(ctx, meta.ShareID)
		if err != nil {
			slog.Debug("ResolveShare: skip", "shareID", meta.ShareID, "error", err)
			continue
		}

		if isIDMatch {
			idMatch = share
			idMatchCount++
		}

		shareName, err := share.Link.Name()
		if err != nil {
			continue
		}
		if shareName == nameOrID {
			nameMatch = share
			nameMatchCount++
		}
	}

	// Resolution logic.
	switch {
	case nameMatchCount == 1 && idMatchCount == 0:
		return nameMatch, nil
	case nameMatchCount == 0 && idMatchCount == 1:
		return idMatch, nil
	case nameMatchCount == 1 && idMatchCount == 1:
		// Both match — same share or different?
		if nameMatch.Metadata().ShareID == idMatch.Metadata().ShareID {
			return nameMatch, nil
		}
		return nil, fmt.Errorf("ambiguous: %q matches share name %q and ID prefix %q — use full ID to disambiguate",
			nameOrID, nameMatch.Metadata().ShareID, idMatch.Metadata().ShareID)
	case nameMatchCount > 1:
		return nil, fmt.Errorf("ambiguous: multiple shares named %q — use share ID to disambiguate", nameOrID)
	case idMatchCount > 1:
		return nil, fmt.Errorf("ambiguous: %q matches multiple share IDs — use a longer prefix", nameOrID)
	default:
		return nil, ErrFileNotFound
	}
}

// ResolvePath resolves a slash-separated path to a link across all shares.
func (c *Client) ResolvePath(ctx context.Context, path string, all bool) (*Link, error) {
	parts := strings.Split(path, "/")

	if len(parts) == 0 {
		return nil, ErrInvalidPath
	}

	share, err := c.ResolveShare(ctx, parts[0], all)
	if err != nil {
		return nil, err
	}

	return share.Link.ResolvePath(ctx, strings.Join(parts[1:], "/"), all)
}

// ShareLink creates a new share from an existing link. It encapsulates the
// full share-creation flow: "already shared" check, keyring derivation from
// link.Share(), crypto generation, API call, and optional rename.
// If name is non-empty, the new share is renamed after creation.
func (c *Client) ShareLink(ctx context.Context, link *Link, name string) (*Share, error) {
	// Check if the link is already shared.
	metas, err := c.ListSharesMetadata(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("ShareLink: listing shares: %w", err)
	}
	for _, meta := range metas {
		if meta.LinkID == link.LinkID() {
			return nil, fmt.Errorf("ShareLink: %s: already shared", link.LinkID())
		}
	}

	// Get the containing share for keyring derivation.
	linkShare := link.Share()
	if linkShare == nil {
		return nil, fmt.Errorf("ShareLink: %s: link has no share", link.LinkID())
	}

	// Get the link's node keyring.
	linkNodeKR, err := link.KeyRing()
	if err != nil {
		return nil, fmt.Errorf("ShareLink: %s: link keyring: %w", link.LinkID(), err)
	}

	// Get the address keyring from the containing share's address.
	addrID := linkShare.ProtonShare().AddressID
	addrKR, ok := c.AddressKeyRing(addrID)
	if !ok {
		return nil, fmt.Errorf("ShareLink: address keyring not found for %s", addrID)
	}

	// Derive parent keyring: parent link's keyring, or share keyring for roots.
	var parentKR *crypto.KeyRing
	if link.ParentLink() != nil {
		parentKR, err = link.ParentLink().KeyRing()
		if err != nil {
			return nil, fmt.Errorf("ShareLink: parent keyring: %w", err)
		}
	} else {
		parentKR = linkShare.KeyRingValue()
	}

	// Raw link fields (same package — direct access to unexported fields).
	linkPassphrase := link.protonLink.NodePassphrase
	linkEncName := link.protonLink.Name

	// Generate share crypto material.
	shareKey, sharePassphrase, sharePassphraseSig, ppKP, nameKP, err := GenerateShareCrypto(
		addrKR, linkNodeKR, parentKR, linkPassphrase, linkEncName,
	)
	if err != nil {
		return nil, fmt.Errorf("ShareLink: %s: %w", link.LinkID(), err)
	}

	// Build payload and create the share.
	payload := CreateDriveSharePayload{
		AddressID:                addrID,
		RootLinkID:               link.LinkID(),
		ShareKey:                 shareKey,
		SharePassphrase:          sharePassphrase,
		SharePassphraseSignature: sharePassphraseSig,
		PassphraseKeyPacket:      ppKP,
		NameKeyPacket:            nameKP,
	}

	volumeID := link.VolumeID()
	shareID, err := c.CreateShareFromLink(ctx, volumeID, payload)
	if err != nil {
		return nil, fmt.Errorf("ShareLink: %s: %w", link.LinkID(), err)
	}

	// Resolve the newly created share.
	resolved, err := c.GetShare(ctx, shareID)
	if err != nil {
		return nil, fmt.Errorf("ShareLink: resolve new share: %w", err)
	}

	// Rename if a name was provided.
	if name != "" {
		if err := c.ShareRename(ctx, resolved, name); err != nil {
			return nil, fmt.Errorf("ShareLink: rename: %w", err)
		}
	}

	return resolved, nil
}
