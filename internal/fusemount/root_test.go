//go:build linux

package fusemount

import (
	"context"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
)

func TestRootNodeGetattr(t *testing.T) {
	reg := NewRegistry()
	root := NewRoot(reg)

	var out fuse.AttrOut
	errno := root.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr returned errno %d", errno)
	}
	wantMode := uint32(syscall.S_IFDIR | 0555)
	if out.Mode != wantMode {
		t.Errorf("Mode = %o, want %o", out.Mode, wantMode)
	}
	if out.Nlink != 2 {
		t.Errorf("Nlink = %d, want 2", out.Nlink)
	}
}

func TestRootNodeReaddirEmpty(t *testing.T) {
	reg := NewRegistry()
	root := NewRoot(reg)

	stream, errno := root.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir returned errno %d", errno)
	}

	if stream.HasNext() {
		t.Error("expected empty DirStream for empty registry")
	}
}

func TestRootNodeReaddirPopulated(t *testing.T) {
	reg := NewRegistry()
	reg.Register("drive", &mockHandler{})
	reg.Register("mail", &mockHandler{})
	root := NewRoot(reg)

	stream, errno := root.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir returned errno %d", errno)
	}

	var entries []fuse.DirEntry
	for stream.HasNext() {
		e, _ := stream.Next()
		entries = append(entries, e)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Entries should be sorted (drive, mail).
	if entries[0].Name != "drive" {
		t.Errorf("entries[0].Name = %q, want %q", entries[0].Name, "drive")
	}
	if entries[1].Name != "mail" {
		t.Errorf("entries[1].Name = %q, want %q", entries[1].Name, "mail")
	}
	for i, e := range entries {
		if e.Mode != fuse.S_IFDIR {
			t.Errorf("entries[%d].Mode = %o, want S_IFDIR", i, e.Mode)
		}
	}
}

func TestRootNodeLookupNotFound(t *testing.T) {
	reg := NewRegistry()
	root := NewRoot(reg)

	_, errno := root.Lookup(context.Background(), "nonexistent", &fuse.EntryOut{})
	if errno != syscall.ENOENT {
		t.Errorf("Lookup returned errno %d, want ENOENT (%d)", errno, syscall.ENOENT)
	}
}
