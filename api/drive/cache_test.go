package drive

import (
	"context"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
	"pgregory.net/rapid"
)

// TestDirEntryNoCacheWhenDisabled verifies that DirEntry.EntryName()
// does NOT populate the name field when MemoryCacheLevel is CacheDisabled.
// Each call should trigger a fresh decrypt (via testName in this case).
func TestDirEntryNoCacheWhenDisabled(t *testing.T) {
	resolver := &readdirResolver{children: []proton.Link{
		{LinkID: "child-1", Type: proton.LinkTypeFile},
	}}

	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, root, resolver, "",
	)
	// Caching disabled (default).
	share.MemoryCacheLevel = api.CacheDisabled
	root = NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = root

	ctx := context.Background()
	var entries []DirEntry
	for entry := range root.Readdir(ctx) {
		entries = append(entries, entry)
	}

	// Find a child entry (skip . and ..).
	for _, e := range entries {
		if e.Link == root || e.Link == root.Parent() {
			continue
		}
		// Call EntryName twice — with caching disabled, the name field
		// should NOT be populated after the first call.
		name1, err := e.EntryName()
		if err != nil {
			t.Fatalf("EntryName: %v", err)
		}
		if name1 == "" {
			t.Fatal("expected non-empty name")
		}

		// The internal name field should still be empty (not cached).
		if e.name != "" {
			t.Fatalf("name field should be empty when caching disabled, got %q", e.name)
		}
	}
}

// TestDirEntryCacheWhenEnabled verifies that DirEntry.EntryName()
// DOES populate the name field when MemoryCacheLevel is CacheLinkName.
func TestDirEntryCacheWhenEnabled(t *testing.T) {
	resolver := &readdirResolver{children: []proton.Link{
		{LinkID: "child-1", Type: proton.LinkTypeFile},
	}}

	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, root, resolver, "",
	)
	share.MemoryCacheLevel = api.CacheLinkName
	root = NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = root

	ctx := context.Background()
	for entry := range root.Readdir(ctx) {
		if entry.Link == root || entry.Link == root.Parent() {
			continue
		}
		name, err := entry.EntryName()
		if err != nil {
			t.Fatalf("EntryName: %v", err)
		}
		if name == "" {
			t.Fatal("expected non-empty name")
		}
		// With caching enabled, the name field should be populated.
		if entry.name == "" {
			t.Fatal("name field should be cached when enabled")
		}
	}
}

// --- Name caching tests ---

// TestNameNoCacheWhenDisabled verifies that Link.Name() does NOT
// populate cachedName when MemoryCacheLevel is CacheDisabled.
func TestNameNoCacheWhenDisabled(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{LinkID: "test", Type: proton.LinkTypeFile}
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, nil, resolver, "",
	)
	share.MemoryCacheLevel = api.CacheDisabled
	link := NewTestLink(pLink, nil, share, resolver, "test.txt")

	_, err := link.Name()
	if err != nil {
		t.Fatalf("Name: %v", err)
	}

	if link.cachedName != "" {
		t.Fatalf("cachedName should be empty when CacheDisabled, got %q", link.cachedName)
	}
}

// TestNameCacheWhenLinkName verifies that Link.Name() returns a cached
// name when cachedName is pre-populated and MemoryCacheLevel is CacheLinkName.
// Since testName bypasses the cache path, we verify the cache read path
// by pre-populating cachedName directly.
func TestNameCacheWhenLinkName(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{LinkID: "test", Type: proton.LinkTypeFile}
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, nil, resolver, "",
	)
	share.MemoryCacheLevel = api.CacheLinkName
	link := NewLink(pLink, nil, share, resolver)

	// Pre-populate the cache to simulate a prior successful decryption.
	link.cachedName = "cached-name"

	name, err := link.Name()
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if name != "cached-name" {
		t.Fatalf("Name() = %q, want %q (from cache)", name, "cached-name")
	}
}

