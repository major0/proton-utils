package drive

import (
	"bytes"
	"context"
	"encoding/gob"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

// Feature: object-cache-disk, Property 7: Hydration correctness
//
// For any set of ObjectCache entries (mix of link entries, block entries,
// corrupt entries, and incomplete file-type entries), hydrateFromCache SHALL
// populate the hydratedLinks staging map (NOT the Link_Table) with exactly
// the valid link entries — skipping keys containing .block., erasing entries
// that fail gob-decode, erasing file-type entries lacking FileProperties or
// empty ActiveRevision.ID, and staging all remaining entries. File-type entries
// with empty XAttr but valid revision SHALL be erased from the cache but still
// staged (for later XAttr-only completion via takeStagedIncomplete). cacheCount
// SHALL be initialized to the number of complete entries remaining on disk
// (staged-but-erased incomplete entries are not counted).
//
// **Validates: Requirements 4.1, 4.2, 4.3, 4.6, 4.7, 5.3**
func TestPropertyHydrationCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate distinct IDs for each entry type.
		completeFileID := rapid.StringMatching(`[a-zA-Z0-9]{5,15}`).Draw(rt, "completeFileID")
		folderID := rapid.StringMatching(`[a-zA-Z0-9]{5,15}`).Draw(rt, "folderID")
		incompleteFileID := rapid.StringMatching(`[a-zA-Z0-9]{5,15}`).Draw(rt, "incompleteFileID")
		corruptID := rapid.StringMatching(`[a-zA-Z0-9]{5,15}`).Draw(rt, "corruptID")
		invalidFileID := rapid.StringMatching(`[a-zA-Z0-9]{5,15}`).Draw(rt, "invalidFileID")
		blockKey := completeFileID + ".block.0"

		cacheDir := t.TempDir()
		oc := api.NewObjectCache(cacheDir)

		// 1. Complete file entry (valid XAttr).
		completeFile := proton.Link{
			LinkID: completeFileID,
			Type:   proton.LinkTypeFile,
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID:    "rev-complete",
					XAttr: "encrypted-xattr-data",
				},
			},
		}
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(completeFile); err != nil {
			rt.Fatalf("encode completeFile: %v", err)
		}
		_ = oc.Write(SanitizeLinkID(completeFileID), buf.Bytes())

		// 2. Folder entry.
		folder := proton.Link{
			LinkID: folderID,
			Type:   proton.LinkTypeFolder,
			FolderProperties: &proton.FolderProperties{
				NodeHashKey: "hash-key-value",
			},
		}
		buf.Reset()
		if err := gob.NewEncoder(&buf).Encode(folder); err != nil {
			rt.Fatalf("encode folder: %v", err)
		}
		_ = oc.Write(SanitizeLinkID(folderID), buf.Bytes())

		// 3. Block entry (key contains .block. — should be skipped entirely).
		_ = oc.Write(blockKey, []byte("block-data-bytes"))

		// 4. Corrupt entry (invalid gob — should be erased, not staged).
		_ = oc.Write(SanitizeLinkID(corruptID), []byte("not-valid-gob-data"))

		// 5. Incomplete file entry (valid revision, empty XAttr).
		// Should be erased from disk but still staged for XAttr completion.
		incompleteFile := proton.Link{
			LinkID: incompleteFileID,
			Type:   proton.LinkTypeFile,
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID:    "rev-incomplete",
					XAttr: "",
				},
			},
		}
		buf.Reset()
		if err := gob.NewEncoder(&buf).Encode(incompleteFile); err != nil {
			rt.Fatalf("encode incompleteFile: %v", err)
		}
		_ = oc.Write(SanitizeLinkID(incompleteFileID), buf.Bytes())

		// 6. Invalid file entry (nil FileProperties — should be erased, not staged).
		invalidFile := proton.Link{
			LinkID: invalidFileID,
			Type:   proton.LinkTypeFile,
		}
		buf.Reset()
		if err := gob.NewEncoder(&buf).Encode(invalidFile); err != nil {
			rt.Fatalf("encode invalidFile: %v", err)
		}
		_ = oc.Write(SanitizeLinkID(invalidFileID), buf.Bytes())

		// Create a Client with the pre-populated ObjectCache and call hydrateFromCache.
		client := &Client{
			objectCache:    oc,
			linkTable:      make(map[string]*Link),
			xattrFailCount: make(map[string]int),
			hydratedLinks:  make(map[string]proton.Link),
			fetcher: &mockFetcher{
				getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) {
					return proton.Link{}, nil
				},
				getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
					return proton.Revision{}, nil
				},
			},
		}
		client.hydrateFromCache()

		// --- Assertions ---

		// Verify staging map population.
		client.tableMu.RLock()
		_, hasComplete := client.hydratedLinks[completeFileID]
		_, hasFolder := client.hydratedLinks[folderID]
		_, hasIncomplete := client.hydratedLinks[incompleteFileID]
		_, hasCorrupt := client.hydratedLinks[corruptID]
		_, hasInvalid := client.hydratedLinks[invalidFileID]
		tableLen := len(client.linkTable)
		client.tableMu.RUnlock()

		if !hasComplete {
			rt.Fatal("complete file should be staged in hydratedLinks")
		}
		if !hasFolder {
			rt.Fatal("folder should be staged in hydratedLinks")
		}
		if !hasIncomplete {
			rt.Fatal("incomplete file should be staged in hydratedLinks (for XAttr completion)")
		}
		if hasCorrupt {
			rt.Fatal("corrupt entry should NOT be staged in hydratedLinks")
		}
		if hasInvalid {
			rt.Fatal("invalid file entry should NOT be staged in hydratedLinks")
		}

		// linkTable must be empty — hydration goes to staging map, not the Link_Table.
		if tableLen != 0 {
			rt.Fatalf("linkTable should be empty after hydration, has %d entries", tableLen)
		}

		// cacheCount = number of complete entries remaining on disk = 2
		// (complete file + folder). Incomplete file was erased from disk.
		if count := client.cacheCount.Load(); count != 2 {
			rt.Fatalf("expected cacheCount == 2, got %d", count)
		}

		// Block entry should still exist on disk (not erased, not counted).
		blockData, _ := oc.Read(blockKey)
		if blockData == nil {
			rt.Fatal("block entry should still exist on disk")
		}

		// Corrupt entry should be erased from disk.
		if d, _ := oc.Read(SanitizeLinkID(corruptID)); d != nil {
			rt.Fatal("corrupt entry should be erased from disk")
		}

		// Invalid file entry should be erased from disk.
		if d, _ := oc.Read(SanitizeLinkID(invalidFileID)); d != nil {
			rt.Fatal("invalid file entry should be erased from disk")
		}

		// Incomplete file entry should be erased from disk (but still staged).
		if d, _ := oc.Read(SanitizeLinkID(incompleteFileID)); d != nil {
			rt.Fatal("incomplete file entry should be erased from disk (staged for XAttr completion)")
		}

		// Complete file and folder entries should remain on disk.
		if d, _ := oc.Read(SanitizeLinkID(completeFileID)); d == nil {
			rt.Fatal("complete file entry should remain on disk")
		}
		if d, _ := oc.Read(SanitizeLinkID(folderID)); d == nil {
			rt.Fatal("folder entry should remain on disk")
		}
	})
}
