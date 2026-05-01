package client

import (
	"path/filepath"

	"github.com/peterbourgon/diskv/v3"
)

// NewObjectCache constructs a diskv-backed object cache with a flat on-disk
// layout (one file per key) and an optional in-memory LRU cache.
//
// basePath is the root directory for on-disk storage, typically
// $XDG_RUNTIME_DIR/proton/<service>/<namespace>. cacheSizeBytes controls the
// in-memory LRU cache size in bytes; 0 disables the in-memory cache.
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
