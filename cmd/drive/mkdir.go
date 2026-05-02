package driveCmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
	driveClient "github.com/major0/proton-cli/api/drive/client"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var mkdirFlags struct {
	parents bool
	verbose bool
}

var driveMkdirCmd = &cobra.Command{
	Use:   "mkdir [options] <path> [<path> ...]",
	Short: "Create directories in Proton Drive",
	Long:  "Create directories in Proton Drive",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runMkdir,
}

func init() {
	driveCmd.AddCommand(driveMkdirCmd)
	cli.BoolFlagP(driveMkdirCmd.Flags(), &mkdirFlags.parents, "parents", "p", false, "Create parent directories as needed")
	cli.BoolFlagP(driveMkdirCmd.Flags(), &mkdirFlags.verbose, "verbose", "v", false, "Print each directory as it is created")
}

func runMkdir(_ *cobra.Command, args []string) error {
	ctx := context.Background()

	session, err := cli.RestoreSession(ctx)
	if err != nil {
		return err
	}

	dc, err := cli.NewDriveClient(ctx, session)
	if err != nil {
		return err
	}

	for _, arg := range args {
		if err := mkdirOne(ctx, dc, arg); err != nil {
			return err
		}
	}

	return nil
}

func mkdirOne(ctx context.Context, dc *driveClient.Client, rawPath string) error {
	sharePart, pathPart, err := parseProtonURI(rawPath)
	if err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if pathPart == "" {
		return fmt.Errorf("mkdir: missing directory name")
	}

	share, err := dc.ResolveShareComponent(ctx, sharePart)
	if err != nil {
		return fmt.Errorf("mkdir: %s: %w", sharePart, err)
	}

	if mkdirFlags.parents {
		_, err := dc.MkDirAll(ctx, share, share.Link, pathPart)
		if err != nil {
			return err
		}
		if mkdirFlags.verbose {
			shareName, _ := share.GetName(ctx)
			fmt.Printf("mkdir: created directory '%s/%s'\n", shareName, pathPart)
		}
		return nil
	}

	return mkdirSingle(ctx, dc, share, pathPart)
}

func mkdirSingle(ctx context.Context, dc *driveClient.Client, share *drive.Share, relPath string) error {
	relPath = strings.TrimSuffix(relPath, "/")
	dir := ""
	name := relPath
	if idx := strings.LastIndex(relPath, "/"); idx >= 0 {
		dir = relPath[:idx]
		name = relPath[idx+1:]
	}

	var parent *drive.Link
	var err error
	if dir == "" {
		parent = share.Link
	} else {
		parent, err = share.Link.ResolvePath(ctx, dir, true)
		if err != nil {
			return fmt.Errorf("mkdir: %s: %w", dir, err)
		}
	}

	if parent.Type() != proton.LinkTypeFolder {
		return fmt.Errorf("mkdir: %s: not a directory", dir)
	}

	newDir, err := dc.MkDir(ctx, share, parent, name)
	if err != nil {
		return err
	}

	if mkdirFlags.verbose {
		shareName, _ := share.GetName(ctx)
		fmt.Printf("mkdir: created directory '%s/%s'\n", shareName, relPath)
	}

	_ = newDir
	return nil
}
