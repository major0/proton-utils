package drive_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
)

// TestMove_NewParentNotFolder verifies that Move returns ErrNotAFolder
// when the destination parent is a file link.
func TestMove_NewParentNotFolder(t *testing.T) {
	resolver := &mockResolver{}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: "s1"},
	}
	rootPLink := &proton.Link{LinkID: "root", Type: proton.LinkTypeFolder}
	rootLink := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
	share := drive.NewShare(pShare, nil, rootLink, resolver, "")
	rootLink = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
	share.Link = rootLink

	// Source: a file in root.
	srcPLink := &proton.Link{LinkID: "src-file", Type: proton.LinkTypeFile}
	srcLink := drive.NewTestLink(srcPLink, rootLink, share, resolver, "source.txt")

	// Destination parent: also a file (invalid).
	dstPLink := &proton.Link{LinkID: "dst-file", Type: proton.LinkTypeFile}
	dstLink := drive.NewTestLink(dstPLink, rootLink, share, resolver, "dest.txt")

	c := &drive.Client{}
	err := c.Move(context.Background(), share, srcLink, dstLink, "newname.txt")
	if err == nil {
		t.Fatal("expected error for file destination parent, got nil")
	}
	if !errors.Is(err, drive.ErrNotAFolder) {
		t.Fatalf("expected ErrNotAFolder, got: %v", err)
	}
}

// TestRename_ShareRoot verifies that Rename rejects share root links
// (links with no parent).
func TestRename_ShareRoot(t *testing.T) {
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
	err := c.Rename(context.Background(), share, rootLink, "new-name")
	if err == nil {
		t.Fatal("expected error for share root rename, got nil")
	}
	if !strings.Contains(err.Error(), "cannot rename share root") {
		t.Fatalf("unexpected error: %v", err)
	}
}
