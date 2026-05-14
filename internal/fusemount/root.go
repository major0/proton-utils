//go:build linux

package fusemount

import (
	"context"
	"os"
	"syscall"
	"time"

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
	mtime    time.Time
	uid      uint32
	gid      uint32
}

// NewRoot creates a RootNode backed by the given registry.
// The info parameter provides timestamps from the mountpoint directory.
// Owner uid/gid are captured from the current process at construction time.
func NewRoot(registry *NamespaceRegistry, info os.FileInfo) *RootNode {
	return &RootNode{
		registry: registry,
		mtime:    info.ModTime(),
		uid:      uint32(os.Getuid()), //nolint:gosec // UID fits uint32 on Linux
		gid:      uint32(os.Getgid()), //nolint:gosec // GID fits uint32 on Linux
	}
}

// Getattr returns directory attributes for the root (mode 0500, owned by
// the user that started the process). Write permission is not granted at
// the namespace root — only within individual namespaces.
func (r *RootNode) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFDIR | 0500
	out.Nlink = 2
	out.Ino = 1
	out.Uid = r.uid
	out.Gid = r.gid
	sec := uint64(r.mtime.Unix())        //nolint:gosec // G115: time values are always positive
	nsec := uint32(r.mtime.Nanosecond()) //nolint:gosec // G115: time values are always positive
	out.Atime = sec
	out.Atimensec = nsec
	out.Mtime = sec
	out.Mtimensec = nsec
	out.Ctime = sec
	out.Ctimensec = nsec
	return 0
}

// Readdir returns entries from the registry as S_IFDIR directory entries.
// Always includes . and .. for POSIX compliance.
func (r *RootNode) Readdir(_ context.Context) (fs.DirStream, syscall.Errno) {
	prefixes := r.registry.List()
	entries := make([]fuse.DirEntry, 0, 2+len(prefixes))
	entries = append(entries,
		fuse.DirEntry{Name: ".", Mode: syscall.S_IFDIR, Ino: 1},
		fuse.DirEntry{Name: "..", Mode: syscall.S_IFDIR},
	)
	for _, p := range prefixes {
		entries = append(entries, fuse.DirEntry{Name: p, Mode: fuse.S_IFDIR})
	}
	return fs.NewListDirStream(entries), 0
}

// Lookup returns a DispatchNode for a registered namespace prefix, or ENOENT.
// Populates the EntryOut with the namespace's attributes so the kernel
// caches the correct mode (0500) from the first response.
func (r *RootNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	handler, ok := r.registry.Lookup(name)
	if !ok {
		return nil, syscall.ENOENT
	}

	// Fill EntryOut with the namespace root's attributes.
	attr, errno := handler.Getattr(ctx)
	if errno != 0 {
		return nil, errno
	}
	out.Mode = attr.Mode
	out.Nlink = attr.Nlink
	out.Uid = r.uid
	out.Gid = r.gid

	node := &DispatchNode{handler: handler, isRoot: true, uid: r.uid, gid: r.gid}
	child := r.NewInode(ctx, node, fs.StableAttr{Mode: syscall.S_IFDIR})
	return child, 0
}
