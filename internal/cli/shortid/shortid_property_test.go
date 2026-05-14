package shortid

import (
	"errors"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// TestStripPaddingPreservesContent verifies that StripPadding preserves
// non-padding content and only removes trailing '=' characters.
//
// **Validates: Requirements 1.4**
func TestStripPaddingPreservesContent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.String().Draw(t, "input")

		result := StripPadding(s)

		// Result must have no trailing '='
		if len(result) > 0 && result[len(result)-1] == '=' {
			t.Fatalf("result %q still has trailing '='", result)
		}

		// Result + removed '=' chars must equal original
		removed := len(s) - len(result)
		for i := 0; i < removed; i++ {
			if s[len(result)+i] != '=' {
				t.Fatalf("removed character at position %d is %q, expected '='",
					len(result)+i, s[len(result)+i])
			}
		}
		reconstructed := result + strings.Repeat("=", removed)
		if reconstructed != s {
			t.Fatalf("reconstructed %q != original %q", reconstructed, s)
		}
	})
}

// TestResolveCorrectness verifies all branches of the Resolve function.
//
// **Validates: Requirements 1.5, 1.6, 1.7, 1.8**
func TestResolveCorrectness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a slice of IDs (non-empty strings)
		ids := rapid.SliceOfN(
			rapid.StringMatching(`[A-Za-z0-9+/]{4,20}={0,3}`),
			1, 10,
		).Draw(t, "ids")

		// Choose a branch to test
		branch := rapid.IntRange(0, 3).Draw(t, "branch")

		switch branch {
		case 0:
			// Full ID lookup with '=' — ID exists in slice
			idx := rapid.IntRange(0, len(ids)-1).Draw(t, "idx")
			target := ids[idx]
			if !strings.Contains(target, "=") {
				target += "="
				ids[idx] = target
			}
			result, err := Resolve(ids, target)
			if err != nil {
				t.Fatalf("expected match for full ID %q, got error: %v", target, err)
			}
			if result != target {
				t.Fatalf("expected %q, got %q", target, result)
			}

		case 1:
			// Full ID lookup with '=' — ID not in slice
			missing := rapid.StringMatching(`[A-Za-z0-9+/]{4,20}={1,3}`).Draw(t, "missing")
			// Ensure it's not in the slice
			found := false
			for _, id := range ids {
				if id == missing {
					found = true
					break
				}
			}
			if found {
				return // skip this case if collision
			}
			_, err := Resolve(ids, missing)
			var nfe *NotFoundError
			if !errors.As(err, &nfe) {
				t.Fatalf("expected NotFoundError for missing full ID %q, got: %v", missing, err)
			}
			if nfe.Prefix != missing {
				t.Fatalf("NotFoundError.Prefix = %q, want %q", nfe.Prefix, missing)
			}

		case 2:
			// Prefix match — unique match
			idx := rapid.IntRange(0, len(ids)-1).Draw(t, "idx")
			stripped := StripPadding(ids[idx])
			if len(stripped) == 0 {
				return // skip empty stripped IDs
			}
			// Use the full stripped form as prefix to maximize chance of unique match
			prefix := stripped
			// Count matches
			var matches []string
			for _, id := range ids {
				if strings.HasPrefix(StripPadding(id), prefix) {
					matches = append(matches, id)
				}
			}
			result, err := Resolve(ids, prefix)
			if len(matches) == 1 {
				if err != nil {
					t.Fatalf("expected unique match for prefix %q, got error: %v", prefix, err)
				}
				if result != matches[0] {
					t.Fatalf("expected %q, got %q", matches[0], result)
				}
			} else if len(matches) > 1 {
				var ae *AmbiguousError
				if !errors.As(err, &ae) {
					t.Fatalf("expected AmbiguousError for prefix %q with %d matches, got: %v",
						prefix, len(matches), err)
				}
				if len(ae.Matches) != len(matches) {
					t.Fatalf("AmbiguousError.Matches has %d entries, want %d",
						len(ae.Matches), len(matches))
				}
			}

		case 3:
			// Prefix match — zero matches
			// Generate a prefix that won't match any ID
			prefix := rapid.StringMatching(`[!@#$%^&]{3,8}`).Draw(t, "noMatchPrefix")
			if strings.Contains(prefix, "=") {
				return // skip if it contains '='
			}
			_, err := Resolve(ids, prefix)
			// Check if it actually matches anything
			var matches []string
			for _, id := range ids {
				if strings.HasPrefix(StripPadding(id), prefix) {
					matches = append(matches, id)
				}
			}
			if len(matches) == 0 {
				var nfe *NotFoundError
				if !errors.As(err, &nfe) {
					t.Fatalf("expected NotFoundError for prefix %q, got: %v", prefix, err)
				}
			}
		}
	})
}

// TestFormatShortIDsUniqueness verifies the uniqueness invariant of
// FormatShortIDs.
//
// **Validates: Requirements 1.9, 1.10**
func TestFormatShortIDsUniqueness(t *testing.T) {
	// Test empty input returns empty map
	result := FormatShortIDs(nil)
	if len(result) != 0 {
		t.Fatalf("FormatShortIDs(nil) returned %d entries, want 0", len(result))
	}
	result = FormatShortIDs([]string{})
	if len(result) != 0 {
		t.Fatalf("FormatShortIDs([]) returned %d entries, want 0", len(result))
	}

	rapid.Check(t, func(t *rapid.T) {
		// Generate distinct IDs
		ids := rapid.SliceOfNDistinct(
			rapid.StringMatching(`[A-Za-z0-9+/]{8,30}={0,3}`),
			1, 15,
			func(s string) string { return s },
		).Draw(t, "ids")

		result := FormatShortIDs(ids)

		// Every input ID has an entry
		for _, id := range ids {
			if _, ok := result[id]; !ok {
				t.Fatalf("missing entry for ID %q", id)
			}
		}

		// Each prefix is at least min(DefaultLength, len(StripPadding(id))) chars
		for _, id := range ids {
			prefix := result[id]
			stripped := StripPadding(id)
			minLen := DefaultLength
			if len(stripped) < minLen {
				minLen = len(stripped)
			}
			if len(prefix) < minLen {
				t.Fatalf("prefix %q for ID %q is shorter than min %d",
					prefix, id, minLen)
			}
		}

		// No two IDs share a prefix
		seen := make(map[string]string, len(ids))
		for _, id := range ids {
			prefix := result[id]
			if other, ok := seen[prefix]; ok {
				t.Fatalf("duplicate prefix %q for IDs %q and %q",
					prefix, other, id)
			}
			seen[prefix] = id
		}

		// Each prefix is a prefix of its stripped ID
		for _, id := range ids {
			prefix := result[id]
			stripped := StripPadding(id)
			if !strings.HasPrefix(stripped, prefix) {
				t.Fatalf("prefix %q is not a prefix of stripped ID %q",
					prefix, stripped)
			}
		}
	})
}
