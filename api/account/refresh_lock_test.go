package account

import "testing"

// TestAcquireRefreshLockNoRuntimeDirIsNoop verifies that when
// $XDG_RUNTIME_DIR is unset the lock degrades to a usable no-op: acquire
// succeeds without touching the filesystem, the returned lock holds no file
// handle, and release is safe to call repeatedly (idempotent).
//
// Covers Requirement 1.6 (best-effort degrade when $XDG_RUNTIME_DIR is unset)
// and the idempotent-release contract of refreshLock.release.
func TestAcquireRefreshLockNoRuntimeDirIsNoop(t *testing.T) {
	// Setting to empty makes os.Getenv report it as unset (Getenv returns ""
	// for both unset and empty), driving the no-op path.
	t.Setenv("XDG_RUNTIME_DIR", "")

	lock, err := acquireRefreshLock("some-uid")
	if err != nil {
		t.Fatalf("acquireRefreshLock with unset XDG_RUNTIME_DIR: unexpected error: %v", err)
	}
	if lock == nil {
		t.Fatal("acquireRefreshLock returned nil lock; want a usable no-op lock")
	}
	if lock.f != nil {
		t.Errorf("no-op lock holds a file handle (f != nil); want no filesystem interaction")
	}

	// release must be safe and idempotent on the no-op lock.
	lock.release()
	lock.release()

	// release on a nil lock must also be a safe no-op.
	var nilLock *refreshLock
	nilLock.release()
}
