package shareCmd

import (
	"context"
	"fmt"
	"os"

	"github.com/major0/proton-cli/api/config"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var delFlags struct {
	force bool
}

var shareDelCmd = &cobra.Command{
	Use:   "del <share-name>",
	Short: "Delete a share",
	Long:  "Delete a share by name. The underlying file or folder is not deleted.",
	Args:  cobra.ExactArgs(1),
	RunE:  runShareDel,
}

func init() {
	shareCmd.AddCommand(shareDelCmd)
	shareDelCmd.Flags().BoolVarP(&delFlags.force, "force", "f", false, "Force delete even if members exist")
}

func runShareDel(cmd *cobra.Command, args []string) error {
	name := args[0]

	rc := cli.GetContext(cmd)
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
		return fmt.Errorf("share del: %s: share not found", name)
	}

	shareID := resolved.Metadata().ShareID

	if err := deleteShareFn(ctx, dc, shareID, delFlags.force); err != nil {
		if !delFlags.force {
			return fmt.Errorf("share del: %s: %w (use --force to override)", name, err)
		}
		return fmt.Errorf("share del: %s: %w", name, err)
	}

	// Remove cache config entry if present.
	cfg := rc.Config
	if cfg != nil {
		if _, ok := cfg.Shares[shareID]; ok {
			delete(cfg.Shares, shareID)
			if err := config.SaveConfig(cli.ConfigFilePath(), cfg); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to update config: %v\n", err)
			}
		}
	}

	fmt.Printf("Deleted share %s\n", name)
	return nil
}
