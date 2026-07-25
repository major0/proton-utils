package drive

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
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

// --- Unit tests for staged-incomplete seeding (Req 1.8) and context cancellation (Req 1.7) ---

// TestStagedIncompleteSeeding verifies that when hydratedLinks contains an
// incomplete file entry (empty XAttr), GetCachedLink skips the GetLink call
// and only issues GetRevisionAllBlocks to fetch the XAttr.
//
// **Validates: Requirements 1.8**
func TestStagedIncompleteSeeding(t *testing.T) {
	linkID := "staged-link-1"
	shareID := "test-share"
	revID := "revision-abc"
	xattrValue := "encrypted-xattr"

	var getLinkCalls int
	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) {
			getLinkCalls++
			return proton.Link{}, fmt.Errorf("should not be called")
		},
		getRevisionFn: func(_ context.Context, _, _, rid string) (proton.Revision, error) {
			if rid != revID {
				t.Fatalf("expected revID %q, got %q", revID, rid)
			}
			return proton.Revision{
				RevisionMetadata: proton.RevisionMetadata{
					XAttr: xattrValue,
					Size:  1024,
				},
			}, nil
		},
	}

	client := newPropTestClient(t, mock)

	// Seed the staging map with an incomplete file entry.
	client.tableMu.Lock()
	client.hydratedLinks[linkID] = proton.Link{
		LinkID: linkID,
		Type:   proton.LinkTypeFile,
		FileProperties: &proton.FileProperties{
			ActiveRevision: proton.RevisionMetadata{
				ID:    revID,
				XAttr: "", // incomplete
			},
		},
	}
	client.tableMu.Unlock()

	result, err := client.GetCachedLink(context.Background(), shareID, linkID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// GetLink should NOT have been called — the staged Link was used.
	if getLinkCalls != 0 {
		t.Fatalf("expected 0 GetLink calls, got %d", getLinkCalls)
	}

	// XAttr should be populated.
	if result.FileProperties == nil || result.FileProperties.ActiveRevision.XAttr != xattrValue {
		t.Fatalf("expected XAttr %q, got %q", xattrValue, result.FileProperties.ActiveRevision.XAttr)
	}
}

// TestContextCancellationPropagatesOnGetLink verifies that a cancelled caller
// context causes GetLink cancellation to propagate as an error with no writes
// to the ObjectCache.
//
// **Validates: Requirements 1.7**
func TestContextCancellationPropagatesOnGetLink(t *testing.T) {
	linkID := "cancel-link-1"
	shareID := "test-share"

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mock := &mockFetcher{
		getLinkFn: func(ctx context.Context, _, _ string) (proton.Link, error) {
			return proton.Link{}, ctx.Err()
		},
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			t.Fatal("GetRevisionAllBlocks should not be called")
			return proton.Revision{}, nil
		},
	}

	client := newPropTestClient(t, mock)
	_, err := client.GetCachedLink(ctx, shareID, linkID)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}

	// Nothing should be written.
	data, _ := client.objectCache.Read(SanitizeLinkID(linkID))
	if data != nil {
		t.Fatal("ObjectCache should be empty after cancelled GetLink")
	}
}

