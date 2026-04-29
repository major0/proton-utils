// Package driveCmd implements the drive subcommands for proton-cli.
package driveCmd

import (
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var driveCmd = &cobra.Command{
	Use:   "drive",
	Short: "Manage files and directories in Proton Drive",
	Long:  "Manage files and directories in Proton Drive",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if p := cmd.Root(); p != nil && p.PersistentPreRunE != nil {
			if err := p.PersistentPreRunE(p, args); err != nil {
				return err
			}
		}
		cli.SetServiceCmd(cmd, "drive")
		return nil
	},
	Run: func(cmd *cobra.Command, _ []string) {
		_ = cmd.Help()
	},
}

func init() {
	cli.AddCommand(driveCmd)
}

// AddCommand registers a subcommand under the drive command group.
func AddCommand(cmd *cobra.Command) {
	driveCmd.AddCommand(cmd)
}
