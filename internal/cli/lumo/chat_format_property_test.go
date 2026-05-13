package lumoCmd

import (
	"strings"
	"testing"

	"github.com/major0/proton-utils/api/lumo"
	cli "github.com/major0/proton-utils/internal/cli"
	"pgregory.net/rapid"
)

// --- Property 1: Status bar contains required components and respects width ---

// TestFormatStatusBar_Property verifies that for any conversation ID
// (1–64 chars), model name (1–32 chars), and terminal width (≥ 20),
// FormatStatusBar produces a string that contains the truncated
// conversation ID (first 8 chars or full ID if shorter), the model
// name, and whose display length equals the given width.
//
// Feature: lumo-chat, Property 1: Status bar contains required components and respects width
//
// **Validates: Requirements 2.2**
func TestFormatStatusBar_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		convID := rapid.StringMatching(`[a-zA-Z0-9]{1,64}`).Draw(t, "conv_id")
		model := rapid.StringMatching(`[a-zA-Z0-9]{1,32}`).Draw(t, "model")

		// Compute minimum width needed to display the full inner string.
		truncID := convID
		if len(truncID) > 8 {
			truncID = truncID[:8]
		}
		minInner := len(" conv:") + len(truncID) + len(" | model:") + len(model) + len(" ")
		// Width must be at least minInner + 2 (for at least one ─ on each side).
		minWidth := minInner + 2
		if minWidth < 20 {
			minWidth = 20
		}
		width := rapid.IntRange(minWidth, minWidth+100).Draw(t, "width")

		result := FormatStatusBar(convID, model, width)

		if !strings.Contains(result, truncID) {
			t.Fatalf("status bar missing truncated ID %q: %q", truncID, result)
		}

		if !strings.Contains(result, model) {
			t.Fatalf("status bar missing model %q: %q", model, result)
		}

		// Display length must equal width.
		displayLen := 0
		for range result {
			displayLen++
		}
		if displayLen != width {
			t.Fatalf("status bar display length %d != width %d: %q", displayLen, width, result)
		}
	})
}

// --- Property 3: Conversation list output contains all fields and is sorted ---

// TestFormatConversationList_Property verifies that for any non-empty
// slice of ConversationRow values, FormatConversationList produces
// output where each row's ID, title (or "Untitled"), and creation time
// appear, and rows are ordered by creation time descending.
//
// Feature: lumo-chat, Property 3: Conversation list output contains all fields and is sorted
//
// **Validates: Requirements 5.1, 5.2**
func TestFormatConversationList_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "num_rows")
		rows := make([]ConversationRow, n)
		for i := range rows {
			rows[i] = ConversationRow{
				ID:         rapid.StringMatching(`[a-zA-Z0-9]{8,16}`).Draw(t, "id"),
				Title:      rapid.StringMatching(`[a-zA-Z0-9 ]{0,20}`).Draw(t, "title"),
				CreateTime: rapid.StringMatching(`2024-0[1-9]-[0][1-9]T[01][0-9]:[0-5][0-9]:[0-5][0-9]Z`).Draw(t, "create_time"),
			}
		}

		result := FormatConversationList(rows)

		// Each row's ID and creation time must appear (formatted to local time).
		for _, r := range rows {
			if !strings.Contains(result, r.ID) {
				t.Fatalf("output missing ID %q", r.ID)
			}
			formatted := cli.FormatISO(r.CreateTime)
			if !strings.Contains(result, formatted) {
				t.Fatalf("output missing formatted CreateTime %q (from %q)", formatted, r.CreateTime)
			}
			title := r.Title
			if title == "" {
				title = "Untitled"
			}
			if !strings.Contains(result, title) {
				t.Fatalf("output missing title %q", title)
			}
		}

		// Verify sort order: extract lines, skip header, check CreateTime descending.
		lines := strings.Split(strings.TrimSpace(result), "\n")
		if len(lines) < 2 {
			t.Fatalf("expected header + data lines, got %d lines", len(lines))
		}
		dataLines := lines[1:]

		// Collect CreateTimes from output order by matching IDs.
		type indexedRow struct {
			createTime string
			lineIdx    int
		}
		var ordered []indexedRow
		for li, line := range dataLines {
			for _, r := range rows {
				if strings.Contains(line, r.ID) {
					ordered = append(ordered, indexedRow{r.CreateTime, li})
					break
				}
			}
		}
		for i := 1; i < len(ordered); i++ {
			if ordered[i-1].createTime < ordered[i].createTime {
				t.Fatalf("rows not sorted newest-first: %q before %q",
					ordered[i-1].createTime, ordered[i].createTime)
			}
		}
	})
}

