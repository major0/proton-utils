package drive

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
)

// blockStore fetches and stores encrypted blocks. Session-aware and
// cache-aware — implementations check the in-memory buffer cache, then
// the on-disk object cache, before making HTTP requests.
type blockStore interface {
	// GetBlock fetches a raw encrypted block by linkID and block index.
	// Checks buffer cache, then disk cache, then HTTP. Returns the full
	// block bytes.
	GetBlock(ctx context.Context, linkID string, index int, bareURL, token string) ([]byte, error)
	// fetchBlock fetches an encrypted block from disk cache or HTTP,
	// bypassing the buffer cache. Used by the FD layer which manages
	// its own plaintext caching via the buffer cache.
	fetchBlock(ctx context.Context, linkID string, index int, bareURL, token string) ([]byte, error)
	// getBufCache returns the buffer cache (or nil if disabled).
	getBufCache() *bufferCache
	// RequestUpload obtains upload URLs for a batch of blocks.
	RequestUpload(ctx context.Context, req proton.BlockUploadReq) ([]proton.BlockUploadLink, error)
	// UploadBlock uploads an encrypted block to the given URL.
	UploadBlock(ctx context.Context, linkID string, index int, bareURL, token string, data []byte) error
	// Invalidate removes cached blocks for a linkID from both the
	// buffer cache and the on-disk cache.
	Invalidate(linkID string)
}

// blockReader wraps a []byte to satisfy resty.MultiPartStream.
type blockReader struct {
	r io.Reader
}

func (b *blockReader) GetMultipartReader() io.Reader { return b.r }

// httpBlockStore implements blockStore using the session's HTTP transport,
// an optional in-memory buffer cache, and an optional ObjectCache-backed
// on-disk block cache.
type httpBlockStore struct {
	session  *api.Session
	cache    *api.ObjectCache // nil when disk caching disabled
	bufCache *bufferCache     // nil when buffer caching disabled
}

// newBlockStore creates a blockStore backed by the session's HTTP transport.
// If diskCache is non-nil, blocks are checked/populated in the on-disk cache
// via ObjectCache. If bufCache is non-nil, blocks are checked/populated in
// the in-memory buffer cache.
func newBlockStore(session *api.Session, diskCache *api.ObjectCache, bufCache *bufferCache) blockStore {
	return &httpBlockStore{session: session, cache: diskCache, bufCache: bufCache}
}

// getBufCache returns the buffer cache (or nil if disabled).
func (s *httpBlockStore) getBufCache() *bufferCache {
	return s.bufCache
}

// blockCacheKey returns the cache key for a cached block.
func blockCacheKey(linkID string, index int) string {
	return fmt.Sprintf("%s.block.%d", linkID, index)
}

// GetBlock fetches a raw encrypted block. In encrypted mode the buffer
// cache is managed here: check Get → Reserve → fetchBlock → Put. In
// decrypted mode the FD layer calls fetchBlock directly and never
// reaches this method. When bufCache is nil, falls through to fetchBlock.
func (s *httpBlockStore) GetBlock(ctx context.Context, linkID string, index int, bareURL, token string) ([]byte, error) {
	if s.bufCache != nil {
		// Fast path: already cached.
		if data, err := s.bufCache.Get(linkID, index); data != nil || err != nil {
			return data, err
		}

		// Reserve a slot. If another goroutine holds it, re-Get to
		// block on their result.
		if !s.bufCache.Reserve(linkID, index) {
			if data, err := s.bufCache.Get(linkID, index); data != nil || err != nil {
				return data, err
			}
			// Slot was evicted/invalidated while waiting — fall through.
		} else {
			// We own the slot. Fetch encrypted bytes and Put.
			data, err := s.fetchBlock(ctx, linkID, index, bareURL, token)
			if err != nil {
				s.bufCache.PutError(linkID, index, err)
				return nil, err
			}
			s.bufCache.Put(linkID, index, data)
			return data, nil
		}
	}
	return s.fetchBlock(ctx, linkID, index, bareURL, token)
}

// fetchBlock reads a block from the disk cache or HTTP, populating the
// disk cache on HTTP fetch. Does not interact with the buffer cache.
func (s *httpBlockStore) fetchBlock(ctx context.Context, linkID string, index int, bareURL, token string) ([]byte, error) {
	// Disk cache check (ObjectCache).
	key := blockCacheKey(linkID, index)
	t0 := time.Now()
	if data, _ := s.cache.Read(key); data != nil {
		slog.Debug("blockstore.GetBlock disk-cache hit", "linkID", linkID, "block", index, "elapsed", time.Since(t0))
		return data, nil
	}
	if elapsed := time.Since(t0); elapsed > 100*time.Millisecond {
		slog.Warn("blockstore.GetBlock disk-cache SLOW miss", "linkID", linkID, "block", index, "elapsed", elapsed)
	}

	// HTTP fetch.
	t0 = time.Now()
	rc, err := s.session.Client.GetBlock(ctx, bareURL, token)
	if err != nil {
		return nil, fmt.Errorf("blockstore.GetBlock %s block %d: %w", linkID, index, err)
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("blockstore.GetBlock %s block %d: read: %w", linkID, index, err)
	}
	slog.Debug("blockstore.GetBlock HTTP", "linkID", linkID, "block", index, "size", len(data), "elapsed", time.Since(t0))

	// Populate disk cache (best-effort).
	if err := s.cache.Write(key, data); err != nil {
		slog.Debug("blockstore.cache.Write", "key", key, "error", err)
	}

	return data, nil
}

// RequestUpload obtains upload URLs for a batch of blocks.
func (s *httpBlockStore) RequestUpload(ctx context.Context, req proton.BlockUploadReq) ([]proton.BlockUploadLink, error) {
	links, err := s.session.Client.RequestBlockUpload(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("blockstore.RequestUpload: %w", err)
	}
	return links, nil
}

// UploadBlock uploads an encrypted block to the given URL.
func (s *httpBlockStore) UploadBlock(ctx context.Context, linkID string, index int, bareURL, token string, data []byte) error {
	stream := &blockReader{r: bytes.NewReader(data)}
	if err := s.session.Client.UploadBlock(ctx, bareURL, token, stream); err != nil {
		return fmt.Errorf("blockstore.UploadBlock %s block %d: %w", linkID, index, err)
	}

	// Populate disk cache only (always encrypted).
	// NOTE: bufCache.Put removed — avoids mode-dependent corruption.
	if err := s.cache.Write(blockCacheKey(linkID, index), data); err != nil {
		slog.Debug("blockstore.cache.Write", "key", blockCacheKey(linkID, index), "error", err)
	}

	return nil
}

// Invalidate removes cached blocks for a linkID from both the buffer
// cache and the on-disk ObjectCache.
func (s *httpBlockStore) Invalidate(linkID string) {
	if s.bufCache != nil {
		s.bufCache.Invalidate(linkID)
	}
	if s.cache != nil {
		prefix := linkID + ".block."
		cancel := make(chan struct{})
		defer close(cancel)
		for key := range s.cache.Keys(cancel) {
			if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
				_ = s.cache.Erase(key)
			}
		}
	}
}
