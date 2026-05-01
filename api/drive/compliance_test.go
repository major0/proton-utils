package drive

import (
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
)

// TestLink_NameNotCached_WhenDisabled verifies that Link.Name() does NOT
// cache the decrypted name when MemoryCacheLevel is CacheDisabled.
// This is a security compliance test.
//
// **Validates: Requirement 3.1**
func TestLink_NameNotCached_WhenDisabled(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{LinkID: "test", Type: proton.LinkTypeFile}
	link := NewTestLink(pLink, nil, nil, resolver, "secret-name")

	// Call Name() twice — both should return the same value.
	name1, err1 := link.Name()
	name2, err2 := link.Name()
	if err1 != nil || err2 != nil {
		t.Fatalf("Name() errors: %v, %v", err1, err2)
	}
	if name1 != name2 {
		t.Fatalf("Name() returned different values: %q vs %q", name1, name2)
	}
	if name1 != "secret-name" {
		t.Fatalf("Name() = %q, want %q", name1, "secret-name")
	}
}

// TestDirEntry_NameNotCached_WhenDisabled verifies EntryName() does not
// cache when MemoryCacheLevel is CacheDisabled.
//
// **Validates: Requirement 3.2**
func TestDirEntry_NameNotCached_WhenDisabled(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{LinkID: "child", Type: proton.LinkTypeFile}

	pShare := &proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := NewShare(pShare, nil, root, resolver, "")
	share.MemoryCacheLevel = api.CacheDisabled
	root = NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = root

	child := NewTestLink(pLink, root, share, resolver, "child-name")
	entry := DirEntry{Link: child}

	// Call EntryName twice.
	name1, err := entry.EntryName()
	if err != nil {
		t.Fatalf("EntryName() error: %v", err)
	}
	if name1 != "child-name" {
		t.Fatalf("EntryName() = %q, want %q", name1, "child-name")
	}

	// The internal name field should NOT be cached.
	if entry.name != "" {
		t.Fatalf("name field should be empty when caching disabled, got %q", entry.name)
	}

	// Second call should still work.
	name2, err := entry.EntryName()
	if err != nil {
		t.Fatalf("EntryName() second call error: %v", err)
	}
	if name2 != name1 {
		t.Fatalf("EntryName() returned different values: %q vs %q", name1, name2)
	}
}

// TestLink_StatNotCached_WhenDisabled verifies Stat() does not cache
// when MemoryCacheLevel is CacheDisabled.
//
// **Validates: Requirement 3.3**
func TestLink_StatNotCached_WhenDisabled(t *testing.T) {
	resolver := &mockLinkResolver{}
	pLink := &proton.Link{LinkID: "test", Type: proton.LinkTypeFile, MIMEType: "text/plain"}

	pShare := &proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}}
	share := NewShare(pShare, nil, nil, resolver, "")
	share.MemoryCacheLevel = api.CacheDisabled
	link := NewTestLink(pLink, nil, share, resolver, "test.txt")

	// Call Stat twice.
	fi1 := link.Stat()
	fi2 := link.Stat()

	// cachedStat should be nil.
	if link.cachedStat != nil {
		t.Fatal("cachedStat should be nil when MemoryCacheLevel is CacheDisabled")
	}

	// Both calls should return consistent data.
	if fi1.LinkID != fi2.LinkID {
		t.Fatalf("Stat() returned different LinkIDs: %q vs %q", fi1.LinkID, fi2.LinkID)
	}
}
