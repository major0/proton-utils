//go:build linux

package fusemount

import (
	"context"
	"syscall"
	"testing"

	"github.com/hanwen/go-fuse/v2/fuse"
)

// mockNode implements Node for testing.
type mockNode struct {
	attr Attr
}

func (m *mockNode) Getattr(_ context.Context) (Attr, syscall.Errno) {
	return m.attr, 0
}

// mockDirNode implements DirNode for testing.
type mockDirNode struct {
	attr    Attr
	entries []DirEntry
	nodes   map[string]Node
}

func (m *mockDirNode) Getattr(_ context.Context) (Attr, syscall.Errno) {
	return m.attr, 0
}

func (m *mockDirNode) Lookup(_ context.Context, name string) (Node, syscall.Errno) {
	if n, ok := m.nodes[name]; ok {
		return n, 0
	}
	return nil, syscall.ENOENT
}

func (m *mockDirNode) Readdir(_ context.Context) ([]DirEntry, syscall.Errno) {
	return m.entries, 0
}

// mockCreatorHandler implements NamespaceHandler + NodeCreator.
type mockCreatorHandler struct {
	mockHandler
}

func (m *mockCreatorHandler) Create(_ context.Context, _ string, _ uint32, _ uint32) (Node, FileHandle, syscall.Errno) {
	return &mockNode{attr: Attr{Mode: syscall.S_IFREG | 0644}}, nil, 0
}

// mockMkdirerHandler implements NamespaceHandler + NodeMkdirer.
type mockMkdirerHandler struct {
	mockHandler
}

func (m *mockMkdirerHandler) Mkdir(_ context.Context, _ string, _ uint32) (Node, syscall.Errno) {
	return &mockNode{attr: Attr{Mode: syscall.S_IFDIR | 0755}}, 0
}

// mockRemoverHandler implements NamespaceHandler + NodeRemover.
type mockRemoverHandler struct {
	mockHandler
}

func (m *mockRemoverHandler) Unlink(_ context.Context, _ string) syscall.Errno {
	return 0
}

func (m *mockRemoverHandler) Rmdir(_ context.Context, _ string) syscall.Errno {
	return 0
}

// panicHandler panics on every method call.
type panicHandler struct {
	msg string
}

func (p *panicHandler) Lookup(_ context.Context, _ string) (Node, syscall.Errno) {
	panic(p.msg)
}

func (p *panicHandler) Readdir(_ context.Context) ([]DirEntry, syscall.Errno) {
	panic(p.msg)
}

func (p *panicHandler) Getattr(_ context.Context) (Attr, syscall.Errno) {
	panic(p.msg)
}

func TestDispatchNodeGetattr_NamespaceRoot(t *testing.T) {
	h := &mockHandler{attr: Attr{Mode: syscall.S_IFDIR | 0755, Nlink: 2}}
	d := &DispatchNode{handler: h, isRoot: true}

	var out fuse.AttrOut
	errno := d.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr returned errno %d", errno)
	}
	if out.Mode != syscall.S_IFDIR|0755 {
		t.Errorf("Mode = %o, want %o", out.Mode, syscall.S_IFDIR|0755)
	}
	if out.Nlink != 2 {
		t.Errorf("Nlink = %d, want 2", out.Nlink)
	}
}

func TestDispatchNodeGetattr_ChildNode(t *testing.T) {
	h := &mockHandler{}
	n := &mockNode{attr: Attr{Mode: syscall.S_IFREG | 0644, Size: 42}}
	d := &DispatchNode{handler: h, node: n, isRoot: false}

	var out fuse.AttrOut
	errno := d.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr returned errno %d", errno)
	}
	if out.Mode != syscall.S_IFREG|0644 {
		t.Errorf("Mode = %o, want %o", out.Mode, syscall.S_IFREG|0644)
	}
	if out.Size != 42 {
		t.Errorf("Size = %d, want 42", out.Size)
	}
}

