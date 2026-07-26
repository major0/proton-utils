// Package drive provides the Proton Drive API
package drive

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
	"golang.org/x/sync/singleflight"
)

// linkFetcher abstracts the API calls needed to fetch a link and its
// revision metadata. The production implementation is *proton.Client;
// tests substitute a mock to exercise the fetch path without network.
type linkFetcher interface {
	GetLink(ctx context.Context, shareID, linkID string) (proton.Link, error)
	GetRevisionAllBlocks(ctx context.Context, shareID, linkID, revisionID string) (proton.Revision, error)
}

// driveEventFetcher abstracts the Drive event API calls needed by the
// event poller. The production implementation is *proton.Client; tests
// substitute a mock to exercise the poll path without a live session.
// This seam is shared with the protonfs-event-invalidation daemon.
type driveEventFetcher interface {
	GetVolumeEvent(ctx context.Context, volumeID, eventID string) (proton.DriveEvent, error)
	GetShareEvent(ctx context.Context, shareID, eventID string) (proton.DriveEvent, error)
	GetLatestVolumeEventID(ctx context.Context, volumeID string) (string, error)
	GetLatestShareEventID(ctx context.Context, shareID string) (string, error)
}

// Cache capacity and fetch-path constants.
const (
	maxCacheEntries = 10000            // trigger eviction above this many link entries
	evictionTarget  = 9000             // evict down to this count (90% of max)
	maxXAttrRetries = 3                // consecutive XAttr failures before giving up
	maxFetchTimeout = 30 * time.Second // deadline for combined Link+XAttr fetch
)

