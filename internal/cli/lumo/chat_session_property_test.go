package lumoCmd

import (
	"testing"

	"pgregory.net/rapid"
)

// --- Property 2: Whitespace-only input is treated as empty ---

// TestWhitespaceIsEmpty_Property verifies that for any string composed
// entirely of whitespace characters (spaces, tabs, newlines), the input
// validation classifies it as empty, identical to a zero-length string.
//
// Feature: lumo-chat, Property 2: Whitespace-only input is treated as empty
//
// **Validates: Requirements 2.5**
func TestWhitespaceIsEmpty_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate whitespace-only strings of varying length.
		n := rapid.IntRange(0, 50).Draw(t, "len")
		ws := make([]byte, n)
		for i := range ws {
			ws[i] = []byte{' ', '\t', '\n', '\r'}[rapid.IntRange(0, 3).Draw(t, "char")]
		}
		input := string(ws)

		if !IsEmptyInput(input) {
			t.Fatalf("whitespace-only input %q not classified as empty", input)
		}
	})
}
