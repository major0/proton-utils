package lumoCmd

import (
	"strings"
	"testing"

	"github.com/major0/proton-cli/api/lumo"
	"github.com/major0/proton-cli/internal/cli/shortid"
	"pgregory.net/rapid"
)

// --- Property 1: ID prefix resolution ---

// TestResolveByIDPrefix_Property verifies that for any set of
// conversation IDs and any prefix that uniquely matches exactly one ID,
// resolveFromPairs returns that conversation's ID and parent space ID.
//
// Feature: lumo-chat-log, Property 1: ID prefix resolution
//
// **Validates: Requirements 1.2, 1.3**
func TestResolveByIDPrefix_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 2–10 unique conversation IDs (base64-like).
		n := rapid.IntRange(2, 10).Draw(t, "num_convs")
		idSet := make(map[string]bool, n)
		var pairs []lumo.SpaceConversation

		for len(pairs) < n {
			id := rapid.StringMatching(`[A-Za-z0-9+/]{16,32}`).Draw(t, "conv_id")
			if idSet[id] {
				continue
			}
			idSet[id] = true

			spaceID := rapid.StringMatching(`[A-Za-z0-9]{8,16}`).Draw(t, "space_id")
			pairs = append(pairs, lumo.SpaceConversation{
				Space: &lumo.Space{
					ID:       spaceID,
					SpaceTag: "tag-" + spaceID,
				},
				Conversation: lumo.Conversation{
					ID:      id,
					SpaceID: spaceID,
				},
			})
		}

		// Pick one conversation to resolve.
		targetIdx := rapid.IntRange(0, len(pairs)-1).Draw(t, "target_idx")
		target := pairs[targetIdx]
		targetID := shortid.StripPadding(target.Conversation.ID)

		// Find the shortest unique prefix for this ID.
		prefix := findUniquePrefix(targetID, pairs, targetIdx)
		if prefix == "" {
			// No unique prefix exists (duplicate stripped IDs) — skip.
			return
		}

		// No title decryption needed for ID resolution.
		decryptTitle := func(_ lumo.Conversation, _ []byte, _ string) string { return "" }
		deriveDEK := func(_ *lumo.Space) ([]byte, error) { return nil, nil }

		result, err := resolveFromPairs(pairs, prefix, decryptTitle, deriveDEK)
		if err != nil {
			t.Fatalf("resolveFromPairs(%q) error: %v", prefix, err)
		}
		if result.ConversationID != target.Conversation.ID {
			t.Fatalf("expected ConversationID=%q, got %q", target.Conversation.ID, result.ConversationID)
		}
		if result.SpaceID != target.Space.ID {
			t.Fatalf("expected SpaceID=%q, got %q", target.Space.ID, result.SpaceID)
		}
	})
}

// findUniquePrefix returns the shortest prefix of targetID that uniquely
// matches only the target among all pairs. Returns "" if no unique prefix
// exists.
func findUniquePrefix(targetID string, pairs []lumo.SpaceConversation, targetIdx int) string {
	for prefixLen := 1; prefixLen <= len(targetID); prefixLen++ {
		prefix := targetID[:prefixLen]
		count := 0
		for i, p := range pairs {
			if i == targetIdx {
				continue
			}
			other := shortid.StripPadding(p.Conversation.ID)
			if strings.HasPrefix(other, prefix) {
				count++
			}
		}
		if count == 0 {
			return prefix
		}
	}
	return ""
}

// --- Property 2: Title fallback resolution ---

