package lumo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// CreateConversation creates a conversation in the given space with an
// encrypted title. The space object must include SpaceKey and SpaceTag.
func (c *Client) CreateConversation(ctx context.Context, space *Space, title string) (*Conversation, error) {
	dek, err := c.deriveSpaceDEK(ctx, space)
	if err != nil {
		return nil, fmt.Errorf("lumo: create conversation: %w", err)
	}

	convTag := GenerateTag()
	ad := ConversationAD(convTag, space.SpaceTag)

	var encrypted string
	if title != "" {
		privJSON, err := json.Marshal(map[string]string{"title": title})
		if err != nil {
			return nil, fmt.Errorf("lumo: create conversation: marshal: %w", err)
		}
		encrypted, err = EncryptString(string(privJSON), dek, ad)
		if err != nil {
			return nil, fmt.Errorf("lumo: create conversation: encrypt: %w", err)
		}
	}

	req := CreateConversationReq{
		SpaceID:         space.ID,
		ConversationTag: convTag,
		Encrypted:       encrypted,
	}

	var resp GetConversationResponse
	err = c.Session.DoJSON(ctx, "POST", c.url("/lumo/v1/spaces/"+space.ID+"/conversations"), req, &resp)
	if err != nil {
		return nil, fmt.Errorf("lumo: create conversation: %w", mapCRUDError(err))
	}
	return &resp.Conversation, nil
}

// ListConversations returns conversations for a space. The Lumo API
// embeds conversations in the list-spaces response, so this fetches all
// spaces and returns conversations from the matching space.
func (c *Client) ListConversations(ctx context.Context, spaceID string) ([]Conversation, error) {
	spaces, err := c.ListSpaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("lumo: list conversations: %w", err)
	}
	for _, s := range spaces {
		if s.ID == spaceID {
			return s.Conversations, nil
		}
	}
	return nil, fmt.Errorf("lumo: list conversations: space %s not found", spaceID)
}

// ListAllConversations returns conversations from all spaces, paired with
// their parent space for decryption context.
func (c *Client) ListAllConversations(ctx context.Context) ([]SpaceConversation, error) {
	spaces, err := c.ListSpaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("lumo: list all conversations: %w", err)
	}
	var result []SpaceConversation
	for i := range spaces {
		for _, conv := range spaces[i].Conversations {
			result = append(result, SpaceConversation{
				Space:        &spaces[i],
				Conversation: conv,
			})
		}
	}
	return result, nil
}

// SpaceConversation pairs a conversation with its parent space.
type SpaceConversation struct {
	Space        *Space
	Conversation Conversation
}

// GetConversation fetches a conversation by ID.
func (c *Client) GetConversation(ctx context.Context, conversationID string) (*Conversation, error) {
	var resp GetConversationResponse
	err := c.Session.DoJSON(ctx, "GET", c.url("/lumo/v1/conversations/"+conversationID), nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("lumo: get conversation: %w", mapCRUDError(err))
	}
	return &resp.Conversation, nil
}

// DeleteConversation deletes a conversation by ID.
func (c *Client) DeleteConversation(ctx context.Context, conversationID string) error {
	err := c.Session.DoJSON(ctx, "DELETE", c.url("/lumo/v1/conversations/"+conversationID), nil, nil)
	if err != nil {
		return fmt.Errorf("lumo: delete conversation: %w", mapCRUDError(err))
	}
	return nil
}

// deriveSpaceDEK unwraps a space's key and derives the DEK.
func (c *Client) deriveSpaceDEK(ctx context.Context, space *Space) ([]byte, error) {
	masterKey, err := c.GetMasterKey(ctx)
	if err != nil {
		return nil, err
	}

	wrappedKey, err := base64.StdEncoding.DecodeString(space.SpaceKey)
	if err != nil {
		return nil, fmt.Errorf("decode space key: %w", err)
	}

	spaceKey, err := UnwrapSpaceKey(masterKey, wrappedKey)
	if err != nil {
		return nil, err
	}

	return DeriveDataEncryptionKey(spaceKey)
}

// DeriveSpaceDEK is the exported version of deriveSpaceDEK for use by
// command-layer code that needs to decrypt conversation content.
func (c *Client) DeriveSpaceDEK(ctx context.Context, space *Space) ([]byte, error) {
	return c.deriveSpaceDEK(ctx, space)
}
