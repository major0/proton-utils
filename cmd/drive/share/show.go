package shareCmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
	driveClient "github.com/major0/proton-cli/api/drive/client"
	"github.com/spf13/cobra"
)

func init() {
	shareShowCmd.RunE = runShareShow
}

func runShareShow(_ *cobra.Command, args []string) error {
	name := args[0]

	ctx := context.Background()

	session, err := restoreSessionFn(ctx)
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

	printShareMetadata(ctx, resolved)

	meta := resolved.Metadata()
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
	fmt.Printf("Created:  %s\n", fmtTime(meta.CreationTime))
}

func printMembers(ctx context.Context, dc *driveClient.Client, shareID string) {
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

func printInvitations(ctx context.Context, dc *driveClient.Client, shareID string) {
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
			fmtTime(inv.CreateTime),
			inv.InvitationID,
		)
	}
}

func printExternalInvitations(ctx context.Context, dc *driveClient.Client, shareID string) {
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
			fmtTime(ext.CreateTime),
			ext.ExternalInvitationID,
		)
	}
}
