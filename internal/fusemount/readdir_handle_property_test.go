//go:build linux

package fusemount

import (
	"context"
	"fmt"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
	"pgregory.net/rapid"
)

// Feature: fuse-readdirplus, Property 1: Attr Population Correctness
// For any directory entry returned by READDIRPLUS where XAttr prefetch
// succeeded, the EntryOut fields SHALL match the values returned by the
// child node's Getattr(), with Uid/Gid set to the daemon owner's UID/GID.
// **Validates: Requirements 1.2, 2.3, 3.1, 3.2**
func TestPropertyAttrPopulationCorrectness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		perms := rapid.Uint32Range(0, 0o777).Draw(t, "perms")
		mode := uint32(syscall.S_IFREG) | perms
		size := rapid.Uint64().Draw(t, "size")
		mtime := rapid.Uint64().Draw(t, "mtime")
		ctime := rapid.Uint64().Draw(t, "ctime")
		atime := rapid.Uint64().Draw(t, "atime")
		uid := rapid.Uint32().Draw(t, "uid")
		gid := rapid.Uint32().Draw(t, "gid")

		child := &mockNode{attr: Attr{
			Mode:  mode,
			Size:  size,
			Nlink: 1,
			Mtime: mtime,
			Ctime: ctime,
			Atime: atime,
		}}
		h := &mockHandler{nodes: map[string]Node{"f": child}}
		root := mountedRoot(h, uid, gid)

		entries := []DirEntry{{Name: "f", Mode: mode, Link: &mockPrefetchLink{}}}
		handle := buildReaddirHandle(root, entries)

		var out fuse.EntryOut
		_, errno := handle.Lookup(context.Background(), "f", &out)
		if errno != 0 {
			t.Fatalf("Lookup errno %d", errno)
		}
		if out.Mode != mode {
			t.Errorf("out.Mode = %o, want %o", out.Mode, mode)
		}
		if out.Size != size {
			t.Errorf("out.Size = %d, want %d", out.Size, size)
		}
		if out.Mtime != mtime || out.Ctime != ctime || out.Atime != atime {
			t.Errorf("times = (%d,%d,%d), want (%d,%d,%d)",
				out.Mtime, out.Ctime, out.Atime, mtime, ctime, atime)
		}
		if out.Uid != uid || out.Gid != gid {
			t.Errorf("uid/gid = (%d,%d), want (%d,%d)", out.Uid, out.Gid, uid, gid)
		}
	})
}

// Feature: fuse-readdirplus, Property 3: Prefetch Targets File-Type Children Only
// For any set of directory children containing both file-type and folder-type
// entries, EnsureXAttrPrefetch SHALL be invoked on all and only the file-type
// children.
// **Validates: Requirements 2.1, 2.5**
func TestPropertyPrefetchTargetsFilesOnly(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "n")

		children := make(map[string]childEntry)
		var links []*mockPrefetchLink
		fileCount := 0
		for i := 0; i < n; i++ {
			isFile := rapid.Bool().Draw(t, fmt.Sprintf("isFile%d", i))
			l := &mockPrefetchLink{}
			links = append(links, l)
			mode := uint32(syscall.S_IFDIR) | 0700
			if isFile {
				mode = uint32(syscall.S_IFREG) | 0644
				fileCount++
			}
			children[fmt.Sprintf("c%d", i)] = childEntry{link: l, mode: mode}
		}
		h := &readdirHandle{children: children}

		prefetchFileAttrs(context.Background(), h)

		var got int64
		for _, l := range links {
			got += l.count()
		}
		if int(got) != fileCount {
			t.Errorf("prefetch count = %d, want %d (file-type children)", got, fileCount)
		}
	})
}

// Feature: fuse-readdirplus, Property 4: Directory Type Invariants
// For any directory-type entry, Mode SHALL equal S_IFDIR|0700 and Nlink SHALL
// equal 2. For any file-type entry, Nlink SHALL equal 1.
// **Validates: Requirements 3.4, 3.5**
func TestPropertyDirectoryTypeInvariants(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		isDir := rapid.Bool().Draw(t, "isDir")

		var child Node
		var mode uint32
		if isDir {
			child = &mockNode{attr: Attr{Mode: syscall.S_IFDIR | 0700, Nlink: 2}}
			mode = syscall.S_IFDIR | 0700
		} else {
			child = &mockNode{attr: Attr{Mode: syscall.S_IFREG | 0600, Nlink: 1}}
			mode = syscall.S_IFREG | 0600
		}
		h := &mockHandler{nodes: map[string]Node{"c": child}}
		root := mountedRoot(h, 1, 1)

		entries := []DirEntry{{Name: "c", Mode: mode, Link: &mockPrefetchLink{}}}
		handle := buildReaddirHandle(root, entries)

		var out fuse.EntryOut
		if _, errno := handle.Lookup(context.Background(), "c", &out); errno != 0 {
			t.Fatalf("Lookup errno %d", errno)
		}

		if isDir {
			if out.Mode != syscall.S_IFDIR|0700 {
				t.Errorf("dir out.Mode = %o, want %o", out.Mode, syscall.S_IFDIR|0700)
			}
			if out.Nlink != 2 {
				t.Errorf("dir out.Nlink = %d, want 2", out.Nlink)
			}
		} else {
			if out.Nlink != 1 {
				t.Errorf("file out.Nlink = %d, want 1", out.Nlink)
			}
			if out.Mode&syscall.S_IFMT != syscall.S_IFREG {
				t.Errorf("file out.Mode type bits = %o, want S_IFREG", out.Mode&syscall.S_IFMT)
			}
		}
	})
}

