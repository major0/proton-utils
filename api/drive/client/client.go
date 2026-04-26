// Package client provides the Proton Drive API client.
package client

import (
	"context"
	"fmt"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/drive"
)

// Client wraps an api.Session with Drive-specific state and operations.
// Implements drive.LinkResolver.
type Client struct {
	Session         *api.Session
	Config          *api.Config // loaded config for cache policy lookup; may be nil
	addresses       map[string]proton.Address
	addressKeyRings map[string]*crypto.KeyRing
}

// Verify Client implements LinkResolver at compile time.
var _ drive.LinkResolver = (*Client)(nil)

// NewClient constructs a Drive client from an existing session.
func NewClient(ctx context.Context, session *api.Session) (*Client, error) {
	addrs, err := session.Addresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("drive.NewClient: %w", err)
	}

	addrMap := make(map[string]proton.Address, len(addrs))
	for _, addr := range addrs {
		addrMap[addr.Email] = addr
	}

	return &Client{
		Session:         session,
		addresses:       addrMap,
		addressKeyRings: session.AddressKeyRings(),
	}, nil
}

// ListLinkChildren fetches raw child links from the API.
func (c *Client) ListLinkChildren(ctx context.Context, shareID, linkID string, all bool) ([]proton.Link, error) {
	return c.Session.Client.ListChildren(ctx, shareID, linkID, all)
}

// NewChildLink constructs a child Link from a raw proton.Link.
func (c *Client) NewChildLink(_ context.Context, parent *drive.Link, pLink *proton.Link) *drive.Link {
	return drive.NewLink(pLink, parent, parent.Share(), c)
}

// AddressForEmail returns the proton.Address for the given email.
func (c *Client) AddressForEmail(email string) (proton.Address, bool) {
	addr, ok := c.addresses[email]
	return addr, ok
}

// AddressKeyRing returns the keyring for the given address ID.
func (c *Client) AddressKeyRing(addressID string) (*crypto.KeyRing, bool) {
	kr, ok := c.addressKeyRings[addressID]
	return kr, ok
}

// Throttle returns the session's rate limiter.
func (c *Client) Throttle() *api.Throttle {
	return c.Session.Throttle
}

// MaxWorkers returns the default concurrency limit for parallel operations.
func (c *Client) MaxWorkers() int {
	return api.DefaultMaxWorkers()
}