// --- Property 4: Deleted conversations are excluded from list ---

// TestFilterDeletedConversations_Property verifies that filtering out
// entries with non-empty DeleteTime produces a result containing only
// conversations with empty DeleteTime.
//
// Feature: lumo-chat, Property 4: Deleted conversations are excluded from list
//
// **Validates: Requirements 5.3**
func TestFilterDeletedConversations_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "num_convs")
		convs := make([]lumo.Conversation, n)
		for i := range convs {
			convs[i] = lumo.Conversation{
				ID:              rapid.StringMatching(`[a-zA-Z0-9]{8,16}`).Draw(t, "id"),
				SpaceID:         rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "space_id"),
				ConversationTag: rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "tag"),
				CreateTime:      rapid.StringMatching(`2024-0[1-9]-[0][1-9]T[01][0-9]:[0-5][0-9]:[0-5][0-9]Z`).Draw(t, "create_time"),
			}
			// Randomly mark some as deleted.
			if rapid.Bool().Draw(t, "deleted") {
				convs[i].DeleteTime = rapid.StringMatching(`2024-0[1-9]-[0][1-9]T[01][0-9]:[0-5][0-9]:[0-5][0-9]Z`).Draw(t, "delete_time")
			}
		}

		active := FilterActiveConversations(convs)

		// All returned conversations must have empty DeleteTime.
		for _, c := range active {
			if c.DeleteTime != "" {
				t.Fatalf("active list contains deleted conversation %s (DeleteTime=%s)", c.ID, c.DeleteTime)
			}
		}

		// No active conversation from the original should be missing.
		activeIDs := make(map[string]bool, len(active))
		for _, c := range active {
			activeIDs[c.ID] = true
		}
		for _, c := range convs {
			if c.DeleteTime == "" && !activeIDs[c.ID] {
				t.Fatalf("active conversation %s missing from filtered result", c.ID)
			}
		}
	})
}

// --- Property 6: History formatting contains role labels and content ---

// TestFormatHistory_Property verifies that for any non-empty slice of
// messages with user/assistant roles, FormatHistory produces output
// containing the role label and decrypted content for every message.
//
// Feature: lumo-chat, Property 6: History formatting contains role labels and content
//
// **Validates: Requirements 3.4**
func TestFormatHistory_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "num_messages")
		msgs := make([]lumo.Message, n)
		contents := make([]string, n)
		for i := range msgs {
			role := lumo.WireRoleUser
			if i%2 == 1 {
				role = lumo.WireRoleAssistant
			}
			content := rapid.StringMatching(`[a-zA-Z0-9 ]{1,40}`).Draw(t, "content")
			msgs[i] = lumo.Message{
				ID:             rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "id"),
				ConversationID: rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "conv_id"),
				MessageTag:     rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "tag"),
				Role:           role,
				CreateTime:     rapid.StringMatching(`2024-0[1-9]-[0][1-9]T[01][0-9]:[0-5][0-9]:[0-5][0-9]Z`).Draw(t, "create_time"),
			}
			contents[i] = content
		}

		// Decrypt callback returns the pre-generated content.
		decrypt := func(msg lumo.Message) string {
			for i, m := range msgs {
				if m.ID == msg.ID {
					return contents[i]
				}
			}
			return ""
		}

		result := FormatHistory(msgs, decrypt)

		// Every message's content and role label must appear.
		for i, msg := range msgs {
			if !strings.Contains(result, contents[i]) {
				t.Fatalf("history missing content %q for message %s", contents[i], msg.ID)
			}
			var label string
			switch msg.Role {
			case lumo.WireRoleUser:
				label = "You:"
			case lumo.WireRoleAssistant:
				label = "Lumo:"
			}
			if !strings.Contains(result, label) {
				t.Fatalf("history missing role label %q", label)
			}
		}
	})
}
