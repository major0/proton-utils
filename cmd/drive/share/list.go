package shareCmd

import (
	"context"
	"fmt"
	"time"

	"github.com/major0/proton-cli/api/drive"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var shareListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List shares",
	Long:    "List all Proton Drive shares visible to this account",
	RunE:    runShareList,
}

func init() {
	shareCmd.AddCommand(shareListCmd)
}

func runShareList(cmd *cobra.Command, _ []string) error {
	rc := cli.GetContext(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), rc.Timeout)
	defer cancel()

	session, err := restoreSessionFn(ctx)
	if err != nil {
		return err
	}

	dc, err := newDriveClientFn(ctx, session)
	if err != nil {
		return err
	}

	shares, err := listSharesFn(ctx, dc)
	if err != nil {
		return err
	}

	for i := range shares {
		name, _ := shares[i].GetName(ctx)
		meta := shares[i].Metadata()
		fmt.Printf("%-8s  %s  %s\n",
			drive.FormatShareType(meta.Type),
			fmtTime(meta.CreationTime),
			name,
		)
	}

	return nil
}

// fmtTime formats a Unix epoch as YYYY-MM-DD, or "-" for zero.
func fmtTime(epoch int64) string {
	if epoch == 0 {
		return "-"
	}
	return time.Unix(epoch, 0).Format("2006-01-02")
}