// TestContextCancellationDuringXAttrFollowsFailurePath verifies that when the
// XAttr call is cancelled, the XAttr-failure path applies: the Link is
// returned with empty XAttr, xattrFailCount is incremented, no cache write
// occurs, and no error is returned to the caller.
//
// **Validates: Requirements 1.7**
func TestContextCancellationDuringXAttrFollowsFailurePath(t *testing.T) {
	linkID := "cancel-xattr-1"
	shareID := "test-share"
	revID := "rev-xyz"

	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, lid string) (proton.Link, error) {
			return proton.Link{
				LinkID: lid,
				Type:   proton.LinkTypeFile,
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{ID: revID},
				},
			}, nil
		},
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{}, context.Canceled
		},
	}

	client := newPropTestClient(t, mock)
	result, err := client.GetCachedLink(context.Background(), shareID, linkID)

	// No error returned — XAttr failure is graceful.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Link returned with empty XAttr.
	if result.FileProperties.ActiveRevision.XAttr != "" {
		t.Fatalf("expected empty XAttr, got %q", result.FileProperties.ActiveRevision.XAttr)
	}

	// xattrFailCount incremented.
	client.tableMu.RLock()
	count := client.xattrFailCount[linkID]
	client.tableMu.RUnlock()
	if count != 1 {
		t.Fatalf("expected xattrFailCount == 1, got %d", count)
	}

	// No cache write.
	data, _ := client.objectCache.Read(SanitizeLinkID(linkID))
	if data != nil {
		t.Fatal("ObjectCache should be empty after XAttr cancellation")
	}
}

// Feature: object-cache-disk, Task 5.3: Staging-map promotion verification
// TestHydratedLinkPromotionViaGetCachedLink verifies that a complete entry
// seeded into hydratedLinks is returned by GetCachedLink, removed from
// hydratedLinks, and subsequently inserted into the Link_Table by the
// caller (StatLink pattern: GetCachedLink + putLink).
//
// **Validates: Requirements 4.6**
func TestHydratedLinkPromotionViaGetCachedLink(t *testing.T) {
	linkID := "hydrated-promo-1"
	shareID := "test-share"

	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) {
			t.Fatal("GetLink should NOT be called — staging map has the entry")
			return proton.Link{}, nil
		},
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			t.Fatal("GetRevisionAllBlocks should NOT be called — entry is complete")
			return proton.Revision{}, nil
		},
	}

	client := newPropTestClient(t, mock)

	// Seed hydratedLinks with a complete folder entry.
	completeFolderLink := proton.Link{
		LinkID: linkID,
		Type:   proton.LinkTypeFolder,
		FolderProperties: &proton.FolderProperties{
			NodeHashKey: "test-hash-key",
		},
	}
	client.tableMu.Lock()
	client.hydratedLinks[linkID] = completeFolderLink
	client.tableMu.Unlock()

	// Simulate the StatLink path: GetCachedLink → putLink.
	pLink, err := client.GetCachedLink(context.Background(), shareID, linkID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pLink.LinkID != linkID || pLink.Type != proton.LinkTypeFolder {
		t.Fatalf("unexpected link returned: %+v", pLink)
	}

	// Simulate StatLink's follow-up: construct *Link and insert into table.
	link := NewLink(&pLink, nil, &Share{DiskCacheLevel: api.DiskCacheObjectStore}, client)
	client.putLink(linkID, link)

	// Verify: entry removed from hydratedLinks.
	client.tableMu.RLock()
	_, stillStaged := client.hydratedLinks[linkID]
	client.tableMu.RUnlock()
	if stillStaged {
		t.Fatal("hydratedLinks should not contain the entry after GetCachedLink consumed it")
	}

	// Verify: entry present in linkTable.
	if got := client.GetLink(linkID); got == nil {
		t.Fatal("linkTable should contain the entry after putLink")
	}
}

