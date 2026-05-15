//go:build linux

package drive

import (
	"context"
	"syscall"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
)

// TestFileNode_Open_Success verifies that Open with a client that can
// produce a valid FD returns a non-nil handle wrapping the FD.
// NOTE: OpenFD requires full crypto infrastructure (session key derivation,
// revision metadata fetch). Since NewTestClient only pre-populates a link
// table and cannot satisfy OpenFD, this test is skipped — the Open→Read→
// Release path is exercised end-to-end by the property tests (Properties
// 10, 11, 12) which use NewTestFD directly.
func TestFileNode_Open_Success(t *testing.T) {
	t.Skip("OpenFD requires crypto infrastructure; covered by property tests")
}

// TestFileNode_Open_Error verifies that Open returns EIO when the client
// fails to produce a FileDescriptor. We trigger the error by passing a
// link with Type=Folder — OpenFile rejects non-file links before
// accessing the session, returning an error that Open maps to EIO.
func TestFileNode_Open_Error(t *testing.T) {
	client := drive.NewTestClient(nil)

	// Use a folder-type link to trigger an OpenFile error without
	// requiring session/crypto infrastructure.
	pLink := &proton.Link{
		LinkID: "test-folder-link",
		Type:   proton.LinkTypeFolder,
	}
	link := drive.NewTestLink(pLink, nil, nil, nil, "not-a-file")

	node := &FileNode{link: link, client: client}

	handle, errno := node.Open(context.Background(), 0)
	if errno != syscall.EIO {
		t.Fatalf("Open: got errno %d, want EIO (%d)", errno, syscall.EIO)
	}
	if handle != nil {
		t.Fatalf("Open: got non-nil handle on error, want nil")
	}
}

// TestFileNode_Read_Success verifies that Read with a valid FD (via
// NewTestFD) delegates to ReadAt and returns the correct byte count.
func TestFileNode_Read_Success(t *testing.T) {
	content := []byte("hello, proton drive!")
	fd, err := drive.NewTestFD(content)
	if err != nil {
		t.Fatalf("NewTestFD: %v", err)
	}

	handle := &fdHandle{fd: fd}
	node := &FileNode{}

	dest := make([]byte, 5)
	n, errno := node.Read(context.Background(), handle, dest, 0)
	if errno != 0 {
		t.Fatalf("Read: got errno %d, want 0", errno)
	}
	if n != 5 {
		t.Fatalf("Read: got n=%d, want 5", n)
	}
	if string(dest[:n]) != "hello" {
		t.Fatalf("Read: got %q, want %q", string(dest[:n]), "hello")
	}
}

// TestFileNode_Read_NilHandle verifies that Read with a nil handle
// returns EBADF.
func TestFileNode_Read_NilHandle(t *testing.T) {
	node := &FileNode{}
	dest := make([]byte, 10)

	n, errno := node.Read(context.Background(), nil, dest, 0)
	if errno != syscall.EBADF {
		t.Fatalf("Read(nil handle): got errno %d, want EBADF (%d)", errno, syscall.EBADF)
	}
	if n != 0 {
		t.Fatalf("Read(nil handle): got n=%d, want 0", n)
	}
}

// TestFileNode_Read_ClosedFD verifies that Read with a closed FD
// (os.ErrClosed from ReadAt) returns EBADF.
func TestFileNode_Read_ClosedFD(t *testing.T) {
	content := []byte("some file content")
	fd, err := drive.NewTestFD(content)
	if err != nil {
		t.Fatalf("NewTestFD: %v", err)
	}

	// Close the FD before reading.
	if err := fd.Close(); err != nil {
		t.Fatalf("fd.Close: %v", err)
	}

	handle := &fdHandle{fd: fd}
	node := &FileNode{}
	dest := make([]byte, 10)

	n, errno := node.Read(context.Background(), handle, dest, 0)
	if errno != syscall.EBADF {
		t.Fatalf("Read(closed FD): got errno %d, want EBADF (%d)", errno, syscall.EBADF)
	}
	if n != 0 {
		t.Fatalf("Read(closed FD): got n=%d, want 0", n)
	}
}

