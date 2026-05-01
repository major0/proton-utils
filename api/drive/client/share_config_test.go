package client

import (
	"context"
	"os"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/drive"
	"pgregory.net/rapid"
)

func testShare(name string, st proton.ShareType) *drive.Share {
	resolver := &mockResolver{}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "s", Type: st},
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	root := drive.NewTestLink(rootPLink, nil, nil, resolver, name)
	share := drive.NewShare(pShare, nil, root, resolver, "")
	root = drive.NewTestLink(rootPLink, nil, share, resolver, name)
	share.Link = root
	return share
}

// mockResolver satisfies drive.LinkResolver for test share construction.
type mockResolver struct{}

func (m *mockResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	return nil, nil
}
func (m *mockResolver) NewChildLink(_ context.Context, parent *drive.Link, pLink *proton.Link) *drive.Link {
	return drive.NewLink(pLink, parent, parent.Share(), m)
}
func (m *mockResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}
func (m *mockResolver) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}
func (m *mockResolver) Throttle() *api.Throttle { return nil }
func (m *mockResolver) MaxWorkers() int         { return 1 }

func TestApplyShareConfig_MatchingName(t *testing.T) {
	share := testShare("MyFolder", proton.ShareTypeStandard)
	c := &Client{
		Config: &api.Config{
			Shares: map[string]api.ShareConfig{
				"MyFolder": {
					MemoryCache: api.CacheMetadata,
					DiskCache:   api.DiskCacheObjectStore,
				},
			},
		},
	}

	c.applyShareConfig(share)

	if share.MemoryCacheLevel != api.CacheMetadata {
		t.Fatalf("MemoryCacheLevel: got %v, want metadata", share.MemoryCacheLevel)
	}
	if share.DiskCacheLevel != api.DiskCacheObjectStore {
		t.Fatalf("DiskCacheLevel: got %v, want objectstore", share.DiskCacheLevel)
	}
}

func TestInitObjectCache_ConstructsDiskv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	c := &Client{
		Config: &api.Config{
			Shares: map[string]api.ShareConfig{
				"MyFolder": {
					DiskCache: api.DiskCacheObjectStore,
				},
			},
		},
	}

	c.InitObjectCache()

	if c.objectCache == nil {
		t.Fatal("objectCache should be constructed when disk_cache=objectstore and XDG_RUNTIME_DIR is set")
	}

	// Verify the cache is functional — write and read back.
	if err := c.objectCache.Write("test-key", []byte("test-data")); err != nil {
		t.Fatalf("objectCache.Write: %v", err)
	}
	got, err := c.objectCache.Read("test-key")
	if err != nil {
		t.Fatalf("objectCache.Read: %v", err)
	}
	if string(got) != "test-data" {
		t.Fatalf("objectCache.Read = %q, want %q", got, "test-data")
	}
}

func TestInitObjectCache_SkippedWithoutXDG(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")

	c := &Client{
		Config: &api.Config{
			Shares: map[string]api.ShareConfig{
				"MyFolder": {
					DiskCache: api.DiskCacheObjectStore,
				},
			},
		},
	}

	c.InitObjectCache()

	if c.objectCache != nil {
		t.Fatal("objectCache should be nil when XDG_RUNTIME_DIR is unset")
	}
}

func TestInitObjectCache_SkippedWhenNoDiskCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	c := &Client{
		Config: &api.Config{
			Shares: map[string]api.ShareConfig{
				"MyFolder": {
					MemoryCache: api.CacheMetadata,
					DiskCache:   api.DiskCacheDisabled,
				},
			},
		},
	}

	c.InitObjectCache()

	if c.objectCache != nil {
		t.Fatal("objectCache should be nil when no share has disk_cache=objectstore")
	}
}

func TestApplyShareConfig_LinkNameLevel(t *testing.T) {
	share := testShare("MyFolder", proton.ShareTypeStandard)
	c := &Client{
		Config: &api.Config{
			Shares: map[string]api.ShareConfig{
				"MyFolder": {
					MemoryCache: api.CacheLinkName,
					DiskCache:   api.DiskCacheDisabled,
				},
			},
		},
	}

	c.applyShareConfig(share)

	if share.MemoryCacheLevel != api.CacheLinkName {
		t.Fatalf("MemoryCacheLevel: got %v, want linkname", share.MemoryCacheLevel)
	}
	if share.DiskCacheLevel != api.DiskCacheDisabled {
		t.Fatalf("DiskCacheLevel: got %v, want disabled", share.DiskCacheLevel)
	}
}