// TestHydratedLinkPromotionCompleteFile verifies that a complete file-type
// entry in hydratedLinks (with XAttr populated) is promoted without any
// API calls.
//
// **Validates: Requirements 4.6**
func TestHydratedLinkPromotionCompleteFile(t *testing.T) {
	linkID := "hydrated-file-1"
	shareID := "test-share"

	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) {
			t.Fatal("GetLink should NOT be called — staging map has the complete entry")
			return proton.Link{}, nil
		},
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			t.Fatal("GetRevisionAllBlocks should NOT be called — entry is complete")
			return proton.Revision{}, nil
		},
	}

	client := newPropTestClient(t, mock)

	// Seed hydratedLinks with a complete file entry (XAttr populated).
	completeFileLink := proton.Link{
		LinkID: linkID,
		Type:   proton.LinkTypeFile,
		FileProperties: &proton.FileProperties{
			ActiveRevision: proton.RevisionMetadata{
				ID:    "rev-complete",
				XAttr: "encrypted-xattr-data",
				Size:  2048,
			},
		},
	}
	client.tableMu.Lock()
	client.hydratedLinks[linkID] = completeFileLink
	client.tableMu.Unlock()

	// GetCachedLink should return the staged entry as a hit.
	pLink, err := client.GetCachedLink(context.Background(), shareID, linkID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pLink.FileProperties == nil || pLink.FileProperties.ActiveRevision.XAttr != "encrypted-xattr-data" {
		t.Fatalf("expected complete XAttr, got: %+v", pLink.FileProperties)
	}

	// Verify: entry removed from hydratedLinks.
	client.tableMu.RLock()
	_, stillStaged := client.hydratedLinks[linkID]
	client.tableMu.RUnlock()
	if stillStaged {
		t.Fatal("hydratedLinks should not contain the entry after promotion")
	}
}

// TestNewChildLinkRemovesStagedEntry verifies that NewChildLink removes
// matching hydratedLinks entries when inserting a link into the table,
// preventing staged children from leaking permanently.
//
// **Validates: Requirements 4.6**
func TestNewChildLinkRemovesStagedEntry(t *testing.T) {
	childID := "staged-child-1"

	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) {
			return proton.Link{}, nil
		},
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{}, nil
		},
	}
	client := newPropTestClient(t, mock)

	// Seed hydratedLinks with an entry for the child.
	client.tableMu.Lock()
	client.hydratedLinks[childID] = proton.Link{
		LinkID: childID,
		Type:   proton.LinkTypeFile,
		FileProperties: &proton.FileProperties{
			ActiveRevision: proton.RevisionMetadata{
				ID:    "rev-x",
				XAttr: "xattr-value",
			},
		},
	}
	client.tableMu.Unlock()

	// Create a parent link.
	parentPLink := &proton.Link{
		LinkID: "parent-1",
		Type:   proton.LinkTypeFolder,
	}
	share := &Share{DiskCacheLevel: api.DiskCacheObjectStore}
	parent := NewLink(parentPLink, nil, share, client)

	// Call NewChildLink with the child — simulates the directory listing path.
	childPLink := &proton.Link{
		LinkID: childID,
		Type:   proton.LinkTypeFile,
		FileProperties: &proton.FileProperties{
			ActiveRevision: proton.RevisionMetadata{
				ID:    "rev-x",
				XAttr: "xattr-value",
			},
		},
	}
	resultLink := client.NewChildLink(context.Background(), parent, childPLink)
	if resultLink == nil {
		t.Fatal("NewChildLink returned nil")
	}

	// Verify: hydratedLinks entry was removed.
	client.tableMu.RLock()
	_, stillStaged := client.hydratedLinks[childID]
	client.tableMu.RUnlock()
	if stillStaged {
		t.Fatal("hydratedLinks should not contain child entry after NewChildLink")
	}

	// Verify: link IS in the table.
	if got := client.GetLink(childID); got == nil {
		t.Fatal("linkTable should contain the child after NewChildLink")
	}
}

