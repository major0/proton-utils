package client

import (
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
	"pgregory.net/rapid"
)

// TestPropertyMutationInvalidation verifies that after any Drive
// mutation, the affected links are absent from the Link Table and all
// their cached data is discarded. Clear removes all entries and calls
// EraseAll on the ObjectCache.
//
// Since actual mutation methods make API calls, this test exercises the
// invalidation logic directly by pre-populating the Link Table with
// known links, calling the invalidation helpers (deleteLink,
// objectCacheErase, clearLinks, objectCacheEraseAll), and verifying
// the expected entries are absent.
//
// **Property 8: Mutation invalidation**
// **Validates: Requirements 9.1, 9.2, 9.3, 9.4**
func TestPropertyMutationInvalidation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		resolver := &mockResolver{}
		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
		}
		rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
		root := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
		share := drive.NewShare(pShare, nil, root, resolver, "vol-1")
		root = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
		share.Link = root

		// Set up a diskv-backed ObjectCache in a temp dir.
		dir := t.TempDir()
		cache := NewObjectCache(dir, 0)

		c := &Client{
			linkTable:   make(map[string]*drive.Link),
			objectCache: cache,
		}

		// Generate a pool of unique LinkIDs for the test.
		numLinks := rapid.IntRange(5, 30).Draw(rt, "numLinks")
		ids := make([]string, 0, numLinks)
		idSet := make(map[string]bool, numLinks)
		for len(ids) < numLinks {
			id := rapid.StringMatching(`[a-zA-Z0-9_\-]{2,24}`).Draw(rt, "linkID")
			if idSet[id] || id == "root" {
				continue
			}
			idSet[id] = true
			ids = append(ids, id)
		}

		// Pre-populate the Link Table and ObjectCache with all links.
		links := make(map[string]*drive.Link, numLinks)
		for _, id := range ids {
			pLink := &proton.Link{LinkID: id, Type: proton.LinkTypeFolder}
			link := drive.NewTestLink(pLink, root, share, resolver, id)
			c.putLink(id, link)
			links[id] = link
			// Write a dummy entry to the ObjectCache.
			if err := objectCacheWrite(cache, id, []byte("encrypted-"+id)); err != nil {
				rt.Fatalf("objectCacheWrite %q: %v", id, err)
			}
		}

		// Pick a mutation type and exercise the corresponding
		// invalidation pattern.
		mutation := rapid.IntRange(0, 4).Draw(rt, "mutation")

		switch mutation {
		case 0:
			// Move(link, newParent): delete link + old parent + new parent.
			if len(ids) < 3 {
				return
			}
			movedID := ids[0]
			oldParentID := ids[1]
			newParentID := ids[2]

			c.deleteLink(movedID)
			c.deleteLink(oldParentID)
			c.deleteLink(newParentID)

			if c.getLink(movedID) != nil {
				rt.Fatal("Move: moved link still in table")
			}
			if c.getLink(oldParentID) != nil {
				rt.Fatal("Move: old parent still in table")
			}
			if c.getLink(newParentID) != nil {
				rt.Fatal("Move: new parent still in table")
			}

			// ObjectCache entries are NOT erased for Move (object unchanged).
			if !cache.Has(movedID) {
				rt.Fatal("Move: objectCache should still have moved link")
			}

		case 1:
			// Rename(link): delete link + parent.
			if len(ids) < 2 {
				return
			}
			renamedID := ids[0]
			parentID := ids[1]

			c.deleteLink(renamedID)
			c.deleteLink(parentID)

			if c.getLink(renamedID) != nil {
				rt.Fatal("Rename: renamed link still in table")
			}
			if c.getLink(parentID) != nil {
				rt.Fatal("Rename: parent still in table")
			}

			// ObjectCache entries are NOT erased for Rename (object unchanged).
			if !cache.Has(renamedID) {
				rt.Fatal("Rename: objectCache should still have renamed link")
			}

		case 2:
			// Remove(link): delete link + parent + objectCache.Erase(linkID).
			if len(ids) < 2 {
				return
			}
			removedID := ids[0]
			parentID := ids[1]

			c.deleteLink(removedID)
			c.deleteLink(parentID)
			if err := objectCacheErase(cache, removedID); err != nil {
				rt.Fatalf("objectCacheErase: %v", err)
			}

			if c.getLink(removedID) != nil {
				rt.Fatal("Remove: removed link still in table")
			}
			if c.getLink(parentID) != nil {
				rt.Fatal("Remove: parent still in table")
			}

			// ObjectCache entry for the removed link must be gone.
			if cache.Has(removedID) {
				rt.Fatal("Remove: objectCache should not have removed link")
			}

			// Other links' ObjectCache entries should still exist.
			if len(ids) > 2 {
				otherID := ids[2]
				if !cache.Has(otherID) {
					rt.Fatal("Remove: objectCache should still have unrelated link")
				}
			}

		case 3:
			// MkDir(parent): delete parent.
			parentID := ids[0]

			c.deleteLink(parentID)

			if c.getLink(parentID) != nil {
				rt.Fatal("MkDir: parent still in table")
			}

			// Other links should still be in the table.
			if len(ids) > 1 {
				otherID := ids[1]
				if c.getLink(otherID) == nil {
					rt.Fatal("MkDir: unrelated link should still be in table")
				}
			}

		case 4:
			// Clear: clear all Link Table entries + objectCache.EraseAll().
			c.clearLinks()
			if err := objectCacheEraseAll(cache); err != nil {
				rt.Fatalf("objectCacheEraseAll: %v", err)
			}

			// All links must be absent from the table.
			for _, id := range ids {
				if c.getLink(id) != nil {
					rt.Fatalf("Clear: link %q still in table", id)
				}
			}

			// All ObjectCache entries must be gone.
			for _, id := range ids {
				if cache.Has(id) {
					rt.Fatalf("Clear: objectCache still has %q", id)
				}
			}
		}
	})
}
