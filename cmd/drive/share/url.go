package shareCmd

import "github.com/spf13/cobra"

var shareURLCmd = &cobra.Command{
	Use:   "url",
	Short: "Manage share public URLs",
	Long:  "Manage public URLs for shares. Use subcommands to enable, disable, or change passwords.",
	Run: func(cmd *cobra.Command, _ []string) {
		_ = cmd.Help()
	},
}

func init() {
	shareCmd.AddCommand(shareURLCmd)
}
