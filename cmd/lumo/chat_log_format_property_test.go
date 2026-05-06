package lumoCmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/major0/proton-cli/api/lumo"
	"pgregory.net/rapid"
)

// --- Property 3: Role labeling ---

// TestFormatLogRoleLabels_Property verifies that for any non-empty
// sequence of messages with Role ∈ {1, 2}, FormatLog produces output
// where each user message block starts with "You: " and each assistant
// message block starts with "Lumo: ".
//
// Feature: lumo-chat-log, Property 3: Role labeling
//
// **Validates: Requirements 2.4, 2.5**
func TestFormatLogRoleLabels_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "num_messages")
		msgs := make([]lumo.Message, n)
		contents := make([]string, n)
		for i := range msgs {
			role := lumo.WireRoleUser
			if rapid.Bool().Draw(t, "is_assistant") {
				role = lumo.WireRoleAssistant
			}
			content := rapid.StringMatching(`[a-zA-Z0-9 ]{1,40}`).Draw(t, "content")
			msgs[i] = lumo.Message{
				ID:             rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "id"),
				ConversationID: "conv-1",
				MessageTag:     rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "tag"),
				Role:           role,
				CreateTime:     "2024-06-15T10:30:00Z",
			}
			contents[i] = content
		}

		decrypt := func(msg lumo.Message) (string, bool) {
			for i, m := range msgs {
				if m.ID == msg.ID {
					return contents[i], true
				}
			}
			return "", false
		}

		result, _ := FormatLog(msgs, LogFormatOptions{Color: false}, decrypt)
		blocks := strings.Split(result, "\n\n")

		if len(blocks) != n {
			t.Fatalf("expected %d blocks, got %d", n, len(blocks))
		}

		for i, block := range blocks {
			switch msgs[i].Role {
			case lumo.WireRoleUser:
				if !strings.HasPrefix(block, "You: ") {
					t.Fatalf("block %d: expected prefix 'You: ', got %q", i, block)
				}
			case lumo.WireRoleAssistant:
				if !strings.HasPrefix(block, "Lumo: ") {
					t.Fatalf("block %d: expected prefix 'Lumo: ', got %q", i, block)
				}
			}
		}
	})
}

// --- Property 4: Blank-line separation ---

// TestFormatLogBlankLines_Property verifies that for any sequence of
// 2+ messages, consecutive message blocks are separated by exactly one
// blank line (two consecutive newlines between the end of one message
// and the start of the next).
//
// Feature: lumo-chat-log, Property 4: Blank-line separation
//
// **Validates: Requirements 2.6**
func TestFormatLogBlankLines_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(2, 20).Draw(t, "num_messages")
		msgs := make([]lumo.Message, n)
		for i := range msgs {
			role := lumo.WireRoleUser
			if i%2 == 1 {
				role = lumo.WireRoleAssistant
			}
			msgs[i] = lumo.Message{
				ID:             rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "id"),
				ConversationID: "conv-1",
				MessageTag:     rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "tag"),
				Role:           role,
				CreateTime:     "2024-06-15T10:30:00Z",
			}
		}

		decrypt := func(_ lumo.Message) (string, bool) {
			return "hello world", true
		}

		result, _ := FormatLog(msgs, LogFormatOptions{}, decrypt)

		// Count "\n\n" separators — should be exactly n-1.
		separators := strings.Count(result, "\n\n")
		if separators != n-1 {
			t.Fatalf("expected %d blank-line separators, got %d", n-1, separators)
		}

		// Verify no triple newlines (which would mean extra blank lines).
		if strings.Contains(result, "\n\n\n") {
			t.Fatalf("output contains triple newline (extra blank line)")
		}
	})
}

// --- Property 5: Decryption failure placeholder ---