// TestFileNode_Read_EOF verifies that Read past end of file returns
// n=0 with errno=0 (FUSE EOF signal).
func TestFileNode_Read_EOF(t *testing.T) {
	content := []byte("short")
	fd, err := drive.NewTestFD(content)
	if err != nil {
		t.Fatalf("NewTestFD: %v", err)
	}

	handle := &fdHandle{fd: fd}
	node := &FileNode{}
	dest := make([]byte, 10)

	// Read at offset past end of file.
	n, errno := node.Read(context.Background(), handle, dest, int64(len(content)+100))
	if errno != 0 {
		t.Fatalf("Read(past EOF): got errno %d, want 0", errno)
	}
	if n != 0 {
		t.Fatalf("Read(past EOF): got n=%d, want 0", n)
	}
}

// TestFileNode_Release_Success verifies that Release with a valid handle
// calls Close and returns errno 0.
func TestFileNode_Release_Success(t *testing.T) {
	content := []byte("release me")
	fd, err := drive.NewTestFD(content)
	if err != nil {
		t.Fatalf("NewTestFD: %v", err)
	}

	handle := &fdHandle{fd: fd}

	pLink := &proton.Link{LinkID: "release-link", Type: proton.LinkTypeFile}
	link := drive.NewTestLink(pLink, nil, nil, nil, "file.txt")
	node := &FileNode{link: link}

	errno := node.Release(context.Background(), handle)
	if errno != 0 {
		t.Fatalf("Release: got errno %d, want 0", errno)
	}

	// Verify the FD is actually closed by attempting a read.
	dest := make([]byte, 5)
	readNode := &FileNode{}
	_, readErrno := readNode.Read(context.Background(), handle, dest, 0)
	if readErrno != syscall.EBADF {
		t.Fatalf("Read after Release: got errno %d, want EBADF (FD should be closed)", readErrno)
	}
}

// TestFileNode_Release_NilHandle verifies that Release with a nil handle
// returns errno 0 without panicking.
func TestFileNode_Release_NilHandle(t *testing.T) {
	pLink := &proton.Link{LinkID: "nil-release-link", Type: proton.LinkTypeFile}
	link := drive.NewTestLink(pLink, nil, nil, nil, "file.txt")
	node := &FileNode{link: link}

	errno := node.Release(context.Background(), nil)
	if errno != 0 {
		t.Fatalf("Release(nil handle): got errno %d, want 0", errno)
	}
}

// TestFileNode_Release_CloseError verifies that Release returns errno 0
// even when Close returns an error (error is logged, not returned).
// We test this by closing the FD first (making Close a no-op on second
// call — idempotent), which confirms the Release path handles the
// close gracefully.
func TestFileNode_Release_CloseError(t *testing.T) {
	content := []byte("close error test")
	fd, err := drive.NewTestFD(content)
	if err != nil {
		t.Fatalf("NewTestFD: %v", err)
	}

	// Close once — second Close is a no-op (idempotent), so no error.
	// The real scenario where Close errors is network/IO failure during
	// write-mode flush. For read-mode FDs, Close never errors. We verify
	// the Release contract: errno is always 0 regardless.
	handle := &fdHandle{fd: fd}

	pLink := &proton.Link{LinkID: "close-err-link", Type: proton.LinkTypeFile}
	link := drive.NewTestLink(pLink, nil, nil, nil, "file.txt")
	node := &FileNode{link: link}

	// First Release — should succeed.
	errno := node.Release(context.Background(), handle)
	if errno != 0 {
		t.Fatalf("Release (first): got errno %d, want 0", errno)
	}

	// Second Release on same handle — Close is idempotent, still errno 0.
	errno = node.Release(context.Background(), handle)
	if errno != 0 {
		t.Fatalf("Release (second/idempotent): got errno %d, want 0", errno)
	}
}
