package lumoCmd

import (
	"fmt"
	"os"

	"github.com/major0/proton-utils/api/lumo"
	"github.com/spf13/cobra"
)

var chatCpCmd = &cobra.Command{
	Use:     "cp <source> [destination]",
	Aliases: []string{"copy"},
	Short:   "Copy a conversation to a new or existing space",
	RunE:    runChatCp,
}

func init() {
	chatCmd.AddCommand(chatCpCmd)
}

func runChatCp(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true

	if len(args) == 0 || len(args) > 2 {
		return fmt.Errorf("usage: proton lumo chat cp <source> [destination]")
	}

	// Normalize and parse source argument.
	srcNorm := normalizeArg(args[0])
	srcURI, err := parseLumoURI(srcNorm)
	if err != nil {
		return err
	}
	if srcURI.Path == "" {
		return fmt.Errorf("source path must not be empty; provide a conversation ID or title")
	}

	// Normalize and parse destination argument (default: lumo:///).
	destArg := "lumo:///"
	if len(args) >= 2 {
		destArg = normalizeArg(args[1]) //nolint:gosec // bounds checked: len(args) <= 2 enforced above
	}
	destURI, err := parseLumoURI(destArg)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	client, err := restoreClient(cmd)
	if err != nil {
		return err
	}

	// Fetch all spaces once (needed for both source and destination resolution).
	spaces, err := client.ListSpaces(ctx)
	if err != nil {
		return fmt.Errorf("listing spaces: %w", err)
	}

	// Build conversation pairs from spaces.
	var pairs []lumo.SpaceConversation
	for i := range spaces {
		for _, conv := range spaces[i].Conversations {
			pairs = append(pairs, lumo.SpaceConversation{
				Space:        &spaces[i],
				Conversation: conv,
			})
		}
	}

	// Callbacks for scoped resolution.
	isSimple := func(s *lumo.Space) bool {
		return classifySpace(ctx, client, s) == "simple"
	}
	deriveDEK := func(s *lumo.Space) ([]byte, error) {
		return client.DeriveSpaceDEK(ctx, s)
	}

	// Resolve source conversation scoped by URI space component.
	var srcSpaceID string
	if srcURI.Space != "" {
		decryptName := func(s *lumo.Space) string {
			return decryptSpaceName(ctx, client, s)
		}
		srcSpace, serr := resolveSpace(spaces, srcURI.Space, decryptName)
		if serr != nil {
			return fmt.Errorf("resolve source space: %w", serr)
		}
		srcSpaceID = srcSpace.ID
	}

	resolved, err := resolveConversationScoped(pairs, srcURI.Path, srcSpaceID, isSimple, decryptConversationTitle, deriveDEK)
	if err != nil {
		return err
	}

	// Load source space and derive DEK for decrypting source messages.
	space, dek, err := resolveSpaceAndDEK(ctx, client, resolved.SpaceID)
	if err != nil {
		return err
	}

	// Fetch source conversation (includes shallow message list).
	srcConv, err := client.GetConversation(ctx, resolved.ConversationID)
	if err != nil {
		return fmt.Errorf("loading conversation: %w", err)
	}

	// Decrypt source title.
	srcTitle := decryptConversationTitle(*srcConv, dek, space.SpaceTag)

	// Resolve destination space, DEK, and title.
	dest, err := resolveDestination(ctx, client, spaces, destURI, srcTitle)
	if err != nil {
		return err
	}

	// Cascade-deletion warning for existing simple spaces with conversations.
	if !dest.IsNew && classifySpace(ctx, client, dest.Space) == "simple" && len(dest.Space.Conversations) > 0 {
		destName := decryptSpaceName(ctx, client, dest.Space)
		_, _ = fmt.Fprintf(os.Stderr, "warning: space %q already has a conversation; web-UI deletion will cascade to both\n", destName)
	}

	// Create conversation in destination space.
	newConv, err := client.CreateConversation(ctx, dest.Space, dest.Title)
	if err != nil {
		return fmt.Errorf("creating conversation: %w", err)
	}

	// Build a map of MessageID → MessageTag for parentTag resolution.
	idToTag := make(map[string]string, len(srcConv.Messages))
	for _, m := range srcConv.Messages {
		idToTag[m.ID] = m.MessageTag
	}

	// Copy messages in chronological order.
	copied := 0
	failed := 0
	for _, shallow := range srcConv.Messages {
		msg, ferr := client.GetMessage(ctx, shallow.ID)
		if ferr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to fetch message %s: %v\n", shallow.ID, ferr)
			failed++
			continue
		}

		// Determine role string for AD construction.
		role := "user"
		if msg.Role == lumo.WireRoleAssistant {
			role = "assistant"
		}

		// Resolve source parentTag.
		parentTag := ""
		if msg.ParentID != "" {
			parentTag = idToTag[msg.ParentID]
		}

		// Construct source AD and decrypt.
		srcAD := lumo.MessageAD(msg.MessageTag, role, parentTag, srcConv.ConversationTag)
		plainJSON, derr := lumo.DecryptString(msg.Encrypted, dek, srcAD)
		if derr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to decrypt message %s: %v\n", msg.ID, derr)
			failed++
			continue
		}

		// Generate fresh tag and construct target AD (flattened — no parent).
		freshTag, terr := lumo.GenerateTag()
		if terr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to generate tag for message %s: %v\n", msg.ID, terr)
			failed++
			continue
		}
		targetAD := lumo.MessageAD(freshTag, role, "", newConv.ConversationTag)

		// Re-encrypt under target AD using the destination space's DEK.
		encrypted, eerr := lumo.EncryptString(plainJSON, dest.DEK, targetAD)
		if eerr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to encrypt message %s: %v\n", msg.ID, eerr)
			failed++
			continue
		}

		// Create the message in the new conversation.
		req := lumo.CreateMessageReq{
			ConversationID: newConv.ID,
			MessageTag:     freshTag,
			Role:           msg.Role,
			Status:         msg.Status,
			Encrypted:      encrypted,
			CreateTime:     msg.CreateTime,
		}
		_, cerr := client.CreateRawMessage(ctx, req)
		if cerr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to create message: %v\n", cerr)
			failed++
			continue
		}

		copied++
	}

	// Print new conversation ID to stdout.
	fmt.Println(newConv.ID)

	// Print summary to stderr.
	if failed > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "Copied %d messages (%d failed)\n", copied, failed)
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "Copied %d messages\n", copied)
	}

	return nil
}
