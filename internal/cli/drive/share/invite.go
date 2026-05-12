package shareCmd

import (
	"context"
	"fmt"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/drive"
	"github.com/spf13/cobra"
)

var inviteFlags struct {
	permissions string
}

func init() {
	shareInviteCmd.RunE = runShareInvite
	shareInviteCmd.Flags().StringVar(&inviteFlags.permissions, "permissions", "read", "Permission level: read (viewer) or write (editor)")
}

func parsePermissions(s string) (int, error) {
	switch s {
	case "read", "viewer":
		return drive.PermViewer, nil
	case "write", "editor":
		return drive.PermEditor, nil
	default:
		return 0, fmt.Errorf("invalid permissions %q (use read or write)", s)
	}
}

func runShareInvite(cmd *cobra.Command, args []string) error {
	shareName := args[0]
	email := args[1]

	perms, err := parsePermissions(inviteFlags.permissions)
	if err != nil {
		return err
	}

	ctx := context.Background()

	session, err := setupSessionFn(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := newDriveClientFn(ctx, session)
	if err != nil {
		return err
	}

	resolved, err := resolveShareFn(ctx, dc, shareName)
	if err != nil {
		return fmt.Errorf("share invite: %s: share not found", shareName)
	}

	// Determine recipient type.
	pubKeys, recipientType, err := session.Client.GetPublicKeys(ctx, email)
	if err != nil {
		return fmt.Errorf("share invite: %s: failed to fetch recipient keys: %w", email, err)
	}

	shareID := resolved.Metadata().ShareID

	if recipientType == proton.RecipientTypeInternal {
		return inviteInternalUser(ctx, dc, resolved, session, email, shareID, perms, pubKeys)
	}
	return inviteExternalUser(ctx, dc, session, email, shareID, perms)
}

func inviteInternalUser(ctx context.Context, dc *drive.Client, resolved *drive.Share, session *api.Session, email, shareID string, perms int, pubKeys proton.PublicKeys) error {
	// Build invitee keyring from public keys.
	inviteeKR, err := pubKeys.GetKeyRing()
	if err != nil {
		return fmt.Errorf("share invite: %s: building invitee keyring: %w", email, err)
	}

	// Get share keyring and passphrase.
	shareKR := resolved.KeyRingValue()
	if shareKR == nil {
		return fmt.Errorf("share invite: %s: share keyring is nil", shareID)
	}
	sharePassphrase := resolved.ProtonShare().Passphrase

	// Get inviter's address keyring.
	addrID := resolved.ProtonShare().AddressID
	inviterKR, ok := session.AddressKeyRings()[addrID]
	if !ok {
		return fmt.Errorf("share invite: inviter address keyring not found for %s", addrID)
	}

	// Generate key packet.
	keyPacketB64, sigArmored, err := drive.GenerateKeyPacket(shareKR, inviterKR, inviteeKR, sharePassphrase)
	if err != nil {
		return fmt.Errorf("share invite: %s: generating key packet: %w", email, err)
	}

	// Use the share creator as inviter email.
	inviterEmail := resolved.Metadata().Creator

	var payload drive.InviteProtonUserPayload
	payload.Invitation.InviterEmail = inviterEmail
	payload.Invitation.InviteeEmail = email
	payload.Invitation.Permissions = perms
	payload.Invitation.KeyPacket = keyPacketB64
	payload.Invitation.KeyPacketSignature = sigArmored

	if err := dc.InviteProtonUser(ctx, shareID, payload); err != nil {
		return fmt.Errorf("share invite: %s: %w", email, err)
	}

	fmt.Printf("Invited %s to share (permissions: %s)\n", email, drive.FormatPermissions(perms))
	return nil
}

func inviteExternalUser(ctx context.Context, dc *drive.Client, session *api.Session, email, shareID string, perms int) error {
	// For external users, we need the inviter's address ID.
	// Use the first available address.
	addrKRs := session.AddressKeyRings()
	if len(addrKRs) == 0 {
		return fmt.Errorf("share invite: no address keyrings available")
	}

	// Get the first address ID.
	var inviterAddrID string
	for id := range addrKRs {
		inviterAddrID = id
		break
	}

	var payload drive.InviteExternalUserPayload
	payload.ExternalInvitation.InviterAddressID = inviterAddrID
	payload.ExternalInvitation.InviteeEmail = email
	payload.ExternalInvitation.Permissions = perms
	// ExternalInvitationSignature is required but the exact signing
	// mechanism for external invites needs further investigation.
	// For now, leave empty — the API may reject this.
	payload.ExternalInvitation.ExternalInvitationSignature = ""

	if err := dc.InviteExternalUser(ctx, shareID, payload); err != nil {
		return fmt.Errorf("share invite: %s: %w", email, err)
	}

	fmt.Printf("Invited %s (external) to share (permissions: %s)\n", email, drive.FormatPermissions(perms))
	return nil
}
