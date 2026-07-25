package drive

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

// genRevisionMetadata generates an arbitrary proton.RevisionMetadata.
func genRevisionMetadata(t *rapid.T) proton.RevisionMetadata {
	return proton.RevisionMetadata{
		ID:                rapid.String().Draw(t, "rev_id"),
		CreateTime:        rapid.Int64().Draw(t, "rev_create_time"),
		Size:              rapid.Int64().Draw(t, "rev_size"),
		ManifestSignature: rapid.String().Draw(t, "rev_manifest_sig"),
		SignatureEmail:    rapid.String().Draw(t, "rev_sig_email"),
		State:             proton.RevisionState(rapid.IntRange(0, 3).Draw(t, "rev_state")),
		XAttr:             rapid.String().Draw(t, "rev_xattr"),
		Thumbnail:         proton.Bool(rapid.Bool().Draw(t, "rev_thumbnail")),
		ThumbnailHash:     rapid.String().Draw(t, "rev_thumbnail_hash"),
	}
}

// genFileProperties generates arbitrary proton.FileProperties.
func genFileProperties(t *rapid.T) *proton.FileProperties {
	return &proton.FileProperties{
		ContentKeyPacket:          rapid.String().Draw(t, "content_key_packet"),
		ContentKeyPacketSignature: rapid.String().Draw(t, "content_key_packet_sig"),
		ActiveRevision:            genRevisionMetadata(t),
	}
}

// genFolderProperties generates arbitrary proton.FolderProperties.
func genFolderProperties(t *rapid.T) *proton.FolderProperties {
	return &proton.FolderProperties{
		NodeHashKey: rapid.String().Draw(t, "node_hash_key"),
	}
}

// genLink generates an arbitrary proton.Link with consistent Type/Properties:
// - Type==LinkTypeFile → FileProperties non-nil, FolderProperties nil
// - Type==LinkTypeFolder → FolderProperties non-nil, FileProperties nil
func genLink(t *rapid.T) proton.Link {
	linkType := proton.LinkType(rapid.IntRange(1, 2).Draw(t, "link_type"))

	link := proton.Link{
		LinkID:                  rapid.String().Draw(t, "link_id"),
		ParentLinkID:            rapid.String().Draw(t, "parent_link_id"),
		Type:                    linkType,
		Name:                    rapid.String().Draw(t, "name"),
		NameSignatureEmail:      rapid.String().Draw(t, "name_sig_email"),
		Hash:                    rapid.String().Draw(t, "hash"),
		Size:                    rapid.Int64().Draw(t, "size"),
		State:                   proton.LinkState(rapid.IntRange(0, 4).Draw(t, "state")),
		MIMEType:                rapid.String().Draw(t, "mime_type"),
		CreateTime:              rapid.Int64().Draw(t, "create_time"),
		ModifyTime:              rapid.Int64().Draw(t, "modify_time"),
		ExpirationTime:          rapid.Int64().Draw(t, "expiration_time"),
		XAttr:                   rapid.String().Draw(t, "xattr"),
		NodeKey:                 rapid.String().Draw(t, "node_key"),
		NodePassphrase:          rapid.String().Draw(t, "node_passphrase"),
		NodePassphraseSignature: rapid.String().Draw(t, "node_passphrase_sig"),
		SignatureEmail:          rapid.String().Draw(t, "sig_email"),
	}

	switch linkType {
	case proton.LinkTypeFile:
		link.FileProperties = genFileProperties(t)
	case proton.LinkTypeFolder:
		link.FolderProperties = genFolderProperties(t)
	}

	return link
}

// Feature: object-cache-disk, Property 1: Gob serialization round-trip
//
// For any valid proton.Link struct (with arbitrary field values including
// populated ActiveRevision.XAttr), gob-encoding then gob-decoding SHALL
// produce a struct that is field-identical to the original (reflect.DeepEqual),
// confirming no transformation or decryption occurs during storage.
//
// **Validates: Requirements 7.1, 7.2, 7.3, 7.5**
func TestPropertyGobRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		pLink := genLink(t)

		var buf bytes.Buffer
		err := gob.NewEncoder(&buf).Encode(pLink)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}

		var decoded proton.Link
		err = gob.NewDecoder(&buf).Decode(&decoded)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}

		if !reflect.DeepEqual(pLink, decoded) {
			t.Fatalf("round-trip mismatch:\n  original: %+v\n  decoded:  %+v", pLink, decoded)
		}
	})
}

