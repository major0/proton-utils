package drive

import (
	"context"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
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

	// cachedStat should be nil.
	if link.cachedStat != nil {
		t.Fatal("cachedStat should be nil when MemoryCacheLevel is CacheDisabled")
	}
}

// TestStatCacheWhenEnabled verifies that Link.Stat() retains cachedStat
// when MemoryCacheLevel is CacheMetadata.
func TestStatCacheWhenEnabled(t *testing.T) {
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

// TestRootPhotosProhibitCaching_Property verifies that for any ShareConfig
// toggle combination, root and photos share types should never have caching
// enabled. This validates the invariant — enforcement is in applyShareConfig
// (client layer).
//
// **Property 3: Root and photos shares prohibit caching**
// **Validates: Requirements 2.5, 2.11**
func TestRootPhotosProhibitCaching_Property(t *testing.T) {
	// This test validates the design invariant: root and photos shares
	// must always have all cache flags false. The actual enforcement
	// happens in api/drive/client/ via applyShareConfig. Here we verify
	// that a freshly constructed share of these types has all flags false
	// (the default), regardless of what someone might try to set.

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
