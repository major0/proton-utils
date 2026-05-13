// Package driveCmd implements the drive subcommands for proton-cli.
package driveCmd

import (
	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
)

var driveCmd = &cobra.Command{
	Use:   "drive",
	Short: "Manage files and directories in Proton Drive",
	Long:  "Manage files and directories in Proton Drive",
	PersistentPreRunE: cli.ServicePreRunE("drive"),
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
