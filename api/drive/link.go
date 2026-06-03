package drive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
)

// resolvedMeta holds all resolved metadata values from XAttr decryption.
// A nil pointer means "not yet resolved" — no per-field validity booleans needed.
// Allocated once on first successful resolution; never mutated after creation.
// Invalidation: set the pointer to nil under cacheMu.Lock.
type resolvedMeta struct {
	size  int64  // plaintext file size from XAttr (or fallback)
	mtime int64  // modification time as Unix epoch (or fallback)
	ctime int64  // creation time as Unix epoch (or fallback)
	mode  uint32 // Unix permission bits from XAttr (or fallback)
}

// Link represents a file or folder in a Proton Drive share. The raw
// encrypted proton.Link is the canonical representation. Decrypted
// fields are derived on demand via accessor methods. When the share's
// MemoryCacheLevel is enabled, accessors cache results for the session
// using double-checked locking via cacheMu.
type Link struct {
	// Raw encrypted link from the API. Always populated.
	protonLink *proton.Link

	// Relationships — always set at construction time.
	parentLink *Link
	resolver   LinkResolver
	share      *Share

	// testName overrides Name() when non-empty. Set only by
	// NewTestLink to avoid needing real crypto in tests.
	testName string

	// cacheMu protects the cached fields below. Readers take RLock
	// to check the cache; writers take Lock for decrypt-and-store.
	cacheMu sync.RWMutex

	// cachedName caches the decrypted name when the share's
	// MemoryCacheLevel is >= CacheLinkName. Empty when not cached.
	cachedName string

	// cachedStat caches the FileInfo result when the share's
	// MemoryCacheLevel is >= CacheMetadata. Nil when not cached.
	cachedStat *FileInfo

	// cachedKeyRing caches the derived keyring when the share's
	// MemoryCacheLevel is >= CacheMetadata. Nil when not cached.
	cachedKeyRing *crypto.KeyRing

	// cachedChildIDs caches the child LinkIDs after a Readdir. When
	// non-nil, Lookup can resolve children from the link table without
	// a fresh ListLinkChildren API call. Populated by Readdir when the
	// share's MemoryCacheLevel is >= CacheMetadata.
	cachedChildIDs []string

	// meta holds resolved metadata (size, mtime, ctime, mode). nil means
	// not yet resolved. Protected by cacheMu. Immutable after creation —
	// never mutated, only replaced atomically (pointer swap under cacheMu.Lock).
	meta *resolvedMeta

	// fetchMu serializes the XAttr fetch. Separate from cacheMu so the
	// network call does not block concurrent cache reads.
	fetchMu sync.Mutex

	// fetchDone indicates whether the XAttr fetch has completed
	// permanently (success OR max retries exceeded). Reset to false on
	// transient failure to allow retry, up to the fetchFailCount cap.
	fetchDone bool

	// fetchFailCount tracks consecutive fetch failures. When it reaches
	// 3, fetchDone is set to true permanently (give up, return fallback
	// values). Reset to 0 when the link is removed from the link table
	// (event invalidation).
	fetchFailCount uint8
}

// Type returns the link type (file or folder) without decryption.
func (l *Link) Type() proton.LinkType { return l.protonLink.Type }

// State returns the link state without decryption.
func (l *Link) State() proton.LinkState { return l.protonLink.State }

// IsDir returns true if the link is a folder.
func (l *Link) IsDir() bool { return l.protonLink.Type == proton.LinkTypeFolder }

// IsFile returns true if the link is a file.
func (l *Link) IsFile() bool { return l.protonLink.Type == proton.LinkTypeFile }

// IsActive returns true if the link state is active.
func (l *Link) IsActive() bool { return l.protonLink.State == proton.LinkStateActive }

// IsTrashed returns true if the link state is trashed.
func (l *Link) IsTrashed() bool { return l.protonLink.State == proton.LinkStateTrashed }

// IsDraft returns true if the link state is draft.
func (l *Link) IsDraft() bool { return l.protonLink.State == proton.LinkStateDraft }

