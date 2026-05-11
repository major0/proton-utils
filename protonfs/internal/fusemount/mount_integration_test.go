//go:build integration

package fusemount

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func skipIfNoFuse(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/dev/fuse"); err != nil {
		t.Skip("skipping: /dev/fuse not available")
	}
}

func TestIntegration_MountRootGetattr(t *testing.T) {
	skipIfNoFuse(t)

	mountpoint := t.TempDir()
	registry := NewRegistry()

	server, err := Mount(MountConfig{Mountpoint: mountpoint}, registry)
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

func TestIntegration_SignalHandling(t *testing.T) {
	skipIfNoFuse(t)

	mountpoint := t.TempDir()
	registry := NewRegistry()

	server, err := Mount(MountConfig{Mountpoint: mountpoint}, registry)
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	// Set up signal handling like the real binary does.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	unmounted := make(chan struct{})
	go func() {
		<-sigCh
		server.Unmount()
		close(unmounted)
	}()

	// Send SIGTERM to self.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill self: %v", err)
	}

	select {
	case <-unmounted:
		// Clean unmount succeeded.
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for unmount after SIGTERM")
	}

	// Verify the mountpoint is no longer a FUSE mount.
	if DetectStaleMount(mountpoint) {
		t.Error("mount still detected after SIGTERM unmount")
	}
}
