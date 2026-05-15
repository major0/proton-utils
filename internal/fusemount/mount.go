//go:build linux

package fusemount

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// MountConfig holds configuration for the per-user FUSE mount.
type MountConfig struct {
	Mountpoint     string
	EntryTimeout   time.Duration // default 1s; zero = kernel default
	AttrTimeout    time.Duration // default 1s; zero = kernel default
	PrefetchBlocks int           // kernel read-ahead in blocks (0 = kernel default, max 64)
}

// EnsureMountDir creates the mountpoint and its parent directory with mode 0700
// and verifies the current user owns the parent with the correct permissions.
func EnsureMountDir(path string) error {
	parentDir := filepath.Dir(path)

	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("creating mount directory: %w", err)
	}

	info, err := os.Stat(parentDir)
	if err != nil {
		return fmt.Errorf("stat mount parent directory: %w", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("unable to get ownership info for %s", parentDir)
	}

	uid := os.Getuid()
	if uid < 0 || uid > math.MaxUint32 {
		return fmt.Errorf("invalid UID: %d", uid)
	}
	currentUID := uint32(uid) //nolint:gosec // bounds checked above
	if stat.Uid != currentUID {
		return fmt.Errorf("mount parent directory %s is owned by uid %d, expected %d", parentDir, stat.Uid, currentUID)
	}

	mode := info.Mode().Perm()
	if mode&0077 != 0 {
		// Directory is group/world accessible — tighten to 0700.
		if err := os.Chmod(parentDir, 0700); err != nil { //nolint:gosec // G302: tightening permissions is intentional
			return fmt.Errorf("mount parent directory %s has mode %04o, chmod to 0700 failed: %w", parentDir, mode, err)
		}
	}

	return nil
}

// DetectStaleMount checks /proc/mounts for an existing FUSE mount at path.
func DetectStaleMount(path string) bool {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
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

// blockSize is the Proton Drive block size (4 MiB). Defined locally to
// avoid importing api/drive from the fusemount package.
const blockSize = 4 * 1024 * 1024

// buildFSOptions constructs the go-fuse fs.Options from a MountConfig.
// Extracted for testability — the timeout wiring can be verified without
// requiring /dev/fuse.
func buildFSOptions(cfg MountConfig) *fs.Options {
	mopts := fuse.MountOptions{
		FsName:     "proton-fuse",
		Name:       "proton-fuse",
		AllowOther: true,
		MaxWrite:   blockSize, // 4 MiB — aligns FUSE reads with block size
	}
	if cfg.PrefetchBlocks > 0 {
		mopts.MaxReadAhead = cfg.PrefetchBlocks * blockSize
	}
	return &fs.Options{
		MountOptions:   mopts,
		RootStableAttr: &fs.StableAttr{Ino: 1},
		EntryTimeout:   &cfg.EntryTimeout,
		AttrTimeout:    &cfg.AttrTimeout,
		UID:            uint32(os.Getuid()), //nolint:gosec // UID fits uint32 on Linux
		GID:            uint32(os.Getgid()), //nolint:gosec // GID fits uint32 on Linux
	}
}

// Mount creates and starts the per-user FUSE server. It detects and cleans
// any stale mount, ensures the mount directory exists, and starts the FUSE
// server with the given registry as the root filesystem.
func Mount(cfg MountConfig, registry *NamespaceRegistry) (*fuse.Server, error) {
	// Clean stale mounts first — a dead FUSE mount at the path causes
	// MkdirAll to fail with "file exists" because the kernel reports the
	// mountpoint as existing but inaccessible.
	if DetectStaleMount(cfg.Mountpoint) {
		if err := CleanStaleMount(cfg.Mountpoint); err != nil {
			return nil, fmt.Errorf("cleaning stale mount: %w", err)
		}
	}

	if err := EnsureMountDir(cfg.Mountpoint); err != nil {
		return nil, fmt.Errorf("ensuring mount directory: %w", err)
	}

	// Stat the mountpoint before mounting to capture timestamps.
	mountInfo, err := os.Stat(cfg.Mountpoint)
	if err != nil {
		return nil, fmt.Errorf("stat mountpoint: %w", err)
	}

	opts := buildFSOptions(cfg)

	server, err := fs.Mount(cfg.Mountpoint, NewRoot(registry, mountInfo), opts)
	if err != nil {
		return nil, fmt.Errorf("mounting FUSE filesystem: %w", err)
	}

	return server, nil
}
