package driveCmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/major0/proton-cli/api/drive"
	cli "github.com/major0/proton-cli/internal/cli"
	"github.com/spf13/cobra"
)

var rmFlags struct {
	recursive bool
	verbose   bool
	permanent bool
}

var driveRmCmd = &cobra.Command{
	Use:   "rm [options] <path> [<path> ...]",
	Short: "Remove files and directories from Proton Drive",
	Long:  "Remove files and directories from Proton Drive (moves to trash by default)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRm,
}

var driveTrashEmptyCmd = &cobra.Command{
	Use:   "empty-trash",
	Short: "Permanently delete all items in the trash",
	Long:  "Permanently delete all items in the Proton Drive trash",
	RunE:  runEmptyTrash,
}

func init() {
	driveCmd.AddCommand(driveRmCmd)
	cli.BoolFlagP(driveRmCmd.Flags(), &rmFlags.recursive, "recursive", "r", false, "Remove directories and their contents recursively")
	cli.BoolFlagP(driveRmCmd.Flags(), &rmFlags.verbose, "verbose", "v", false, "Print each removal")
	cli.BoolFlag(driveRmCmd.Flags(), &rmFlags.permanent, "permanent", false, "Permanently delete instead of moving to trash")

	driveCmd.AddCommand(driveTrashEmptyCmd)
}

func runRm(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	session, err := cli.SetupSession(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := cli.NewDriveClient(ctx, session)
	if err != nil {
		return err
	}
	for _, arg := range args {
		if err := rmOne(ctx, dc, arg); err != nil {
			return err
		}
	}

	return nil
}

func rmOne(ctx context.Context, dc *drive.Client, rawPath string) error {
	if !strings.HasPrefix(rawPath, "proton://") {
		return fmt.Errorf("invalid path: %s (must start with proton://)", rawPath)
	}

	link, share, err := ResolveProtonPath(ctx, dc, rawPath)
	if err != nil {
		return fmt.Errorf("rm: %s: %w", rawPath, err)
	}

	err = dc.Remove(ctx, share, link, drive.RemoveOpts{
		Recursive: rmFlags.recursive,
		Permanent: rmFlags.permanent,
	})

	if err != nil {
		return err
	}

	if rmFlags.verbose {
		name, _ := link.Name()
		action := "trashed"
		if rmFlags.permanent {
			action = "deleted"
		}
		fmt.Printf("rm: %s '%s'\n", action, name)
	}

	return nil
}

func runEmptyTrash(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()

	session, err := cli.SetupSession(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := cli.NewDriveClient(ctx, session)
	if err != nil {
		return err
	}
	shares, err := dc.ListShares(ctx, true)
	if err != nil {
		return err
	}

	for i := range shares {
		if err := dc.EmptyTrash(ctx, &shares[i]); err != nil {
			return fmt.Errorf("emptying trash: %w", err)
		}
	}

	fmt.Println("Trash emptied.")
	return nil
}
