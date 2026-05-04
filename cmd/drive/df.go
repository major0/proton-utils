package driveCmd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ProtonMail/go-proton-api"
	"github.com/docker/go-units"
	"github.com/major0/proton-cli/api/account"
	"github.com/major0/proton-cli/api/drive"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var driveDfCmd = &cobra.Command{
	Use:   "df",
	Short: "Show volume disk usage",
	Long:  "Show Proton Drive volume usage in df-style output",
	RunE:  runDf,
}

func init() {
	driveCmd.AddCommand(driveDfCmd)
}

// buildNameIndex resolves volume share names, logging errors at debug level.
func buildNameIndex(ctx context.Context, dc *drive.Client, volumes []drive.Volume) map[string]string {
	nameIndex := make(map[string]string)
	for _, v := range volumes {
		share, err := dc.GetShare(ctx, v.ProtonVolume.Share.ShareID)
		if err != nil {
			slog.Debug("df: resolve share", "shareID", v.ProtonVolume.Share.ShareID, "error", err)
			continue
		}
		name, err := share.GetName(ctx)
		if err != nil {
			slog.Debug("df: share name", "shareID", v.ProtonVolume.Share.ShareID, "error", err)
			continue
		}
		nameIndex[v.ProtonVolume.VolumeID] = name
	}
	return nameIndex
}

// printVolumeRows prints the df-style table rows for each volume.
func printVolumeRows(volumes []drive.Volume, nameIndex map[string]string, shareIndex map[string]proton.ShareMetadata, shortIDs map[string]string) {
	for _, v := range volumes {
		label := nameIndex[v.ProtonVolume.VolumeID]
		if label == "" {
			if s, ok := shareIndex[v.ProtonVolume.Share.ShareID]; ok {
				label = drive.FormatShareType(s.Type)
			} else if s, ok := shortIDs[v.ProtonVolume.VolumeID]; ok {
				label = s
			} else {
				label = v.ProtonVolume.VolumeID
			}
		}

		used := v.ProtonVolume.UsedSpace
		size := "unlimited"
		avail := "-"
		usePct := "-"

		if v.ProtonVolume.MaxSpace != nil {
			total := *v.ProtonVolume.MaxSpace
			size = units.BytesSize(float64(total))
			free := total - used
			if free < 0 {
				free = 0
			}
			avail = units.BytesSize(float64(free))
			if total > 0 {
				usePct = fmt.Sprintf("%.0f%%", float64(used)/float64(total)*100)
			}
		}

		fmt.Printf("%-20s %10s %10s %10s %5s %10s %10s %s\n",
			label,
			size,
			units.BytesSize(float64(used)),
			avail,
			usePct,
			units.BytesSize(float64(v.ProtonVolume.DownloadedBytes)),
			units.BytesSize(float64(v.ProtonVolume.UploadedBytes)),
			dfVolState(v.ProtonVolume.State),
		)
	}
}

func runDf(cmd *cobra.Command, _ []string) error {
	rc := cli.GetContext(cmd)
	ctx := context.Background()

	session, err := cli.SetupSession(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := cli.NewDriveClient(ctx, cmd, session)
	if err != nil {
		return err
	}

	volumes, err := dc.ListVolumes(ctx)
	if err != nil {
		return err
	}

	shares, err := dc.ListSharesMetadata(ctx, true)
	if err != nil {
		return err
	}

	shareIndex := make(map[string]proton.ShareMetadata, len(shares))
	for _, s := range shares {
		shareIndex[s.ShareID] = proton.ShareMetadata(s)
	}

	nameIndex := buildNameIndex(ctx, dc, volumes)

	// Compute short volume IDs for fallback labels.
	volIDs := make([]string, len(volumes))
	for i, v := range volumes {
		volIDs[i] = v.ProtonVolume.VolumeID
	}
	shortVolIDs := map[string]string{}
	if rc.Verbose < 1 {
		shortVolIDs = formatShortIDs(volIDs)
	}

	// Account-level quota from the user object.
	acct := account.NewClient(session)
	user, err := acct.GetUser(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("%-20s %10s %10s %10s %5s %10s %10s %s\n",
		"Volume", "Size", "Used", "Avail", "Use%", "Down", "Up", "State")

	printVolumeRows(volumes, nameIndex, shareIndex, shortVolIDs)

	// Account total line.
	acctSize := units.BytesSize(float64(user.MaxSpace()))
	acctUsed := units.BytesSize(float64(user.UsedSpace()))
	acctAvail := "-"
	acctPct := "-"
	if user.MaxSpace() > 0 {
		free := user.MaxSpace() - user.UsedSpace()
		acctAvail = units.BytesSize(float64(free))
		acctPct = fmt.Sprintf("%.0f%%", float64(user.UsedSpace())/float64(user.MaxSpace())*100)
	}
	fmt.Printf("%-20s %10s %10s %10s %5s %10s %10s %s\n",
		"total", acctSize, acctUsed, acctAvail, acctPct, "-", "-", "-")

	return nil
}

func dfVolState(state proton.VolumeState) string {
	switch state {
	case proton.VolumeStateActive:
		return "active"
	case proton.VolumeStateLocked:
		return "locked"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}
