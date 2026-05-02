package client

import (
	"context"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
)

// WalkEntry is a single entry yielded by TreeWalk. Lives in the client
// layer because it carries decrypted content (EntryName, Path) that the
// types layer (api/drive/) must not hold.
type WalkEntry struct {
	Path      string      // constructed traversal path from decrypted names
	Link      *drive.Link // raw encrypted link
	Depth     int         // depth from walk root (root = 0)
	EntryName string      // decrypted entry name via DirEntry.EntryName()
}

// TreeWalk walks the directory tree rooted at root and sends each entry
// to the results channel. The caller owns the channel and controls
// buffering, backpressure, and lifetime. Cancel ctx to stop the walk.
// maxDepth limits descent depth (-1 for unlimited, 0 = root only).
func (c *Client) TreeWalk(ctx context.Context, root *drive.Link, rootPath string, order drive.WalkOrder, maxDepth int, results chan<- WalkEntry) error {
	switch order {
	case drive.DepthFirst:
		return c.walkDepthFirst(ctx, root, rootPath, 0, maxDepth, "", results)
	default:
		return c.walkBreadthFirst(ctx, root, rootPath, maxDepth, results)
	}
}

type queueItem struct {
	link  *drive.Link
	path  string
	depth int
}

func (c *Client) walkBreadthFirst(ctx context.Context, root *drive.Link, rootPath string, maxDepth int, results chan<- WalkEntry) error {
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
					continue
				}
				name, err := entry.EntryName()
				if err != nil {
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

func (c *Client) walkDepthFirst(ctx context.Context, link *drive.Link, linkPath string, depth, maxDepth int, entryName string, results chan<- WalkEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if link.Type() == proton.LinkTypeFolder && (maxDepth < 0 || depth < maxDepth) {
		for entry := range link.Readdir(ctx) {
			if entry.Err != nil {
				continue
			}
			name, err := entry.EntryName()
			if err != nil {
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
