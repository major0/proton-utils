package drive

import (
	"context"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
)

// LinkResolver is the interface that Link depends on for operations
// requiring API calls or client-owned state. Implemented by
// Client.
//
// This interface breaks the Link ↔ Client dependency cycle and enables
// testing Link behavior with mock resolvers.
type LinkResolver interface {
	// ListLinkChildren fetches raw child links from the API.
	ListLinkChildren(ctx context.Context, shareID, linkID string, all bool) ([]proton.Link, error)

	// NewChildLink constructs a child Link from a raw proton.Link.
	NewChildLink(ctx context.Context, parent *Link, pLink *proton.Link) *Link

	// AddressForEmail returns the proton.Address for the given email.
	AddressForEmail(email string) (proton.Address, bool)

	// AddressKeyRing returns the keyring for the given address ID.
	AddressKeyRing(addressID string) (*crypto.KeyRing, bool)

	// Throttle returns the rate limiter, or nil if none is configured.
	Throttle() *api.Throttle

	// MaxWorkers returns the concurrency limit for parallel operations.
	MaxWorkers() int
}
