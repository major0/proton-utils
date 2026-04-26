package driveCmd

import (
	"context"
	"fmt"
	"os"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
	driveClient "github.com/major0/proton-cli/api/drive/client"
)

// handleConflict checks whether the destination already exists and
// handles it according to the flags:
//
//   - No flags: refuse to overwrite (return error)
//   - -f / --force: overwrite (truncate local, trash remote)
//   - --remove-destination: remove before copy (local rm, remote trash)
//   - --backup: rename local to <name>~ before copy
//
// Directories always merge — no conflict.
func handleConflict(ctx context.Context, dc *driveClient.Client, dst *resolvedEndpoint, opts cpOptions) error {
	if dst.isDir() {
		return nil
	}

	switch dst.pathType {
	case PathLocal:
		if dst.localInfo == nil {
			return nil // doesn't exist, no conflict
		}
		if opts.backup {
			return os.Rename(dst.localPath, dst.localPath+"~")
		}
		if opts.removeDest {
			return os.Remove(dst.localPath)
		}
		if opts.force {
			return os.Truncate(dst.localPath, 0)
		}
		return fmt.Errorf("cp: %s: file exists (use -f to overwrite)", dst.localPath)

	case PathProton:
		if dst.link == nil {
			return nil // doesn't exist, no conflict
		}
		if dst.link.Type() == proton.LinkTypeFolder {
			return nil // directory, merge
		}
		if opts.removeDest {
			return dc.Remove(ctx, dst.share, dst.link, drive.RemoveOpts{})
		}
		if opts.force {
			// Don't trash — buildCopyJob handles -f via OverwriteFile
			// (CreateRevision on existing link preserves link identity).
			return nil
		}
		return fmt.Errorf("cp: %s: file exists (use -f to overwrite)", dst.raw)
	}
	return nil
}
