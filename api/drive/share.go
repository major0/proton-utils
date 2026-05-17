package drive

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
)

// ShareMetadata represents the metadata for a Proton Drive share.
type ShareMetadata proton.ShareMetadata

// ShareTypePhotos is the undocumented share type for Proton Photos.
const ShareTypePhotos proton.ShareType = 4

// FormatShareType returns a human-readable label for a share type.
func FormatShareType(st proton.ShareType) string {
	switch st {
	case proton.ShareTypeMain:
		return "main"
	case proton.ShareTypeStandard:
		return "shared"
	case proton.ShareTypeDevice:
		return "device"
	case ShareTypePhotos:
		return "photos"
	default:
		return fmt.Sprintf("unknown(%d)", st)
	}
}

// Share represents a fully-resolved Proton Drive share with its keyring.
type Share struct {
	Link        *Link
	keyRing     *crypto.KeyRing
	protonShare *proton.Share
	resolver    LinkResolver
	volumeID    string // volume this share belongs to

	// MemoryCacheLevel controls in-memory caching of decrypted data on
	// Link objects. Default: CacheDisabled. Set via config-store.
	MemoryCacheLevel api.MemoryCacheLevel

	// DiskCacheLevel controls on-disk caching of encrypted API objects.
	// Default: DiskCacheDisabled.
	DiskCacheLevel api.DiskCacheLevel
}

// IsSystemShare returns true for shares that cannot have members (main, photos, device).
func (s *Share) IsSystemShare() bool {
	st := s.protonShare.Type
	return st == proton.ShareTypeMain || st == ShareTypePhotos || st == proton.ShareTypeDevice
}

// IsShared returns true for user-created standard shares that can have members.
func (s *Share) IsShared() bool {
	return s.protonShare.Type == proton.ShareTypeStandard
}

// TypeName returns a human-readable label for the share's type.
func (s *Share) TypeName() string {
	return FormatShareType(s.protonShare.Type)
}

// GetName returns the decrypted name of the share's root link.
func (s *Share) GetName(_ context.Context) (string, error) {
	return s.Link.Name()
}

// Metadata returns the share's metadata (type, state, flags, creator, etc.).
func (s *Share) Metadata() proton.ShareMetadata {
	return s.protonShare.ShareMetadata
}

// ListChildren returns the child links of the share's root folder.
func (s *Share) ListChildren(ctx context.Context, all bool) ([]*Link, error) {
	slog.Debug("share.ListChildren", "all", all)
	return s.Link.ListChildren(ctx, all)
}

// ResolvePath resolves a slash-separated path relative to the share's root link.
func (s *Share) ResolvePath(ctx context.Context, path string, all bool) (*Link, error) {
	slog.Debug("share.ResolvePath", "shareID", s.Metadata().ShareID, "all", all)
	return s.Link.ResolvePath(ctx, path, all)
}

// ProtonShare returns the raw proton.Share. Used by the client package
// for API operations that need raw share fields.
func (s *Share) ProtonShare() *proton.Share { return s.protonShare }

// KeyRingValue returns the share's keyring.
func (s *Share) KeyRingValue() *crypto.KeyRing { return s.keyRing }

// VolumeID returns the volume ID this share belongs to.
func (s *Share) VolumeID() string { return s.volumeID }

// NewShare constructs a Share. Used by the client package.
func NewShare(pShare *proton.Share, keyRing *crypto.KeyRing, link *Link, resolver LinkResolver, volumeID string) *Share {
	return &Share{
		protonShare: pShare,
		keyRing:     keyRing,
		Link:        link,
		resolver:    resolver,
		volumeID:    volumeID,
	}
}

func (s *Share) getKeyRing() (*crypto.KeyRing, error) {
	linkKR, ok := s.resolver.AddressKeyRing(s.protonShare.AddressID)
	if !ok {
		return nil, api.ErrKeyNotFound
	}
	return s.protonShare.GetKeyRing(linkKR)
}

// VolumeOrigin returns "root", "photos", or "unknown" for a given volumeID
// by comparing against the account's known share types. If any share on the
// same volume is the main share, the origin is "root". If any share on the
// same volume is the photos share, the origin is "photos".
// Pure lookup against cached metadata — no additional API calls beyond the
// initial ListSharesMetadata (which is cached by the session).
func (c *Client) VolumeOrigin(ctx context.Context, volumeID string) string {
	metas, err := c.ListSharesMetadata(ctx, true)
	if err != nil {
		return "unknown"
	}

	for _, meta := range metas {
		if meta.VolumeID != volumeID {
			continue
		}
		switch meta.Type {
		case proton.ShareTypeMain:
			return "root"
		case ShareTypePhotos:
			return "photos"
		}
	}

	return "unknown"
}