// Feature: object-cache-disk
// TestNestedCallSafety verifies that GetCachedLink invoked from within a
// bounded semaphore pool completes without deadlock, confirming the inline
// fetch design (Req 1.5).
//
// **Validates: Requirements 1.5**
func TestNestedCallSafety(t *testing.T) {
	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, lid string) (proton.Link, error) {
			// Simulate a slow API call.
			time.Sleep(time.Millisecond)
			return proton.Link{
				LinkID: lid,
				Type:   proton.LinkTypeFile,
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{ID: "rev-1"},
				},
			}, nil
		},
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			time.Sleep(time.Millisecond)
			return proton.Revision{
				RevisionMetadata: proton.RevisionMetadata{
					XAttr: "test-xattr",
					Size:  42,
				},
			}, nil
		},
	}

	client := newPropTestClient(t, mock)

	// Use a small bounded pool to simulate the real Session.Sem.
	const poolSize = 4
	const numLinks = 50
	sem := make(chan struct{}, poolSize)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < numLinks; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sem <- struct{}{}        // acquire slot (like Sem.Go)
			defer func() { <-sem }() // release slot

			linkID := fmt.Sprintf("link-%d", id)
			_, err := client.GetCachedLink(ctx, "test-share", linkID)
			if err != nil {
				t.Errorf("GetCachedLink(%s): %v", linkID, err)
			}
		}(i)
	}

	// If this blocks forever, the inline-fetch design is broken.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		// Success — all workers completed.
	case <-ctx.Done():
		t.Fatal("DEADLOCK: GetCachedLink blocked when invoked from within a bounded pool")
	}
}

// --- Task 7.4: Unit tests for eviction-on-invalidation, clearLinks, and failcount reset ---

// TestEraseCacheEntryDecrementsCount verifies that eraseCacheEntry removes the
// disk entry, decrements cacheCount, and clears xattrFailCount for the link.
//
// **Validates: Requirements 1.3, 3.1, 3.6, 9.1, 9.2**
func TestEraseCacheEntryDecrementsCount(t *testing.T) {
	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) { return proton.Link{}, nil },
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{}, nil
		},
	}
	client := newPropTestClient(t, mock)

	// Write an entry.
	pLink := proton.Link{LinkID: "link-1", Type: proton.LinkTypeFolder}
	client.writeCacheEntry("link-1", pLink)
	if client.cacheCount.Load() != 1 {
		t.Fatalf("expected cacheCount 1, got %d", client.cacheCount.Load())
	}

	// Set up a failcount.
	client.tableMu.Lock()
	client.xattrFailCount["link-1"] = 3
	client.tableMu.Unlock()

	// Erase.
	client.eraseCacheEntry("link-1")

	// Count decremented.
	if client.cacheCount.Load() != 0 {
		t.Fatalf("expected cacheCount 0, got %d", client.cacheCount.Load())
	}

	// Failcount cleared.
	client.tableMu.RLock()
	if client.xattrFailCount["link-1"] != 0 {
		t.Fatalf("expected xattrFailCount cleared, got %d", client.xattrFailCount["link-1"])
	}
	client.tableMu.RUnlock()

	// Entry gone from disk.
	data, _ := client.objectCache.Read(SanitizeLinkID("link-1"))
	if data != nil {
		t.Fatal("expected entry erased from disk")
	}
}

// TestClearLinksResetsEverything verifies that clearLinks calls EraseAll, resets
// cacheCount to 0, clears xattrFailCount, hydratedLinks, and linkTable.
//
// **Validates: Requirements 1.3, 3.2**
func TestClearLinksResetsEverything(t *testing.T) {
	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) { return proton.Link{}, nil },
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{}, nil
		},
	}
	client := newPropTestClient(t, mock)

	// Write entries and set up state.
	client.writeCacheEntry("a", proton.Link{LinkID: "a", Type: proton.LinkTypeFolder})
	client.writeCacheEntry("b", proton.Link{LinkID: "b", Type: proton.LinkTypeFolder})
	if client.cacheCount.Load() != 2 {
		t.Fatalf("expected cacheCount 2, got %d", client.cacheCount.Load())
	}
	client.tableMu.Lock()
	client.xattrFailCount["a"] = 2
	client.xattrFailCount["b"] = 1
	client.hydratedLinks["c"] = proton.Link{LinkID: "c"}
	client.tableMu.Unlock()
	client.putLink("a", &Link{})

	// Clear.
	client.clearLinks()

	// Everything reset.
	if client.cacheCount.Load() != 0 {
		t.Fatalf("expected cacheCount 0, got %d", client.cacheCount.Load())
	}
	client.tableMu.RLock()
	if len(client.xattrFailCount) != 0 {
		t.Fatalf("expected xattrFailCount empty, got %d entries", len(client.xattrFailCount))
	}
	if len(client.hydratedLinks) != 0 {
		t.Fatalf("expected hydratedLinks empty, got %d entries", len(client.hydratedLinks))
	}
	if len(client.linkTable) != 0 {
		t.Fatalf("expected linkTable empty, got %d entries", len(client.linkTable))
	}
	client.tableMu.RUnlock()

	// Disk cache empty.
	data, _ := client.objectCache.Read(SanitizeLinkID("a"))
	if data != nil {
		t.Fatal("expected ObjectCache empty after clearLinks")
	}
}

