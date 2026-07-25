package drive

import (
	"bytes"
	"context"
	"encoding/gob"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
)

// SanitizeLinkID strips '=' padding from a LinkID for use as a directory
// entry name. Proton LinkIDs are base64-encoded and may contain trailing
// '=' which is problematic in filesystem paths.
func SanitizeLinkID(id string) string {
	return strings.TrimRight(id, "=")
}

// InitObjectCache constructs the shared ObjectCache instance if the config
// has any share with disk_cache: objectstore and $XDG_RUNTIME_DIR is
// set. The cache is a single flat namespace at
// $XDG_RUNTIME_DIR/proton/drive/ — shared across all shares because
// LinkIDs are globally unique and shares are windows into the same
// volume system.
func (c *Client) InitObjectCache() {
	if c.Config == nil {
		return
	}

	needDisk := false
	for _, sc := range c.Config.Shares {
		if sc.DiskCache == api.DiskCacheObjectStore {
			needDisk = true
			break
		}
	}
	if !needDisk {
		return
	}

	xdgRuntimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntimeDir == "" {
		slog.Warn("InitObjectCache: $XDG_RUNTIME_DIR is unset, disk cache disabled")
		return
	}

	basePath := filepath.Join(xdgRuntimeDir, "proton", "drive")
	c.objectCache = api.NewObjectCache(basePath)

	// Initialize the shared block store with the disk cache and buffer cache.
	c.blockStore = newBlockStore(c.Session, c.objectCache, newBufferCache(64))

	// Hydrate the staging map from existing cache entries. Blocks startup.
	c.hydrateFromCache()
}

// tryObjectCacheHit checks the hydration staging map and then the ObjectCache
// for a complete entry. Returns the Link and true on hit. Erases stale/corrupt
// entries from disk and returns false on miss.
func (c *Client) tryObjectCacheHit(linkID string) (proton.Link, bool) {
	// 1. Check staging map under tableMu.
	c.tableMu.Lock()
	if staged, ok := c.hydratedLinks[linkID]; ok {
		if staged.Type == proton.LinkTypeFolder {
			delete(c.hydratedLinks, linkID)
			c.tableMu.Unlock()
			return staged, true
		}
		if staged.FileProperties != nil && staged.FileProperties.ActiveRevision.XAttr != "" {
			delete(c.hydratedLinks, linkID)
			c.tableMu.Unlock()
			return staged, true
		}
		// Staged file with empty XAttr — leave for takeStagedIncomplete.
	}
	c.tableMu.Unlock()

	// 2. Check ObjectCache (no lock held during disk I/O).
	data, _ := c.objectCache.Read(SanitizeLinkID(linkID))
	if data == nil {
		return proton.Link{}, false
	}

	var pLink proton.Link
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&pLink); err != nil {
		_ = c.objectCache.Erase(SanitizeLinkID(linkID))
		if c.cacheCount.Load() > 0 {
			c.cacheCount.Add(-1)
		}
		slog.Debug("objectCache.decode", "key", linkID, "error", err)
		return proton.Link{}, false
	}

	// Folder: hit unconditionally.
	if pLink.Type == proton.LinkTypeFolder {
		return pLink, true
	}

	// File: hit only if XAttr is non-empty.
	if pLink.FileProperties != nil && pLink.FileProperties.ActiveRevision.XAttr != "" {
		return pLink, true
	}

	// Incomplete file entry on disk — erase.
	_ = c.objectCache.Erase(SanitizeLinkID(linkID))
	if c.cacheCount.Load() > 0 {
		c.cacheCount.Add(-1)
	}
	slog.Debug("objectCache.stale", "key", linkID)
	return proton.Link{}, false
}

// takeStagedIncomplete removes and returns a staged incomplete file-type Link
// (empty XAttr but valid ActiveRevision.ID) for seeding the fetch path.
// Returns nil if no such entry exists.
func (c *Client) takeStagedIncomplete(linkID string) *proton.Link {
	c.tableMu.Lock()
	defer c.tableMu.Unlock()
	staged, ok := c.hydratedLinks[linkID]
	if !ok {
		return nil
	}
	if staged.Type != proton.LinkTypeFile {
		return nil
	}
	if staged.FileProperties == nil || staged.FileProperties.ActiveRevision.ID == "" {
		return nil
	}
	if staged.FileProperties.ActiveRevision.XAttr != "" {
		return nil // Complete entry — should have been caught by tryObjectCacheHit.
	}
	delete(c.hydratedLinks, linkID)
	return &staged
}

