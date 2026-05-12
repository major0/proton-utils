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
var _ = (fs.NodeLookuper)((*DispatchNode)(nil))
var _ = (fs.NodeReaddirer)((*DispatchNode)(nil))

// DispatchNode bridges a namespace handler's Node to go-fuse's InodeEmbedder.
// It operates in two modes:
//   - isRoot=true: wraps a NamespaceHandler (the namespace root directory)
//   - isRoot=false: wraps a Node returned by handler.Lookup()
type DispatchNode struct {
	fs.Inode
	handler NamespaceHandler // always set (for capability checks)
	node    Node             // nil when isRoot=true
	isRoot  bool
}

// Getattr returns file attributes, delegating to the handler or node.
func (d *DispatchNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) (errno syscall.Errno) {
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
	return 0
}

// Readdir returns directory entries, delegating to the handler or DirNode.
func (d *DispatchNode) Readdir(ctx context.Context) (stream fs.DirStream, errno syscall.Errno) {
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
			return fs.NewListDirStream(nil), syscall.ENOSYS
		}
		entries, errno = dir.Readdir(ctx)
	}
	if errno != 0 {
		return nil, errno
	}

	fuseEntries := make([]fuse.DirEntry, 0, len(entries))
	for _, e := range entries {
		fuseEntries = append(fuseEntries, fuse.DirEntry{Name: e.Name, Mode: e.Mode})
	}
	return fs.NewListDirStream(fuseEntries), 0
}

// Lookup finds a child node by name, delegating to the handler or DirNode.
func (d *DispatchNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (child *fs.Inode, errno syscall.Errno) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Lookup(%q): %v\n%s", name, r, debug.Stack())
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
			return nil, syscall.ENOSYS
		}
		n, errno = dir.Lookup(ctx, name)
	}
	if errno != 0 {
		return nil, errno
	}

	// Determine mode for the child inode.
	attr, attrErr := n.Getattr(ctx)
	mode := uint32(syscall.S_IFREG)
	if attrErr == 0 {
		mode = attr.Mode & syscall.S_IFMT
	}

	childNode := &DispatchNode{handler: d.handler, node: n, isRoot: false}
	inode := d.NewInode(ctx, childNode, fs.StableAttr{Mode: mode})
	return inode, 0
}

// Create delegates to NodeCreator if the handler supports it.
func (d *DispatchNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (inode *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Create(%q): %v\n%s", name, r, debug.Stack())
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
		return nil, nil, 0, syscall.ENOSYS
	}

	n, handle, errno := creator.Create(ctx, name, flags, mode)
	if errno != 0 {
		return nil, nil, 0, errno
	}

	childNode := &DispatchNode{handler: d.handler, node: n, isRoot: false}
	child := d.NewInode(ctx, childNode, fs.StableAttr{Mode: syscall.S_IFREG})
	return child, &dispatchFileHandle{handle: handle}, 0, 0
}

// Mkdir delegates to NodeMkdirer if the handler supports it.
func (d *DispatchNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (inode *fs.Inode, errno syscall.Errno) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Mkdir(%q): %v\n%s", name, r, debug.Stack())
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
		return nil, syscall.ENOSYS
	}

	n, errno := mkdirer.Mkdir(ctx, name, mode)
	if errno != 0 {
		return nil, errno
	}

	childNode := &DispatchNode{handler: d.handler, node: n, isRoot: false}
	child := d.NewInode(ctx, childNode, fs.StableAttr{Mode: syscall.S_IFDIR})
	return child, 0
}

// Open delegates to NodeReader or NodeWriter if the handler supports it.
func (d *DispatchNode) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Open: %v\n%s", r, debug.Stack())
			fh = nil
			errno = syscall.EIO
		}
	}()

	if d.isRoot || d.node == nil {
		return nil, 0, syscall.ENOSYS
	}

	// Return a handle if the node supports read or write.
	if _, ok := d.node.(NodeReader); ok {
		return &dispatchFileHandle{}, 0, 0
	}
	if _, ok := d.node.(NodeWriter); ok {
		return &dispatchFileHandle{}, 0, 0
	}

	return nil, 0, syscall.ENOSYS
}

// Read delegates to NodeReader if the node supports it.
func (d *DispatchNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (res fuse.ReadResult, errno syscall.Errno) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Read: %v\n%s", r, debug.Stack())
			res = nil
			errno = syscall.EIO
		}
	}()

	if d.isRoot || d.node == nil {
		return nil, syscall.ENOSYS
	}

	reader, ok := d.node.(NodeReader)
	if !ok {
		return nil, syscall.ENOSYS
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
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Write: %v\n%s", r, debug.Stack())
			written = 0
			errno = syscall.EIO
		}
	}()

	if d.isRoot || d.node == nil {
		return 0, syscall.ENOSYS
	}

	writer, ok := d.node.(NodeWriter)
	if !ok {
		return 0, syscall.ENOSYS
	}

	var handle FileHandle
	if dfh, ok := f.(*dispatchFileHandle); ok {
		handle = dfh.handle
	}

	return writer.Write(ctx, handle, data, off)
}

// Unlink delegates to NodeRemover if the handler supports it.
func (d *DispatchNode) Unlink(ctx context.Context, name string) (errno syscall.Errno) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Unlink(%q): %v\n%s", name, r, debug.Stack())
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
		return syscall.ENOSYS
	}

	return remover.Unlink(ctx, name)
}

// Rmdir delegates to NodeRemover if the handler supports it.
func (d *DispatchNode) Rmdir(ctx context.Context, name string) (errno syscall.Errno) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Rmdir(%q): %v\n%s", name, r, debug.Stack())
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
		return syscall.ENOSYS
	}

	return remover.Rmdir(ctx, name)
}

// Rename delegates to NodeRenamer if the handler supports it.
func (d *DispatchNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) (errno syscall.Errno) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in handler Rename(%q → %q): %v\n%s", name, newName, r, debug.Stack())
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
		return syscall.ENOSYS
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
