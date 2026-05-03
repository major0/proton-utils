package api

import (
	"path/filepath"

	"github.com/peterbourgon/diskv/v3"
)

// ObjectCache is a nil-safe on-disk K/V store for encrypted API objects.
// All operations on a nil instance are no-ops (cache miss on read, silent
// discard on write). Each subsystem creates its own instance with its own
// BasePath.
//
// The cache stores raw []byte values keyed by string. Subsystems handle
// JSON marshal/unmarshal themselves. diskv remains an unexported
// implementation detail — it does not appear in the exported API surface.
type ObjectCache struct {
	dv *diskv.Diskv
}

// NewObjectCache constructs an ObjectCache rooted at basePath.
// Returns nil when basePath is empty (disabling caching).
func NewObjectCache(basePath string) *ObjectCache {
	if basePath == "" {
		return nil
	}
	dv := diskv.New(diskv.Options{
		BasePath:     basePath,
		Transform:    func(_ string) []string { return []string{} },
		CacheSizeMax: 0, // no in-memory LRU — subsystems manage their own
		TempDir:      filepath.Join(basePath, ".tmp"),
	})
	return &ObjectCache{dv: dv}
}

// Read returns the value for key, or (nil, nil) on cache miss or nil receiver.
// All diskv errors are treated as cache misses.
func (c *ObjectCache) Read(key string) ([]byte, error) {
	if c == nil {
		return nil, nil
	}
	data, err := c.dv.Read(key)
	if err != nil {
		return nil, nil
	}
	return data, nil
}

// Write stores data under key. No-op on nil receiver.
func (c *ObjectCache) Write(key string, data []byte) error {
	if c == nil {
		return nil
	}
	return c.dv.Write(key, data)
}

// Erase removes a single key. No-op on nil receiver.
func (c *ObjectCache) Erase(key string) error {
	if c == nil {
		return nil
	}
	return c.dv.Erase(key)
}

// EraseAll removes all keys. No-op on nil receiver.
func (c *ObjectCache) EraseAll() error {
	if c == nil {
		return nil
	}
	return c.dv.EraseAll()
}

// Has reports whether key exists in the cache. Returns false on nil receiver.
func (c *ObjectCache) Has(key string) bool {
	if c == nil {
		return false
	}
	return c.dv.Has(key)
}

// Keys returns a channel that yields all keys in the cache. The cancel
// channel can be closed to stop iteration early. Returns a closed channel
// on nil receiver.
func (c *ObjectCache) Keys(cancel <-chan struct{}) <-chan string {
	if c == nil {
		ch := make(chan string)
		close(ch)
		return ch
	}
	return c.dv.Keys(cancel)
}