// hydrateFromCache loads ObjectCache entries into the hydration staging map
// on startup. It erases corrupt or incomplete entries and initializes
// cacheCount to the number of complete entries remaining on disk.
func (c *Client) hydrateFromCache() {
	if c.objectCache == nil {
		return
	}

	cancel := make(chan struct{})
	defer close(cancel)

	staged := make(map[string]proton.Link)
	var completeOnDisk int64

	for key := range c.objectCache.Keys(cancel) {
		// Skip block cache entries.
		if strings.Contains(key, ".block.") {
			continue
		}

		data, err := c.objectCache.Read(key)
		if err != nil || data == nil {
			continue
		}

		var pLink proton.Link
		if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&pLink); err != nil {
			// Corrupt entry — erase and continue.
			_ = c.objectCache.Erase(key)
			slog.Debug("hydrateFromCache: decode failed", "key", key, "error", err)
			continue
		}

		// File-type validation.
		if pLink.Type == proton.LinkTypeFile {
			if pLink.FileProperties == nil || pLink.FileProperties.ActiveRevision.ID == "" {
				// Invalid file entry — erase.
				_ = c.objectCache.Erase(key)
				slog.Debug("hydrateFromCache: invalid file entry", "key", key)
				continue
			}

			if pLink.FileProperties.ActiveRevision.XAttr == "" {
				// Incomplete file entry (valid revision but no XAttr):
				// erase from disk, stage for XAttr-only completion.
				_ = c.objectCache.Erase(key)
				staged[pLink.LinkID] = pLink
				continue
			}
		}

		// Complete entry (file with XAttr, or folder) — stage it.
		completeOnDisk++
		staged[pLink.LinkID] = pLink
	}

	// Batch-insert staged entries under tableMu.
	c.tableMu.Lock()
	for id, link := range staged {
		c.hydratedLinks[id] = link
	}
	c.tableMu.Unlock()

	c.cacheCount.Store(completeOnDisk)
}

// fetchLinkWithXAttr performs the sequential Link+XAttr fetch inline in
// the calling goroutine. It never acquires a semaphore slot — concurrency
// across different links comes from the StatLinks/FindLinkByName fan-out.
func (c *Client) fetchLinkWithXAttr(ctx context.Context, shareID, linkID string, diskAllowed bool, staged *proton.Link) (proton.Link, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, maxFetchTimeout)
	defer cancel()

	// 1. Obtain the Link.
	var pLink proton.Link
	if staged != nil {
		pLink = *staged
	} else {
		var err error
		pLink, err = c.fetcher.GetLink(fetchCtx, shareID, linkID)
		if err != nil {
			return proton.Link{}, err
		}
	}

	// 2. Folder-type: XAttr is at the link level; write immediately.
	if pLink.Type == proton.LinkTypeFolder {
		if diskAllowed {
			c.writeCacheEntry(linkID, pLink)
		}
		return pLink, nil
	}

	// 3. File-type: need revision ID for XAttr fetch.
	if pLink.FileProperties == nil || pLink.FileProperties.ActiveRevision.ID == "" {
		return pLink, nil
	}
	revID := pLink.FileProperties.ActiveRevision.ID

	// Give-up gate: skip XAttr if too many consecutive failures.
	c.tableMu.RLock()
	giveUp := c.xattrFailCount[linkID] >= maxXAttrRetries
	c.tableMu.RUnlock()
	if giveUp {
		return pLink, nil
	}

	// 4. Fetch XAttr via revision.
	fullRev, err := c.fetcher.GetRevisionAllBlocks(fetchCtx, shareID, linkID, revID)
	if err != nil {
		c.tableMu.Lock()
		c.xattrFailCount[linkID]++
		c.tableMu.Unlock()
		return pLink, nil
	}

	// 5. Combine and write.
	pLink.FileProperties.ActiveRevision.XAttr = fullRev.XAttr
	pLink.FileProperties.ActiveRevision.Size = fullRev.Size
	c.tableMu.Lock()
	delete(c.xattrFailCount, linkID)
	c.tableMu.Unlock()
	if diskAllowed {
		c.writeCacheEntry(linkID, pLink)
	}
	return pLink, nil
}

// runEviction performs the background eviction pass. It iterates all
// non-block keys, stats each file for mtime, sorts by oldest-write-first,
// and erases entries until the remaining count is at or below evictionTarget.
// Resyncs cacheCount at the end of the pass. Called from triggerEviction
// in a new goroutine.
func (c *Client) runEviction() {
	defer c.evicting.Store(false)

	cancel := make(chan struct{})
	defer close(cancel)

	type entry struct {
		key   string
		mtime time.Time
	}

	var entries []entry
	for key := range c.objectCache.Keys(cancel) {
		if strings.Contains(key, ".block.") {
			continue
		}
		path := c.objectCache.PathFor(key)
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		entries = append(entries, entry{key: key, mtime: info.ModTime()})
	}

	// If total count is within bounds after scanning, resync and return.
	if len(entries) <= evictionTarget {
		c.cacheCount.Store(int64(len(entries)))
		return
	}

	// Sort by mtime ascending (oldest write first).
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].mtime.Before(entries[j].mtime)
	})

	// Erase oldest entries until remaining count <= evictionTarget.
	remaining := len(entries)
	toEvict := remaining - evictionTarget
	for i := 0; i < toEvict; i++ {
		if err := c.objectCache.Erase(entries[i].key); err == nil {
			c.cacheCount.Add(-1)
			remaining--
		} else if !os.IsNotExist(err) {
			// Log non-"file not found" errors; skip the entry.
			slog.Debug("triggerEviction: erase failed", "key", entries[i].key, "error", err)
		}
		// "file not found" means another goroutine (invalidation) already
		// removed it — do NOT decrement to avoid double-decrement.
	}

	// Resync cacheCount to the observed remaining non-.block. key count.
	c.cacheCount.Store(int64(remaining))
}
