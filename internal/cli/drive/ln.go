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

var lnFlags struct {
	symbolic bool
	verbose  bool
}

var driveLnCmd = &cobra.Command{
	Use:   "ln -s <target> <linkpath>",
	Short: "Create a symbolic link in Proton Drive",
	Long: "Create a symbolic link in Proton Drive.\n\n" +
		"Only symbolic links are supported, so -s/--symbolic is required. The " +
		"target is stored verbatim and is not checked for existence — dangling " +
		"symlinks are valid, matching symlink(2) / ln -s.",
	Args: cobra.ExactArgs(2),
	RunE: runLn,
}

func init() {
	driveCmd.AddCommand(driveLnCmd)
	cli.BoolFlagP(driveLnCmd.Flags(), &lnFlags.symbolic, "symbolic", "s", false, "Create a symbolic link (required)")
	cli.BoolFlagP(driveLnCmd.Flags(), &lnFlags.verbose, "verbose", "v", false, "Print the symlink as it is created")
}

func runLn(cmd *cobra.Command, args []string) error {
	if !lnFlags.symbolic {
		return fmt.Errorf("ln: only symbolic links are supported; pass -s/--symbolic")
	}

	ctx := context.Background()

	session, err := cli.SetupSession(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := cli.NewDriveClient(ctx, session)
	if err != nil {
		return err
	}

	return lnSymlink(ctx, dc, args[0], args[1])
}

// lnSymlink resolves the parent directory of linkPath and creates a symlink
// there whose target is stored verbatim. The target is opaque — it is neither
// resolved nor checked for existence, so dangling targets are valid.
func lnSymlink(ctx context.Context, dc *drive.Client, target, rawLinkPath string) error {
	sharePart, pathPart, err := parseProtonURI(rawLinkPath)
	if err != nil {
		return fmt.Errorf("ln: %w", err)
	}
	if pathPart == "" {
		return fmt.Errorf("ln: missing link name")
	}

	share, err := dc.ResolveShareComponent(ctx, sharePart)
	if err != nil {
		return fmt.Errorf("ln: %s: %w", sharePart, err)
	}

	// Split the link path into its parent directory and the final component,
	// which becomes the new symlink's name.
	pathPart = strings.TrimSuffix(pathPart, "/")
	dir := ""
	name := pathPart
	if idx := strings.LastIndex(pathPart, "/"); idx >= 0 {
		dir = pathPart[:idx]
		name = pathPart[idx+1:]
	}

	parent := share.Link
	if dir != "" {
		parent, err = share.Link.ResolvePath(ctx, dir, true)
		if err != nil {
			return fmt.Errorf("ln: %s: %w", dir, err)
		}
	}

	if parent.Type() != proton.LinkTypeFolder {
		return fmt.Errorf("ln: %s: not a directory", dir)
	}

	if _, err := dc.CreateSymlink(ctx, share, parent, name, target); err != nil {
		return fmt.Errorf("ln: %s: %w", rawLinkPath, err)
	}

	if lnFlags.verbose {
		shareName, _ := share.GetName(ctx)
		fmt.Printf("ln: created symlink '%s/%s' -> '%s'\n", shareName, pathPart, target)
	}

	return nil
}
