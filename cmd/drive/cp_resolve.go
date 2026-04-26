package driveCmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ProtonMail/go-proton-api"
	driveClient "github.com/major0/proton-cli/api/drive/client"
)

// resolveDest resolves the destination path with coreutils cp semantics.
// For existing paths, returns the resolved endpoint directly. For
// non-existent paths, verifies the parent exists and returns an endpoint
// with the parent info (localPath set but localInfo nil for local;
// link pointing to parent for Proton).
func resolveDest(ctx context.Context, dc *driveClient.Client, arg pathArg, multiSource bool) (*resolvedEndpoint, error) {
	ep := &resolvedEndpoint{pathType: arg.pathType, raw: arg.raw}

	switch arg.pathType {
	case PathLocal:
		info, err := os.Stat(arg.raw)
		if err == nil {
			// Dest exists.
			ep.localPath = arg.raw
			ep.localInfo = info
			if multiSource && !info.IsDir() {
				return nil, fmt.Errorf("cp: %s: not a directory", arg.raw)
			}
			return ep, nil
		}
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("cp: %s: %w", arg.raw, err)
		}
		// Dest doesn't exist — parent must exist.
		if multiSource {
			return nil, fmt.Errorf("cp: %s: no such file or directory", arg.raw)
		}
		parent := filepath.Dir(arg.raw)
		pInfo, pErr := os.Stat(parent)
		if pErr != nil {
			return nil, fmt.Errorf("cp: %s: %w", arg.raw, pErr)
		}
		if !pInfo.IsDir() {
			return nil, fmt.Errorf("cp: %s: not a directory", parent)
		}
		// localPath set but localInfo nil signals "create new file at this path".
		ep.localPath = arg.raw
		return ep, nil

	case PathProton:
		link, share, err := ResolveProtonPath(ctx, dc, arg.raw)
		if err == nil {
			// Dest exists.
			ep.link = link
			ep.share = share
			if multiSource && link.Type() != proton.LinkTypeFolder {
				return nil, fmt.Errorf("cp: %s: not a directory", arg.raw)
			}
			return ep, nil
		}
		// Dest doesn't exist — resolve the parent within the same share.
		if multiSource {
			return nil, fmt.Errorf("cp: %s: no such file or directory", arg.raw)
		}
		// Parse the URI to get the share and path components separately.
		sharePart, pathPart, parseErr := parseProtonURI(arg.raw)
		if parseErr != nil {
			return nil, fmt.Errorf("cp: %s: %w", arg.raw, parseErr)
		}
		if pathPart == "" {
			// proton://share with no sub-path — the share itself doesn't exist.
			return nil, fmt.Errorf("cp: %s: %w", arg.raw, err)
		}
		// Resolve the share first — if the share doesn't exist, fail here.
		share, shareErr := dc.ResolveShareComponent(ctx, sharePart)
		if shareErr != nil {
			return nil, fmt.Errorf("cp: %s: %w", arg.raw, shareErr)
		}
		// Resolve the parent path within the share.
		parentPath := filepath.Dir(pathPart)
		if parentPath == "." || parentPath == "" {
			// File is directly under the share root.
			ep.link = share.Link
			ep.share = share
			return ep, nil
		}
		parentLink, pErr := share.Link.ResolvePath(ctx, parentPath, true)
		if pErr != nil {
			return nil, fmt.Errorf("cp: %s: %w", arg.raw, pErr)
		}
		if parentLink.Type() != proton.LinkTypeFolder {
			return nil, fmt.Errorf("cp: %s: not a directory", parentPath)
		}
		ep.link = parentLink
		ep.share = share
		return ep, nil
	}

	return ep, nil
}

// errSkipSymlink signals that a symlink source should be skipped.
var errSkipSymlink = errors.New("skipping symbolic link")

// resolveSource resolves a source path argument to a resolvedEndpoint.
// For local paths, uses os.Lstat to detect symlinks. With -L, follows
// symlinks via os.Stat. Without -L, returns errSkipSymlink.
func resolveSource(ctx context.Context, dc *driveClient.Client, arg pathArg, opts cpOptions) (*resolvedEndpoint, error) {
	ep := &resolvedEndpoint{pathType: arg.pathType, raw: arg.raw}
	switch arg.pathType {
	case PathProton:
		link, share, err := ResolveProtonPath(ctx, dc, arg.raw)
		if err != nil {
			return nil, fmt.Errorf("cp: %s: %w", arg.raw, err)
		}
		ep.link = link
		ep.share = share
	case PathLocal:
		info, err := os.Lstat(arg.raw)
		if err != nil {
			return nil, fmt.Errorf("cp: %s: %w", arg.raw, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if !opts.dereference {
				return nil, fmt.Errorf("cp: %s: %w", arg.raw, errSkipSymlink)
			}
			// -L: follow the symlink.
			info, err = os.Stat(arg.raw)
			if err != nil {
				return nil, fmt.Errorf("cp: %s: %w", arg.raw, err)
			}
		}
		ep.localPath = arg.raw
		ep.localInfo = info
	}
	return ep, nil
}
