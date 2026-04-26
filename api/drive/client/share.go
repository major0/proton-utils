package client

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
)

// ListSharesMetadata returns metadata for all shares visible to this session.
func (c *Client) ListSharesMetadata(ctx context.Context, all bool) ([]drive.ShareMetadata, error) {
	pShares, err := c.Session.Client.ListShares(ctx, all)
	if err != nil {
		return nil, err
	}

	shares := make([]drive.ShareMetadata, len(pShares))
	for i := range pShares {
		shares[i] = drive.ShareMetadata(pShares[i])
	}
	return shares, nil
}

// GetShareMetadata returns the metadata for the share with the given ID.
// If metas is non-nil, searches the provided list instead of calling the API.
func (c *Client) GetShareMetadata(ctx context.Context, id string, metas []drive.ShareMetadata) (drive.ShareMetadata, error) {
	if metas == nil {
		var err error
		metas, err = c.ListSharesMetadata(ctx, true)
		if err != nil {
			return drive.ShareMetadata{}, err
		}
	}

	for _, meta := range metas {
		if meta.ShareID == id {
			return meta, nil
		}
	}

	return drive.ShareMetadata{}, nil
}

// ListShares returns all fully-resolved shares visible to this session.
func (c *Client) ListShares(ctx context.Context, all bool) ([]drive.Share, error) {
	return c.listShares(ctx, "", all)
}

func (c *Client) listShares(ctx context.Context, volumeID string, all bool) ([]drive.Share, error) {
	pshares, err := c.Session.Client.ListShares(ctx, all)
	if err != nil {
		return nil, err
	}

	slog.Debug("client.ListShares", "shares", len(pshares))
	slog.Debug("client.ListShares", "volumeID", volumeID)

	var mu sync.Mutex
	var wg sync.WaitGroup
	shares := make([]drive.Share, 0, len(pshares))

	for _, s := range pshares {
		if volumeID != "" && volumeID != s.VolumeID {
			continue
		}
		shareID := s.ShareID
		c.Session.Pool.Go(&wg, func(ctx context.Context) error {
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
func (c *Client) GetShare(ctx context.Context, id string) (*drive.Share, error) {
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

	share := drive.NewShare(&pShare, shareKR, nil, c, pShare.VolumeID)
	link := drive.NewLink(&pLink, nil, share, c)
	// Set the link on the share after construction to break the circular reference.
	share.Link = link

	// Apply per-share cache config.
	c.applyShareConfig(share)

	return share, nil
}

// applyShareConfig sets cache flags on a share based on the loaded config.
// Root and photos shares are always forced to false. Looks up the share
// by its decrypted root link name.
func (c *Client) applyShareConfig(share *drive.Share) {
	// Root and photos shares: caching prohibited.
	st := share.ProtonShare().Type
	if st == proton.ShareTypeMain || st == drive.ShareTypePhotos {
		share.DirentCacheEnabled = false
		share.MetadataCacheEnabled = false
		share.DiskCacheEnabled = false
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
		return // defaults to false
	}

	share.DirentCacheEnabled = sc.DirentCacheEnabled
	share.MetadataCacheEnabled = sc.MetadataCacheEnabled
	share.DiskCacheEnabled = sc.DiskCacheEnabled
}

// ResolveShareByType finds a share by its type (Main, Photos, etc.)
// without decrypting share names. Uses metadata to find the type match,
// then resolves only that share.
func (c *Client) ResolveShareByType(ctx context.Context, st proton.ShareType) (*drive.Share, error) {
	metas, err := c.ListSharesMetadata(ctx, true)
	if err != nil {
		return nil, err
	}
	for _, meta := range metas {
		if meta.Type == st {
			return c.GetShare(ctx, meta.ShareID)
		}
	}
	return nil, drive.ErrFileNotFound
}

// ResolveShare finds a share by its root link name.
// Fetches metadata first, then decrypts shares one at a time until a
// match is found — avoids decrypting all shares upfront.
func (c *Client) ResolveShare(ctx context.Context, name string, all bool) (*drive.Share, error) {
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

	return nil, drive.ErrFileNotFound
}

// ResolvePath resolves a slash-separated path to a link across all shares.
func (c *Client) ResolvePath(ctx context.Context, path string, all bool) (*drive.Link, error) {
	parts := strings.Split(path, "/")

	if len(parts) == 0 {
		return nil, drive.ErrInvalidPath
	}

	share, err := c.ResolveShare(ctx, parts[0], all)
	if err != nil {
		return nil, err
	}

	return share.Link.ResolvePath(ctx, strings.Join(parts[1:], "/"), all)
}
