//go:build linux

package drive

import (
	"context"
	"errors"
	"syscall"
	"testing"

	"github.com/major0/proton-utils/api/drive"
	"github.com/major0/proton-utils/internal/fusemount"
	"pgregory.net/rapid"
)

// fakeDriveClient is a deterministic test double for the narrow driveClient
// seam. It records the mode passed to Chmod and returns a StatLink refresh
// whose Mode() reflects the recorded mode, so a FileNode.Setattr -> Getattr
// round-trip can be exercised without session/crypto infrastructure. The
// OpenFD/OverwriteFD/ReadSymlinkTarget methods are unused by the chmod tests
// and return errors if called.
type fakeDriveClient struct {
	chmodCalls int
	lastMode   uint32
	chmodErr   error // when non-nil, Chmod returns it (failure-path test)
	statErr    error // when non-nil, StatLink returns it (refresh-failure test)

	persistedMode uint32 // mode the refreshed link reports; set by a successful Chmod
}

func (f *fakeDriveClient) Chmod(_ context.Context, _ *drive.Share, _ *drive.Link, mode uint32) error {
	f.chmodCalls++
	f.lastMode = mode
	if f.chmodErr != nil {
		return f.chmodErr
	}
	f.persistedMode = mode
	return nil
}

func (f *fakeDriveClient) StatLink(_ context.Context, _ *drive.Share, _ *drive.Link, _ string) (*drive.Link, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return drive.NewTestResolvedFileLink("refreshed", 0, f.persistedMode), nil
}

func (f *fakeDriveClient) OpenFD(_ context.Context, _ *drive.Link) (*drive.FileDescriptor, error) {
	return nil, errors.New("fakeDriveClient.OpenFD: not used")
}

func (f *fakeDriveClient) OverwriteFD(_ context.Context, _ *drive.Share, _ *drive.Link) (*drive.FileDescriptor, error) {
	return nil, errors.New("fakeDriveClient.OverwriteFD: not used")
}

func (f *fakeDriveClient) ReadSymlinkTarget(_ context.Context, _ *drive.Link) (string, error) {
	return "", errors.New("fakeDriveClient.ReadSymlinkTarget: not used")
}

// TestFileNodeSetattr_ChmodPersistsAndVisible_BugCondition is the Property 1
// (Bug Condition) exploration test for the fuse-chmod-noop bugfix.
//
// **Property 1: Bug Condition — Mode Persisted And Visible Same-Process**
// **Validates: Requirements 1.1, 1.2, 2.1, 2.2, 2.6**
//
// It is a scoped rapid property: the bug is deterministic, so the domain is
// scoped to the failing case — a chmod reaching FileNode.Setattr with the
// SetattrMode bit set and NO open write FD. The mode is drawn from
// [1, 0o7777]; 0 is excluded because on this branch a stored 0 is
// indistinguishable from "unset" (the zero-vs-unset distinction was
// deliberately not adopted), so a chmod 0000 resolves to the default 0600 —
// see the fuse-chmod-noop design and the parked chmod-zero-vs-unset-mode work.
//
// On UNFIXED code the SetattrMode branch is a silent no-op: Getattr keeps
// reporting the original link's mode (default 0600) and Chmod is never called,
// so this test FAILS — that failure confirms the bug. After the fix it PASSES:
// Setattr persists the masked mode via the drive Chmod path and re-stats the
// link, so an immediately following Getattr reports S_IFREG | (mode & 0o7777).
func TestFileNodeSetattr_ChmodPersistsAndVisible_BugCondition(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mode := rapid.Uint32Range(1, 0o7777).Draw(t, "mode")

		fake := &fakeDriveClient{}
		// Original link reports mode 0 (unset) -> Getattr default 0600.
		orig := drive.NewTestResolvedFileLink("orig", 0, 0)
		node := &FileNode{link: orig, client: fake}

		// No open write FD (fh == nil): the primary chmod path.
		in := &fusemount.SetattrIn{Valid: fusemount.SetattrMode, Mode: mode}
		if errno := node.Setattr(context.Background(), nil, in); errno != 0 {
			t.Fatalf("Setattr(mode=%#o) errno=%d, want 0", mode, errno)
		}

		// Case 2: the persistence path was invoked with the masked mode.
		if fake.chmodCalls != 1 {
			t.Fatalf("Chmod calls=%d, want 1 (mode dropped on unfixed code)", fake.chmodCalls)
		}
		if fake.lastMode != mode&0o7777 {
			t.Fatalf("Chmod mode=%#o, want %#o", fake.lastMode, mode&0o7777)
		}

		// Case 1 (core): Getattr reflects the persisted mode after refresh.
		attr, errno := node.Getattr(context.Background())
		if errno != 0 {
			t.Fatalf("Getattr errno=%d, want 0", errno)
		}
		want := uint32(syscall.S_IFREG) | (mode & 0o7777)
		if attr.Mode != want {
			t.Fatalf("Getattr Mode=%#o, want %#o (mode not persisted/refreshed)", attr.Mode, want)
		}
	})
}

