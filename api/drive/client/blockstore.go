package client

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
)

// BlockStore fetches and stores encrypted blocks. Session-aware and
// cache-aware — implementations check the in-memory buffer cache, then
// the on-disk object cache, before making HTTP requests.
type BlockStore interface {
	// GetBlock fetches a raw encrypted block by linkID and block index.
	// Checks buffer cache, then disk cache, then HTTP. Returns the full
	// block bytes.
	GetBlock(ctx context.Context, linkID string, index int, bareURL, token string) ([]byte, error)
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

// httpBlockStore implements BlockStore using the session's HTTP transport,
// an optional in-memory buffer cache, and an optional ObjectCache-backed
// on-disk block cache.
type httpBlockStore struct {
	session  *api.Session
	cache    *api.ObjectCache // nil when disk caching disabled
	bufCache *bufferCache     // nil when buffer caching disabled
}

// NewBlockStore creates a BlockStore backed by the session's HTTP transport.
// If diskCache is non-nil, blocks are checked/populated in the on-disk cache
// via ObjectCache. If bufCache is non-nil, blocks are checked/populated in
// the in-memory buffer cache.
func NewBlockStore(session *api.Session, diskCache *api.ObjectCache, bufCache *bufferCache) BlockStore {
	return &httpBlockStore{session: session, cache: diskCache, bufCache: bufCache}
}

// blockCacheKey returns the cache key for a cached block.
func blockCacheKey(linkID string, index int) string {
	return fmt.Sprintf("%s.block.%d", linkID, index)
}

// GetBlock fetches a raw encrypted block. Checks the buffer cache first,
// then the on-disk ObjectCache, then falls through to HTTP. Populates
// both caches on fetch.
func (s *httpBlockStore) GetBlock(ctx context.Context, linkID string, index int, bareURL, token string) ([]byte, error) {
	// 1. Buffer cache check.
	if s.bufCache != nil {
		if data, err := s.bufCache.Get(linkID, index); data != nil || err != nil {
			return data, err
		}
	}

	// 2. Disk cache check (ObjectCache).
	key := blockCacheKey(linkID, index)
	if data, _ := s.cache.Read(key); data != nil {
		// Populate buffer cache on disk hit.
		if s.bufCache != nil {
			s.bufCache.Put(linkID, index, data)
		}
		return data, nil
	}

	// 3. HTTP fetch.
	rc, err := s.session.Client.GetBlock(ctx, bareURL, token)
	if err != nil {
		return nil, fmt.Errorf("blockstore.GetBlock %s block %d: %w", linkID, index, err)
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("blockstore.GetBlock %s block %d: read: %w", linkID, index, err)
	}

	// Populate both caches (best-effort).
	if s.bufCache != nil {
		s.bufCache.Put(linkID, index, data)
	}
	_ = s.cache.Write(key, data)

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

	// Cache populate (best-effort).
	if s.bufCache != nil {
		s.bufCache.Put(linkID, index, data)
	}
	_ = s.cache.Write(blockCacheKey(linkID, index), data)

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
		for key := range s.cache.Keys(nil) {
			if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
				_ = s.cache.Erase(key)
			}
		}
	}
}
