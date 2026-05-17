//go:build linux

package fusemount

import (
	"context"
	"log"
	"runtime/debug"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Compile-time interface assertions.
var _ = (fs.NodeGetattrer)((*DispatchNode)(nil))
var _ = (fs.NodeSetattrer)((*DispatchNode)(nil))
var _ = (fs.NodeLookuper)((*DispatchNode)(nil))
var _ = (fs.NodeReaddirer)((*DispatchNode)(nil))
var _ = (fs.NodeCreater)((*DispatchNode)(nil))
var _ = (fs.NodeMkdirer)((*DispatchNode)(nil))
var _ = (fs.NodeOpener)((*DispatchNode)(nil))
var _ = (fs.NodeReader)((*DispatchNode)(nil))
var _ = (fs.NodeWriter)((*DispatchNode)(nil))
var _ = (fs.NodeReleaser)((*DispatchNode)(nil))
var _ = (fs.NodeFsyncer)((*DispatchNode)(nil))
var _ = (fs.NodeUnlinker)((*DispatchNode)(nil))
var _ = (fs.NodeRmdirer)((*DispatchNode)(nil))
var _ = (fs.NodeRenamer)((*DispatchNode)(nil))

// DispatchNode bridges a namespace handler's Node to go-fuse's InodeEmbedder.
// It operates in two modes:
//   - isRoot=true: wraps a NamespaceHandler (the namespace root directory)
//   - isRoot=false: wraps a Node returned by handler.Lookup()
type DispatchNode struct {
	fs.Inode
	handler NamespaceHandler // always set (for capability checks)
	node    Node             // nil when isRoot=true
	isRoot  bool
	uid     uint32 // owner UID — propagated to all child nodes
	gid     uint32 // owner GID — propagated to all child nodes
}

// checkAccess verifies the calling process UID matches the daemon owner.
// Returns EPERM if the caller is any other user, including root.
// Encrypted data belongs to the daemon owner — no other user gets access.
// checkAccess verifies the calling process UID matches the daemon owner.
// Returns EPERM if the caller is not the daemon owner. Only the user who
// started proton-fuse may access namespace contents.
//
// The access gate is at the namespace boundary: Getattr on the namespace
// root is allowed for any caller (so the redirector can stat "drive/"),
// but Readdir/Lookup/Open and all operations on child nodes require the
// owner UID.
func (d *DispatchNode) checkAccess(ctx context.Context) syscall.Errno {
	caller, ok := fuse.FromContext(ctx)
	if !ok {
		return 0 // no caller info available — allow (internal call)
	}
	if caller.Uid != d.uid {
		return syscall.EPERM
	}
	return 0
}

// Getattr returns file attributes, delegating to the handler or node.
// For namespace roots (isRoot=true), Getattr is allowed without access
// check so the redirector and mount-root ls can stat the "drive/" entry.
func (d *DispatchNode) Getattr(ctx context.Context, _ fs.FileHandle, out *fuse.AttrOut) (errno syscall.Errno) {
	if !d.isRoot {
		if err := d.checkAccess(ctx); err != 0 {
			return err
		}
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Getattr: %v\n%s", r, debug.Stack())
			errno = syscall.EIO
		}
	}()

	var attr Attr
	if d.isRoot {
		attr, errno = d.handler.Getattr(ctx)
	} else {
		attr, errno = d.node.Getattr(ctx)
	}
	if errno != 0 {
		return errno
	}
	out.Mode = attr.Mode
	out.Size = attr.Size
	out.Nlink = attr.Nlink
	out.Mtime = attr.Mtime
	out.Ctime = attr.Ctime
	out.Atime = attr.Atime
	out.Uid = d.uid
	out.Gid = d.gid
	return 0
}

// Setattr rejects all attribute changes on namespace root directories.
// Namespace roots have fixed permissions (0500) that cannot be modified.
// For non-root nodes, returns success (no-op) — attribute changes like
// chmod/chown are silently ignored since Proton Drive manages metadata
// server-side. Truncation via O_TRUNC is handled in Open/Create, not here.
// Note: ENOSYS is avoided because go-fuse caches it per-connection,
// which would disable setattr for the entire filesystem.
func (d *DispatchNode) Setattr(_ context.Context, _ fs.FileHandle, _ *fuse.SetAttrIn, _ *fuse.AttrOut) syscall.Errno {
	if d.isRoot {
		return syscall.EPERM
	}
	return 0
}

// Readdir returns directory entries, delegating to the handler or DirNode.
func (d *DispatchNode) Readdir(ctx context.Context) (stream fs.DirStream, errno syscall.Errno) {
	if err := d.checkAccess(ctx); err != 0 {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Readdir: %v\n%s", r, debug.Stack())
			stream = nil
			errno = syscall.EIO
		}
	}()

	var entries []DirEntry
	if d.isRoot {
		entries, errno = d.handler.Readdir(ctx)
	} else {
		dir, ok := d.node.(DirNode)
		if !ok {
			return fs.NewListDirStream(nil), syscall.ENOTDIR
		}
		entries, errno = dir.Readdir(ctx)
	}
	if errno != 0 {
		return nil, errno
	}

	fuseEntries := make([]fuse.DirEntry, 0, 2+len(entries))
	fuseEntries = append(fuseEntries,
		fuse.DirEntry{Name: ".", Mode: syscall.S_IFDIR},
		fuse.DirEntry{Name: "..", Mode: syscall.S_IFDIR},
	)
	for _, e := range entries {
		fuseEntries = append(fuseEntries, fuse.DirEntry{Name: e.Name, Mode: e.Mode})
	}
	return fs.NewListDirStream(fuseEntries), 0
}

