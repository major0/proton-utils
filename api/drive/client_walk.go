package drive

import (
	"context"

	"github.com/ProtonMail/go-proton-api"
)

// WalkEntry is a single entry yielded by TreeWalk. Lives in the client
// layer because it carries decrypted content (EntryName, Path) that the
// types layer (api/drive/) must not hold.
//
// NOTE: This is an intentional exception to the encrypted data handling
// rule "do not pass decrypted content through channels." TreeWalk is a
// streaming API where entries are consumed and discarded immediately.
// Passing only *Link would force consumers to call Name() (which
// triggers decryption) on every entry, defeating the purpose of the
// streaming pattern. The decrypted Path and EntryName are short-lived —
// they exist only for the duration of the consumer's iteration.
type WalkEntry struct {
	Path      string // constructed traversal path from decrypted names
	Link      *Link  // raw encrypted link
	Depth     int    // depth from walk root (root = 0)
	EntryName string // decrypted entry name via DirEntry.EntryName()
	Err       error  // non-nil when the entry could not be fetched or decrypted
}

// TreeWalk walks the directory tree rooted at root and sends each entry
// to the results channel. The caller owns the channel and controls
// buffering, backpressure, and lifetime. Cancel ctx to stop the walk.
// maxDepth limits descent depth (-1 for unlimited, 0 = root only).
func (c *Client) TreeWalk(ctx context.Context, root *Link, rootPath string, order WalkOrder, maxDepth int, results chan<- WalkEntry) error {
	switch order {
	case DepthFirst:
		return c.walkDepthFirst(ctx, root, rootPath, 0, maxDepth, "", results)
	default:
		return c.walkBreadthFirst(ctx, root, rootPath, maxDepth, results)
	}
}

type queueItem struct {
	link  *Link
	path  string
	depth int
}

func (c *Client) walkBreadthFirst(ctx context.Context, root *Link, rootPath string, maxDepth int, results chan<- WalkEntry) error {
	select {
	case results <- WalkEntry{Path: rootPath, Link: root, Depth: 0}:
	case <-ctx.Done():
		return ctx.Err()
	}

	queue := []queueItem{{link: root, path: rootPath, depth: 0}}

	for len(queue) > 0 {
		var next []queueItem

		for _, item := range queue {
			if item.link.Type() != proton.LinkTypeFolder {
				continue
			}

			for entry := range item.link.Readdir(ctx) {
				if entry.Err != nil {
					select {
					case results <- WalkEntry{Err: entry.Err, Depth: item.depth + 1}:
					case <-ctx.Done():
						return ctx.Err()
					}
					continue
				}
				name, err := entry.EntryName()
				if err != nil {
					select {
					case results <- WalkEntry{Err: err, Link: entry.Link, Depth: item.depth + 1}:
					case <-ctx.Done():
						return ctx.Err()
					}
					continue
				}
				if name == "." || name == ".." {
					continue
				}

				childPath := item.path + name
				if entry.Link.Type() == proton.LinkTypeFolder {
					childPath += "/"
				}
				childDepth := item.depth + 1

				select {
				case results <- WalkEntry{Path: childPath, Link: entry.Link, Depth: childDepth, EntryName: name}:
				case <-ctx.Done():
					return ctx.Err()
				}

				// Only descend if we haven't reached maxdepth.
				if entry.Link.Type() == proton.LinkTypeFolder && (maxDepth < 0 || childDepth < maxDepth) {
					next = append(next, queueItem{link: entry.Link, path: childPath, depth: childDepth})
				}
			}
		}

		queue = next
	}

	return nil
}

func (c *Client) walkDepthFirst(ctx context.Context, link *Link, linkPath string, depth, maxDepth int, entryName string, results chan<- WalkEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if link.Type() == proton.LinkTypeFolder && (maxDepth < 0 || depth < maxDepth) {
		for entry := range link.Readdir(ctx) {
			if entry.Err != nil {
				select {
				case results <- WalkEntry{Err: entry.Err, Depth: depth + 1}:
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}
			name, err := entry.EntryName()
			if err != nil {
				select {
				case results <- WalkEntry{Err: err, Link: entry.Link, Depth: depth + 1}:
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}
			if name == "." || name == ".." {
				continue
			}

			childPath := linkPath + name
			if entry.Link.Type() == proton.LinkTypeFolder {
				childPath += "/"
			}
			if err := c.walkDepthFirst(ctx, entry.Link, childPath, depth+1, maxDepth, name, results); err != nil {
				return err
			}
		}
	}

	select {
	case results <- WalkEntry{Path: linkPath, Link: link, Depth: depth, EntryName: entryName}:
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}
