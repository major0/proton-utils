//go:build linux

package fusemount

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
)

// testMountInfo returns a fake os.FileInfo for tests.
type testMountInfo struct{}

func (testMountInfo) Name() string      { return "fs" }
func (testMountInfo) Size() int64       { return 0 }
func (testMountInfo) Mode() os.FileMode { return os.ModeDir | 0700 }
func (testMountInfo) ModTime() time.Time { return time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) }
func (testMountInfo) IsDir() bool       { return true }
func (testMountInfo) Sys() any          { return nil }

func TestRootNodeGetattr(t *testing.T) {
	reg := NewRegistry()
	root := NewRoot(reg, testMountInfo{})

	var out fuse.AttrOut
	errno := root.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr returned errno %d", errno)
	}
	wantMode := uint32(syscall.S_IFDIR | 0500)
	if out.Mode != wantMode {
		t.Errorf("Mode = %o, want %o", out.Mode, wantMode)
	}
	if out.Nlink != 2 {
		t.Errorf("Nlink = %d, want 2", out.Nlink)
	}
}

func TestRootNodeReaddirEmpty(t *testing.T) {
	reg := NewRegistry()
	root := NewRoot(reg, testMountInfo{})

	stream, errno := root.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir returned errno %d", errno)
	}

	// Should have . and .. even with empty registry.
	var entries []fuse.DirEntry
	for stream.HasNext() {
		e, _ := stream.Next()
		entries = append(entries, e)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (. and ..), got %d", len(entries))
	}
	if entries[0].Name != "." {
		t.Errorf("entries[0].Name = %q, want %q", entries[0].Name, ".")
	}
	if entries[1].Name != ".." {
		t.Errorf("entries[1].Name = %q, want %q", entries[1].Name, "..")
	}
}

func TestRootNodeReaddirPopulated(t *testing.T) {
	reg := NewRegistry()
	reg.Register("drive", &mockHandler{})
	reg.Register("mail", &mockHandler{})
	root := NewRoot(reg, testMountInfo{})

	stream, errno := root.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir returned errno %d", errno)
	}

	var entries []fuse.DirEntry
	for stream.HasNext() {
		e, _ := stream.Next()
		entries = append(entries, e)
	}

	// 2 (. and ..) + 2 (drive, mail) = 4
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	// First two are . and ..
	if entries[0].Name != "." {
		t.Errorf("entries[0].Name = %q, want %q", entries[0].Name, ".")
	}
	if entries[1].Name != ".." {
		t.Errorf("entries[1].Name = %q, want %q", entries[1].Name, "..")
	}

	// Namespace entries should be sorted (drive, mail).
	if entries[2].Name != "drive" {
		t.Errorf("entries[2].Name = %q, want %q", entries[2].Name, "drive")
	}
	if entries[3].Name != "mail" {
		t.Errorf("entries[3].Name = %q, want %q", entries[3].Name, "mail")
	}
	for i, e := range entries {
		if e.Mode != fuse.S_IFDIR && e.Mode != syscall.S_IFDIR {
			t.Errorf("entries[%d].Mode = %o, want S_IFDIR", i, e.Mode)
		}
	}
}

func TestRootNodeLookupNotFound(t *testing.T) {
	reg := NewRegistry()
	root := NewRoot(reg, testMountInfo{})

	_, errno := root.Lookup(context.Background(), "nonexistent", &fuse.EntryOut{})
	if errno != syscall.ENOENT {
		t.Errorf("Lookup returned errno %d, want ENOENT (%d)", errno, syscall.ENOENT)
	}
}
