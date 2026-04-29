package accountCmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/ProtonMail/go-proton-api"
	"github.com/docker/go-units"
	"github.com/major0/proton-cli/api/account"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

// renderUserInfo writes user profile and storage information to the given writer.
//
//nolint:errcheck
func renderUserInfo(w io.Writer, user proton.User) {
	fmt.Fprintln(w, "ID: "+user.ID)
	fmt.Fprintln(w, "Display Name: "+user.DisplayName)
	fmt.Fprintln(w, "Username: "+user.Name)
	fmt.Fprintln(w, "Email: "+user.Email)
	fmt.Fprintln(w, "")

	total := units.BytesSize(float64(user.MaxSpace))
	used := units.BytesSize(float64(user.UsedSpace))
	avail := "-"
	pct := "-"
	if user.MaxSpace > 0 {
		free := user.MaxSpace - user.UsedSpace
		avail = units.BytesSize(float64(free))
		pct = fmt.Sprintf("%.1f%%", float64(user.UsedSpace)/float64(user.MaxSpace)*100)
	}

	fmt.Fprintf(w, "Storage: %s / %s (%s free, %s used)\n", used, total, avail, pct)
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "%-12s %10s\n", "Service", "Used")
	fmt.Fprintf(w, "%-12s %10s\n", "Mail", units.BytesSize(float64(user.ProductUsedSpace.Mail)))
	fmt.Fprintf(w, "%-12s %10s\n", "Drive", units.BytesSize(float64(user.ProductUsedSpace.Drive)))
	fmt.Fprintf(w, "%-12s %10s\n", "Calendar", units.BytesSize(float64(user.ProductUsedSpace.Calendar)))
	fmt.Fprintf(w, "%-12s %10s\n", "Pass", units.BytesSize(float64(user.ProductUsedSpace.Pass)))
	fmt.Fprintf(w, "%-12s %10s\n", "Contacts", units.BytesSize(float64(user.ProductUsedSpace.Contact)))
}

var accountInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "report account information",
	Long:  `report information about currently logged in user`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		rc := cli.GetContext(cmd)
		ctx, cancel := context.WithTimeout(context.Background(), rc.Timeout)
		defer cancel()

		session, err := cli.RestoreSession(ctx)
		if err != nil {
			return err
		}

		acct := account.NewClient(session)
		user, err := acct.GetUser(ctx)
		if err != nil {
			return err
		}

		renderUserInfo(os.Stdout, user)
		return nil
	},
}

func init() {
	accountCmd.AddCommand(accountInfoCmd)
}
