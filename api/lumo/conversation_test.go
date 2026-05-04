package lumo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// crudMockServer provides a mock server that handles masterkeys, spaces,
// conversations, and messages endpoints for integration-style tests.
type crudMockServer struct {
	tc    *testCryptoChain
	space Space
	t     *testing.T
}

func newCRUDMockServer(t *testing.T) *crudMockServer {
	t.Helper()
	tc := newTestCryptoChain(t)
	space := tc.makeEncryptedSpace(t, "space-1", "space-tag-1", false)
	return &crudMockServer{tc: tc, space: space, t: t}
}

func (m *crudMockServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/api/lumo/v1/masterkeys":
			m.tc.masterKeyHandler(m.t)(w, r)

		case path == "/api/lumo/v1/spaces/space-1" && r.Method == "GET":
			writeJSON(m.t, w, GetSpaceResponse{Code: 1000, Space: m.space})

		case strings.HasPrefix(path, "/api/lumo/v1/spaces/space-1/conversations") && r.Method == "POST":
			var req CreateConversationReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				m.t.Errorf("decode conversation req: %v", err)
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			writeJSON(m.t, w, GetConversationResponse{
				Code: 1000,
				Conversation: Conversation{
					ID:              "conv-new",
					SpaceID:         "space-1",
					ConversationTag: req.ConversationTag,
					Encrypted:       req.Encrypted,
					CreateTime:      "2024-01-01T00:00:00Z",
				},
			})

		case strings.HasPrefix(path, "/api/lumo/v1/conversations/conv-1") && r.Method == "GET":
			writeJSON(m.t, w, GetConversationResponse{
				Code: 1000,
				Conversation: Conversation{
					ID:              "conv-1",
					SpaceID:         "space-1",
					ConversationTag: "conv-tag-1",
					CreateTime:      "2024-01-01T00:00:00Z",
				},
			})

		case strings.HasPrefix(path, "/api/lumo/v1/conversations/") && r.Method == "DELETE":
			writeJSON(m.t, w, map[string]int{"Code": 1000})

		case strings.HasPrefix(path, "/api/lumo/v1/conversations/conv-1/messages") && r.Method == "POST":
			var req CreateMessageReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				m.t.Errorf("decode message req: %v", err)
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			writeJSON(m.t, w, GetMessageResponse{
				Code: 1000,
				Message: Message{
					ID:             "msg-new",
					ConversationID: "conv-1",
					MessageTag:     req.MessageTag,
					Role:           req.Role,
					Encrypted:      req.Encrypted,
					CreateTime:     "2024-01-01T00:00:00Z",
				},
			})

		case strings.HasPrefix(path, "/api/lumo/v1/messages/msg-1") && r.Method == "GET":
			writeJSON(m.t, w, GetMessageResponse{
				Code: 1000,
				Message: Message{
					ID:             "msg-1",
					ConversationID: "conv-1",
					MessageTag:     "msg-tag-1",
					Role:           1,
					CreateTime:     "2024-01-01T00:00:00Z",
				},
			})

		default:
			http.NotFound(w, r)
		}
	}
}

func TestCreateConversation_RequestBody(t *testing.T) {
	mock := newCRUDMockServer(t)
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = mock.tc.kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	conv, err := c.CreateConversation(context.Background(), &mock.space, "Hello World")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	if conv.ID != "conv-new" {
		t.Fatalf("conv ID = %q, want %q", conv.ID, "conv-new")
	}
	if conv.ConversationTag == "" {
		t.Fatal("ConversationTag is empty")
	}
	if conv.Encrypted == "" {
		t.Fatal("Encrypted is empty — title should be encrypted")
	}

	// Verify we can decrypt the title using the space's DEK.
	dek, err := mock.tc.deriveSpaceDEK(t)
	if err != nil {
		t.Fatalf("derive DEK: %v", err)
	}
	ad := ConversationAD(conv.ConversationTag, mock.space.SpaceTag)
	privJSON, err := DecryptString(conv.Encrypted, dek, ad)
	if err != nil {
		t.Fatalf("decrypt title: %v", err)
	}
	var priv map[string]string
	if err := json.Unmarshal([]byte(privJSON), &priv); err != nil {
		t.Fatalf("unmarshal priv: %v", err)
	}
	if priv["title"] != "Hello World" {
		t.Fatalf("title = %q, want %q", priv["title"], "Hello World")
	}
}

func TestGetConversation_HappyPath(t *testing.T) {
	mock := newCRUDMockServer(t)
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	sess := testSession(t)
	sess.UserKeyRing = mock.tc.kr
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	conv, err := c.GetConversation(context.Background(), "conv-1")
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if conv.ID != "conv-1" {
		t.Fatalf("conv ID = %q, want %q", conv.ID, "conv-1")
	}
	if conv.SpaceID != "space-1" {
		t.Fatalf("SpaceID = %q, want %q", conv.SpaceID, "space-1")
	}
}

func TestDeleteConversation_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		writeJSON(t, w, map[string]any{"Code": 2501, "Error": "deleted"})
	}))
	defer srv.Close()

	sess := testSession(t)
	c := NewClient(sess)
	c.BaseURL = srv.URL + "/api"

	err := c.DeleteConversation(context.Background(), "deleted-conv")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}