// Lookup finds a child node by name, delegating to the handler or DirNode.
func (d *DispatchNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (child *fs.Inode, errno syscall.Errno) {
	if err := d.checkAccess(ctx); err != 0 {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Lookup: %v\n%s", r, debug.Stack())
			child = nil
			errno = syscall.EIO
		}
	}()

	var n Node
	if d.isRoot {
		n, errno = d.handler.Lookup(ctx, name)
	} else {
		dir, ok := d.node.(DirNode)
		if !ok {
			return nil, syscall.ENOTDIR
		}
		n, errno = dir.Lookup(ctx, name)
	}
	if errno != 0 {
		return nil, errno
	}

	// Get child attributes and populate EntryOut so the kernel caches
	// correct mode, uid, gid from the first Lookup response.
	attr, attrErr := n.Getattr(ctx)
	mode := uint32(syscall.S_IFREG)
	if attrErr == 0 {
		mode = attr.Mode & syscall.S_IFMT
		out.Mode = attr.Mode
		out.Size = attr.Size
		out.Nlink = attr.Nlink
		out.Mtime = attr.Mtime
		out.Ctime = attr.Ctime
		out.Atime = attr.Atime
		out.Uid = d.uid
		out.Gid = d.gid
	}

	childNode := &DispatchNode{handler: d.handler, node: n, isRoot: false, uid: d.uid, gid: d.gid}
	inode := d.NewInode(ctx, childNode, fs.StableAttr{Mode: mode})
	return inode, 0
}

// Create delegates to NodeCreator if the handler supports it.
func (d *DispatchNode) Create(ctx context.Context, name string, flags uint32, mode uint32, _ *fuse.EntryOut) (inode *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	if err := d.checkAccess(ctx); err != 0 {
		return nil, nil, 0, err
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Create: %v\n%s", r, debug.Stack())
			inode = nil
			fh = nil
			errno = syscall.EIO
		}
	}()

	var target interface{}
	if d.isRoot {
		target = d.handler
	} else {
		target = d.node
	}

	creator, ok := target.(NodeCreator)
	if !ok {
		return nil, nil, 0, syscall.EPERM
	}

	n, handle, errno := creator.Create(ctx, name, flags, mode)
	if errno != 0 {
		return nil, nil, 0, errno
	}

	childNode := &DispatchNode{handler: d.handler, node: n, isRoot: false, uid: d.uid, gid: d.gid}
	child := d.NewInode(ctx, childNode, fs.StableAttr{Mode: syscall.S_IFREG})
	return child, &dispatchFileHandle{handle: handle}, 0, 0
}

// Mkdir delegates to NodeMkdirer if the handler supports it.
func (d *DispatchNode) Mkdir(ctx context.Context, name string, mode uint32, _ *fuse.EntryOut) (inode *fs.Inode, errno syscall.Errno) {
	if err := d.checkAccess(ctx); err != 0 {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Mkdir: %v\n%s", r, debug.Stack())
			inode = nil
			errno = syscall.EIO
		}
	}()

	var target interface{}
	if d.isRoot {
		target = d.handler
	} else {
		target = d.node
	}

	mkdirer, ok := target.(NodeMkdirer)
	if !ok {
		return nil, syscall.EPERM
	}

	n, errno := mkdirer.Mkdir(ctx, name, mode)
	if errno != 0 {
		return nil, errno
	}

	childNode := &DispatchNode{handler: d.handler, node: n, isRoot: false, uid: d.uid, gid: d.gid}
	child := d.NewInode(ctx, childNode, fs.StableAttr{Mode: syscall.S_IFDIR})
	return child, 0
}

// Open delegates to NodeOpener, NodeReader, or NodeWriter if the node supports it.
func (d *DispatchNode) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	if err := d.checkAccess(ctx); err != 0 {
		return nil, 0, err
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Open: %v\n%s", r, debug.Stack())
			fh = nil
			errno = syscall.EIO
		}
	}()

	if d.isRoot || d.node == nil {
		return nil, 0, syscall.EPERM
	}

	// If node supports NodeOpener, call it to get a handle with per-open state.
	if opener, ok := d.node.(NodeOpener); ok {
		handle, errno := opener.Open(ctx, flags)
		if errno != 0 {
			return nil, 0, errno
		}
		return &dispatchFileHandle{handle: handle}, 0, 0
	}

	// NodeReader/NodeWriter without NodeOpener — return nil-handle.
	if _, ok := d.node.(NodeReader); ok {
		return &dispatchFileHandle{}, 0, 0
	}
	if _, ok := d.node.(NodeWriter); ok {
		return &dispatchFileHandle{}, 0, 0
	}

	return nil, 0, syscall.EPERM
}

