package drive_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
	"github.com/major0/proton-utils/api/drive"
	"pgregory.net/rapid"
)

// mockResolver is a minimal LinkResolver for testing Remove logic
// without real API calls.
type mockResolver struct{}

func (m *mockResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	return nil, nil
}

func (m *mockResolver) NewChildLink(_ context.Context, parent *drive.Link, pLink *proton.Link) *drive.Link {
	return drive.NewLink(pLink, parent, parent.Share(), m)
}

func (m *mockResolver) GetLink(_ string) *drive.Link { return nil }

func (m *mockResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}

func (m *mockResolver) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}

func (m *mockResolver) Throttle() *api.Throttle { return nil }
func (m *mockResolver) MaxWorkers() int         { return 1 }

// TestRemove_ShareRoot_Property verifies that Remove rejects share root
// links for any RemoveOpts combination.
// **Property 3: Remove Rejects Share Root**
// **Validates: Requirement 3.2**
func TestRemove_ShareRoot_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		recursive := rapid.Bool().Draw(t, "recursive")
		permanent := rapid.Bool().Draw(t, "permanent")

		resolver := &mockResolver{}
		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
		}
		rootPLink := &proton.Link{LinkID: "root-link", Type: proton.LinkTypeFolder}
		rootLink := drive.NewLink(rootPLink, nil, nil, resolver)
		share := drive.NewShare(pShare, nil, rootLink, resolver, "")

		// Share root link: ParentLink() == nil.
		linkPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
		link := drive.NewLink(linkPLink, nil, share, resolver)

		// Client with nil Session — Remove returns before accessing it.
		c := &drive.Client{}

		err := c.Remove(context.Background(), share, link, drive.RemoveOpts{
			Recursive: recursive,
			Permanent: permanent,
		})

		if err == nil {
			t.Fatal("expected error for share root, got nil")
		}
	})
}

// mockResolverWithChildren returns predetermined children for testing
// the non-empty folder check in Remove.
type mockResolverWithChildren struct {
	children []proton.Link
}

func (m *mockResolverWithChildren) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	return m.children, nil
}

func (m *mockResolverWithChildren) NewChildLink(_ context.Context, parent *drive.Link, pLink *proton.Link) *drive.Link {
	return drive.NewTestLink(pLink, parent, parent.Share(), m, pLink.LinkID)
}

func (m *mockResolverWithChildren) GetLink(_ string) *drive.Link { return nil }

func (m *mockResolverWithChildren) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}

func (m *mockResolverWithChildren) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}

func (m *mockResolverWithChildren) Throttle() *api.Throttle { return nil }
func (m *mockResolverWithChildren) MaxWorkers() int         { return 1 }

// TestRemove_NonRecursiveNonEmpty verifies that Remove returns ErrNotEmpty
// for a non-empty folder when Recursive is false.
func TestRemove_NonRecursiveNonEmpty(t *testing.T) {
	resolver := &mockResolverWithChildren{
		children: []proton.Link{
			{LinkID: "child-1", Type: proton.LinkTypeFile},
		},
	}

	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
	}
	rootPLink := &proton.Link{LinkID: "root-link", Type: proton.LinkTypeFolder}
	rootLink := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := drive.NewShare(pShare, nil, rootLink, resolver, "")
	rootLink = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = rootLink

	// Create a folder link with a parent (not share root).
	folderPLink := &proton.Link{LinkID: "folder-1", Type: proton.LinkTypeFolder}
	folder := drive.NewTestLink(folderPLink, rootLink, share, resolver, "my-folder")

	c := &drive.Client{}
	err := c.Remove(context.Background(), share, folder, drive.RemoveOpts{
		Recursive: false,
		Permanent: false,
	})

	if err == nil {
		t.Fatal("expected error for non-empty folder, got nil")
	}
	if !errors.Is(err, drive.ErrNotEmpty) {
		t.Fatalf("expected ErrNotEmpty, got: %v", err)
	}
}

// TestRemove_EmptyFolderNonRecursive verifies that Remove proceeds past
// the non-empty check for an empty folder (then fails at session access).
func TestRemove_EmptyFolderNonRecursive(t *testing.T) {
	resolver := &mockResolver{} // returns no children

	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
	}
	rootPLink := &proton.Link{LinkID: "root-link", Type: proton.LinkTypeFolder}
	rootLink := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := drive.NewShare(pShare, nil, rootLink, resolver, "")
	rootLink = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = rootLink

	folderPLink := &proton.Link{LinkID: "folder-1", Type: proton.LinkTypeFolder}
	folder := drive.NewTestLink(folderPLink, rootLink, share, resolver, "folder")

	c := &drive.Client{}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic from nil session, got none")
			}
		}()
		_ = c.Remove(context.Background(), share, folder, drive.RemoveOpts{
			Recursive: false,
			Permanent: false,
		})
	}()
}

// TestRemove_FileLink verifies that Remove skips the children check for
// file links (then fails at session access).
func TestRemove_FileLink(t *testing.T) {
	resolver := &mockResolver{}

	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
	}
	rootPLink := &proton.Link{LinkID: "root-link", Type: proton.LinkTypeFolder}
	rootLink := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := drive.NewShare(pShare, nil, rootLink, resolver, "")
	rootLink = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = rootLink

	filePLink := &proton.Link{LinkID: "file-1", Type: proton.LinkTypeFile}
	file := drive.NewTestLink(filePLink, rootLink, share, resolver, "file")

	c := &drive.Client{}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic from nil session, got none")
			}
		}()
		_ = c.Remove(context.Background(), share, file, drive.RemoveOpts{
			Recursive: false,
			Permanent: false,
		})
	}()
}

// TestRemove_RecursiveSkipsChildrenCheck verifies that Remove with
// Recursive=true skips the children check (then fails at session access).
func TestRemove_RecursiveSkipsChildrenCheck(t *testing.T) {
	resolver := &mockResolverWithChildren{
		children: []proton.Link{
			{LinkID: "child-1", Type: proton.LinkTypeFile},
		},
	}

	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
	}
	rootPLink := &proton.Link{LinkID: "root-link", Type: proton.LinkTypeFolder}
	rootLink := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := drive.NewShare(pShare, nil, rootLink, resolver, "")
	rootLink = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = rootLink

	folderPLink := &proton.Link{LinkID: "folder-1", Type: proton.LinkTypeFolder}
	folder := drive.NewTestLink(folderPLink, rootLink, share, resolver, "folder")

	c := &drive.Client{}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic from nil session")
			}
		}()
		_ = c.Remove(context.Background(), share, folder, drive.RemoveOpts{
			Recursive: true,
			Permanent: false,
		})
	}()
}