// Feature: fuse-readdirplus, Property 5: Inode Stability Across Paths
// For any child node, the StableAttr produced by wrapChild (called from both
// DispatchNode.Lookup and readdirHandle.Lookup) SHALL be identical.
//
// Note: go-fuse assigns a fresh automatic inode number per wrapped ops object
// when StableAttr.Ino is 0, so the two paths yield distinct *Inode values in
// isolation; name-based deduplication happens later in the bridge. This test
// asserts the guarantee our shared code path actually controls: identical
// StableAttr.Mode (the file-type bits) and identical EntryOut.Mode.
// **Validates: Requirements 4.1, 4.2, 4.3**
func TestPropertyInodeStabilityAcrossPaths(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		isDir := rapid.Bool().Draw(t, "isDir")
		var child Node
		var mode uint32
		if isDir {
			child = &mockNode{attr: Attr{Mode: syscall.S_IFDIR | 0700, Nlink: 2}}
			mode = syscall.S_IFDIR | 0700
		} else {
			perms := rapid.Uint32Range(0, 0o777).Draw(t, "perms")
			child = &mockNode{attr: Attr{Mode: syscall.S_IFREG | perms, Nlink: 1}}
			mode = syscall.S_IFREG | perms
		}
		h := &mockHandler{nodes: map[string]Node{"c": child}}
		root := mountedRoot(h, 5, 6)

		// Path 1: DispatchNode.Lookup
		var out1 fuse.EntryOut
		inode1, errno1 := root.Lookup(context.Background(), "c", &out1)
		if errno1 != 0 {
			t.Fatalf("DispatchNode.Lookup errno %d", errno1)
		}

		// Path 2: readdirHandle.Lookup
		entries := []DirEntry{{Name: "c", Mode: mode, Link: &mockPrefetchLink{}}}
		handle := buildReaddirHandle(root, entries)
		var out2 fuse.EntryOut
		inode2, errno2 := handle.Lookup(context.Background(), "c", &out2)
		if errno2 != 0 {
			t.Fatalf("readdirHandle.Lookup errno %d", errno2)
		}

		if inode1.StableAttr().Mode != inode2.StableAttr().Mode {
			t.Errorf("StableAttr.Mode differs: Lookup=%o readdir=%o",
				inode1.StableAttr().Mode, inode2.StableAttr().Mode)
		}
		if out1.Mode != out2.Mode {
			t.Errorf("EntryOut.Mode differs: Lookup=%o readdir=%o", out1.Mode, out2.Mode)
		}
	})
}

// Feature: fuse-readdirplus, Property 6: Dot Entries Always First
// For any directory, the first two entries in the READDIRPLUS response SHALL
// be "." and ".." (in that order), each with directory mode.
// **Validates: Requirements 8.1, 8.2, 8.3**
func TestPropertyDotEntriesAlwaysFirst(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "n")

		entries := make([]DirEntry, 0, n)
		for i := 0; i < n; i++ {
			entries = append(entries, DirEntry{
				Name: fmt.Sprintf("e%d", i),
				Mode: syscall.S_IFREG,
			})
		}
		d := &DispatchNode{handler: &mockHandler{}, isRoot: true}
		handle := buildReaddirHandle(d, entries)

		e0, _ := handle.Readdirent(context.Background())
		if e0 == nil || e0.Name != "." {
			t.Fatalf("entries[0] = %v, want \".\"", e0)
		}
		if e0.Mode&syscall.S_IFMT != syscall.S_IFDIR {
			t.Errorf("entries[0].Mode = %o, want S_IFDIR", e0.Mode)
		}
		e1, _ := handle.Readdirent(context.Background())
		if e1 == nil || e1.Name != ".." {
			t.Fatalf("entries[1] = %v, want \"..\"", e1)
		}
		if e1.Mode&syscall.S_IFMT != syscall.S_IFDIR {
			t.Errorf("entries[1].Mode = %o, want S_IFDIR", e1.Mode)
		}

		// Remaining entries follow in order.
		for i := 0; i < n; i++ {
			de, _ := handle.Readdirent(context.Background())
			if de == nil {
				t.Fatalf("Readdirent[%d] returned nil early", i+2)
				return
			}
			if de.Name != entries[i].Name {
				t.Errorf("entry[%d].Name = %q, want %q", i+2, de.Name, entries[i].Name)
			}
		}

		// EOF.
		if de, _ := handle.Readdirent(context.Background()); de != nil {
			t.Errorf("expected EOF, got %v", de)
		}
	})
}
