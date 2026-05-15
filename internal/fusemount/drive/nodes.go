//go:build linux

package drive

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
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
		Mode:  syscall.S_IFDIR | 0700,
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
		Mode:  syscall.S_IFDIR | 0700,
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
// Directories use 0700 (owner rwx). Files use 0600 (owner rw).
// Group/other bits are cosmetic — DispatchNode.checkAccess enforces
// UID-gated access at the FUSE layer regardless of permission bits.
func linkMode(l *drive.Link) uint32 {
	if l.Type() == proton.LinkTypeFolder {
		return syscall.S_IFDIR | 0700
	}
	return syscall.S_IFREG | 0600
}

// linkNode returns the appropriate fusemount.Node for a link based on its type.
func linkNode(l *drive.Link, client *drive.Client) fusemount.Node {
	if l.Type() == proton.LinkTypeFolder {
		return &LinkDirNode{link: l, client: client}
	}
	return &FileNode{link: l, client: client}
}

// FileNode wraps a *drive.Link (file) and implements fusemount.Node,
// NodeOpener, NodeReader, and NodeReleaser for read-only file access.
type FileNode struct {
	link   *drive.Link
	client *drive.Client
}

// Compile-time interface assertions.
var _ fusemount.Node = (*FileNode)(nil)
var _ fusemount.NodeOpener = (*FileNode)(nil)
var _ fusemount.NodeReader = (*FileNode)(nil)
var _ fusemount.NodeReleaser = (*FileNode)(nil)

// fdHandle wraps a *drive.FileDescriptor as a fusemount.FileHandle.
type fdHandle struct {
	fd *drive.FileDescriptor
}

// Compile-time interface assertion.
var _ fusemount.FileHandle = (*fdHandle)(nil)

// Getattr returns file attributes including size and timestamps.
func (n *FileNode) Getattr(_ context.Context) (fusemount.Attr, syscall.Errno) {
	//nolint:gosec // Size/ModifyTime/CreateTime are non-negative from API
	return fusemount.Attr{
		Mode:  syscall.S_IFREG | 0600,
		Size:  uint64(n.link.Size()),
		Nlink: 1,
		Mtime: uint64(n.link.ModifyTime()),
		Ctime: uint64(n.link.CreateTime()),
	}, 0
}

// Open creates a FileDescriptor for reading. The context passed to OpenFD
// is context.Background() — the FD context must outlive the FUSE request.
func (n *FileNode) Open(_ context.Context, _ uint32) (fusemount.FileHandle, syscall.Errno) {
	fd, err := n.client.OpenFD(context.Background(), n.link)
	if err != nil {
		slog.Debug("FileNode.Open: failed", "linkID", n.link.LinkID(), "error", err)
		return nil, syscall.EIO
	}
	return &fdHandle{fd: fd}, 0
}

// Read delegates to fd.ReadAt and maps errors to FUSE errnos.
func (n *FileNode) Read(_ context.Context, fh fusemount.FileHandle, dest []byte, off int64) (int, syscall.Errno) {
	h, ok := fh.(*fdHandle)
	if !ok || h == nil {
		return 0, syscall.EBADF
	}
	bytesRead, err := h.fd.ReadAt(dest, off)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return bytesRead, 0
		}
		if bytesRead > 0 {
			return bytesRead, 0 // short read — return what we have
		}
		if errors.Is(err, os.ErrClosed) {
			return 0, syscall.EBADF
		}
		return 0, syscall.EIO
	}
	return bytesRead, 0
}

// Release closes the FileDescriptor. Errors are logged but not returned
// (Release errors are non-fatal per FUSE semantics).
func (n *FileNode) Release(_ context.Context, fh fusemount.FileHandle) syscall.Errno {
	h, ok := fh.(*fdHandle)
	if !ok || h == nil {
		return 0
	}
	if err := h.fd.Close(); err != nil {
		slog.Warn("FileNode.Release: close error", "linkID", n.link.LinkID(), "error", err)
	}
	return 0
}

// LinkIDDir is the virtual .linkid/ directory that provides O(1) access
// to any link by its LinkID. Implements fusemount.DirNode.
//
// Readdir lists share root links by their sanitized LinkID (no decryption
// needed). Lookup resolves any LinkID to a node from the client's link table.
type LinkIDDir struct {
	client *drive.Client
	shares func() map[string]*drive.Share // returns current share map snapshot
	mtime  uint64                         // copied from main volume at startup
	ctime  uint64                         // copied from main volume at startup
}

// Compile-time interface assertions.
var _ fusemount.Node = (*LinkIDDir)(nil)
var _ fusemount.DirNode = (*LinkIDDir)(nil)

// Getattr returns directory attributes for the .linkid virtual directory.
func (n *LinkIDDir) Getattr(_ context.Context) (fusemount.Attr, syscall.Errno) {
	return fusemount.Attr{
		Mode:  syscall.S_IFDIR | 0700,
		Nlink: 2,
		Mtime: n.mtime,
		Ctime: n.ctime,
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
