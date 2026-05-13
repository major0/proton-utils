package lumoCmd

import (
	"strings"
	"testing"

	"github.com/major0/proton-utils/api/lumo"
	"pgregory.net/rapid"
)

// Feature: lumo-chat-cp-dest, Property 4: Space resolution priority — ID prefix wins over name; exact name wins over substring
//
// **Validates: Requirements 3.1, 3.2**
func TestPropertySpaceResolutionPriority(t *testing.T) {
	// Sub-property: ID prefix match takes priority over name match.
	t.Run("id_prefix_wins_over_name", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			// Generate a target space with a known ID prefix.
			targetID := rapid.StringMatching(`[A-Za-z0-9]{16,24}`).Draw(t, "target_id")
			targetName := rapid.StringMatching(`[A-Za-z]{4,10}`).Draw(t, "target_name")

			// Generate another space whose name equals the target's ID prefix
			// (so name-matching would find it if ID matching didn't win).
			prefix := targetID[:8]
			otherID := rapid.StringMatching(`[0-9]{16,24}`).Draw(t, "other_id")

			spaces := []lumo.Space{
				{ID: targetID, SpaceTag: "tag1"},
				{ID: otherID, SpaceTag: "tag2"},
			}

			names := map[string]string{
				targetID: targetName,
				otherID:  prefix, // other space's name matches the ID prefix
			}
			decryptName := func(s *lumo.Space) string { return names[s.ID] }

			result, err := resolveSpace(spaces, prefix, decryptName)
			if err != nil {
				t.Fatalf("resolveSpace(%q) error: %v", prefix, err)
			}
			if result.ID != targetID {
				t.Fatalf("expected ID=%q (ID prefix match), got %q", targetID, result.ID)
			}
		})
	})

	// Sub-property: exact name wins over substring match.
	t.Run("exact_name_wins_over_substring", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			// Generate a short exact name.
			exactName := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, "exact_name")

			// Generate spaces: one with exact name, others with names containing it as substring.
			exactID := rapid.StringMatching(`[0-9]{16,24}`).Draw(t, "exact_id")
			subID := rapid.StringMatching(`[0-9]{16,24}`).Draw(t, "sub_id")

			// Ensure IDs are distinct.
			if exactID == subID {
				return
			}

			// The substring space has a longer name containing the exact name.
			subName := "prefix-" + exactName + "-suffix"

			spaces := []lumo.Space{
				{ID: exactID, SpaceTag: "tag1"},
				{ID: subID, SpaceTag: "tag2"},
			}

			names := map[string]string{
				exactID: exactName,
				subID:   subName,
			}
			decryptName := func(s *lumo.Space) string { return names[s.ID] }

			result, err := resolveSpace(spaces, exactName, decryptName)
			if err != nil {
				t.Fatalf("resolveSpace(%q) error: %v", exactName, err)
			}
			if result.ID != exactID {
				t.Fatalf("expected ID=%q (exact name match), got %q", exactID, result.ID)
			}
		})
	})

	// Sub-property: multiple substring matches with one exact match returns the exact match.
	t.Run("exact_match_among_substrings", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			exactName := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, "exact_name")

			// Generate 2–4 spaces with substring matches plus one exact match.
			numSub := rapid.IntRange(2, 4).Draw(t, "num_substring")
			var spaces []lumo.Space
			names := make(map[string]string)

			// Add the exact match space.
			exactID := rapid.StringMatching(`[0-9]{20,24}`).Draw(t, "exact_id")
			spaces = append(spaces, lumo.Space{ID: exactID, SpaceTag: "tag-exact"})
			names[exactID] = exactName

			// Add substring-match spaces.
			idSet := map[string]bool{exactID: true}
			for i := 0; i < numSub; i++ {
				id := rapid.StringMatching(`[0-9]{20,24}`).Draw(t, "sub_id")
				if idSet[id] {
					continue
				}
				idSet[id] = true
				suffix := rapid.StringMatching(`[A-Z][a-z]{2,6}`).Draw(t, "suffix")
				spaces = append(spaces, lumo.Space{ID: id, SpaceTag: "tag-sub"})
				names[id] = exactName + " " + suffix
			}

			decryptName := func(s *lumo.Space) string { return names[s.ID] }

			result, err := resolveSpace(spaces, exactName, decryptName)
			if err != nil {
				t.Fatalf("resolveSpace(%q) error: %v", exactName, err)
			}
			if result.ID != exactID {
				t.Fatalf("expected ID=%q (exact name), got %q", exactID, result.ID)
			}
		})
	})

	// Sub-property: case-insensitive exact match.
	t.Run("case_insensitive_exact", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			name := rapid.StringMatching(`[a-z]{4,10}`).Draw(t, "name")
			id := rapid.StringMatching(`[0-9]{16,24}`).Draw(t, "id")

			spaces := []lumo.Space{{ID: id, SpaceTag: "tag1"}}
			names := map[string]string{id: name}
			decryptName := func(s *lumo.Space) string { return names[s.ID] }

			// Query with different case.
			query := strings.ToUpper(name)
			result, err := resolveSpace(spaces, query, decryptName)
			if err != nil {
				t.Fatalf("resolveSpace(%q) error: %v", query, err)
			}
			if result.ID != id {
				t.Fatalf("expected ID=%q, got %q", id, result.ID)
			}
		})
	})
}

