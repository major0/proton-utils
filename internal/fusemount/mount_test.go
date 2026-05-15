//go:build linux

package fusemount

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureMountDir_CreatesWithCorrectMode(t *testing.T) {
	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "proton", "fs")

	if err := EnsureMountDir(mountpoint); err != nil {
		t.Fatalf("EnsureMountDir() error = %v", err)
	}

	parentDir := filepath.Dir(mountpoint)
	info, err := os.Stat(parentDir)
	if err != nil {
		t.Fatalf("stat parent dir: %v", err)
	}

	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("parent dir mode = %04o, want 0700", mode)
	}
}

func TestEnsureMountDir_ExistingCorrectDir(t *testing.T) {
	tmpDir := t.TempDir()
	parentDir := filepath.Join(tmpDir, "proton")
	if err := os.MkdirAll(parentDir, 0700); err != nil {
		t.Fatalf("setup: %v", err)
	}

	mountpoint := filepath.Join(parentDir, "fs")
	if err := EnsureMountDir(mountpoint); err != nil {
		t.Fatalf("EnsureMountDir() error = %v", err)
	}
}

func TestEnsureMountDir_FixesWrongMode(t *testing.T) {
	tmpDir := t.TempDir()
	parentDir := filepath.Join(tmpDir, "proton")
	if err := os.MkdirAll(parentDir, 0755); err != nil { //nolint:gosec // G301: test setup — verifying EnsureMountDir tightens permissions
		t.Fatalf("setup: %v", err)
	}

	mountpoint := filepath.Join(parentDir, "fs")
	err := EnsureMountDir(mountpoint)
	if err != nil {
		t.Fatalf("EnsureMountDir() unexpected error: %v", err)
	}

	// Verify the parent directory was tightened to 0700.
	info, err := os.Stat(parentDir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("parent mode = %04o, want 0700", mode)
	}
}

func TestDetectStaleMount_Found(t *testing.T) {
	content := `/dev/sda1 / ext4 rw,relatime 0 0
/dev/fuse /run/user/1000/proton/fs fuse.proton-fuse rw,nosuid,nodev,relatime,user_id=1000,group_id=1000 0 0
tmpfs /tmp tmpfs rw 0 0
`
	r := strings.NewReader(content)
	if !detectStaleMountFrom(r, "/run/user/1000/proton/fs") {
		t.Error("detectStaleMountFrom() = false, want true")
	}
}

func TestDetectStaleMount_NotFound(t *testing.T) {
	content := `/dev/sda1 / ext4 rw,relatime 0 0
tmpfs /tmp tmpfs rw 0 0
`
	r := strings.NewReader(content)
	if detectStaleMountFrom(r, "/run/user/1000/proton/fs") {
		t.Error("detectStaleMountFrom() = true, want false")
	}
}

func TestDetectStaleMount_NonFuseAtSamePath(t *testing.T) {
	content := `/dev/sda1 /run/user/1000/proton/fs ext4 rw,relatime 0 0
`
	r := strings.NewReader(content)
	if detectStaleMountFrom(r, "/run/user/1000/proton/fs") {
		t.Error("detectStaleMountFrom() = true for non-fuse mount, want false")
	}
}

