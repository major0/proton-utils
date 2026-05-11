//go:build linux

// Package redirector implements the system-wide FUSE filesystem mounted at
// /proton. It returns symlinks to per-user mounts based on the calling UID
// from the FUSE request header.
package redirector

import (
	"context"
	"fmt"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Compile-time interface assertions.
var _ = (fs.NodeGetattrer)((*RedirectorRoot)(nil))
var _ = (fs.NodeLookuper)((*RedirectorRoot)(nil))
var _ = (fs.NodeReaddirer)((*RedirectorRoot)(nil))
var _ = (fs.NodeReadlinker)((*SymlinkNode)(nil))
var _ = (fs.NodeGetattrer)((*SymlinkNode)(nil))

// RedirectorRoot implements the /proton FUSE root directory.
// Every Lookup returns a symlink to /run/user/<uid>/proton/fs/<name>.
type RedirectorRoot struct {
	fs.Inode
}

// Getattr returns directory attributes for the redirector root.
func (r *RedirectorRoot) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFDIR | 0555
	out.Nlink = 2
	return 0
}

// Readdir returns an empty directory listing. The kernel follows symlinks
// from Lookup to discover contents.
func (r *RedirectorRoot) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return fs.NewListDirStream(nil), 0
}

// Lookup returns a symlink node pointing to the calling user's per-user mount.
// Returns ENOENT for UID 0 to prevent symlink loops.
func (r *RedirectorRoot) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	caller, _ := fuse.FromContext(ctx)
	uid := caller.Uid
	if uid == 0 {
		return nil, syscall.ENOENT
	}

	target := symlinkTarget(uid, name)
	node := &SymlinkNode{target: target}
	child := r.NewInode(ctx, node, fs.StableAttr{Mode: syscall.S_IFLNK})
	return child, 0
}

// SymlinkNode represents a symlink returned by RedirectorRoot.Lookup.
type SymlinkNode struct {
	fs.Inode
	target string
}

// Readlink returns the symlink target path.
func (s *SymlinkNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	return []byte(s.target), 0
}

// Getattr returns symlink attributes including the target length as size.
func (s *SymlinkNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFLNK | 0777
	out.Size = uint64(len(s.target))
	return 0
}

// symlinkTarget constructs the per-user mount path for the given UID and name.
func symlinkTarget(uid uint32, name string) string {
	return fmt.Sprintf("/run/user/%d/proton/fs/%s", uid, name)
}
