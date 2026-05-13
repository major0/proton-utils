// Package accountCmd implements the account subcommands for proton-cli.
package accountCmd

import (
	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
)

var accountCmd = &cobra.Command{
	Use:   "account",
	Short: "Manage user authentication with Proton",
	Long:  "Manage user authentication with Proton",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if p := cmd.Root(); p != nil && p.PersistentPreRunE != nil {
			if err := p.PersistentPreRunE(p, args); err != nil {
				return err
			}
		}
		cli.SetServiceCmd(cmd, "account")
		return nil
	},
	Run: func(cmd *cobra.Command, _ []string) {
		_ = cmd.Help()
	},
}

func init() {
	cli.AddCommand(accountCmd)
}
