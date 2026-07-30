package drive

// This file implements the exported cache-invalidation seam consumed by the
// protonfs-event-invalidation daemon. deleteLink is unexported, so the daemon
// (which lives outside this package) cannot call it directly; these methods
// perform the full invalidation in one call. They are best-effort: disk
// errors are swallowed by the helpers they delegate to (eraseCacheEntry),
// tableMu is never held across disk I/O, and it is safe to invalidate a
// linkID that is not cached.

// InvalidateLink removes a link from all caches: the in-memory Link Table,
// the on-disk object cache (with cacheCount decrement and xattrFailCount
// reset, via eraseCacheEntry), and — for file links (blockCount > 0) — the
// block cache. It then fires the invalidation hook for parentLinkID (when
// non-empty) so the FUSE layer can refresh the parent's children listing.
//
// linkID, parentLinkID, and blockCount all come from the triggering event's
// embedded Link, so the caller supplies them directly. This is not an atomic
// transaction (tableMu is released before disk I/O per the concurrency
// rules); it is a single convenience entry point.
func (c *Client) InvalidateLink(linkID, parentLinkID string, blockCount int) {
	c.deleteLink(linkID)
	c.eraseCacheEntry(linkID)
	if blockCount > 0 {
		c.blockStore.Invalidate(linkID, blockCount)
	}
	// Clear the parent's cached child listing on the retained Link object.
	// The child was evicted above, so Readdir would fall through to the API
	// anyway; clearing here makes the stale listing explicit and matches the
	// local-mutation pattern (deleteLink + InvalidateChildren).
	if parent := c.getLink(parentLinkID); parent != nil {
		parent.InvalidateChildren()
	}
	c.fireInvalidationHook(parentLinkID)
}

// InvalidateParent invalidates a parent directory: it removes the parent's
// Link Table and object-cache entries (so the next access re-lists children)
// and fires the invalidation hook for parentLinkID. Used for child
// create/delete events where only the parent listing is stale.
func (c *Client) InvalidateParent(parentLinkID string) {
	if parentLinkID == "" {
		return
	}
	// Clear the parent's cached child listing before deleting the table
	// entry. The FUSE layer retains a direct reference to the parent *Link,
	// so deleting the table entry alone does not refresh its cachedChildIDs;
	// without this, a removed child lingers in the listing until the child
	// link itself is evicted. This path does not evict the child, so the
	// explicit clear is required (matches the mkdir/remove local pattern).
	if parent := c.getLink(parentLinkID); parent != nil {
		parent.InvalidateChildren()
	}
	c.deleteLink(parentLinkID)
	c.eraseCacheEntry(parentLinkID)
	c.fireInvalidationHook(parentLinkID)
}

// SetInvalidationHook registers a callback invoked with the parent LinkID
// whenever a child is created or removed. It is nil-safe and stores at most
// one hook (the most recent registration wins). Passing nil clears the hook.
func (c *Client) SetInvalidationHook(hook func(parentLinkID string)) {
	c.hookMu.Lock()
	c.invalidationHook = hook
	c.hookMu.Unlock()
}

// fireInvalidationHook invokes the registered hook (if any) for a non-empty
// parent LinkID.
func (c *Client) fireInvalidationHook(parentLinkID string) {
	if parentLinkID == "" {
		return
	}
	c.hookMu.RLock()
	hook := c.invalidationHook
	c.hookMu.RUnlock()
	if hook != nil {
		hook(parentLinkID)
	}
}
