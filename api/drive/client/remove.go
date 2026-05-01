package client

import (
	"context"
	"fmt"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
)

// Remove moves a link to trash or permanently deletes it.
// Returns an error if the link is a share root. For non-empty folders,
// returns ErrNotEmpty unless opts.Recursive is true.
func (c *Client) Remove(ctx context.Context, share *drive.Share, link *drive.Link, opts drive.RemoveOpts) error {
	if link.ParentLink() == nil {
		return fmt.Errorf("remove: cannot remove share root")
	}

	if link.Type() == proton.LinkTypeFolder && !opts.Recursive {
		children, err := link.ListChildren(ctx, true)
		if err != nil {
			return fmt.Errorf("remove: listing children: %w", err)
		}
		if len(children) > 0 {
			name, _ := link.Name()
			return fmt.Errorf("remove: %s: %w", name, drive.ErrNotEmpty)
		}
	}

	shareID := share.ProtonShare().ShareID
	linkID := link.ProtonLink().LinkID

	var err error
	switch {
	case link.State() == proton.LinkStateTrashed && opts.Permanent:
		// Trashed links must be deleted via the trash endpoint.
		err = c.deleteTrashedLinks(ctx, share.ProtonShare().VolumeID, linkID)

	case link.State() == proton.LinkStateTrashed:
		// Already trashed — nothing to do.
		return nil

	case link.State() == proton.LinkStateDraft && opts.Permanent:
		// Drafts are deleted from the parent folder.
		err = c.Session.Client.DeleteChildren(
			ctx, shareID,
			link.ParentLink().ProtonLink().LinkID,
			linkID,
		)

	case opts.Permanent:
		// Active links: permanent delete from parent.
		err = c.Session.Client.DeleteChildren(
			ctx, shareID,
			link.ParentLink().ProtonLink().LinkID,
			linkID,
		)

	default:
		// Active links: move to trash.
		err = c.Session.Client.TrashChildren(
			ctx, shareID,
			link.ParentLink().ProtonLink().LinkID,
			linkID,
		)
	}

	if err != nil {
		return err
	}

	// Invalidate affected Link Table entries and on-disk cache.
	c.deleteLink(linkID)
	c.deleteLink(link.ParentLink().ProtonLink().LinkID)
	_ = objectCacheErase(c.objectCache, linkID)

	return nil
}

// deleteTrashedLinks permanently deletes trashed links via the v2
// volume-based trash endpoint.
func (c *Client) deleteTrashedLinks(ctx context.Context, volumeID string, linkIDs ...string) error {
	req := struct {
		LinkIDs []string
	}{LinkIDs: linkIDs}

	var res struct {
		Code      int
		Responses []struct {
			LinkID   string
			Response struct {
				Code  int
				Error string
			}
		}
	}

	if err := c.Session.DoJSON(ctx, "POST", "/drive/v2/volumes/"+volumeID+"/trash/delete_multiple", req, &res); err != nil {
		return fmt.Errorf("delete trashed links: %w", err)
	}

	for _, r := range res.Responses {
		if r.Response.Code != int(proton.SuccessCode) {
			return fmt.Errorf("delete trashed link %s: %s (Code=%d)", r.LinkID, r.Response.Error, r.Response.Code)
		}
	}
	return nil
}

// EmptyTrash permanently deletes all items in the trash for the given share.
func (c *Client) EmptyTrash(ctx context.Context, share *drive.Share) error {
	return c.Session.Client.EmptyTrash(ctx, share.ProtonShare().ShareID)
}
