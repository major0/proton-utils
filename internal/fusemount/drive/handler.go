//go:build linux

// Package drive implements the fusemount.NamespaceHandler for Proton Drive,
// exposing shares as a read-only directory tree under the "drive" namespace.
package drive

import (
	"context"
	"log/slog"
	"sync"
	"syscall"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
	"github.com/major0/proton-utils/internal/fusemount"
)

// DriveHandler implements fusemount.NamespaceHandler for the "drive" namespace.
// It exposes Proton Drive shares as top-level directories.
type DriveHandler struct { //nolint:revive // name specified by design doc
	client   *drive.Client
	shares   map[string]*drive.Share // keyed by ShareID
	sharesMu sync.RWMutex

	// volumeMtime and volumeCtime are captured from the main share's root
	// link at startup. Used as timestamps for the namespace directory and
	// the .linkid virtual directory.
	volumeMtime uint64
	volumeCtime uint64
}

// Compile-time interface assertion.
var _ fusemount.NamespaceHandler = (*DriveHandler)(nil)

// NewDriveHandler constructs a DriveHandler with the given drive client.
func NewDriveHandler(client *drive.Client) *DriveHandler {
	return &DriveHandler{
		client: client,
		shares: make(map[string]*drive.Share),
	}
}

// Getattr returns attributes for the drive namespace root directory.
// Mode 0500: only the daemon owner can access namespace contents.
// checkAccess enforces this at the dispatch layer.
// Timestamps are captured from the main share's root link at startup.
func (h *DriveHandler) Getattr(_ context.Context) (fusemount.Attr, syscall.Errno) {
	return fusemount.Attr{
		Mode:  syscall.S_IFDIR | 0500,
		Nlink: 2,
		Mtime: h.volumeMtime,
		Ctime: h.volumeCtime,
	}, 0
}

// Readdir lists shares as directory entries under the drive namespace root.
// Device shares are excluded. Standard shares use their decrypted name
// (via GetName). Shares where decryption fails are silently skipped.
func (h *DriveHandler) Readdir(ctx context.Context) ([]fusemount.DirEntry, syscall.Errno) {
	h.sharesMu.RLock()
	defer h.sharesMu.RUnlock()

	entries := make([]fusemount.DirEntry, 0, len(h.shares)+2)

	for _, share := range h.shares {
		st := share.ProtonShare().Type

		switch st {
		case proton.ShareTypeMain:
			entries = append(entries, fusemount.DirEntry{
				Name: "Home",
				Mode: syscall.S_IFDIR,
			})
		case drive.ShareTypePhotos:
			entries = append(entries, fusemount.DirEntry{
				Name: "Photos",
				Mode: syscall.S_IFDIR,
			})
		case proton.ShareTypeStandard:
			name, err := share.GetName(ctx)
			if err != nil {
				slog.Debug("drive.Readdir: skipping share with decryption error",
					"shareID", share.Metadata().ShareID)
				continue
			}
			entries = append(entries, fusemount.DirEntry{
				Name: name,
				Mode: syscall.S_IFDIR,
			})
		case proton.ShareTypeDevice:
			// Excluded from listing.
			continue
		default:
			continue
		}
	}

	// Virtual .linkid directory entry.
	entries = append(entries, fusemount.DirEntry{
		Name: ".linkid",
		Mode: syscall.S_IFDIR,
	})

	return entries, 0
}

