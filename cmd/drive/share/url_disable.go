package shareCmd

import (
	"context"
	"fmt"

	"github.com/major0/proton-cli/api/drive"
	"github.com/spf13/cobra"
)

var shareURLDisableCmd = &cobra.Command{
	Use:   "disable <share-name>",
	Short: "Disable the public URL on a share",
	Long:  "Delete the public URL for the specified share, revoking public access.",
	Args:  cobra.ExactArgs(1),
	RunE:  runShareURLDisable,
}

func init() {
	shareURLCmd.AddCommand(shareURLDisableCmd)
}

// deleteShareURLFn is a test seam for DeleteShareURL.
var deleteShareURLFn = func(ctx context.Context, dc *drive.Client, shareID, urlID string) error {
	return dc.DeleteShareURL(ctx, shareID, urlID)
}

// listShareURLsFn is a test seam for ListShareURLs.
var listShareURLsFn = func(ctx context.Context, dc *drive.Client, shareID string) ([]drive.ShareURL, error) {
	return dc.ListShareURLs(ctx, shareID)
}

func runShareURLDisable(cmd *cobra.Command, args []string) error {
	name := args[0]
	ctx := context.Background()

	session, err := setupSessionFn(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := newDriveClientFn(ctx, session)
	if err != nil {
		return err
	}

	resolved, err := resolveShareFn(ctx, dc, name)
	if err != nil {
		return fmt.Errorf("share url disable: %s: share not found", name)
	}

	shareID := resolved.Metadata().ShareID

	urls, err := listShareURLsFn(ctx, dc, shareID)
	if err != nil {
		return fmt.Errorf("share url disable: %s: %w", name, err)
	}
	if len(urls) == 0 {
		return fmt.Errorf("share url disable: %s: no public URL exists", name)
	}

	if err := deleteShareURLFn(ctx, dc, shareID, urls[0].ShareURLID); err != nil {
		return fmt.Errorf("share url disable: %s: %w", name, err)
	}

	fmt.Printf("Disabled public URL for %s\n", name)
	return nil
}
