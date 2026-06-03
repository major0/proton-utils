package drive

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

// xattrTestKeyRing generates a fresh RSA keyring for use in XAttr property tests.
func xattrTestKeyRing(t testing.TB) *crypto.KeyRing {
	t.Helper()
	key, err := crypto.GenerateKey("test", "test@test.local", "rsa", 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	kr, err := crypto.NewKeyRing(key)
	if err != nil {
		t.Fatalf("NewKeyRing: %v", err)
	}
	return kr
}

// fataler is satisfied by both *testing.T and *rapid.T.
type fataler interface {
	Fatalf(format string, args ...interface{})
}

// encryptXAttrT encrypts a RevisionXAttrCommon into an armored PGP message
// suitable for use as the XAttr field on a revision or link.
// Works with both *testing.T and *rapid.T via the fataler interface.
func encryptXAttrT(t fataler, nodeKR, addrKR *crypto.KeyRing, common *proton.RevisionXAttrCommon) string {
	data, err := json.Marshal(proton.RevisionXAttr{Common: *common})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	enc, err := nodeKR.Encrypt(crypto.NewPlainMessage(data), addrKR)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	armored, err := enc.GetArmored()
	if err != nil {
		t.Fatalf("GetArmored: %v", err)
	}
	return armored
}

// xattrResolver is a mock LinkResolver for XAttr property tests. It supports
// configurable FetchRevisionXAttr behavior and optional address/keyring lookups.
type xattrResolver struct {
	// fetchFunc is called when FetchRevisionXAttr is invoked. May be nil (no-op).
	fetchFunc func(link *Link)

	// fetchCount tracks the number of FetchRevisionXAttr calls (atomic).
	fetchCount atomic.Int64

	// addrKR is returned by AddressKeyRing when non-nil.
	addrKR *crypto.KeyRing

	// addrID is the address ID returned by AddressForEmail.
	addrID string
}

func (r *xattrResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	return nil, nil
}

func (r *xattrResolver) NewChildLink(_ context.Context, parent *Link, pLink *proton.Link) *Link {
	return NewLink(pLink, parent, parent.share, r)
}

func (r *xattrResolver) GetLink(_ string) *Link { return nil }

func (r *xattrResolver) AddressForEmail(_ string) (proton.Address, bool) {
	if r.addrKR == nil {
		return proton.Address{}, false
	}
	return proton.Address{ID: r.addrID}, true
}

func (r *xattrResolver) AddressKeyRing(id string) (*crypto.KeyRing, bool) {
	if r.addrKR == nil || id != r.addrID {
		return nil, false
	}
	return r.addrKR, true
}

func (r *xattrResolver) Throttle() *api.Throttle { return nil }
func (r *xattrResolver) MaxWorkers() int         { return 1 }

func (r *xattrResolver) FetchRevisionXAttr(_ context.Context, link *Link) {
	r.fetchCount.Add(1)
	if r.fetchFunc != nil {
		r.fetchFunc(link)
	}
}

// makeXAttrShare creates a Share with MemoryCacheLevel set to CacheMetadata
// for use in XAttr property tests.
func makeXAttrShare(resolver LinkResolver) *Share {
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := NewShare(
		&proton.Share{ShareMetadata: proton.ShareMetadata{ShareID: "s"}},
		nil, root, resolver, "",
	)
	root = NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = root
	share.MemoryCacheLevel = api.CacheMetadata
	return share
}

// TestXAttrAccessorResolution_Property verifies that when a fetch-triggering
// metadata accessor (Size, ModifyTime, Mode) is called on a file-type Link
// and the resolver successfully populates XAttr, the accessor returns the
// value decoded from the XAttr blob. CreateTime() is excluded — it returns
// ActiveRevision.CreateTime directly.
//
// **Property 1: Accessor Resolution Returns Correct XAttr Values**
// **Validates: Requirements 1.1, 1.2, 1.4, 3.1, 4.1, 6.1**
func TestXAttrAccessorResolution_Property(t *testing.T) {
	// Generate keyring once (expensive RSA keygen) — reused across property iterations.
	kr := xattrTestKeyRing(t)

	rapid.Check(t, func(t *rapid.T) {
		// Generate XAttr values.
		xattrSize := rapid.Int64Range(0, 1<<40).Draw(t, "xattrSize")
		xattrMode := rapid.Uint32Range(0, 0o7777).Draw(t, "xattrMode")
		// Generate a modification time as a Unix timestamp in a reasonable range.
		xattrMtime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "xattrMtime")
		mtimeStr := time.Unix(xattrMtime, 0).UTC().Format(time.RFC3339)

		// API-level fallback values (should NOT be returned on success).
		apiSize := rapid.Int64Range(100, 1<<30).Draw(t, "apiSize")
		revCreateTime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "revCreateTime")

		// Build the XAttr blob using the pre-generated keyring.
		xattrCommon := &proton.RevisionXAttrCommon{
			ModificationTime: mtimeStr,
			Size:             xattrSize,
			Mode:             xattrMode,
		}
		xattrBlob := encryptXAttrT(t, kr, kr, xattrCommon)

		// Build mock resolver that populates XAttr on fetch.
		resolver := &xattrResolver{
			addrKR: kr,
			addrID: "addr-1",
			fetchFunc: func(link *Link) {
				link.protonLink.FileProperties.ActiveRevision.XAttr = xattrBlob
				link.protonLink.FileProperties.ActiveRevision.SignatureEmail = "test@test.local"
			},
		}

		share := makeXAttrShare(resolver)

		// Build a file-type Link with an active revision and empty XAttr.
		pLink := &proton.Link{
			LinkID:         "file-1",
			Type:           proton.LinkTypeFile,
			State:          proton.LinkStateActive,
			SignatureEmail: "test@test.local",
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID:             "rev-1",
					State:          proton.RevisionStateActive,
					Size:           apiSize,
					CreateTime:     revCreateTime,
					SignatureEmail: "test@test.local",
				},
			},
		}
		link := NewTestLink(pLink, share.Link, share, resolver, "testfile.txt")
		// Pre-cache the keyring to bypass real crypto derivation in tests.
		link.cachedKeyRing = kr

		// Verify Size() returns XAttr size.
		gotSize := link.Size()
		if gotSize != xattrSize {
			t.Fatalf("Size() = %d, want %d (xattr)", gotSize, xattrSize)
		}

		// Verify ModifyTime() returns parsed XAttr mtime.
		gotMtime := link.ModifyTime()
		if gotMtime != xattrMtime {
			t.Fatalf("ModifyTime() = %d, want %d (xattr)", gotMtime, xattrMtime)
		}

		// Verify Mode() returns XAttr mode.
		gotMode := link.Mode()
		if gotMode != xattrMode {
			t.Fatalf("Mode() = %d, want %d (xattr)", gotMode, xattrMode)
		}

		// Verify resolvedMeta is populated (all fields set after first accessor).
		link.cacheMu.RLock()
		m := link.meta
		link.cacheMu.RUnlock()
		if m == nil {
			t.Fatal("resolvedMeta is nil after accessor call")
			return
		}
		if m.size != xattrSize || m.mtime != xattrMtime || m.mode != xattrMode {
			t.Fatalf("resolvedMeta fields mismatch: size=%d mtime=%d mode=%d",
				m.size, m.mtime, m.mode)
		}
	})
}

