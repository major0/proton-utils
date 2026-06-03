package drive

import (
	"context"
	"log/slog"

	"github.com/ProtonMail/go-proton-api"
)

// FetchRevisionXAttr fetches the full revision for a file link and
// populates the ActiveRevision.XAttr field on the underlying proton.Link.
// This enables Link.Mode(), Link.Size() (via XAttr), and Link.ModifyTime()
// to return correct values from the revision metadata.
//
// No-op for folders, links without an active revision, or links that
// already have XAttr populated.
func (c *Client) FetchRevisionXAttr(ctx context.Context, link *Link) {
	pLink := link.ProtonLink()
	if pLink.Type != proton.LinkTypeFile {
		return
	}
	if pLink.State == proton.LinkStateDraft {
		return // drafts cannot be fetched
	}
	if pLink.FileProperties == nil {
		return
	}
	rev := &pLink.FileProperties.ActiveRevision
	if rev.ID == "" || rev.State != proton.RevisionStateActive {
		return
	}
	if rev.XAttr != "" {
		return // already populated
	}

	shareID := link.Share().ProtonShare().ShareID
	fullRev, err := c.Session.Client.GetRevisionAllBlocks(ctx, shareID, pLink.LinkID, rev.ID)
	if err != nil {
		slog.Debug("FetchRevisionXAttr: failed",
			"linkID", pLink.LinkID, "error", err)
		return
	}

	// Populate the XAttr on the in-memory proton.Link so that
	// Link.Mode() and decryptXAttr() can decrypt it.
	rev.XAttr = fullRev.XAttr

	// Also update Size from the revision (the listing Size may be stale).
	rev.Size = fullRev.Size
}