// mockFetcher implements linkFetcher for property tests that exercise the
// fetch path without network access. Each function field can be overridden
// per test; nil fields panic if called unexpectedly.
type mockFetcher struct {
	getLinkFn     func(ctx context.Context, shareID, linkID string) (proton.Link, error)
	getRevisionFn func(ctx context.Context, shareID, linkID, revisionID string) (proton.Revision, error)
}

func (m *mockFetcher) GetLink(ctx context.Context, shareID, linkID string) (proton.Link, error) {
	return m.getLinkFn(ctx, shareID, linkID)
}

func (m *mockFetcher) GetRevisionAllBlocks(ctx context.Context, shareID, linkID, revisionID string) (proton.Revision, error) {
	return m.getRevisionFn(ctx, shareID, linkID, revisionID)
}

// newPropTestClient constructs a minimal Client with a mockFetcher and an
// isolated ObjectCache for property tests. The returned client has disk
// caching enabled for the "test-share" shareID.
func newPropTestClient(t testing.TB, mock *mockFetcher) *Client {
	t.Helper()
	cacheDir := t.(*testing.T).TempDir()
	oc := api.NewObjectCache(cacheDir)

	return &Client{
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
}

// Feature: object-cache-disk, Property 4: Link fetch failure writes nothing
//
// For any LinkID where the Link fetch fails, neither the ObjectCache nor the
// Link_Table SHALL contain a new entry for that LinkID — both stores remain
// unchanged from their pre-call state.
//
// **Validates: Requirements 1.4**
func TestPropertyLinkFailureWritesNothing(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		linkID := rapid.StringMatching(`[a-zA-Z0-9]{5,20}`).Draw(rt, "linkID")
		shareID := "test-share"

		mock := &mockFetcher{
			getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) {
				return proton.Link{}, fmt.Errorf("API error")
			},
			getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
				rt.Fatal("GetRevisionAllBlocks should not be called")
				return proton.Revision{}, nil
			},
		}

		client := newPropTestClient(t, mock)
		_, err := client.GetCachedLink(context.Background(), shareID, linkID)

		// Error must be returned.
		if err == nil {
			rt.Fatal("expected error from GetCachedLink")
		}

		// ObjectCache should be empty for that linkID.
		data, _ := client.objectCache.Read(SanitizeLinkID(linkID))
		if data != nil {
			rt.Fatalf("ObjectCache should not have entry for %s", linkID)
		}

		// linkTable should have no entry for that linkID.
		if link := client.GetLink(linkID); link != nil {
			rt.Fatalf("linkTable should not have entry for %s", linkID)
		}
	})
}

// Feature: object-cache-disk, Property 3: XAttr failure prevents cache write
//
// For any file-type link where the Link fetch succeeds but the XAttr fetch
// fails, fetchLinkWithXAttr SHALL return the proton.Link with an empty
// ActiveRevision.XAttr AND the ObjectCache SHALL NOT contain an entry for
// that LinkID. The xattrFailCount for that LinkID SHALL be incremented.
//
// **Validates: Requirements 1.3**
func TestPropertyXAttrFailureSkipsCache(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a file-type link with a valid active revision.
		linkID := rapid.StringMatching(`[a-zA-Z0-9]{5,30}`).Draw(rt, "linkID")
		shareID := "test-share"
		revID := rapid.StringMatching(`[a-zA-Z0-9]{10,20}`).Draw(rt, "revID")

		pLink := proton.Link{
			LinkID: linkID,
			Type:   proton.LinkTypeFile,
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID: revID,
				},
			},
		}

		mock := &mockFetcher{
			getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) {
				return pLink, nil
			},
			getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
				return proton.Revision{}, fmt.Errorf("network error")
			},
		}

		client := newPropTestClient(t, mock)

		// Call fetchLinkWithXAttr directly — this is the code under test.
		// GetCachedLink delegates to this after singleflight; testing the
		// fetch function directly isolates the property.
		result, err := client.fetchLinkWithXAttr(context.Background(), shareID, linkID, true, nil)

		// No error — XAttr failure is graceful (Req 1.3).
		if err != nil {
			rt.Fatalf("unexpected error: %v", err)
		}

		// Link returned with empty XAttr.
		if result.FileProperties == nil {
			rt.Fatal("expected FileProperties, got nil")
		}
		if result.FileProperties.ActiveRevision.XAttr != "" {
			rt.Fatalf("expected empty XAttr, got %q", result.FileProperties.ActiveRevision.XAttr)
		}

		// ObjectCache should NOT have an entry for this linkID.
		data, _ := client.objectCache.Read(SanitizeLinkID(linkID))
		if data != nil {
			rt.Fatalf("ObjectCache should not have entry for %s, but found %d bytes", linkID, len(data))
		}

		// xattrFailCount[linkID] must be incremented to 1.
		client.tableMu.RLock()
		count := client.xattrFailCount[linkID]
		client.tableMu.RUnlock()
		if count != 1 {
			rt.Fatalf("expected xattrFailCount[%s] == 1, got %d", linkID, count)
		}
	})
}

