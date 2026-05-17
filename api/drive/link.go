package drive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
)

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

// CreateTime returns the creation timestamp without decryption.
func (l *Link) CreateTime() int64 { return l.protonLink.CreateTime }

// ModifyTime returns the modification timestamp. For files with an active
// revision, returns the revision's create time (which is the upload time).
func (l *Link) ModifyTime() int64 {
	if l.protonLink.Type == proton.LinkTypeFile && l.protonLink.FileProperties != nil {
		return l.protonLink.FileProperties.ActiveRevision.CreateTime
	}
	return l.protonLink.ModifyTime
}

// ExpirationTime returns the expiration timestamp without decryption.
func (l *Link) ExpirationTime() int64 { return l.protonLink.ExpirationTime }

// Size returns the file size. Folders return 0.
func (l *Link) Size() int64 {
	if l.protonLink.Type == proton.LinkTypeFile && l.protonLink.FileProperties != nil {
		return l.protonLink.FileProperties.ActiveRevision.Size
	}
	return 0
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
func (l *Link) Stat() FileInfo {
	l.cacheMu.RLock()
	if l.cachedStat != nil {
		defer l.cacheMu.RUnlock()
		return *l.cachedStat
	}
	l.cacheMu.RUnlock()

	l.cacheMu.Lock()
	defer l.cacheMu.Unlock()
	if l.cachedStat != nil {
		return *l.cachedStat
	}

	fi := FileInfo{
		LinkID:     l.protonLink.LinkID,
		Name:       l.Name,
		Size:       l.Size(),
		ModifyTime: l.ModifyTime(),
		CreateTime: l.CreateTime(),
		MIMEType:   l.protonLink.MIMEType,
		IsDir:      l.protonLink.Type == proton.LinkTypeFolder,
	}

	if l.share != nil && l.share.MemoryCacheLevel >= api.CacheMetadata {
		l.cachedStat = &fi
	}

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

// getParentKeyRing returns the parent's keyring for decryption.
func (l *Link) getParentKeyRing() (*crypto.KeyRing, error) {
	if l.parentLink == nil {
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
