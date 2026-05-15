package drive

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/major0/proton-utils/api"
)

// sanitizeKey strips '=' padding from a Proton ID for use as a cache key.
// Proton IDs are base64-encoded and may contain '=' padding which can
// cause issues with filesystem path construction.
func sanitizeKey(id string) string {
	return strings.TrimRight(id, "=")
}

// SanitizeLinkID strips '=' padding from a LinkID for use as a directory
// entry name. Proton LinkIDs are base64-encoded and may contain trailing
// '=' which is problematic in filesystem paths.
func SanitizeLinkID(id string) string {
	return strings.TrimRight(id, "=")
}

// InitObjectCache constructs the shared ObjectCache instance if the config
// has any share with disk_cache: objectstore and $XDG_RUNTIME_DIR is
// set. The cache is a single flat namespace at
// $XDG_RUNTIME_DIR/proton/drive/ — shared across all shares because
// LinkIDs are globally unique and shares are windows into the same
// volume system.
func (c *Client) InitObjectCache() {
	if c.Config == nil {
		return
	}

	needDisk := false
	for _, sc := range c.Config.Shares {
		if sc.DiskCache == api.DiskCacheObjectStore {
			needDisk = true
			break
		}
	}
	if !needDisk {
		return
	}

	xdgRuntimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntimeDir == "" {
		return
	}

	c.objectCache = api.NewObjectCache(filepath.Join(xdgRuntimeDir, "proton", "drive"))

	// Initialize the shared block store with the disk cache and buffer cache.
	c.blockStore = newBlockStore(c.Session, c.objectCache, newBufferCache(64))
}