// TestFailcountClearedOnInvalidation verifies that a link at maxXAttrRetries
// failures has its failcount cleared when invalidated via eraseCacheEntry.
//
// **Validates: Requirements 1.3, 3.1**
func TestFailcountClearedOnInvalidation(t *testing.T) {
	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) { return proton.Link{}, nil },
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{}, nil
		},
	}
	client := newPropTestClient(t, mock)

	// Set failcount to max.
	client.tableMu.Lock()
	client.xattrFailCount["link-x"] = maxXAttrRetries
	client.tableMu.Unlock()

	// Write an entry to have something to erase.
	client.writeCacheEntry("link-x", proton.Link{LinkID: "link-x", Type: proton.LinkTypeFolder})

	// Simulate event invalidation: deleteLink + eraseCacheEntry.
	client.deleteLink("link-x")
	client.eraseCacheEntry("link-x")

	// Failcount should be cleared.
	client.tableMu.RLock()
	count := client.xattrFailCount["link-x"]
	client.tableMu.RUnlock()
	if count != 0 {
		t.Fatalf("expected failcount cleared after invalidation, got %d", count)
	}
}

// TestEraseCacheEntryClampsCacheCount verifies that erasing a non-existent
// entry does not cause cacheCount to go negative.
//
// **Validates: Requirements 9.1, 9.2**
func TestEraseCacheEntryClampsCacheCount(t *testing.T) {
	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) { return proton.Link{}, nil },
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{}, nil
		},
	}
	client := newPropTestClient(t, mock)
	// cacheCount starts at 0.

	// Erase a non-existent entry — should not go negative.
	client.eraseCacheEntry("nonexistent")
	if client.cacheCount.Load() < 0 {
		t.Fatalf("cacheCount went negative: %d", client.cacheCount.Load())
	}
}

// TestMoveRenameDoNotErase verifies that deleteLink alone (as used by
// Move/Rename) does NOT erase the ObjectCache entry or change cacheCount.
//
// **Validates: Requirements 3.6**
func TestMoveRenameDoNotErase(t *testing.T) {
	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) { return proton.Link{}, nil },
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{}, nil
		},
	}
	client := newPropTestClient(t, mock)

	// Write an entry to disk.
	pLink := proton.Link{LinkID: "move-link", Type: proton.LinkTypeFolder}
	client.writeCacheEntry("move-link", pLink)
	countBefore := client.cacheCount.Load()
	if countBefore != 1 {
		t.Fatalf("expected cacheCount 1, got %d", countBefore)
	}

	// Also put in the linkTable so deleteLink has something to remove.
	client.putLink("move-link", &Link{})

	// Simulate Move/Rename: only deleteLink is called, NOT eraseCacheEntry.
	client.deleteLink("move-link")

	// cacheCount should be UNCHANGED — disk entry is still there.
	if client.cacheCount.Load() != countBefore {
		t.Fatalf("expected cacheCount unchanged at %d, got %d", countBefore, client.cacheCount.Load())
	}

	// ObjectCache should still have the entry.
	data, _ := client.objectCache.Read(SanitizeLinkID("move-link"))
	if data == nil {
		t.Fatal("expected ObjectCache entry to still exist after deleteLink (Move/Rename)")
	}

	// linkTable should be empty (deleteLink removes from table).
	if link := client.GetLink("move-link"); link != nil {
		t.Fatal("expected linkTable entry removed by deleteLink")
	}
}

