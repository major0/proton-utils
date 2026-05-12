package shareCmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
	cli "github.com/major0/proton-cli/internal/cli"
	"github.com/spf13/cobra"
)

var showFlags struct {
	getPassword bool
}

func init() {
	shareShowCmd.RunE = runShareShow
	shareShowCmd.Flags().BoolVar(&showFlags.getPassword, "get-password", false, "Output only the decrypted ShareURL password")
}

func runShareShow(cmd *cobra.Command, args []string) error {
	name := args[0]

	ctx := context.Background()

	session, err := setupSessionFn(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := newDriveClientFn(ctx, session)
	if err != nil {
		return err
	}

	resolved, err := resolveShareFn(ctx, dc, name)
	if err != nil {
		return fmt.Errorf("share show: %s: share not found", name)
	}

	meta := resolved.Metadata()

	// --get-password: exclusive output mode.
	if showFlags.getPassword {
		urls, err := listShareURLsFn(ctx, dc, meta.ShareID)
		if err != nil {
			return fmt.Errorf("share show: %s: %w", name, err)
		}
		if len(urls) == 0 || urls[0].Password == "" {
			return fmt.Errorf("share show: %s: no public URL with password exists", name)
		}
		password, err := dc.DecryptShareURLPassword(ctx, resolved, &urls[0])
		if err != nil {
			return fmt.Errorf("share show: %s: decrypt password: %w", name, err)
		}
		fmt.Println(password)
		return nil
	}

	printShareMetadata(ctx, resolved)

	// Show origin volume.
	origin := dc.VolumeOrigin(ctx, resolved.VolumeID())
	fmt.Printf("Origin:   %s\n", origin)

	// Show public URL status.
	urls, err := listShareURLsFn(ctx, dc, meta.ShareID)
	switch {
	case err != nil:
		slog.Error("share show: listing URLs", "error", err)
	case len(urls) > 0:
		fmt.Printf("Public URL: enabled (%d downloads)\n", urls[0].NumAccesses)
	default:
		fmt.Println("Public URL: disabled")
	}

	if meta.Type == proton.ShareTypeMain || meta.Type == drive.ShareTypePhotos {
		fmt.Println("\nMembers:      -")
		fmt.Println("Invitations:  -")
		return nil
	}

	shareID := meta.ShareID
	printMembers(ctx, dc, shareID)
	printInvitations(ctx, dc, shareID)
	printExternalInvitations(ctx, dc, shareID)

	return nil
}

func printShareMetadata(ctx context.Context, s *drive.Share) {
	meta := s.Metadata()
	shareName, _ := s.GetName(ctx)

	fmt.Printf("Share:    %s\n", shareName)
	fmt.Printf("Type:     %s\n", drive.FormatShareType(meta.Type))
	fmt.Printf("Creator:  %s\n", meta.Creator)
	fmt.Printf("Created:  %s\n", cli.FormatEpoch(meta.CreationTime))
}

func printMembers(ctx context.Context, dc *drive.Client, shareID string) {
	members, err := listMembersFn(ctx, dc, shareID)
	if err != nil {
		slog.Error("share show: listing members", "error", err)
		fmt.Fprintf(os.Stderr, "warning: failed to list members: %v\n", err)
		return
	}

	fmt.Printf("\nMembers (%d):\n", len(members))
	if len(members) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, m := range members {
		fmt.Printf("  %-30s  %-8s  %s\n",
			m.Email,
			drive.FormatPermissions(m.Permissions),
			m.MemberID,
		)
	}
}

func printInvitations(ctx context.Context, dc *drive.Client, shareID string) {
	invs, err := listInvitationsFn(ctx, dc, shareID)
	if err != nil {
		slog.Error("share show: listing invitations", "error", err)
		fmt.Fprintf(os.Stderr, "warning: failed to list invitations: %v\n", err)
		return
	}

	fmt.Printf("\nPending Invitations (%d):\n", len(invs))
	if len(invs) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, inv := range invs {
		fmt.Printf("  %-30s  %-8s  %s  %s\n",
			inv.InviteeEmail,
			drive.FormatPermissions(inv.Permissions),
			cli.FormatEpoch(inv.CreateTime),
			inv.InvitationID,
		)
	}
}

func printExternalInvitations(ctx context.Context, dc *drive.Client, shareID string) {
	exts, err := listExternalInvitationsFn(ctx, dc, shareID)
	if err != nil {
		slog.Error("share show: listing external invitations", "error", err)
		fmt.Fprintf(os.Stderr, "warning: failed to list external invitations: %v\n", err)
		return
	}

	fmt.Printf("\nPending External Invitations (%d):\n", len(exts))
	if len(exts) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, ext := range exts {
		fmt.Printf("  %-30s  %-8s  %s  %s\n",
			ext.InviteeEmail,
			drive.FormatPermissions(ext.Permissions),
			cli.FormatEpoch(ext.CreateTime),
			ext.ExternalInvitationID,
		)
	}
}
