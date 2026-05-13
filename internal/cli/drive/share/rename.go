package shareCmd

import (
	"context"
	"fmt"

	"github.com/major0/proton-utils/api/drive"
	"github.com/spf13/cobra"
)

var shareRenameCmd = &cobra.Command{
	Use:     "rename <share-name> <new-name>",
	Aliases: []string{"rn"},
	Short:   "Rename a share",
	Long:    "Rename a share's display name (renames the root link).",
	Args:    cobra.ExactArgs(2),
	RunE:    runShareRename,
}

func init() {
	shareCmd.AddCommand(shareRenameCmd)
}

// shareRenameFn is a test seam for ShareRename.
var shareRenameFn = func(ctx context.Context, dc *drive.Client, share *drive.Share, newName string) error {
	return dc.ShareRename(ctx, share, newName)
}

func runShareRename(cmd *cobra.Command, args []string) error {
	name := args[0]
	newName := args[1]

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
		return fmt.Errorf("share rename: %s: share not found", name)
	}

	if err := shareRenameFn(ctx, dc, resolved, newName); err != nil {
		return fmt.Errorf("share rename: %s: %w", name, err)
	}

	fmt.Printf("Renamed share %q → %q\n", name, newName)
	return nil
}