// TestResolveByTitle_Property verifies that for any set of conversations
// with decrypted titles, and any input that does not match any ID prefix
// but is a case-insensitive substring of exactly one title, the resolver
// returns that conversation.
//
// Feature: lumo-chat-log, Property 2: Title fallback resolution
//
// **Validates: Requirements 1.4**
func TestResolveByTitle_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 2–8 conversations with distinct titles.
		n := rapid.IntRange(2, 8).Draw(t, "num_convs")
		var entries []convEntry
		titleSet := make(map[string]bool)

		for len(entries) < n {
			// Use numeric IDs that won't match our search input (alphabetic).
			id := rapid.StringMatching(`[0-9]{20,24}`).Draw(t, "conv_id")
			spaceID := rapid.StringMatching(`[0-9]{12}`).Draw(t, "space_id")
			// Generate distinct titles using only alphabetic chars.
			title := rapid.StringMatching(`[A-Z][a-z]{4,12}`).Draw(t, "title")
			lowerTitle := strings.ToLower(title)
			if titleSet[lowerTitle] {
				continue
			}
			titleSet[lowerTitle] = true

			entries = append(entries, convEntry{
				pair: lumo.SpaceConversation{
					Space: &lumo.Space{
						ID:       spaceID,
						SpaceTag: "tag-" + spaceID,
					},
					Conversation: lumo.Conversation{
						ID:      id,
						SpaceID: spaceID,
					},
				},
				title: title,
			})
		}

		// Pick one conversation as the target.
		targetIdx := rapid.IntRange(0, len(entries)-1).Draw(t, "target_idx")
		target := entries[targetIdx]

		// Find a substring of the target title that matches only this one.
		searchInput := findUniqueSubstring(target.title, entries, targetIdx)
		if searchInput == "" {
			// Could not find a unique substring — skip this iteration.
			return
		}

		// Build pairs slice.
		pairs := make([]lumo.SpaceConversation, len(entries))
		for i, e := range entries {
			pairs[i] = e.pair
		}

		// Title decryption returns the pre-set title.
		titleMap := make(map[string]string, len(entries))
		for _, e := range entries {
			titleMap[e.pair.Conversation.ID] = e.title
		}
		decryptTitle := func(conv lumo.Conversation, _ []byte, _ string) string {
			return titleMap[conv.ID]
		}
		deriveDEK := func(_ *lumo.Space) ([]byte, error) {
			return []byte("fake-dek"), nil
		}

		result, err := resolveFromPairs(pairs, searchInput, decryptTitle, deriveDEK)
		if err != nil {
			t.Fatalf("resolveFromPairs(%q) error: %v", searchInput, err)
		}
		if result.ConversationID != target.pair.Conversation.ID {
			t.Fatalf("expected ConversationID=%q, got %q",
				target.pair.Conversation.ID, result.ConversationID)
		}
		if result.SpaceID != target.pair.Space.ID {
			t.Fatalf("expected SpaceID=%q, got %q",
				target.pair.Space.ID, result.SpaceID)
		}
	})

	// Sub-property: multiple title matches return an ambiguous error.
	t.Run("ambiguous_title", func(t *testing.T) {
		rapid.Check(t, func(t *rapid.T) {
			// Generate 2+ conversations sharing a common title substring.
			commonWord := rapid.StringMatching(`[a-z]{3,6}`).Draw(t, "common")
			n := rapid.IntRange(2, 5).Draw(t, "num_matches")

			var pairs []lumo.SpaceConversation
			titleMap := make(map[string]string)

			for i := 0; i < n; i++ {
				id := rapid.StringMatching(`[0-9]{20,24}`).Draw(t, "conv_id")
				spaceID := rapid.StringMatching(`[0-9]{12}`).Draw(t, "space_id")
				// Each title contains the common word.
				suffix := rapid.StringMatching(`[A-Z][a-z]{2,6}`).Draw(t, "suffix")
				title := suffix + " " + commonWord + " chat"

				pairs = append(pairs, lumo.SpaceConversation{
					Space: &lumo.Space{
						ID:       spaceID,
						SpaceTag: "tag-" + spaceID,
					},
					Conversation: lumo.Conversation{
						ID:      id,
						SpaceID: spaceID,
					},
				})
				titleMap[id] = title
			}

			decryptTitle := func(conv lumo.Conversation, _ []byte, _ string) string {
				return titleMap[conv.ID]
			}
			deriveDEK := func(_ *lumo.Space) ([]byte, error) {
				return []byte("fake-dek"), nil
			}

			_, err := resolveFromPairs(pairs, commonWord, decryptTitle, deriveDEK)
			if err == nil {
				t.Fatalf("expected ambiguous error for input %q, got nil", commonWord)
			}
			if !strings.Contains(err.Error(), "multiple matches for") {
				t.Fatalf("expected ambiguous error message, got: %v", err)
			}
		})
	})
}

