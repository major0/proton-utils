//go:build linux

package fusemount

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := os.MkdirAll(parentDir, 0755); err != nil {
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
