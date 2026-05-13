package driveCmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
)

var rmdirFlags struct {
	verbose   bool
	permanent bool
}

var driveRmdirCmd = &cobra.Command{
	Use:   "rmdir [options] <path> [<path> ...]",
	Short: "Remove empty directories from Proton Drive",
	Long:  "Remove empty directories from Proton Drive (moves to trash by default)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRmdir,
}

func init() {
	driveCmd.AddCommand(driveRmdirCmd)
	cli.BoolFlagP(driveRmdirCmd.Flags(), &rmdirFlags.verbose, "verbose", "v", false, "Print each directory as it is removed")
	cli.BoolFlag(driveRmdirCmd.Flags(), &rmdirFlags.permanent, "permanent", false, "Permanently delete instead of moving to trash")
}

func runRmdir(cmd *cobra.Command, args []string) error {
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
		if err := rmdirOne(ctx, dc, arg); err != nil {
			return err
		}
	}

	return nil
}

func rmdirOne(ctx context.Context, dc *drive.Client, rawPath string) error {
	sharePart, pathPart, err := parseProtonURI(rawPath)
	if err != nil {
		return fmt.Errorf("rmdir: %w", err)
	}
	if pathPart == "" {
		return fmt.Errorf("rmdir: missing directory name")
	}

	share, err := dc.ResolveShareComponent(ctx, sharePart)
	if err != nil {
		return fmt.Errorf("rmdir: %s: %w", sharePart, err)
	}

	pathPart = strings.TrimSuffix(pathPart, "/")
	link, err := share.Link.ResolvePath(ctx, pathPart, true)
	if err != nil {
		return fmt.Errorf("rmdir: %s: %w", pathPart, err)
	}

	if link.Type() != proton.LinkTypeFolder {
		return fmt.Errorf("rmdir: %s: not a directory", pathPart)
	}

	err = dc.Remove(ctx, share, link, drive.RemoveOpts{
		Recursive: false,
		Permanent: rmdirFlags.permanent,
	})

	if err != nil {
		return err
	}

	if rmdirFlags.verbose {
		action := "trashed"
		if rmdirFlags.permanent {
			action = "deleted"
		}
		name, _ := link.Name()
		fmt.Printf("rmdir: %s '%s'\n", action, name)
	}

	return nil
}
