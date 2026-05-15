//go:build linux

// Package fusemount implements the per-user FUSE filesystem that dispatches
// operations to registered namespace handlers.
package fusemount

import (
	"context"
	"sort"
	"syscall"
)

// Attr holds filesystem attributes for a node.
type Attr struct {
	Mode  uint32
	Size  uint64
	Nlink uint32
	Mtime uint64 // Unix seconds
	Ctime uint64 // Unix seconds
	Atime uint64 // Unix seconds
}

// DirEntry represents a single directory entry returned by Readdir.
// Mode should be one of syscall.S_IFDIR, syscall.S_IFREG, or syscall.S_IFLNK.
type DirEntry struct {
	Name string
	Mode uint32
}

// Node represents a filesystem object returned by handlers.
// The infrastructure wraps it with an inode for kernel tracking.
type Node interface {
	// Getattr returns file attributes.
	Getattr(ctx context.Context) (Attr, syscall.Errno)
}

// DirNode is a Node that supports directory operations.
type DirNode interface {
	Node
	Lookup(ctx context.Context, name string) (Node, syscall.Errno)
	Readdir(ctx context.Context) ([]DirEntry, syscall.Errno)
}

// FileHandle is an opaque handle returned by Open, consumed by Read/Write.
type FileHandle interface{}

// NamespaceHandler is the base interface for namespace plugins.
// Implementations handle filesystem operations for a top-level prefix (e.g. "drive").
type NamespaceHandler interface {
	// Lookup finds a child by name within this namespace.
	// Returns nil, ENOENT if not found.
	Lookup(ctx context.Context, name string) (Node, syscall.Errno)

	// Readdir lists entries at the namespace root.
	Readdir(ctx context.Context) ([]DirEntry, syscall.Errno)

	// Getattr returns attributes for the namespace root directory.
	Getattr(ctx context.Context) (Attr, syscall.Errno)
}

// NodeCreator indicates the handler supports file creation.
type NodeCreator interface {
	Create(ctx context.Context, name string, flags uint32, mode uint32) (Node, FileHandle, syscall.Errno)
}

// NodeMkdirer indicates the handler supports mkdir.
type NodeMkdirer interface {
	Mkdir(ctx context.Context, name string, mode uint32) (Node, syscall.Errno)
}

// NodeOpener indicates the node supports Open (creating per-open state).
// Nodes implementing NodeOpener typically also implement NodeReader and/or
// NodeWriter. NodeOpener creates per-open state; NodeReader/NodeWriter consume it.
type NodeOpener interface {
	Open(ctx context.Context, flags uint32) (FileHandle, syscall.Errno)
}

// NodeReleaser indicates the node supports Release (cleanup on close).
type NodeReleaser interface {
	Release(ctx context.Context, fh FileHandle) syscall.Errno
}

// NodeWriter indicates the node supports write operations.
type NodeWriter interface {
	Write(ctx context.Context, fh FileHandle, data []byte, off int64) (uint32, syscall.Errno)
}

// NodeReader indicates the node supports read operations.
type NodeReader interface {
	Read(ctx context.Context, fh FileHandle, dest []byte, off int64) (int, syscall.Errno)
}

// NodeRemover indicates the handler supports unlink/rmdir.
type NodeRemover interface {
	Unlink(ctx context.Context, name string) syscall.Errno
	Rmdir(ctx context.Context, name string) syscall.Errno
}

// NodeRenamer indicates the handler supports rename.
type NodeRenamer interface {
	Rename(ctx context.Context, oldName string, newParent Node, newName string) syscall.Errno
}

// NamespaceRegistry holds registered namespace handlers.
// Populated at startup, immutable after mount.
type NamespaceRegistry struct {
	handlers map[string]NamespaceHandler
}

// NewRegistry creates a new empty NamespaceRegistry.
func NewRegistry() *NamespaceRegistry {
	return &NamespaceRegistry{
		handlers: make(map[string]NamespaceHandler),
	}
}

// Register adds a namespace handler for the given prefix.
func (r *NamespaceRegistry) Register(prefix string, h NamespaceHandler) {
	r.handlers[prefix] = h
}

// Lookup returns the handler for the given prefix, or false if not found.
func (r *NamespaceRegistry) Lookup(prefix string) (NamespaceHandler, bool) {
	h, ok := r.handlers[prefix]
	return h, ok
}

// List returns all registered prefixes in sorted order.
func (r *NamespaceRegistry) List() []string {
	prefixes := make([]string, 0, len(r.handlers))
	for p := range r.handlers {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)
	return prefixes
}
