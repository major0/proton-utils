//go:build linux

// Package drive implements the fusemount.NamespaceHandler for Proton Drive,
// exposing shares as a read-only directory tree under the "drive" namespace.
package drive

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"syscall"
	"time"

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

	// dirNodes is the live-directory-node registry keyed by LinkID, used by
	// event-driven invalidation to clear a directory's cached children.
	// Bounded by maxDirNodes; entries are pruned on the go-fuse Forget
	// lifecycle. Guarded by dirNodesMu.
	dirNodes   map[string]dirInvalidator
	dirNodesMu sync.Mutex

	// startTime is captured at construction. Used as mtime/ctime for the
	// namespace directory and .linkid — avoids leaking internal Proton
	// metadata (volume creation date) outside the encrypted boundary.
	startTime uint64
}

// maxDirNodes bounds the live-node registry so a missed Forget cannot grow it
// without limit. A dropped entry only costs a redundant re-list on next access.
const maxDirNodes = 4096

// Compile-time interface assertion.
var _ fusemount.NamespaceHandler = (*DriveHandler)(nil)
var _ fusemount.NodeRenamer = (*DriveHandler)(nil)

// NewDriveHandler constructs a DriveHandler with the given drive client.
func NewDriveHandler(client *drive.Client) *DriveHandler {
	//nolint:gosec // Unix timestamp is always positive
	now := uint64(time.Now().Unix())
	return &DriveHandler{
		client:    client,
		shares:    make(map[string]*drive.Share),
		dirNodes:  make(map[string]dirInvalidator),
		startTime: now,
	}
}

// registerDir records a live directory node under its LinkID so event-driven
// invalidation can find it. The newest node for a LinkID wins. The registry
// is size-capped: when full, one arbitrary entry is evicted (a dropped live
// node only costs a redundant re-list).
func (h *DriveHandler) registerDir(linkID string, node dirInvalidator) {
	h.dirNodesMu.Lock()
	defer h.dirNodesMu.Unlock()
	if h.dirNodes == nil {
		h.dirNodes = make(map[string]dirInvalidator)
	}
	if _, exists := h.dirNodes[linkID]; !exists && len(h.dirNodes) >= maxDirNodes {
		for k := range h.dirNodes {
			delete(h.dirNodes, k)
			break
		}
	}
	h.dirNodes[linkID] = node
}

// unregisterDir removes a directory node from the registry (on go-fuse Forget).
func (h *DriveHandler) unregisterDir(linkID string) {
	h.dirNodesMu.Lock()
	delete(h.dirNodes, linkID)
	h.dirNodesMu.Unlock()
}

// OnInvalidateParent clears the cached children of the live directory node for
// parentLinkID, if one is registered; otherwise it is a no-op. This is wired
// to the Drive client via SetInvalidationHook so a remote child create/delete
// refreshes the parent listing.
func (h *DriveHandler) OnInvalidateParent(parentLinkID string) {
	h.dirNodesMu.Lock()
	node := h.dirNodes[parentLinkID]
	h.dirNodesMu.Unlock()
	if node != nil {
		node.invalidateChildren()
	}
}

// InvalidateAll clears the share map and every registered directory node's
// cached children. Used on a full resync (DriveEvent.Refresh).
func (h *DriveHandler) InvalidateAll() {
	h.invalidateShares()

	h.dirNodesMu.Lock()
	nodes := make([]dirInvalidator, 0, len(h.dirNodes))
	for _, n := range h.dirNodes {
		nodes = append(nodes, n)
	}
	h.dirNodesMu.Unlock()

	for _, n := range nodes {
		n.invalidateChildren()
	}
}

