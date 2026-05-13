package shareCmd

import (
	"context"
	"fmt"
	"os"

	"github.com/major0/proton-utils/api/drive"
	driveCmd "github.com/major0/proton-utils/internal/cli/drive"
	"github.com/spf13/cobra"
)

var shareAddCmd = &cobra.Command{
	Use:   "add [share-name] <proton-path>",
	Short: "Create a share from an existing file or folder",
	Long:  "Create a share from an existing Proton Drive file or folder. If share-name is provided, the share is renamed after creation.",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runShareAdd,
}

func init() {
	shareCmd.AddCommand(shareAddCmd)
}

func runShareAdd(cmd *cobra.Command, args []string) error {
	var shareName, protonPath string
	if len(args) == 2 {
		shareName = args[0]
		protonPath = args[1]
	} else {
		protonPath = args[0]
	}

	// Validate share name early if provided.
	if shareName != "" {
		if err := drive.ValidateShareName(shareName); err != nil {
			return fmt.Errorf("share add: %w", err)
		}
	}

	ctx := context.Background()

	session, err := setupSessionFn(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := newDriveClientFn(ctx, session)
	if err != nil {
		return err
	}

	link, _, err := driveCmd.ResolveProtonPath(ctx, dc, protonPath)
	if err != nil {
		return fmt.Errorf("share add: %s: not found", protonPath)
	}

	share, err := dc.ShareLink(ctx, link, shareName)
	if err != nil {
		return fmt.Errorf("share add: %s: %w", protonPath, err)
	}

	// Determine the effective display name.
	effectiveName := shareName
	if effectiveName == "" {
		linkName, nameErr := link.Name()
		if nameErr != nil {
			effectiveName = link.LinkID()
		} else {
			effectiveName = linkName
		}
	}

	fmt.Printf("Created share %q (%s)\n", effectiveName, share.Metadata().ShareID)
	fmt.Fprintf(os.Stderr, "warning: share will be garbage-collected unless shared with another user or a public URL is enabled\n")
	return nil
}
