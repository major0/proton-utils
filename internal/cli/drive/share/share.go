// Package shareCmd implements the share subcommands for proton-cli.
package shareCmd

import (
	"context"
	"fmt"

	"github.com/major0/proton-utils/api/drive"
	cli "github.com/major0/proton-utils/internal/cli"
	driveCmd "github.com/major0/proton-utils/internal/cli/drive"
	"github.com/spf13/cobra"
)

// restoreSessionFn, newDriveClientFn, resolveShareFn, and listSharesFn
// are replaceable for testing.
var (
	setupSessionFn   = cli.SetupSession
	newDriveClientFn = cli.NewDriveClient
	resolveShareFn   = func(ctx context.Context, dc *drive.Client, name string) (*drive.Share, error) {
		return dc.ResolveShare(ctx, name, true)
	}
	listSharesFn = func(ctx context.Context, dc *drive.Client) ([]*drive.Share, error) {
		shares, err := dc.ListShares(ctx, true)
		if err != nil {
			return nil, err
		}
		ptrs := make([]*drive.Share, len(shares))
		for i := range shares {
			ptrs[i] = &shares[i]
		}
		return ptrs, nil
	}
	deleteShareFn = func(ctx context.Context, dc *drive.Client, shareID string, force bool) error {
		return dc.DeleteShareByID(ctx, shareID, force)
	}
	listMembersFn = func(ctx context.Context, dc *drive.Client, shareID string) ([]drive.Member, error) {
		return dc.ListMembers(ctx, shareID)
	}
	listInvitationsFn = func(ctx context.Context, dc *drive.Client, shareID string) ([]drive.Invitation, error) {
		return dc.ListInvitations(ctx, shareID)
	}
	listExternalInvitationsFn = func(ctx context.Context, dc *drive.Client, shareID string) ([]drive.ExternalInvitation, error) {
		return dc.ListExternalInvitations(ctx, shareID)
	}
	removeMemberFn = func(ctx context.Context, dc *drive.Client, shareID, memberID string) error {
		return dc.RemoveMember(ctx, shareID, memberID)
	}
	deleteInvitationFn = func(ctx context.Context, dc *drive.Client, shareID, invitationID string) error {
		return dc.DeleteInvitation(ctx, shareID, invitationID)
	}
	deleteExternalInvitationFn = func(ctx context.Context, dc *drive.Client, shareID, externalInvitationID string) error {
		return dc.DeleteExternalInvitation(ctx, shareID, externalInvitationID)
	}
)

var shareCmd = &cobra.Command{
	Use:   "share",
	Short: "Manage Proton Drive shares",
	Long:  "Manage Proton Drive shares, invitations, and members",
	Run: func(cmd *cobra.Command, _ []string) {
		_ = cmd.Help()
	},
}

// notImplemented returns a RunE that prints a not-yet-implemented message.
func notImplemented(name string) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, _ []string) error {
		return fmt.Errorf("%s: not yet implemented (requires sharing API additions to go-proton-api)", name)
	}
}

var shareShowCmd = &cobra.Command{
	Use:   "show <share-name>",
	Short: "Show detailed share information",
	Long:  "Show detailed information about a share including members and invitations",
	Args:  cobra.ExactArgs(1),
	RunE:  notImplemented("share show"),
}

var shareInviteCmd = &cobra.Command{
	Use:   "invite <share-name> <email> [permissions]",
	Short: "Invite a user to a share",
	Long:  "Invite a Proton user or external email to a share",
	Args:  cobra.MinimumNArgs(2),
	RunE:  notImplemented("share invite"),
}

var shareRevokeCmd = &cobra.Command{
	Use:   "revoke <share-name> <email-or-member-id>",
	Short: "Revoke access to a share",
	Long:  "Remove a member or cancel an invitation for a share",
	Args:  cobra.ExactArgs(2),
	RunE:  notImplemented("share revoke"),
}

func init() {
	driveCmd.AddCommand(shareCmd)
	shareCmd.AddCommand(shareShowCmd)
	shareCmd.AddCommand(shareInviteCmd)
	shareCmd.AddCommand(shareRevokeCmd)
}