func TestDispatchNodeGetattr_TimestampPropagation(t *testing.T) {
	h := &mockHandler{}
	n := &mockNode{attr: Attr{
		Mode:  syscall.S_IFREG | 0400,
		Size:  1024,
		Nlink: 1,
		Mtime: 1700000000,
		Ctime: 1699000000,
		Atime: 1700100000,
	}}
	d := &DispatchNode{handler: h, node: n, isRoot: false}

	var out fuse.AttrOut
	errno := d.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr returned errno %d", errno)
	}
	if out.Mtime != 1700000000 {
		t.Errorf("Mtime = %d, want 1700000000", out.Mtime)
	}
	if out.Ctime != 1699000000 {
		t.Errorf("Ctime = %d, want 1699000000", out.Ctime)
	}
	if out.Atime != 1700100000 {
		t.Errorf("Atime = %d, want 1700100000", out.Atime)
	}
}

func TestDispatchNodeGetattr_ZeroTimestamps(t *testing.T) {
	h := &mockHandler{}
	n := &mockNode{attr: Attr{
		Mode:  syscall.S_IFDIR | 0500,
		Nlink: 2,
		// Mtime, Ctime, Atime left at zero (default).
	}}
	d := &DispatchNode{handler: h, node: n, isRoot: false}

	var out fuse.AttrOut
	errno := d.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr returned errno %d", errno)
	}
	if out.Mtime != 0 {
		t.Errorf("Mtime = %d, want 0 (not set)", out.Mtime)
	}
	if out.Ctime != 0 {
		t.Errorf("Ctime = %d, want 0 (not set)", out.Ctime)
	}
	if out.Atime != 0 {
		t.Errorf("Atime = %d, want 0 (not set)", out.Atime)
	}
}

func TestDispatchNodeGetattr_TimestampPropagation_RootHandler(t *testing.T) {
	h := &mockHandler{attr: Attr{
		Mode:  syscall.S_IFDIR | 0755,
		Nlink: 2,
		Mtime: 1680000000,
		Ctime: 1670000000,
		Atime: 1685000000,
	}}
	d := &DispatchNode{handler: h, isRoot: true}

	var out fuse.AttrOut
	errno := d.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr returned errno %d", errno)
	}
	if out.Mtime != 1680000000 {
		t.Errorf("Mtime = %d, want 1680000000", out.Mtime)
	}
	if out.Ctime != 1670000000 {
		t.Errorf("Ctime = %d, want 1670000000", out.Ctime)
	}
	if out.Atime != 1685000000 {
		t.Errorf("Atime = %d, want 1685000000", out.Atime)
	}
}

func TestDispatchNodeReaddir_NamespaceRoot(t *testing.T) {
	h := &mockHandler{entries: []DirEntry{
		{Name: "file1.txt", Mode: syscall.S_IFREG},
		{Name: "subdir", Mode: syscall.S_IFDIR},
	}}
	d := &DispatchNode{handler: h, isRoot: true}

	stream, errno := d.Readdir(context.Background())
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
	if entries[0].Name != "file1.txt" {
		t.Errorf("entries[0].Name = %q, want %q", entries[0].Name, "file1.txt")
	}
}