// Feature: object-cache-disk, Property 5: Folder links cached without XAttr fetch
//
// For any folder-type proton.Link, when GetCachedLink encounters a cache miss,
// the ObjectCache entry SHALL be written after the Link fetch completes without
// dispatching a separate XAttr fetch. The cached entry SHALL gob-decode to the
// same proton.Link returned by the API.
//
// **Validates: Requirements 1.6**
func TestPropertyFolderCachedImmediately(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		linkID := rapid.StringMatching(`[a-zA-Z0-9]{5,20}`).Draw(rt, "linkID")
		shareID := "test-share"

		pLink := proton.Link{
			LinkID: linkID,
			Type:   proton.LinkTypeFolder,
			FolderProperties: &proton.FolderProperties{
				NodeHashKey: rapid.String().Draw(rt, "nodeHashKey"),
			},
		}

		mock := &mockFetcher{
			getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) {
				return pLink, nil
			},
			getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
				rt.Fatal("GetRevisionAllBlocks should NOT be called for folder links")
				return proton.Revision{}, nil
			},
		}

		client := newPropTestClient(t, mock)
		result, err := client.GetCachedLink(context.Background(), shareID, linkID)
		if err != nil {
			rt.Fatalf("unexpected error: %v", err)
		}

		// Returned link should match the API response.
		if result.LinkID != linkID || result.Type != proton.LinkTypeFolder {
			rt.Fatalf("unexpected result: %+v", result)
		}

		// ObjectCache should contain the entry.
		data, _ := client.objectCache.Read(SanitizeLinkID(linkID))
		if data == nil {
			rt.Fatalf("ObjectCache should have entry for folder %s", linkID)
		}

		// Decode and verify it matches the original.
		var cached proton.Link
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&cached); err != nil {
			rt.Fatalf("decode cached entry: %v", err)
		}
		if !reflect.DeepEqual(pLink, cached) {
			rt.Fatalf("cached entry mismatch:\n  want: %+v\n  got:  %+v", pLink, cached)
		}
	})
}

// Feature: object-cache-disk, Property 2: Cache miss produces complete entry
//
// For any file-type proton.Link with a valid active revision, when
// GetCachedLink encounters a cache miss and the sequential Link fetch then
// XAttr fetch both succeed, the entry written to the ObjectCache SHALL
// gob-decode to a proton.Link whose ActiveRevision.XAttr field is non-empty.
// The XAttr fetch SHALL be issued with the ActiveRevision.ID obtained from
// the just-fetched Link (confirming the sequential dependency).
//
// **Validates: Requirements 1.1, 1.2**
func TestPropertyCacheMissWritesComplete(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		linkID := rapid.StringMatching(`[a-zA-Z0-9]{5,20}`).Draw(rt, "linkID")
		shareID := "test-share"
		revID := rapid.StringMatching(`[a-zA-Z0-9]{10,20}`).Draw(rt, "revID")
		xattrValue := rapid.StringMatching(`[a-zA-Z0-9]{10,50}`).Draw(rt, "xattr")
		revSize := rapid.Int64Min(1).Draw(rt, "revSize")

		pLink := proton.Link{
			LinkID: linkID,
			Type:   proton.LinkTypeFile,
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID: revID,
				},
			},
		}

		var capturedRevID string
		mock := &mockFetcher{
			getLinkFn: func(_ context.Context, sid, lid string) (proton.Link, error) {
				if sid != shareID || lid != linkID {
					rt.Fatalf("GetLink called with wrong args: %s/%s", sid, lid)
				}
				return pLink, nil
			},
			getRevisionFn: func(_ context.Context, _, _, rid string) (proton.Revision, error) {
				capturedRevID = rid
				return proton.Revision{
					RevisionMetadata: proton.RevisionMetadata{
						ID:    rid,
						XAttr: xattrValue,
						Size:  revSize,
					},
				}, nil
			},
		}

		client := newPropTestClient(t, mock)
		result, err := client.GetCachedLink(context.Background(), shareID, linkID)
		if err != nil {
			rt.Fatalf("unexpected error: %v", err)
		}

		// Verify sequential dependency: XAttr fetch used the Link's revision ID.
		if capturedRevID != revID {
			rt.Fatalf("expected revID %q, got %q", revID, capturedRevID)
		}

		// Returned link has populated XAttr.
		if result.FileProperties == nil || result.FileProperties.ActiveRevision.XAttr != xattrValue {
			rt.Fatalf("expected XAttr %q, got %q",
				xattrValue, result.FileProperties.ActiveRevision.XAttr)
		}

		// ObjectCache contains a complete entry.
		data, _ := client.objectCache.Read(SanitizeLinkID(linkID))
		if data == nil {
			rt.Fatalf("ObjectCache should have entry for %s", linkID)
		}
		var cached proton.Link
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&cached); err != nil {
			rt.Fatalf("decode cached entry: %v", err)
		}
		if cached.FileProperties == nil || cached.FileProperties.ActiveRevision.XAttr == "" {
			rt.Fatal("cached entry has empty XAttr")
		}
		if cached.FileProperties.ActiveRevision.XAttr != xattrValue {
			rt.Fatalf("cached XAttr mismatch: want %q, got %q",
				xattrValue, cached.FileProperties.ActiveRevision.XAttr)
		}
	})
}

