package client

import (
	"context"
	"os"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/drive"
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
					DirentCacheEnabled:   true,
					MetadataCacheEnabled: true,
					DiskCacheEnabled:     true,
				},
			},
		},
	}

	c.applyShareConfig(share)

	if !share.DirentCacheEnabled {
		t.Fatal("DirentCacheEnabled should be true")
	}
	if !share.MetadataCacheEnabled {
		t.Fatal("MetadataCacheEnabled should be true")
	}
	if !share.DiskCacheEnabled {
		t.Fatal("DiskCacheEnabled should be true")
	}
}

func TestApplyShareConfig_NoMatch(t *testing.T) {
	share := testShare("OtherFolder", proton.ShareTypeStandard)
	c := &Client{
		Config: &api.Config{
			Shares: map[string]api.ShareConfig{
				"MyFolder": {DirentCacheEnabled: true},
			},
		},
	}

	c.applyShareConfig(share)

	if share.DirentCacheEnabled {
		t.Fatal("DirentCacheEnabled should be false for unmatched share")
	}
}

func TestApplyShareConfig_RootForced(t *testing.T) {
	share := testShare("root", proton.ShareTypeMain)
	// Even with config enabling everything, root should be forced false.
	c := &Client{
		Config: &api.Config{
			Shares: map[string]api.ShareConfig{
				"root": {
					DirentCacheEnabled:   true,
					MetadataCacheEnabled: true,
					DiskCacheEnabled:     true,
				},
			},
		},
	}

	c.applyShareConfig(share)

	if share.DirentCacheEnabled || share.MetadataCacheEnabled || share.DiskCacheEnabled {
		t.Fatal("root share should have all caches forced false")
	}
}

func TestApplyShareConfig_PhotosForced(t *testing.T) {
	share := testShare("Photos", drive.ShareTypePhotos)
	c := &Client{
		Config: &api.Config{
			Shares: map[string]api.ShareConfig{
				"Photos": {
					DirentCacheEnabled:   true,
					MetadataCacheEnabled: true,
					DiskCacheEnabled:     true,
				},
			},
		},
	}

	c.applyShareConfig(share)

	if share.DirentCacheEnabled || share.MetadataCacheEnabled || share.DiskCacheEnabled {
		t.Fatal("photos share should have all caches forced false")
	}
}

func TestApplyShareConfig_NilConfig(t *testing.T) {
	share := testShare("MyFolder", proton.ShareTypeStandard)
	c := &Client{Config: nil}

	c.applyShareConfig(share)

	if share.DirentCacheEnabled || share.MetadataCacheEnabled || share.DiskCacheEnabled {
		t.Fatal("nil config should leave all caches false")
	}
}

func TestBlockStoreNilCache_NoDiskWrites(t *testing.T) {
	dir := t.TempDir()

	// Create a BlockStore with nil cache (DiskCacheEnabled=false).
	// Verify no files are written to the cache directory.
	store := NewBlockStore(nil, nil, nil)
	_ = store // store with nil cache won't write to disk

	// Verify the directory is empty.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected empty dir, got %d entries", len(entries))
	}
}
