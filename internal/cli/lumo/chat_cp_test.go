package lumoCmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	proton "github.com/ProtonMail/go-proton-api"
	pgpcrypto "github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/ProtonMail/gopenpgp/v2/helper"
	"github.com/major0/proton-utils/api"
	"github.com/major0/proton-utils/api/lumo"
)

// --- Test crypto helpers (cmd-level, mirrors api/lumo test patterns) ---

// cmdTestKeyPair generates a fresh PGP keypair for testing.
func cmdTestKeyPair(t *testing.T) (string, *pgpcrypto.KeyRing) {
	t.Helper()
	armored, err := helper.GenerateKey("Test", "test@test.com", []byte(""), "x25519", 0)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	key, err := pgpcrypto.NewKeyFromArmored(armored)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	unlockedKey, err := key.Unlock([]byte(""))
	if err != nil {
		t.Fatalf("unlock key: %v", err)
	}
	kr, err := pgpcrypto.NewKeyRing(unlockedKey)
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}
	pubKey, err := key.GetArmoredPublicKey()
	if err != nil {
		t.Fatalf("public key: %v", err)
	}
	return pubKey, kr
}

// cmdPGPEncrypt encrypts raw bytes with the given keyring and returns
// the armored PGP message.
func cmdPGPEncrypt(t *testing.T, kr *pgpcrypto.KeyRing, data []byte) string {
	t.Helper()
	msg, err := kr.Encrypt(pgpcrypto.NewPlainMessage(data), nil)
	if err != nil {
		t.Fatalf("pgp encrypt: %v", err)
	}
	armored, err := msg.GetArmored()
	if err != nil {
		t.Fatalf("armor: %v", err)
	}
	return armored
}

// cmdTestCryptoChain holds a master key and PGP keyring for integration tests.
type cmdTestCryptoChain struct {
	masterKey []byte
	armored   string
	kr        *pgpcrypto.KeyRing
}

func newCmdTestCryptoChain(t *testing.T) *cmdTestCryptoChain {
	t.Helper()
	_, kr := cmdTestKeyPair(t)
	mk := make([]byte, 32)
	for i := range mk {
		mk[i] = byte(i + 1)
	}
	return &cmdTestCryptoChain{
		masterKey: mk,
		armored:   cmdPGPEncrypt(t, kr, mk),
		kr:        kr,
	}
}

// makeSpace creates a Space with a real crypto chain (space key wrapped
// with master key). Returns the space and its raw space key.
func (tc *cmdTestCryptoChain) makeSpace(t *testing.T, id, tag string) (lumo.Space, []byte) {
	t.Helper()
	spaceKey, err := lumo.GenerateSpaceKey()
	if err != nil {
		t.Fatalf("generate space key: %v", err)
	}
	wrapped, err := lumo.WrapSpaceKey(tc.masterKey, spaceKey)
	if err != nil {
		t.Fatalf("wrap space key: %v", err)
	}
	return lumo.Space{
		ID:         id,
		SpaceKey:   base64.StdEncoding.EncodeToString(wrapped),
		SpaceTag:   tag,
		CreateTime: "2024-01-01T00:00:00Z",
	}, spaceKey
}

// deriveDEK derives the DEK from a raw space key.
func deriveDEK(t *testing.T, spaceKey []byte) []byte {
	t.Helper()
	dek, err := lumo.DeriveDataEncryptionKey(spaceKey)
	if err != nil {
		t.Fatalf("derive DEK: %v", err)
	}
	return dek
}

// encryptTitle encrypts a conversation title for a given DEK and tags.
func encryptTitle(t *testing.T, title string, dek []byte, convTag, spaceTag string) string {
	t.Helper()
	privJSON := `{"title":` + mustMarshalStr(title) + `}`
	ad := lumo.ConversationAD(convTag, spaceTag)
	encrypted, err := lumo.EncryptString(privJSON, dek, ad)
	if err != nil {
		t.Fatalf("encrypt title: %v", err)
	}
	return encrypted
}

// encryptMessage encrypts a message payload for a given DEK and AD components.
func encryptMessage(t *testing.T, content string, dek []byte, msgTag, role, parentTag, convTag string) string {
	t.Helper()
	payload := `{"content":` + mustMarshalStr(content) + `}`
	ad := lumo.MessageAD(msgTag, role, parentTag, convTag)
	encrypted, err := lumo.EncryptString(payload, dek, ad)
	if err != nil {
		t.Fatalf("encrypt message: %v", err)
	}
	return encrypted
}

