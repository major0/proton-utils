//go:build linux

package fusemount

import (
	"context"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Compile-time interface assertions.
var _ = (fs.NodeGetattrer)((*RootNode)(nil))
var _ = (fs.NodeLookuper)((*RootNode)(nil))
var _ = (fs.NodeReaddirer)((*RootNode)(nil))

// RootNode implements the FUSE root directory for the per-user mount.
// It dispatches Lookup to registered namespace handlers.
type RootNode struct {
	fs.Inode
	registry *NamespaceRegistry
}

// NewRoot creates a RootNode backed by the given registry.
func NewRoot(registry *NamespaceRegistry) *RootNode {
	return &RootNode{registry: registry}
}

// Getattr returns directory attributes for the root (mode 0555).
func (r *RootNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFDIR | 0555
	out.Nlink = 2
	return 0
}

// Readdir returns entries from the registry as S_IFDIR directory entries.
func (r *RootNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	prefixes := r.registry.List()
	entries := make([]fuse.DirEntry, 0, len(prefixes))
	for _, p := range prefixes {
		entries = append(entries, fuse.DirEntry{Name: p, Mode: fuse.S_IFDIR})
	}
	return fs.NewListDirStream(entries), 0
}

// Lookup returns a DispatchNode for a registered namespace prefix, or ENOENT.
func (r *RootNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	handler, ok := r.registry.Lookup(name)
	if !ok {
		return nil, syscall.ENOENT
	}
	node := &DispatchNode{handler: handler, isRoot: true}
	child := r.NewInode(ctx, node, fs.StableAttr{Mode: syscall.S_IFDIR})
	return child, 0
}