// Getattr returns attributes for the drive namespace root directory.
// Mode 0500: only the daemon owner can access namespace contents.
// checkAccess enforces this at the dispatch layer.
// Timestamps reflect daemon startup time.
func (h *DriveHandler) Getattr(_ context.Context) (fusemount.Attr, syscall.Errno) {
	return fusemount.Attr{
		Mode:  syscall.S_IFDIR | 0500,
		Nlink: 2,
		Mtime: h.startTime,
		Ctime: h.startTime,
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
				return newShareDirNode(share, h.client, h), 0
			}
		}
		return nil, syscall.ENOENT

	case "Photos":
		for _, share := range h.shares {
			if share.ProtonShare().Type == drive.ShareTypePhotos {
				return newShareDirNode(share, h.client, h), 0
			}
		}
		return nil, syscall.ENOENT

	case ".linkid":
		return &LinkIDDir{
			client:  h.client,
			handler: h,
			shares:  h.snapshotShares,
			mtime:   h.startTime,
			ctime:   h.startTime,
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
			return newShareDirNode(share, h.client, h), 0
		}
	}

	return nil, syscall.ENOENT
}

// LoadShares populates the internal share map at startup by listing all
// share metadata and resolving each non-device share.
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

// invalidateShares clears the handler's internal share map, forcing the
// next Readdir/Lookup to reload from the API via RefreshShares.
func (h *DriveHandler) invalidateShares() {
	h.sharesMu.Lock()
	h.shares = make(map[string]*drive.Share)
	h.sharesMu.Unlock()
}

// snapshotShares returns the current share map under a read lock.
// Used by LinkIDDir.Readdir to list share root LinkIDs.
func (h *DriveHandler) snapshotShares() map[string]*drive.Share {
	h.sharesMu.RLock()
	defer h.sharesMu.RUnlock()
	return h.shares
}

// findShareByName resolves a share by its display name using the same
// logic as Lookup: volumes match by hardcoded name ("Home" → main,
// "Photos" → photos), standard shares match by decrypted name.
// Returns nil if no share matches.
// Caller must hold sharesMu (at least RLock).
func (h *DriveHandler) findShareByName(name string) *drive.Share {
	switch name {
	case "Home":
		for _, share := range h.shares {
			if share.ProtonShare().Type == proton.ShareTypeMain {
				return share
			}
		}
		return nil

	case "Photos":
		for _, share := range h.shares {
			if share.ProtonShare().Type == drive.ShareTypePhotos {
				return share
			}
		}
		return nil
	}

	// O(N) scan for standard shares by decrypted name.
	for _, share := range h.shares {
		if share.ProtonShare().Type != proton.ShareTypeStandard {
			continue
		}
		shareName, err := share.GetName(context.Background())
		if err != nil {
			continue
		}
		if shareName == name {
			return share
		}
	}

	return nil
}

// Rename supports renaming standard shares at the namespace root.
// Cross-directory moves of shares are rejected with EXDEV.
// Volume renames (Home, Photos) are rejected with EPERM.
func (h *DriveHandler) Rename(_ context.Context, oldName string, newParent fusemount.Node, newName string) syscall.Errno {
	// Same-directory rename: newParent is nil (dispatch passes dp.node
	// which is nil for root nodes). Any non-nil newParent means cross-dir.
	if newParent != nil {
		return syscall.EXDEV
	}

	h.sharesMu.RLock()
	share := h.findShareByName(oldName)
	h.sharesMu.RUnlock()

	if share == nil {
		return syscall.ENOENT
	}

	// Only standard shares can be renamed.
	if share.ProtonShare().Type != proton.ShareTypeStandard {
		return syscall.EPERM
	}

	if err := h.client.ShareRename(context.Background(), share, newName); err != nil {
		if errors.Is(err, drive.ErrNotStandardShare) {
			return syscall.EPERM
		}
		slog.Debug("DriveHandler.Rename: failed", "error", err)
		return syscall.EIO
	}

	// Invalidate the handler's share map — the renamed share has stale
	// encrypted name data. The next Readdir/Lookup will trigger RefreshShares.
	h.invalidateShares()

	return 0
}
