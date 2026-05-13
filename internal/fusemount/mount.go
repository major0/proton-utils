//go:build linux

package fusemount

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// MountConfig holds configuration for the per-user FUSE mount.
type MountConfig struct {
	Mountpoint string
}

// EnsureMountDir creates the parent directory of the mountpoint with mode 0700
// and verifies the current user owns it with the correct permissions.
func EnsureMountDir(path string) error {
	parentDir := filepath.Dir(path)

	if err := os.MkdirAll(parentDir, 0700); err != nil {
		return fmt.Errorf("creating mount parent directory: %w", err)
	}

	info, err := os.Stat(parentDir)
	if err != nil {
		return fmt.Errorf("stat mount parent directory: %w", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("unable to get ownership info for %s", parentDir)
	}

	currentUID := uint32(os.Getuid())
	if stat.Uid != currentUID {
		return fmt.Errorf("mount parent directory %s is owned by uid %d, expected %d", parentDir, stat.Uid, currentUID)
	}

	mode := info.Mode().Perm()
	if mode != 0700 {
		return fmt.Errorf("mount parent directory %s has mode %04o, expected 0700", parentDir, mode)
	}

	return nil
}

// DetectStaleMount checks /proc/mounts for an existing FUSE mount at path.
func DetectStaleMount(path string) bool {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return false
	}
	defer f.Close()
	return detectStaleMountFrom(f, path)
}

// detectStaleMountFrom reads mount entries from r and returns true if any line
// has the given path as mountpoint with a fuse filesystem type.
func detectStaleMountFrom(r io.Reader, path string) bool {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		mountpoint := fields[1]
		fstype := fields[2]
		if mountpoint == path && strings.Contains(fstype, "fuse") {
			return true
		}
	}
	return false
}

// CleanStaleMount attempts to unmount a stale FUSE mount at path using
// fusermount. If the normal unmount fails, it falls back to lazy unmount.
// The path must be an absolute path (validated by EnsureMountDir).
func CleanStaleMount(path string) error {
	// Reject paths with null bytes which could cause unexpected behavior.
	for i := 0; i < len(path); i++ {
		if path[i] == 0 {
			return fmt.Errorf("mount path contains null byte")
		}
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return fmt.Errorf("mount path must be absolute: %s", path)
	}
	if err := exec.Command("fusermount", "-u", clean).Run(); err != nil { //nolint:gosec // clean is validated absolute path
		// Try lazy unmount as fallback.
		return exec.Command("fusermount", "-uz", clean).Run() //nolint:gosec // clean is validated absolute path
	}
	return nil
}

// Mount creates and starts the per-user FUSE server. It ensures the mount
// directory exists, cleans any stale mount, and starts the FUSE server with
// the given registry as the root filesystem.
func Mount(cfg MountConfig, registry *NamespaceRegistry) (*fuse.Server, error) {
	if err := EnsureMountDir(cfg.Mountpoint); err != nil {
		return nil, fmt.Errorf("ensuring mount directory: %w", err)
	}

	if DetectStaleMount(cfg.Mountpoint) {
		if err := CleanStaleMount(cfg.Mountpoint); err != nil {
			return nil, fmt.Errorf("cleaning stale mount: %w", err)
		}
	}

	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			FsName: "proton-fuse",
			Name:   "proton-fuse",
		},
		RootStableAttr: &fs.StableAttr{Ino: 1},
	}

	server, err := fs.Mount(cfg.Mountpoint, NewRoot(registry), opts)
	if err != nil {
		return nil, fmt.Errorf("mounting FUSE filesystem: %w", err)
	}

	return server, nil
}
