package client

// Clear resets all Drive client caches — the in-memory Link Table and
// the on-disk ObjectCache. Intended for session logout or full reset.
func (c *Client) Clear() error {
	c.clearLinks()
	return objectCacheEraseAll(c.objectCache)
}