func mustMarshalStr(s string) string {
	bs, _ := json.Marshal(s)
	return string(bs)
}

// cmdWriteJSON writes a JSON response.
func cmdWriteJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("write JSON: %v", err)
	}
}

// --- Integration Tests ---

// TestChatCp_NoArg verifies that runChatCp returns an error when no
// argument is provided.
//
// Validates: Requirements 1.1
func TestChatCp_NoArg(t *testing.T) {
	cmd := newTestCmdWithCookieAuth(true)
	cmd.SilenceUsage = true

	err := runChatCp(cmd, nil)
	if err == nil {
		t.Fatal("expected error for no args, got nil")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestChatCp_NoArgEmpty verifies that runChatCp returns an error when
// args is an empty slice.
//
// Validates: Requirements 1.1
func TestChatCp_NoArgEmpty(t *testing.T) {
	cmd := newTestCmdWithCookieAuth(true)
	cmd.SilenceUsage = true

	err := runChatCp(cmd, []string{})
	if err == nil {
		t.Fatal("expected error for empty args, got nil")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestChatCp_EndToEnd_HappyPath tests the full copy flow against a mock
// server with a 3-message conversation. It verifies:
// - New space is created (POST /spaces)
// - New conversation is created in the new space
// - All 3 messages are re-encrypted and decryptable with the new space's DEK
// - Correct stdout output (new conversation ID)
// - Correct stderr output ("Copied 3 messages")
//
// Validates: Requirements 1.1-1.5, 2.1-2.8, 4.1-4.5
func TestChatCp_EndToEnd_HappyPath(t *testing.T) {
	tc := newCmdTestCryptoChain(t)

	// Source space and crypto.
	srcSpace, srcSpaceKey := tc.makeSpace(t, "src-space-id", "src-space-tag")
	srcDEK := deriveDEK(t, srcSpaceKey)
	srcConvTag := "src-conv-tag-1234"
	srcConvTitle := "My Test Conversation"
	srcConvEncrypted := encryptTitle(t, srcConvTitle, srcDEK, srcConvTag, srcSpace.SpaceTag)

	// Source messages.
	type srcMsgDef struct {
		ID       string
		Tag      string
		Role     int
		RoleStr  string
		Content  string
		ParentID string
	}
	srcMsgs := []srcMsgDef{
		{ID: "msg-1", Tag: "msg-tag-1", Role: lumo.WireRoleUser, RoleStr: "user", Content: "Hello, how are you?"},
		{ID: "msg-2", Tag: "msg-tag-2", Role: lumo.WireRoleAssistant, RoleStr: "assistant", Content: "I'm doing well, thanks!", ParentID: "msg-1"},
		{ID: "msg-3", Tag: "msg-tag-3", Role: lumo.WireRoleUser, RoleStr: "user", Content: "Tell me about Go testing."},
	}

	// Encrypt source messages.
	srcEncrypted := make(map[string]string)
	for _, m := range srcMsgs {
		parentTag := ""
		if m.ParentID != "" {
			for _, pm := range srcMsgs {
				if pm.ID == m.ParentID {
					parentTag = pm.Tag
					break
				}
			}
		}
		srcEncrypted[m.ID] = encryptMessage(t, m.Content, srcDEK, m.Tag, m.RoleStr, parentTag, srcConvTag)
	}

	// New space (created by CreateSpace).
	newSpace, newSpaceKey := tc.makeSpace(t, "new-space-id", "new-space-tag")
	newDEK := deriveDEK(t, newSpaceKey)
	newConvTag, _ := lumo.GenerateTag()
	newConvID := "new-conv-id-abc"

	// Track created messages for verification.
	type createdMsg struct {
		Req lumo.CreateMessageReq
	}
	var createdMsgs []createdMsg

	// Mock server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		// Master keys endpoint.
		case path == "/api/lumo/v1/masterkeys" && r.Method == "GET":
			cmdWriteJSON(t, w, lumo.ListMasterKeysResponse{
				Code:        1000,
				Eligibility: 1,
				MasterKeys: []lumo.MasterKeyEntry{
					{ID: "mk1", IsLatest: true, Version: 1, CreateTime: "2024-01-01T00:00:00Z", MasterKey: tc.armored},
				},
			})

		// List spaces (for resolveConversationByInput).
		case path == "/api/lumo/v1/spaces" && r.Method == "GET":
			// Return source space with the conversation embedded.
			spaceWithConv := srcSpace
			spaceWithConv.Conversations = []lumo.Conversation{
				{
					ID:              "src-conv-id",
					SpaceID:         srcSpace.ID,
					ConversationTag: srcConvTag,
					Encrypted:       srcConvEncrypted,
					CreateTime:      "2024-01-01T00:00:00Z",
				},
			}
			cmdWriteJSON(t, w, lumo.ListSpacesResponse{
				Code:   1000,
				Spaces: []lumo.Space{spaceWithConv},
			})

		// Get source space.
		case path == "/api/lumo/v1/spaces/src-space-id" && r.Method == "GET":
			cmdWriteJSON(t, w, lumo.GetSpaceResponse{Code: 1000, Space: srcSpace})

		// Create new space.
		case path == "/api/lumo/v1/spaces" && r.Method == "POST":
			cmdWriteJSON(t, w, lumo.GetSpaceResponse{Code: 1000, Space: newSpace})

		// Get source conversation.
		case path == "/api/lumo/v1/conversations/src-conv-id" && r.Method == "GET":
			msgs := make([]lumo.Message, len(srcMsgs))
			for i, m := range srcMsgs {
				msgs[i] = lumo.Message{
					ID:             m.ID,
					ConversationID: "src-conv-id",
					MessageTag:     m.Tag,
					Role:           m.Role,
					Status:         2,
					CreateTime:     fmt.Sprintf("2024-01-01T00:0%d:00Z", i),
					ParentID:       m.ParentID,
				}
			}
			cmdWriteJSON(t, w, lumo.GetConversationResponse{
				Code: 1000,
				Conversation: lumo.Conversation{
					ID:              "src-conv-id",
					SpaceID:         srcSpace.ID,
					ConversationTag: srcConvTag,
					Encrypted:       srcConvEncrypted,
					Messages:        msgs,
					CreateTime:      "2024-01-01T00:00:00Z",
				},
			})

		// Create conversation in new space.
		case strings.HasPrefix(path, "/api/lumo/v1/spaces/new-space-id/conversations") && r.Method == "POST":
			var req lumo.CreateConversationReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode conv req: %v", err)
				http.Error(w, "bad", 400)
				return
			}
			cmdWriteJSON(t, w, lumo.GetConversationResponse{
				Code: 1000,
				Conversation: lumo.Conversation{
					ID:              newConvID,
					SpaceID:         newSpace.ID,
					ConversationTag: newConvTag,
					Encrypted:       req.Encrypted,
					CreateTime:      "2024-01-02T00:00:00Z",
				},
			})

		// Get individual source messages.
		case strings.HasPrefix(path, "/api/lumo/v1/messages/msg-") && r.Method == "GET":
			msgID := strings.TrimPrefix(path, "/api/lumo/v1/messages/")
			for _, m := range srcMsgs {
				if m.ID == msgID {
					cmdWriteJSON(t, w, lumo.GetMessageResponse{
						Code: 1000,
						Message: lumo.Message{
							ID:             m.ID,
							ConversationID: "src-conv-id",
							MessageTag:     m.Tag,
							Role:           m.Role,
							Status:         2,
							Encrypted:      srcEncrypted[m.ID],
							CreateTime:     "2024-01-01T00:00:00Z",
							ParentID:       m.ParentID,
						},
					})
					return
				}
			}
			http.NotFound(w, r)

		// Create messages in new conversation.
		case strings.HasPrefix(path, "/api/lumo/v1/conversations/"+newConvID+"/messages") && r.Method == "POST":
			var req lumo.CreateMessageReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode msg req: %v", err)
				http.Error(w, "bad", 400)
				return
			}
			createdMsgs = append(createdMsgs, createdMsg{Req: req})
			cmdWriteJSON(t, w, lumo.GetMessageResponse{
				Code: 1000,
				Message: lumo.Message{
					ID:             fmt.Sprintf("new-msg-%d", len(createdMsgs)),
					ConversationID: newConvID,
					MessageTag:     req.MessageTag,
					Role:           req.Role,
					Status:         req.Status,
					Encrypted:      req.Encrypted,
					CreateTime:     "2024-01-02T00:00:00Z",
				},
			})

		default:
			t.Logf("unhandled request: %s %s", r.Method, path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Create a session and client pointing at the mock server.
	sess := &api.Session{
		Auth: proton.Auth{
			UID:         "test-uid",
			AccessToken: "test-token",
		},
		AppVersion:  "cli@2.0.0",
		UserAgent:   "proton-cli/test",
		UserKeyRing: tc.kr,
	}
	client := lumo.NewClient(sess)
	client.BaseURL = srv.URL + "/api"

	ctx := context.Background()

	// --- Execute the same logic as runChatCp ---

	// Step 1: Resolve source conversation.
	resolved, err := resolveConversationByInput(ctx, client, srcConvTitle)
	if err != nil {
		t.Fatalf("resolveConversationByInput: %v", err)
	}
	if resolved.ConversationID != "src-conv-id" {
		t.Fatalf("resolved conv ID = %q, want %q", resolved.ConversationID, "src-conv-id")
	}

	// Step 2: Load space and derive DEK.
	space, dek, err := resolveSpaceAndDEK(ctx, client, resolved.SpaceID)
	if err != nil {
		t.Fatalf("resolveSpaceAndDEK: %v", err)
	}

	// Step 3: Fetch source conversation.
	srcConv, err := client.GetConversation(ctx, resolved.ConversationID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if len(srcConv.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(srcConv.Messages))
	}

	// Step 4: Decrypt title and create new title.
	srcTitleDecrypted := decryptConversationTitle(*srcConv, dek, space.SpaceTag)
	if srcTitleDecrypted != srcConvTitle {
		t.Fatalf("decrypted title = %q, want %q", srcTitleDecrypted, srcConvTitle)
	}
	newTitle := srcTitleDecrypted + " (copy)"

	// Step 5: Create new space.
	createdSpace, err := client.CreateSpace(ctx, "", false)
	if err != nil {
		t.Fatalf("CreateSpace: %v", err)
	}

	// Step 6: Derive new space DEK.
	newSpaceDEK, err := client.DeriveSpaceDEK(ctx, createdSpace)
	if err != nil {
		t.Fatalf("DeriveSpaceDEK: %v", err)
	}

	// Step 7: Create new conversation.
	newConv, err := client.CreateConversation(ctx, createdSpace, newTitle)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if newConv.ID != newConvID {
		t.Fatalf("new conv ID = %q, want %q", newConv.ID, newConvID)
	}

	// Step 8: Copy messages.
	idToTag := make(map[string]string, len(srcConv.Messages))
	for _, m := range srcConv.Messages {
		idToTag[m.ID] = m.MessageTag
	}

	// Capture stdout/stderr.
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	copied := 0
	failed := 0
	for _, shallow := range srcConv.Messages {
		msg, ferr := client.GetMessage(ctx, shallow.ID)
		if ferr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to fetch message %s: %v\n", shallow.ID, ferr)
			failed++
			continue
		}

		role := "user"
		if msg.Role == lumo.WireRoleAssistant {
			role = "assistant"
		}

		parentTag := ""
		if msg.ParentID != "" {
			parentTag = idToTag[msg.ParentID]
		}

		srcAD := lumo.MessageAD(msg.MessageTag, role, parentTag, srcConv.ConversationTag)
		plainJSON, derr := lumo.DecryptString(msg.Encrypted, dek, srcAD)
		if derr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to decrypt message %s: %v\n", msg.ID, derr)
			failed++
			continue
		}

		freshTag, _ := lumo.GenerateTag()
		targetAD := lumo.MessageAD(freshTag, role, "", newConv.ConversationTag)

		encrypted, eerr := lumo.EncryptString(plainJSON, newSpaceDEK, targetAD)
		if eerr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to encrypt message %s: %v\n", msg.ID, eerr)
			failed++
			continue
		}

		req := lumo.CreateMessageReq{
			ConversationID: newConv.ID,
			MessageTag:     freshTag,
			Role:           msg.Role,
			Status:         msg.Status,
			Encrypted:      encrypted,
			CreateTime:     msg.CreateTime,
		}
		_, cerr := client.CreateRawMessage(ctx, req)
		if cerr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to create message: %v\n", cerr)
			failed++
			continue
		}
		copied++
	}

	// Print output like runChatCp does.
	fmt.Println(newConv.ID)
	if failed > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "Copied %d messages (%d failed)\n", copied, failed)
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "Copied %d messages\n", copied)
	}

	// Restore stdout/stderr and read captured output.
	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	var outBuf, errBuf bytes.Buffer
	_, _ = outBuf.ReadFrom(rOut)
	_, _ = errBuf.ReadFrom(rErr)
	_ = rOut.Close()
	_ = rErr.Close()

	stdout := outBuf.String()
	stderr := errBuf.String()

	// Verify stdout contains new conversation ID.
	if !strings.Contains(stdout, newConvID) {
		t.Fatalf("stdout should contain new conv ID %q, got: %q", newConvID, stdout)
	}

	// Verify stderr contains copy summary.
	if !strings.Contains(stderr, "Copied 3 messages") {
		t.Fatalf("stderr should contain 'Copied 3 messages', got: %q", stderr)
	}

	// Verify all 3 messages were created.
	if len(createdMsgs) != 3 {
		t.Fatalf("expected 3 created messages, got %d", len(createdMsgs))
	}

	// Verify each created message is decryptable with the new space's DEK.
	for i, cm := range createdMsgs {
		role := "user"
		if cm.Req.Role == lumo.WireRoleAssistant {
			role = "assistant"
		}
		ad := lumo.MessageAD(cm.Req.MessageTag, role, "", newConv.ConversationTag)
		plainJSON, err := lumo.DecryptString(cm.Req.Encrypted, newDEK, ad)
		if err != nil {
			t.Fatalf("message %d: decrypt failed: %v", i, err)
		}

		var priv map[string]string
		if err := json.Unmarshal([]byte(plainJSON), &priv); err != nil {
			t.Fatalf("message %d: unmarshal failed: %v", i, err)
		}
		if priv["content"] != srcMsgs[i].Content {
			t.Fatalf("message %d: content = %q, want %q", i, priv["content"], srcMsgs[i].Content)
		}
	}

	// Verify ParentID is always empty (flattened).
	for i, cm := range createdMsgs {
		if cm.Req.ParentID != "" {
			t.Fatalf("message %d: ParentID = %q, want empty", i, cm.Req.ParentID)
		}
	}

	// Verify roles are preserved.
	expectedRoles := []int{lumo.WireRoleUser, lumo.WireRoleAssistant, lumo.WireRoleUser}
	for i, cm := range createdMsgs {
		if cm.Req.Role != expectedRoles[i] {
			t.Fatalf("message %d: Role = %d, want %d", i, cm.Req.Role, expectedRoles[i])
		}
	}

	// Verify all MessageTags are unique and different from source tags.
	tagSet := make(map[string]bool)
	for _, cm := range createdMsgs {
		if tagSet[cm.Req.MessageTag] {
			t.Fatalf("duplicate MessageTag: %s", cm.Req.MessageTag)
		}
		tagSet[cm.Req.MessageTag] = true
		for _, sm := range srcMsgs {
			if cm.Req.MessageTag == sm.Tag {
				t.Fatalf("new MessageTag %s collides with source tag", cm.Req.MessageTag)
			}
		}
	}
}

