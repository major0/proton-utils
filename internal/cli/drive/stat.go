package driveCmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ProtonMail/go-proton-api"
	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/spf13/cobra"
)

var driveStatCmd = &cobra.Command{
	Use:   "stat <path>",
	Short: "Display file metadata from Proton Drive",
	Long:  "Dump all metadata fields including decrypted XAttr for a file",
	Args:  cobra.ExactArgs(1),
	RunE:  runStat,
}

func init() {
	driveCmd.AddCommand(driveStatCmd)
}

func runStat(cmd *cobra.Command, args []string) error {
	rawPath := args[0]
	ctx := context.Background()

	session, err := cli.SetupSession(ctx, cmd)
	if err != nil {
		return err
	}

	dc, err := cli.NewDriveClient(ctx, session)
	if err != nil {
		return err
	}

	sharePart, pathPart, err := parseProtonURI(rawPath)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	if pathPart == "" {
		return fmt.Errorf("stat: missing file path")
	}

	share, err := dc.ResolveShareComponent(ctx, sharePart)
	if err != nil {
		return fmt.Errorf("stat: %s: %w", sharePart, err)
	}

	pathPart = strings.TrimSuffix(pathPart, "/")
	link, err := share.Link.ResolvePath(ctx, pathPart, true)
	if err != nil {
		return fmt.Errorf("stat: %s: %w", pathPart, err)
	}

	pLink := link.ProtonLink()
	name, _ := link.Name()

	fmt.Printf("  File: %s\n", name)
	fmt.Printf("LinkID: %s\n", pLink.LinkID)
	fmt.Printf("  Type: %d (1=file, 2=folder)\n", pLink.Type)
	fmt.Printf(" State: %d (1=active, 2=draft, 3=trashed)\n", pLink.State)
	fmt.Printf("  Size: %d\n", link.Size())
	fmt.Printf("  MIME: %s\n", pLink.MIMEType)
	fmt.Printf("Create: %d\n", pLink.CreateTime)
	fmt.Printf("Modify: %d\n", link.ModifyTime())

	if pLink.Type == proton.LinkTypeFile && pLink.FileProperties != nil {
		rev := &pLink.FileProperties.ActiveRevision
		fmt.Printf("\nRevision (from listing):\n")
		fmt.Printf("     ID: %s\n", rev.ID)
		fmt.Printf("  State: %d\n", rev.State)
		fmt.Printf("   Size: %d\n", rev.Size)
		fmt.Printf("  XAttr: %s\n", truncate(rev.XAttr, 60))

		// Fetch full revision to get XAttr (listings don't include it).
		shareID := share.ProtonShare().ShareID
		fullRev, err := dc.Session.Client.GetRevisionAllBlocks(ctx, shareID, pLink.LinkID, rev.ID)
		if err != nil {
			fmt.Printf("  Full revision fetch error: %v\n", err)
		} else {
			fmt.Printf("\nRevision (full fetch):\n")
			fmt.Printf("  XAttr: %s\n", truncate(fullRev.XAttr, 60))

			// Decrypt XAttr from full revision
			nodeKR, krErr := link.KeyRing()
			addrKR, addrErr := dc.AddrKRForLink(link)
			switch {
			case krErr != nil:
				fmt.Printf("  XAttr decrypt error (keyring): %v\n", krErr)
			case addrErr != nil:
				fmt.Printf("  XAttr decrypt error (addrKR): %v\n", addrErr)
			default:
				xattr, xErr := fullRev.GetDecXAttrString(addrKR, nodeKR)
				switch {
				case xErr != nil:
					fmt.Printf("  XAttr decrypt error: %v\n", xErr)
				case xattr == nil:
					fmt.Printf("  XAttr: (nil)\n")
				default:
					fmt.Printf("\nDecrypted XAttr:\n")
					fmt.Printf("  ModificationTime: %s\n", xattr.Common.ModificationTime)
					fmt.Printf("            Size: %d\n", xattr.Common.Size)
					fmt.Printf("      BlockSizes: %v\n", xattr.Common.BlockSizes)
					fmt.Printf("         Digests: %v\n", xattr.Common.Digests)
					// Mode migrated from Common to the POSIX section; other
					// clients' sections live in Extra. Dump every preserved
					// section verbatim (Link.Mode() below reports the mode).
					for _, k := range sortedKeys(xattr.Extra) {
						fmt.Printf("      Extra[%s]: %s\n", k, string(xattr.Extra[k]))
					}
				}
			}
		}
	}

	mode, present := link.Mode()
	fmt.Printf("\nLink.Mode(): %04o (%d) present=%v\n", mode, mode, present)

	return nil
}

// sortedKeys returns the keys of the Extra section map in ascending order so
// the diagnostic XAttr dump is deterministic.
func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