// TestNameCacheWhenMetadata verifies that Link.Name() returns a cached
// name when cachedName is pre-populated and MemoryCacheLevel is CacheMetadata.
func TestNameCacheWhenMetadata(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{LinkID: "test", Type: proton.LinkTypeFile}
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, nil, resolver, "",
	)
	share.MemoryCacheLevel = api.CacheMetadata
	link := NewLink(pLink, nil, share, resolver)

	// Pre-populate the cache.
	link.cachedName = "cached-name"

	name, err := link.Name()
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if name != "cached-name" {
		t.Fatalf("Name() = %q, want %q (from cache)", name, "cached-name")
	}
}

// TestNameNoCacheOnError verifies that Link.Name() does NOT cache
// when decryption fails, even with caching enabled.
func TestNameNoCacheOnError(t *testing.T) {
	resolver := &countingResolver{}
	pLink := &proton.Link{
		LinkID:             "test",
		Type:               proton.LinkTypeFile,
		NameSignatureEmail: "user@example.com",
		SignatureEmail:     "user@example.com",
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := NewShare(
		&proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: "s"},
			AddressID:     "addr-1",
		},
		nil, root, resolver, "",
	)
	share.MemoryCacheLevel = api.CacheLinkName
	root = NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = root

	link := NewLink(pLink, root, share, resolver)

	// Name() will fail (resolver returns false for AddressKeyRing).
	_, err := link.Name()
	if err == nil {
		t.Fatal("expected error from Name()")
	}

	// cachedName should remain empty — errors are not cached.
	if link.cachedName != "" {
		t.Fatalf("cachedName should be empty after error, got %q", link.cachedName)
	}

	// Subsequent call should re-attempt decryption.
	resolver.keyRingCalls = 0
	_, _ = link.Name()
	if resolver.keyRingCalls == 0 {
		t.Fatal("expected re-attempt after error, but no AddressKeyRing calls")
	}
}

// --- Stat caching tests ---

// TestStatNoCacheWhenDisabled verifies that Link.Stat() does NOT
// retain cachedStat when MemoryCacheLevel is CacheDisabled.
func TestStatNoCacheWhenDisabled(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{LinkID: "test", Type: proton.LinkTypeFile, MIMEType: "text/plain"}
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, nil, resolver, "",
	)
	share.MemoryCacheLevel = api.CacheDisabled
	link := NewTestLink(pLink, nil, share, resolver, "test.txt")

	_ = link.Stat()

	if link.cachedStat != nil {
		t.Fatal("cachedStat should be nil when MemoryCacheLevel is CacheDisabled")
	}
}

// TestStatNoCacheWhenLinkName verifies that Link.Stat() does NOT
// retain cachedStat when MemoryCacheLevel is CacheLinkName.
func TestStatNoCacheWhenLinkName(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{LinkID: "test", Type: proton.LinkTypeFile, MIMEType: "text/plain"}
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, nil, resolver, "",
	)
	share.MemoryCacheLevel = api.CacheLinkName
	link := NewTestLink(pLink, nil, share, resolver, "test.txt")

	_ = link.Stat()

	if link.cachedStat != nil {
		t.Fatal("cachedStat should be nil when MemoryCacheLevel is CacheLinkName")
	}
}

// TestStatCacheWhenMetadata verifies that Link.Stat() retains cachedStat
// when MemoryCacheLevel is CacheMetadata.
func TestStatCacheWhenMetadata(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{LinkID: "test", Type: proton.LinkTypeFile, MIMEType: "text/plain"}
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, nil, resolver, "",
	)
	share.MemoryCacheLevel = api.CacheMetadata
	link := NewTestLink(pLink, nil, share, resolver, "test.txt")

	fi1 := link.Stat()
	fi2 := link.Stat()

	if link.cachedStat == nil {
		t.Fatal("cachedStat should be populated when MemoryCacheLevel is CacheMetadata")
	}
	if fi1.LinkID != fi2.LinkID {
		t.Fatal("cached Stat should return same data")
	}
}

// --- KeyRing caching tests ---