// TestFormatLogDecryptFailure_Property verifies that when decrypt
// returns ok=false for some subset of messages, FormatLog renders
// "[message decryption failed]" for each failed message and the
// returned failure count matches.
//
// Feature: lumo-chat-log, Property 5: Decryption failure placeholder
//
// **Validates: Requirements 2.7**
func TestFormatLogDecryptFailure_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "num_messages")
		msgs := make([]lumo.Message, n)
		failSet := make(map[string]bool)
		expectedFailures := 0

		for i := range msgs {
			id := rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "id")
			role := lumo.WireRoleUser
			if i%2 == 1 {
				role = lumo.WireRoleAssistant
			}
			msgs[i] = lumo.Message{
				ID:             id,
				ConversationID: "conv-1",
				MessageTag:     rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "tag"),
				Role:           role,
				CreateTime:     "2024-06-15T10:30:00Z",
			}
			if rapid.Bool().Draw(t, "fail_decrypt") {
				failSet[id] = true
				expectedFailures++
			}
		}

		decrypt := func(msg lumo.Message) (string, bool) {
			if failSet[msg.ID] {
				return "", false
			}
			return "decrypted content", true
		}

		result, failures := FormatLog(msgs, LogFormatOptions{}, decrypt)

		if failures != expectedFailures {
			t.Fatalf("expected %d failures, got %d", expectedFailures, failures)
		}

		// Count placeholder occurrences.
		placeholderCount := strings.Count(result, "[message decryption failed]")
		if placeholderCount != expectedFailures {
			t.Fatalf("expected %d placeholders, got %d", expectedFailures, placeholderCount)
		}
	})
}

// --- Property 6: Timestamp toggle ---

// TestFormatLogTimestamps_Property verifies that when Timestamps=true,
// each message block contains a timestamp pattern (YYYY-MM-DD HH:MM:SS)
// before the sender label; when Timestamps=false, no timestamp pattern
// appears.
//
// Feature: lumo-chat-log, Property 6: Timestamp toggle
//
// **Validates: Requirements 3.1, 3.2**
func TestFormatLogTimestamps_Property(t *testing.T) {
	tsPattern := regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)

	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "num_messages")
		msgs := make([]lumo.Message, n)
		for i := range msgs {
			role := lumo.WireRoleUser
			if i%2 == 1 {
				role = lumo.WireRoleAssistant
			}
			// Generate valid RFC3339 timestamps.
			hour := rapid.IntRange(0, 23).Draw(t, "hour")
			minute := rapid.IntRange(0, 59).Draw(t, "minute")
			second := rapid.IntRange(0, 59).Draw(t, "second")
			month := rapid.IntRange(1, 12).Draw(t, "month")
			day := rapid.IntRange(1, 28).Draw(t, "day")

			var createTime strings.Builder
			createTime.WriteString("2024-")
			if month < 10 {
				createTime.WriteByte('0')
			}
			writeInt(&createTime, month)
			createTime.WriteByte('-')
			if day < 10 {
				createTime.WriteByte('0')
			}
			writeInt(&createTime, day)
			createTime.WriteByte('T')
			if hour < 10 {
				createTime.WriteByte('0')
			}
			writeInt(&createTime, hour)
			createTime.WriteByte(':')
			if minute < 10 {
				createTime.WriteByte('0')
			}
			writeInt(&createTime, minute)
			createTime.WriteByte(':')
			if second < 10 {
				createTime.WriteByte('0')
			}
			writeInt(&createTime, second)
			createTime.WriteString("Z")

			msgs[i] = lumo.Message{
				ID:             rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "id"),
				ConversationID: "conv-1",
				MessageTag:     rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "tag"),
				Role:           role,
				CreateTime:     createTime.String(),
			}
		}

		decrypt := func(_ lumo.Message) (string, bool) {
			return "content", true
		}

		// With timestamps enabled.
		resultOn, _ := FormatLog(msgs, LogFormatOptions{Timestamps: true}, decrypt)
		blocksOn := strings.Split(resultOn, "\n\n")
		for i, block := range blocksOn {
			if !tsPattern.MatchString(block) {
				t.Fatalf("block %d: expected timestamp pattern with Timestamps=true, got %q", i, block)
			}
		}

		// With timestamps disabled.
		resultOff, _ := FormatLog(msgs, LogFormatOptions{Timestamps: false}, decrypt)
		if tsPattern.MatchString(resultOff) {
			t.Fatalf("output contains timestamp pattern with Timestamps=false: %q", resultOff)
		}
	})
}

