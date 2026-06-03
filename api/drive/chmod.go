package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
)

// Chmod updates the Unix permission bits for a file by creating a new
// revision with the same content but updated XAttr. This requires a
// full re-upload because the Proton Drive API only allows XAttr changes
// on draft revisions (not active ones).
//
// mode should contain only permission bits (lower 12 bits: 0o7777).
// Returns an error if the link is not a file or has no active revision.
func (c *Client) Chmod(ctx context.Context, share *Share, link *Link, mode uint32) error {
	if !link.HasActiveRevision() {
		return fmt.Errorf("drive.Chmod %s: no active revision", link.LinkID())
	}

	// Read the entire file into memory first. We must close the reader
	// before opening the writer because OverwriteFD invalidates cached
	// blocks for this link.
	reader, err := c.OpenFD(ctx, link)
	if err != nil {
		return fmt.Errorf("drive.Chmod %s: open for read: %w", link.LinkID(), err)
	}

	// Use ReadAt with the FD's known file size to avoid io.ReadAll's
	// EOF-signaling issues with the block cache. Seek to end to get
	// the actual file size from the revision (not the stale listing).
	fileSize, err := reader.Seek(0, io.SeekEnd)
	if err != nil {
		_ = reader.Close()
		return fmt.Errorf("drive.Chmod %s: seek: %w", link.LinkID(), err)
	}
	content := make([]byte, fileSize)
	n, err := reader.ReadAt(content, 0)
	_ = reader.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("drive.Chmod %s: read: %w", link.LinkID(), err)
	}
	content = content[:n]

	slog.Debug("drive.Chmod: read complete",
		"linkID", link.LinkID(), "bytes", n, "mode", fmt.Sprintf("%04o", mode))

	// Create a new revision for writing.
	writer, err := c.OverwriteFD(ctx, share, link)
	if err != nil {
		return fmt.Errorf("drive.Chmod %s: open for write: %w", link.LinkID(), err)
	}

	// Set the new mode before writing (stored in XAttr on commit).
	writer.SetMode(mode & 0o7777)

	// Write all content to the new revision.
	if _, err := writer.Write(content); err != nil {
		_ = writer.Close()
		return fmt.Errorf("drive.Chmod %s: write: %w", link.LinkID(), err)
	}

	// Close the writer to commit the new revision with updated XAttr.
	if err := writer.Close(); err != nil {
		return fmt.Errorf("drive.Chmod %s: commit: %w", link.LinkID(), err)
	}

	// Invalidate cached metadata so the next accessor re-resolves from XAttr.
	link.InvalidateMeta()

	// Invalidate stale link from the link table and on-disk cache so
	// subsequent operations re-fetch fresh state from the API.
	c.deleteLink(link.LinkID())
	_ = c.objectCache.Erase(SanitizeLinkID(link.LinkID()))
	if link.ParentLink() != nil {
		c.deleteLink(link.ParentLink().LinkID())
		_ = c.objectCache.Erase(SanitizeLinkID(link.ParentLink().LinkID()))
		link.ParentLink().InvalidateChildren()
	}

	return nil
}
