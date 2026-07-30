package drive

import (
	"context"
	"fmt"
	"strings"

	"github.com/ProtonMail/go-proton-api"
)

// maxSymlinkDepth caps the number of symlinks dereferenced along a single
// resolution chain, matching Linux's MAXSYMLINKS. Exceeding it means the
// chain is (or behaves like) a loop and yields ErrSymlinkLoop → ELOOP.
const maxSymlinkDepth = 40

// ResolveFollow resolves the slash-separated path relative to root within
// share, dereferencing symlinks POSIX-style: a relative target resolves
// against the symlink's parent directory, an absolute target against the
// share root. "." and ".." are normalized during the walk; ".." at the
// share root clamps to the root (it never escapes).
//
// When noFollow is true the final component is NOT dereferenced — the symlink
// link itself is returned (used by readlink and --no-dereference). Intermediate
// symlink components are always followed so the final component is reachable.
//
// A component that does not exist, or a target that resolves outside the share
// (e.g. an absolute host path), surfaces as ErrFileNotFound (ENOENT) — the CLI
// resolver is best-effort and does not reach the host filesystem. A chain
// deeper than maxSymlinkDepth returns ErrSymlinkLoop (ELOOP).
//
// This is the CLI proton:// resolver's traversal. The FUSE mount does not use
// it — the kernel follows symlinks after Readlink.
func (c *Client) ResolveFollow(ctx context.Context, share *Share, root *Link, path string, noFollow bool) (*Link, error) {
	return c.walkParts(ctx, share, root, pathComponents(path), !noFollow, 0)
}

// walkParts walks the components relative to current. followFinal controls
// whether a symlink in the final position is dereferenced; intermediate
// symlinks are always followed. depth tracks the current symlink-follow chain
// length for loop detection.
func (c *Client) walkParts(ctx context.Context, share *Share, current *Link, parts []string, followFinal bool, depth int) (*Link, error) {
	for i, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			current = current.Parent()
			continue
		}

		if current.Type() != proton.LinkTypeFolder {
			return nil, ErrNotAFolder
		}

		child, err := current.Lookup(ctx, part)
		if err != nil {
			return nil, err
		}
		if child == nil {
			return nil, ErrFileNotFound
		}

		isFinal := i == len(parts)-1
		if child.IsSymlink() && (!isFinal || followFinal) {
			resolved, err := c.followSymlink(ctx, share, current, child, depth)
			if err != nil {
				return nil, err
			}
			current = resolved
			continue
		}
		current = child
	}
	return current, nil
}

// followSymlink reads a symlink's target and resolves it: a relative target
// against parent (the symlink's directory), an absolute target against the
// share root. The target's own final component is always dereferenced. depth
// is the chain length so far; exceeding maxSymlinkDepth yields ErrSymlinkLoop.
func (c *Client) followSymlink(ctx context.Context, share *Share, parent, symlink *Link, depth int) (*Link, error) {
	if depth+1 > maxSymlinkDepth {
		return nil, ErrSymlinkLoop
	}

	readTarget := c.ReadSymlinkTarget
	if c.symlinkTargetReader != nil {
		readTarget = c.symlinkTargetReader
	}
	target, err := readTarget(ctx, symlink)
	if err != nil {
		// A target that cannot be read is an unresolved path for the
		// best-effort resolver.
		return nil, fmt.Errorf("resolve symlink %s: %w", symlink.LinkID(), err)
	}

	base := parent
	if strings.HasPrefix(target, "/") {
		// Absolute target: resolve against the share root. Without a share
		// root the target escapes the resolvable namespace → ENOENT.
		if share == nil || share.Link == nil {
			return nil, ErrFileNotFound
		}
		base = share.Link
	}

	return c.walkParts(ctx, share, base, pathComponents(target), true, depth+1)
}

// pathComponents splits a slash-separated path into components, trimming
// leading and trailing slashes. An empty or root path yields no components.
func pathComponents(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}
