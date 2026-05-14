//go:build linux

// Package redirector implements the system-wide FUSE filesystem mounted at
// /proton. It returns symlinks to per-user mounts based on the calling UID
// from the FUSE request header.
package redirector

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Compile-time interface assertions.
var _ = (fs.NodeGetattrer)((*Root)(nil))
var _ = (fs.NodeLookuper)((*Root)(nil))
var _ = (fs.NodeReaddirer)((*Root)(nil))
var _ = (fs.NodeReadlinker)((*SymlinkNode)(nil))
var _ = (fs.NodeGetattrer)((*SymlinkNode)(nil))

// Root implements the /proton FUSE root directory.
// Every Lookup returns a symlink to /run/user/<uid>/proton/fs/<name>.
type Root struct {
	fs.Inode
	mtime time.Time // mountpoint mtime, used for atime/mtime/ctime
}

// Getattr returns directory attributes for the redirector root.
func (r *Root) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFDIR | 0555
	out.Nlink = 2
	out.Ino = 1
	sec := uint64(r.mtime.Unix())
	nsec := uint32(r.mtime.Nanosecond())
	out.Atime = sec
	out.Atimensec = nsec
	out.Mtime = sec
	out.Mtimensec = nsec
	out.Ctime = sec
	out.Ctimensec = nsec
	return 0
}

// Readdir returns directory entries for the calling user. It reads the
// user's per-user mount directory to discover registered namespaces and
// returns them as symlink entries. Falls back to just . and .. if the
// per-user mount is not available.
func (r *Root) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	dirEntries := []fuse.DirEntry{
		{Name: ".", Mode: syscall.S_IFDIR, Ino: 1},
		{Name: "..", Mode: syscall.S_IFDIR},
	}

	caller, _ := fuse.FromContext(ctx)
	if caller != nil && caller.Uid != 0 {
		userDir := fmt.Sprintf("/run/user/%d/proton/fs", caller.Uid)
		if entries, err := os.ReadDir(userDir); err == nil {
			for _, e := range entries {
				dirEntries = append(dirEntries, fuse.DirEntry{
					Name: e.Name(),
					Mode: syscall.S_IFLNK,
				})
			}
		}
	}

	return fs.NewListDirStream(dirEntries), 0
}

// Lookup returns a symlink node pointing to the calling user's per-user mount.
// Returns ENOENT for UID 0 or when called outside a FUSE context.
func (r *Root) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	caller, _ := fuse.FromContext(ctx)
	if caller == nil || caller.Uid == 0 {
		return nil, syscall.ENOENT
	}

	target := symlinkTarget(caller.Uid, name)
	node := &SymlinkNode{target: target}
	child := r.NewInode(ctx, node, fs.StableAttr{Mode: syscall.S_IFLNK})
	return child, 0
}

// SymlinkNode represents a symlink returned by Root.Lookup.
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
