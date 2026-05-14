package drive

import (
	"context"
	"log/slog"
	"sync"

	"github.com/major0/proton-utils/api"
)

// DirEntry is a single entry yielded by Readdir.
type DirEntry struct {
	Link *Link
	Err  error
	name string // pre-set for . and ..; cached for children when dirent cache enabled
}

// EntryName returns the display name for this entry. For . and ..,
// returns the pre-set literal. For children, calls Link.Name() to
// decrypt on demand. When the share's dirent cache is enabled, the
// resolved name is stored for subsequent calls.
func (e *DirEntry) EntryName() (string, error) {
	if e.name != "" {
		return e.name, nil
	}
	name, err := e.Link.Name()
	if err != nil {
		return "", err
	}
	if e.Link.share != nil && e.Link.share.MemoryCacheLevel >= api.CacheLinkName {
		e.name = name
	}
	return name, nil
}

// Readdir returns a channel that yields directory entries for this folder.
// The first two entries are always . (self) and .. (parent), followed by
// children fetched via the resolver. Children are yielded without name
// decryption — the types layer constructs child Links only.
//
// The channel is closed when all entries have been yielded or the context
// is cancelled.
func (l *Link) Readdir(ctx context.Context) <-chan DirEntry {
	ch := make(chan DirEntry)

	go func() {
		defer close(ch)

		slog.Debug("link.Readdir", "linkID", l.protonLink.LinkID)

		// Emit . (self) and .. (parent) as the first two entries.
		// For share roots, both point to the same link (POSIX /.. → /).
		// Names are pre-set — no decryption needed.
		select {
		case ch <- DirEntry{Link: l, name: "."}:
		case <-ctx.Done():
			return
		}
		select {
		case ch <- DirEntry{Link: l.Parent(), name: ".."}:
		case <-ctx.Done():
			return
		}

		// Cache hit: if cachedChildIDs is populated, yield children from
		// the link table without an API call.
		l.cacheMu.RLock()
		childIDs := l.cachedChildIDs
		l.cacheMu.RUnlock()

		if childIDs != nil {
			for _, id := range childIDs {
				child := l.resolver.GetLink(id)
				if child == nil {
					// Link evicted — invalidate cache and fall through to API.
					l.cacheMu.Lock()
					l.cachedChildIDs = nil
					l.cacheMu.Unlock()
					childIDs = nil
					break
				}
				select {
				case ch <- DirEntry{Link: child}:
				case <-ctx.Done():
					return
				}
			}
			if childIDs != nil {
				return // all children yielded from cache
			}
		}

		// Respect throttle before making the API call.
		if throttle := l.resolver.Throttle(); throttle != nil {
			if err := throttle.Wait(ctx); err != nil {
				select {
				case ch <- DirEntry{Err: err}:
				case <-ctx.Done():
				}
				return
			}
		}

		pChildren, err := l.resolver.ListLinkChildren(
			ctx, l.share.protonShare.ShareID, l.protonLink.LinkID, true,
		)
		if err != nil {
			// Signal throttle on 429-like errors.
			if throttle := l.resolver.Throttle(); throttle != nil {
				throttle.Signal(0)
			}
			select {
			case ch <- DirEntry{Err: err}:
			case <-ctx.Done():
			}
			return
		}

		if throttle := l.resolver.Throttle(); throttle != nil {
			throttle.Reset()
		}

		if len(pChildren) == 0 {
			return
		}

		// Cache child LinkIDs on the parent for subsequent Lookup calls.
		// This avoids redundant ListLinkChildren API calls when the kernel
		// issues Lookup for each entry after Readdir (the N+1 problem).
		if l.share != nil && l.share.MemoryCacheLevel >= api.CacheMetadata {
			ids := make([]string, len(pChildren))
			for i := range pChildren {
				ids[i] = pChildren[i].LinkID
			}
			l.cacheMu.Lock()
			l.cachedChildIDs = ids
			l.cacheMu.Unlock()
		}

		// Fan out child link construction across workers.
		workers := min(l.resolver.MaxWorkers(), len(pChildren))
		indexCh := make(chan int)
		var wg sync.WaitGroup

		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range indexCh {
					child := l.resolver.NewChildLink(ctx, l, &pChildren[idx])

					select {
					case ch <- DirEntry{Link: child}:
					case <-ctx.Done():
						return
					}
				}
			}()
		}

		// Feed indices, respecting cancellation.
		go func() {
			defer close(indexCh)
			for i := range pChildren {
				select {
				case indexCh <- i:
				case <-ctx.Done():
					return
				}
			}
		}()

		wg.Wait()
	}()

	return ch
}

// Lookup finds a child by name in this folder. Returns nil if not found.
// Handles "." (self) and ".." (parent) directly without scanning children.
//
// When cachedChildIDs is populated (from a prior Readdir), resolves
// children from the link table without an API call. Falls back to a
// fresh Readdir if the cache is empty.
func (l *Link) Lookup(ctx context.Context, name string) (*Link, error) {
	// Fast path for . and ..
	switch name {
	case ".":
		return l, nil
	case "..":
		return l.Parent(), nil
	}

	// Try cached child IDs first — avoids redundant ListLinkChildren calls.
	l.cacheMu.RLock()
	childIDs := l.cachedChildIDs
	l.cacheMu.RUnlock()

	if childIDs != nil {
		for _, id := range childIDs {
			child := l.resolver.GetLink(id)
			if child == nil {
				continue // link evicted from table — fall through below
			}
			childName, err := child.Name()
			if err != nil {
				continue
			}
			if childName == name {
				return child, nil
			}
		}
		// Name not found in cached children.
		return nil, nil
	}

	// No cache — fall back to streaming Readdir with early termination.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for entry := range l.Readdir(ctx) {
		if entry.Err != nil {
			return nil, entry.Err
		}
		entryName, err := entry.EntryName()
		if err != nil {
			continue // skip entries with decryption errors
		}
		// Skip . and ..
		if entryName == "." || entryName == ".." {
			continue
		}
		if entryName == name {
			return entry.Link, nil
		}
	}
	return nil, nil
}

// ListChildren returns all child links of this folder as a slice.
// Excludes the synthetic . and .. entries (first two from Readdir).
// Built on Readdir — prefer Readdir for streaming or early termination.
func (l *Link) ListChildren(ctx context.Context, _ bool) ([]*Link, error) {
	links := make([]*Link, 0, 16)
	for entry := range l.Readdir(ctx) {
		if entry.Err != nil {
			return nil, entry.Err
		}
		entryName, err := entry.EntryName()
		if err != nil {
			return nil, err
		}
		// Skip . and ..
		if entryName == "." || entryName == ".." {
			continue
		}
		links = append(links, entry.Link)
	}
	return links, nil
}