// Feature: object-cache-disk, Property 6: Cache hit completeness validation
//
// For any file-type link entry in the ObjectCache, GetCachedLink SHALL return
// it as a cache hit only if ActiveRevision.XAttr is a non-empty string. For
// folder-type entries, GetCachedLink SHALL return the entry as a hit regardless
// of XAttr state. Entries that fail validation (empty XAttr for file-type,
// decode failure) SHALL be erased from the ObjectCache.
//
// **Validates: Requirements 2.1, 2.2, 2.3, 2.4**
func TestPropertyCacheHitCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		linkID := rapid.StringMatching(`[a-zA-Z0-9]{5,20}`).Draw(rt, "linkID")
		shareID := "test-share"

		// Choose: complete file, incomplete file, folder, or corrupt entry.
		variant := rapid.IntRange(0, 3).Draw(rt, "variant")

		client := newPropTestClient(t, &mockFetcher{
			getLinkFn: func(_ context.Context, _, lid string) (proton.Link, error) {
				// Fallback for miss path — return a minimal link.
				return proton.Link{LinkID: lid, Type: proton.LinkTypeFile}, nil
			},
			getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
				return proton.Revision{}, fmt.Errorf("not needed")
			},
		})

		// Pre-populate the ObjectCache based on variant.
		switch variant {
		case 0: // Complete file (hit expected)
			pLink := proton.Link{
				LinkID: linkID,
				Type:   proton.LinkTypeFile,
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{
						ID:    "rev-1",
						XAttr: "encrypted-xattr-data",
					},
				},
			}
			var buf bytes.Buffer
			_ = gob.NewEncoder(&buf).Encode(pLink)
			_ = client.objectCache.Write(SanitizeLinkID(linkID), buf.Bytes())
			client.cacheCount.Add(1)

			result, err := client.GetCachedLink(context.Background(), shareID, linkID)
			if err != nil {
				rt.Fatalf("variant 0: unexpected error: %v", err)
			}
			if result.FileProperties == nil || result.FileProperties.ActiveRevision.XAttr != "encrypted-xattr-data" {
				rt.Fatal("variant 0: expected hit with XAttr populated")
			}

		case 1: // Incomplete file (miss expected, entry erased)
			pLink := proton.Link{
				LinkID: linkID,
				Type:   proton.LinkTypeFile,
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{
						ID:    "rev-1",
						XAttr: "", // empty = incomplete
					},
				},
			}
			var buf bytes.Buffer
			_ = gob.NewEncoder(&buf).Encode(pLink)
			_ = client.objectCache.Write(SanitizeLinkID(linkID), buf.Bytes())
			client.cacheCount.Add(1)

			// tryObjectCacheHit should erase it and return miss.
			_, hit := client.tryObjectCacheHit(linkID)
			if hit {
				rt.Fatal("variant 1: incomplete file entry should NOT be a hit")
			}
			// Entry should be erased.
			data, _ := client.objectCache.Read(SanitizeLinkID(linkID))
			if data != nil {
				rt.Fatal("variant 1: incomplete entry should be erased")
			}

		case 2: // Folder (hit expected regardless of XAttr)
			pLink := proton.Link{
				LinkID: linkID,
				Type:   proton.LinkTypeFolder,
				FolderProperties: &proton.FolderProperties{
					NodeHashKey: "hash-key",
				},
			}
			var buf bytes.Buffer
			_ = gob.NewEncoder(&buf).Encode(pLink)
			_ = client.objectCache.Write(SanitizeLinkID(linkID), buf.Bytes())
			client.cacheCount.Add(1)

			result, err := client.GetCachedLink(context.Background(), shareID, linkID)
			if err != nil {
				rt.Fatalf("variant 2: unexpected error: %v", err)
			}
			if result.Type != proton.LinkTypeFolder {
				rt.Fatal("variant 2: expected folder hit")
			}

		case 3: // Corrupt entry (miss, entry erased)
			_ = client.objectCache.Write(SanitizeLinkID(linkID), []byte("not-valid-gob"))
			client.cacheCount.Add(1)

			_, hit := client.tryObjectCacheHit(linkID)
			if hit {
				rt.Fatal("variant 3: corrupt entry should NOT be a hit")
			}
			data, _ := client.objectCache.Read(SanitizeLinkID(linkID))
			if data != nil {
				rt.Fatal("variant 3: corrupt entry should be erased")
			}
		}
	})
}

