package driveCmd

import (
	"context"
	"fmt"
	"path"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
	driveClient "github.com/major0/proton-cli/api/drive/client"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var mvFlags struct {
	verbose bool
}

var driveMvCmd = &cobra.Command{
	Use:     "mv [options] <source> [<source> ...] <dest>",
	Aliases: []string{"rename"},
	Short:   "Move or rename files and directories in Proton Drive",
	Long:    "Move or rename files and directories in Proton Drive",
	Args:    cobra.MinimumNArgs(2),
	RunE:    runMv,
}

func init() {
	driveCmd.AddCommand(driveMvCmd)
	cli.BoolFlagP(driveMvCmd.Flags(), &mvFlags.verbose, "verbose", "v", false, "Print each move operation")
}

func runMv(_ *cobra.Command, args []string) error {
	ctx := context.Background()

	session, err := cli.RestoreSession(ctx)
	if err != nil {
		return err
	}

	dc, err := cli.NewDriveClient(ctx, session)
	if err != nil {
		return err
	}
	sources := args[:len(args)-1]
	dest := args[len(args)-1]

	// Multiple sources → dest must be a directory.
	if len(sources) > 1 {
		return mvMultiple(ctx, dc, sources, dest)
	}

	return mvSingle(ctx, dc, sources[0], dest)
}

// mvSingle handles: mv src dest
// If dest is an existing directory, move src into it.
// If dest doesn't exist, rename src to dest (parent must exist).
func mvSingle(ctx context.Context, dc *driveClient.Client, srcPath, destPath string) error {
	src, srcShare, err := ResolveProtonPath(ctx, dc, srcPath)
	if err != nil {
		return fmt.Errorf("mv: %s: %w", srcPath, err)
	}

	// Try to resolve dest as an existing path.
	dest, destShare, destErr := ResolveProtonPath(ctx, dc, destPath)

	if destErr == nil && dest.Type() == proton.LinkTypeFolder {
		// Dest exists and is a directory — move src into it.
		srcName, _ := src.Name()
		return doMove(ctx, dc, srcShare, src, destShare, dest, srcName)
	}

	// Dest doesn't exist or is a file — treat as rename.
	// Parent of dest must exist.
	destParsed := parsePath(destPath)
	destDir := path.Dir(destParsed)
	destName := path.Base(destParsed)

	parent, parentShare, err := ResolveProtonPath(ctx, dc, "proton://"+destDir)
	if err != nil {
		return fmt.Errorf("mv: %s: parent not found: %w", destPath, err)
	}

	_ = destShare
	return doMove(ctx, dc, srcShare, src, parentShare, parent, destName)
}

// mvMultiple handles: mv src1 src2 ... dest
// Dest must be an existing directory.
func mvMultiple(ctx context.Context, dc *driveClient.Client, srcPaths []string, destPath string) error {
	dest, destShare, err := ResolveProtonPath(ctx, dc, destPath)
	if err != nil {
		return fmt.Errorf("mv: %s: %w", destPath, err)
	}

	if dest.Type() != proton.LinkTypeFolder {
		return fmt.Errorf("mv: %s: not a directory", destPath)
	}

	for _, srcPath := range srcPaths {
		src, srcShare, err := ResolveProtonPath(ctx, dc, srcPath)
		if err != nil {
			return fmt.Errorf("mv: %s: %w", srcPath, err)
		}

		srcName, _ := src.Name()
		if err := doMove(ctx, dc, srcShare, src, destShare, dest, srcName); err != nil {
			return err
		}
	}

	return nil
}

func doMove(ctx context.Context, dc *driveClient.Client, srcShare *drive.Share, src *drive.Link, _ *drive.Share, destParent *drive.Link, newName string) error {
	// Currently only support moves within the same volume.
	if !drive.SameDevice(src, destParent) {
		return fmt.Errorf("mv: cross-volume moves not supported (source and destination are on different volumes)")
	}

	if err := dc.Move(ctx, srcShare, src, destParent, newName); err != nil {
		return err
	}

	if mvFlags.verbose {
		srcName, _ := src.Name()
		destParentName, _ := destParent.Name()
		fmt.Printf("'%s' -> '%s/%s'\n", srcName, destParentName, newName)
	}

	return nil
}
