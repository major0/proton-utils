package drive

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
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
// by its decrypted root link name.
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

	// Look up by decrypted name.
	name, err := share.Link.Name()
	if err != nil {
		return
	}

	sc, ok := c.Config.Shares[name]
	if !ok {
		return // defaults to disabled
	}

	share.MemoryCacheLevel = sc.MemoryCache
	share.DiskCacheLevel = sc.DiskCache
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

// ResolveShare finds a share by its root link name.
// Fetches metadata first, then decrypts shares one at a time until a
// match is found — avoids decrypting all shares upfront.
func (c *Client) ResolveShare(ctx context.Context, name string, all bool) (*Share, error) {
	metas, err := c.ListSharesMetadata(ctx, all)
	if err != nil {
		return nil, err
	}

	for _, meta := range metas {
		share, err := c.GetShare(ctx, meta.ShareID)
		if err != nil {
			slog.Debug("ResolveShare: skip", "shareID", meta.ShareID, "error", err)
			continue
		}
		shareName, err := share.Link.Name()
		if err != nil {
			continue
		}
		if shareName == name {
			return share, nil
		}
	}

	return nil, ErrFileNotFound
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
