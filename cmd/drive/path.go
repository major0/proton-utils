package driveCmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/major0/proton-cli/api/drive"
	driveClient "github.com/major0/proton-cli/api/drive/client"
)

// parseProtonURI parses a proton:// URI into its share and path components.
//
// URI format: proton://<share>/<path>
//
//   - proton:///path/to/file            → share="" (empty → root share), path="path/to/file"
//   - proton://Photos/2024/vacation.jpg → share="Photos", path="2024/vacation.jpg"
//   - proton://root/Documents/file.txt  → share="root" (resolved by name), path="Documents/file.txt"
//   - proton://{id}/path                → share="{id}" (resolved by share ID), path="path"
//   - proton://                         → error: no share specified
//
// Path normalization may collapse the path to empty (e.g. "proton:///test1/.."
// normalizes to the share root). This is silent — no error is returned.
//
// The proton:// prefix is a cmd/ concern — the api/ layer never sees it.
func parseProtonURI(rawPath string) (sharePart, pathPart string, err error) {
	if !strings.HasPrefix(rawPath, "proton://") {
		return "", "", fmt.Errorf("invalid path: %s (must start with proton://)", rawPath)
	}

	// Strip the "proton://" prefix.
	remainder := strings.TrimPrefix(rawPath, "proton://")

	// Bare "proton://" with nothing after it → error.
	if remainder == "" {
		return "", "", fmt.Errorf("no share specified (use proton://<share>/<path> or proton:///<path> for root share)")
	}

	// Triple-slash: proton:///path → empty share (root), path starts after.
	// proton:/// alone → root share, root directory (no sub-path).
	if strings.HasPrefix(remainder, "/") {
		pathPart = strings.TrimPrefix(remainder, "/")
		if pathPart == "" {
			return "", "", nil
		}
		normalized, err := drive.NormalizePath(pathPart)
		if err != nil {
			// Path normalized to empty (e.g. "test1/..") → share root.
			return "", "", nil
		}
		return "", normalized, nil
	}

	// Split on first "/" to separate share from path.
	parts := strings.SplitN(remainder, "/", 2)
	sharePart = parts[0]

	if len(parts) == 1 || parts[1] == "" {
		// proton://Drive or proton://Drive/ → share root, no sub-path.
		return sharePart, "", nil
	}

	normalized, err := drive.NormalizePath(parts[1])
	if err != nil {
		// Path normalized to empty — treat as share root.
		return sharePart, "", nil
	}

	return sharePart, normalized, nil
}

// parsePath strips the proton:// prefix and returns the normalized
// share/path string. Retained for command handlers that need the
// combined string for display or splitting.
func parsePath(rawPath string) string {
	sharePart, pathPart, err := parseProtonURI(rawPath)
	if err != nil {
		return ""
	}
	if sharePart == "" && pathPart == "" {
		return ""
	}
	if pathPart == "" {
		return sharePart
	}
	if sharePart == "" {
		return pathPart
	}
	return sharePart + "/" + pathPart
}

// ResolveProtonPath parses a proton:// URI and resolves it to a Link and Share.
func ResolveProtonPath(ctx context.Context, dc *driveClient.Client, rawPath string) (*drive.Link, *drive.Share, error) {
	sharePart, pathPart, err := parseProtonURI(rawPath)
	if err != nil {
		return nil, nil, err
	}

	share, err := dc.ResolveShareComponent(ctx, sharePart)
	if err != nil {
		return nil, nil, err
	}

	if pathPart == "" {
		return share.Link, share, nil
	}

	link, err := share.Link.ResolvePath(ctx, pathPart, true)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve %s: %w", rawPath, err)
	}

	return link, share, nil
}