// TestXAttrFallbackOnFailure_Property verifies that when FetchRevisionXAttr
// does NOT populate XAttr (fetch failure), accessors return fallback values:
// Size→ActiveRevision.Size, ModifyTime→ActiveRevision.CreateTime,
// CreateTime→protonLink.CreateTime, Mode→0.
//
// **Property 2: Fallback Values on Fetch Failure**
// **Validates: Requirements 1.6, 3.2, 4.2, 5.2, 6.4**
func TestXAttrFallbackOnFailure_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		apiSize := rapid.Int64Range(1, 1<<30).Draw(t, "apiSize")
		revCreateTime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "revCreateTime")
		linkCreateTime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "linkCreateTime")

		// Resolver does NOT populate XAttr — simulates fetch failure.
		resolver := &xattrResolver{
			fetchFunc: nil, // no-op: XAttr remains empty
		}

		share := makeXAttrShare(resolver)

		pLink := &proton.Link{
			LinkID:         "file-fail",
			Type:           proton.LinkTypeFile,
			State:          proton.LinkStateActive,
			CreateTime:     linkCreateTime,
			SignatureEmail: "test@test.local",
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID:         "rev-1",
					State:      proton.RevisionStateActive,
					Size:       apiSize,
					CreateTime: revCreateTime,
				},
			},
		}
		link := NewTestLink(pLink, share.Link, share, resolver, "failfile.txt")

		// Size() falls back to ActiveRevision.Size.
		if got := link.Size(); got != apiSize {
			t.Fatalf("Size() = %d, want %d (API fallback)", got, apiSize)
		}

		// ModifyTime() falls back to ActiveRevision.CreateTime.
		if got := link.ModifyTime(); got != revCreateTime {
			t.Fatalf("ModifyTime() = %d, want %d (rev.CreateTime fallback)", got, revCreateTime)
		}

		// CreateTime() falls back — with active revision present, returns
		// ActiveRevision.CreateTime (not protonLink.CreateTime).
		if got := link.CreateTime(); got != revCreateTime {
			t.Fatalf("CreateTime() = %d, want %d (rev.CreateTime)", got, revCreateTime)
		}

		// Mode() returns 0 (no XAttr to decrypt).
		if got := link.Mode(); got != 0 {
			t.Fatalf("Mode() = %d, want 0 (fallback)", got)
		}
	})
}