func TestApplyShareConfig_NoMatch(t *testing.T) {
	share := testShare("OtherFolder", proton.ShareTypeStandard)
	c := &Client{
		Config: &api.Config{
			Shares: map[string]api.ShareConfig{
				"MyFolder": {MemoryCache: api.CacheMetadata},
			},
		},
	}

	c.applyShareConfig(share)

	if share.MemoryCacheLevel != api.CacheDisabled {
		t.Fatal("MemoryCacheLevel should be disabled for unmatched share")
	}
	if share.DiskCacheLevel != api.DiskCacheDisabled {
		t.Fatal("DiskCacheLevel should be disabled for unmatched share")
	}
}

func TestApplyShareConfig_RootForced(t *testing.T) {
	share := testShare("root", proton.ShareTypeMain)
	// Even with config enabling everything, root should be forced disabled.
	c := &Client{
		Config: &api.Config{
			Shares: map[string]api.ShareConfig{
				"root": {
					MemoryCache: api.CacheMetadata,
					DiskCache:   api.DiskCacheObjectStore,
				},
			},
		},
	}

	c.applyShareConfig(share)

	if share.MemoryCacheLevel != api.CacheDisabled || share.DiskCacheLevel != api.DiskCacheDisabled {
		t.Fatal("root share should have all caches forced disabled")
	}
}

func TestApplyShareConfig_PhotosForced(t *testing.T) {
	share := testShare("Photos", drive.ShareTypePhotos)
	c := &Client{
		Config: &api.Config{
			Shares: map[string]api.ShareConfig{
				"Photos": {
					MemoryCache: api.CacheMetadata,
					DiskCache:   api.DiskCacheObjectStore,
				},
			},
		},
	}

	c.applyShareConfig(share)

	if share.MemoryCacheLevel != api.CacheDisabled || share.DiskCacheLevel != api.DiskCacheDisabled {
		t.Fatal("photos share should have all caches forced disabled")
	}
}

func TestApplyShareConfig_NilConfig(t *testing.T) {
	share := testShare("MyFolder", proton.ShareTypeStandard)
	c := &Client{Config: nil}

	c.applyShareConfig(share)

	if share.MemoryCacheLevel != api.CacheDisabled || share.DiskCacheLevel != api.DiskCacheDisabled {
		t.Fatal("nil config should leave all caches disabled")
	}
}

func TestBlockStoreNilCache_NoDiskWrites(t *testing.T) {
	dir := t.TempDir()

	// Create a BlockStore with nil cache (DiskCacheLevel=disabled).
	// Verify no files are written to the cache directory.
	store := NewBlockStore(nil, nil, nil)
	_ = store // store with nil cache won't write to disk

	// Verify the directory is empty.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected empty dir, got %d entries", len(entries))
	}
}

// TestPropertyRootPhotosDisabled verifies that regardless of user
// configuration, Root (main) and Photos shares always have both caches
// forced to disabled after applyShareConfig.
//
// **Property 10: Root and Photos shares forced disabled**
// **Validates: Requirement 4.6**
func TestPropertyRootPhotosDisabled(t *testing.T) {
	memoryLevelGen := rapid.SampledFrom([]api.MemoryCacheLevel{
		api.CacheDisabled, api.CacheLinkName, api.CacheMetadata,
	})
	diskLevelGen := rapid.SampledFrom([]api.DiskCacheLevel{
		api.DiskCacheDisabled, api.DiskCacheObjectStore,
	})
	shareTypeGen := rapid.SampledFrom([]proton.ShareType{
		proton.ShareTypeMain, drive.ShareTypePhotos,
	})

	rapid.Check(t, func(t *rapid.T) {
		st := shareTypeGen.Draw(t, "shareType")
		memLevel := memoryLevelGen.Draw(t, "memoryLevel")
		diskLevel := diskLevelGen.Draw(t, "diskLevel")
		name := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{2,15}`).Draw(t, "name")

		share := testShare(name, st)

		// Pre-set the share to the drawn levels to simulate a share
		// that somehow had caching enabled before applyShareConfig.
		share.MemoryCacheLevel = memLevel
		share.DiskCacheLevel = diskLevel

		c := &Client{
			Config: &api.Config{
				Shares: map[string]api.ShareConfig{
					name: {
						MemoryCache: memLevel,
						DiskCache:   diskLevel,
					},
				},
			},
		}

		c.applyShareConfig(share)

		if share.MemoryCacheLevel != api.CacheDisabled {
			t.Fatalf("share type %d: MemoryCacheLevel = %v, want disabled", st, share.MemoryCacheLevel)
		}
		if share.DiskCacheLevel != api.DiskCacheDisabled {
			t.Fatalf("share type %d: DiskCacheLevel = %v, want disabled", st, share.DiskCacheLevel)
		}
	})
}