// Feature: object-cache-disk, Property 8: NewChildLink does not write file-type links
//
// For any file-type proton.Link passed to NewChildLink, the ObjectCache SHALL
// NOT gain a new entry for that LinkID. The Link SHALL be inserted into the
// Link_Table only.
//
// **Validates: Requirements 3.5**
func TestPropertyNewChildLinkNoFileWrite(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		linkID := rapid.StringMatching(`[a-zA-Z0-9]{5,20}`).Draw(rt, "linkID")

		mock := &mockFetcher{
			getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) {
				return proton.Link{}, nil
			},
			getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
				return proton.Revision{}, nil
			},
		}
		client := newPropTestClient(t, mock)

		// Create a parent folder link with a share that has disk caching enabled.
		parentProtonLink := &proton.Link{
			LinkID: "parent-folder",
			Type:   proton.LinkTypeFolder,
		}
		share := &Share{DiskCacheLevel: api.DiskCacheObjectStore}
		parent := NewLink(parentProtonLink, nil, share, client)

		// Create a file-type child link.
		childPLink := &proton.Link{
			LinkID: linkID,
			Type:   proton.LinkTypeFile,
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID:    "rev-1",
					XAttr: "some-xattr",
				},
			},
		}

		// Call NewChildLink — should NOT write to ObjectCache for file-type.
		client.NewChildLink(context.Background(), parent, childPLink)

		// Verify ObjectCache does NOT have an entry for the file link.
		data, _ := client.objectCache.Read(SanitizeLinkID(linkID))
		if data != nil {
			rt.Fatalf("ObjectCache should NOT have entry for file-type link %s", linkID)
		}

		// Verify the link IS in the link table.
		if link := client.GetLink(linkID); link == nil {
			rt.Fatalf("linkTable should have entry for %s", linkID)
		}
	})
}

