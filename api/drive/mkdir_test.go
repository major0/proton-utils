package drive_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
)

// TestMkDir_ParentNotFolder verifies that MkDir returns ErrNotAFolder
// when the parent link is a file.
func TestMkDir_ParentNotFolder(t *testing.T) {
	resolver := &mockResolver{}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "s1"},
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	rootLink := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := drive.NewShare(pShare, nil, rootLink, resolver, "")
	rootLink = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = rootLink

	// Create a file link as the "parent".
	filePLink := &proton.Link{LinkID: "file-1", Type: proton.LinkTypeFile}
	fileLink := drive.NewTestLink(filePLink, rootLink, share, resolver, "readme.txt")

	c := &drive.Client{}
	_, err := c.MkDir(context.Background(), share, fileLink, "subdir")
	if err == nil {
		t.Fatal("expected error for file parent, got nil")
	}
	if !errors.Is(err, drive.ErrNotAFolder) {
		t.Fatalf("expected ErrNotAFolder, got: %v", err)
	}
}

// TestMkDirAll_EmptyPath verifies that MkDirAll with an empty path
// returns the root link unchanged.
func TestMkDirAll_EmptyPath(t *testing.T) {
	resolver := &mockResolver{}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "s1"},
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	rootLink := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := drive.NewShare(pShare, nil, rootLink, resolver, "")
	rootLink = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = rootLink

	c := &drive.Client{}
	result, err := c.MkDirAll(context.Background(), share, rootLink, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != rootLink {
		t.Fatal("expected root link returned for empty path")
	}
}

// TestMkDirAll_SlashOnlyPath verifies that MkDirAll with "/" returns
// the root link unchanged.
func TestMkDirAll_SlashOnlyPath(t *testing.T) {
	resolver := &mockResolver{}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "s1"},
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	rootLink := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := drive.NewShare(pShare, nil, rootLink, resolver, "")
	rootLink = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = rootLink

	c := &drive.Client{}
	result, err := c.MkDirAll(context.Background(), share, rootLink, "/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != rootLink {
		t.Fatal("expected root link returned for slash-only path")
	}
}

// TestMkDirAll_FileBlocksPath verifies that MkDirAll returns
// ErrNotAFolder when an intermediate component is a file.
func TestMkDirAll_FileBlocksPath(t *testing.T) {
	// mockResolverWithChildren returns a file child named "readme"
	// so MkDirAll("readme/subdir") hits the "not a folder" check.
	resolver := &mockResolverWithChildren{
		children: []proton.Link{
			{LinkID: "file-1", Type: proton.LinkTypeFile},
		},
	}

	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "s1"},
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	rootLink := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := drive.NewShare(pShare, nil, rootLink, resolver, "")
	rootLink = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = rootLink

	c := &drive.Client{}
	_, err := c.MkDirAll(context.Background(), share, rootLink, "file-1/subdir")
	if err == nil {
		t.Fatal("expected error for file in path, got nil")
	}
	if !errors.Is(err, drive.ErrNotAFolder) {
		t.Fatalf("expected ErrNotAFolder, got: %v", err)
	}
}
