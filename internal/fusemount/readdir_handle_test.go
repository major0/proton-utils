//go:build linux

package fusemount

import (
	"context"
	"fmt"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// mockPrefetchLink implements the xattrPrefetcher interface and counts
// EnsureXAttrPrefetch invocations. It stands in for a *drive.Link in the
// DirEntry.Link field without importing api/drive.
type mockPrefetchLink struct {
	prefetch atomic.Int64
}

func (m *mockPrefetchLink) EnsureXAttrPrefetch() { m.prefetch.Add(1) }

func (m *mockPrefetchLink) count() int64 { return m.prefetch.Load() }

// errnoNode is a Node whose Getattr always fails, used to exercise the
// wrapChild mode-fallback path.
type errnoNode struct{}

func (errnoNode) Getattr(_ context.Context) (Attr, syscall.Errno) {
	return Attr{}, syscall.EIO
}

// mountedRoot returns a root DispatchNode attached to an in-process go-fuse
// bridge via NewNodeFS. This lets NewInode succeed without mounting on
// /dev/fuse, so wrapChild-based Lookup paths can be exercised in unit tests.
func mountedRoot(h NamespaceHandler, uid, gid uint32) *DispatchNode {
	root := &DispatchNode{handler: h, isRoot: true, uid: uid, gid: gid}
	_ = fs.NewNodeFS(root, &fs.Options{})
	return root
}

// --- Task 6.1: wrapChild ---

func TestWrapChild_PopulatesEntryOutAndInode(t *testing.T) {
	child := &mockNode{attr: Attr{
		Mode:  syscall.S_IFREG | 0644,
		Size:  123,
		Nlink: 1,
		Mtime: 111,
		Ctime: 222,
		Atime: 333,
	}}
	h := &mockHandler{nodes: map[string]Node{"file.txt": child}}
	root := mountedRoot(h, 4242, 4343)

	var out fuse.EntryOut
	inode, errno := root.Lookup(context.Background(), "file.txt", &out)
	if errno != 0 {
		t.Fatalf("Lookup returned errno %d", errno)
	}
	if inode == nil {
		t.Fatal("Lookup returned nil inode")
	}
	if out.Mode != syscall.S_IFREG|0644 {
		t.Errorf("out.Mode = %o, want %o", out.Mode, syscall.S_IFREG|0644)
	}
	if out.Size != 123 {
		t.Errorf("out.Size = %d, want 123", out.Size)
	}
	if out.Nlink != 1 {
		t.Errorf("out.Nlink = %d, want 1", out.Nlink)
	}
	if out.Mtime != 111 || out.Ctime != 222 || out.Atime != 333 {
		t.Errorf("times = (%d,%d,%d), want (111,222,333)", out.Mtime, out.Ctime, out.Atime)
	}
	if out.Uid != 4242 || out.Gid != 4343 {
		t.Errorf("uid/gid = (%d,%d), want (4242,4343)", out.Uid, out.Gid)
	}
	if inode.StableAttr().Mode != syscall.S_IFREG {
		t.Errorf("inode StableAttr.Mode = %o, want S_IFREG", inode.StableAttr().Mode)
	}
}

func TestWrapChild_GetattrErrorFallsBackToRegularFileMode(t *testing.T) {
	h := &mockHandler{nodes: map[string]Node{"x": errnoNode{}}}
	root := mountedRoot(h, 1, 1)

	var out fuse.EntryOut
	inode, errno := root.Lookup(context.Background(), "x", &out)
	// wrapChild returns the inode with errno 0 even when Getattr fails —
	// READDIRPLUS/Lookup are best-effort for attrs.
	if errno != 0 {
		t.Fatalf("Lookup returned errno %d, want 0", errno)
	}
	if inode == nil {
		t.Fatal("Lookup returned nil inode")
	}
	if inode.StableAttr().Mode != syscall.S_IFREG {
		t.Errorf("inode StableAttr.Mode = %o, want S_IFREG fallback", inode.StableAttr().Mode)
	}
	// out is not populated when Getattr errors.
	if out.Mode != 0 {
		t.Errorf("out.Mode = %o, want 0 (unpopulated on Getattr error)", out.Mode)
	}
}

// --- Task 6.2: Readdirent ---

func TestReaddirent_YieldsEntriesInOrderThenEOF(t *testing.T) {
	d := &DispatchNode{handler: &mockHandler{}, isRoot: true}
	entries := []DirEntry{
		{Name: "alpha", Mode: syscall.S_IFREG},
		{Name: "beta", Mode: syscall.S_IFDIR},
	}
	h := buildReaddirHandle(d, entries)

	want := []struct {
		name string
		mode uint32
	}{
		{".", syscall.S_IFDIR},
		{"..", syscall.S_IFDIR},
		{"alpha", syscall.S_IFREG},
		{"beta", syscall.S_IFDIR},
	}

	for i, w := range want {
		de, errno := h.Readdirent(context.Background())
		if errno != 0 {
			t.Fatalf("Readdirent[%d] errno %d", i, errno)
		}
		if de == nil {
			t.Fatalf("Readdirent[%d] returned nil, want %q", i, w.name)
		}
		if de.Name != w.name {
			t.Errorf("Readdirent[%d].Name = %q, want %q", i, de.Name, w.name)
		}
		if de.Mode != w.mode {
			t.Errorf("Readdirent[%d].Mode = %o, want %o", i, de.Mode, w.mode)
		}
	}

	// EOF: nil entry, errno 0.
	de, errno := h.Readdirent(context.Background())
	if de != nil || errno != 0 {
		t.Errorf("Readdirent at EOF = (%v, %d), want (nil, 0)", de, errno)
	}
}

// --- Task 6.3: readdirHandle.Lookup ---

func TestReaddirHandleLookup_DotReturnsSelfAttrs(t *testing.T) {
	h := &mockHandler{attr: Attr{Mode: syscall.S_IFDIR | 0700, Nlink: 2, Mtime: 555, Ctime: 666}}
	d := &DispatchNode{handler: h, isRoot: true, uid: 7, gid: 8}
	handle := buildReaddirHandle(d, nil)

	var out fuse.EntryOut
	inode, errno := handle.Lookup(context.Background(), ".", &out)
	if errno != 0 {
		t.Fatalf("Lookup(.) errno %d", errno)
	}
	if inode != nil {
		t.Errorf("Lookup(.) inode = %v, want nil (dot entries are not tree nodes)", inode)
	}
	if out.Mode != syscall.S_IFDIR|0700 {
		t.Errorf("out.Mode = %o, want %o", out.Mode, syscall.S_IFDIR|0700)
	}
	if out.Nlink != 2 {
		t.Errorf("out.Nlink = %d, want 2", out.Nlink)
	}
	if out.Mtime != 555 || out.Ctime != 666 {
		t.Errorf("times = (%d,%d), want (555,666)", out.Mtime, out.Ctime)
	}
	if out.Uid != 7 || out.Gid != 8 {
		t.Errorf("uid/gid = (%d,%d), want (7,8)", out.Uid, out.Gid)
	}
}

func TestReaddirHandleLookup_DotDotReturnsParentDirAttrs(t *testing.T) {
	d := &DispatchNode{handler: &mockHandler{}, isRoot: true, uid: 7, gid: 8}
	handle := buildReaddirHandle(d, nil)

	var out fuse.EntryOut
	inode, errno := handle.Lookup(context.Background(), "..", &out)
	if errno != 0 {
		t.Fatalf("Lookup(..) errno %d", errno)
	}
	if inode != nil {
		t.Errorf("Lookup(..) inode = %v, want nil", inode)
	}
	if out.Mode != syscall.S_IFDIR|0700 {
		t.Errorf("out.Mode = %o, want %o", out.Mode, syscall.S_IFDIR|0700)
	}
	if out.Nlink != 2 {
		t.Errorf("out.Nlink = %d, want 2", out.Nlink)
	}
}

func TestReaddirHandleLookup_KnownChildPopulatesEntryOut(t *testing.T) {
	child := &mockNode{attr: Attr{Mode: syscall.S_IFREG | 0644, Size: 99, Nlink: 1}}
	h := &mockHandler{nodes: map[string]Node{"f": child}}
	root := mountedRoot(h, 100, 200)

	entries := []DirEntry{{Name: "f", Mode: syscall.S_IFREG | 0644, Link: &mockPrefetchLink{}}}
	handle := buildReaddirHandle(root, entries)

	var out fuse.EntryOut
	inode, errno := handle.Lookup(context.Background(), "f", &out)
	if errno != 0 {
		t.Fatalf("Lookup(f) errno %d", errno)
	}
	if inode == nil {
		t.Fatal("Lookup(f) returned nil inode")
	}
	if out.Size != 99 {
		t.Errorf("out.Size = %d, want 99", out.Size)
	}
	if out.Uid != 100 || out.Gid != 200 {
		t.Errorf("uid/gid = (%d,%d), want (100,200)", out.Uid, out.Gid)
	}
}

func TestReaddirHandleLookup_UnknownNameReturnsENOENT(t *testing.T) {
	d := &DispatchNode{handler: &mockHandler{}, isRoot: true}
	handle := buildReaddirHandle(d, nil)

	var out fuse.EntryOut
	_, errno := handle.Lookup(context.Background(), "missing", &out)
	if errno != syscall.ENOENT {
		t.Errorf("Lookup(missing) errno %d, want ENOENT", errno)
	}
}

// --- Task 6.4: prefetchFileAttrs ---

func TestPrefetchFileAttrs_OnlyFileTypeChildren(t *testing.T) {
	fileLink := &mockPrefetchLink{}
	dirLink := &mockPrefetchLink{}
	h := &readdirHandle{
		children: map[string]childEntry{
			"f": {link: fileLink, mode: syscall.S_IFREG | 0644},
			"d": {link: dirLink, mode: syscall.S_IFDIR | 0700},
		},
	}

	prefetchFileAttrs(context.Background(), h)

	if got := fileLink.count(); got != 1 {
		t.Errorf("file-type child prefetch count = %d, want 1", got)
	}
	if got := dirLink.count(); got != 0 {
		t.Errorf("folder-type child prefetch count = %d, want 0", got)
	}
}

func TestPrefetchFileAttrs_CancelledContextStopsDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	children := make(map[string]childEntry)
	var links []*mockPrefetchLink
	for i := 0; i < 100; i++ {
		l := &mockPrefetchLink{}
		links = append(links, l)
		children[fmt.Sprintf("f%d", i)] = childEntry{link: l, mode: syscall.S_IFREG | 0644}
	}
	h := &readdirHandle{children: children}

	prefetchFileAttrs(ctx, h)

	var total int64
	for _, l := range links {
		total += l.count()
	}
	if total != 0 {
		t.Errorf("with pre-cancelled context, dispatched %d prefetches, want 0", total)
	}
}