// Feature: object-cache-disk, Property 9: Write-once invariant
//
// For any sequence of ObjectCache operations on a given LinkID, an entry is
// either absent or identical to the value that was originally written — no
// partial update or in-place modification ever occurs. The only transitions
// are: absent → written (complete) and written → erased.
//
// **Validates: Requirements 3.4**
func TestPropertyWriteOnceInvariant(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		linkID := rapid.StringMatching(`[a-zA-Z0-9]{5,20}`).Draw(rt, "linkID")
		shareID := "test-share"
		revID := rapid.StringMatching(`[a-zA-Z0-9]{10,20}`).Draw(rt, "revID")
		xattrValue := rapid.StringMatching(`[a-zA-Z0-9]{10,50}`).Draw(rt, "xattr")

		pLink := proton.Link{
			LinkID: linkID,
			Type:   proton.LinkTypeFile,
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{ID: revID},
			},
		}

		mock := &mockFetcher{
			getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) {
				return pLink, nil
			},
			getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
				return proton.Revision{
					RevisionMetadata: proton.RevisionMetadata{XAttr: xattrValue, Size: 100},
				}, nil
			},
		}

		client := newPropTestClient(t, mock)

		// First call: cache miss → sequential fetch → write complete entry.
		_, err := client.GetCachedLink(context.Background(), shareID, linkID)
		if err != nil {
			rt.Fatalf("first call: %v", err)
		}

		// Capture the raw bytes written to disk after the first call.
		data1, _ := client.objectCache.Read(SanitizeLinkID(linkID))
		if data1 == nil {
			rt.Fatal("expected entry in ObjectCache after first call")
		}

		// Second call: cache hit — should NOT modify the on-disk entry.
		_, err = client.GetCachedLink(context.Background(), shareID, linkID)
		if err != nil {
			rt.Fatalf("second call: %v", err)
		}

		// Verify the raw bytes on disk are byte-for-byte identical (no in-place update).
		data2, _ := client.objectCache.Read(SanitizeLinkID(linkID))
		if !bytes.Equal(data1, data2) {
			rt.Fatal("write-once violated: entry was modified in-place on second access")
		}

		// Third call after erase: verify the transition absent → written produces
		// a new entry identical to the original (same API data, same encoding).
		_ = client.objectCache.Erase(SanitizeLinkID(linkID))
		client.cacheCount.Add(-1)

		// Confirm entry is absent.
		dataErased, _ := client.objectCache.Read(SanitizeLinkID(linkID))
		if dataErased != nil {
			rt.Fatal("expected entry absent after erase")
		}

		// Re-fetch: absent → written (complete).
		_, err = client.GetCachedLink(context.Background(), shareID, linkID)
		if err != nil {
			rt.Fatalf("third call: %v", err)
		}

		data3, _ := client.objectCache.Read(SanitizeLinkID(linkID))
		if data3 == nil {
			rt.Fatal("expected entry re-written after erase + fetch")
		}

		// The re-written entry must be identical to the original (same API response,
		// deterministic gob encoding → same bytes).
		if !bytes.Equal(data1, data3) {
			rt.Fatal("write-once violated: re-written entry differs from original")
		}
	})
}

// Feature: object-cache-disk, Property 10: Eviction correctness
//
// Post-eviction: link count ≤ evictionTarget, evicted entries are
// oldest-mtime, Link_Table unchanged.
//
// This test creates entries exceeding evictionTarget on disk, assigns
// controlled mtimes, triggers eviction via runEviction, and verifies:
// (a) link count ≤ evictionTarget after eviction
// (b) evicted entries are those with the oldest mtimes
// (c) Link_Table is unchanged (disk eviction is space management only)
//
// Note: Because evictionTarget is 9000 and runEviction checks
// len(entries) > evictionTarget, this test creates 9000 + N entries
// (where N is small) so the eviction pass actually fires. This makes
// the test too slow for rapid property iteration (100×9050 files), so
// it runs as a standard Go test validating the property once.
//
// **Validates: Requirements 5.2, 5.4, 5.6**
func TestPropertyEvictionCorrectness(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping eviction test in short mode (creates >9000 files)")
	}

	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, _ string) (proton.Link, error) {
			return proton.Link{}, nil
		},
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{}, nil
		},
	}
	client := newPropTestClient(t, mock)

	// Write entries exceeding evictionTarget so runEviction actually evicts.
	const excessCount = 50
	totalEntries := evictionTarget + excessCount // 9050

	// Base time: entries 0..excessCount-1 get the oldest mtimes,
	// entries excessCount..totalEntries-1 get newer mtimes.
	baseTime := time.Now().Add(-2 * time.Hour)

	for i := 0; i < totalEntries; i++ {
		linkID := fmt.Sprintf("evict-link-%05d", i)
		pLink := proton.Link{LinkID: linkID, Type: proton.LinkTypeFolder}

		// Write directly (bypasses triggerEviction threshold check since
		// we're building up the cache manually).
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(pLink); err != nil {
			t.Fatalf("encode entry %d: %v", i, err)
		}
		if err := client.objectCache.Write(SanitizeLinkID(linkID), buf.Bytes()); err != nil {
			t.Fatalf("write entry %d: %v", i, err)
		}

		// Set mtime: entries 0..49 are oldest (eviction candidates),
		// entries 50..9049 are progressively newer.
		path := client.objectCache.PathFor(SanitizeLinkID(linkID))
		if path == "" {
			t.Fatalf("PathFor returned empty for entry %d", i)
		}
		mtime := baseTime.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("Chtimes entry %d: %v", i, err)
		}
	}

	// Set cacheCount to match the actual number of entries.
	client.cacheCount.Store(int64(totalEntries))

	// Insert entries into Link_Table to verify they survive eviction.
	for i := 0; i < totalEntries; i++ {
		linkID := fmt.Sprintf("evict-link-%05d", i)
		client.putLink(linkID, &Link{})
	}

	// Trigger eviction directly via runEviction (sets evicting=false on exit).
	client.evicting.Store(true)
	client.runEviction()

	// (a) Post-eviction count must be ≤ evictionTarget.
	count := client.cacheCount.Load()
	if count > int64(evictionTarget) {
		t.Fatalf("post-eviction cacheCount %d exceeds evictionTarget %d", count, evictionTarget)
	}

	// (b) The oldest entries (indices 0..excessCount-1) must be evicted.
	for i := 0; i < excessCount; i++ {
		linkID := fmt.Sprintf("evict-link-%05d", i)
		data, _ := client.objectCache.Read(SanitizeLinkID(linkID))
		if data != nil {
			t.Fatalf("oldest entry %s (index %d) should be evicted but is still on disk", linkID, i)
		}
	}

	// Newer entries (indices excessCount..totalEntries-1) must remain.
	for i := excessCount; i < totalEntries; i++ {
		linkID := fmt.Sprintf("evict-link-%05d", i)
		data, _ := client.objectCache.Read(SanitizeLinkID(linkID))
		if data == nil {
			t.Fatalf("newer entry %s (index %d) should remain but was evicted", linkID, i)
		}
	}

	// (c) Link_Table must be unchanged — disk eviction does not remove in-memory entries.
	for i := 0; i < totalEntries; i++ {
		linkID := fmt.Sprintf("evict-link-%05d", i)
		if client.GetLink(linkID) == nil {
			t.Fatalf("Link_Table entry %s was removed during eviction (should be unchanged)", linkID)
		}
	}

	// evicting flag must be cleared.
	if client.evicting.Load() {
		t.Fatal("evicting flag should be false after runEviction completes")
	}
}

