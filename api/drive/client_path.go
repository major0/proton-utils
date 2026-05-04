package drive

import (
	"context"
	"fmt"
	"strings"

	"github.com/ProtonMail/go-proton-api"
)

// ResolveShareComponent resolves the share part of a proton:// URI.
// Resolution priority:
//  1. Empty string → root share (main volume share)
//  2. {id} brackets → resolve by share ID directly
//  3. "Photos" → photos share (ShareTypePhotos)
//  4. Otherwise → resolve by decrypted share root link name
func (c *Client) ResolveShareComponent(ctx context.Context, sharePart string) (*Share, error) {
	// Empty share → root share (triple-slash case).
	if sharePart == "" {
		return c.ResolveShareByType(ctx, proton.ShareTypeMain)
	}

	// Direct share ID: {ABC123DEF-456}
	if strings.HasPrefix(sharePart, "{") && strings.HasSuffix(sharePart, "}") {
		id := sharePart[1 : len(sharePart)-1]
		return c.GetShare(ctx, id)
	}

	// Well-known alias (case-sensitive).
	if sharePart == "Photos" {
		return c.ResolveShareByType(ctx, ShareTypePhotos)
	}

	// Resolve by decrypted share root link name.
	return c.ResolveShare(ctx, sharePart, true)
}

// ResolveDrivePath resolves a normalized drive path to its Link and Share.
// The path format is "sharename/relative/path". The proton:// prefix must
// be stripped by the caller before calling this method.
func (c *Client) ResolveDrivePath(ctx context.Context, rawPath string) (*Link, *Share, error) {
	path, err := NormalizePath(rawPath)
	if err != nil {
		return nil, nil, err
	}

	parts := strings.SplitN(path, "/", 2)

	share, err := c.ResolveShareComponent(ctx, parts[0])
	if err != nil {
		return nil, nil, err
	}

	if len(parts) == 1 || parts[1] == "" {
		return share.Link, share, nil
	}

	link, err := share.Link.ResolvePath(ctx, parts[1], true)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve %s: %w", rawPath, err)
	}

	return link, share, nil
}
