//go:build integration

package redirector

import (
	"os"
	"os/user"
	"syscall"
	"testing"
)

func skipIfNoFuse(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skip("skipping: /dev/fuse not available")
	}
}

func skipIfNotRoot(t *testing.T) {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Skipf("skipping: cannot determine current user: %v", err)
	}
	if u.Uid != "0" {
		t.Skip("skipping: requires root or CAP_SYS_ADMIN for allow_other")
	}
}

func TestIntegration_RedirectorMountGetattr(t *testing.T) {
	skipIfNoFuse(t)
	skipIfNotRoot(t)

	mountpoint := t.TempDir()

	// Mount uses allow_other which requires root.
	server, err := Mount(mountpoint)
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}
	defer server.Unmount()

	var stat syscall.Stat_t
	if err := syscall.Stat(mountpoint, &stat); err != nil {
		t.Fatalf("stat mountpoint: %v", err)
	}

	wantMode := uint32(syscall.S_IFDIR | 0555)
	if stat.Mode != wantMode {
		t.Errorf("root mode = %#o, want %#o", stat.Mode, wantMode)
	}
}
