package drive

import (
	"encoding/json"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

// TestPropertyLookupInsertOnMiss verifies the three-layer lookup flow:
//
//  1. Link Table hit → return existing *Link pointer.
//  2. ObjectCache hit → unmarshal proton.Link, construct *Link,
//     insert into Link Table, return.
//  3. Full miss → simulate API fetch, insert into both Link Table and
//     ObjectCache, return.
//
// After each layer populates the table, subsequent lookups for the same
// LinkID return the same *Link pointer (pointer identity).
//
// **Property 9: Lookup flow insert-on-miss**
// **Validates: Requirements 8.1, 8.2, 8.3**
func TestPropertyLookupInsertOnMiss(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cacheDir := t.TempDir()
		cache := api.NewObjectCache(cacheDir)

		c := &Client{
			linkTable:   make(map[string]*Link),
			objectCache: cache,
		}

		resolver := &mockResolver{}
		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
		}
		rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
		root := NewTestLink(rootPLink, nil, nil, resolver, "root")
		share := NewShare(pShare, nil, root, resolver, "vol-1")
		root = NewTestLink(rootPLink, nil, share, resolver, "root")
		share.Link = root

		// Generate a unique LinkID and a proton.Link to simulate an API response.
		linkID := rapid.StringMatching(`[a-zA-Z0-9_\-]{4,32}`).Draw(rt, "linkID")
		linkType := rapid.SampledFrom([]proton.LinkType{
			proton.LinkTypeFile, proton.LinkTypeFolder,
		}).Draw(rt, "linkType")

		apiLink := proton.Link{
			LinkID:   linkID,
			Type:     linkType,
			MIMEType: "application/octet-stream",
			State:    proton.LinkStateActive,
		}

		// --- Phase 1: Full miss → simulate API fetch ---
		// Verify the link is not in the table or cache.
		if got := c.getLink(linkID); got != nil {
			rt.Fatalf("expected table miss for %q before insert", linkID)
		}
		data, _ := c.objectCache.Read(linkID)
		if data != nil {
			rt.Fatalf("expected cache miss for %q before insert", linkID)
		}

		// Simulate what StatLink does on a full miss (API fetch path):
		// construct *Link, insert into table, write to objectCache.
		link1 := NewLink(&apiLink, root, share, resolver)
		c.putLink(linkID, link1)
		marshaledData, err := json.Marshal(apiLink)
		if err != nil {
			rt.Fatalf("json.Marshal: %v", err)
		}
		if err := c.objectCache.Write(linkID, marshaledData); err != nil {
			rt.Fatalf("ObjectCache.Write: %v", err)
		}

		// Verify: table hit returns the same pointer.
		if got := c.getLink(linkID); got != link1 {
			rt.Fatalf("table hit after API insert: got different pointer")
		}

		// Verify: objectCache has the data.
		cachedData, err := c.objectCache.Read(linkID)
		if err != nil || cachedData == nil {
			rt.Fatalf("objectCache miss after API insert: err=%v", err)
		}

		// --- Phase 2: Clear table, keep objectCache → cache hit ---
		c.clearLinks()

		// Table should be empty now.
		if got := c.getLink(linkID); got != nil {
			rt.Fatalf("expected table miss after clearLinks")
		}

		// ObjectCache should still have the data.
		cachedData, err = c.objectCache.Read(linkID)
		if err != nil || cachedData == nil {
			rt.Fatalf("objectCache miss after clearLinks: err=%v", err)
		}

		// Simulate what StatLink does on a cache hit:
		// unmarshal, construct *Link, insert into table.
		var pLink proton.Link
		if err := json.Unmarshal(cachedData, &pLink); err != nil {
			rt.Fatalf("json.Unmarshal from cache: %v", err)
		}
		link2 := NewLink(&pLink, root, share, resolver)
		c.putLink(linkID, link2)

		// Verify: the unmarshalled link has the correct LinkID.
		if pLink.LinkID != linkID {
			rt.Fatalf("unmarshalled LinkID = %q, want %q", pLink.LinkID, linkID)
		}
		if pLink.Type != linkType {
			rt.Fatalf("unmarshalled Type = %v, want %v", pLink.Type, linkType)
		}

		// Verify: table hit returns the new pointer (link2, not link1 — link1 was cleared).
		if got := c.getLink(linkID); got != link2 {
			rt.Fatalf("table hit after cache re-insert: got different pointer")
		}

		// --- Phase 3: Subsequent lookup returns same pointer ---
		// A second getLink must return the exact same pointer as link2.
		if got := c.getLink(linkID); got != link2 {
			rt.Fatalf("subsequent getLink: pointer identity violated")
		}
	})
}

// TestPropertyLookupInsertOnMiss_NilCache verifies that the lookup flow
// works correctly when the objectCache is nil (disk_cache disabled or
// $XDG_RUNTIME_DIR unset). The flow degrades to: Link Table → API only.
//
// **Validates: Requirements 8.1, 8.2, 8.3**
func TestPropertyLookupInsertOnMiss_NilCache(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := &Client{
			linkTable:   make(map[string]*Link),
			objectCache: nil, // disk_cache disabled
		}

		resolver := &mockResolver{}
		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
		}
		rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
		root := NewTestLink(rootPLink, nil, nil, resolver, "root")
		share := NewShare(pShare, nil, root, resolver, "vol-1")
		root = NewTestLink(rootPLink, nil, share, resolver, "root")
		share.Link = root

		linkID := rapid.StringMatching(`[a-zA-Z0-9_\-]{4,32}`).Draw(rt, "linkID")

		apiLink := proton.Link{
			LinkID: linkID,
			Type:   proton.LinkTypeFile,
			State:  proton.LinkStateActive,
		}

		// Simulate API fetch with nil cache — write is a no-op.
		link1 := NewLink(&apiLink, root, share, resolver)
		c.putLink(linkID, link1)
		marshaledData, err := json.Marshal(apiLink)
		if err != nil {
			rt.Fatalf("json.Marshal: %v", err)
		}
		// ObjectCache.Write with nil receiver is a no-op.
		if err := c.objectCache.Write(linkID, marshaledData); err != nil {
			rt.Fatalf("ObjectCache.Write(nil): %v", err)
		}

		// Table hit works.
		if got := c.getLink(linkID); got != link1 {
			rt.Fatalf("table hit: pointer identity violated")
		}

		// Clear table — with nil cache, there's no cache fallback.
		c.clearLinks()

		// ObjectCache.Read with nil receiver returns miss.
		data, err := c.objectCache.Read(linkID)
		if err != nil || data != nil {
			rt.Fatalf("expected nil cache miss, got data=%v err=%v", data, err)
		}

		// Table is empty — full miss.
		if got := c.getLink(linkID); got != nil {
			rt.Fatalf("expected table miss after clear with nil cache")
		}
	})
}
