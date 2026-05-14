//go:build linux

package drive

import (
	"context"
	"errors"
	"log/slog"
	"syscall"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
	"github.com/major0/proton-utils/internal/fusemount"
)

// apiErrno maps an API or context error to the appropriate FUSE errno.
// Context cancellation and deadline exceeded map to EINTR; all other
// errors map to EIO.
func apiErrno(err error) syscall.Errno {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return syscall.EINTR
	}
	return syscall.EIO
}

// ShareDirNode wraps a *drive.Share and implements fusemount.DirNode.
// It exposes the share's root link children as directory entries.
type ShareDirNode struct {
	share  *drive.Share
	client *drive.Client
}

// Compile-time interface assertions.
var _ fusemount.Node = (*ShareDirNode)(nil)
var _ fusemount.DirNode = (*ShareDirNode)(nil)

// Getattr returns directory attributes for the share root.
func (n *ShareDirNode) Getattr(_ context.Context) (fusemount.Attr, syscall.Errno) {
	return fusemount.Attr{
		Mode:  syscall.S_IFDIR | 0500,
		Nlink: 2,
	}, 0
}

// Readdir lists children of the share's root link. Uses the streaming
// Readdir channel to avoid double-decryption — EntryName() decrypts once
// and the result is used directly. Children with decryption errors are skipped.
func (n *ShareDirNode) Readdir(ctx context.Context) ([]fusemount.DirEntry, syscall.Errno) {
	var entries []fusemount.DirEntry
	for de := range n.share.Link.Readdir(ctx) {
		if de.Err != nil {
			slog.Debug("ShareDirNode.Readdir: error from Readdir stream",
				"shareID", n.share.Metadata().ShareID, "error", de.Err)
			return nil, apiErrno(de.Err)
		}
		name, err := de.EntryName()
		if err != nil {
			slog.Debug("ShareDirNode.Readdir: skipping child with decryption error",
				"error", err)
			continue
		}
		// Skip . and .. — FUSE handles these automatically.
		if name == "." || name == ".." {
			continue
		}
		entries = append(entries, fusemount.DirEntry{
			Name: name,
			Mode: linkMode(de.Link),
		})
	}
	return entries, 0
}

// Lookup finds a child by name within the share root. Uses the streaming
// Readdir channel with early termination — cancels remaining work as soon
// as the match is found. Skips children with decryption errors.
func (n *ShareDirNode) Lookup(ctx context.Context, name string) (fusemount.Node, syscall.Errno) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for de := range n.share.Link.Readdir(ctx) {
		if de.Err != nil {
			slog.Debug("ShareDirNode.Lookup: error from Readdir stream",
				"shareID", n.share.Metadata().ShareID, "error", de.Err)
			return nil, apiErrno(de.Err)
		}
		entryName, err := de.EntryName()
		if err != nil {
			// Skip entries with decryption errors.
			continue
		}
		if entryName == "." || entryName == ".." {
			continue
		}
		if entryName == name {
			return linkNode(de.Link, n.client), 0
		}
	}
	return nil, syscall.ENOENT
}

// LinkDirNode wraps a *drive.Link (folder) and implements fusemount.DirNode.
// It exposes the link's children as directory entries.
type LinkDirNode struct {
	link   *drive.Link
	client *drive.Client
}

// Compile-time interface assertions.
var _ fusemount.Node = (*LinkDirNode)(nil)
var _ fusemount.DirNode = (*LinkDirNode)(nil)

// Getattr returns directory attributes for the folder.
func (n *LinkDirNode) Getattr(_ context.Context) (fusemount.Attr, syscall.Errno) {
	return fusemount.Attr{
		Mode:  syscall.S_IFDIR | 0500,
		Nlink: 2,
	}, 0
}

