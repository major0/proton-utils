//go:build linux

package drive

import (
	"context"
	"errors"
	"syscall"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
	"github.com/major0/proton-utils/internal/fusemount"
)

// buildTestHandler creates a DriveHandler with pre-populated shares for testing.
// Bypasses LoadShares (which requires a real client) by directly setting the map.
func buildTestHandler(shares map[string]*drive.Share) *DriveHandler {
	return &DriveHandler{
		shares: shares,
	}
}

// testShare creates a *drive.Share with a test name for use in handler tests.
func testShare(name string, shareID string, st proton.ShareType) *drive.Share {
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{
			ShareID: shareID,
			Type:    st,
		},
	}
	pLink := &proton.Link{LinkID: "link-" + shareID, Type: proton.LinkTypeFolder}
	root := drive.NewTestLink(pLink, nil, nil, nil, name)
	share := drive.NewShare(pShare, nil, root, nil, "vol-1")
	return share
}

func TestDriveHandler_Getattr(t *testing.T) {
	h := buildTestHandler(nil)
	attr, errno := h.Getattr(context.Background())
	if errno != 0 {
		t.Fatalf("Getattr returned errno %d", errno)
	}
	if attr.Mode != syscall.S_IFDIR|0500 {
		t.Errorf("Mode = %o, want %o", attr.Mode, syscall.S_IFDIR|0500)
	}
	if attr.Nlink != 2 {
		t.Errorf("Nlink = %d, want 2", attr.Nlink)
	}
}

func TestDriveHandler_Readdir_AllShareTypes(t *testing.T) {
	shares := map[string]*drive.Share{
		"main-id":     testShare("root", "main-id", proton.ShareTypeMain),
		"photos-id":   testShare("photos", "photos-id", drive.ShareTypePhotos),
		"standard-id": testShare("MyFolder", "standard-id", proton.ShareTypeStandard),
		"device-id":   testShare("device", "device-id", proton.ShareTypeDevice),
	}

	h := buildTestHandler(shares)
	entries, errno := h.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir returned errno %d", errno)
	}

	// Expect: Home, Photos, MyFolder, .linkid (device excluded)
	nameSet := make(map[string]bool)
	for _, e := range entries {
		nameSet[e.Name] = true
		if e.Mode != syscall.S_IFDIR {
			t.Errorf("entry %q has mode %o, want S_IFDIR", e.Name, e.Mode)
		}
	}

	for _, expected := range []string{"Home", "Photos", "MyFolder", ".linkid"} {
		if !nameSet[expected] {
			t.Errorf("missing expected entry %q", expected)
		}
	}
	if nameSet["device"] {
		t.Error("device share should not appear in listing")
	}
	// Total: 4 entries (Home + Photos + MyFolder + .linkid)
	if len(entries) != 4 {
		t.Errorf("got %d entries, want 4", len(entries))
	}
}

func TestDriveHandler_Readdir_NoPhotos(t *testing.T) {
	shares := map[string]*drive.Share{
		"main-id": testShare("root", "main-id", proton.ShareTypeMain),
	}

	h := buildTestHandler(shares)
	entries, errno := h.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir returned errno %d", errno)
	}

	// Expect: Home, .linkid
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
}

func TestDriveHandler_Lookup_Home(t *testing.T) {
	shares := map[string]*drive.Share{
		"main-id": testShare("root", "main-id", proton.ShareTypeMain),
	}

	h := buildTestHandler(shares)
	node, errno := h.Lookup(context.Background(), "Home")
	if errno != 0 {
		t.Fatalf("Lookup(Home) returned errno %d", errno)
	}
	if node == nil {
		t.Fatal("Lookup(Home) returned nil node")
	}
	if _, ok := node.(*ShareDirNode); !ok {
		t.Errorf("Lookup(Home) returned %T, want *ShareDirNode", node)
	}
}

func TestDriveHandler_Lookup_Photos(t *testing.T) {
	shares := map[string]*drive.Share{
		"photos-id": testShare("photos", "photos-id", drive.ShareTypePhotos),
	}

	h := buildTestHandler(shares)
	node, errno := h.Lookup(context.Background(), "Photos")
	if errno != 0 {
		t.Fatalf("Lookup(Photos) returned errno %d", errno)
	}
	if _, ok := node.(*ShareDirNode); !ok {
		t.Errorf("Lookup(Photos) returned %T, want *ShareDirNode", node)
	}
}

func TestDriveHandler_Lookup_PhotosNotPresent(t *testing.T) {
	shares := map[string]*drive.Share{
		"main-id": testShare("root", "main-id", proton.ShareTypeMain),
	}

	h := buildTestHandler(shares)
	_, errno := h.Lookup(context.Background(), "Photos")
	if errno != syscall.ENOENT {
		t.Errorf("Lookup(Photos) errno = %d, want ENOENT (%d)", errno, syscall.ENOENT)
	}
}

func TestDriveHandler_Lookup_LinkID(t *testing.T) {
	h := buildTestHandler(map[string]*drive.Share{})
	node, errno := h.Lookup(context.Background(), ".linkid")
	if errno != 0 {
		t.Fatalf("Lookup(.linkid) returned errno %d", errno)
	}
	if _, ok := node.(*LinkIDDir); !ok {
		t.Errorf("Lookup(.linkid) returned %T, want *LinkIDDir", node)
	}
}

func TestDriveHandler_Lookup_StandardShare(t *testing.T) {
	shares := map[string]*drive.Share{
		"std-id": testShare("MyFolder", "std-id", proton.ShareTypeStandard),
	}

	h := buildTestHandler(shares)
	node, errno := h.Lookup(context.Background(), "MyFolder")
	if errno != 0 {
		t.Fatalf("Lookup(MyFolder) returned errno %d", errno)
	}
	if _, ok := node.(*ShareDirNode); !ok {
		t.Errorf("Lookup(MyFolder) returned %T, want *ShareDirNode", node)
	}
}

func TestDriveHandler_Lookup_Unknown(t *testing.T) {
	h := buildTestHandler(map[string]*drive.Share{})
	_, errno := h.Lookup(context.Background(), "nonexistent")
	if errno != syscall.ENOENT {
		t.Errorf("Lookup(nonexistent) errno = %d, want ENOENT (%d)", errno, syscall.ENOENT)
	}
}

func TestDriveHandler_ImplementsNamespaceHandler(_ *testing.T) {
	// Compile-time check is in handler.go, but verify at runtime too.
	var _ fusemount.NamespaceHandler = (*DriveHandler)(nil)
}

func TestApiErrno_ContextCanceled(t *testing.T) {
	errno := apiErrno(context.Canceled)
	if errno != syscall.EINTR {
		t.Errorf("apiErrno(context.Canceled) = %d, want EINTR (%d)", errno, syscall.EINTR)
	}
}

func TestApiErrno_DeadlineExceeded(t *testing.T) {
	errno := apiErrno(context.DeadlineExceeded)
	if errno != syscall.EINTR {
		t.Errorf("apiErrno(context.DeadlineExceeded) = %d, want EINTR (%d)", errno, syscall.EINTR)
	}
}

func TestApiErrno_OtherError(t *testing.T) {
	errno := apiErrno(errors.New("network failure"))
	if errno != syscall.EIO {
		t.Errorf("apiErrno(other) = %d, want EIO (%d)", errno, syscall.EIO)
	}
}