// CreateTime returns the creation timestamp. This is a non-fetch accessor:
// it checks the resolvedMeta cache (benefiting if a fetch-triggering accessor
// has already run) but does NOT call ensureXAttr() or build resolvedMeta itself.
// For files with an active revision, returns ActiveRevision.CreateTime.
// Fallback: protonLink.CreateTime when no active revision exists.
func (l *Link) CreateTime() int64 {
	l.cacheMu.RLock()
	if m := l.meta; m != nil {
		l.cacheMu.RUnlock()
		return m.ctime
	}
	l.cacheMu.RUnlock()

	if l.HasActiveRevision() {
		return l.protonLink.FileProperties.ActiveRevision.CreateTime
	}
	return l.protonLink.CreateTime
}

// ModifyTime returns the modification timestamp from XAttr. For file-type
// links, triggers a lazy XAttr fetch on first access and parses the ISO 8601
// ModificationTime field. For folder links, decrypts the link-level XAttr.
// Fallback: ActiveRevision.CreateTime for files, protonLink.ModifyTime for folders.
func (l *Link) ModifyTime() int64 {
	// Fast path: return cached value if already resolved.
	l.cacheMu.RLock()
	if m := l.meta; m != nil {
		l.cacheMu.RUnlock()
		return m.mtime
	}
	l.cacheMu.RUnlock()

	// Resolve XAttr and keyring BEFORE acquiring cacheMu.Lock.
	var xattr *proton.RevisionXAttrCommon
	if l.protonLink.Type == proton.LinkTypeFile {
		l.ensureXAttr()
		nodeKR, err := l.KeyRing()
		if err == nil {
			xattr = l.decryptXAttr(nodeKR)
		}
	} else {
		// Folder: decrypt link-level XAttr (no fetch needed).
		nodeKR, err := l.KeyRing()
		if err == nil {
			xattr = l.decryptXAttr(nodeKR)
		}
	}

	// Build resolvedMeta with ALL fields.
	m := l.buildResolvedMeta(xattr)

	// Cache store with double-checked locking. Do NOT cache fallback
	// values for file-type links when the fetch is still retryable
	// (Requirement 7.7) — allows subsequent calls to re-attempt the fetch.
	l.cacheMu.Lock()
	if l.meta == nil && l.share != nil && l.share.MemoryCacheLevel >= api.CacheMetadata {
		if xattr != nil || l.protonLink.Type != proton.LinkTypeFile || l.fetchDone {
			l.meta = m
		}
	}
	l.cacheMu.Unlock()

	return m.mtime
}

// ExpirationTime returns the expiration timestamp without decryption.
func (l *Link) ExpirationTime() int64 { return l.protonLink.ExpirationTime }

// Size returns the plaintext file size from XAttr. For file-type links,
// triggers a lazy XAttr fetch on first access. For folder links, reads
// from the link-level XAttr field (already populated by the listing API).
// Fallback: ActiveRevision.Size for files, 0 for folders.
func (l *Link) Size() int64 {
	// Fast path: return cached value if already resolved.
	l.cacheMu.RLock()
	if m := l.meta; m != nil {
		l.cacheMu.RUnlock()
		return m.size
	}
	l.cacheMu.RUnlock()

	// Resolve XAttr and keyring BEFORE acquiring cacheMu.Lock.
	var xattr *proton.RevisionXAttrCommon
	if l.protonLink.Type == proton.LinkTypeFile {
		l.ensureXAttr()
		nodeKR, err := l.KeyRing()
		if err == nil {
			xattr = l.decryptXAttr(nodeKR)
		}
	} else {
		// Folder: decrypt link-level XAttr (no fetch needed).
		nodeKR, err := l.KeyRing()
		if err == nil {
			xattr = l.decryptXAttr(nodeKR)
		}
	}

	// Build resolvedMeta with ALL fields.
	m := l.buildResolvedMeta(xattr)

	// Cache store with double-checked locking. Do NOT cache fallback
	// values for file-type links when the fetch is still retryable
	// (Requirement 7.7) — allows subsequent calls to re-attempt the fetch.
	l.cacheMu.Lock()
	if l.meta == nil && l.share != nil && l.share.MemoryCacheLevel >= api.CacheMetadata {
		if xattr != nil || l.protonLink.Type != proton.LinkTypeFile || l.fetchDone {
			l.meta = m
		}
	}
	l.cacheMu.Unlock()

	return m.size
}

