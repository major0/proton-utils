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
// Retains children from the last Readdir so Lookup can resolve locally.
type ShareDirNode struct {
	share    *drive.Share
	client   *drive.Client
	children map[string]*drive.Link // name → Link, populated by Readdir
}

// Compile-time interface assertions.
var _ fusemount.Node = (*ShareDirNode)(nil)
var _ fusemount.DirNode = (*ShareDirNode)(nil)

// Getattr returns directory attributes for the share root.
func (n *ShareDirNode) Getattr(_ context.Context) (fusemount.Attr, syscall.Errno) {
	//nolint:gosec // ModifyTime/CreateTime are non-negative from API
	return fusemount.Attr{
		Mode:  syscall.S_IFDIR | 0555,
		Nlink: 2,
		Mtime: uint64(n.share.Link.ModifyTime()),
		Ctime: uint64(n.share.Link.CreateTime()),
	}, 0
}

// Readdir lists children of the share's root link. Retains the name→Link
// mapping so subsequent Lookup calls resolve locally without an API call.
func (n *ShareDirNode) Readdir(_ context.Context) ([]fusemount.DirEntry, syscall.Errno) {
	// Use a detached context for the API call. The kernel FUSE timeout is
	// too short for paginated ListChildren on large directories (the main
	// volume may have hundreds of entries across multiple API pages).
	ctx := context.Background()
	var entries []fusemount.DirEntry
	children := make(map[string]*drive.Link)
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
		// Hide trashed and draft links.
		if de.Link.IsTrashed() || de.Link.IsDraft() {
			continue
		}
		children[name] = de.Link
		entries = append(entries, fusemount.DirEntry{
			Name: name,
			Mode: linkMode(de.Link),
		})
	}
	n.children = children
	return entries, 0
}

// Lookup finds a child by name. Uses the retained children map from the
// last Readdir to avoid a redundant ListLinkChildren API call.
func (n *ShareDirNode) Lookup(_ context.Context, name string) (fusemount.Node, syscall.Errno) {
	// Fast path: child retained from last Readdir.
	if n.children != nil {
		if child, ok := n.children[name]; ok {
			slog.Debug("ShareDirNode.Lookup: cache hit",
				"shareID", n.share.Metadata().ShareID, "name", name)
			return linkNode(child, n.client), 0
		}
	}

	// Slow path: no retained children (first Lookup before Readdir).
	slog.Debug("ShareDirNode.Lookup: cache miss, calling API",
		"shareID", n.share.Metadata().ShareID, "name", name)
	child, err := n.share.Link.Lookup(context.Background(), name)
	if err != nil {
		slog.Debug("ShareDirNode.Lookup: failed",
			"shareID", n.share.Metadata().ShareID, "name", name, "error", err)
		return nil, apiErrno(err)
	}
	if child == nil {
		return nil, syscall.ENOENT
	}
	return linkNode(child, n.client), 0
}

// LinkDirNode wraps a *drive.Link (folder) and implements fusemount.DirNode.
// It exposes the link's children as directory entries.
// Retains children from the last Readdir so Lookup can resolve locally.
type LinkDirNode struct {
	link     *drive.Link
	client   *drive.Client
	children map[string]*drive.Link // name → Link, populated by Readdir
}

// Compile-time interface assertions.
var _ fusemount.Node = (*LinkDirNode)(nil)
var _ fusemount.DirNode = (*LinkDirNode)(nil)

// Getattr returns directory attributes for the folder.
func (n *LinkDirNode) Getattr(_ context.Context) (fusemount.Attr, syscall.Errno) {
	//nolint:gosec // ModifyTime/CreateTime are non-negative from API
	return fusemount.Attr{
		Mode:  syscall.S_IFDIR | 0555,
		Nlink: 2,
		Mtime: uint64(n.link.ModifyTime()),
		Ctime: uint64(n.link.CreateTime()),
	}, 0
}

// Readdir lists children of the folder link. Retains the name→Link
// mapping so subsequent Lookup calls resolve locally without an API call.
func (n *LinkDirNode) Readdir(_ context.Context) ([]fusemount.DirEntry, syscall.Errno) {
	ctx := context.Background()
	var entries []fusemount.DirEntry
	children := make(map[string]*drive.Link)
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
		// Hide trashed and draft links.
		if de.Link.IsTrashed() || de.Link.IsDraft() {
			continue
		}
		children[name] = de.Link
		entries = append(entries, fusemount.DirEntry{
			Name: name,
			Mode: linkMode(de.Link),
		})
	}
	n.children = children
	return entries, 0
}

