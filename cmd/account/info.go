package accountCmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/docker/go-units"
	"github.com/major0/proton-cli/api/account"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

// userInfo holds the primitive values extracted from an opaque account.User
// for rendering. This keeps the render function testable without needing
// to construct opaque types.
type userInfo struct {
	ID                string
	DisplayName       string
	Name              string
	Email             string
	UsedSpace         int64
	MaxSpace          int64
	MailUsedSpace     int64
	DriveUsedSpace    int64
	CalendarUsedSpace int64
	PassUsedSpace     int64
	ContactUsedSpace  int64
}

// userInfoFrom extracts rendering data from an opaque account.User.
func userInfoFrom(u account.User) userInfo {
	return userInfo{
		ID:                u.ID(),
		DisplayName:       u.DisplayName(),
		Name:              u.Name(),
		Email:             u.Email(),
		UsedSpace:         u.UsedSpace(),
		MaxSpace:          u.MaxSpace(),
		MailUsedSpace:     u.MailUsedSpace(),
		DriveUsedSpace:    u.DriveUsedSpace(),
		CalendarUsedSpace: u.CalendarUsedSpace(),
		PassUsedSpace:     u.PassUsedSpace(),
		ContactUsedSpace:  u.ContactUsedSpace(),
	}
}

// renderUserInfo writes user profile and storage information to the given writer.
//
//nolint:errcheck
func renderUserInfo(w io.Writer, user userInfo) {
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
	fmt.Fprintf(w, "%-12s %10s\n", "Mail", units.BytesSize(float64(user.MailUsedSpace)))
	fmt.Fprintf(w, "%-12s %10s\n", "Drive", units.BytesSize(float64(user.DriveUsedSpace)))
	fmt.Fprintf(w, "%-12s %10s\n", "Calendar", units.BytesSize(float64(user.CalendarUsedSpace)))
	fmt.Fprintf(w, "%-12s %10s\n", "Pass", units.BytesSize(float64(user.PassUsedSpace)))
	fmt.Fprintf(w, "%-12s %10s\n", "Contacts", units.BytesSize(float64(user.ContactUsedSpace)))
}

var accountInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "report account information",
	Long:  `report information about currently logged in user`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		rc := cli.GetContext(cmd)
		ctx, cancel := context.WithTimeout(context.Background(), rc.Timeout)
		defer cancel()

		session, err := cli.SetupSession(ctx, cmd)
		if err != nil {
			return err
		}

		acct := account.NewClient(session)
		user, err := acct.GetUser(ctx)
		if err != nil {
			return err
		}

		renderUserInfo(os.Stdout, userInfoFrom(user))
		return nil
	},
}

func init() {
	accountCmd.AddCommand(accountInfoCmd)
}