// HasActiveRevision returns true if the link is a file with a committed
// active revision. A file in state Active but with no active revision is
// a "ghost" file, not a draft.
func (l *Link) HasActiveRevision() bool {
	return l.protonLink.Type == proton.LinkTypeFile &&
		l.protonLink.FileProperties != nil &&
		l.protonLink.FileProperties.ActiveRevision.ID != "" &&
		l.protonLink.FileProperties.ActiveRevision.State == proton.RevisionStateActive
}

// RevisionID returns the active revision ID if file properties exist,
// or empty string otherwise.
func (l *Link) RevisionID() string {
	if l.protonLink.FileProperties != nil {
		return l.protonLink.FileProperties.ActiveRevision.ID
	}
	return ""
}

// MIMEType returns the MIME type without decryption.
func (l *Link) MIMEType() string { return l.protonLink.MIMEType }

// LinkID returns the encrypted link ID without decryption.
func (l *Link) LinkID() string { return l.protonLink.LinkID }

// Stat returns file metadata without decrypting content. When the share's
// MemoryCacheLevel is >= CacheMetadata, the result is cached for subsequent
// calls using double-checked locking via cacheMu.
// BlockSizes is nil — it requires decrypting the revision XAttr which is
// a client-layer operation.
//
// Uses two paths to avoid reentrant RWMutex deadlock:
// - Fast path: returns from cachedStat or builds from meta (no public accessor calls under lock)
// - Slow path: calls public accessors BEFORE acquiring cacheMu.Lock
func (l *Link) Stat() FileInfo {
	// Fast path: check cachedStat and meta under RLock.
	l.cacheMu.RLock()
	if l.cachedStat != nil {
		fi := *l.cachedStat
		l.cacheMu.RUnlock()
		return fi
	}
	if m := l.meta; m != nil {
		// Build FileInfo directly from meta fields — no public accessor calls.
		l.cacheMu.RUnlock()

		// Gather name reference outside of any lock (Name() acquires cacheMu internally).
		fi := FileInfo{
			LinkID:     l.protonLink.LinkID,
			Name:       l.Name,
			Size:       m.size,
			ModifyTime: m.mtime,
			CreateTime: m.ctime,
			MIMEType:   l.protonLink.MIMEType,
			IsDir:      l.protonLink.Type == proton.LinkTypeFolder,
		}

		// Store under write lock with double-check.
		l.cacheMu.Lock()
		if l.cachedStat == nil && l.share != nil && l.share.MemoryCacheLevel >= api.CacheMetadata {
			l.cachedStat = &fi
		}
		l.cacheMu.Unlock()
		return fi
	}
	l.cacheMu.RUnlock()

	// Slow path: both cachedStat and meta are nil. Call public accessors
	// BEFORE acquiring cacheMu.Lock — each accessor acquires/releases
	// cacheMu on its own without deadlock.
	size := l.Size()
	mtime := l.ModifyTime()
	ctime := l.CreateTime()
	// Mode() is called to ensure resolvedMeta is populated as a side effect.
	// The return value is intentionally discarded — FileInfo has no Mode field.
	l.Mode()

	fi := FileInfo{
		LinkID:     l.protonLink.LinkID,
		Name:       l.Name,
		Size:       size,
		ModifyTime: mtime,
		CreateTime: ctime,
		MIMEType:   l.protonLink.MIMEType,
		IsDir:      l.protonLink.Type == proton.LinkTypeFolder,
	}

	// Store under write lock with double-check.
	l.cacheMu.Lock()
	if l.cachedStat == nil && l.share != nil && l.share.MemoryCacheLevel >= api.CacheMetadata {
		l.cachedStat = &fi
	}
	l.cacheMu.Unlock()
	return fi
}