// Lookup finds a child by name. Uses the retained children map from the
// last Readdir to avoid a redundant ListLinkChildren API call.
func (n *LinkDirNode) Lookup(_ context.Context, name string) (fusemount.Node, syscall.Errno) {
	// Fast path: child retained from last Readdir.
	if n.children != nil {
		if child, ok := n.children[name]; ok {
			slog.Debug("LinkDirNode.Lookup: cache hit",
				"linkID", n.link.LinkID(), "name", name)
			return linkNode(child, n.client), 0
		}
	}

	// Slow path: no retained children (first Lookup before Readdir).
	slog.Debug("LinkDirNode.Lookup: cache miss, calling API",
		"linkID", n.link.LinkID(), "name", name)
	child, err := n.link.Lookup(context.Background(), name)
	if err != nil {
		slog.Debug("LinkDirNode.Lookup: failed",
			"linkID", n.link.LinkID(), "name", name, "error", err)
		return nil, apiErrno(err)
	}
	if child == nil {
		return nil, syscall.ENOENT
	}
	return linkNode(child, n.client), 0
}

// linkMode returns the FUSE mode for a link based on its type.
// Proton Drive has no Unix permission model — use 0555 (read+execute
// for owner/group/other) matching the CLI's display of "rwxr-xr-x".
func linkMode(l *drive.Link) uint32 {
	if l.Type() == proton.LinkTypeFolder {
		return syscall.S_IFDIR | 0555
	}
	return syscall.S_IFREG | 0555
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
		Mode:  syscall.S_IFREG | 0555,
		Size:  uint64(n.link.Size()),
		Nlink: 1,
		Mtime: uint64(n.link.ModifyTime()),
		Ctime: uint64(n.link.CreateTime()),
	}, 0
}

// LinkIDDir is the virtual .linkid/ directory that provides O(1) access
// to any link by its LinkID. Implements fusemount.DirNode.
//
// Readdir lists share root links by their sanitized LinkID (no decryption
// needed). Lookup resolves any LinkID to a node from the client's link table.
type LinkIDDir struct {
	client *drive.Client
	shares func() map[string]*drive.Share // returns current share map snapshot
}

// Compile-time interface assertions.
var _ fusemount.Node = (*LinkIDDir)(nil)
var _ fusemount.DirNode = (*LinkIDDir)(nil)

// Getattr returns directory attributes for the .linkid virtual directory.
func (n *LinkIDDir) Getattr(_ context.Context) (fusemount.Attr, syscall.Errno) {
	return fusemount.Attr{
		Mode:  syscall.S_IFDIR | 0555,
		Nlink: 2,
	}, 0
}

// Readdir lists share root links as directories using their sanitized LinkID.
// No name decryption is performed — IDs are used directly.
func (n *LinkIDDir) Readdir(_ context.Context) ([]fusemount.DirEntry, syscall.Errno) {
	shares := n.shares()
	entries := make([]fusemount.DirEntry, 0, len(shares))
	for _, share := range shares {
		linkID := share.Link.LinkID()
		entries = append(entries, fusemount.DirEntry{
			Name: drive.SanitizeLinkID(linkID),
			Mode: syscall.S_IFDIR,
		})
	}
	return entries, 0
}

// Lookup resolves a LinkID to a node. The name parameter IS the LinkID
// (sanitized — trailing '=' stripped). Resolution is by ID only — no
// name decryption is performed.
// Checks the client's link table first, then share root links.
// Returns ENOENT if the link is not found.
func (n *LinkIDDir) Lookup(_ context.Context, name string) (fusemount.Node, syscall.Errno) {
	// Check the client's link table (O(1) by ID).
	link := n.client.GetLink(name)
	if link != nil {
		return linkNode(link, n.client), 0
	}

	// Check share root links — these may not be in the link table.
	shares := n.shares()
	for _, share := range shares {
		if drive.SanitizeLinkID(share.Link.LinkID()) == name {
			return &ShareDirNode{share: share, client: n.client}, 0
		}
	}

	return nil, syscall.ENOENT
}
