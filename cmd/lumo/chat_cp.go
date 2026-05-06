package lumoCmd

import (
	"fmt"
	"os"

	"github.com/major0/proton-cli/api/lumo"
	"github.com/spf13/cobra"
)

var chatCpCmd = &cobra.Command{
	Use:     "cp <id|title>",
	Aliases: []string{"copy"},
	Short:   "Duplicate a conversation within the same space",
	RunE:    runChatCp,
}

func init() {
	chatCmd.AddCommand(chatCpCmd)
}

func runChatCp(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true

	if len(args) == 0 {
		return fmt.Errorf("requires a conversation identifier (ID or title)")
	}

	ctx := cmd.Context()

	client, err := restoreClient(cmd)
	if err != nil {
		return err
	}

	// Resolve source conversation.
	resolved, err := resolveConversationByInput(ctx, client, args[0])
	if err != nil {
		return err
	}

	// Load space and derive DEK for decrypting source messages.
	space, dek, err := resolveSpaceAndDEK(ctx, client, resolved.SpaceID)
	if err != nil {
		return err
	}

	// Fetch source conversation (includes shallow message list).
	srcConv, err := client.GetConversation(ctx, resolved.ConversationID)
	if err != nil {
		return fmt.Errorf("loading conversation: %w", err)
	}

	// Decrypt source title and create new title with " (copy)" suffix.
	srcTitle := decryptConversationTitle(*srcConv, dek, space.SpaceTag)
	newTitle := srcTitle + " (copy)"

	// Create a new space for the copy. Simple spaces have a 1:1
	// relationship with conversations in the web client — placing the
	// copy in the same space would cause cascade-deletion of both when
	// either is deleted.
	newSpace, err := client.CreateSpace(ctx, "", false)
	if err != nil {
		return fmt.Errorf("creating space for copy: %w", err)
	}

	// Derive DEK for the new space (needed for encrypting the copy).
	newDEK, err := client.DeriveSpaceDEK(ctx, newSpace)
	if err != nil {
		return fmt.Errorf("deriving DEK for new space: %w", err)
	}

	newConv, err := client.CreateConversation(ctx, newSpace, newTitle)
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
		freshTag := lumo.GenerateTag()
		targetAD := lumo.MessageAD(freshTag, role, "", newConv.ConversationTag)

		// Re-encrypt under target AD using the new space's DEK.
		encrypted, eerr := lumo.EncryptString(plainJSON, newDEK, targetAD)
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
