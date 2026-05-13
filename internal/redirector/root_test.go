//go:build linux

package redirector

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
)

func TestSymlinkTarget(t *testing.T) {
	tests := []struct {
		uid  uint32
		name string
		want string
	}{
		{1000, "drive", "/run/user/1000/proton/fs/drive"},
		{1001, "lumo", "/run/user/1001/proton/fs/lumo"},
		{65534, "mail", "/run/user/65534/proton/fs/mail"},
		{1, "x", "/run/user/1/proton/fs/x"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("uid=%d/name=%s", tt.uid, tt.name), func(t *testing.T) {
			got := symlinkTarget(tt.uid, tt.name)
			if got != tt.want {
				t.Errorf("symlinkTarget(%d, %q) = %q, want %q", tt.uid, tt.name, got, tt.want)
			}
		})
	}
}

func TestRedirectorRoot_Getattr(t *testing.T) {
	root := &RedirectorRoot{}
	var out fuse.AttrOut
	errno := root.Getattr(context.TODO(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr returned errno %d", errno)
	}
	wantMode := uint32(syscall.S_IFDIR | 0555)
	if out.Mode != wantMode {
		t.Errorf("Mode = %#o, want %#o", out.Mode, wantMode)
	}
	if out.Nlink != 2 {
		t.Errorf("Nlink = %d, want 2", out.Nlink)
	}
}

func TestRedirectorRoot_Readdir(t *testing.T) {
	root := &RedirectorRoot{}
	stream, errno := root.Readdir(context.TODO())
	if errno != 0 {
		t.Fatalf("Readdir returned errno %d", errno)
	}
	// Should always have at least . and .. entries.
	var entries []fuse.DirEntry
	for stream.HasNext() {
		e, _ := stream.Next()
		entries = append(entries, e)
	}
	if len(entries) < 2 {
		t.Fatalf("Readdir returned %d entries, want at least 2 (. and ..)", len(entries))
	}
	if entries[0].Name != "." {
		t.Errorf("entries[0].Name = %q, want %q", entries[0].Name, ".")
	}
	if entries[1].Name != ".." {
		t.Errorf("entries[1].Name = %q, want %q", entries[1].Name, "..")
	}
}

func TestSymlinkNode_Readlink(t *testing.T) {
	target := "/run/user/1000/proton/fs/drive"
	node := &SymlinkNode{target: target}
	got, errno := node.Readlink(context.TODO())
	if errno != 0 {
		t.Fatalf("Readlink returned errno %d", errno)
	}
	if string(got) != target {
		t.Errorf("Readlink = %q, want %q", string(got), target)
	}
}

func TestSymlinkNode_Getattr(t *testing.T) {
	target := "/run/user/1000/proton/fs/drive"
	node := &SymlinkNode{target: target}
	var out fuse.AttrOut
	errno := node.Getattr(context.TODO(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr returned errno %d", errno)
	}
	wantMode := uint32(syscall.S_IFLNK | 0777)
	if out.Mode != wantMode {
		t.Errorf("Mode = %#o, want %#o", out.Mode, wantMode)
	}
	wantSize := uint64(len(target))
	if out.Size != wantSize {
		t.Errorf("Size = %d, want %d", out.Size, wantSize)
	}
}

func TestClearEnvironment(t *testing.T) {
	// Set some env vars to ensure they get cleared.
	t.Setenv("TEST_CLEAR_A", "value_a")
	t.Setenv("TEST_CLEAR_B", "value_b")

	if len(os.Environ()) == 0 {
		t.Fatal("expected non-empty environment before ClearEnvironment")
	}

	ClearEnvironment()

	if env := os.Environ(); len(env) != 0 {
		t.Errorf("os.Environ() = %v, want empty", env)
	}
}