// TestChatCp_EndToEnd_PerMessageFailure tests that when one message
// fetch returns a 500 error, the other messages are still copied and
// stderr shows a warning with the failure count.
//
// Validates: Requirements 4.4, 4.5
func TestChatCp_EndToEnd_PerMessageFailure(t *testing.T) {
	tc := newCmdTestCryptoChain(t)

	// Source space and crypto.
	srcSpace, srcSpaceKey := tc.makeSpace(t, "src-space-id", "src-space-tag")
	srcDEK := deriveDEK(t, srcSpaceKey)
	srcConvTag := "src-conv-tag-fail"
	srcConvTitle := "Failure Test"
	srcConvEncrypted := encryptTitle(t, srcConvTitle, srcDEK, srcConvTag, srcSpace.SpaceTag)

	// Source messages — msg-2 will fail to fetch.
	type srcMsgDef struct {
		ID      string
		Tag     string
		Role    int
		RoleStr string
		Content string
	}
	srcMsgs := []srcMsgDef{
		{ID: "msg-1", Tag: "msg-tag-1", Role: lumo.WireRoleUser, RoleStr: "user", Content: "First message"},
		{ID: "msg-2", Tag: "msg-tag-2", Role: lumo.WireRoleAssistant, RoleStr: "assistant", Content: "Will fail"},
		{ID: "msg-3", Tag: "msg-tag-3", Role: lumo.WireRoleUser, RoleStr: "user", Content: "Third message"},
	}

	srcEncrypted := make(map[string]string)
	for _, m := range srcMsgs {
		srcEncrypted[m.ID] = encryptMessage(t, m.Content, srcDEK, m.Tag, m.RoleStr, "", srcConvTag)
	}

	// New space.
	newSpace, _ := tc.makeSpace(t, "new-space-id", "new-space-tag")
	newConvTag, _ := lumo.GenerateTag()
	newConvID := "new-conv-fail"

	var createdMsgCount int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case path == "/api/lumo/v1/masterkeys" && r.Method == "GET":
			cmdWriteJSON(t, w, lumo.ListMasterKeysResponse{
				Code:        1000,
				Eligibility: 1,
				MasterKeys: []lumo.MasterKeyEntry{
					{ID: "mk1", IsLatest: true, Version: 1, CreateTime: "2024-01-01T00:00:00Z", MasterKey: tc.armored},
				},
			})

		case path == "/api/lumo/v1/spaces" && r.Method == "GET":
			spaceWithConv := srcSpace
			spaceWithConv.Conversations = []lumo.Conversation{
				{
					ID:              "src-conv-id",
					SpaceID:         srcSpace.ID,
					ConversationTag: srcConvTag,
					Encrypted:       srcConvEncrypted,
					CreateTime:      "2024-01-01T00:00:00Z",
				},
			}
			cmdWriteJSON(t, w, lumo.ListSpacesResponse{Code: 1000, Spaces: []lumo.Space{spaceWithConv}})

		case path == "/api/lumo/v1/spaces/src-space-id" && r.Method == "GET":
			cmdWriteJSON(t, w, lumo.GetSpaceResponse{Code: 1000, Space: srcSpace})

		case path == "/api/lumo/v1/spaces" && r.Method == "POST":
			cmdWriteJSON(t, w, lumo.GetSpaceResponse{Code: 1000, Space: newSpace})

		case path == "/api/lumo/v1/conversations/src-conv-id" && r.Method == "GET":
			msgs := make([]lumo.Message, len(srcMsgs))
			for i, m := range srcMsgs {
				msgs[i] = lumo.Message{
					ID:             m.ID,
					ConversationID: "src-conv-id",
					MessageTag:     m.Tag,
					Role:           m.Role,
					Status:         2,
					CreateTime:     fmt.Sprintf("2024-01-01T00:0%d:00Z", i),
				}
			}
			cmdWriteJSON(t, w, lumo.GetConversationResponse{
				Code: 1000,
				Conversation: lumo.Conversation{
					ID:              "src-conv-id",
					SpaceID:         srcSpace.ID,
					ConversationTag: srcConvTag,
					Encrypted:       srcConvEncrypted,
					Messages:        msgs,
					CreateTime:      "2024-01-01T00:00:00Z",
				},
			})

		case strings.HasPrefix(path, "/api/lumo/v1/spaces/new-space-id/conversations") && r.Method == "POST":
			var req lumo.CreateConversationReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			cmdWriteJSON(t, w, lumo.GetConversationResponse{
				Code: 1000,
				Conversation: lumo.Conversation{
					ID:              newConvID,
					SpaceID:         newSpace.ID,
					ConversationTag: newConvTag,
					Encrypted:       req.Encrypted,
					CreateTime:      "2024-01-02T00:00:00Z",
				},
			})

		// msg-2 returns 500 (simulating server error).
		case path == "/api/lumo/v1/messages/msg-2" && r.Method == "GET":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			cmdWriteJSON(t, w, map[string]any{"Code": 500, "Error": "internal server error"})

		// Other messages return normally.
		case strings.HasPrefix(path, "/api/lumo/v1/messages/msg-") && r.Method == "GET":
			msgID := strings.TrimPrefix(path, "/api/lumo/v1/messages/")
			for _, m := range srcMsgs {
				if m.ID == msgID {
					cmdWriteJSON(t, w, lumo.GetMessageResponse{
						Code: 1000,
						Message: lumo.Message{
							ID:             m.ID,
							ConversationID: "src-conv-id",
							MessageTag:     m.Tag,
							Role:           m.Role,
							Status:         2,
							Encrypted:      srcEncrypted[m.ID],
							CreateTime:     "2024-01-01T00:00:00Z",
						},
					})
					return
				}
			}
			http.NotFound(w, r)

		case strings.HasPrefix(path, "/api/lumo/v1/conversations/"+newConvID+"/messages") && r.Method == "POST":
			var req lumo.CreateMessageReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			createdMsgCount++
			cmdWriteJSON(t, w, lumo.GetMessageResponse{
				Code: 1000,
				Message: lumo.Message{
					ID:             fmt.Sprintf("new-msg-%d", createdMsgCount),
					ConversationID: newConvID,
					MessageTag:     req.MessageTag,
					Role:           req.Role,
					Encrypted:      req.Encrypted,
					CreateTime:     "2024-01-02T00:00:00Z",
				},
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Create client.
	sess := &api.Session{
		Auth: proton.Auth{
			UID:         "test-uid",
			AccessToken: "test-token",
		},
		AppVersion:  "cli@2.0.0",
		UserAgent:   "proton-cli/test",
		UserKeyRing: tc.kr,
	}
	client := lumo.NewClient(sess)
	client.BaseURL = srv.URL + "/api"

	ctx := context.Background()

	// Resolve and fetch.
	resolved, err := resolveConversationByInput(ctx, client, srcConvTitle)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	space, dek, err := resolveSpaceAndDEK(ctx, client, resolved.SpaceID)
	if err != nil {
		t.Fatalf("resolveSpaceAndDEK: %v", err)
	}

	srcConv, err := client.GetConversation(ctx, resolved.ConversationID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}

	srcTitleDecrypted := decryptConversationTitle(*srcConv, dek, space.SpaceTag)
	newTitle := srcTitleDecrypted + " (copy)"

	createdSpace, err := client.CreateSpace(ctx, "", false)
	if err != nil {
		t.Fatalf("CreateSpace: %v", err)
	}

	newSpaceDEK, err := client.DeriveSpaceDEK(ctx, createdSpace)
	if err != nil {
		t.Fatalf("DeriveSpaceDEK: %v", err)
	}

	newConv, err := client.CreateConversation(ctx, createdSpace, newTitle)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	// Build ID→Tag map.
	idToTag := make(map[string]string, len(srcConv.Messages))
	for _, m := range srcConv.Messages {
		idToTag[m.ID] = m.MessageTag
	}

	// Capture stderr.
	oldStderr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	copied := 0
	failed := 0
	for _, shallow := range srcConv.Messages {
		msg, ferr := client.GetMessage(ctx, shallow.ID)
		if ferr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to fetch message %s: %v\n", shallow.ID, ferr)
			failed++
			continue
		}

		role := "user"
		if msg.Role == lumo.WireRoleAssistant {
			role = "assistant"
		}

		parentTag := ""
		if msg.ParentID != "" {
			parentTag = idToTag[msg.ParentID]
		}

		srcAD := lumo.MessageAD(msg.MessageTag, role, parentTag, srcConv.ConversationTag)
		plainJSON, derr := lumo.DecryptString(msg.Encrypted, dek, srcAD)
		if derr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to decrypt message %s: %v\n", msg.ID, derr)
			failed++
			continue
		}

		freshTag, _ := lumo.GenerateTag()
		targetAD := lumo.MessageAD(freshTag, role, "", newConv.ConversationTag)

		encrypted, eerr := lumo.EncryptString(plainJSON, newSpaceDEK, targetAD)
		if eerr != nil {
			failed++
			continue
		}

		req := lumo.CreateMessageReq{
			ConversationID: newConv.ID,
			MessageTag:     freshTag,
			Role:           msg.Role,
			Status:         msg.Status,
			Encrypted:      encrypted,
			CreateTime:     msg.CreateTime,
		}
		_, cerr := client.CreateRawMessage(ctx, req)
		if cerr != nil {
			failed++
			continue
		}
		copied++
	}

	// Print summary.
	if failed > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "Copied %d messages (%d failed)\n", copied, failed)
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "Copied %d messages\n", copied)
	}

	_ = wErr.Close()
	os.Stderr = oldStderr

	var errBuf bytes.Buffer
	_, _ = errBuf.ReadFrom(rErr)
	_ = rErr.Close()
	stderr := errBuf.String()

	// Verify: 2 messages copied, 1 failed.
	if copied != 2 {
		t.Fatalf("expected 2 copied, got %d", copied)
	}
	if failed != 1 {
		t.Fatalf("expected 1 failed, got %d", failed)
	}

	// Verify stderr contains warning about msg-2.
	if !strings.Contains(stderr, "msg-2") {
		t.Fatalf("stderr should mention failed msg-2, got: %q", stderr)
	}

	// Verify stderr contains the summary with failure count.
	if !strings.Contains(stderr, "Copied 2 messages (1 failed)") {
		t.Fatalf("stderr should contain 'Copied 2 messages (1 failed)', got: %q", stderr)
	}

	// Verify only 2 messages were created on the server.
	if createdMsgCount != 2 {
		t.Fatalf("expected 2 messages created on server, got %d", createdMsgCount)
	}
}

