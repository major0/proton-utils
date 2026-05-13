package driveCmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
)

// TestMvCommandRegistration verifies that the mv command is registered
// with the correct use, aliases, and minimum args.
func TestMvCommandRegistration(t *testing.T) {
	if driveMvCmd.Use == "" {
		t.Fatal("mv command has empty Use")
	}
	if !driveMvCmd.HasAlias("rename") {
		t.Error("mv command missing 'rename' alias")
	}
	if driveMvCmd.Args == nil {
		t.Fatal("mv command has no Args validator")
	}
	if err := driveMvCmd.Args(driveMvCmd, []string{"src"}); err == nil {
		t.Error("mv accepted 1 arg, want minimum 2")
	}
	if err := driveMvCmd.Args(driveMvCmd, []string{"src", "dst"}); err != nil {
		t.Errorf("mv rejected 2 args: %v", err)
	}
	if err := driveMvCmd.Args(driveMvCmd, []string{"a", "b", "c"}); err != nil {
		t.Errorf("mv rejected 3 args: %v", err)
	}
}

// TestMvVerboseFlag verifies the --verbose / -v flag is registered.
func TestMvVerboseFlag(t *testing.T) {
	f := driveMvCmd.Flags().Lookup("verbose")
	if f == nil {
		t.Fatal("--verbose flag not registered on mv command")
	}
	if f.Shorthand != "v" {
		t.Errorf("verbose shorthand = %q, want 'v'", f.Shorthand)
	}
}

// TestDoMove_CrossVolume verifies that doMove returns an error when
// source and destination are on different volumes.
func TestDoMove_CrossVolume(t *testing.T) {
	resolver := &testResolver{}

	// Source on volume "vol-1".
	srcShare := makeTestShareWithVolume(resolver, "src-share", "vol-1")
	srcPLink := &proton.Link{LinkID: "src-link", Type: proton.LinkTypeFile}
	srcLink := drive.NewTestLink(srcPLink, srcShare.Link, srcShare, resolver, "file.txt")

	// Destination parent on volume "vol-2".
	dstShare := makeTestShareWithVolume(resolver, "dst-share", "vol-2")
	dstPLink := &proton.Link{LinkID: "dst-dir", Type: proton.LinkTypeFolder}
	dstLink := drive.NewTestLink(dstPLink, dstShare.Link, dstShare, resolver, "dest")

	err := doMove(context.Background(), nil, srcShare, srcLink, dstShare, dstLink, "file.txt")
	if err == nil {
		t.Fatal("expected cross-volume error, got nil")
	}
	if !strings.Contains(err.Error(), "cross-volume") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDoMove_SameVolume verifies that doMove proceeds past the volume
// check when source and destination are on the same volume (then fails
// at the nil client).
func TestDoMove_SameVolume(t *testing.T) {
	resolver := &testResolver{}

	share := makeTestShareWithVolume(resolver, "share", "vol-1")
	srcPLink := &proton.Link{LinkID: "src", Type: proton.LinkTypeFile}
	srcLink := drive.NewTestLink(srcPLink, share.Link, share, resolver, "file.txt")

	dstPLink := &proton.Link{LinkID: "dst", Type: proton.LinkTypeFolder}
	dstLink := drive.NewTestLink(dstPLink, share.Link, share, resolver, "dest")

	// Same volume — passes the cross-volume check, then fails at dc.Move (nil client).
	err := doMove(context.Background(), nil, share, srcLink, share, dstLink, "file.txt")
	if err == nil {
		t.Fatal("expected error from nil client, got nil")
	}
	// The error should NOT be about cross-volume.
	if strings.Contains(err.Error(), "cross-volume") {
		t.Fatalf("same-volume move should not get cross-volume error: %v", err)
	}
}

// makeTestShareWithVolume creates a minimal Share with a given volume ID for testing.
func makeTestShareWithVolume(resolver drive.LinkResolver, name, volumeID string) *drive.Share {
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{ShareID: fmt.Sprintf("share-%s", name)},
	}
	rootPLink := &proton.Link{LinkID: fmt.Sprintf("root-%s", name), Type: proton.LinkTypeFolder}
	rootLink := drive.NewTestLink(rootPLink, nil, nil, resolver, name)
	share := drive.NewShare(pShare, nil, rootLink, resolver, volumeID)
	rootLink = drive.NewTestLink(rootPLink, nil, share, resolver, name)
	share.Link = rootLink
	return share
}