// Release delegates to NodeReleaser if the node supports it.
func (d *DispatchNode) Release(ctx context.Context, f fs.FileHandle) (errno syscall.Errno) {
	if d.isRoot || d.node == nil {
		return 0
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Release: %v\n%s", r, debug.Stack())
			errno = syscall.EIO
		}
	}()

	releaser, ok := d.node.(NodeReleaser)
	if !ok {
		return 0
	}

	var handle FileHandle
	if dfh, ok := f.(*dispatchFileHandle); ok {
		handle = dfh.handle
	}
	return releaser.Release(ctx, handle)
}

// Fsync delegates to NodeFsyncer if the node supports it.
func (d *DispatchNode) Fsync(ctx context.Context, f fs.FileHandle, flags uint32) (errno syscall.Errno) {
	if err := d.checkAccess(ctx); err != 0 {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Fsync: %v\n%s", r, debug.Stack())
			errno = syscall.EIO
		}
	}()

	if d.isRoot || d.node == nil {
		return 0 // no-op for directories
	}

	fsyncer, ok := d.node.(NodeFsyncer)
	if !ok {
		return 0 // no-op if not supported
	}

	var handle FileHandle
	if dfh, ok := f.(*dispatchFileHandle); ok {
		handle = dfh.handle
	}

	return fsyncer.Fsync(ctx, handle, flags)
}

// Read delegates to NodeReader if the node supports it.
func (d *DispatchNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (res fuse.ReadResult, errno syscall.Errno) {
	if err := d.checkAccess(ctx); err != 0 {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Read: %v\n%s", r, debug.Stack())
			res = nil
			errno = syscall.EIO
		}
	}()

	if d.isRoot || d.node == nil {
		return nil, syscall.EPERM
	}

	reader, ok := d.node.(NodeReader)
	if !ok {
		return nil, syscall.EBADF
	}

	var handle FileHandle
	if dfh, ok := f.(*dispatchFileHandle); ok {
		handle = dfh.handle
	}

	n, errno := reader.Read(ctx, handle, dest, off)
	if errno != 0 {
		return nil, errno
	}
	return fuse.ReadResultData(dest[:n]), 0
}

// Write delegates to NodeWriter if the node supports it.
func (d *DispatchNode) Write(ctx context.Context, f fs.FileHandle, data []byte, off int64) (written uint32, errno syscall.Errno) {
	if err := d.checkAccess(ctx); err != 0 {
		return 0, err
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Write: %v\n%s", r, debug.Stack())
			written = 0
			errno = syscall.EIO
		}
	}()

	if d.isRoot || d.node == nil {
		return 0, syscall.EPERM
	}

	writer, ok := d.node.(NodeWriter)
	if !ok {
		return 0, syscall.EBADF
	}

	var handle FileHandle
	if dfh, ok := f.(*dispatchFileHandle); ok {
		handle = dfh.handle
	}

	return writer.Write(ctx, handle, data, off)
}

// Unlink delegates to NodeRemover if the handler supports it.
func (d *DispatchNode) Unlink(ctx context.Context, name string) (errno syscall.Errno) {
	if err := d.checkAccess(ctx); err != 0 {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Unlink: %v\n%s", r, debug.Stack())
			errno = syscall.EIO
		}
	}()

	var target interface{}
	if d.isRoot {
		target = d.handler
	} else {
		target = d.node
	}

	remover, ok := target.(NodeRemover)
	if !ok {
		return syscall.EPERM
	}

	return remover.Unlink(ctx, name)
}

// Rmdir delegates to NodeRemover if the handler supports it.
func (d *DispatchNode) Rmdir(ctx context.Context, name string) (errno syscall.Errno) {
	if err := d.checkAccess(ctx); err != 0 {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Rmdir: %v\n%s", r, debug.Stack())
			errno = syscall.EIO
		}
	}()

	var target interface{}
	if d.isRoot {
		target = d.handler
	} else {
		target = d.node
	}

	remover, ok := target.(NodeRemover)
	if !ok {
		return syscall.EPERM
	}

	return remover.Rmdir(ctx, name)
}

// Rename delegates to NodeRenamer if the handler supports it.
func (d *DispatchNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, _ uint32) (errno syscall.Errno) {
	if err := d.checkAccess(ctx); err != 0 {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Rename: %v\n%s", r, debug.Stack())
			errno = syscall.EIO
		}
	}()

	var target interface{}
	if d.isRoot {
		target = d.handler
	} else {
		target = d.node
	}

	renamer, ok := target.(NodeRenamer)
	if !ok {
		return syscall.EPERM
	}

	// Extract the Node from the new parent DispatchNode if possible.
	var newParentNode Node
	if dp, ok := newParent.(*DispatchNode); ok {
		newParentNode = dp.node
	}

	return renamer.Rename(ctx, name, newParentNode, newName)
}

// dispatchFileHandle wraps a handler's FileHandle for go-fuse.
type dispatchFileHandle struct {
	handle FileHandle
}
