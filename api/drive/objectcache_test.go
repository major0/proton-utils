package drive

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

// TestPropertyObjectCacheTypeAgnostic verifies that the ObjectCache stores
// and returns arbitrary byte slices without interpreting or transforming them.
//
// **Validates: Requirements 1.7**
func TestPropertyObjectCacheTypeAgnostic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		key := rapid.StringMatching(`[a-zA-Z0-9_\-]{1,64}`).Draw(rt, "key")
		data := rapid.SliceOf(rapid.Byte()).Draw(rt, "data")

		cache := api.NewObjectCache(t.TempDir())

		if err := cache.Write(key, data); err != nil {
			rt.Fatalf("Write(%q): %v", key, err)
		}

		got, err := cache.Read(key)
		if err != nil {
			rt.Fatalf("Read(%q): %v", key, err)
		}

		if !bytes.Equal(got, data) {
			rt.Fatalf("Read(%q) returned %d bytes, want %d bytes", key, len(got), len(data))
		}
	})
}

// TestPropertyObjectCacheNoAutoExpiration verifies that an object written to
// the cache and never explicitly erased is still readable after any number of
// intervening reads and writes to other keys. Only Erase and EraseAll remove
// entries — there is no automatic time-based expiration.
//
// **Validates: Requirements 2.1**
func TestPropertyObjectCacheNoAutoExpiration(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cache := api.NewObjectCache(t.TempDir())

		// Target key/value that must survive all intervening operations.
		targetKey := rapid.StringMatching(`[a-zA-Z0-9_\-]{1,64}`).Draw(rt, "targetKey")
		targetValue := rapid.SliceOfN(rapid.Byte(), 1, 512).Draw(rt, "targetValue")

		if err := cache.Write(targetKey, targetValue); err != nil {
			rt.Fatalf("Write target key %q: %v", targetKey, err)
		}

		// Generate a random sequence of operations on OTHER keys.
		numOps := rapid.IntRange(1, 50).Draw(rt, "numOps")
		for i := 0; i < numOps; i++ {
			// Generate a key that differs from targetKey.
			otherKey := rapid.StringMatching(`[a-zA-Z0-9_\-]{1,64}`).Draw(rt, "otherKey")
			if otherKey == targetKey {
				otherKey += "_other"
			}

			kind := rapid.SampledFrom([]string{"write", "read", "erase"}).Draw(rt, "opKind")
			switch kind {
			case "write":
				val := rapid.SliceOfN(rapid.Byte(), 0, 256).Draw(rt, "writeValue")
				if err := cache.Write(otherKey, val); err != nil {
					rt.Fatalf("Write other key %q: %v", otherKey, err)
				}
			case "read":
				// Read may or may not find the key — that's fine.
				_, _ = cache.Read(otherKey)
			case "erase":
				// Erase may or may not find the key — that's fine.
				_ = cache.Erase(otherKey)
			}
		}

		// The target key must still be readable with the original value.
		got, err := cache.Read(targetKey)
		if err != nil {
			rt.Fatalf("Read target key %q after %d intervening ops: %v", targetKey, numOps, err)
		}
		if !bytes.Equal(got, targetValue) {
			rt.Fatalf("target key %q: got %d bytes, want %d bytes", targetKey, len(got), len(targetValue))
		}
	})
}

// TestPropertyObjectCacheNilInstance verifies that when the ObjectCache
// instance is nil (simulating $XDG_RUNTIME_DIR unset), all cache operations
// are safe no-ops: writes return nil error, reads return nil data and nil
// error, and no panic occurs.
//
// **Validates: Requirement 1.5**
func TestPropertyObjectCacheNilInstance(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		key := rapid.StringMatching(`[a-zA-Z0-9_\-]{1,64}`).Draw(rt, "key")
		data := rapid.SliceOf(rapid.Byte()).Draw(rt, "data")

		// All methods called on a nil *api.ObjectCache must not panic.
		var nilCache *api.ObjectCache

		// Write is a no-op.
		if err := nilCache.Write(key, data); err != nil {
			rt.Fatalf("Write(nil, %q): unexpected error: %v", key, err)
		}

		// Read returns a miss (nil data, nil error).
		got, err := nilCache.Read(key)
		if err != nil {
			rt.Fatalf("Read(nil, %q): unexpected error: %v", key, err)
		}
		if got != nil {
			rt.Fatalf("Read(nil, %q): expected nil data, got %d bytes", key, len(got))
		}

		// Erase is a no-op.
		if err := nilCache.Erase(key); err != nil {
			rt.Fatalf("Erase(nil, %q): unexpected error: %v", key, err)
		}

		// EraseAll is a no-op.
		if err := nilCache.EraseAll(); err != nil {
			rt.Fatalf("EraseAll(nil): unexpected error: %v", err)
		}

		// Has returns false.
		if nilCache.Has(key) {
			rt.Fatalf("Has(nil, %q): expected false", key)
		}
	})
}