func TestPrefetchFileAttrs_EmptyIsNoOp(_ *testing.T) {
	h := &readdirHandle{children: map[string]childEntry{}}
	// Should return promptly without panic or deadlock.
	prefetchFileAttrs(context.Background(), h)
}

func TestPrefetchFileAttrs_ManyFilesAllPrefetched(t *testing.T) {
	children := make(map[string]childEntry)
	var links []*mockPrefetchLink
	for i := 0; i < 50; i++ {
		l := &mockPrefetchLink{}
		links = append(links, l)
		children[fmt.Sprintf("f%d", i)] = childEntry{link: l, mode: syscall.S_IFREG | 0644}
	}
	h := &readdirHandle{children: children}

	prefetchFileAttrs(context.Background(), h)

	for i, l := range links {
		if got := l.count(); got != 1 {
			t.Errorf("link[%d] prefetch count = %d, want 1", i, got)
		}
	}
}

// --- Releasedir ---

func TestReleasedir_ClearsState(t *testing.T) {
	d := &DispatchNode{handler: &mockHandler{}, isRoot: true}
	handle := buildReaddirHandle(d, []DirEntry{{Name: "a", Mode: syscall.S_IFREG}})

	handle.Releasedir(context.Background(), 0)

	if handle.children != nil {
		t.Error("Releasedir did not nil children map")
	}
	if handle.entries != nil {
		t.Error("Releasedir did not nil entries slice")
	}
}