// Client wraps an api.Session with Drive-specific state and operations.
// Implements LinkResolver.
type Client struct {
	Session         *api.Session
	Config          *api.SessionConfig // loaded config for cache policy lookup; may be nil
	PrefetchBlocks  int                // number of blocks to prefetch ahead on read (0 = disabled)
	BlockCacheMode  string             // "encrypted" or "decrypted"; controls buffer cache content type
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

	// linkFlight deduplicates concurrent GetCachedLink cache misses
	// on the same shareID/linkID pair via singleflight.
	linkFlight singleflight.Group

	// xattrFailCount tracks consecutive XAttr fetch failures per LinkID.
	// Protected by tableMu.
	xattrFailCount map[string]int

	// hydratedLinks is the startup hydration staging map. Stores raw
	// proton.Link structs keyed by LinkID, populated during
	// hydrateFromCache. Protected by tableMu.
	hydratedLinks map[string]proton.Link

	// cacheCount is the current number of link entries in the
	// ObjectCache (excludes block entries).
	cacheCount atomic.Int64

	// evicting prevents concurrent eviction goroutines.
	evicting atomic.Bool

	// fetcher abstracts API calls for link and revision fetches,
	// allowing tests to substitute a mock without network access.
	fetcher linkFetcher

	// eventFetcher abstracts Drive event API calls for the poller,
	// allowing tests to substitute a mock without network access.
	eventFetcher driveEventFetcher

	// invalidationHook, when set, is invoked with a parent LinkID whenever
	// a child link is created or removed, so consumers (the FUSE
	// DriveHandler) can refresh their own per-directory caches. Guarded by
	// hookMu.
	invalidationHook func(parentLinkID string)
	hookMu           sync.RWMutex
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
		xattrFailCount:  make(map[string]int),
		hydratedLinks:   make(map[string]proton.Link),
		blockStore:      newBlockStore(session, nil, nil),
		fetcher:         session.Client,
		eventFetcher:    session.Client,
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
// NewChildLink constructs a child Link from a raw proton.Link, inserting
// it into the link table. If the link is already in the table, the
// existing pointer is returned to maintain pointer identity.
// Callers that need fresh state should delete the link from the table
// first (via deleteLink) so the next access re-fetches from the API.
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

	// Remove any hydrated staging entry — the link is now in the table.
	delete(c.hydratedLinks, pLink.LinkID)
	c.tableMu.Unlock()

	// Write folder-type links to objectCache only when the share permits
	// disk caching. File-type links are written exclusively by
	// GetCachedLink's fetch path after the complete entry (Link + XAttr)
	// is assembled (Requirement 3.5).
	if pLink.Type == proton.LinkTypeFolder && parent.Share() != nil && parent.Share().DiskCacheLevel >= api.DiskCacheObjectStore {
		c.writeCacheEntry(pLink.LinkID, *pLink)
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
// On a cache hit, returns the complete entry directly (file-type links
// include XAttr). On a miss, dispatches a singleflight sequential fetch
// (GetLink → GetRevisionAllBlocks) and writes the complete entry to the
// ObjectCache on success.
//
// Note: GetShare bypasses this and calls the API directly — share root
// links have a cache interaction issue that needs further investigation.
func (c *Client) GetCachedLink(ctx context.Context, shareID, linkID string) (proton.Link, error) {
	diskAllowed := c.sharePermitsDiskCache(shareID)

	// 1. Complete-entry hit (staging map, then ObjectCache).
	if diskAllowed {
		if pLink, ok := c.tryObjectCacheHit(linkID); ok {
			return pLink, nil
		}
	}

	// 2. Check for staged incomplete entry to seed the fetch.
	var staged *proton.Link
	if diskAllowed {
		staged = c.takeStagedIncomplete(linkID)
	}

	// 3. Singleflight fetch.
	key := shareID + "/" + linkID
	result, err, _ := c.linkFlight.Do(key, func() (interface{}, error) {
		return c.fetchLinkWithXAttr(ctx, shareID, linkID, diskAllowed, staged)
	})
	if err != nil {
		return proton.Link{}, err
	}
	return result.(proton.Link), nil
}

// writeCacheEntry gob-encodes pLink and writes it to the ObjectCache.
// Increments cacheCount on success and calls triggerEviction. All disk
// errors are logged at slog.Debug and swallowed (Req 9).
func (c *Client) writeCacheEntry(linkID string, pLink proton.Link) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(pLink); err != nil {
		slog.Debug("objectCache.encode", "key", linkID, "error", err)
		return
	}
	if err := c.objectCache.Write(SanitizeLinkID(linkID), buf.Bytes()); err != nil {
		slog.Debug("objectCache.Write", "key", linkID, "error", err)
		return
	}
	c.cacheCount.Add(1)
	c.triggerEviction()
}

// triggerEviction spawns a background goroutine to evict oldest entries
// when cacheCount exceeds maxCacheEntries. At most one goroutine runs at
// a time (guarded by evicting CAS).
func (c *Client) triggerEviction() {
	if c.cacheCount.Load() <= int64(maxCacheEntries) {
		return
	}
	if !c.evicting.CompareAndSwap(false, true) {
		return
	}
	go c.runEviction()
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

// eraseCacheEntry removes a link entry from the ObjectCache, decrements
// cacheCount on success, and clears the xattrFailCount for the link.
// Errors are logged at debug level and swallowed.
func (c *Client) eraseCacheEntry(linkID string) {
	if err := c.objectCache.Erase(SanitizeLinkID(linkID)); err == nil {
		if c.cacheCount.Load() > 0 {
			c.cacheCount.Add(-1)
		}
	}
	c.tableMu.Lock()
	delete(c.xattrFailCount, linkID)
	c.tableMu.Unlock()
}

// clearLinks removes all entries from the table, clears xattrFailCount
// and hydratedLinks, erases all ObjectCache entries, and resets
// cacheCount to 0. Takes an exclusive write lock.
func (c *Client) clearLinks() {
	c.tableMu.Lock()
	c.linkTable = make(map[string]*Link)
	c.xattrFailCount = make(map[string]int)
	c.hydratedLinks = make(map[string]proton.Link)
	c.tableMu.Unlock()
	_ = c.objectCache.EraseAll()
	c.cacheCount.Store(0)
}
