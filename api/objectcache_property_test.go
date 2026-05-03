package api

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// validKey generates a non-empty key string without path separators,
// suitable for use as a diskv key.
func validKey(t *rapid.T) string {
	return rapid.StringMatching(`[a-zA-Z0-9_\-]{1,64}`).Draw(t, "key")
}

// tempCache creates an ObjectCache in a temporary directory that is
// cleaned up when the test completes.
func tempCache(t *rapid.T) *ObjectCache {
	dir, err := os.MkdirTemp("", "objectcache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return NewObjectCache(dir)
}

// TestObjectCache_RoundTrip_Property verifies that for any non-empty key
// and any byte slice value, Write followed by Read returns identical bytes.
//
// **Validates: Requirements 3.1**
func TestObjectCache_RoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cache := tempCache(t)

		key := validKey(t)
		value := rapid.SliceOf(rapid.Byte()).Draw(t, "value")

		if err := cache.Write(key, value); err != nil {
			t.Fatalf("Write(%q): %v", key, err)
		}

		got, err := cache.Read(key)
		if err != nil {
			t.Fatalf("Read(%q): %v", key, err)
		}

		if !bytes.Equal(got, value) {
			t.Fatalf("round-trip mismatch for key %q: wrote %d bytes, read %d bytes",
				key, len(value), len(got))
		}
	})
}

// TestObjectCache_MultiKey_Property verifies that writing distinct keys
// does not cause cross-contamination — each key returns its own value.
//
// **Validates: Requirements 3.1**
func TestObjectCache_MultiKey_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cache := tempCache(t)

		n := rapid.IntRange(2, 20).Draw(t, "numKeys")
		keys := make([]string, n)
		values := make([][]byte, n)

		// Generate unique keys.
		seen := make(map[string]bool, n)
		for i := 0; i < n; i++ {
			for {
				k := validKey(t)
				if !seen[k] {
					seen[k] = true
					keys[i] = k
					break
				}
			}
			values[i] = rapid.SliceOf(rapid.Byte()).Draw(t, "value")
		}

		// Write all.
		for i := 0; i < n; i++ {
			if err := cache.Write(keys[i], values[i]); err != nil {
				t.Fatalf("Write(%q): %v", keys[i], err)
			}
		}

		// Read all and verify.
		for i := 0; i < n; i++ {
			got, err := cache.Read(keys[i])
			if err != nil {
				t.Fatalf("Read(%q): %v", keys[i], err)
			}
			if !bytes.Equal(got, values[i]) {
				t.Fatalf("key %q: wrote %d bytes, read %d bytes",
					keys[i], len(values[i]), len(got))
			}
		}
	})
}

// TestObjectCache_NilReceiver_Property verifies that all operations on a
// nil *ObjectCache return zero values without panicking.
//
// **Validates: Requirements 3.1**
func TestObjectCache_NilReceiver_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var cache *ObjectCache // nil

		key := validKey(t)
		value := rapid.SliceOf(rapid.Byte()).Draw(t, "value")

		// Read on nil returns (nil, nil).
		got, err := cache.Read(key)
		if got != nil || err != nil {
			t.Fatalf("nil.Read(%q) = (%v, %v), want (nil, nil)", key, got, err)
		}

		// Write on nil is a silent no-op.
		if err := cache.Write(key, value); err != nil {
			t.Fatalf("nil.Write(%q): %v", key, err)
		}

		// Erase on nil is a silent no-op.
		if err := cache.Erase(key); err != nil {
			t.Fatalf("nil.Erase(%q): %v", key, err)
		}

		// EraseAll on nil is a silent no-op.
		if err := cache.EraseAll(); err != nil {
			t.Fatalf("nil.EraseAll(): %v", err)
		}
	})
}

// TestObjectCache_EmptyBasePath verifies NewObjectCache("") returns nil
// (disabled caching) and nil cache operations do not panic.
func TestObjectCache_EmptyBasePath(t *testing.T) {
	cache := NewObjectCache("")
	if cache != nil {
		t.Fatal("NewObjectCache(\"\") should return nil")
	}

	got, err := cache.Read("anything")
	if got != nil || err != nil {
		t.Fatalf("nil cache Read = (%v, %v), want (nil, nil)", got, err)
	}
	if err := cache.Write("anything", []byte("data")); err != nil {
		t.Fatalf("nil cache Write: %v", err)
	}
}

// TestObjectCache_Erase_Property verifies that Erase removes a key so
// that subsequent Read returns a cache miss.
//
// **Validates: Requirements 3.1**
func TestObjectCache_Erase_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cache := tempCache(t)

		key := validKey(t)
		value := rapid.SliceOfN(rapid.Byte(), 1, -1).Draw(t, "value")

		if err := cache.Write(key, value); err != nil {
			t.Fatalf("Write(%q): %v", key, err)
		}

		if err := cache.Erase(key); err != nil {
			t.Fatalf("Erase(%q): %v", key, err)
		}

		got, err := cache.Read(key)
		if err != nil {
			t.Fatalf("Read(%q) after Erase: %v", key, err)
		}
		if got != nil {
			t.Fatalf("Read(%q) after Erase returned %d bytes, want nil", key, len(got))
		}
	})
}

// TestObjectCache_ReadMiss verifies Read returns nil for unknown keys.
func TestObjectCache_ReadMiss(t *testing.T) {
	dir := t.TempDir()
	cache := NewObjectCache(dir)

	got, err := cache.Read("nonexistent")
	if got != nil || err != nil {
		t.Fatalf("Read(nonexistent) = (%v, %v), want (nil, nil)", got, err)
	}
}

// TestObjectCache_KeyWithPathSeparator verifies that keys containing
// path separators do not cause panics. diskv with a flat transform may
// reject or accept these depending on the OS.
func TestObjectCache_KeyWithPathSeparator(t *testing.T) {
	dir := t.TempDir()
	cache := NewObjectCache(dir)

	for _, key := range []string{"a/b", "a\\b"} {
		if strings.ContainsRune(key, '/') || strings.ContainsRune(key, '\\') {
			// diskv may reject or accept these depending on OS.
			// We just verify no panic.
			_ = cache.Write(key, []byte("test"))
			_, _ = cache.Read(key)
		}
	}
}