// findUniqueSubstring finds a case-insensitive substring of the target
// title that matches only the target among all entries. Returns "" if
// none found.
func findUniqueSubstring(targetTitle string, entries []convEntry, targetIdx int) string {
	lower := strings.ToLower(targetTitle)
	// Try progressively longer substrings from the start.
	for length := 3; length <= len(lower); length++ {
		for start := 0; start+length <= len(lower); start++ {
			sub := lower[start : start+length]
			matchCount := 0
			for i, e := range entries {
				if i == targetIdx {
					continue
				}
				if strings.Contains(strings.ToLower(e.title), sub) {
					matchCount++
					break
				}
			}
			if matchCount == 0 {
				return sub
			}
		}
	}
	return ""
}

type convEntry struct {
	pair  lumo.SpaceConversation
	title string
}

// --- Property 5: Bare strings resolve only within simple spaces ---

// TestScopedResolveBareSimpleOnly_Property verifies that for any set of
// spaces containing both simple and project spaces, and a conversation
// that exists only in a project space, resolving with an empty spaceID
// (bare string) SHALL NOT return that conversation.
//
// Feature: lumo-chat-cp-dest, Property 5: Bare strings resolve only within simple spaces
//
// **Validates: Requirements 4.1, 4.5, 9.4**
func TestScopedResolveBareSimpleOnly_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 1–4 simple spaces (no conversations matching our target).
		numSimple := rapid.IntRange(1, 4).Draw(t, "num_simple")
		// Generate 1–3 project spaces, one of which has our target conversation.
		numProject := rapid.IntRange(1, 3).Draw(t, "num_project")

		simpleIDs := make(map[string]bool)
		var pairs []lumo.SpaceConversation
		titleMap := make(map[string]string)

		// Create simple spaces with conversations that do NOT match the target.
		for i := 0; i < numSimple; i++ {
			spaceID := rapid.StringMatching(`[0-9]{12}`).Draw(t, "simple_space_id")
			if simpleIDs[spaceID] {
				continue
			}
			simpleIDs[spaceID] = true

			convID := rapid.StringMatching(`[0-9]{20,24}`).Draw(t, "simple_conv_id")
			// Title uses a prefix that won't match our target query.
			title := "simple-" + rapid.StringMatching(`[a-z]{4,8}`).Draw(t, "simple_title")

			pairs = append(pairs, lumo.SpaceConversation{
				Space: &lumo.Space{
					ID:       spaceID,
					SpaceTag: "tag-" + spaceID,
				},
				Conversation: lumo.Conversation{
					ID:      convID,
					SpaceID: spaceID,
				},
			})
			titleMap[convID] = title
		}

		// Create project spaces; one has the target conversation.
		targetTitle := "project-target-" + rapid.StringMatching(`[A-Z]{4,8}`).Draw(t, "target_suffix")
		for i := 0; i < numProject; i++ {
			spaceID := rapid.StringMatching(`[A-Z]{12}`).Draw(t, "project_space_id")
			convID := rapid.StringMatching(`[0-9]{20,24}`).Draw(t, "project_conv_id")

			var title string
			if i == 0 {
				// First project space has the target conversation.
				title = targetTitle
			} else {
				title = "project-other-" + rapid.StringMatching(`[a-z]{4,8}`).Draw(t, "other_title")
			}

			pairs = append(pairs, lumo.SpaceConversation{
				Space: &lumo.Space{
					ID:       spaceID,
					SpaceTag: "tag-" + spaceID,
				},
				Conversation: lumo.Conversation{
					ID:      convID,
					SpaceID: spaceID,
				},
			})
			titleMap[convID] = title
		}

		// isSimple returns true only for spaces in our simpleIDs set.
		isSimple := func(s *lumo.Space) bool {
			return simpleIDs[s.ID]
		}
		decryptTitle := func(conv lumo.Conversation, _ []byte, _ string) string {
			return titleMap[conv.ID]
		}
		deriveDEK := func(_ *lumo.Space) ([]byte, error) {
			return []byte("fake-dek"), nil
		}

		// Resolve with empty spaceID (bare string behavior).
		result, err := resolveConversationScoped(pairs, targetTitle, "", isSimple, decryptTitle, deriveDEK)

		// The target exists only in a project space, so resolution must fail.
		if err == nil {
			t.Fatalf("expected error resolving %q with empty spaceID, got result: %+v", targetTitle, result)
		}
	})
}

