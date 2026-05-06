package lumo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

func TestCreateRawMessage_SendsBodyVerbatim(t *testing.T) {
	// Capture the raw request body to verify it's sent as-is.
	var capturedReq CreateMessageReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		writeJSON(t, w, GetMessageResponse{
			Code: 1000,
			Message: Message{
				ID:             "msg-raw-1",
				ConversationID: capturedReq.ConversationID,
				MessageTag:     capturedReq.MessageTag,
				Role:           capturedReq.Role,
				Status:         capturedReq.Status,
				Encrypted:      capturedReq.Encrypted,
				ParentID:       capturedReq.ParentID,
				CreateTime:     "2024-01-01T00:00:00Z",
			},
		})
	}))
	defer srv.Close()

	sess := testSession(t)
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	// Use a pre-encrypted blob — CreateRawMessage must NOT transform it.
	preEncrypted := "already-encrypted-ciphertext-base64"
	req := CreateMessageReq{
		ConversationID: "conv-1",
		MessageTag:     "fresh-tag-abc",
		Role:           WireRoleAssistant,
		Status:         2,
		Encrypted:      preEncrypted,
		ParentID:       "",
	}

	msg, err := c.CreateRawMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateRawMessage: %v", err)
	}

	// Verify the request body was sent verbatim — no encryption applied.
	if capturedReq.Encrypted != preEncrypted {
		t.Fatalf("Encrypted sent = %q, want %q (verbatim)", capturedReq.Encrypted, preEncrypted)
	}
	if capturedReq.ConversationID != "conv-1" {
		t.Fatalf("ConversationID sent = %q, want %q", capturedReq.ConversationID, "conv-1")
	}
	if capturedReq.MessageTag != "fresh-tag-abc" {
		t.Fatalf("MessageTag sent = %q, want %q", capturedReq.MessageTag, "fresh-tag-abc")
	}
	if capturedReq.Role != WireRoleAssistant {
		t.Fatalf("Role sent = %d, want %d", capturedReq.Role, WireRoleAssistant)
	}
	if capturedReq.Status != 2 {
		t.Fatalf("Status sent = %d, want %d", capturedReq.Status, 2)
	}
	if capturedReq.ParentID != "" {
		t.Fatalf("ParentID sent = %q, want empty", capturedReq.ParentID)
	}

	// Verify the returned Message has expected fields populated.
	if msg.ID != "msg-raw-1" {
		t.Fatalf("msg.ID = %q, want %q", msg.ID, "msg-raw-1")
	}
	if msg.ConversationID != "conv-1" {
		t.Fatalf("msg.ConversationID = %q, want %q", msg.ConversationID, "conv-1")
	}
	if msg.MessageTag != "fresh-tag-abc" {
		t.Fatalf("msg.MessageTag = %q, want %q", msg.MessageTag, "fresh-tag-abc")
	}
	if msg.Role != WireRoleAssistant {
		t.Fatalf("msg.Role = %d, want %d", msg.Role, WireRoleAssistant)
	}
	if msg.Encrypted != preEncrypted {
		t.Fatalf("msg.Encrypted = %q, want %q", msg.Encrypted, preEncrypted)
	}
	if msg.CreateTime != "2024-01-01T00:00:00Z" {
		t.Fatalf("msg.CreateTime = %q, want %q", msg.CreateTime, "2024-01-01T00:00:00Z")
	}
}

func TestCreateRawMessage_ErrorPropagation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		writeJSON(t, w, map[string]any{"Code": 2501, "Error": "resource deleted"})
	}))
	defer srv.Close()

	sess := testSession(t)
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	_, err := c.CreateRawMessage(context.Background(), CreateMessageReq{
		ConversationID: "deleted-conv",
		MessageTag:     "tag-1",
		Role:           WireRoleUser,
		Encrypted:      "some-blob",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}
