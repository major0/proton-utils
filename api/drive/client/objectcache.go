package client

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/major0/proton-cli/api"
	"github.com/peterbourgon/diskv/v3"
)

// NewObjectCache constructs a diskv-backed object cache with a flat on-disk
// layout (one file per key) and an optional in-memory LRU cache.
//
// basePath is the root directory for on-disk storage, typically
// $XDG_RUNTIME_DIR/proton/drive/. cacheSizeBytes controls the in-memory
// LRU cache size in bytes; 0 disables the in-memory cache.
//
// Atomic writes are enabled via a TempDir on the same filesystem as basePath,
// so os.Rename is guaranteed to work.
//
// Callers must handle a nil *diskv.Diskv when $XDG_RUNTIME_DIR is unset —
// writes should be no-ops and reads should return a miss. The caller is
// responsible for skipping diskv construction entirely when the base path
// is unavailable.
func NewObjectCache(basePath string, cacheSizeBytes uint64) *diskv.Diskv {
	return diskv.New(diskv.Options{
		BasePath:     basePath,
		Transform:    func(_ string) []string { return []string{} },
		CacheSizeMax: cacheSizeBytes,
		TempDir:      filepath.Join(basePath, ".tmp"),
	})
}

// sanitizeKey strips '=' padding from a Proton ID for use as a diskv key.
// Proton IDs are base64-encoded and may contain '=' padding which can
// cause issues with filesystem path construction.
func sanitizeKey(id string) string {
	return strings.TrimRight(id, "=")
}

// InitObjectCache constructs the shared diskv instance if the config
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

	c.objectCache = NewObjectCache(filepath.Join(xdgRuntimeDir, "proton", "drive"), 0)

	// Initialize the shared block store with the disk cache.
	c.blockStore = NewBlockStore(c.Session, c.objectCache, nil)
}

// objectCacheRead reads a value from the cache. When d is nil (e.g.
// $XDG_RUNTIME_DIR unset), it returns nil, nil — a cache miss with no error.
func objectCacheRead(d *diskv.Diskv, key string) ([]byte, error) {
	if d == nil {
		return nil, nil
	}
	return d.Read(key)
}

// objectCacheWrite writes a value to the cache. When d is nil (e.g.
// $XDG_RUNTIME_DIR unset), it is a no-op and returns nil.
func objectCacheWrite(d *diskv.Diskv, key string, data []byte) error {
	if d == nil {
		return nil
	}
	return d.Write(key, data)
}

// objectCacheErase removes a single key from the cache. When d is nil (e.g.
// $XDG_RUNTIME_DIR unset), it is a no-op and returns nil.
func objectCacheErase(d *diskv.Diskv, key string) error {
	if d == nil {
		return nil
	}
	return d.Erase(key)
}

// objectCacheEraseAll removes all keys from the cache. When d is nil (e.g.
// $XDG_RUNTIME_DIR unset), it is a no-op and returns nil.
func objectCacheEraseAll(d *diskv.Diskv) error {
	if d == nil {
		return nil
	}
	return d.EraseAll()
}