// TestFileNodeSetattr_ChmodInvokedWithMaskedMode is Case 2 as a focused unit
// test: a mode with high bits set is masked to 0o7777 before persistence.
//
// **Property 1 (Case 2). Validates: Requirements 2.1.**
func TestFileNodeSetattr_ChmodInvokedWithMaskedMode(t *testing.T) {
	fake := &fakeDriveClient{}
	node := &FileNode{link: drive.NewTestResolvedFileLink("orig", 0, 0), client: fake}

	// S_IFREG bits in the high part must be masked away.
	in := &fusemount.SetattrIn{Valid: fusemount.SetattrMode, Mode: syscall.S_IFREG | 0o750}
	if errno := node.Setattr(context.Background(), nil, in); errno != 0 {
		t.Fatalf("Setattr errno=%d, want 0", errno)
	}
	if fake.chmodCalls != 1 {
		t.Fatalf("Chmod calls=%d, want 1", fake.chmodCalls)
	}
	if fake.lastMode != 0o750 {
		t.Fatalf("Chmod mode=%#o, want %#o (masked to 0o7777)", fake.lastMode, 0o750)
	}
}

// TestFileNodeSetattr_ChmodFailureMapsToEIO is Case 4: a persistence error is
// mapped to an errno (EIO), never silent success (0) and never ENOSYS.
//
// **Property 4: Failure Maps To An Errno. Validates: Requirements 2.4, 2.5.**
func TestFileNodeSetattr_ChmodFailureMapsToEIO(t *testing.T) {
	fake := &fakeDriveClient{chmodErr: errors.New("backend down")}
	node := &FileNode{link: drive.NewTestResolvedFileLink("orig", 0, 0), client: fake}

	in := &fusemount.SetattrIn{Valid: fusemount.SetattrMode, Mode: 0o644}
	errno := node.Setattr(context.Background(), nil, in)
	if errno != syscall.EIO {
		t.Fatalf("Setattr errno=%d, want EIO (%d)", errno, syscall.EIO)
	}
	if errno == syscall.ENOSYS {
		t.Fatal("Setattr returned ENOSYS (go-fuse caches it per-connection)")
	}
}

// TestFileNodeSetattr_ChmodCancelMapsToEINTR verifies a cancelled/deadline
// persistence maps to EINTR (not EIO, not 0, not ENOSYS).
//
// **Property 4. Validates: Requirements 2.4, 2.5.**
func TestFileNodeSetattr_ChmodCancelMapsToEINTR(t *testing.T) {
	fake := &fakeDriveClient{chmodErr: context.Canceled}
	node := &FileNode{link: drive.NewTestResolvedFileLink("orig", 0, 0), client: fake}

	in := &fusemount.SetattrIn{Valid: fusemount.SetattrMode, Mode: 0o644}
	if errno := node.Setattr(context.Background(), nil, in); errno != syscall.EINTR {
		t.Fatalf("Setattr errno=%d, want EINTR (%d)", errno, syscall.EINTR)
	}
}

// TestFileNodeSetattr_RefreshFailureStillSucceeds verifies that when the mode
// persists but the follow-up re-stat fails, Setattr still returns 0 (the mode
// is already committed server-side; a failed refresh is a transient coherence
// miss, not a persistence failure).
//
// **Validates: Requirements 2.1, 2.2 (refresh-failure policy).**
func TestFileNodeSetattr_RefreshFailureStillSucceeds(t *testing.T) {
	fake := &fakeDriveClient{statErr: errors.New("stat failed")}
	node := &FileNode{link: drive.NewTestResolvedFileLink("orig", 0, 0), client: fake}

	in := &fusemount.SetattrIn{Valid: fusemount.SetattrMode, Mode: 0o644}
	if errno := node.Setattr(context.Background(), nil, in); errno != 0 {
		t.Fatalf("Setattr errno=%d, want 0 (mode persisted; refresh miss is non-fatal)", errno)
	}
	if fake.chmodCalls != 1 {
		t.Fatalf("Chmod calls=%d, want 1", fake.chmodCalls)
	}
}

