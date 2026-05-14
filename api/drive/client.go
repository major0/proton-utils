// Package drive provides the Proton Drive API
package drive

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
)

// Client wraps an api.Session with Drive-specific state and operations.
// Implements LinkResolver.
type Client struct {
	Session         *api.Session
	Config          *api.SessionConfig // loaded config for cache policy lookup; may be nil
	addresses       map[string]proton.Address
	addressKeyRings map[string]*crypto.KeyRing

	// linkTable is the in-memory Link Table keyed by LinkID. Same LinkID
	// always returns the same *Link pointer within a session. Protected
	// by tableMu.
	linkTable map[string]*Link
	tableMu   sync.RWMutex

	// objectCache is the on-disk cache for encrypted API objects backed
	// by api.ObjectCache. Nil when disk_cache is disabled or
	// $XDG_RUNTIME_DIR is unset. Callers must handle nil gracefully
	// (all ObjectCache methods are nil-safe).
	objectCache *api.ObjectCache

	// blockStore is the shared block store for all block I/O. Created
	// lazily after InitObjectCache so the disk cache is wired up.
	blockStore blockStore
}

// Verify Client implements LinkResolver at compile time.
var _ LinkResolver = (*Client)(nil)

// NewClient constructs a Drive client from an existing session.
func NewClient(ctx context.Context, session *api.Session) (*Client, error) {
	addrs, err := session.Addresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("NewClient: %w", err)
	}

	addrMap := make(map[string]proton.Address, len(addrs))
	for _, addr := range addrs {
		addrMap[addr.Email] = addr
	}

	return &Client{
		Session:         session,
		addresses:       addrMap,
		addressKeyRings: session.AddressKeyRings(),
		linkTable:       make(map[string]*Link),
		blockStore:      newBlockStore(session, nil, nil),
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
//
// Uses a load-or-store pattern under a single write lock to prevent
// races where two goroutines both miss the table and both insert,
// which would break the pointer-identity invariant.
func (c *Client) NewChildLink(_ context.Context, parent *Link, pLink *proton.Link) *Link {
	c.tableMu.Lock()
	if existing := c.linkTable[pLink.LinkID]; existing != nil {
		c.tableMu.Unlock()
		return existing
	}
	link := NewLink(pLink, parent, parent.Share(), c)
	if c.linkTable == nil {
		c.linkTable = make(map[string]*Link)
	}
	c.linkTable[pLink.LinkID] = link
	c.tableMu.Unlock()

	// Write to objectCache only when the share permits disk caching.
	if parent.Share() != nil && parent.Share().DiskCacheLevel >= api.DiskCacheObjectStore {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(pLink); err == nil {
			if err := c.objectCache.Write(sanitizeKey(pLink.LinkID), buf.Bytes()); err != nil {
				slog.Debug("objectCache.Write", "key", pLink.LinkID, "error", err)
			}
		}
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

// InternalBlockStore returns the client's shared block store. This is a
// temporary accessor for cmd/ code that constructs copy pipelines
// directly. It should be removed when block I/O is fully encapsulated
// in the client package.
func (c *Client) InternalBlockStore() blockStore {
	return c.blockStore
}

// GetLink returns the *Link for linkID from the Link Table, or nil if
// absent. This is the exported accessor for O(1) link resolution by ID.
// Takes a read lock — concurrent reads are allowed.
func (c *Client) GetLink(linkID string) *Link {
	c.tableMu.RLock()
	defer c.tableMu.RUnlock()
	return c.linkTable[linkID]
}

// getLink returns the *Link for linkID from the table, or nil if absent.
// Takes a read lock — concurrent reads are allowed.
func (c *Client) getLink(linkID string) *Link {
	return c.GetLink(linkID)
}

// GetCachedLink fetches a raw proton.Link by ID. This is the single
// chokepoint for all link fetches from the API — every code path that
// needs a proton.Link should call this instead of
// c.Session.Client.GetLink.
//
// GetCachedLink fetches a raw proton.Link by ID, checking the object
// cache first and populating it on a miss. Uses gob encoding for
// faithful struct round-trip. When the cache is nil (disabled or
// XDG_RUNTIME_DIR unset), falls straight through to the API.
//
// Note: GetShare bypasses this and calls the API directly — share root
// links have a cache interaction issue that needs further investigation.
func (c *Client) GetCachedLink(ctx context.Context, shareID, linkID string) (proton.Link, error) {
	// Only use the object cache if the share permits disk caching.
	diskAllowed := c.sharePermitsDiskCache(shareID)

	// ObjectCache hit — return without API call.
	if diskAllowed {
		if data, _ := c.objectCache.Read(sanitizeKey(linkID)); data != nil {
			var pLink proton.Link
			if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&pLink); err == nil {
				return pLink, nil
			}
			// Decode failed — fall through to API fetch.
		}
	}

	// API fetch.
	pLink, err := c.Session.Client.GetLink(ctx, shareID, linkID)
	if err != nil {
		return proton.Link{}, err
	}

	// Populate objectCache only when the share permits disk caching.
	if diskAllowed {
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(pLink); err == nil {
			if err := c.objectCache.Write(sanitizeKey(linkID), buf.Bytes()); err != nil {
				slog.Debug("objectCache.Write", "key", linkID, "error", err)
			}
		}
	}

	return pLink, nil
}

// sharePermitsDiskCache checks whether the share identified by shareID
// has disk caching enabled. Returns false if the config has no entry for
// the share or if disk caching is disabled.
func (c *Client) sharePermitsDiskCache(shareID string) bool {
	if c.Config == nil || c.objectCache == nil {
		return false
	}
	sc, ok := c.Config.Shares[shareID]
	if !ok {
		return false
	}
	return sc.DiskCache >= api.DiskCacheObjectStore
}

// putLink inserts a *Link into the table. Takes an exclusive write lock.
// Lazily initializes the table if needed (for Clients not constructed
// via NewClient, e.g. in tests).
func (c *Client) putLink(linkID string, link *Link) {
	c.tableMu.Lock()
	defer c.tableMu.Unlock()
	if c.linkTable == nil {
		c.linkTable = make(map[string]*Link)
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
	c.linkTable = make(map[string]*Link)
}
