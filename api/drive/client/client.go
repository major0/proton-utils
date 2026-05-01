// Package client provides the Proton Drive API client.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/drive"
	"github.com/peterbourgon/diskv/v3"
)

// Client wraps an api.Session with Drive-specific state and operations.
// Implements drive.LinkResolver.
type Client struct {
	Session         *api.Session
	Config          *api.Config // loaded config for cache policy lookup; may be nil
	addresses       map[string]proton.Address
	addressKeyRings map[string]*crypto.KeyRing

	// linkTable is the in-memory Link Table keyed by LinkID. Same LinkID
	// always returns the same *Link pointer within a session. Protected
	// by tableMu.
	linkTable map[string]*drive.Link
	tableMu   sync.RWMutex

	// objectCache is the diskv-backed on-disk cache for encrypted API
	// objects. Nil when disk_cache is disabled or $XDG_RUNTIME_DIR is
	// unset. Callers must handle nil gracefully.
	objectCache *diskv.Diskv
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
		linkTable:       make(map[string]*drive.Link),
	}, nil
}

// ListLinkChildren fetches raw child links from the API.
func (c *Client) ListLinkChildren(ctx context.Context, shareID, linkID string, all bool) ([]proton.Link, error) {
	return c.Session.Client.ListChildren(ctx, shareID, linkID, all)
}

// NewChildLink constructs a child Link from a raw proton.Link. If the
// Link Table already contains an entry for this LinkID, the existing
// *Link pointer is returned (pointer identity guarantee). On a table
// miss, a new *Link is constructed with the correct parentLink, inserted
// into the table, and returned.
func (c *Client) NewChildLink(_ context.Context, parent *drive.Link, pLink *proton.Link) *drive.Link {
	// Fast path: table hit — return existing pointer.
	if existing := c.getLink(pLink.LinkID); existing != nil {
		return existing
	}

	// Table miss: construct, insert into table, populate objectCache.
	link := drive.NewLink(pLink, parent, parent.Share(), c)
	c.putLink(pLink.LinkID, link)

	// Best-effort write to objectCache (no-op when nil).
	if data, err := json.Marshal(pLink); err == nil {
		_ = objectCacheWrite(c.objectCache, sanitizeKey(pLink.LinkID), data)
	}

	return link
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

// getLink returns the *Link for linkID from the table, or nil if absent.
// Takes a read lock — concurrent reads are allowed.
func (c *Client) getLink(linkID string) *drive.Link {
	c.tableMu.RLock()
	defer c.tableMu.RUnlock()
	return c.linkTable[linkID]
}

// GetCachedLink fetches a raw proton.Link by ID, checking the object cache
// first and populating it on a miss. This is the single chokepoint for
// all link fetches from the API — every code path that needs a
// proton.Link should call this instead of c.Session.Client.GetLink.
func (c *Client) GetCachedLink(ctx context.Context, shareID, linkID string) (proton.Link, error) {
	// ObjectCache hit — return without API call.
	if data, err := objectCacheRead(c.objectCache, sanitizeKey(linkID)); err == nil && data != nil {
		var pLink proton.Link
		if err := json.Unmarshal(data, &pLink); err == nil {
			return pLink, nil
		}
		// Unmarshal failed — fall through to API fetch.
	}

	// API fetch.
	pLink, err := c.Session.Client.GetLink(ctx, shareID, linkID)
	if err != nil {
		return proton.Link{}, err
	}

	// Populate objectCache (no-op when nil).
	if data, err := json.Marshal(pLink); err == nil {
		_ = objectCacheWrite(c.objectCache, sanitizeKey(linkID), data)
	}

	return pLink, nil
}

// putLink inserts a *Link into the table. Takes an exclusive write lock.
// Lazily initializes the table if needed (for Clients not constructed
// via NewClient, e.g. in tests).
func (c *Client) putLink(linkID string, link *drive.Link) {
	c.tableMu.Lock()
	defer c.tableMu.Unlock()
	if c.linkTable == nil {
		c.linkTable = make(map[string]*drive.Link)
	}
	c.linkTable[linkID] = link
}

// deleteLink removes a *Link from the table. Takes an exclusive write lock.
func (c *Client) deleteLink(linkID string) {
	c.tableMu.Lock()
	defer c.tableMu.Unlock()
	delete(c.linkTable, linkID)
}

// clearLinks removes all entries from the table. Takes an exclusive write lock.
func (c *Client) clearLinks() {
	c.tableMu.Lock()
	defer c.tableMu.Unlock()
	c.linkTable = make(map[string]*drive.Link)
}
