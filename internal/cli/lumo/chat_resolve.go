package lumoCmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/major0/proton-cli/api/lumo"
)

// ResolvedConversation holds the result of conversation resolution.
type ResolvedConversation struct {
	ConversationID string
	SpaceID        string
}

// resolveConversationByInput resolves a user-provided string to a
// conversation. Resolution order:
//  1. Try as ID prefix via resolveShortID against all conversation IDs.
//  2. If no ID match, decrypt all conversation titles and match by
//     case-insensitive substring.
//
// Returns an error if zero or multiple conversations match.
func resolveConversationByInput(ctx context.Context, client *lumo.Client, input string) (*ResolvedConversation, error) {
	pairs, err := client.ListAllConversations(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading conversations: %w", err)
	}

	deriveDEK := func(s *lumo.Space) ([]byte, error) {
		return client.DeriveSpaceDEK(ctx, s)
	}

	return resolveFromPairs(pairs, input, decryptConversationTitle, deriveDEK)
}

// resolveConversationScoped resolves a conversation within a specific scope.
// If spaceID is empty, searches only simple spaces (filtered by isSimple).
// If spaceID is non-empty, searches only within that space.
func resolveConversationScoped(
	pairs []lumo.SpaceConversation,
	query string,
	spaceID string,
	isSimple func(*lumo.Space) bool,
	decryptTitle func(lumo.Conversation, []byte, string) string,
	deriveDEK func(*lumo.Space) ([]byte, error),
) (*ResolvedConversation, error) {
	var filtered []lumo.SpaceConversation
	if spaceID == "" {
		for _, p := range pairs {
			if isSimple(p.Space) {
				filtered = append(filtered, p)
			}
		}
	} else {
		for _, p := range pairs {
			if p.Space.ID == spaceID {
				filtered = append(filtered, p)
			}
		}
	}
	return resolveFromPairs(filtered, query, decryptTitle, deriveDEK)
}

// resolveFromPairs resolves input against pre-fetched conversation pairs.
// This is the testable core of resolveConversationByInput.
func resolveFromPairs(
	pairs []lumo.SpaceConversation,
	input string,
	decryptTitle func(lumo.Conversation, []byte, string) string,
	deriveDEK func(*lumo.Space) ([]byte, error),
) (*ResolvedConversation, error) {
	// Filter out deleted conversations and build ID slice.
	var active []lumo.SpaceConversation
	for _, p := range pairs {
		if p.Conversation.DeleteTime == "" {
			active = append(active, p)
		}
	}

	ids := make([]string, len(active))
	for i, p := range active {
		ids[i] = p.Conversation.ID
	}

	// Step 1: Try ID prefix resolution.
	resolved, err := resolveShortID(ids, input)
	if err == nil {
		// Found a unique ID match.
		for _, p := range active {
			if p.Conversation.ID == resolved {
				return &ResolvedConversation{
					ConversationID: p.Conversation.ID,
					SpaceID:        p.Space.ID,
				}, nil
			}
		}
	}

	// If ambiguous, return that error directly.
	var ambErr *shortIDAmbiguousError
	if errors.As(err, &ambErr) {
		return nil, err
	}

	// Step 2: Title fallback — only reached on shortIDNotFoundError.
	dekCache := make(map[string][]byte) // spaceID → DEK
	type titleMatch struct {
		convID  string
		spaceID string
		title   string
	}
	var matches []titleMatch

	lowerInput := strings.ToLower(input)

	for _, p := range active {
		dek, ok := dekCache[p.Space.ID]
		if !ok {
			d, derr := deriveDEK(p.Space)
			if derr != nil {
				// Non-fatal: skip spaces where DEK derivation fails.
				dekCache[p.Space.ID] = nil
				continue
			}
			dek = d
			dekCache[p.Space.ID] = dek
		}
		if dek == nil {
			continue
		}

		title := decryptTitle(p.Conversation, dek, p.Space.SpaceTag)
		if title == "" {
			continue
		}

		if strings.Contains(strings.ToLower(title), lowerInput) {
			matches = append(matches, titleMatch{
				convID:  p.Conversation.ID,
				spaceID: p.Space.ID,
				title:   title,
			})
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no conversation matching %q", input)
	case 1:
		return &ResolvedConversation{
			ConversationID: matches[0].convID,
			SpaceID:        matches[0].spaceID,
		}, nil
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "multiple conversations match %q:", input)
		for _, m := range matches {
			fmt.Fprintf(&b, "\n  %s  %s", m.convID, m.title)
		}
		return nil, fmt.Errorf("%s", b.String())
	}
}