// Lookup resolves a name to a node within the drive namespace root.
// "Home" maps to the main share, "Photos" to the photos share,
// ".linkid" to the LinkID virtual directory, and standard share names
// are resolved via O(N) decryption scan.
func (h *DriveHandler) Lookup(ctx context.Context, name string) (fusemount.Node, syscall.Errno) {
	h.sharesMu.RLock()
	defer h.sharesMu.RUnlock()

	switch name {
	case "Home":
		for _, share := range h.shares {
			if share.ProtonShare().Type == proton.ShareTypeMain {
				return &ShareDirNode{share: share, client: h.client}, 0
			}
		}
		return nil, syscall.ENOENT

	case "Photos":
		for _, share := range h.shares {
			if share.ProtonShare().Type == drive.ShareTypePhotos {
				return &ShareDirNode{share: share, client: h.client}, 0
			}
		}
		return nil, syscall.ENOENT

	case ".linkid":
		return &LinkIDDir{
			client: h.client,
			shares: h.snapshotShares,
			mtime:  h.volumeMtime,
			ctime:  h.volumeCtime,
		}, 0
	}

	// O(N) scan for standard shares by decrypted name.
	for _, share := range h.shares {
		if share.ProtonShare().Type != proton.ShareTypeStandard {
			continue
		}
		shareName, err := share.GetName(ctx)
		if err != nil {
			continue
		}
		if shareName == name {
			return &ShareDirNode{share: share, client: h.client}, 0
		}
	}

	return nil, syscall.ENOENT
}

// LoadShares populates the internal share map at startup by listing all
// share metadata and resolving each non-device share. Captures the main
// volume's root link timestamps for use in Getattr.
func (h *DriveHandler) LoadShares(ctx context.Context) error {
	metas, err := h.client.ListSharesMetadata(ctx, true)
	if err != nil {
		return err
	}

	shares := make(map[string]*drive.Share, len(metas))
	for _, meta := range metas {
		if meta.Type == proton.ShareTypeDevice {
			continue
		}
		share, err := h.client.GetShare(ctx, meta.ShareID)
		if err != nil {
			slog.Warn("drive.LoadShares: skipping share",
				"shareID", meta.ShareID, "error", err)
			continue
		}
		shares[meta.ShareID] = share
	}

	// Capture main volume timestamps for namespace directory attrs.
	for _, share := range shares {
		if share.ProtonShare().Type == proton.ShareTypeMain {
			//nolint:gosec // timestamps are non-negative from API
			mtime := uint64(share.Link.ModifyTime())
			//nolint:gosec // timestamps are non-negative from API
			ctime := uint64(share.Link.CreateTime())
			// Fall back to CreateTime if ModifyTime is zero (common for
			// share root links where the API doesn't populate ModifyTime).
			if mtime == 0 {
				mtime = ctime
			}
			h.volumeMtime = mtime
			h.volumeCtime = ctime
			break
		}
	}

	h.sharesMu.Lock()
	h.shares = shares
	h.sharesMu.Unlock()

	return nil
}

// RefreshShares re-lists shares from the API and swaps the internal map
// under a write lock. On API failure the existing map is retained and an
// error is returned. Individual share resolution failures are logged and
// skipped — the remaining shares are still updated.
func (h *DriveHandler) RefreshShares(ctx context.Context) error {
	metas, err := h.client.ListSharesMetadata(ctx, true)
	if err != nil {
		return err
	}

	shares := make(map[string]*drive.Share, len(metas))
	for _, meta := range metas {
		if meta.Type == proton.ShareTypeDevice {
			continue
		}
		share, err := h.client.GetShare(ctx, meta.ShareID)
		if err != nil {
			slog.Warn("drive.RefreshShares: skipping share",
				"shareID", meta.ShareID, "error", err)
			continue
		}
		shares[meta.ShareID] = share
	}

	h.sharesMu.Lock()
	h.shares = shares
	h.sharesMu.Unlock()

	slog.Debug("drive.RefreshShares: updated share map", "count", len(shares))
	return nil
}

// SetShares replaces the internal share map under a write lock. This is
// exported for testing (simulating refresh without a real API client).
func (h *DriveHandler) SetShares(shares map[string]*drive.Share) {
	h.sharesMu.Lock()
	h.shares = shares
	h.sharesMu.Unlock()
}

// snapshotShares returns the current share map under a read lock.
// Used by LinkIDDir.Readdir to list share root LinkIDs.
func (h *DriveHandler) snapshotShares() map[string]*drive.Share {
	h.sharesMu.RLock()
	defer h.sharesMu.RUnlock()
	return h.shares
}
