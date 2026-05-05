package shareCmd

import (
	"context"
	"fmt"

	"github.com/major0/proton-cli/api/drive"
	"github.com/spf13/cobra"
)

var shareURLEnableCmd = &cobra.Command{
	Use:   "enable <share-name>",
	Short: "Enable a public URL on a share",
	Long:  "Create a public URL with a generated password for the specified share.",
	Args:  cobra.ExactArgs(1),
	RunE:  runShareURLEnable,
}

func init() {
	shareURLCmd.AddCommand(shareURLEnableCmd)
}

// createShareURLFn is a test seam for CreateShareURL.
var createShareURLFn = func(ctx context.Context, dc *drive.Client, share *drive.Share) (string, *drive.ShareURL, error) {
	return dc.CreateShareURL(ctx, share)
}

func runShareURLEnable(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf("share url enable: %s: share not found", name)
	}

	password, _, err := createShareURLFn(ctx, dc, resolved)
	if err != nil {
		return fmt.Errorf("share url enable: %s: %w", name, err)
	}

	// Output password as single line to stdout (machine-readable).
	fmt.Println(password)
	return nil
}
