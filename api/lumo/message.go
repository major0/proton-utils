package lumo

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	// WireRoleUser is the wire-format integer for user messages.
	WireRoleUser = 1
	// WireRoleAssistant is the wire-format integer for assistant messages.
	WireRoleAssistant = 2
)

// wireRoleString maps wire-format role integers to AD string values.
func wireRoleString(role int) string {
	switch role {
	case WireRoleUser:
		return "user"
	case WireRoleAssistant:
		return "assistant"
	default:
		return "unknown"
	}
}

// CreateMessage creates a message in the given conversation with
// encrypted content. The space object is needed for key derivation.
func (c *Client) CreateMessage(ctx context.Context, space *Space, conv *Conversation, role int, content string) (*Message, error) {
	dek, err := c.deriveSpaceDEK(ctx, space)
	if err != nil {
		return nil, fmt.Errorf("lumo: create message: %w", err)
	}

	msgTag := GenerateTag()
	ad := MessageAD(msgTag, wireRoleString(role), "", conv.ConversationTag)

	var encrypted string
	if content != "" {
		privJSON, err := json.Marshal(map[string]string{"content": content})
		if err != nil {
			return nil, fmt.Errorf("lumo: create message: marshal: %w", err)
		}
		encrypted, err = EncryptString(string(privJSON), dek, ad)
		if err != nil {
			return nil, fmt.Errorf("lumo: create message: encrypt: %w", err)
		}
	}

	req := CreateMessageReq{
		ConversationID: conv.ID,
		MessageTag:     msgTag,
		Role:           role,
		Status:         2, // succeeded
		Encrypted:      encrypted,
	}

	var resp GetMessageResponse
	err = c.Session.DoJSON(ctx, "POST", c.url("/lumo/v1/conversations/"+conv.ID+"/messages"), req, &resp)
	if err != nil {
		return nil, fmt.Errorf("lumo: create message: %w", mapCRUDError(err))
	}
	return &resp.Message, nil
}

// CreateRawMessage creates a message with pre-encrypted content.
// Unlike CreateMessage, it does not perform any encryption — the caller
// provides the already-encrypted blob in req.Encrypted.
func (c *Client) CreateRawMessage(ctx context.Context, req CreateMessageReq) (*Message, error) {
	var resp GetMessageResponse
	err := c.Session.DoJSON(ctx, "POST", c.url("/lumo/v1/conversations/"+req.ConversationID+"/messages"), req, &resp)
	if err != nil {
		return nil, fmt.Errorf("lumo: create raw message: %w", mapCRUDError(err))
	}
	return &resp.Message, nil
}

// GetMessage fetches a message by ID.
func (c *Client) GetMessage(ctx context.Context, messageID string) (*Message, error) {
	var resp GetMessageResponse
	err := c.Session.DoJSON(ctx, "GET", c.url("/lumo/v1/messages/"+messageID), nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("lumo: get message: %w", mapCRUDError(err))
	}
	return &resp.Message, nil
}

// ListMessages fetches all messages in a conversation. The
// GET /conversations/:id endpoint returns shallow messages (no Encrypted
// field), so each message is then fetched individually via
// GET /messages/:id to get the full encrypted content.
func (c *Client) ListMessages(ctx context.Context, conversationID string) ([]Message, error) {
	conv, err := c.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("lumo: list messages: %w", err)
	}

	if len(conv.Messages) == 0 {
		return nil, nil
	}

	full := make([]Message, 0, len(conv.Messages))
	for _, s := range conv.Messages {
		msg, err := c.GetMessage(ctx, s.ID)
		if err != nil {
			return nil, fmt.Errorf("lumo: list messages: fetch %s: %w", s.ID, err)
		}
		full = append(full, *msg)
	}
	return full, nil
}
