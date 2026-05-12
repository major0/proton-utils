package lumoCmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/major0/proton-cli/api/lumo"
	cli "github.com/major0/proton-cli/internal/cli"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var chatLogCmd = &cobra.Command{
	Use:   "log <id|title>",
	Short: "Print the full chat log of a conversation",
	Args:  cobra.ExactArgs(1),
	RunE:  runChatLog,
}

func init() {
	chatCmd.AddCommand(chatLogCmd)
	chatLogCmd.Flags().String("color", "auto", "Color output: always, auto, or never")
	chatLogCmd.Flags().Bool("no-pager", false, "Disable automatic paging")
	chatLogCmd.Flags().String("format", "", "Output format: json")

}

func runChatLog(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// Validate --color flag.
	colorFlag, _ := cmd.Flags().GetString("color")
	if colorFlag != "always" && colorFlag != "auto" && colorFlag != "never" {
		return fmt.Errorf("invalid --color value %q: must be always, auto, or never", colorFlag)
	}

	client, err := restoreClient(cmd)
	if err != nil {
		return err
	}

	resolved, err := resolveConversationByInput(ctx, client, args[0])
	if err != nil {
		return err
	}

	_, dek, err := resolveSpaceAndDEK(ctx, client, resolved.SpaceID)
	if err != nil {
		return err
	}

	conv, err := client.GetConversation(ctx, resolved.ConversationID)
	if err != nil {
		return fmt.Errorf("loading conversation: %w", err)
	}

	// Get the shallow message list (no Encrypted field).
	shallow := conv.Messages
	if len(shallow) == 0 {
		return nil
	}

	// JSON mode: dump all message fields (public + decrypted private).
	formatFlag, _ := cmd.Flags().GetString("format")
	if formatFlag == "json" {
		return runChatLogJSON(cmd, client, conv, dek)
	}

	// Resolve color.
	isTTY := term.IsTerminal(int(os.Stdout.Fd())) //nolint:gosec // fd conversion is safe
	var useColor bool
	switch colorFlag {
	case "always":
		useColor = true
	case "never":
		useColor = false
	default: // "auto"
		useColor = isTTY
	}

	opts := LogFormatOptions{
		Color: useColor,
	}

	noPager, _ := cmd.Flags().GetBool("no-pager")

	// Set up output writer (pager or direct stdout).
	w, cleanup := setupOutput(noPager)
	defer cleanup()

	// Fetch, decrypt, and write each message as we go.
	failures := 0
	fetched := make([]lumo.Message, 0, len(shallow))
	for _, s := range shallow {
		msg, ferr := client.GetMessage(ctx, s.ID)
		if ferr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to fetch message %s: %v\n", s.ID, ferr)
			failures++
			continue
		}

		fetched = append(fetched, *msg)
		content := decryptMessageContent(*msg, dek, conv.ConversationTag, fetched)
		if content == "" && msg.Encrypted != "" {
			content = "[message decryption failed]"
			failures++
		}

		// Strip leading whitespace/newlines from assistant messages.
		if msg.Role == lumo.WireRoleAssistant {
			content = strings.TrimLeft(content, " \t\n\r")
		}

		if werr := writeMessage(w, *msg, content, opts); werr != nil {
			return nil // pipe closed, stop silently
		}
	}

	if failures > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "%d message(s) could not be decrypted\n", failures)
	}

	return nil
}

// writeMessage writes a single formatted message to the writer.
// Returns an error if the write fails (e.g., broken pipe).
// Format:
//
//	<sender> <date> <time>
//	────────────────────────
//	<message body>
func writeMessage(w io.Writer, msg lumo.Message, content string, opts LogFormatOptions) error {
	// Determine sender label.
	var label string
	switch msg.Role {
	case lumo.WireRoleUser:
		label = "You"
	case lumo.WireRoleAssistant:
		label = "Lumo"
	default:
		label = "?"
	}

	// Apply color to sender label and wrap in angle brackets.
	if opts.Color {
		switch msg.Role {
		case lumo.WireRoleUser:
			label = "<\x1b[34m" + label + "\x1b[0m>"
		case lumo.WireRoleAssistant:
			label = "<\x1b[95m" + label + "\x1b[0m>"
		default:
			label = "<" + label + ">"
		}
	} else {
		label = "<" + label + ">"
	}

	// Calculate plain-text header width (without ANSI codes).
	var plainLabel string
	switch msg.Role {
	case lumo.WireRoleUser:
		plainLabel = "<You>"
	case lumo.WireRoleAssistant:
		plainLabel = "<Lumo>"
	default:
		plainLabel = "<?>"
	}
	ts := cli.FormatISO(msg.CreateTime)
	headerWidth := len(plainLabel) + 1 + len(ts)

	if _, err := fmt.Fprintf(w, "%s %s\n", label, ts); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s\n", strings.Repeat("─", headerWidth)); err != nil {
		return err
	}

	// Body: message content (may be multi-line).
	if _, err := fmt.Fprint(w, content); err != nil {
		return err
	}

	// Ensure content ends with a newline, then add two blank lines after.
	if len(content) == 0 || content[len(content)-1] != '\n' {
		if _, err := fmt.Fprint(w, "\n"); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(w, "\n\n")
	return err
}