// TestPropertyObjectCacheNamespaceIsolation verifies that two ObjectCache
// instances with different BasePath values do not share storage. A key
// written to one instance is not readable from the other, while it remains
// readable from the original instance with the correct data.
//
// **Validates: Requirements 1.2**
func TestPropertyObjectCacheNamespaceIsolation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		key := rapid.StringMatching(`[a-zA-Z0-9_\-]{1,64}`).Draw(rt, "key")
		data := rapid.SliceOfN(rapid.Byte(), 1, 512).Draw(rt, "data")

		cacheA := api.NewObjectCache(t.TempDir())
		cacheB := api.NewObjectCache(t.TempDir())

		// Write to instance A.
		if err := cacheA.Write(key, data); err != nil {
			rt.Fatalf("Write to cacheA(%q): %v", key, err)
		}

		// The key must NOT be readable from instance B.
		if cacheB.Has(key) {
			rt.Fatalf("cacheB.Has(%q) = true, want false", key)
		}
		got, _ := cacheB.Read(key)
		if got != nil {
			rt.Fatalf("cacheB.Read(%q) returned data, want nil (key should not exist)", key)
		}

		// The key must still be readable from instance A with the original data.
		got, err := cacheA.Read(key)
		if err != nil {
			rt.Fatalf("cacheA.Read(%q): %v", key, err)
		}
		if !bytes.Equal(got, data) {
			rt.Fatalf("cacheA.Read(%q) returned %d bytes, want %d bytes", key, len(got), len(data))
		}
	})
}

// TestPropertyObjectCacheAtomicWrite verifies that after Write completes, the
// on-disk file is complete and valid — never a partial write or zero-length
// file. This validates that diskv's TempDir atomic write mechanism (write to
// temp file, then os.Rename) produces complete files.
//
// **Validates: Requirement 1.6**
func TestPropertyObjectCacheAtomicWrite(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir := t.TempDir()
		cache := api.NewObjectCache(dir)

		key := rapid.StringMatching(`[a-zA-Z0-9_\-]{2,64}`).Draw(rt, "key")
		data := rapid.SliceOfN(rapid.Byte(), 1, 4096).Draw(rt, "data")

		if err := cache.Write(key, data); err != nil {
			rt.Fatalf("Write(%q): %v", key, err)
		}

		// Read the file directly from disk, bypassing diskv's in-memory cache.
		// The prefix transform stores files at <dir>/<2-char-prefix>/<key>.
		diskPath := filepath.Join(dir, key[:2], key)
		got, err := os.ReadFile(diskPath) //nolint:gosec // test reads from t.TempDir()
		if err != nil {
			rt.Fatalf("os.ReadFile(%q): %v", diskPath, err)
		}

		if len(got) == 0 {
			rt.Fatalf("on-disk file %q is zero-length after Write", diskPath)
		}

		if !bytes.Equal(got, data) {
			rt.Fatalf("on-disk file %q: got %d bytes, want %d bytes", diskPath, len(got), len(data))
		}
	})
}

// TestPropertyObjectCacheDiskLayout verifies that the prefix transform places
// each cached object at <BasePath>/<2-char-prefix>/<key> as a single file.
// Erase removes the corresponding file. EraseAll removes all cached files
// (the .tmp directory and prefix directories may remain).
//
// **Validates: Requirements 1.3**
func TestPropertyObjectCacheDiskLayout(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir := t.TempDir()
		cache := api.NewObjectCache(dir)

		// Generate 1–10 unique keys (min 2 chars for prefix) with associated data.
		numKeys := rapid.IntRange(1, 10).Draw(rt, "numKeys")
		keys := make(map[string][]byte, numKeys)
		for len(keys) < numKeys {
			k := rapid.StringMatching(`[a-zA-Z0-9_\-]{2,64}`).Draw(rt, "key")
			if _, exists := keys[k]; exists {
				continue
			}
			keys[k] = rapid.SliceOfN(rapid.Byte(), 1, 512).Draw(rt, "data")
		}

		// Write all keys.
		for k, v := range keys {
			if err := cache.Write(k, v); err != nil {
				rt.Fatalf("Write(%q): %v", k, err)
			}
		}

		// Assert each key exists at <dir>/<prefix>/<key>.
		for k, v := range keys {
			path := filepath.Join(dir, k[:2], k)
			info, err := os.Stat(path)
			if err != nil {
				rt.Fatalf("os.Stat(%q): %v — expected file at BasePath/<prefix>/<key>", path, err)
			}
			if info.IsDir() {
				rt.Fatalf("%q is a directory, expected a regular file", path)
			}
			got, err := os.ReadFile(path) //nolint:gosec // test reads from t.TempDir()
			if err != nil {
				rt.Fatalf("os.ReadFile(%q): %v", path, err)
			}
			if !bytes.Equal(got, v) {
				rt.Fatalf("file %q: got %d bytes, want %d bytes", path, len(got), len(v))
			}
		}

		// Pick one key to erase and verify its file is removed while others remain.
		var eraseKey string
		for k := range keys {
			eraseKey = k
			break
		}
		if err := cache.Erase(eraseKey); err != nil {
			rt.Fatalf("Erase(%q): %v", eraseKey, err)
		}

		erasedPath := filepath.Join(dir, eraseKey[:2], eraseKey)
		if _, err := os.Stat(erasedPath); !os.IsNotExist(err) {
			rt.Fatalf("after Erase(%q): file still exists (err=%v)", eraseKey, err)
		}

		// Remaining keys must still be present on disk.
		for k := range keys {
			if k == eraseKey {
				continue
			}
			path := filepath.Join(dir, k[:2], k)
			if _, err := os.Stat(path); err != nil {
				rt.Fatalf("after Erase(%q): sibling file %q missing: %v", eraseKey, path, err)
			}
		}

		// EraseAll and assert no cached files remain. The base directory
		// itself may or may not exist after EraseAll (diskv removes it).
		if err := cache.EraseAll(); err != nil {
			rt.Fatalf("EraseAll: %v", err)
		}

		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			// Directory removed entirely — that's fine.
			return
		}
		if err != nil {
			rt.Fatalf("os.ReadDir(%q) after EraseAll: %v", dir, err)
		}
		for _, e := range entries {
			if e.Name() != ".tmp" {
				rt.Fatalf("after EraseAll: unexpected entry %q in %q", e.Name(), dir)
			}
		}
	})
}
