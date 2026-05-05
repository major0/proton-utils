package drive

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// TestPropertyValidateShareName verifies that ValidateShareName rejects a
// string if and only if it is empty, contains a "/" character, or exceeds
// 255 bytes in UTF-8 encoding.
//
// **Validates: Requirements 6.6, 6.7**
func TestPropertyValidateShareName(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		category := rapid.IntRange(0, 3).Draw(t, "category")

		var name string
		var shouldReject bool

		switch category {
		case 0: // Valid name: non-empty, no slash, ≤255 bytes.
			name = rapid.StringMatching(`[a-zA-Z0-9][a-zA-Z0-9 _.\-]{0,100}`).Draw(t, "validName")
			shouldReject = false
		case 1: // Empty string.
			name = ""
			shouldReject = true
		case 2: // Contains path separator.
			prefix := rapid.StringMatching(`[a-zA-Z0-9]{1,10}`).Draw(t, "prefix")
			suffix := rapid.StringMatching(`[a-zA-Z0-9]{1,10}`).Draw(t, "suffix")
			name = prefix + "/" + suffix
			shouldReject = true
		case 3: // Exceeds 255 bytes.
			name = strings.Repeat("x", 256)
			shouldReject = true
		}

		err := ValidateShareName(name)
		if shouldReject && err == nil {
			t.Fatalf("expected rejection for %q (category %d)", name, category)
		}
		if !shouldReject && err != nil {
			t.Fatalf("unexpected rejection for %q: %v", name, err)
		}
	})
}