func TestDetectStaleMount_VariousFormats(t *testing.T) {
	tests := []struct {
		name    string
		content string
		path    string
		want    bool
	}{
		{
			name:    "fuse prefix",
			content: "device /mnt/test fuse rw 0 0\n",
			path:    "/mnt/test",
			want:    true,
		},
		{
			name:    "fuse dot suffix",
			content: "device /mnt/test fuse.myfs rw 0 0\n",
			path:    "/mnt/test",
			want:    true,
		},
		{
			name:    "fuseblk",
			content: "device /mnt/test fuseblk rw 0 0\n",
			path:    "/mnt/test",
			want:    true,
		},
		{
			name:    "empty file",
			content: "",
			path:    "/mnt/test",
			want:    false,
		},
		{
			name:    "short line",
			content: "device /mnt/test\n",
			path:    "/mnt/test",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := strings.NewReader(tt.content)
			got := detectStaleMountFrom(r, tt.path)
			if got != tt.want {
				t.Errorf("detectStaleMountFrom() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMountConfig_ZeroTimeouts(t *testing.T) {
	cfg := MountConfig{
		Mountpoint: "/run/user/1000/proton/fs",
	}

	// Zero-value timeouts mean "kernel default" — verify they are zero.
	if cfg.EntryTimeout != 0 {
		t.Errorf("EntryTimeout = %v, want 0 (kernel default)", cfg.EntryTimeout)
	}
	if cfg.AttrTimeout != 0 {
		t.Errorf("AttrTimeout = %v, want 0 (kernel default)", cfg.AttrTimeout)
	}
}

func TestMountConfig_ExplicitTimeouts(t *testing.T) {
	cfg := MountConfig{
		Mountpoint:   "/run/user/1000/proton/fs",
		EntryTimeout: 2 * time.Second,
		AttrTimeout:  3 * time.Second,
	}

	if cfg.EntryTimeout != 2*time.Second {
		t.Errorf("EntryTimeout = %v, want 2s", cfg.EntryTimeout)
	}
	if cfg.AttrTimeout != 3*time.Second {
		t.Errorf("AttrTimeout = %v, want 3s", cfg.AttrTimeout)
	}
}

func TestMountConfig_TimeoutFieldsIndependent(t *testing.T) {
	cfg := MountConfig{
		Mountpoint:   "/run/user/1000/proton/fs",
		EntryTimeout: 5 * time.Second,
		// AttrTimeout left at zero.
	}

	if cfg.EntryTimeout != 5*time.Second {
		t.Errorf("EntryTimeout = %v, want 5s", cfg.EntryTimeout)
	}
	if cfg.AttrTimeout != 0 {
		t.Errorf("AttrTimeout = %v, want 0 (kernel default)", cfg.AttrTimeout)
	}
}

func TestBuildFSOptions_TimeoutWiring(t *testing.T) {
	cfg := MountConfig{
		Mountpoint:   "/run/user/1000/proton/fs",
		EntryTimeout: 2 * time.Second,
		AttrTimeout:  3 * time.Second,
	}

	opts := buildFSOptions(cfg)

	if opts.EntryTimeout == nil {
		t.Fatal("EntryTimeout pointer is nil")
	}
	if *opts.EntryTimeout != 2*time.Second {
		t.Errorf("fs.Options.EntryTimeout = %v, want 2s", *opts.EntryTimeout)
	}
	if opts.AttrTimeout == nil {
		t.Fatal("AttrTimeout pointer is nil")
	}
	if *opts.AttrTimeout != 3*time.Second {
		t.Errorf("fs.Options.AttrTimeout = %v, want 3s", *opts.AttrTimeout)
	}
}

func TestBuildFSOptions_ZeroTimeoutWiring(t *testing.T) {
	cfg := MountConfig{
		Mountpoint: "/run/user/1000/proton/fs",
		// EntryTimeout and AttrTimeout left at zero.
	}

	opts := buildFSOptions(cfg)

	if opts.EntryTimeout == nil {
		t.Fatal("EntryTimeout pointer is nil")
	}
	if *opts.EntryTimeout != 0 {
		t.Errorf("fs.Options.EntryTimeout = %v, want 0 (kernel default)", *opts.EntryTimeout)
	}
	if opts.AttrTimeout == nil {
		t.Fatal("AttrTimeout pointer is nil")
	}
	if *opts.AttrTimeout != 0 {
		t.Errorf("fs.Options.AttrTimeout = %v, want 0 (kernel default)", *opts.AttrTimeout)
	}
}

func TestBuildFSOptions_MountOptions(t *testing.T) {
	cfg := MountConfig{
		Mountpoint:   "/run/user/1000/proton/fs",
		EntryTimeout: time.Second,
		AttrTimeout:  time.Second,
	}

	opts := buildFSOptions(cfg)

	if opts.FsName != "proton-fuse" {
		t.Errorf("FsName = %q, want %q", opts.FsName, "proton-fuse")
	}
	if opts.Name != "proton-fuse" {
		t.Errorf("Name = %q, want %q", opts.Name, "proton-fuse")
	}
	if !opts.AllowOther {
		t.Error("AllowOther = false, want true")
	}
	if opts.MaxWrite != blockSize {
		t.Errorf("MaxWrite = %d, want %d", opts.MaxWrite, blockSize)
	}
	if opts.RootStableAttr == nil || opts.RootStableAttr.Ino != 1 {
		t.Errorf("RootStableAttr.Ino = %v, want 1", opts.RootStableAttr)
	}
}

func TestBuildFSOptions_PrefetchBlocks(t *testing.T) {
	tests := []struct {
		name           string
		prefetchBlocks int
		wantReadAhead  int
	}{
		{"zero means kernel default", 0, 0},
		{"one block", 1, 4 * 1024 * 1024},
		{"four blocks", 4, 16 * 1024 * 1024},
		{"max 64 blocks", 64, 64 * 4 * 1024 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := MountConfig{
				Mountpoint:     "/run/user/1000/proton/fs",
				EntryTimeout:   time.Second,
				AttrTimeout:    time.Second,
				PrefetchBlocks: tt.prefetchBlocks,
			}

			opts := buildFSOptions(cfg)

			if opts.MaxReadAhead != tt.wantReadAhead {
				t.Errorf("MaxReadAhead = %d, want %d", opts.MaxReadAhead, tt.wantReadAhead)
			}
		})
	}
}