// Feature: object-cache-disk, Task 8.4: Eviction goroutine singleton
// TestEvictionGoroutineSingleton verifies that at most one eviction goroutine
// is active at any time — if an eviction is already in progress when a new
// write exceeds the threshold, the new write skips triggering another eviction.
//
// **Validates: Requirements 5.7**
func TestEvictionGoroutineSingleton(t *testing.T) {
	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) { return proton.Link{}, nil },
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{}, nil
		},
	}
	client := newPropTestClient(t, mock)

	// Simulate an eviction already in progress.
	client.evicting.Store(true)
	// Set cacheCount above threshold.
	client.cacheCount.Store(int64(maxCacheEntries) + 1)

	// triggerEviction should be a no-op since evicting is already true.
	client.triggerEviction()

	// If a second goroutine was spawned, we'd see issues. Give it a moment.
	time.Sleep(50 * time.Millisecond)

	// The evicting flag should still be true (we set it, no goroutine reset it).
	if !client.evicting.Load() {
		t.Fatal("evicting flag should still be true — no new goroutine should have been spawned")
	}

	// Clean up: reset the flag.
	client.evicting.Store(false)
}

// Feature: object-cache-disk, Task 9.4: Concurrent write/erase race on the same LinkID
// TestConcurrentWriteEraseRace verifies that when a write and erase race on the
// same LinkID, both outcomes (entry present or absent) are accepted without
// error — no panics, no data races.
//
// **Validates: Requirements 8.5**
func TestConcurrentWriteEraseRace(t *testing.T) {
	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, lid string) (proton.Link, error) {
			return proton.Link{
				LinkID: lid, Type: proton.LinkTypeFile,
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{ID: "rev-race"},
				},
			}, nil
		},
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{
				RevisionMetadata: proton.RevisionMetadata{XAttr: "race-xattr", Size: 10},
			}, nil
		},
	}

	client := newPropTestClient(t, mock)
	linkID := "race-link"
	shareID := "test-share"

	const iterations = 100
	for i := 0; i < iterations; i++ {
		var wg sync.WaitGroup

		// Writer: GetCachedLink (writes on miss).
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = client.GetCachedLink(context.Background(), shareID, linkID)
		}()

		// Eraser: concurrently erase the same entry.
		wg.Add(1)
		go func() {
			defer wg.Done()
			client.eraseCacheEntry(linkID)
		}()

		wg.Wait()

		// Either outcome is acceptable — no panics, no data races.
		// The entry may be present or absent depending on ordering.
		// Just verify no crash occurred and cacheCount is non-negative.
		if client.cacheCount.Load() < 0 {
			t.Fatalf("iteration %d: cacheCount went negative", i)
		}

		// Clean up for next iteration.
		_ = client.objectCache.Erase(SanitizeLinkID(linkID))
		client.cacheCount.Store(0)
	}
}