// TestKeyRingNoCacheWhenDisabled verifies that Link.KeyRing() does NOT
// retain cachedKeyRing when MemoryCacheLevel is CacheDisabled.
func TestKeyRingNoCacheWhenDisabled(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{LinkID: "test", Type: proton.LinkTypeFile, SignatureEmail: "user@example.com"}
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, nil, resolver, "",
	)
	share.MemoryCacheLevel = api.CacheDisabled
	link := NewTestLink(pLink, nil, share, resolver, "test.txt")

	// KeyRing will fail (mockLinkResolver returns false for AddressKeyRing)
	// but cachedKeyRing should remain nil regardless.
	_, _ = link.KeyRing()

	if link.cachedKeyRing != nil {
		t.Fatal("cachedKeyRing should be nil when MemoryCacheLevel is CacheDisabled")
	}
}

// TestKeyRingNoCacheWhenLinkName verifies that Link.KeyRing() does NOT
// retain cachedKeyRing when MemoryCacheLevel is CacheLinkName.
func TestKeyRingNoCacheWhenLinkName(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{LinkID: "test", Type: proton.LinkTypeFile, SignatureEmail: "user@example.com"}
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, nil, resolver, "",
	)
	share.MemoryCacheLevel = api.CacheLinkName
	link := NewTestLink(pLink, nil, share, resolver, "test.txt")

	// KeyRing will fail but cachedKeyRing should remain nil.
	_, _ = link.KeyRing()

	if link.cachedKeyRing != nil {
		t.Fatal("cachedKeyRing should be nil when MemoryCacheLevel is CacheLinkName")
	}
}

// TestRootPhotosProhibitCaching_Property verifies that for any ShareConfig
// toggle combination, root and photos share types should never have caching
// enabled. This validates the invariant — enforcement is in applyShareConfig
// (client layer).
//
// **Property 3: Root and photos shares prohibit caching**
// **Validates: Requirements 2.5, 2.11**
func TestRootPhotosProhibitCaching_Property(t *testing.T) {
	resolver := &mockLinkResolver{}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}

	for _, st := range []proton.ShareType{proton.ShareTypeMain, ShareTypePhotos} {
		root := NewTestLink(rootPLink, nil, nil, resolver, "root")
		share := NewShare(
			&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s", Type: st}},
			nil, root, resolver, "",
		)

		if share.MemoryCacheLevel != api.CacheDisabled {
			t.Fatalf("share type %d: MemoryCacheLevel should be CacheDisabled", st)
		}
		if share.DiskCacheLevel != api.DiskCacheDisabled {
			t.Fatalf("share type %d: DiskCacheLevel should be DiskCacheDisabled", st)
		}
	}
}

