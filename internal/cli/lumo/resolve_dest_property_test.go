package lumoCmd

import (
	"testing"

	"pgregory.net/rapid"
)

// Compile-time assertion that resolveDestination has the expected signature.
var _ = resolveDestination

// Feature: lumo-chat-cp-dest, Property 3: Destination title derivation
//
// For any source title t and destination LumoURI{Path: p}: if p is empty,
// the resolved title SHALL be t + " (copy)"; if p is non-empty, the
// resolved title SHALL be p verbatim.
//
// **Validates: Requirements 2.5, 2.6**
func TestPropertyDestinationTitleDerivation(t *testing.T) {
	t.Run("empty_path_appends_copy_suffix", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			srcTitle := rapid.String().Draw(t, "src_title")

			result := resolveDestTitle("", srcTitle)
			expected := srcTitle + " (copy)"

			if result != expected {
				t.Fatalf("resolveDestTitle(%q, %q) = %q, want %q",
					"", srcTitle, result, expected)
			}
		})
	})

	t.Run("non_empty_path_used_verbatim", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			srcTitle := rapid.String().Draw(t, "src_title")
			// Generate a non-empty path.
			path := rapid.StringMatching(`.+`).Draw(t, "path")

			result := resolveDestTitle(path, srcTitle)

			if result != path {
				t.Fatalf("resolveDestTitle(%q, %q) = %q, want %q",
					path, srcTitle, result, path)
			}
		})
	})
}