// TestXAttrFolderLinkLevel_Property verifies that folder Links read metadata
// from the link-level XAttr field (already populated by the listing API) and
// do NOT invoke FetchRevisionXAttr. Also verifies folder ModifyTime() falls
// back to protonLink.ModifyTime when XAttr is empty.
//
// **Property 3: Folder Links Read From Link-Level XAttr**
// **Validates: Requirements 1.7, 4.5, 6.3, 11.2**
func TestXAttrFolderLinkLevel_Property(t *testing.T) {
	// Generate keyring once (expensive RSA keygen).
	kr := xattrTestKeyRing(t)

	rapid.Check(t, func(t *rapid.T) {
		xattrSize := rapid.Int64Range(0, 1<<20).Draw(t, "xattrSize")
		xattrMode := rapid.Uint32Range(0, 0o7777).Draw(t, "xattrMode")
		xattrMtime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "xattrMtime")
		mtimeStr := time.Unix(xattrMtime, 0).UTC().Format(time.RFC3339)
		linkModifyTime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "linkModifyTime")

		xattrCommon := &proton.RevisionXAttrCommon{
			ModificationTime: mtimeStr,
			Size:             xattrSize,
			Mode:             xattrMode,
		}
		xattrBlob := encryptXAttrT(t, kr, kr, xattrCommon)

		resolver := &xattrResolver{
			addrKR: kr,
			addrID: "addr-1",
		}

		share := makeXAttrShare(resolver)

		// Folder link with link-level XAttr populated.
		pLink := &proton.Link{
			LinkID:           "folder-1",
			Type:             proton.LinkTypeFolder,
			State:            proton.LinkStateActive,
			ModifyTime:       linkModifyTime,
			SignatureEmail:   "test@test.local",
			XAttr:            xattrBlob,
			FolderProperties: &proton.FolderProperties{},
		}
		link := NewTestLink(pLink, share.Link, share, resolver, "testfolder")
		// Pre-cache the keyring to bypass real crypto derivation.
		link.cachedKeyRing = kr

		// Verify accessors read from link-level XAttr.
		if got := link.Size(); got != xattrSize {
			t.Fatalf("folder Size() = %d, want %d", got, xattrSize)
		}
		if got := link.ModifyTime(); got != xattrMtime {
			t.Fatalf("folder ModifyTime() = %d, want %d", got, xattrMtime)
		}
		if got := link.Mode(); got != xattrMode {
			t.Fatalf("folder Mode() = %d, want %d", got, xattrMode)
		}

		// FetchRevisionXAttr should NOT have been called for a folder.
		if n := resolver.fetchCount.Load(); n != 0 {
			t.Fatalf("FetchRevisionXAttr called %d times for folder, want 0", n)
		}
	})

	// Sub-test: folder with empty XAttr falls back to protonLink.ModifyTime.
	t.Run("folder_empty_xattr_fallback", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			linkModifyTime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "linkModifyTime")

			resolver := &xattrResolver{}
			share := makeXAttrShare(resolver)

			pLink := &proton.Link{
				LinkID:           "folder-empty",
				Type:             proton.LinkTypeFolder,
				State:            proton.LinkStateActive,
				ModifyTime:       linkModifyTime,
				SignatureEmail:   "test@test.local",
				FolderProperties: &proton.FolderProperties{},
			}
			link := NewTestLink(pLink, share.Link, share, resolver, "emptyfolder")

			// ModifyTime() falls back to protonLink.ModifyTime.
			if got := link.ModifyTime(); got != linkModifyTime {
				t.Fatalf("folder ModifyTime() = %d, want %d (fallback)", got, linkModifyTime)
			}

			// No fetch for folders.
			if n := resolver.fetchCount.Load(); n != 0 {
				t.Fatalf("FetchRevisionXAttr called %d times for folder, want 0", n)
			}
		})
	})
}

