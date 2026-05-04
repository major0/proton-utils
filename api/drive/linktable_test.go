package drive

import (
	"context"
	"sync"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

// TestPropertyLinkTableIdentity verifies that the same LinkID always
// returns the same *Link pointer from the Link Table within a session.
// Two calls to NewChildLink with the same LinkID must return the same
// pointer (not just equal values).
//
// **Property 5: Link Table identity**
// **Validates: Requirement 3.2**
func TestPropertyLinkTableIdentity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := &Client{
			linkTable: make(map[string]*Link),
		}

		// Generate a set of unique LinkIDs.
		numLinks := rapid.IntRange(1, 20).Draw(rt, "numLinks")
		linkIDs := make([]string, 0, numLinks)
		seen := make(map[string]bool, numLinks)
		for len(linkIDs) < numLinks {
			id := rapid.StringMatching(`[a-zA-Z0-9_\-]{1,32}`).Draw(rt, "linkID")
			if seen[id] {
				continue
			}
			seen[id] = true
			linkIDs = append(linkIDs, id)
		}

		// Build a parent share and root link for constructing children.
		resolver := &mockResolver{}
		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
		}
		rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
		root := NewTestLink(rootPLink, nil, nil, resolver, "root")
		share := NewShare(pShare, nil, root, resolver, "vol-1")
		root = NewTestLink(rootPLink, nil, share, resolver, "root")
		share.Link = root

		ctx := context.Background()

		// First pass: insert all links via NewChildLink.
		firstPtrs := make(map[string]*Link, numLinks)
		for _, id := range linkIDs {
			pLink := &proton.Link{LinkID: id, Type: proton.LinkTypeFile}
			link := c.NewChildLink(ctx, root, pLink)
			firstPtrs[id] = link
		}

		// Second pass: request the same LinkIDs again. Must get the
		// exact same pointer back (pointer identity).
		for _, id := range linkIDs {
			pLink := &proton.Link{LinkID: id, Type: proton.LinkTypeFile}
			link := c.NewChildLink(ctx, root, pLink)
			if link != firstPtrs[id] {
				rt.Fatalf("LinkID %q: second NewChildLink returned different pointer", id)
			}
		}
	})
}

// TestPropertyParentPointers verifies that after inserting a Link via
// NewChildLink, its ParentLink() references the correct parent *Link
// in the table.
//
// **Property 11: Parent pointer correctness**
// **Validates: Requirement 3.5**
func TestPropertyParentPointers(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := &Client{
			linkTable: make(map[string]*Link),
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

		ctx := context.Background()

		// Build a tree: root → parent → child.
		// Generate a random depth (1–5 levels below root).
		depth := rapid.IntRange(1, 5).Draw(rt, "depth")
		current := root
		usedIDs := make(map[string]bool)
		for i := 0; i < depth; i++ {
			var id string
			for {
				id = rapid.StringMatching(`[a-zA-Z0-9_]{2,16}`).Draw(rt, "linkID")
				if !usedIDs[id] && id != "root" {
					break
				}
			}
			usedIDs[id] = true
			pLink := &proton.Link{LinkID: id, Type: proton.LinkTypeFolder}
			child := c.NewChildLink(ctx, current, pLink)

			// The child's ParentLink must be the current node.
			if child.ParentLink() != current {
				rt.Fatalf("depth %d, LinkID %q: ParentLink() != expected parent", i, id)
			}

			current = child
		}

		// Generate siblings under root — each should have root as parent.
		numSiblings := rapid.IntRange(1, 10).Draw(rt, "numSiblings")
		siblingIDs := make(map[string]bool, numSiblings)
		for len(siblingIDs) < numSiblings {
			id := rapid.StringMatching(`sib-[a-zA-Z0-9]{1,12}`).Draw(rt, "siblingID")
			if siblingIDs[id] {
				continue
			}
			siblingIDs[id] = true

			pLink := &proton.Link{LinkID: id, Type: proton.LinkTypeFile}
			child := c.NewChildLink(ctx, root, pLink)
			if child.ParentLink() != root {
				rt.Fatalf("sibling %q: ParentLink() != root", id)
			}
		}
	})
}

// TestPropertyLinkTableConcurrency verifies that concurrent getLink,
// putLink, deleteLink, and clearLinks operations never produce data
// races or corrupt the map. Run with -race flag.
//
// **Property 12: Link Table concurrency safety**
// **Validates: Requirement 3.4**
func TestPropertyLinkTableConcurrency(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		c := &Client{
			linkTable: make(map[string]*Link),
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

		// Pre-populate some links.
		numInitial := rapid.IntRange(5, 20).Draw(rt, "numInitial")
		ids := make([]string, 0, numInitial)
		idSet := make(map[string]bool, numInitial)
		for len(ids) < numInitial {
			id := rapid.StringMatching(`[a-zA-Z0-9]{1,16}`).Draw(rt, "id")
			if idSet[id] || id == "root" {
				continue
			}
			idSet[id] = true
			ids = append(ids, id)
			pLink := &proton.Link{LinkID: id, Type: proton.LinkTypeFile}
			link := NewLink(pLink, root, share, resolver)
			c.putLink(id, link)
		}

		// Run concurrent operations.
		numWorkers := rapid.IntRange(4, 16).Draw(rt, "numWorkers")
		var wg sync.WaitGroup

		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					idx := (workerID*50 + j) % len(ids)
					id := ids[idx]

					switch j % 5 {
					case 0: // read
						_ = c.getLink(id)
					case 1: // write
						pLink := &proton.Link{LinkID: id, Type: proton.LinkTypeFile}
						link := NewLink(pLink, root, share, resolver)
						c.putLink(id, link)
					case 2: // delete
						c.deleteLink(id)
					case 3: // read again
						_ = c.getLink(id)
					case 4: // clear
						c.clearLinks()
					}
				}
			}(i)
		}

		wg.Wait()

		// After all operations, the table must be in a consistent state.
		// We can't predict exact contents, but getLink must not panic.
		for _, id := range ids {
			_ = c.getLink(id)
		}
	})
}