// Feature: object-cache-disk, Property 12: Singleflight error propagation
//
// For any LinkID where the singleflight fetch fails, all goroutines waiting
// on that LinkID SHALL receive the same error, and the singleflight state for
// that key SHALL be reset — a subsequent call SHALL be permitted to retry.
//
// **Validates: Requirements 8.4**
func TestPropertySingleflightErrorPropagation(t *testing.T) {
	linkID := "sf-error-link"
	shareID := "test-share"

	var callCount atomic.Int64
	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, lid string) (proton.Link, error) {
			n := callCount.Add(1)
			time.Sleep(30 * time.Millisecond)
			if n == 1 {
				return proton.Link{}, fmt.Errorf("first fetch fails")
			}
			// Second attempt succeeds.
			return proton.Link{
				LinkID: lid, Type: proton.LinkTypeFile,
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{ID: "rev-retry"},
				},
			}, nil
		},
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{
				RevisionMetadata: proton.RevisionMetadata{XAttr: "retry-xattr", Size: 50},
			}, nil
		},
	}

	client := newPropTestClient(t, mock)

	// Phase 1: Concurrent calls that all fail.
	const numCallers = 10
	var wg sync.WaitGroup
	for i := 0; i < numCallers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.GetCachedLink(context.Background(), shareID, linkID)
			if err == nil {
				t.Error("expected error from first batch")
			}
		}()
	}
	wg.Wait()

	// Phase 2: A subsequent call should be permitted to retry (singleflight state reset).
	result, err := client.GetCachedLink(context.Background(), shareID, linkID)
	if err != nil {
		t.Fatalf("retry should succeed: %v", err)
	}
	if result.FileProperties == nil || result.FileProperties.ActiveRevision.XAttr != "retry-xattr" {
		t.Fatalf("unexpected retry result: %+v", result)
	}

	// Total GetLink calls: 1 (first singleflight, failed) + 1 (retry) = 2.
	if calls := callCount.Load(); calls != 2 {
		t.Fatalf("expected 2 GetLink calls total, got %d", calls)
	}
}