// TestXAttrSkipConditions_Property verifies that Links in states that should
// prevent fetching (draft, nil FileProperties, empty revision ID, non-active
// revision, pre-populated XAttr) do NOT invoke FetchRevisionXAttr.
//
// **Property 4: Skip Conditions Prevent Fetch**
// **Validates: Requirements 2.1, 2.2, 2.4, 2.5, 9.1, 9.5**
func TestXAttrSkipConditions_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Choose a skip condition.
		condition := rapid.SampledFrom([]string{
			"draft",
			"nil_file_properties",
			"empty_revision_id",
			"non_active_revision",
			"pre_populated_xattr",
		}).Draw(t, "condition")

		apiSize := rapid.Int64Range(1, 1<<30).Draw(t, "apiSize")
		revCreateTime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "revCreateTime")

		resolver := &xattrResolver{}
		share := makeXAttrShare(resolver)

		var pLink *proton.Link
		switch condition {
		case "draft":
			pLink = &proton.Link{
				LinkID:         "draft-file",
				Type:           proton.LinkTypeFile,
				State:          proton.LinkStateDraft,
				SignatureEmail: "test@test.local",
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{
						ID:         "rev-1",
						State:      proton.RevisionStateActive,
						Size:       apiSize,
						CreateTime: revCreateTime,
					},
				},
			}
		case "nil_file_properties":
			pLink = &proton.Link{
				LinkID:         "no-fp-file",
				Type:           proton.LinkTypeFile,
				State:          proton.LinkStateActive,
				SignatureEmail: "test@test.local",
				// FileProperties is nil.
			}
		case "empty_revision_id":
			pLink = &proton.Link{
				LinkID:         "no-rev-file",
				Type:           proton.LinkTypeFile,
				State:          proton.LinkStateActive,
				SignatureEmail: "test@test.local",
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{
						ID:         "", // empty
						State:      proton.RevisionStateActive,
						Size:       apiSize,
						CreateTime: revCreateTime,
					},
				},
			}
		case "non_active_revision":
			pLink = &proton.Link{
				LinkID:         "obsolete-rev-file",
				Type:           proton.LinkTypeFile,
				State:          proton.LinkStateActive,
				SignatureEmail: "test@test.local",
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{
						ID:         "rev-1",
						State:      proton.RevisionStateObsolete,
						Size:       apiSize,
						CreateTime: revCreateTime,
					},
				},
			}
		case "pre_populated_xattr":
			pLink = &proton.Link{
				LinkID:         "prepop-file",
				Type:           proton.LinkTypeFile,
				State:          proton.LinkStateActive,
				SignatureEmail: "test@test.local",
				FileProperties: &proton.FileProperties{
					ActiveRevision: proton.RevisionMetadata{
						ID:         "rev-1",
						State:      proton.RevisionStateActive,
						Size:       apiSize,
						CreateTime: revCreateTime,
						XAttr:      "already-populated", // non-empty
					},
				},
			}
		}

		link := NewTestLink(pLink, share.Link, share, resolver, "skipfile.txt")

		// Call all metadata accessors.
		_ = link.Size()
		_ = link.ModifyTime()
		_ = link.Mode()

		// Verify FetchRevisionXAttr was never called.
		if n := resolver.fetchCount.Load(); n != 0 {
			t.Fatalf("condition=%s: FetchRevisionXAttr called %d times, want 0", condition, n)
		}
	})
}