// isTransient returns true for errors that may succeed on retry.
func isTransient(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

// Name returns the decrypted name. When the share's MemoryCacheLevel is
// >= CacheLinkName, the result is cached for subsequent calls using
// double-checked locking via cacheMu. For test links with testName set,
// returns the override directly.
func (l *Link) Name() (string, error) {
	if l.testName != "" {
		return l.testName, nil
	}

	l.cacheMu.RLock()
	if l.cachedName != "" {
		defer l.cacheMu.RUnlock()
		return l.cachedName, nil
	}
	l.cacheMu.RUnlock()

	l.cacheMu.Lock()
	defer l.cacheMu.Unlock()
	if l.cachedName != "" {
		return l.cachedName, nil
	}

	parentKR, err := l.getParentKeyRing()
	if err != nil {
		return "", fmt.Errorf("name %s: parent keyring: %w", l.protonLink.LinkID, err)
	}
	name, err := l.decryptName(parentKR)
	if err != nil {
		return "", err
	}
	if l.share != nil && l.share.MemoryCacheLevel >= api.CacheLinkName {
		l.cachedName = name
	}
	return name, nil
}

// KeyRing returns the link's keyring. When the share's MemoryCacheLevel
// is >= CacheMetadata, the result is cached for subsequent calls using
// double-checked locking via cacheMu.
func (l *Link) KeyRing() (*crypto.KeyRing, error) {
	l.cacheMu.RLock()
	if l.cachedKeyRing != nil {
		defer l.cacheMu.RUnlock()
		return l.cachedKeyRing, nil
	}
	l.cacheMu.RUnlock()

	l.cacheMu.Lock()
	defer l.cacheMu.Unlock()
	if l.cachedKeyRing != nil {
		return l.cachedKeyRing, nil
	}

	parentKR, err := l.getParentKeyRing()
	if err != nil {
		return nil, fmt.Errorf("keyring %s: parent keyring: %w", l.protonLink.LinkID, err)
	}
	kr, err := l.deriveKeyRing(parentKR)
	if err != nil {
		return nil, err
	}
	if l.share != nil && l.share.MemoryCacheLevel >= api.CacheMetadata {
		l.cachedKeyRing = kr
	}
	return kr, nil
}

// Mode returns the Unix permission bits from the XAttr blob.
// For file-type links, triggers a lazy XAttr fetch on first access.
// For folder links, reads from the link-level XAttr (populated by the
// listing API). Returns 0 when Mode is not set (legacy files, fetch
// failure, or decryption error).
//
// The keyring is resolved BEFORE acquiring cacheMu.Lock to avoid the
// RWMutex deadlock (KeyRing() acquires cacheMu.RLock internally).
func (l *Link) Mode() uint32 {
	// Fast path: return cached value if available.
	l.cacheMu.RLock()
	if m := l.meta; m != nil {
		l.cacheMu.RUnlock()
		return m.mode
	}
	l.cacheMu.RUnlock()

	// For file-type links, trigger the fetch gate to populate XAttr.
	if l.protonLink.Type == proton.LinkTypeFile {
		l.ensureXAttr()
	}

	// Resolve keyring BEFORE acquiring cacheMu.Lock — KeyRing()
	// acquires cacheMu.RLock internally; Go's RWMutex is not reentrant.
	nodeKR, err := l.KeyRing()
	if err != nil {
		return 0
	}

	// Decrypt XAttr using the pre-resolved keyring.
	xattr := l.decryptXAttr(nodeKR)

	// Build resolvedMeta with all fields from XAttr + fallbacks.
	m := l.buildResolvedMeta(xattr)

	// Cache store: double-checked locking under cacheMu.Lock.
	// Do NOT cache fallback values for file-type links when the fetch
	// is still retryable (Requirement 7.7).
	l.cacheMu.Lock()
	if l.meta == nil && l.share != nil && l.share.MemoryCacheLevel >= api.CacheMetadata {
		if xattr != nil || l.protonLink.Type != proton.LinkTypeFile || l.fetchDone {
			l.meta = m
		}
	}
	l.cacheMu.Unlock()

	return m.mode
}

// InvalidateMeta clears the cached resolvedMeta and cachedStat so that
// the next metadata accessor call re-resolves all fields from XAttr,
// and the next Stat() call rebuilds FileInfo. Called by Chmod after
// committing a new revision with updated mode bits.
func (l *Link) InvalidateMeta() {
	l.cacheMu.Lock()
	l.meta = nil
	l.cachedStat = nil
	l.cacheMu.Unlock()
}

// ensureXAttr ensures the ActiveRevision.XAttr field is populated for
// file-type links. Uses fetchMu as the fetch gate (Phase 1). No-op for
// folders, drafts, links without an active revision.
//
// NOTE: There is no pre-lock fast path for rev.XAttr != "". The XAttr
// field is written by FetchRevisionXAttr under fetchMu, so reading it
// without the lock would be a data race (flagged by -race). The check
// is performed UNDER fetchMu instead.
func (l *Link) ensureXAttr() {
	// Skip conditions (no lock needed — these are immutable after construction):
	if l.protonLink.Type != proton.LinkTypeFile {
		return
	}
	if l.protonLink.FileProperties == nil {
		return
	}
	if l.protonLink.State == proton.LinkStateDraft {
		return
	}
	rev := &l.protonLink.FileProperties.ActiveRevision
	if rev.ID == "" || rev.State != proton.RevisionStateActive {
		return
	}
	// NOTE: We do NOT check rev.XAttr != "" here without synchronization.
	// The XAttr field is written by FetchRevisionXAttr under fetchMu, so
	// reading it without the lock would be a data race (flagged by -race).
	// Instead, we check under fetchMu below.

	// Phase 1: fetch gate — at most one goroutine fetches.
	l.fetchMu.Lock()
	// Double-check under lock: XAttr may have been populated by another
	// goroutine, or fetchDone may already be set.
	if l.fetchDone || rev.XAttr != "" {
		l.fetchMu.Unlock()
		return
	}

	// Perform the network call WITHOUT holding cacheMu.
	l.resolver.FetchRevisionXAttr(context.Background(), l)

	// Mark fetch as done. On failure (XAttr still empty), increment
	// the fail counter. After 3 consecutive failures, give up
	// permanently (set fetchDone = true) to avoid unbounded retries
	// for links deleted server-side but still in the local link table.
	if rev.XAttr == "" {
		l.fetchFailCount++
		if l.fetchFailCount >= 3 {
			l.fetchDone = true // permanent give-up — return fallback values
		}
		// else: fetchDone remains false, next accessor call retries
	} else {
		l.fetchDone = true
	}
	l.fetchMu.Unlock()
}

// decryptXAttr decrypts the XAttr blob and returns the common fields.
// Returns nil on any error (non-fatal — callers use fallback values).
// Works for both file-type links (reads ActiveRevision.XAttr) and
// folder links (reads protonLink.XAttr).
//
// nodeKR is the pre-resolved node keyring — callers must obtain it via
// l.KeyRing() BEFORE acquiring cacheMu to avoid RWMutex deadlock.
func (l *Link) decryptXAttr(nodeKR *crypto.KeyRing) *proton.RevisionXAttrCommon {
	var xattrStr string
	var sigEmail string

	if l.protonLink.Type == proton.LinkTypeFile && l.protonLink.FileProperties != nil {
		rev := &l.protonLink.FileProperties.ActiveRevision
		xattrStr = rev.XAttr
		sigEmail = rev.SignatureEmail
	} else {
		xattrStr = l.protonLink.XAttr
		sigEmail = l.protonLink.SignatureEmail
	}

	if xattrStr == "" {
		return nil
	}

	addr, ok := l.resolver.AddressForEmail(sigEmail)
	if !ok {
		return nil
	}
	addrKR, ok := l.resolver.AddressKeyRing(addr.ID)
	if !ok {
		return nil
	}

	// Build a temporary RevisionMetadata to call GetDecXAttrString.
	// This reuses the existing go-proton-api decryption path.
	tmp := proton.RevisionMetadata{
		XAttr:          xattrStr,
		SignatureEmail: sigEmail,
	}
	xattr, err := tmp.GetDecXAttrString(addrKR, nodeKR)
	if err != nil || xattr == nil {
		return nil
	}
	return xattr
}

// buildResolvedMeta constructs a resolvedMeta struct from decrypted XAttr
// values (when available) with appropriate fallbacks. Called by all
// fetch-triggering accessors (Size, ModifyTime, Mode) after decryption.
func (l *Link) buildResolvedMeta(xattr *proton.RevisionXAttrCommon) *resolvedMeta {
	m := &resolvedMeta{}

	// size: XAttr value if available, else ActiveRevision.Size for files / 0 for folders.
	if xattr != nil {
		m.size = xattr.Size
	} else if l.protonLink.Type == proton.LinkTypeFile && l.protonLink.FileProperties != nil {
		m.size = l.protonLink.FileProperties.ActiveRevision.Size
	}

	// mtime: parsed ModificationTime from XAttr; fallback ActiveRevision.CreateTime
	// for files, protonLink.ModifyTime for folders.
	switch {
	case xattr != nil && xattr.ModificationTime != "":
		if t, err := time.Parse(time.RFC3339, xattr.ModificationTime); err == nil {
			m.mtime = t.Unix()
		} else if l.protonLink.Type == proton.LinkTypeFile && l.protonLink.FileProperties != nil {
			m.mtime = l.protonLink.FileProperties.ActiveRevision.CreateTime
		} else {
			m.mtime = l.protonLink.ModifyTime
		}
	case l.protonLink.Type == proton.LinkTypeFile && l.protonLink.FileProperties != nil:
		m.mtime = l.protonLink.FileProperties.ActiveRevision.CreateTime
	default:
		m.mtime = l.protonLink.ModifyTime
	}

	// ctime: ActiveRevision.CreateTime for files, protonLink.CreateTime for folders.
	if l.protonLink.Type == proton.LinkTypeFile && l.protonLink.FileProperties != nil {
		m.ctime = l.protonLink.FileProperties.ActiveRevision.CreateTime
	} else {
		m.ctime = l.protonLink.CreateTime
	}

	// mode: XAttr value if available, else 0.
	if xattr != nil {
		m.mode = xattr.Mode
	}

	return m
}

// getParentKeyRing returns the parent's keyring for decryption.
func (l *Link) getParentKeyRing() (*crypto.KeyRing, error) {
	if l.parentLink == nil {
		if l.share == nil {
			return nil, fmt.Errorf("getParentKeyRing %s: no parent and no share", l.protonLink.LinkID)
		}
		return l.share.getKeyRing()
	}
	return l.parentLink.KeyRing()
}

// deriveKeyRing derives this link's keyring from the parent keyring.
func (l *Link) deriveKeyRing(parentKR *crypto.KeyRing) (*crypto.KeyRing, error) {
	email := l.protonLink.SignatureEmail
	if addr, ok := l.resolver.AddressForEmail(email); ok {
		if linkKR, ok := l.resolver.AddressKeyRing(addr.ID); ok {
			return l.protonLink.GetKeyRing(parentKR, linkKR)
		}
	}
	return nil, fmt.Errorf("deriveKeyRing: signature email %q: %w", email, api.ErrKeyNotFound)
}

// decryptName decrypts the link name using the parent keyring.
func (l *Link) decryptName(parentKR *crypto.KeyRing) (string, error) {
	email := l.protonLink.NameSignatureEmail
	if addr, ok := l.resolver.AddressForEmail(email); ok {
		if addrKR, ok := l.resolver.AddressKeyRing(addr.ID); ok {
			return l.protonLink.GetName(parentKR, addrKR)
		}
	}
	return "", fmt.Errorf("decryptName: name signature email %q: %w", email, api.ErrKeyNotFound)
}

// ProtonLink returns the raw encrypted proton.Link. Used by the client
// package for API operations that need raw link fields.
func (l *Link) ProtonLink() *proton.Link { return l.protonLink }

// Parent returns the parent directory link (..).
// For share roots (parentLink == nil), returns self — matching POSIX /.. → / behavior.
func (l *Link) Parent() *Link {
	if l.parentLink == nil {
		return l
	}
	return l.parentLink
}

// InvalidateChildren clears the cached child IDs, forcing the next
// Readdir or Lookup to re-fetch children from the API. Call this after
// any mutation that changes the parent's children (mkdir, create, remove, rename).
func (l *Link) InvalidateChildren() {
	l.cacheMu.Lock()
	l.cachedChildIDs = nil
	l.cacheMu.Unlock()
}

// ParentLink returns the parent Link, or nil for share roots.
func (l *Link) ParentLink() *Link { return l.parentLink }

// AbsPath walks the parent chain to the share root and returns the
// fully qualified path from the share root. Triggers lazy decryption
// of names along the chain.
func (l *Link) AbsPath(_ context.Context) (string, error) {
	var parts []string
	current := l
	for current.parentLink != nil {
		name, err := current.Name()
		if err != nil {
			return "", fmt.Errorf("abspath: %w", err)
		}
		parts = append(parts, name)
		current = current.parentLink
	}
	// current is now the share root — prepend its name.
	rootName, err := current.Name()
	if err != nil {
		return "", fmt.Errorf("abspath: root: %w", err)
	}
	// Reverse parts (we walked leaf→root, need root→leaf).
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	if len(parts) == 0 {
		return rootName, nil
	}
	return rootName + "/" + strings.Join(parts, "/"), nil
}

// Share returns the Link's associated Share.
func (l *Link) Share() *Share { return l.share }

// VolumeID returns the volume ID for this link's share.
func (l *Link) VolumeID() string {
	if l.share == nil {
		return ""
	}
	return l.share.VolumeID()
}

// SameDevice returns true if two links are on the same volume.
func SameDevice(a, b *Link) bool {
	return a.VolumeID() == b.VolumeID()
}

// NewLink creates a Link wrapper without decrypting anything.
// parent is the parent directory link. For share roots, pass nil —
// Parent() will return self, matching POSIX /.. → / behavior.
func NewLink(pLink *proton.Link, parent *Link, share *Share, resolver LinkResolver) *Link {
	return &Link{
		protonLink: pLink,
		parentLink: parent,
		share:      share,
		resolver:   resolver,
	}
}

// ResolvePath resolves a slash-separated path relative to this link.
// Only decrypts names along the path — siblings are not decrypted.
func (l *Link) ResolvePath(ctx context.Context, path string, _ bool) (*Link, error) {
	slog.Debug("link.ResolvePath", "linkID", l.LinkID())
	path = strings.Trim(path, "/")
	if path == "" {
		return l, nil
	}
	parts := strings.Split(path, "/")
	return l.resolveParts(ctx, parts)
}

// resolveParts walks path components, handling "." (self) and ".." (parent)
// via tree traversal. Only the matching child at each level is decrypted.
func (l *Link) resolveParts(ctx context.Context, parts []string) (*Link, error) {
	current := l
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			current = current.Parent()
		default:
			if current.Type() != proton.LinkTypeFolder {
				return nil, ErrNotAFolder
			}
			child, err := current.Lookup(ctx, part)
			if err != nil {
				return nil, err
			}
			if child == nil {
				return nil, ErrFileNotFound
			}
			current = child
		}
	}
	return current, nil
}