// writeInt writes an integer (0–99) to a strings.Builder.
func writeInt(b *strings.Builder, n int) {
	if n >= 10 {
		b.WriteByte(byte('0' + n/10%10)) //nolint:gosec // n is bounded 0–99
	}
	b.WriteByte(byte('0' + n%10)) //nolint:gosec // n is bounded 0–99
}

// --- Property 7: Color-disabled produces no ANSI ---

// TestFormatLogNoColor_Property verifies that when Color=false, the
// output of FormatLog contains no ANSI escape sequences.
//
// Feature: lumo-chat-log, Property 7: Color-disabled produces no ANSI
//
// **Validates: Requirements 4.3, 6.5**
func TestFormatLogNoColor_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "num_messages")
		msgs := make([]lumo.Message, n)
		for i := range msgs {
			role := lumo.WireRoleUser
			if rapid.Bool().Draw(t, "is_assistant") {
				role = lumo.WireRoleAssistant
			}
			msgs[i] = lumo.Message{
				ID:             rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "id"),
				ConversationID: "conv-1",
				MessageTag:     rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "tag"),
				Role:           role,
				CreateTime:     "2024-06-15T10:30:00Z",
			}
		}

		decrypt := func(_ lumo.Message) (string, bool) {
			return rapid.StringMatching(`[a-zA-Z0-9 ]{1,40}`).Draw(t, "content"), true
		}

		result, _ := FormatLog(msgs, LogFormatOptions{Color: false}, decrypt)

		if strings.Contains(result, "\x1b[") {
			t.Fatalf("output contains ANSI escape with Color=false: %q", result)
		}
	})
}

// --- Property 8: Color-enabled wraps only sender names ---

// TestFormatLogColorSenderOnly_Property verifies that when Color=true,
// "Lumo" is wrapped in bright purple (\x1b[95m...\x1b[0m), "You" is
// wrapped in blue (\x1b[34m...\x1b[0m), and no ANSI codes appear in
// the content portions.
//
// Feature: lumo-chat-log, Property 8: Color-enabled wraps only sender names
//
// **Validates: Requirements 6.4, 6.6, 6.7, 6.8**
func TestFormatLogColorSenderOnly_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "num_messages")
		msgs := make([]lumo.Message, n)
		contents := make([]string, n)
		for i := range msgs {
			role := lumo.WireRoleUser
			if rapid.Bool().Draw(t, "is_assistant") {
				role = lumo.WireRoleAssistant
			}
			// Content without ANSI codes.
			content := rapid.StringMatching(`[a-zA-Z0-9 ]{1,40}`).Draw(t, "content")
			msgs[i] = lumo.Message{
				ID:             rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "id"),
				ConversationID: "conv-1",
				MessageTag:     rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "tag"),
				Role:           role,
				CreateTime:     "2024-06-15T10:30:00Z",
			}
			contents[i] = content
		}

		decrypt := func(msg lumo.Message) (string, bool) {
			for i, m := range msgs {
				if m.ID == msg.ID {
					return contents[i], true
				}
			}
			return "", false
		}

		result, _ := FormatLog(msgs, LogFormatOptions{Color: true}, decrypt)
		blocks := strings.Split(result, "\n\n")

		for i, block := range blocks {
			switch msgs[i].Role {
			case lumo.WireRoleAssistant:
				// Lumo should be wrapped in bright purple.
				coloredLumo := "\x1b[95mLumo\x1b[0m"
				if !strings.Contains(block, coloredLumo) {
					t.Fatalf("block %d: expected colored Lumo %q, got %q", i, coloredLumo, block)
				}
			case lumo.WireRoleUser:
				// You should be wrapped in blue.
				coloredYou := "\x1b[34mYou\x1b[0m"
				if !strings.Contains(block, coloredYou) {
					t.Fatalf("block %d: expected colored You %q, got %q", i, coloredYou, block)
				}
			}

			// Content portion should not contain ANSI codes.
			// Extract content after ": "
			colonIdx := strings.Index(block, ": ")
			if colonIdx == -1 {
				t.Fatalf("block %d: no ': ' separator found", i)
			}
			contentPart := block[colonIdx+2:]
			if strings.Contains(contentPart, "\x1b[") {
				t.Fatalf("block %d: content contains ANSI codes: %q", i, contentPart)
			}
		}
	})
}