// TestXAttrTrashedLinksFetchable_Property verifies that trashed file-type
// Links with an active revision and empty XAttr DO invoke FetchRevisionXAttr
// when a metadata accessor is called.
//
// **Property 5: Trashed Links Are Fetchable**
// **Validates: Requirements 2.3**
func TestXAttrTrashedLinksFetchable_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		apiSize := rapid.Int64Range(1, 1<<30).Draw(t, "apiSize")
		revCreateTime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "revCreateTime")

		resolver := &xattrResolver{
			fetchFunc: nil, // no-op: just counts the call
		}
		share := makeXAttrShare(resolver)

		pLink := &proton.Link{
			LinkID:         "trashed-file",
			Type:           proton.LinkTypeFile,
			State:          proton.LinkStateTrashed,
			SignatureEmail: "test@test.local",
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID:         "rev-1",
					State:      proton.RevisionStateActive,
					Size:       apiSize,
					CreateTime: revCreateTime,
					// XAttr is empty — fetch should be triggered.
				},
			},
		}
		link := NewTestLink(pLink, share.Link, share, resolver, "trashedfile.txt")

		// Call a fetch-triggering accessor.
		_ = link.Size()

		// Verify FetchRevisionXAttr WAS called (trashed state does not prevent fetch).
		if n := resolver.fetchCount.Load(); n == 0 {
			t.Fatal("FetchRevisionXAttr not called for trashed link, expected at least 1 call")
		}
	})
}

// TestXAttrSingleFlightConcurrency_Property verifies that when N goroutines
// call Size() concurrently on the same Link, FetchRevisionXAttr is called at
// most once, and all goroutines observe the same resolvedMeta pointer.
//
// **Property 6: Single-Flight Fetch Under Concurrency**
// **Validates: Requirements 1.5, 8.1, 8.5**
func TestXAttrSingleFlightConcurrency_Property(t *testing.T) {
	kr := xattrTestKeyRing(t)

	rapid.Check(t, func(t *rapid.T) {
		xattrSize := rapid.Int64Range(1, 1<<40).Draw(t, "xattrSize")
		xattrMode := rapid.Uint32Range(0, 0o7777).Draw(t, "xattrMode")
		xattrMtime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "xattrMtime")
		mtimeStr := time.Unix(xattrMtime, 0).UTC().Format(time.RFC3339)

		xattrCommon := &proton.RevisionXAttrCommon{
			ModificationTime: mtimeStr,
			Size:             xattrSize,
			Mode:             xattrMode,
		}
		xattrBlob := encryptXAttrT(t, kr, kr, xattrCommon)

		// Mock resolver with a brief sleep to simulate network latency.
		resolver := &xattrResolver{
			addrKR: kr,
			addrID: "addr-1",
			fetchFunc: func(link *Link) {
				time.Sleep(5 * time.Millisecond)
				link.protonLink.FileProperties.ActiveRevision.XAttr = xattrBlob
				link.protonLink.FileProperties.ActiveRevision.SignatureEmail = "test@test.local"
			},
		}

		share := makeXAttrShare(resolver)

		pLink := &proton.Link{
			LinkID:         "concurrent-file",
			Type:           proton.LinkTypeFile,
			State:          proton.LinkStateActive,
			SignatureEmail: "test@test.local",
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID:             "rev-1",
					State:          proton.RevisionStateActive,
					Size:           999,
					CreateTime:     1000000000,
					SignatureEmail: "test@test.local",
				},
			},
		}
		link := NewTestLink(pLink, share.Link, share, resolver, "concurrent.txt")
		link.cachedKeyRing = kr

		const N = 50
		var wg sync.WaitGroup
		wg.Add(N)
		results := make([]int64, N)

		for i := 0; i < N; i++ {
			go func(idx int) {
				defer wg.Done()
				results[idx] = link.Size()
			}(i)
		}
		wg.Wait()

		// Verify FetchRevisionXAttr was called exactly once.
		if n := resolver.fetchCount.Load(); n != 1 {
			t.Fatalf("FetchRevisionXAttr called %d times, want 1", n)
		}

		// Verify all goroutines observed the same value.
		for i, v := range results {
			if v != xattrSize {
				t.Fatalf("goroutine %d got Size()=%d, want %d", i, v, xattrSize)
			}
		}

		// Verify all goroutines share the same resolvedMeta pointer.
		link.cacheMu.RLock()
		m := link.meta
		link.cacheMu.RUnlock()
		if m == nil {
			t.Fatal("resolvedMeta is nil after concurrent access")
			return
		}
		if m.size != xattrSize {
			t.Fatalf("resolvedMeta.size=%d, want %d", m.size, xattrSize)
		}
	})
}