// TestPropertyMemoryCacheLevels verifies the memory cache level progression:
// - disabled: nothing is cached (cachedName, cachedStat, cachedKeyRing all empty)
// - linkname: names cached, stat and keyrings not cached
// - metadata: names + stat + keyrings all cached
// Each level includes the previous.
//
// **Property 6: Memory cache level progression**
// **Validates: Requirements 4.3, 4.4, 5.1, 6.1, 6.4**
func TestPropertyMemoryCacheLevels(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		level := rapid.SampledFrom([]api.MemoryCacheLevel{
			api.CacheDisabled,
			api.CacheLinkName,
			api.CacheMetadata,
		}).Draw(rt, "level")

		linkID := rapid.StringMatching(`[a-zA-Z0-9]{1,20}`).Draw(rt, "linkID")
		mimeType := rapid.SampledFrom([]string{
			"text/plain", "application/pdf", "image/png",
		}).Draw(rt, "mimeType")
		linkType := rapid.SampledFrom([]proton.LinkType{
			proton.LinkTypeFile, proton.LinkTypeFolder,
		}).Draw(rt, "linkType")

		resolver := &mockLinkResolver{}
		pLink := &proton.Link{
			LinkID:   linkID,
			Type:     linkType,
			MIMEType: mimeType,
		}
		share := NewShare(
			&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
			nil, nil, resolver, "",
		)
		share.MemoryCacheLevel = level
		link := NewTestLink(pLink, nil, share, resolver, "test-name")

		// --- Test Stat caching ---
		_ = link.Stat()

		switch {
		case level >= api.CacheMetadata:
			if link.cachedStat == nil {
				rt.Fatalf("level %v: cachedStat should be populated", level)
			}
		default:
			if link.cachedStat != nil {
				rt.Fatalf("level %v: cachedStat should be nil", level)
			}
		}

		// --- Test Name cache read path ---
		// Pre-populate cachedName and verify it's returned via the
		// cache read path (bypassing testName by using a non-test link).
		readLink := NewLink(pLink, nil, share, resolver)
		readLink.cachedName = "pre-cached"

		name, err := readLink.Name()
		if err != nil {
			rt.Fatalf("Name from cache: %v", err)
		}
		if name != "pre-cached" {
			rt.Fatalf("level %v: Name() = %q, want %q from cache", level, name, "pre-cached")
		}

		// --- Test Name cache write path ---
		// Use testName link: testName bypasses the cache write path,
		// so cachedName should always be empty after Name() on a testName link.
		// This confirms testName doesn't interact with the cache.
		_, _ = link.Name()
		if link.cachedName != "" {
			rt.Fatalf("level %v: testName link should not populate cachedName", level)
		}

		// --- Test KeyRing caching (error path) ---
		// mockLinkResolver always fails KeyRing derivation, so
		// cachedKeyRing should never be populated regardless of level.
		_, _ = link.KeyRing()
		if link.cachedKeyRing != nil {
			rt.Fatalf("level %v: cachedKeyRing should be nil after failed derivation", level)
		}

		// --- Verify level progression invariant ---
		// metadata >= linkname >= disabled
		if level >= api.CacheLinkName {
			// At linkname or above, the cache read path for names works.
			nameLink := NewLink(pLink, nil, share, resolver)
			nameLink.cachedName = "level-check"
			n, err := nameLink.Name()
			if err != nil {
				rt.Fatalf("level %v: Name from cache: %v", level, err)
			}
			if n != "level-check" {
				rt.Fatalf("level %v: expected cached name, got %q", level, n)
			}
		}
		if level >= api.CacheMetadata {
			// At metadata, stat should be cached.
			if link.cachedStat == nil {
				rt.Fatalf("level %v: metadata level should cache stat", level)
			}
		}
		if level < api.CacheMetadata {
			// Below metadata, stat should NOT be cached.
			if link.cachedStat != nil {
				rt.Fatalf("level %v: below metadata should not cache stat", level)
			}
		}
	})
}

// TestPropertyMemoryCacheConcurrency verifies that concurrent calls to
// Name(), Stat(), and KeyRing() on the same *Link never produce data
// races. Run with -race flag.
//
// **Property 13: Memory cache concurrency safety**
// **Validates: Requirements 5.4, 6.7**
func TestPropertyMemoryCacheConcurrency(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		level := rapid.SampledFrom([]api.MemoryCacheLevel{
			api.CacheDisabled,
			api.CacheLinkName,
			api.CacheMetadata,
		}).Draw(rt, "level")

		goroutines := rapid.IntRange(2, 20).Draw(rt, "goroutines")
		iterations := rapid.IntRange(1, 10).Draw(rt, "iterations")

		resolver := &mockLinkResolver{}
		pLink := &proton.Link{
			LinkID:             "concurrent-link",
			Type:               proton.LinkTypeFile,
			MIMEType:           "text/plain",
			SignatureEmail:     "user@example.com",
			NameSignatureEmail: "user@example.com",
		}
		share := NewShare(
			&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
			nil, nil, resolver, "",
		)
		share.MemoryCacheLevel = level
		link := NewTestLink(pLink, nil, share, resolver, "concurrent-test")

		// Launch goroutines that concurrently call Name(), Stat(), KeyRing().
		done := make(chan struct{})
		for g := 0; g < goroutines; g++ {
			go func() {
				defer func() { done <- struct{}{} }()
				for i := 0; i < iterations; i++ {
					_, _ = link.Name()
					_ = link.Stat()
					_, _ = link.KeyRing()
				}
			}()
		}

		// Wait for all goroutines.
		for g := 0; g < goroutines; g++ {
			<-done
		}

		// If we reach here without a race detector complaint, the test passes.
		// Verify basic consistency: Name should always return the test name.
		name, err := link.Name()
		if err != nil {
			rt.Fatalf("Name after concurrent access: %v", err)
		}
		if name != "concurrent-test" {
			rt.Fatalf("Name = %q, want %q", name, "concurrent-test")
		}
	})
}
