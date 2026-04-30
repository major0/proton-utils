package lumoCmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/major0/proton-cli/api/lumo"
	lumoClient "github.com/major0/proton-cli/api/lumo/client"
	"github.com/major0/proton-cli/api/shortid"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var chatSpaceFlag string

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Interactive chat with Proton Lumo",
	Run: func(cmd *cobra.Command, _ []string) {
		_ = cmd.Help()
	},
}

func init() {
	AddCommand(chatCmd)
	chatCmd.PersistentFlags().StringVar(&chatSpaceFlag, "space", "", "Space ID (defaults to simple space)")
}

// resolveSpace returns the space ID from the --space flag or the default space.
func resolveSpace(ctx context.Context, client *lumoClient.Client) (string, error) {
	if chatSpaceFlag != "" {
		return chatSpaceFlag, nil
	}
	space, err := client.GetDefaultSpace(ctx)
	if err != nil {
		return "", fmt.Errorf("resolving default space: %w", err)
	}
	return space.ID, nil
}

// --- chat create ---

var chatCreateCmd = &cobra.Command{
	Use:   "create <title>",
	Short: "Create a new conversation and enter interactive chat",
	Args:  cobra.ExactArgs(1),
	RunE:  runChatCreate,
}

func init() {
	chatCmd.AddCommand(chatCreateCmd)
}

func runChatCreate(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	title := args[0]

	client, err := restoreClient(cmd)
	if err != nil {
		return err
	}

	// Create a new space for this conversation (matches browser flow:
	// each chat gets its own space).
	space, err := client.CreateSpace(ctx, title, false)
	if err != nil {
		return fmt.Errorf("creating space: %w", err)
	}

	conv, err := client.CreateConversation(ctx, space, title)
	if err != nil {
		return fmt.Errorf("creating conversation: %w", err)
	}

	session := &ChatSession{
		Client:       client,
		Space:        space,
		Conversation: conv,
		SpaceID:      space.ID,
		Writer:       os.Stdout,
		Reader:       os.Stdin,
	}

	return session.Run(ctx)
}

// --- chat resume ---

var chatResumeCmd = &cobra.Command{
	Use:   "resume <conversation-id>",
	Short: "Resume an existing conversation",
	Args:  cobra.ExactArgs(1),
	RunE:  runChatResume,
}

func init() {
	chatCmd.AddCommand(chatResumeCmd)
}

func runChatResume(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, err := restoreClient(cmd)
	if err != nil {
		return err
	}

	convID, err := resolveConversationID(ctx, client, args[0])
	if err != nil {
		return err
	}

	conv, err := client.GetConversation(ctx, convID)
	if err != nil {
		return fmt.Errorf("loading conversation: %w", err)
	}

	messages, err := client.ListMessages(ctx, convID)
	if err != nil {
		return fmt.Errorf("loading messages: %w", err)
	}

	space, dek, err := resolveSpaceAndDEK(ctx, client, conv.SpaceID)
	if err != nil {
		return fmt.Errorf("loading space: %w", err)
	}

	decrypt := func(msg lumo.Message) string {
		return decryptMessageContent(msg, dek, conv.ConversationTag)
	}

	// Build turns from history.
	var turns []lumo.Turn
	for _, msg := range messages {
		content := decrypt(msg)
		role := lumo.RoleUser
		if msg.Role == lumoClient.RoleAssistant {
			role = lumo.RoleAssistant
		}
		turns = append(turns, lumo.Turn{Role: role, Content: content})
	}

	// Print history.
	if history := FormatHistory(messages, decrypt); history != "" {
		_, _ = fmt.Fprint(os.Stdout, history)
	}

	session := &ChatSession{
		Client:       client,
		Space:        space,
		Conversation: conv,
		SpaceID:      conv.SpaceID,
		Turns:        turns,
		Writer:       os.Stdout,
		Reader:       os.Stdin,
	}

	return session.Run(ctx)
}