// TestXAttrCachingAfterResolution_Property verifies that after a successful
// XAttr resolution, subsequent accessor calls return the cached value without
// invoking FetchRevisionXAttr or decryptXAttr again.
//
// **Property 7: Caching After Successful Resolution**
// **Validates: Requirements 7.1, 7.2, 7.3, 7.4, 10.5**
func TestXAttrCachingAfterResolution_Property(t *testing.T) {
	kr := xattrTestKeyRing(t)

	rapid.Check(t, func(t *rapid.T) {
		xattrSize := rapid.Int64Range(0, 1<<40).Draw(t, "xattrSize")
		xattrMode := rapid.Uint32Range(0, 0o7777).Draw(t, "xattrMode")
		xattrMtime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "xattrMtime")
		mtimeStr := time.Unix(xattrMtime, 0).UTC().Format(time.RFC3339)
		revCreateTime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "revCreateTime")

		xattrCommon := &proton.RevisionXAttrCommon{
			ModificationTime: mtimeStr,
			Size:             xattrSize,
			Mode:             xattrMode,
		}
		xattrBlob := encryptXAttrT(t, kr, kr, xattrCommon)

		resolver := &xattrResolver{
			addrKR: kr,
			addrID: "addr-1",
			fetchFunc: func(link *Link) {
				link.protonLink.FileProperties.ActiveRevision.XAttr = xattrBlob
				link.protonLink.FileProperties.ActiveRevision.SignatureEmail = "test@test.local"
			},
		}

		share := makeXAttrShare(resolver)

		pLink := &proton.Link{
			LinkID:         "cache-file",
			Type:           proton.LinkTypeFile,
			State:          proton.LinkStateActive,
			SignatureEmail: "test@test.local",
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID:             "rev-1",
					State:          proton.RevisionStateActive,
					Size:           42,
					CreateTime:     revCreateTime,
					SignatureEmail: "test@test.local",
				},
			},
		}
		link := NewTestLink(pLink, share.Link, share, resolver, "cached.txt")
		link.cachedKeyRing = kr

		// First call — triggers fetch + decrypt.
		got1 := link.Size()
		if got1 != xattrSize {
			t.Fatalf("first Size() = %d, want %d", got1, xattrSize)
		}

		// Verify meta is populated after first call.
		link.cacheMu.RLock()
		m := link.meta
		link.cacheMu.RUnlock()
		if m == nil {
			t.Fatal("l.meta is nil after first Size() call")
		}

		// Second call — should use cache, no additional fetch.
		got2 := link.Size()
		if got2 != xattrSize {
			t.Fatalf("second Size() = %d, want %d", got2, xattrSize)
		}

		// Also verify ModifyTime and Mode use cache.
		gotMtime := link.ModifyTime()
		if gotMtime != xattrMtime {
			t.Fatalf("ModifyTime() = %d, want %d", gotMtime, xattrMtime)
		}
		gotMode := link.Mode()
		if gotMode != xattrMode {
			t.Fatalf("Mode() = %d, want %d", gotMode, xattrMode)
		}

		// CreateTime benefits from cache (returns ctime from resolvedMeta).
		gotCtime := link.CreateTime()
		if gotCtime != revCreateTime {
			t.Fatalf("CreateTime() = %d, want %d", gotCtime, revCreateTime)
		}

		// Verify FetchRevisionXAttr was called exactly once across all accessor calls.
		if n := resolver.fetchCount.Load(); n != 1 {
			t.Fatalf("FetchRevisionXAttr called %d times, want 1", n)
		}
	})
}

