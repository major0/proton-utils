package shareCmd

import (
	"context"
	"fmt"

	"github.com/major0/proton-cli/api/drive"
	cli "github.com/major0/proton-cli/internal/cli"
	"github.com/major0/proton-cli/internal/cli/shortid"
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
	ctx := context.Background()

	session, err := setupSessionFn(ctx, cmd)
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

	// Collect share IDs for short ID formatting.
	ids := make([]string, len(shares))
	for i := range shares {
		ids[i] = shares[i].Metadata().ShareID
	}
	short := map[string]string{}
	if rc.Verbose < 1 {
		short = shortid.FormatShortIDs(ids)
	}

	for i := range shares {
		name, _ := shares[i].GetName(ctx)
		meta := shares[i].Metadata()
		displayID := meta.ShareID
		if s, ok := short[meta.ShareID]; ok {
			displayID = s
		}
		fmt.Printf("%-8s  %-10s  %s  %s\n",
			drive.FormatShareType(meta.Type),
			displayID,
			cli.FormatEpoch(meta.CreationTime),
			name,
		)
	}

	return nil
}
