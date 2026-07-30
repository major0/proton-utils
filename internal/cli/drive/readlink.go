package driveCmd

import (
	"context"
	"fmt"

	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
)

var driveReadlinkCmd = &cobra.Command{
	Use:   "readlink <path>",
	Short: "Print the target of a symlink on Proton Drive",
	Long:  "Resolve a symlink without following its final component and print its verbatim target, like readlink(1)",
	Args:  cobra.ExactArgs(1),
	RunE:  runReadlink,
}

func init() {
	driveCmd.AddCommand(driveReadlinkCmd)
}

func runReadlink(cmd *cobra.Command, args []string) error {
	rawPath := args[0]
	ctx := context.Background()

	session, err := cli.SetupSession(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := cli.NewDriveClient(ctx, session)
	if err != nil {
		return err
	}

	sharePart, pathPart, err := parseProtonURI(rawPath)
	if err != nil {
		return fmt.Errorf("readlink: %w", err)
	}
	if pathPart == "" {
		return fmt.Errorf("readlink: missing file path")
	}

	share, err := dc.ResolveShareComponent(ctx, sharePart)
	if err != nil {
		return fmt.Errorf("readlink: %s: %w", sharePart, err)
	}

	// Resolve without following the final component so the symlink itself is
	// returned (intermediate symlink components are still followed).
	link, err := dc.ResolveFollow(ctx, share, share.Link, pathPart, true)
	if err != nil {
		return fmt.Errorf("readlink: %s: %w", pathPart, err)
	}

	// Read the verbatim target; a non-symlink surfaces as ErrNotSymlink.
	target, err := dc.ReadSymlinkTarget(ctx, link)
	if err != nil {
		return fmt.Errorf("readlink: %s: %w", pathPart, err)
	}

	fmt.Println(target)
	return nil
}