// TestXAttrRetryOnTransientFailure_Property verifies that when
// FetchRevisionXAttr fails on the first call (XAttr remains empty),
// a subsequent accessor call re-attempts the fetch and succeeds.
//
// **Property 8: Retry on Transient Failure**
// **Validates: Requirements 7.7, 8.4**
func TestXAttrRetryOnTransientFailure_Property(t *testing.T) {
	kr := xattrTestKeyRing(t)

	rapid.Check(t, func(t *rapid.T) {
		xattrSize := rapid.Int64Range(1, 1<<40).Draw(t, "xattrSize")
		xattrMode := rapid.Uint32Range(0, 0o7777).Draw(t, "xattrMode")
		xattrMtime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "xattrMtime")
		mtimeStr := time.Unix(xattrMtime, 0).UTC().Format(time.RFC3339)
		apiSize := rapid.Int64Range(100, 1<<30).Draw(t, "apiSize")
		revCreateTime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "revCreateTime")

		xattrCommon := &proton.RevisionXAttrCommon{
			ModificationTime: mtimeStr,
			Size:             xattrSize,
			Mode:             xattrMode,
		}
		xattrBlob := encryptXAttrT(t, kr, kr, xattrCommon)

		// Counter-based mock: first call does nothing (simulates failure),
		// second call populates XAttr (simulates success).
		var callCount atomic.Int64
		resolver := &xattrResolver{
			addrKR: kr,
			addrID: "addr-1",
			fetchFunc: func(link *Link) {
				n := callCount.Add(1)
				if n >= 2 {
					// Second call succeeds.
					link.protonLink.FileProperties.ActiveRevision.XAttr = xattrBlob
					link.protonLink.FileProperties.ActiveRevision.SignatureEmail = "test@test.local"
				}
				// First call: no-op — XAttr remains empty (simulates failure).
			},
		}

		share := makeXAttrShare(resolver)

		pLink := &proton.Link{
			LinkID:         "retry-file",
			Type:           proton.LinkTypeFile,
			State:          proton.LinkStateActive,
			SignatureEmail: "test@test.local",
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID:             "rev-1",
					State:          proton.RevisionStateActive,
					Size:           apiSize,
					CreateTime:     revCreateTime,
					SignatureEmail: "test@test.local",
				},
			},
		}
		link := NewTestLink(pLink, share.Link, share, resolver, "retry.txt")
		link.cachedKeyRing = kr

		// First call — fetch fails, returns fallback (apiSize).
		got1 := link.Size()
		if got1 != apiSize {
			t.Fatalf("first Size() = %d, want %d (API fallback)", got1, apiSize)
		}

		// Verify l.meta remains nil after failed fetch (fallback not cached).
		link.cacheMu.RLock()
		m1 := link.meta
		link.cacheMu.RUnlock()
		if m1 != nil {
			t.Fatal("l.meta should be nil after failed fetch, but is non-nil")
		}

		// Second call — fetch succeeds, returns XAttr size.
		got2 := link.Size()
		if got2 != xattrSize {
			t.Fatalf("second Size() = %d, want %d (XAttr)", got2, xattrSize)
		}

		// Verify FetchRevisionXAttr was called twice (retry after failure).
		if n := resolver.fetchCount.Load(); n != 2 {
			t.Fatalf("FetchRevisionXAttr called %d times, want 2", n)
		}
	})
}