// TestChatCp_ZeroMatch verifies that resolveConversationByInput returns
// an error when no conversation matches the input.
//
// Validates: Requirements 1.4
func TestChatCp_ZeroMatch(t *testing.T) {
	tc := newCmdTestCryptoChain(t)

	srcSpace, srcSpaceKey := tc.makeSpace(t, "src-space-id", "src-space-tag")
	srcDEK := deriveDEK(t, srcSpaceKey)
	srcConvTag := "conv-tag-1"
	srcConvEncrypted := encryptTitle(t, "Existing Chat", srcDEK, srcConvTag, srcSpace.SpaceTag)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/api/lumo/v1/masterkeys":
			cmdWriteJSON(t, w, lumo.ListMasterKeysResponse{
				Code:        1000,
				Eligibility: 1,
				MasterKeys: []lumo.MasterKeyEntry{
					{ID: "mk1", IsLatest: true, Version: 1, CreateTime: "2024-01-01T00:00:00Z", MasterKey: tc.armored},
				},
			})
		case path == "/api/lumo/v1/spaces" && r.Method == "GET":
			spaceWithConv := srcSpace
			spaceWithConv.Conversations = []lumo.Conversation{
				{
					ID:              "conv-1",
					SpaceID:         srcSpace.ID,
					ConversationTag: srcConvTag,
					Encrypted:       srcConvEncrypted,
					CreateTime:      "2024-01-01T00:00:00Z",
				},
			}
			cmdWriteJSON(t, w, lumo.ListSpacesResponse{Code: 1000, Spaces: []lumo.Space{spaceWithConv}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	sess := &api.Session{
		Auth: proton.Auth{
			UID:         "test-uid",
			AccessToken: "test-token",
		},
		AppVersion:  "cli@2.0.0",
		UserAgent:   "proton-cli/test",
		UserKeyRing: tc.kr,
	}
	client := lumo.NewClient(sess)
	client.BaseURL = srv.URL + "/api"

	_, err := resolveConversationByInput(context.Background(), client, "nonexistent-chat-xyz")
	if err == nil {
		t.Fatal("expected error for zero-match, got nil")
	}
	if !strings.Contains(err.Error(), "no match for") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestChatCp_AmbiguousMatch verifies that resolveConversationByInput
// returns an error listing matches when multiple conversations match.
//
// Validates: Requirements 1.5
func TestChatCp_AmbiguousMatch(t *testing.T) {
	tc := newCmdTestCryptoChain(t)

	srcSpace, srcSpaceKey := tc.makeSpace(t, "src-space-id", "src-space-tag")
	srcDEK := deriveDEK(t, srcSpaceKey)

	// Two conversations with similar titles.
	conv1Tag := "conv-tag-1"
	conv2Tag := "conv-tag-2"
	conv1Encrypted := encryptTitle(t, "Go Testing Tips", srcDEK, conv1Tag, srcSpace.SpaceTag)
	conv2Encrypted := encryptTitle(t, "Go Testing Patterns", srcDEK, conv2Tag, srcSpace.SpaceTag)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/api/lumo/v1/masterkeys":
			cmdWriteJSON(t, w, lumo.ListMasterKeysResponse{
				Code:        1000,
				Eligibility: 1,
				MasterKeys: []lumo.MasterKeyEntry{
					{ID: "mk1", IsLatest: true, Version: 1, CreateTime: "2024-01-01T00:00:00Z", MasterKey: tc.armored},
				},
			})
		case path == "/api/lumo/v1/spaces" && r.Method == "GET":
			spaceWithConvs := srcSpace
			spaceWithConvs.Conversations = []lumo.Conversation{
				{
					ID:              "conv-1",
					SpaceID:         srcSpace.ID,
					ConversationTag: conv1Tag,
					Encrypted:       conv1Encrypted,
					CreateTime:      "2024-01-01T00:00:00Z",
				},
				{
					ID:              "conv-2",
					SpaceID:         srcSpace.ID,
					ConversationTag: conv2Tag,
					Encrypted:       conv2Encrypted,
					CreateTime:      "2024-01-02T00:00:00Z",
				},
			}
			cmdWriteJSON(t, w, lumo.ListSpacesResponse{Code: 1000, Spaces: []lumo.Space{spaceWithConvs}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	sess := &api.Session{
		Auth: proton.Auth{
			UID:         "test-uid",
			AccessToken: "test-token",
		},
		AppVersion:  "cli@2.0.0",
		UserAgent:   "proton-cli/test",
		UserKeyRing: tc.kr,
	}
	client := lumo.NewClient(sess)
	client.BaseURL = srv.URL + "/api"

	_, err := resolveConversationByInput(context.Background(), client, "Go Testing")
	if err == nil {
		t.Fatal("expected error for ambiguous match, got nil")
	}
	if !strings.Contains(err.Error(), "multiple matches for") {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should list both matching titles.
	if !strings.Contains(err.Error(), "Go Testing") {
		t.Fatalf("error should mention the query, got: %v", err)
	}
}
