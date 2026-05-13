package lumoCmd

import (
	"fmt"
	"sort"
	"strings"

	cli "github.com/major0/proton-utils/internal/cli"
	"github.com/major0/proton-utils/api/lumo"
)

// ConversationRow is a display-only struct for the conversation list table.
// Title is decrypted on demand and discarded after rendering.
type ConversationRow struct {
	ID         string
	Title      string
	CreateTime string
}

// FormatStatusBar renders a status bar line:
//
//	── conv:<truncID> | model:<model> ──
//
// The conversation ID is truncated to 8 characters. The result is
// padded with horizontal-rule characters to fill the given width.
func FormatStatusBar(convID, model string, width int) string {
	truncID := convID
	if len(truncID) > 8 {
		truncID = truncID[:8]
	}

	inner := fmt.Sprintf(" conv:%s | model:%s ", truncID, model)
	remaining := width - len(inner)
	if remaining <= 0 {
		return inner[:width]
	}

	left := remaining / 2
	right := remaining - left
	return strings.Repeat("─", left) + inner + strings.Repeat("─", right)
}

// FormatHistory renders prior messages for chat resume. Each message is
// prefixed with a role label (You: / Lumo:). The decrypt callback
// decrypts each message's content on demand — the formatter never sees
// keys.
func FormatHistory(messages []lumo.Message, decrypt func(lumo.Message) string) string {
	if len(messages) == 0 {
		return ""
	}

	var b strings.Builder
	for _, msg := range messages {
		content := decrypt(msg)
		switch msg.Role {
		case lumo.WireRoleUser:
			b.WriteString("You: ")
		case lumo.WireRoleAssistant:
			b.WriteString("Lumo: ")
		default:
			b.WriteString("?: ")
		}
		b.WriteString(content)
		b.WriteByte('\n')
	}
	return b.String()
}

// FormatConversationList renders a tab-aligned table of conversations.
// Rows are sorted by creation time descending (newest first). Empty
// titles are replaced with "Untitled".
func FormatConversationList(rows []ConversationRow) string {
	if len(rows) == 0 {
		return "No conversations found.\n"
	}

	// Sort newest first (lexicographic descending on CreateTime).
	sorted := make([]ConversationRow, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreateTime > sorted[j].CreateTime
	})

	// Compute ID column width from the longest ID in the set.
	idWidth := 2 // minimum "ID" header
	for _, r := range sorted {
		if len(r.ID) > idWidth {
			idWidth = len(r.ID)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-*s  %-19s  %s\n", idWidth, "ID", "CREATED", "TITLE")
	for _, r := range sorted {
		title := r.Title
		if title == "" {
			title = "Untitled"
		}
		fmt.Fprintf(&b, "%-*s  %-19s  %s\n", idWidth, r.ID, cli.FormatISO(r.CreateTime), title)
	}
	return b.String()
}

// FilterActiveConversations returns only conversations with an empty
// DeleteTime (i.e., not soft-deleted).
func FilterActiveConversations(convs []lumo.Conversation) []lumo.Conversation {
	active := make([]lumo.Conversation, 0, len(convs))
	for _, c := range convs {
		if c.DeleteTime == "" {
			active = append(active, c)
		}
	}
	return active
}