// decryptMessageContent decrypts a message's encrypted content, returning
// the plaintext. Returns an empty string on decryption failure.
func decryptMessageContent(msg lumo.Message, dek []byte, convTag string) string {
	if msg.Encrypted == "" {
		return ""
	}

	role := "user"
	if msg.Role == lumoClient.RoleAssistant {
		role = "assistant"
	}

	ad := lumo.MessageAD(msg.MessageTag, role, msg.ParentID, convTag)
	plainJSON, err := lumo.DecryptString(msg.Encrypted, dek, ad)
	if err != nil {
		return ""
	}

	var priv struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(plainJSON), &priv); err != nil {
		return ""
	}
	return priv.Content
}

// --- chat list ---

var chatShowAll bool
var chatShowProject bool

var chatListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List conversations (simple chats; --project for projects; -A for all)",
	RunE:    runChatList,
}

func init() {
	chatCmd.AddCommand(chatListCmd)
	chatListCmd.Flags().BoolVarP(&chatShowAll, "all", "A", false, "Include all conversations")
	chatListCmd.Flags().BoolVar(&chatShowProject, "project", false, "Show project conversations only")
}

func runChatList(cmd *cobra.Command, _ []string) error {
	rc := cli.GetContext(cmd)
	ctx := cmd.Context()
	client, err := restoreClient(cmd)
	if err != nil {
		return err
	}

	verbose := rc.Verbose >= 1

	// When --space is set, list conversations in that space only.
	// Otherwise, list all conversations across all spaces.
	if chatSpaceFlag != "" {
		return runChatListSpace(ctx, client, chatSpaceFlag, verbose)
	}
	return runChatListAll(ctx, client, verbose)
}

// runChatListSpace lists conversations in a single space.
func runChatListSpace(ctx context.Context, client *lumoClient.Client, spaceID string, verbose bool) error {
	convs, err := client.ListConversations(ctx, spaceID)
	if err != nil {
		return fmt.Errorf("listing conversations: %w", err)
	}

	active := FilterActiveConversations(convs)

	space, dek, err := resolveSpaceAndDEK(ctx, client, spaceID)
	if err != nil {
		return fmt.Errorf("loading space: %w", err)
	}

	// Compute short conversation IDs.
	short := map[string]string{}
	if !verbose {
		ids := make([]string, len(active))
		for i, c := range active {
			ids[i] = c.ID
		}
		short = shortid.Format(ids)
	}

	rows := make([]ConversationRow, len(active))
	for i, c := range active {
		displayID := c.ID
		if s, ok := short[c.ID]; ok {
			displayID = s
		}
		rows[i] = ConversationRow{
			ID:         displayID,
			Title:      decryptConversationTitle(c, dek, space.SpaceTag),
			CreateTime: c.CreateTime,
		}
	}

	_, _ = fmt.Fprint(os.Stdout, FormatConversationList(rows))
	return nil
}

// runChatListAll lists conversations across all simple (non-project) spaces.
func runChatListAll(ctx context.Context, client *lumoClient.Client, verbose bool) error {
	pairs, err := client.ListAllConversations(ctx)
	if err != nil {
		return fmt.Errorf("listing conversations: %w", err)
	}

	// Build rows, skipping project spaces and decrypting titles per-space.
	dekCache := map[string][]byte{}  // spaceID → DEK
	typeCache := map[string]string{} // spaceID → "simple"/"project"/"unknown"
	var rows []ConversationRow
	for _, p := range pairs {
		conv := p.Conversation
		if conv.DeleteTime != "" || p.Space.DeleteTime != "" {
			continue
		}

		// Skip project space conversations unless -A or --project is set.
		// Skip simple space conversations when --project is set.
		stype, ok := typeCache[p.Space.ID]
		if !ok {
			stype = classifySpace(ctx, client, p.Space)
			typeCache[p.Space.ID] = stype
		}
		if !chatShowAll {
			if chatShowProject && stype != "project" {
				continue
			}
			if !chatShowProject && stype == "project" {
				continue
			}
		}

		dek, ok := dekCache[p.Space.ID]
		if !ok {
			d, err := client.DeriveSpaceDEK(ctx, p.Space)
			if err != nil {
				continue
			}
			dek = d
			dekCache[p.Space.ID] = dek
		}

		rows = append(rows, ConversationRow{
			ID:         conv.ID,
			Title:      decryptConversationTitle(conv, dek, p.Space.SpaceTag),
			CreateTime: conv.CreateTime,
		})
	}

	// Apply short IDs to rows.
	if !verbose && len(rows) > 0 {
		ids := make([]string, len(rows))
		for i := range rows {
			ids[i] = rows[i].ID
		}
		short := shortid.Format(ids)
		for i := range rows {
			if s, ok := short[rows[i].ID]; ok {
				rows[i].ID = s
			}
		}
	}

	_, _ = fmt.Fprint(os.Stdout, FormatConversationList(rows))
	return nil
}

