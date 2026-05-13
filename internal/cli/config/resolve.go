package configCmd

import (
	"context"
	"fmt"

	"github.com/major0/proton-utils/api/config"
	"github.com/major0/proton-utils/api/drive"
	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
)

// setupSessionFn is the function used to create a session. Overridable for testing.
var setupSessionFn = cli.SetupSession

// newDriveClientFn is the function used to create a drive client. Overridable for testing.
var newDriveClientFn = cli.NewDriveClient

// resolveShareFn resolves a share by name. Overridable for testing.
var resolveShareFn = func(ctx context.Context, dc *drive.Client, name string) (*drive.Share, error) {
	return dc.ResolveShare(ctx, name, true)
}

// listSharesFn lists all shares. Overridable for testing.
var listSharesFn = func(ctx context.Context, dc *drive.Client) ([]*drive.Share, error) {
	shares, err := dc.ListShares(ctx, true)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*drive.Share, len(shares))
	for i := range shares {
		ptrs[i] = &shares[i]
	}
	return ptrs, nil
}

// resolveShareName resolves a share name to its Proton share ID.
// Returns the share ID or an error if auth is unavailable or share not found.
func resolveShareName(cmd *cobra.Command, name string) (string, error) {
	cli.SetServiceCmd(cmd, "drive")
	ctx := context.Background()

	session, err := setupSessionFn(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("config: share name resolution requires an authenticated session (use share[id=X] to skip auth): %w", err)
	}

	dc, err := newDriveClientFn(ctx, session)
	if err != nil {
		return "", fmt.Errorf("config: drive client: %w", err)
	}

	share, err := resolveShareFn(ctx, dc, name)
	if err != nil {
		return "", fmt.Errorf("config: share %q not found: %w", name, err)
	}

	return share.Metadata().ShareID, nil
}

// resolveShareSelector rewrites a selector that uses share[name=X] to share[id=Y].
// Returns the rewritten selector and any error from resolution.
// If the selector uses share[id=X], returns it unchanged.
// If the first segment is not "share" or has no index, returns unchanged.
func resolveShareSelector(cmd *cobra.Command, sel config.Selector) (config.Selector, error) {
	if len(sel.Segments) == 0 || sel.Segments[0].Name != "share" {
		return sel, nil
	}
	if sel.Segments[0].IndexKey == "" {
		return sel, nil
	}
	if sel.Segments[0].IndexKey == "id" {
		return sel, nil // already by ID
	}
	if sel.Segments[0].IndexKey == "name" {
		id, err := resolveShareName(cmd, sel.Segments[0].IndexVal)
		if err != nil {
			return sel, err
		}
		sel.Segments[0].IndexKey = "id"
		sel.Segments[0].IndexVal = id
		return sel, nil
	}
	return sel, fmt.Errorf("config: shares index key must be 'name' or 'id', got %q", sel.Segments[0].IndexKey)
}

// cleanupStaleShares removes config entries for share IDs that are no longer
// accessible. Called when an authenticated session is available during a
// share-related config operation. Best-effort: if we can't get a session,
// skip cleanup silently.
func cleanupStaleShares(cmd *cobra.Command, cfg *config.Config) {
	if len(cfg.Shares) == 0 {
		return
	}

	cli.SetServiceCmd(cmd, "drive")
	ctx := context.Background()
	session, err := setupSessionFn(ctx, cmd)
	if err != nil {
		return // no session available — skip cleanup
	}

	dc, err := newDriveClientFn(ctx, session)
	if err != nil {
		return
	}

	shares, err := listSharesFn(ctx, dc)
	if err != nil {
		return
	}

	// Build set of valid share IDs.
	validIDs := make(map[string]bool, len(shares))
	for _, s := range shares {
		validIDs[s.Metadata().ShareID] = true
	}

	// Remove entries for IDs that are no longer valid.
	for id := range cfg.Shares {
		if !validIDs[id] {
			delete(cfg.Shares, id)
		}
	}
}