// Feature: lumo-chat-cp-dest, Property 7: Deleted entities excluded from resolution
//
// **Validates: Requirements 3.5, 4.6**
func TestPropertyDeletedSpacesExcluded(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 2–8 spaces, some deleted.
		n := rapid.IntRange(2, 8).Draw(t, "num_spaces")
		var spaces []lumo.Space
		names := make(map[string]string)
		idSet := make(map[string]bool)

		for len(spaces) < n {
			id := rapid.StringMatching(`[A-Za-z0-9]{16,24}`).Draw(t, "id")
			if idSet[id] {
				continue
			}
			idSet[id] = true

			name := rapid.StringMatching(`[a-z]{4,10}`).Draw(t, "name")
			deleteTime := rapid.SampledFrom([]string{"", "2024-01-01T00:00:00Z"}).Draw(t, "delete_time")

			spaces = append(spaces, lumo.Space{
				ID:         id,
				SpaceTag:   "tag-" + id,
				DeleteTime: deleteTime,
			})
			names[id] = name
		}

		decryptName := func(s *lumo.Space) string { return names[s.ID] }

		// Try resolving each space by its full ID.
		for _, s := range spaces {
			result, err := resolveSpace(spaces, s.ID, decryptName)
			if s.DeleteTime != "" {
				// Deleted space must never be returned.
				if err == nil && result != nil && result.ID == s.ID {
					t.Fatalf("resolveSpace returned deleted space %q", s.ID)
				}
			} else {
				// Non-deleted space should resolve successfully.
				if err != nil {
					t.Fatalf("resolveSpace(%q) error for non-deleted space: %v", s.ID, err)
				}
				if result.ID != s.ID {
					t.Fatalf("expected ID=%q, got %q", s.ID, result.ID)
				}
			}
		}

		// Try resolving each space by name.
		for _, s := range spaces {
			name := names[s.ID]
			result, err := resolveSpace(spaces, name, decryptName)
			if s.DeleteTime != "" {
				// If the only match was a deleted space, resolution must not return it.
				if result != nil && result.DeleteTime != "" {
					t.Fatalf("resolveSpace(%q) returned deleted space %q", name, result.ID)
				}
			}
			// If resolution succeeded, the result must not be deleted.
			if err == nil && result != nil && result.DeleteTime != "" {
				t.Fatalf("resolveSpace returned deleted space %q (DeleteTime=%q)", result.ID, result.DeleteTime)
			}
		}
	})
}
