//go:build linux

package fusemount

import (
	"context"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Compile-time interface assertions for readdirHandle.
var _ fs.FileReaddirenter = (*readdirHandle)(nil)
var _ fs.FileLookuper = (*readdirHandle)(nil)
var _ fs.FileReleasedirer = (*readdirHandle)(nil)

// readdirPrefetchWorkers bounds the number of concurrent XAttr prefetch
// operations dispatched during a single OpendirHandle. The dispatch layer
// cannot reach the session's shared semaphore without importing api/drive
// (which would break the package boundary), so it uses a fixed local bound.
// The per-fetch API calls remain subject to the session Throttle.
const readdirPrefetchWorkers = 16

// xattrPrefetcher is satisfied by *drive.Link. It lets the dispatch layer
// trigger XAttr prefetch without importing api/drive — the DirEntry.Link
// reference is type-asserted to this interface.
type xattrPrefetcher interface {
	EnsureXAttrPrefetch()
}

// readdirHandle is the per-opendir state for READDIRPLUS responses.
// It holds the pre-fetched children and yields entries one at a time
// via Readdirent, with Lookup providing full attrs for each entry.
type readdirHandle struct {
	dispatch *DispatchNode         // parent dispatch node (for uid/gid, ctx)
	entries  []readdirEntry        // . and .. prepended, then children
	pos      int                   // current position in entries slice
	children map[string]childEntry // name → resolved child info
}

// readdirEntry is one entry yielded by Readdirent.
type readdirEntry struct {
	name string
	mode uint32
}

// childEntry holds the resolved child node and its pre-fetched Link
// reference for attr population in Lookup.
type childEntry struct {
	node Node
	link interface{} // opaque *drive.Link reference
	mode uint32      // file-type bits, for prefetch targeting
}

// buildReaddirHandle constructs a readdirHandle from a parent DispatchNode
// and the directory entries returned by Readdir. It prepends "." and ".."
// as the first two entries (mode S_IFDIR), then appends each DirEntry.
// The children map is populated with resolved child nodes for entries that
// have a non-nil Link field.
func buildReaddirHandle(d *DispatchNode, entries []DirEntry) *readdirHandle {
	h := &readdirHandle{
		dispatch: d,
		entries:  make([]readdirEntry, 0, 2+len(entries)),
		children: make(map[string]childEntry, len(entries)),
	}

	// Prepend . and .. with directory mode.
	h.entries = append(h.entries,
		readdirEntry{name: ".", mode: syscall.S_IFDIR},
		readdirEntry{name: "..", mode: syscall.S_IFDIR},
	)

	// Append child entries and populate the children map.
	for _, e := range entries {
		h.entries = append(h.entries, readdirEntry{name: e.Name, mode: e.Mode})

		if e.Link == nil {
			continue
		}

		// Resolve the child node from the parent DirNode's Lookup.
		// After Readdir, the DirNode retains its children map, so this
		// is a local map lookup + struct creation — no network call.
		var node Node
		if d.isRoot {
			n, errno := d.handler.Lookup(context.Background(), e.Name)
			if errno == 0 {
				node = n
			}
		} else if dir, ok := d.node.(DirNode); ok {
			n, errno := dir.Lookup(context.Background(), e.Name)
			if errno == 0 {
				node = n
			}
		}

		h.children[e.Name] = childEntry{
			node: node,
			link: e.Link,
			mode: e.Mode,
		}
	}

	return h
}

// Readdirent yields one fuse.DirEntry at a time from the entries slice.
// Returns nil, 0 at EOF to signal end of stream.
func (h *readdirHandle) Readdirent(_ context.Context) (*fuse.DirEntry, syscall.Errno) {
	if h.pos >= len(h.entries) {
		return nil, 0
	}
	e := h.entries[h.pos]
	h.pos++
	return &fuse.DirEntry{Name: e.name, Mode: e.mode}, 0
}

// Lookup resolves attributes for the named entry, wraps the child node,
// and populates EntryOut. Handles "." and ".." specially.
//
// Note: go-fuse's ReadDirPlus bridge never calls Lookup for "." or ".."
// (the kernel ignores their EntryOut). The dot-entry handling here exists
// for interface completeness and unit testing.
func (h *readdirHandle) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (
	*fs.Inode, syscall.Errno) {

	if name == "." || name == ".." {
		return h.lookupDotEntry(ctx, name, out)
	}

	child, ok := h.children[name]
	if !ok || child.node == nil {
		return nil, syscall.ENOENT
	}

	return wrapChild(ctx, h.dispatch, child.node, out)
}

// lookupDotEntry populates EntryOut for "." (self) and ".." (parent).
// The "." entry carries the directory's own attributes; ".." carries
// generic directory attributes (mode S_IFDIR|0700, nlink 2).
func (h *readdirHandle) lookupDotEntry(ctx context.Context, name string, out *fuse.EntryOut) (
	*fs.Inode, syscall.Errno) {

	d := h.dispatch
	out.Uid = d.uid
	out.Gid = d.gid

	if name == "." {
		var attr Attr
		var errno syscall.Errno
		if d.isRoot {
			attr, errno = d.handler.Getattr(ctx)
		} else if d.node != nil {
			attr, errno = d.node.Getattr(ctx)
		}
		if errno == 0 {
			out.Mode = attr.Mode
			out.Size = attr.Size
			out.Nlink = attr.Nlink
			out.Mtime = attr.Mtime
			out.Ctime = attr.Ctime
			out.Atime = attr.Atime
		} else {
			out.Mode = syscall.S_IFDIR | 0700
			out.Nlink = 2
		}
		return nil, 0
	}

	// ".." — generic parent directory attributes.
	out.Mode = syscall.S_IFDIR | 0700
	out.Nlink = 2
	return nil, 0
}

// Releasedir cleans up the handle state when the directory is closed.
// It nils out the children map and entries slice so the garbage collector
// can reclaim the retained child nodes and link references.
func (h *readdirHandle) Releasedir(_ context.Context, _ uint32) {
	h.children = nil
	h.entries = nil
}

// prefetchFileAttrs calls EnsureXAttrPrefetch on each file-type child in the
// readdirHandle, bounded by a fixed worker pool. Folder children are skipped
// (their attrs are already available from the listing API). On context
// cancellation, it stops dispatching new fetches and returns without
// dispatching the remainder.
func prefetchFileAttrs(ctx context.Context, h *readdirHandle) {
	// Collect file-type children that expose the prefetch interface.
	var targets []xattrPrefetcher
	for _, child := range h.children {
		if child.mode&syscall.S_IFMT != syscall.S_IFREG {
			continue
		}
		if p, ok := child.link.(xattrPrefetcher); ok {
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		return
	}

	workers := readdirPrefetchWorkers
	if len(targets) < workers {
		workers = len(targets)
	}

	indexCh := make(chan int)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range indexCh {
				targets[idx].EnsureXAttrPrefetch()
			}
		}()
	}

	// Feed indices, respecting cancellation. The explicit ctx.Err() check
	// makes cancellation deterministic when the context is already done.
	go func() {
		defer close(indexCh)
		for i := range targets {
			if ctx.Err() != nil {
				return
			}
			select {
			case indexCh <- i:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
}