// Feature: object-cache-disk, Task 9.5: End-to-end integration test
// TestEndToEndCacheMissHitRestartHydration exercises the full lifecycle:
// cache miss → sequential fetch → complete entry written → cache hit →
// simulate restart (new Client + hydrateFromCache over the same temp dir) →
// staged hit without an API call.
//
// Uses the linkFetcher mock with an invocation counter to assert zero API
// calls after hydration.
//
// **Validates: Requirements 2.1, 4.1, 4.6**
func TestEndToEndCacheMissHitRestartHydration(t *testing.T) {
	linkID := "e2e-link"
	shareID := "test-share"
	revID := "e2e-rev"
	xattrValue := "e2e-xattr"

	var getLinkCalls atomic.Int64
	var getRevisionCalls atomic.Int64

	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, lid string) (proton.Link, error) {
			getLinkCalls.Add(1)
			return proton.Link{
				LinkID: lid, Type: proton.LinkTypeFile,
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{ID: revID},
				},
			}, nil
		},
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			getRevisionCalls.Add(1)
			return proton.Revision{
				RevisionMetadata: proton.RevisionMetadata{XAttr: xattrValue, Size: 512},
			}, nil
		},
	}

	// Use a shared temp dir to simulate persistence across restarts.
	cacheDir := t.TempDir()
	oc := api.NewObjectCache(cacheDir)

	client1 := &Client{
		linkTable:      make(map[string]*Link),
		xattrFailCount: make(map[string]int),
		hydratedLinks:  make(map[string]proton.Link),
		objectCache:    oc,
		fetcher:        mock,
		Config: &api.SessionConfig{
			Shares: map[string]api.ShareConfig{
				shareID: {DiskCache: api.DiskCacheObjectStore},
			},
		},
	}

	// Phase 1: Cache miss → sequential fetch → complete entry written.
	result, err := client1.GetCachedLink(context.Background(), shareID, linkID)
	if err != nil {
		t.Fatalf("phase 1: %v", err)
	}
	if result.FileProperties == nil || result.FileProperties.ActiveRevision.XAttr != xattrValue {
		t.Fatalf("phase 1: expected XAttr %q", xattrValue)
	}
	if getLinkCalls.Load() != 1 || getRevisionCalls.Load() != 1 {
		t.Fatalf("phase 1: expected 1 GetLink + 1 GetRevision, got %d/%d",
			getLinkCalls.Load(), getRevisionCalls.Load())
	}

	// Phase 2: Cache hit — no API calls.
	result, err = client1.GetCachedLink(context.Background(), shareID, linkID)
	if err != nil {
		t.Fatalf("phase 2: %v", err)
	}
	if result.FileProperties.ActiveRevision.XAttr != xattrValue {
		t.Fatal("phase 2: XAttr mismatch")
	}
	if getLinkCalls.Load() != 1 || getRevisionCalls.Load() != 1 {
		t.Fatalf("phase 2: API calls should not increase, got %d/%d",
			getLinkCalls.Load(), getRevisionCalls.Load())
	}

	// Phase 3: Simulate restart — new Client over the same cache dir.
	oc2 := api.NewObjectCache(cacheDir)
	client2 := &Client{
		linkTable:      make(map[string]*Link),
		xattrFailCount: make(map[string]int),
		hydratedLinks:  make(map[string]proton.Link),
		objectCache:    oc2,
		fetcher:        mock,
		Config: &api.SessionConfig{
			Shares: map[string]api.ShareConfig{
				shareID: {DiskCache: api.DiskCacheObjectStore},
			},
		},
	}
	client2.hydrateFromCache()

	// Phase 4: GetCachedLink should hit the staging map — zero additional API calls.
	result, err = client2.GetCachedLink(context.Background(), shareID, linkID)
	if err != nil {
		t.Fatalf("phase 4: %v", err)
	}
	if result.FileProperties == nil || result.FileProperties.ActiveRevision.XAttr != xattrValue {
		t.Fatalf("phase 4: expected XAttr %q", xattrValue)
	}
	// API calls should still be 1/1 from phase 1 — no new calls.
	if getLinkCalls.Load() != 1 || getRevisionCalls.Load() != 1 {
		t.Fatalf("phase 4: expected zero new API calls, got total %d/%d",
			getLinkCalls.Load(), getRevisionCalls.Load())
	}
}

