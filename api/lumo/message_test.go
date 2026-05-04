package lumo

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestCreateMessage_RequestBody(t *testing.T) {
	mock := newCRUDMockServer(t)
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = mock.tc.kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	msg, err := c.CreateMessage(context.Background(), &mock.space, &Conversation{
		ID:              "conv-1",
		ConversationTag: "conv-tag-1",
		SpaceID:         "space-1",
	}, WireRoleUser, "Hello!")
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if msg.ID != "msg-new" {
		t.Fatalf("msg ID = %q, want %q", msg.ID, "msg-new")
	}
	if msg.Role != WireRoleUser {
		t.Fatalf("Role = %d, want %d", msg.Role, WireRoleUser)
	}
	if msg.MessageTag == "" {
		t.Fatal("MessageTag is empty")
	}
	if msg.Encrypted == "" {
		t.Fatal("Encrypted is empty — content should be encrypted")
	}

	// Verify we can decrypt the content.
	dek, err := mock.tc.deriveSpaceDEK(t)
	if err != nil {
		t.Fatalf("derive DEK: %v", err)
	}
	ad := MessageAD(msg.MessageTag, "user", "", "conv-tag-1")
	privJSON, err := DecryptString(msg.Encrypted, dek, ad)
	if err != nil {
		t.Fatalf("decrypt content: %v", err)
	}
	var priv map[string]string
	if err := json.Unmarshal([]byte(privJSON), &priv); err != nil {
		t.Fatalf("unmarshal priv: %v", err)
	}
	if priv["content"] != "Hello!" {
		t.Fatalf("content = %q, want %q", priv["content"], "Hello!")
	}
}

func TestGetMessage_HappyPath(t *testing.T) {
	mock := newCRUDMockServer(t)
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = mock.tc.kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	msg, err := c.GetMessage(context.Background(), "msg-1")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if msg.ID != "msg-1" {
		t.Fatalf("msg ID = %q, want %q", msg.ID, "msg-1")
	}
	if msg.ConversationID != "conv-1" {
		t.Fatalf("ConversationID = %q, want %q", msg.ConversationID, "conv-1")
	}
	if msg.Role != WireRoleUser {
		t.Fatalf("Role = %d, want %d", msg.Role, WireRoleUser)
	}
}