// setupOutput returns a writer and cleanup function. If stdout is a
// terminal and noPager is false, it spawns a pager. Otherwise it
// returns stdout directly.
func setupOutput(noPager bool) (io.Writer, func()) {
	if !term.IsTerminal(int(os.Stdout.Fd())) || noPager { //nolint:gosec // fd conversion is safe
		return os.Stdout, func() {}
	}

	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = "less"
	}

	// When using less, ensure raw ANSI passthrough.
	if pager == "less" || strings.Contains(pager, "less") {
		if env := os.Getenv("LESS"); env == "" {
			_ = os.Setenv("LESS", "-R")
		} else if !strings.Contains(env, "R") {
			_ = os.Setenv("LESS", env+"R")
		}
	}

	cmd := exec.Command(pager) //nolint:gosec // pager is user-controlled via $PAGER
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	pipe, err := cmd.StdinPipe()
	if err != nil {
		return os.Stdout, func() {}
	}

	if err := cmd.Start(); err != nil {
		return os.Stdout, func() {}
	}

	cleanup := func() {
		_ = pipe.Close()
		_ = cmd.Wait()
	}
	return pipe, cleanup
}

// jsonMessage is the JSON output structure for --json mode.
// It includes all public fields from the API plus the decrypted payload.
type jsonMessage struct {
	ID             string          `json:"id"`
	ConversationID string          `json:"conversationId"`
	MessageTag     string          `json:"messageTag"`
	Role           int             `json:"role"`
	Status         int             `json:"status"`
	CreateTime     string          `json:"createTime"`
	ParentID       string          `json:"parentId,omitempty"`
	Encrypted      string          `json:"encrypted,omitempty"`
	Decrypted      json.RawMessage `json:"decrypted,omitempty"`
	DecryptError   string          `json:"decryptError,omitempty"`
}

// runChatLogJSON outputs all messages as a JSON array with both public
// fields and decrypted content for debugging.
func runChatLogJSON(cmd *cobra.Command, client *lumo.Client, conv *lumo.Conversation, dek []byte) error {
	ctx := cmd.Context()

	fetched := make([]lumo.Message, 0, len(conv.Messages))
	var out []jsonMessage

	for _, s := range conv.Messages {
		msg, ferr := client.GetMessage(ctx, s.ID)
		if ferr != nil {
			out = append(out, jsonMessage{
				ID:             s.ID,
				ConversationID: s.ConversationID,
				MessageTag:     s.MessageTag,
				Role:           s.Role,
				CreateTime:     s.CreateTime,
				ParentID:       s.ParentID,
				DecryptError:   fmt.Sprintf("fetch failed: %v", ferr),
			})
			continue
		}

		fetched = append(fetched, *msg)

		jm := jsonMessage{
			ID:             msg.ID,
			ConversationID: msg.ConversationID,
			MessageTag:     msg.MessageTag,
			Role:           msg.Role,
			Status:         msg.Status,
			CreateTime:     msg.CreateTime,
			ParentID:       msg.ParentID,
			Encrypted:      msg.Encrypted,
		}

		// Attempt decryption.
		if msg.Encrypted != "" {
			role := "user"
			if msg.Role == lumo.WireRoleAssistant {
				role = "assistant"
			}

			parentTag := ""
			if msg.ParentID != "" {
				for _, m := range fetched {
					if m.ID == msg.ParentID {
						parentTag = m.MessageTag
						break
					}
				}
			}

			ad := lumo.MessageAD(msg.MessageTag, role, parentTag, conv.ConversationTag)
			plainJSON, err := lumo.DecryptString(msg.Encrypted, dek, ad)
			if err != nil {
				// Fallback: try with raw ParentID.
				ad = lumo.MessageAD(msg.MessageTag, role, msg.ParentID, conv.ConversationTag)
				plainJSON, err = lumo.DecryptString(msg.Encrypted, dek, ad)
			}
			if err != nil {
				jm.DecryptError = err.Error()
			} else {
				jm.Decrypted = json.RawMessage(plainJSON)
			}
		}

		out = append(out, jm)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
