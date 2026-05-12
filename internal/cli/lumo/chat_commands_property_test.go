package lumoCmd

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// --- Property 5: Slash command parsing round-trip ---

// TestParseSlashCommand_Property verifies that for any string starting
// with / followed by a non-empty alphabetic command name,
// ParseSlashCommand returns ok=true with cmd equal to the first word
// after /. For any string not starting with /, it returns ok=false.
//
// Feature: lumo-chat, Property 5: Slash command parsing round-trip
//
// **Validates: Requirements 2.7, 2.8**
func TestParseSlashCommand_Property(t *testing.T) {
	t.Run("slash_input_returns_ok", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			cmdName := rapid.StringMatching(`[a-z]{1,10}`).Draw(t, "cmd")
			args := rapid.StringMatching(`[a-zA-Z0-9 ]{0,20}`).Draw(t, "args")

			var input string
			if strings.TrimSpace(args) != "" {
				input = "/" + cmdName + " " + args
			} else {
				input = "/" + cmdName
			}

			cmd, _, ok := ParseSlashCommand(input)
			if !ok {
				t.Fatalf("expected ok=true for slash input %q", input)
			}
			if cmd != cmdName {
				t.Fatalf("expected cmd=%q, got %q for input %q", cmdName, cmd, input)
			}
		})
	})

	t.Run("non_slash_input_returns_not_ok", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			// Generate input that doesn't start with /
			input := rapid.StringMatching(`[a-zA-Z0-9 ]{1,30}`).Draw(t, "input")

			_, _, ok := ParseSlashCommand(input)
			if ok {
				t.Fatalf("expected ok=false for non-slash input %q", input)
			}
		})
	})
}