// TestXAttrFetchDoesNotBlockCacheReads verifies that a slow in-flight
// FetchRevisionXAttr does not block non-fetch accessors like Name() and
// KeyRing() from completing promptly.
//
// **Property 9: Fetch Does Not Block Cache Reads**
// **Validates: Requirements 8.2**
func TestXAttrFetchDoesNotBlockCacheReads(t *testing.T) {
	kr := xattrTestKeyRing(t)

	// Slow mock resolver: takes 200ms to complete the fetch.
	resolver := &xattrResolver{
		addrKR: kr,
		addrID: "addr-1",
		fetchFunc: func(link *Link) {
			time.Sleep(200 * time.Millisecond)
			// Populate XAttr after delay.
			xattrCommon := &proton.RevisionXAttrCommon{
				ModificationTime: time.Now().UTC().Format(time.RFC3339),
				Size:             1234,
				Mode:             0o644,
			}
			link.protonLink.FileProperties.ActiveRevision.XAttr = encryptXAttrT(t, kr, kr, xattrCommon)
			link.protonLink.FileProperties.ActiveRevision.SignatureEmail = "test@test.local"
		},
	}

	share := makeXAttrShare(resolver)

	pLink := &proton.Link{
		LinkID:         "slow-fetch-file",
		Type:           proton.LinkTypeFile,
		State:          proton.LinkStateActive,
		SignatureEmail: "test@test.local",
		FileProperties: &proton.FileProperties{
			ActiveRevision: proton.RevisionMetadata{
				ID:             "rev-1",
				State:          proton.RevisionStateActive,
				Size:           999,
				CreateTime:     1000000000,
				SignatureEmail: "test@test.local",
			},
		},
	}
	link := NewTestLink(pLink, share.Link, share, resolver, "slowfile.txt")
	link.cachedKeyRing = kr

	// Start a goroutine that triggers the slow fetch via Size().
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = link.Size()
	}()

	// Give the fetch goroutine a moment to acquire fetchMu.
	time.Sleep(10 * time.Millisecond)

	// Non-fetch accessors should complete without blocking on fetchMu.
	done := make(chan struct{})
	go func() {
		_, _ = link.Name()
		_, _ = link.KeyRing()
		close(done)
	}()

	select {
	case <-done:
		// Success: Name() and KeyRing() completed while fetch is in-flight.
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Name()/KeyRing() blocked for >100ms while FetchRevisionXAttr is in-flight")
	}

	// Wait for the fetch goroutine to finish.
	wg.Wait()
}

// TestXAttrNoFetchWithoutAccessor_Property verifies that constructing a Link
// and calling only non-metadata accessors (Name, KeyRing, LinkID) or the
// non-fetch accessor CreateTime() does NOT trigger FetchRevisionXAttr.
//
// **Property 10: No Fetch Without Accessor Call**
// **Validates: Requirements 5.3, 9.4**
func TestXAttrNoFetchWithoutAccessor_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		apiSize := rapid.Int64Range(1, 1<<30).Draw(t, "apiSize")
		revCreateTime := rapid.Int64Range(1000000000, 2000000000).Draw(t, "revCreateTime")

		resolver := &xattrResolver{
			fetchFunc: nil, // counts calls but does nothing
		}
		share := makeXAttrShare(resolver)

		pLink := &proton.Link{
			LinkID:         "nofetch-file",
			Type:           proton.LinkTypeFile,
			State:          proton.LinkStateActive,
			SignatureEmail: "test@test.local",
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					ID:         "rev-1",
					State:      proton.RevisionStateActive,
					Size:       apiSize,
					CreateTime: revCreateTime,
				},
			},
		}
		link := NewTestLink(pLink, share.Link, share, resolver, "nofetch.txt")

		// Call only non-metadata accessors.
		_, _ = link.Name()
		_ = link.LinkID()

		// CreateTime is a non-fetch accessor — should NOT trigger fetch.
		_ = link.CreateTime()

		// Verify FetchRevisionXAttr was never called.
		if n := resolver.fetchCount.Load(); n != 0 {
			t.Fatalf("FetchRevisionXAttr called %d times, want 0", n)
		}
	})
}
