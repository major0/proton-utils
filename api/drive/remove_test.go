package drive

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

// mockRemoveResolver is a minimal LinkResolver that returns a fixed
// number of pre-decrypted children. Used to test non-empty folder
// detection without real crypto infrastructure.
type mockRemoveResolver struct {
	childCount int
}

func (m *mockRemoveResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	children := make([]proton.Link, m.childCount)
	for i := range children {
		children[i] = proton.Link{
			LinkID: fmt.Sprintf("child-%d", i),
			Type:   proton.LinkTypeFile,
		}
	}
	return children, nil
}

func (m *mockRemoveResolver) NewChildLink(_ context.Context, parent *Link, pLink *proton.Link) *Link {
	return NewTestLink(pLink, parent, parent.share, m, pLink.LinkID)
}

func (m *mockRemoveResolver) GetLink(_ string) *Link { return nil }

func (m *mockRemoveResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}

func (m *mockRemoveResolver) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}

func (m *mockRemoveResolver) Throttle() *api.Throttle { return nil }
func (m *mockRemoveResolver) MaxWorkers() int         { return 1 }

// makeTestFolder creates a folder Link with a parent, backed by the
// given resolver. Uses NewTestLink for test name overrides.
func makeTestFolder(resolver LinkResolver, name string) *Link {
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	rootLink := NewTestLink(rootPLink, nil, nil, resolver, "root")

	share := NewShare(pShare, nil, rootLink, resolver, "")
	rootLink = NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = rootLink

	folderPLink := &proton.Link{LinkID: name, Type: proton.LinkTypeFolder}
	folder := NewTestLink(folderPLink, rootLink, share, resolver, name)

	return folder
}

// makeTestShareRoot creates a share root Link (ParentLink() == nil).
func makeTestShareRoot(resolver LinkResolver) (*Link, *Share) {
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	rootLink := NewTestLink(rootPLink, nil, nil, resolver, "root")

	share := NewShare(pShare, nil, rootLink, resolver, "")
	rootLink = NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = rootLink

	return rootLink, share
}

// simulateRemoveChecks replicates the pre-API-call checks from
// Client.Remove. This tests the same logic without needing a real
// Client or Session.
func simulateRemoveChecks(ctx context.Context, link *Link, opts RemoveOpts) error {
	if link.ParentLink() == nil {
		return fmt.Errorf("remove: cannot remove share root")
	}

	if link.Type() == proton.LinkTypeFolder && !opts.Recursive {
		children, err := link.ListChildren(ctx, true)
		if err != nil {
			return fmt.Errorf("remove: listing children: %w", err)
		}
		if len(children) > 0 {
			name, _ := link.Name()
			return fmt.Errorf("remove: %s: %w", name, ErrNotEmpty)
		}
	}

	return nil
}

// TestRemoveChecks_ShareRoot_Property verifies that the Remove share-root
// check rejects any link with ParentLink() == nil, for any RemoveOpts.
// **Property 3: Remove Rejects Share Root**
// **Validates: Requirement 3.2**
func TestRemoveChecks_ShareRoot_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		recursive := rapid.Bool().Draw(t, "recursive")
		permanent := rapid.Bool().Draw(t, "permanent")

		resolver := &mockRemoveResolver{}
		rootLink, _ := makeTestShareRoot(resolver)

		err := simulateRemoveChecks(context.Background(), rootLink, RemoveOpts{
			Recursive: recursive,
			Permanent: permanent,
		})

		if err == nil {
			t.Fatal("expected error for share root, got nil")
		}
	})
}

// TestRemoveChecks_NonEmptyFolder_Property verifies that the Remove
// non-empty-folder check returns ErrNotEmpty when Recursive is false,
// for any Permanent value and any positive child count.
// **Property 4: Remove Non-Empty Folder Without Recursive**
// **Validates: Requirement 3.3**
func TestRemoveChecks_NonEmptyFolder_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		permanent := rapid.Bool().Draw(t, "permanent")
		childCount := rapid.IntRange(1, 50).Draw(t, "childCount")

		resolver := &mockRemoveResolver{childCount: childCount}
		folder := makeTestFolder(resolver, "test-folder")

		err := simulateRemoveChecks(context.Background(), folder, RemoveOpts{
			Recursive: false,
			Permanent: permanent,
		})

		if err == nil {
			t.Fatal("expected ErrNotEmpty for non-empty folder with Recursive=false, got nil")
		}
		if !errors.Is(err, ErrNotEmpty) {
			t.Fatalf("expected ErrNotEmpty, got: %v", err)
		}
	})
}

// TestRemoveChecks_EmptyFolder verifies that an empty folder passes
// the non-empty check.
func TestRemoveChecks_EmptyFolder(t *testing.T) {
	resolver := &mockRemoveResolver{childCount: 0}
	folder := makeTestFolder(resolver, "empty-folder")

	err := simulateRemoveChecks(context.Background(), folder, RemoveOpts{
		Recursive: false,
		Permanent: false,
	})

	if err != nil {
		t.Fatalf("expected nil error for empty folder, got: %v", err)
	}
}
