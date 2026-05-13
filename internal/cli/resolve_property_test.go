package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/major0/proton-utils/internal/cli/shortid"
	"pgregory.net/rapid"
)

// TestPropertyResolveEntityPriorityChain verifies all priority branches of
// ResolveEntity: unique ID match wins, ambiguous ID returns error without
// name fallback, exact name wins over substring, single substring returns
// index, multiple name matches returns error, zero matches returns not-found.
//
// **Validates: Requirements 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 4.9**
func TestPropertyResolveEntityPriorityChain(t *testing.T) {
	// Sub-test: unique ID prefix match wins regardless of names.
	t.Run("UniqueIDMatch", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			// Generate 2-5 distinct IDs with base64-like characters.
			n := rapid.IntRange(2, 5).Draw(t, "n")
			ids := make([]string, n)
			for i := range ids {
				// Generate IDs with distinct prefixes to ensure uniqueness.
				ids[i] = fmt.Sprintf("ID%d_%s==", i, rapid.StringMatching(`[A-Za-z0-9+/]{8,12}`).Draw(t, "id"))
			}

			// Pick one ID and use its stripped prefix as query.
			idx := rapid.IntRange(0, n-1).Draw(t, "idx")
			stripped := shortid.StripPadding(ids[idx])
			// Use enough prefix to be unique.
			prefixLen := len(stripped)
			if prefixLen > shortid.DefaultLength {
				prefixLen = shortid.DefaultLength + 4
			}
			if prefixLen > len(stripped) {
				prefixLen = len(stripped)
			}
			query := stripped[:prefixLen]

			// Verify the query uniquely resolves via shortid.
			_, err := shortid.Resolve(ids, query)
			if err != nil {
				t.Skip("generated IDs not uniquely resolvable by prefix")
			}

			names := func(i int) string { return "name" }
			result, err := ResolveEntity(ids, query, names)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != idx {
				t.Fatalf("expected index %d, got %d", idx, result)
			}
		})
	})

	// Sub-test: ambiguous ID returns error without name fallback.
	t.Run("AmbiguousID", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			// Create IDs that share a common prefix.
			prefix := rapid.StringMatching(`[A-Za-z0-9+/]{4}`).Draw(t, "prefix")
			suffix1 := rapid.StringMatching(`[A-Za-z0-9+/]{8}`).Draw(t, "suffix1")
			suffix2 := rapid.StringMatching(`[A-Za-z0-9+/]{8}`).Draw(t, "suffix2")
			if suffix1 == suffix2 {
				t.Skip("identical suffixes")
			}

			ids := []string{prefix + suffix1, prefix + suffix2}
			query := prefix

			// Verify ambiguity.
			_, err := shortid.Resolve(ids, query)
			var ambErr *shortid.AmbiguousError
			if !errors.As(err, &ambErr) {
				t.Skip("not ambiguous by ID")
			}

			names := func(i int) string { return query } // exact name match exists
			_, err = ResolveEntity(ids, query, names)
			if err == nil {
				t.Fatal("expected error for ambiguous ID, got nil")
			}
			// Should be an AmbiguousError, not a name resolution.
			if !errors.As(err, &ambErr) {
				t.Fatalf("expected AmbiguousError, got: %v", err)
			}
		})
	})

	// Sub-test: exact name match wins over substring.
	t.Run("ExactNameWins", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			// IDs that won't match the query by prefix.
			ids := []string{"XXXXXXXX==", "YYYYYYYY==", "ZZZZZZZZ=="}
			query := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, "query")

			// One entity has exact name match, another has substring match.
			nameMap := map[int]string{
				0: query + "extra",       // substring match
				1: query,                 // exact match
				2: "unrelated something", // no match
			}
			names := func(i int) string { return nameMap[i] }

			result, err := ResolveEntity(ids, query, names)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != 1 {
				t.Fatalf("expected index 1 (exact match), got %d", result)
			}
		})
	})

	// Sub-test: single substring match returns index.
	t.Run("SingleSubstring", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			ids := []string{"XXXXXXXX==", "YYYYYYYY==", "ZZZZZZZZ=="}
			query := rapid.StringMatching(`[a-z]{3,6}`).Draw(t, "query")

			nameMap := map[int]string{
				0: "prefix_" + query + "_suffix", // substring match
				1: "no match here",
				2: "also no match",
			}
			names := func(i int) string { return nameMap[i] }

			// Ensure no exact match exists.
			for i := range ids {
				if strings.EqualFold(names(i), query) {
					t.Skip("accidental exact match")
				}
			}

			result, err := ResolveEntity(ids, query, names)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != 0 {
				t.Fatalf("expected index 0 (substring match), got %d", result)
			}
		})
	})

	// Sub-test: multiple name matches returns error.
	t.Run("MultipleNameMatches", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			ids := []string{"XXXXXXXX==", "YYYYYYYY==", "ZZZZZZZZ=="}
			query := rapid.StringMatching(`[a-z]{3,6}`).Draw(t, "query")

			nameMap := map[int]string{
				0: query, // exact match
				1: query, // exact match (duplicate)
				2: "no match",
			}
			names := func(i int) string { return nameMap[i] }

			_, err := ResolveEntity(ids, query, names)
			if err == nil {
				t.Fatal("expected error for multiple matches, got nil")
			}
			if !strings.Contains(err.Error(), "multiple matches") {
				t.Fatalf("expected 'multiple matches' error, got: %v", err)
			}
		})
	})

	// Sub-test: zero matches returns not-found error.
	t.Run("ZeroMatches", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			ids := []string{"XXXXXXXX==", "YYYYYYYY=="}
			query := rapid.StringMatching(`[a-z]{3,6}`).Draw(t, "query")

			names := func(i int) string { return "completely_unrelated_name" }

			_, err := ResolveEntity(ids, query, names)
			if err == nil {
				t.Fatal("expected error for zero matches, got nil")
			}
			if !strings.Contains(err.Error(), "no match") {
				t.Fatalf("expected 'no match' error, got: %v", err)
			}
		})
	})
}