// --- Property 6: Scoped resolution restricts to specified space ---

// TestScopedResolveRestrictsToSpace_Property verifies that for any set
// of spaces where multiple spaces contain conversations with the same
// title, resolving with a non-empty spaceID SHALL return only the
// conversation from the specified space.
//
// Feature: lumo-chat-cp-dest, Property 6: Scoped resolution restricts to specified space
//
// **Validates: Requirements 4.2**
func TestScopedResolveRestrictsToSpace_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 2–5 spaces, each with a conversation having the same title.
		numSpaces := rapid.IntRange(2, 5).Draw(t, "num_spaces")
		sharedTitle := "shared-" + rapid.StringMatching(`[a-z]{4,10}`).Draw(t, "shared_title")

		type spaceConv struct {
			spaceID string
			convID  string
		}
		var spaceConvs []spaceConv
		var pairs []lumo.SpaceConversation
		titleMap := make(map[string]string)
		spaceIDSet := make(map[string]bool)

		for len(spaceConvs) < numSpaces {
			spaceID := rapid.StringMatching(`[A-Za-z0-9]{12}`).Draw(t, "space_id")
			if spaceIDSet[spaceID] {
				continue
			}
			spaceIDSet[spaceID] = true

			convID := rapid.StringMatching(`[0-9]{20,24}`).Draw(t, "conv_id")

			pairs = append(pairs, lumo.SpaceConversation{
				Space: &lumo.Space{
					ID:       spaceID,
					SpaceTag: "tag-" + spaceID,
				},
				Conversation: lumo.Conversation{
					ID:      convID,
					SpaceID: spaceID,
				},
			})
			titleMap[convID] = sharedTitle
			spaceConvs = append(spaceConvs, spaceConv{spaceID: spaceID, convID: convID})
		}

		// Pick one space to scope the resolution to.
		targetIdx := rapid.IntRange(0, len(spaceConvs)-1).Draw(t, "target_idx")
		targetSpaceID := spaceConvs[targetIdx].spaceID
		targetConvID := spaceConvs[targetIdx].convID

		// isSimple is irrelevant when spaceID is non-empty, but provide one.
		isSimple := func(_ *lumo.Space) bool { return true }
		decryptTitle := func(conv lumo.Conversation, _ []byte, _ string) string {
			return titleMap[conv.ID]
		}
		deriveDEK := func(_ *lumo.Space) ([]byte, error) {
			return []byte("fake-dek"), nil
		}

		result, err := resolveConversationScoped(pairs, sharedTitle, targetSpaceID, isSimple, decryptTitle, deriveDEK)
		if err != nil {
			t.Fatalf("resolveConversationScoped(%q, spaceID=%q) error: %v", sharedTitle, targetSpaceID, err)
		}
		if result.ConversationID != targetConvID {
			t.Fatalf("expected ConversationID=%q, got %q", targetConvID, result.ConversationID)
		}
		if result.SpaceID != targetSpaceID {
			t.Fatalf("expected SpaceID=%q, got %q", targetSpaceID, result.SpaceID)
		}
	})
}