// TestFileNodeSetattr_WriteFDStagesMode verifies Case 3 (staging half): with an
// open write FD, a mode change is staged on the descriptor (committed with any
// pending truncate on the one revision) rather than persisted via a standalone
// Chmod — a second overwrite would return EBUSY. Observable at the seam: Chmod
// is NOT called and Setattr returns 0.
//
// **Property 3: Combined Mode And Size. Validates: Requirements 2.3.**
func TestFileNodeSetattr_WriteFDStagesMode(t *testing.T) {
	fake := &fakeDriveClient{}
	node := &FileNode{link: drive.NewTestResolvedFileLink("orig", 0, 0), client: fake}

	// A read-mode test FD is sufficient: SetMode only records the pending
	// mode on the descriptor; the write flag routes Setattr to the staging
	// branch. (Truncate-via-write-FD is covered by integration tests.)
	fd, err := drive.NewTestFD([]byte("content"))
	if err != nil {
		t.Fatalf("NewTestFD: %v", err)
	}
	h := &fdHandle{fd: fd, write: true}

	in := &fusemount.SetattrIn{Valid: fusemount.SetattrMode, Mode: 0o640}
	if errno := node.Setattr(context.Background(), h, in); errno != 0 {
		t.Fatalf("Setattr errno=%d, want 0", errno)
	}
	if fake.chmodCalls != 0 {
		t.Fatalf("Chmod calls=%d, want 0 (mode should be staged on the open write FD)", fake.chmodCalls)
	}
}

// --- Property 2: Preservation — Non-Mode Setattr Unchanged ---

// TestFileNodeSetattr_TruncateOnlyNoWriteFD_Preservation verifies that a
// truncate-only setattr with no write FD stays a no-op success (returns 0),
// exactly as on unfixed code.
//
// **Property 2: Preservation. Validates: Requirements 3.2.**
func TestFileNodeSetattr_TruncateOnlyNoWriteFD_Preservation(t *testing.T) {
	fake := &fakeDriveClient{}
	node := &FileNode{link: drive.NewTestResolvedFileLink("orig", 10, 0), client: fake}

	in := &fusemount.SetattrIn{Valid: fusemount.SetattrSize, Size: 0}
	if errno := node.Setattr(context.Background(), nil, in); errno != 0 {
		t.Fatalf("Setattr(size, no write FD) errno=%d, want 0 (no-op)", errno)
	}
	if fake.chmodCalls != 0 {
		t.Fatalf("Chmod calls=%d, want 0 (size-only must not persist mode)", fake.chmodCalls)
	}
}

// TestFileNodeGetattr_ModeMaskingAndDefault_Preservation verifies that Getattr
// reports link.Mode() masked to 0o7777, defaulting to 0600 when Mode() is 0.
// This is unchanged by the fix and holds across the full permission range.
//
// **Property 2: Preservation. Validates: Requirements 3.3.**
func TestFileNodeGetattr_ModeMaskingAndDefault_Preservation(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mode := rapid.Uint32Range(0, 0o7777).Draw(t, "mode")
		node := &FileNode{link: drive.NewTestResolvedFileLink("f", 0, mode)}

		attr, errno := node.Getattr(context.Background())
		if errno != 0 {
			t.Fatalf("Getattr errno=%d", errno)
		}
		want := uint32(syscall.S_IFREG) | 0o600 // default when Mode() == 0
		if mode != 0 {
			want = uint32(syscall.S_IFREG) | (mode & 0o7777)
		}
		if attr.Mode != want {
			t.Fatalf("Getattr Mode=%#o, want %#o (mode=%#o)", attr.Mode, want, mode)
		}
	})
}

// TestFileNodeSetattr_NeverReturnsENOSYS_Preservation verifies no setattr field
// combination reaching FileNode.Setattr returns ENOSYS (go-fuse caches it
// per-connection, which would disable setattr filesystem-wide).
//
// **Property 2: Preservation. Validates: Requirements 3.4 (2.5).**
func TestFileNodeSetattr_NeverReturnsENOSYS_Preservation(t *testing.T) {
	cases := []fusemount.SetattrIn{
		{Valid: 0},
		{Valid: fusemount.SetattrMtime, Mtime: 123},
		{Valid: fusemount.SetattrSize, Size: 0},
		{Valid: fusemount.SetattrMtime | fusemount.SetattrSize, Size: 0, Mtime: 123},
	}
	for _, in := range cases {
		node := &FileNode{link: drive.NewTestResolvedFileLink("f", 0, 0), client: &fakeDriveClient{}}
		in := in
		if errno := node.Setattr(context.Background(), nil, &in); errno == syscall.ENOSYS {
			t.Fatalf("Setattr(Valid=%#b) returned ENOSYS", in.Valid)
		}
	}
}