func TestDispatchNodeReaddir_ChildDirNode(t *testing.T) {
	h := &mockHandler{}
	dir := &mockDirNode{
		entries: []DirEntry{{Name: "child.txt", Mode: syscall.S_IFREG}},
	}
	d := &DispatchNode{handler: h, node: dir, isRoot: false}

	stream, errno := d.Readdir(context.Background())
	if errno != 0 {
		t.Fatalf("Readdir returned errno %d", errno)
	}

	var entries []fuse.DirEntry
	for stream.HasNext() {
		e, _ := stream.Next()
		entries = append(entries, e)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestDispatchNodeReaddir_NonDirNode_ReturnsENOSYS(t *testing.T) {
	h := &mockHandler{}
	n := &mockNode{attr: Attr{Mode: syscall.S_IFREG | 0644}}
	d := &DispatchNode{handler: h, node: n, isRoot: false}

	_, errno := d.Readdir(context.Background())
	if errno != syscall.ENOSYS {
		t.Errorf("Readdir on non-dir node returned errno %d, want ENOSYS (%d)", errno, syscall.ENOSYS)
	}
}

func TestDispatchNodeCreate_Supported(t *testing.T) {
	// Create requires a mounted FUSE tree for NewInode. We verify the
	// capability is detected (not ENOSYS) by checking the handler is
	// type-asserted correctly. The panic recovery catches the nil bridge.
	h := &mockCreatorHandler{}
	d := &DispatchNode{handler: h, isRoot: true}

	_, _, _, errno := d.Create(context.Background(), "newfile", 0, 0644, &fuse.EntryOut{})
	// Without a FUSE bridge, NewInode panics and we recover with EIO.
	// This confirms the capability IS detected (not ENOSYS).
	if errno == syscall.ENOSYS {
		t.Fatal("Create should detect NodeCreator capability, got ENOSYS")
	}
}

func TestDispatchNodeCreate_Unsupported(t *testing.T) {
	h := &mockHandler{}
	d := &DispatchNode{handler: h, isRoot: true}

	_, _, _, errno := d.Create(context.Background(), "newfile", 0, 0644, &fuse.EntryOut{})
	if errno != syscall.ENOSYS {
		t.Errorf("Create on handler without NodeCreator returned errno %d, want ENOSYS", errno)
	}
}

func TestDispatchNodeMkdir_Supported(t *testing.T) {
	// Mkdir requires a mounted FUSE tree for NewInode. We verify the
	// capability is detected (not ENOSYS) by checking the handler is
	// type-asserted correctly. The panic recovery catches the nil bridge.
	h := &mockMkdirerHandler{}
	d := &DispatchNode{handler: h, isRoot: true}

	_, errno := d.Mkdir(context.Background(), "newdir", 0755, &fuse.EntryOut{})
	// Without a FUSE bridge, NewInode panics and we recover with EIO.
	// This confirms the capability IS detected (not ENOSYS).
	if errno == syscall.ENOSYS {
		t.Fatal("Mkdir should detect NodeMkdirer capability, got ENOSYS")
	}
}

func TestDispatchNodeMkdir_Unsupported(t *testing.T) {
	h := &mockHandler{}
	d := &DispatchNode{handler: h, isRoot: true}

	_, errno := d.Mkdir(context.Background(), "newdir", 0755, &fuse.EntryOut{})
	if errno != syscall.ENOSYS {
		t.Errorf("Mkdir on handler without NodeMkdirer returned errno %d, want ENOSYS", errno)
	}
}

func TestDispatchNodeUnlink_Supported(t *testing.T) {
	h := &mockRemoverHandler{}
	d := &DispatchNode{handler: h, isRoot: true}

	errno := d.Unlink(context.Background(), "file.txt")
	if errno != 0 {
		t.Fatalf("Unlink returned errno %d", errno)
	}
}

func TestDispatchNodeUnlink_Unsupported(t *testing.T) {
	h := &mockHandler{}
	d := &DispatchNode{handler: h, isRoot: true}

	errno := d.Unlink(context.Background(), "file.txt")
	if errno != syscall.ENOSYS {
		t.Errorf("Unlink on handler without NodeRemover returned errno %d, want ENOSYS", errno)
	}
}

func TestDispatchNodeRmdir_Unsupported(t *testing.T) {
	h := &mockHandler{}
	d := &DispatchNode{handler: h, isRoot: true}

	errno := d.Rmdir(context.Background(), "dir")
	if errno != syscall.ENOSYS {
		t.Errorf("Rmdir on handler without NodeRemover returned errno %d, want ENOSYS", errno)
	}
}

func TestDispatchNodeRename_Unsupported(t *testing.T) {
	h := &mockHandler{}
	d := &DispatchNode{handler: h, isRoot: true}

	errno := d.Rename(context.Background(), "old", nil, "new", 0)
	if errno != syscall.ENOSYS {
		t.Errorf("Rename on handler without NodeRenamer returned errno %d, want ENOSYS", errno)
	}
}

func TestDispatchNodePanicRecovery_Getattr(t *testing.T) {
	h := &panicHandler{msg: "test panic in getattr"}
	d := &DispatchNode{handler: h, isRoot: true}

	var out fuse.AttrOut
	errno := d.Getattr(context.Background(), nil, &out)
	if errno != syscall.EIO {
		t.Errorf("Getattr after panic returned errno %d, want EIO (%d)", errno, syscall.EIO)
	}
}

func TestDispatchNodePanicRecovery_Readdir(t *testing.T) {
	h := &panicHandler{msg: "test panic in readdir"}
	d := &DispatchNode{handler: h, isRoot: true}

	_, errno := d.Readdir(context.Background())
	if errno != syscall.EIO {
		t.Errorf("Readdir after panic returned errno %d, want EIO (%d)", errno, syscall.EIO)
	}
}

func TestDispatchNodePanicRecovery_Lookup(t *testing.T) {
	h := &panicHandler{msg: "test panic in lookup"}
	d := &DispatchNode{handler: h, isRoot: true}

	_, errno := d.Lookup(context.Background(), "anything", &fuse.EntryOut{})
	if errno != syscall.EIO {
		t.Errorf("Lookup after panic returned errno %d, want EIO (%d)", errno, syscall.EIO)
	}
}

func TestDispatchNodeWrite_Unsupported(t *testing.T) {
	h := &mockHandler{}
	n := &mockNode{attr: Attr{Mode: syscall.S_IFREG | 0644}}
	d := &DispatchNode{handler: h, node: n, isRoot: false}

	_, errno := d.Write(context.Background(), nil, []byte("data"), 0)
	if errno != syscall.ENOSYS {
		t.Errorf("Write on node without NodeWriter returned errno %d, want ENOSYS", errno)
	}
}

func TestDispatchNodeRead_Unsupported(t *testing.T) {
	h := &mockHandler{}
	n := &mockNode{attr: Attr{Mode: syscall.S_IFREG | 0644}}
	d := &DispatchNode{handler: h, node: n, isRoot: false}

	_, errno := d.Read(context.Background(), nil, make([]byte, 10), 0)
	if errno != syscall.ENOSYS {
		t.Errorf("Read on node without NodeReader returned errno %d, want ENOSYS", errno)
	}
}

func TestDispatchNodeOpen_Unsupported(t *testing.T) {
	h := &mockHandler{}
	n := &mockNode{attr: Attr{Mode: syscall.S_IFREG | 0644}}
	d := &DispatchNode{handler: h, node: n, isRoot: false}

	_, _, errno := d.Open(context.Background(), 0)
	if errno != syscall.ENOSYS {
		t.Errorf("Open on node without NodeReader/NodeWriter returned errno %d, want ENOSYS", errno)
	}
}

func TestDispatchNodeSetattr_NamespaceRoot_ReturnsEPERM(t *testing.T) {
	h := &mockHandler{attr: Attr{Mode: syscall.S_IFDIR | 0500, Nlink: 2}}
	d := &DispatchNode{handler: h, isRoot: true}

	var out fuse.AttrOut
	in := fuse.SetAttrIn{}
	errno := d.Setattr(context.Background(), nil, &in, &out)
	if errno != syscall.EPERM {
		t.Errorf("Setattr on namespace root returned errno %d, want EPERM (%d)", errno, syscall.EPERM)
	}
}

func TestDispatchNodeSetattr_ChildNode_ReturnsENOSYS(t *testing.T) {
	h := &mockHandler{}
	n := &mockNode{attr: Attr{Mode: syscall.S_IFREG | 0644}}
	d := &DispatchNode{handler: h, node: n, isRoot: false}

	var out fuse.AttrOut
	in := fuse.SetAttrIn{}
	errno := d.Setattr(context.Background(), nil, &in, &out)
	if errno != syscall.ENOSYS {
		t.Errorf("Setattr on child node returned errno %d, want ENOSYS (%d)", errno, syscall.ENOSYS)
	}
}

func TestDispatchNodeGetattr_NamespaceRoot_SetsUidGid(t *testing.T) {
	h := &mockHandler{attr: Attr{Mode: syscall.S_IFDIR | 0500, Nlink: 2}}
	d := &DispatchNode{handler: h, isRoot: true, uid: 1000, gid: 1000}

	var out fuse.AttrOut
	errno := d.Getattr(context.Background(), nil, &out)
	if errno != 0 {
		t.Fatalf("Getattr returned errno %d", errno)
	}
	if out.Uid != 1000 {
		t.Errorf("Uid = %d, want 1000", out.Uid)
	}
	if out.Gid != 1000 {
		t.Errorf("Gid = %d, want 1000", out.Gid)
	}
}
