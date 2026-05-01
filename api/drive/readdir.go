package drive

import (
	"context"
	"log/slog"
	"sync"

	"github.com/major0/proton-cli/api"
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
// Cancels remaining work as soon as the match is found.
//
// Note: Lookup fetches all children from the API via Readdir because the
// Proton Drive API has no server-side name lookup. The context is cancelled
// on first match to limit decryption work, but the initial ListChildren
// API call returns the full child list regardless.
func (l *Link) Lookup(ctx context.Context, name string) (*Link, error) {
	// Fast path for . and ..
	switch name {
	case ".":
		return l, nil
	case "..":
		return l.Parent(), nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

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
