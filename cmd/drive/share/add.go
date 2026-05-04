package shareCmd

import (
	"context"
	"fmt"

	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-cli/api/drive"
	driveCmd "github.com/major0/proton-cli/cmd/drive"
	"github.com/spf13/cobra"
)

var shareAddCmd = &cobra.Command{
	Use:   "add <proton-path>",
	Short: "Create a share from an existing file or folder",
	Long:  "Create a share from an existing Proton Drive file or folder. The share name is the link's decrypted name.",
	Args:  cobra.ExactArgs(1),
	RunE:  runShareAdd,
}

func init() {
	shareCmd.AddCommand(shareAddCmd)
}

func runShareAdd(cmd *cobra.Command, args []string) error {
	protonPath := args[0]

	ctx := context.Background()

	session, err := setupSessionFn(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := newDriveClientFn(ctx, cmd, session)
	if err != nil {
		return err
	}

	link, linkShare, err := driveCmd.ResolveProtonPath(ctx, dc, protonPath)
	if err != nil {
		return fmt.Errorf("share add: %s: not found", protonPath)
	}

	// Check if already shared.
	metas, err := dc.ListSharesMetadata(ctx, true)
	if err != nil {
		return fmt.Errorf("share add: listing shares: %w", err)
	}
	for _, meta := range metas {
		if meta.LinkID == link.ProtonLink().LinkID {
			return fmt.Errorf("share add: %s: already shared", protonPath)
		}
	}

	linkName, err := link.Name()
	if err != nil {
		return fmt.Errorf("share add: %s: decrypt name: %w", protonPath, err)
	}

	linkNodeKR, err := link.KeyRing()
	if err != nil {
		return fmt.Errorf("share add: %s: link keyring: %w", protonPath, err)
	}

	addrID := linkShare.ProtonShare().AddressID
	addrKR, ok := session.AddressKeyRings()[addrID]
	if !ok {
		return fmt.Errorf("share add: address keyring not found for %s", addrID)
	}

	// Parent keyring for decrypting link passphrase/name session keys.
	var parentKR *crypto.KeyRing
	if link.ParentLink() != nil {
		parentKR, err = link.ParentLink().KeyRing()
		if err != nil {
			return fmt.Errorf("share add: parent keyring: %w", err)
		}
	} else {
		parentKR = linkShare.KeyRingValue()
	}

	// Generate share crypto.
	linkPassphrase := link.ProtonLink().NodePassphrase
	linkEncName := link.ProtonLink().Name

	shareKey, sharePassphrase, sharePassphraseSig, ppKP, nameKP, err := drive.GenerateShareCrypto(
		addrKR, linkNodeKR, parentKR, linkPassphrase, linkEncName,
	)
	if err != nil {
		return fmt.Errorf("share add: %s: %w", protonPath, err)
	}

	payload := drive.CreateDriveSharePayload{
		AddressID:                addrID,
		RootLinkID:               link.LinkID(),
		ShareKey:                 shareKey,
		SharePassphrase:          sharePassphrase,
		SharePassphraseSignature: sharePassphraseSig,
		PassphraseKeyPacket:      ppKP,
		NameKeyPacket:            nameKP,
	}

	volumeID := link.VolumeID()

	shareID, err := dc.CreateShareFromLink(ctx, volumeID, payload)
	if err != nil {
		return fmt.Errorf("share add: %s: %w", protonPath, err)
	}

	fmt.Printf("Created share %q (%s)\n", linkName, shareID)
	return nil
}