// decryptConversationTitle decrypts a conversation's encrypted title.
// Returns an empty string on failure (FormatConversationList will show
// "Untitled").
func decryptConversationTitle(conv lumo.Conversation, dek []byte, spaceTag string) string {
	if conv.Encrypted == "" {
		return ""
	}

	ad := lumo.ConversationAD(conv.ConversationTag, spaceTag)
	plainJSON, err := lumo.DecryptString(conv.Encrypted, dek, ad)
	if err != nil {
		return ""
	}

	var priv struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(plainJSON), &priv); err != nil {
		return ""
	}
	return priv.Title
}

// --- chat delete ---

var chatDeleteCmd = &cobra.Command{
	Use:   "delete <conversation-id>",
	Short: "Delete a conversation",
	Args:  cobra.ExactArgs(1),
	RunE:  runChatDelete,
}

func init() {
	chatCmd.AddCommand(chatDeleteCmd)
}

func runChatDelete(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, err := restoreClient(cmd)
	if err != nil {
		return err
	}

	// Find the parent space so we can clean it up if it's a simple 1:1 space.
	spaces, _ := client.ListSpaces(ctx)

	// Resolve short conversation ID against all conversations.
	var allConvIDs []string
	for _, s := range spaces {
		for _, c := range s.Conversations {
			allConvIDs = append(allConvIDs, c.ID)
		}
	}
	convID, err := shortid.Resolve(allConvIDs, args[0])
	if err != nil {
		return fmt.Errorf("resolving conversation: %w", err)
	}

	var parentSpace *lumo.Space
	for i := range spaces {
		for _, c := range spaces[i].Conversations {
			if c.ID == convID {
				parentSpace = &spaces[i]
				break
			}
		}
		if parentSpace != nil {
			break
		}
	}

	if err := client.DeleteConversation(ctx, convID); err != nil {
		return fmt.Errorf("deleting conversation: %w", err)
	}
	_, _ = fmt.Fprintf(os.Stderr, "Conversation %s deleted.\n", convID)

	// If the parent space is a simple space with only this conversation,
	// delete the space too — no point leaving an empty container.
	if parentSpace != nil && len(parentSpace.Conversations) <= 1 {
		if err := client.DeleteSpace(ctx, parentSpace.ID); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: failed to delete parent space: %v\n", err)
		} else {
			_, _ = fmt.Fprintf(os.Stderr, "Space %s deleted.\n", parentSpace.ID)
		}
	}

	return nil
}

// resolveConversationID resolves a short or full conversation ID against
// all conversations across all spaces.
func resolveConversationID(ctx context.Context, client *lumoClient.Client, input string) (string, error) {
	pairs, err := client.ListAllConversations(ctx)
	if err != nil {
		return "", fmt.Errorf("loading conversations: %w", err)
	}

	ids := make([]string, len(pairs))
	for i, p := range pairs {
		ids[i] = p.Conversation.ID
	}

	resolved, err := shortid.Resolve(ids, input)
	if err != nil {
		return "", fmt.Errorf("resolving conversation: %w", err)
	}
	return resolved, nil
}