// Readdir lists children of the folder link. Uses the streaming Readdir
// channel to avoid double-decryption — EntryName() decrypts once and the
// result is used directly. Children with decryption errors are skipped.
func (n *LinkDirNode) Readdir(ctx context.Context) ([]fusemount.DirEntry, syscall.Errno) {
	var entries []fusemount.DirEntry
	for de := range n.link.Readdir(ctx) {
		if de.Err != nil {
			slog.Debug("LinkDirNode.Readdir: error from Readdir stream",
				"linkID", n.link.LinkID(), "error", de.Err)
			return nil, apiErrno(de.Err)
		}
		name, err := de.EntryName()
		if err != nil {
			slog.Debug("LinkDirNode.Readdir: skipping child with decryption error",
				"error", err)
			continue
		}
		// Skip . and .. — FUSE handles these automatically.
		if name == "." || name == ".." {
			continue
		}
		entries = append(entries, fusemount.DirEntry{
			Name: name,
			Mode: linkMode(de.Link),
		})
	}
	return entries, 0
}

// Lookup finds a child by name within the folder. Uses the streaming
// Readdir channel with early termination — cancels remaining work as soon
// as the match is found. Skips children with decryption errors.
func (n *LinkDirNode) Lookup(ctx context.Context, name string) (fusemount.Node, syscall.Errno) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for de := range n.link.Readdir(ctx) {
		if de.Err != nil {
			slog.Debug("LinkDirNode.Lookup: error from Readdir stream",
				"linkID", n.link.LinkID(), "error", de.Err)
			return nil, apiErrno(de.Err)
		}
		entryName, err := de.EntryName()
		if err != nil {
			// Skip entries with decryption errors.
			continue
		}
		if entryName == "." || entryName == ".." {
			continue
		}
		if entryName == name {
			return linkNode(de.Link, n.client), 0
		}
	}
	return nil, syscall.ENOENT
}

// linkMode returns the FUSE mode for a link based on its type.
func linkMode(l *drive.Link) uint32 {
	if l.Type() == proton.LinkTypeFolder {
		return syscall.S_IFDIR
	}
	return syscall.S_IFREG
}

// linkNode returns the appropriate fusemount.Node for a link based on its type.
func linkNode(l *drive.Link, client *drive.Client) fusemount.Node {
	if l.Type() == proton.LinkTypeFolder {
		return &LinkDirNode{link: l, client: client}
	}
	return &FileNode{link: l}
}

// FileNode wraps a *drive.Link (file) and implements fusemount.Node.
// Full implementation is provided in a later task.
type FileNode struct {
	link *drive.Link
}

// Compile-time interface assertion.
var _ fusemount.Node = (*FileNode)(nil)

// Getattr returns file attributes including size and timestamps.
func (n *FileNode) Getattr(_ context.Context) (fusemount.Attr, syscall.Errno) {
	//nolint:gosec // Size/ModifyTime/CreateTime are non-negative from API
	return fusemount.Attr{
		Mode:  syscall.S_IFREG | 0400,
		Size:  uint64(n.link.Size()),
		Nlink: 1,
		Mtime: uint64(n.link.ModifyTime()),
		Ctime: uint64(n.link.CreateTime()),
	}, 0
}

// LinkIDDir is the virtual .linkid/ directory that provides O(1) access
// to any link by its LinkID. Implements fusemount.DirNode.
// Full implementation is provided in a later task.
type LinkIDDir struct {
	client *drive.Client
}

// Compile-time interface assertions.
var _ fusemount.Node = (*LinkIDDir)(nil)
var _ fusemount.DirNode = (*LinkIDDir)(nil)

// Getattr returns directory attributes for the .linkid virtual directory.
func (n *LinkIDDir) Getattr(_ context.Context) (fusemount.Attr, syscall.Errno) {
	return fusemount.Attr{
		Mode:  syscall.S_IFDIR | 0500,
		Nlink: 2,
	}, 0
}

// Readdir returns an empty listing — enumerating all LinkIDs is not feasible.
func (n *LinkIDDir) Readdir(_ context.Context) ([]fusemount.DirEntry, syscall.Errno) {
	return nil, 0
}

// Lookup resolves a LinkID to a node. The name parameter IS the LinkID.
// Resolution is by ID only — no name decryption is performed.
// Returns ENOENT if the link is not in the client's link table.
func (n *LinkIDDir) Lookup(_ context.Context, name string) (fusemount.Node, syscall.Errno) {
	link := n.client.GetLink(name)
	if link == nil {
		return nil, syscall.ENOENT
	}
	return linkNode(link, n.client), 0
}
