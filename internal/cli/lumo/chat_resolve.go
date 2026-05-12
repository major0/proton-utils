package lumoCmd

import (
	"context"
	"fmt"

	"github.com/major0/proton-cli/api/lumo"
	cli "github.com/major0/proton-cli/internal/cli"
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

	// Name callback with DEK caching for title decryption.
	dekCache := make(map[string][]byte) // spaceID → DEK
	nameFunc := func(i int) string {
		p := active[i]
		dek, ok := dekCache[p.Space.ID]
		if !ok {
			d, derr := deriveDEK(p.Space)
			if derr != nil {
				dekCache[p.Space.ID] = nil
				return ""
			}
			dek = d
			dekCache[p.Space.ID] = dek
		}
		if dek == nil {
			return ""
		}
		return decryptTitle(p.Conversation, dek, p.Space.SpaceTag)
	}

	idx, err := cli.ResolveEntity(ids, input, nameFunc)
	if err != nil {
		return nil, err
	}
	return &ResolvedConversation{
		ConversationID: active[idx].Conversation.ID,
		SpaceID:        active[idx].Space.ID,
	}, nil
}
