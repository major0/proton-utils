package accountCmd

import (
	"context"
	"io"
	"os"

	"github.com/jedib0t/go-pretty/v6/table"
	common "github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/account"
	cli "github.com/major0/proton-cli/internal/cli"
	"github.com/spf13/cobra"
)

// addressInfo holds the primitive values extracted from an opaque
// account.Address for rendering.
type addressInfo struct {
	Email  string
	Type   int
	Status int
}

// addressInfoFrom extracts rendering data from opaque account.Address values.
func addressInfoFrom(addrs []account.Address) []addressInfo {
	out := make([]addressInfo, len(addrs))
	for i, a := range addrs {
		out[i] = addressInfo{
			Email:  a.Email(),
			Type:   a.Type(),
			Status: a.Status(),
		}
	}
	return out
}

// renderAddresses writes the address table to the given writer.
func renderAddresses(w io.Writer, addresses []addressInfo) {
	t := table.NewWriter()
	t.SetOutputMirror(w)
	t.AppendHeader(table.Row{"Address", "Type", "State"})
	for i := range addresses {
		addr := addresses[i].Email
		addrType := common.AddressType(addresses[i].Type).String()
		addrStatus := common.AddressStatus(addresses[i].Status).String()
		t.AppendRow(table.Row{addr, addrType, addrStatus})
	}
	t.Render()
}

var accountAddressCmd = &cobra.Command{
	Use:     "addresses",
	Aliases: []string{"address", "addr"},
	Short:   "report all email addresses associated with the account",
	Long:    `report all email addresses associated with the account`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		rc := cli.GetContext(cmd)
		ctx, cancel := context.WithTimeout(context.Background(), rc.Timeout)
		defer cancel()

		session, err := cli.SetupSession(ctx, cmd)
		if err != nil {
			return err
		}

		acct := account.NewClient(session)
		addresses, err := acct.GetAddresses(ctx)
		if err != nil {
			return err
		}

		renderAddresses(os.Stdout, addressInfoFrom(addresses))
		return nil
	},
}

func init() {
	accountCmd.AddCommand(accountAddressCmd)
}