// Feature: object-cache-disk, Property 11: Singleflight deduplication
//
// For any LinkID with N concurrent GetCachedLink calls that all encounter a
// cache miss, exactly one API fetch SHALL be dispatched. All N callers SHALL
// receive the same proton.Link result (or the same error).
//
// **Validates: Requirements 8.3**
func TestPropertySingleflightDedup(t *testing.T) {
	linkID := "singleflight-link"
	shareID := "test-share"
	revID := "rev-sf"
	xattrValue := "sf-xattr"

	var getLinkCalls atomic.Int64
	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, lid string) (proton.Link, error) {
			getLinkCalls.Add(1)
			time.Sleep(50 * time.Millisecond) // Slow to ensure concurrent callers pile up
			return proton.Link{
				LinkID: lid, Type: proton.LinkTypeFile,
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{ID: revID},
				},
			}, nil
		},
		getRevisionFn: func(_ context.Context, _, _, _ string) (proton.Revision, error) {
			return proton.Revision{
				RevisionMetadata: proton.RevisionMetadata{XAttr: xattrValue, Size: 100},
			}, nil
		},
	}

	client := newPropTestClient(t, mock)

	const numCallers = 20
	var wg sync.WaitGroup
	results := make([]proton.Link, numCallers)
	errors := make([]error, numCallers)

	for i := 0; i < numCallers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errors[idx] = client.GetCachedLink(context.Background(), shareID, linkID)
		}(i)
	}
	wg.Wait()

	// All callers should succeed with the same result.
	for i := 0; i < numCallers; i++ {
		if errors[i] != nil {
			t.Fatalf("caller %d: %v", i, errors[i])
		}
		if results[i].FileProperties == nil || results[i].FileProperties.ActiveRevision.XAttr != xattrValue {
			t.Fatalf("caller %d: unexpected result", i)
		}
	}

	// Exactly 1 GetLink call should have been made.
	if calls := getLinkCalls.Load(); calls != 1 {
		t.Fatalf("expected 1 GetLink call (singleflight), got %d", calls)
	}
}

// Feature: object-cache-disk, Property 13: Concurrent fetch integrity
//
// Concurrent fetches for distinct LinkIDs produce entries that each decode to
// the correct Link (no cross-contamination between concurrent writes).
//
// **Validates: Requirements 8.1**
func TestPropertyConcurrentIntegrity(t *testing.T) {
	const numLinks = 50
	shareID := "test-share"

	mock := &mockFetcher{
		getLinkFn: func(_ context.Context, _, lid string) (proton.Link, error) {
			return proton.Link{
				LinkID: lid,
				Type:   proton.LinkTypeFile,
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{ID: "rev-" + lid},
				},
			}, nil
		},
		getRevisionFn: func(_ context.Context, _, lid, _ string) (proton.Revision, error) {
			return proton.Revision{
				RevisionMetadata: proton.RevisionMetadata{
					XAttr: "xattr-" + lid,
					Size:  int64(len(lid)),
				},
			}, nil
		},
	}

	client := newPropTestClient(t, mock)

	var wg sync.WaitGroup
	results := make([]proton.Link, numLinks)
	errors := make([]error, numLinks)

	for i := 0; i < numLinks; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			linkID := fmt.Sprintf("concurrent-%03d", idx)
			results[idx], errors[idx] = client.GetCachedLink(context.Background(), shareID, linkID)
		}(i)
	}
	wg.Wait()

	// Each result should match its expected LinkID and XAttr.
	for i := 0; i < numLinks; i++ {
		linkID := fmt.Sprintf("concurrent-%03d", i)
		if errors[i] != nil {
			t.Fatalf("link %s: %v", linkID, errors[i])
		}
		if results[i].LinkID != linkID {
			t.Fatalf("link %s: got LinkID %s (cross-contamination)", linkID, results[i].LinkID)
		}
		expectedXAttr := "xattr-" + linkID
		if results[i].FileProperties == nil || results[i].FileProperties.ActiveRevision.XAttr != expectedXAttr {
			t.Fatalf("link %s: expected XAttr %q, got %q",
				linkID, expectedXAttr, results[i].FileProperties.ActiveRevision.XAttr)
		}

		// Verify the ObjectCache entry decodes correctly.
		data, _ := client.objectCache.Read(SanitizeLinkID(linkID))
		if data == nil {
			t.Fatalf("link %s: no ObjectCache entry", linkID)
		}
		var cached proton.Link
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&cached); err != nil {
			t.Fatalf("link %s: decode: %v", linkID, err)
		}
		if cached.LinkID != linkID {
			t.Fatalf("link %s: cached entry has LinkID %s (cross-contamination)", linkID, cached.LinkID)
		}
	}
}