// Feature: object-cache-disk, Task 10.1: Security/permissions verification
// TestCachePermissions verifies that the ObjectCache prefix subdirectories are
// created with 0700 permissions and written entry files have 0600 permissions,
// ensuring that cached encrypted objects are not accessible to other users.
//
// Note: The basePath itself is verified via a fresh subdirectory (not
// t.TempDir()) because NewObjectCache creates it with os.MkdirAll(path, 0700).
// The prefix transform directories are created by diskv with PathPerm 0700.
//
// **Validates: Requirements 7.4**
func TestCachePermissions(t *testing.T) {
	// Create a fresh subdirectory within t.TempDir() so NewObjectCache
	// creates it from scratch with 0700 (t.TempDir() uses default perms).
	cacheDir := filepath.Join(t.TempDir(), "objectcache")
	oc := api.NewObjectCache(cacheDir)
	if oc == nil {
		t.Fatal("NewObjectCache returned nil")
	}

	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) { return proton.Link{}, nil },
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{}, nil
		},
	}

	client := &Client{
		linkTable:      make(map[string]*Link),
		xattrFailCount: make(map[string]int),
		hydratedLinks:  make(map[string]proton.Link),
		objectCache:    oc,
		fetcher:        mock,
		Config: &api.SessionConfig{
			Shares: map[string]api.ShareConfig{
				"test-share": {DiskCache: api.DiskCacheObjectStore},
			},
		},
	}

	// Write an entry — this creates the prefix subdirectory and file.
	pLink := proton.Link{LinkID: "perm-link", Type: proton.LinkTypeFolder}
	client.writeCacheEntry("perm-link", pLink)

	// Check basePath directory permissions (created by NewObjectCache).
	dirInfo, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatalf("stat basePath: %v", err)
	}
	if dirInfo.Mode().Perm() != 0700 {
		t.Fatalf("expected basePath permissions 0700, got %04o", dirInfo.Mode().Perm())
	}

	// Check prefix subdirectory permissions (created by diskv with PathPerm).
	sanitized := SanitizeLinkID("perm-link")
	prefixDir := filepath.Join(cacheDir, sanitized[:2])
	prefixInfo, err := os.Stat(prefixDir)
	if err != nil {
		t.Fatalf("stat prefix dir: %v", err)
	}
	if prefixInfo.Mode().Perm() != 0700 {
		t.Fatalf("expected prefix dir permissions 0700, got %04o", prefixInfo.Mode().Perm())
	}

	// Check file permissions.
	filePath := oc.PathFor(sanitized)
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if fileInfo.Mode().Perm() != 0600 {
		t.Fatalf("expected file permissions 0600, got %04o", fileInfo.Mode().Perm())
	}
}

// Feature: object-cache-disk, Task 10.1: Gob round-trip DeepEqual
// TestGobRoundTripDeepEqual verifies that a representative proton.Link with
// all fields populated survives a gob encode/decode cycle with exact
// field-level equality, confirming no transformation or decryption occurs
// during serialization.
//
// **Validates: Requirements 7.5**
func TestGobRoundTripDeepEqual(t *testing.T) {
	// Use a representative proton.Link with all fields populated.
	original := proton.Link{
		LinkID:         "test-id-123",
		ParentLinkID:   "parent-456",
		Type:           proton.LinkTypeFile,
		Name:           "encrypted-name-blob",
		Hash:           "hmac-hash",
		Size:           4096,
		State:          proton.LinkStateActive,
		MIMEType:       "application/octet-stream",
		CreateTime:     1700000000,
		ModifyTime:     1700001000,
		XAttr:          "link-level-xattr",
		NodeKey:        "encrypted-node-key",
		NodePassphrase: "encrypted-passphrase",
		SignatureEmail: "user@proton.me",
		FileProperties: &proton.FileProperties{
			ContentKeyPacket: "encrypted-key-packet",
			ActiveRevision: proton.RevisionMetadata{
				ID:    "revision-abc",
				XAttr: "encrypted-revision-xattr-blob",
				Size:  2048,
				State: proton.RevisionStateActive,
			},
		},
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(original); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var decoded proton.Link
	if err := gob.NewDecoder(&buf).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("gob round-trip produced different struct:\n  original: %+v\n  decoded:  %+v", original, decoded)
	}
}
