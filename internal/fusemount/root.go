//go:build linux

package fusemount

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Compile-time interface assertions.
var _ = (fs.NodeGetattrer)((*RootNode)(nil))
var _ = (fs.NodeLookuper)((*RootNode)(nil))
var _ = (fs.NodeReaddirer)((*RootNode)(nil))
var _ = (fs.NodeStatfser)((*RootNode)(nil))

// statfs constants for the FUSE mount. Bsize is the display block size
// reported to df; Proton Drive has no inode limit so Files/Ffree use a
// large sentinel.
const (
	statfsBlockSize     = 4096
	statfsNameLen       = 255
	statfsFilesSentinel = 1<<32 - 1
	quotaCacheTTL       = 60 * time.Second
)

// QuotaFunc returns the account's total and used storage in bytes.
// Injected by the daemon; when nil, Statfs reports zero block counts.
// Keeping this a callback avoids importing api/account into fusemount.
type QuotaFunc func(ctx context.Context) (total, used int64, err error)

// RootNode implements the FUSE root directory for the per-user mount.
// It dispatches Lookup to registered namespace handlers.
type RootNode struct {
	fs.Inode
	registry *NamespaceRegistry
	mtime    time.Time
	uid      uint32
	gid      uint32

	// quota provides account storage figures for Statfs. May be nil.
	quota      QuotaFunc
	quotaMu    sync.Mutex
	quotaTotal int64
	quotaUsed  int64
	quotaAt    time.Time
	quotaValid bool
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

// Getattr returns directory attributes for the root (mode 0555 — world-
// traversable so the redirector and other processes can stat namespace
// entries). Access control is enforced at the namespace boundary, not here.
func (r *RootNode) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFDIR | 0555
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
// caches the correct mode from the first response.
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
	out.Size = attr.Size
	out.Nlink = attr.Nlink
	out.Mtime = attr.Mtime
	out.Ctime = attr.Ctime
	out.Atime = attr.Atime
	out.Uid = r.uid
	out.Gid = r.gid

	node := &DispatchNode{handler: handler, isRoot: true, uid: r.uid, gid: r.gid}
	child := r.NewInode(ctx, node, fs.StableAttr{Mode: syscall.S_IFDIR})
	return child, 0
}

// Statfs reports filesystem statistics derived from the Proton account
// quota so that df and similar tools show total/used/available space.
// It never returns an error to the kernel — on any failure it reports
// zeros for the block counts while keeping the constant fields valid.
func (r *RootNode) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	total, used := r.currentQuota(ctx)

	out.Bsize = statfsBlockSize
	out.Frsize = statfsBlockSize
	out.NameLen = statfsNameLen
	out.Files = statfsFilesSentinel
	out.Ffree = statfsFilesSentinel

	if total <= 0 {
		// Quota unknown — leave block counts at zero.
		return 0
	}

	free := total - used
	if free < 0 {
		free = 0
	}
	//nolint:gosec // total and free are non-negative here
	out.Blocks = uint64(total) / statfsBlockSize
	//nolint:gosec // free is clamped to non-negative above
	freeBlocks := uint64(free) / statfsBlockSize
	out.Bfree = freeBlocks
	out.Bavail = freeBlocks
	return 0
}

// currentQuota returns the account total and used storage in bytes,
// serving cached values within the TTL. On a fetch error it returns the
// last cached values (or zeros if never fetched). A nil quota callback
// yields zeros.
func (r *RootNode) currentQuota(ctx context.Context) (total, used int64) {
	r.quotaMu.Lock()
	defer r.quotaMu.Unlock()

	if r.quota == nil {
		return 0, 0
	}
	if r.quotaValid && time.Since(r.quotaAt) < quotaCacheTTL {
		return r.quotaTotal, r.quotaUsed
	}

	t, u, err := r.quota(ctx)
	if err != nil {
		slog.Debug("statfs: quota fetch failed", "error", err)
		if r.quotaValid {
			return r.quotaTotal, r.quotaUsed
		}
		return 0, 0
	}

	r.quotaTotal = t
	r.quotaUsed = u
	r.quotaAt = time.Now()
	r.quotaValid = true
	return t, u
}
