package accountCmd

import (
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	common "github.com/major0/proton-cli/api"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var accountListCmd = &cobra.Command{
	Use:   "list",
	Short: "List stored accounts",
	Long:  `List all accounts in the session store`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		rc := cli.GetContext(cmd)
		accounts, err := common.SessionList(rc.SessionStore)
		if err != nil {
			return err
		}

		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.AppendHeader(table.Row{"Account"})

		for _, account := range accounts {
			t.AppendRow(table.Row{account})
		}
		t.Render()

		return nil
	},
}

func init() {
	accountCmd.AddCommand(accountListCmd)
}
