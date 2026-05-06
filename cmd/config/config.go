// Package configCmd implements the proton config subcommand tree.
package configCmd

import (
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "View and modify application configuration",
	Long:  "View and modify application configuration using namespaced selectors",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Chain root PersistentPreRunE for flag parsing and config loading.
		// Do NOT call SetServiceCmd — config doesn't need a service context.
		if p := cmd.Root(); p != nil && p.PersistentPreRunE != nil {
			if err := p.PersistentPreRunE(p, args); err != nil {
				return err
			}
		}
		return nil
	},
	Run: func(cmd *cobra.Command, _ []string) {
		_ = cmd.Help()
	},
}

func init() {
	cli.AddCommand(configCmd)
}
