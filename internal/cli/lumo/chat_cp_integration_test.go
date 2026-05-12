package lumoCmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/lumo"
)

// integrationEnv holds the shared test environment for integration tests.
type integrationEnv struct {
	tc *cmdTestCryptoChain

	// Source space (simple).
	srcSpace    lumo.Space
	srcSpaceKey []byte
	srcDEK      []byte

	// Destination space (simple, existing with a conversation).
	destSpace    lumo.Space
	destSpaceKey []byte
	destDEK      []byte

	// Project space.
	projSpace    lumo.Space
	projSpaceKey []byte
	projDEK      []byte

	// Source conversations.
	srcConvTag       string
	srcConvTitle     string
	srcConvEncrypted string
	srcConvID        string

	// Project conversation (only reachable via qualified URI).
	projConvTag       string
	projConvTitle     string
	projConvEncrypted string
	projConvID        string

	// Destination existing conversation.
	destConvTag       string
	destConvTitle     string
	destConvEncrypted string
	destConvID        string

	// Source messages.
	srcMsgs      []srcMsgDef
	srcEncrypted map[string]string
}

// srcMsgDef defines a source message for integration tests.
type srcMsgDef struct {
	ID       string
	Tag      string
	Role     int
	RoleStr  string
	Content  string
	ParentID string
}

// newIntegrationEnv creates a full test environment with multiple spaces
// and conversations for integration testing.
func newIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()
	tc := newCmdTestCryptoChain(t)

	// Source simple space.
	srcSpace, srcSpaceKey := tc.makeSpace(t, "src-simple-id", "src-simple-tag")
	srcDEK := deriveDEK(t, srcSpaceKey)

	// Encrypt simple space metadata (empty JSON = simple).
	srcSpaceAD := lumo.SpaceAD(srcSpace.SpaceTag)
	srcSpaceEnc, err := lumo.EncryptString("{}", srcDEK, srcSpaceAD)
	if err != nil {
		t.Fatalf("encrypt src space metadata: %v", err)
	}
	srcSpace.Encrypted = srcSpaceEnc

	// Destination simple space (already has a conversation).
	destSpace, destSpaceKey := tc.makeSpace(t, "dest-simple-id", "dest-simple-tag")
	destDEK := deriveDEK(t, destSpaceKey)
	destSpaceAD := lumo.SpaceAD(destSpace.SpaceTag)
	destSpaceEnc, err := lumo.EncryptString("{}", destDEK, destSpaceAD)
	if err != nil {
		t.Fatalf("encrypt dest space metadata: %v", err)
	}
	destSpace.Encrypted = destSpaceEnc

	// Project space.
	projSpace, projSpaceKey := tc.makeSpace(t, "proj-space-id", "proj-space-tag")
	projDEK := deriveDEK(t, projSpaceKey)
	projSpaceAD := lumo.SpaceAD(projSpace.SpaceTag)
	projPrivJSON := `{"isProject":true,"projectName":"My Project"}`
	projSpaceEnc, err := lumo.EncryptString(projPrivJSON, projDEK, projSpaceAD)
	if err != nil {
		t.Fatalf("encrypt proj space metadata: %v", err)
	}
	projSpace.Encrypted = projSpaceEnc

	// Source conversation in simple space.
	srcConvTag := "src-conv-tag-int"
	srcConvTitle := "Integration Chat"
	srcConvEncrypted := encryptTitle(t, srcConvTitle, srcDEK, srcConvTag, srcSpace.SpaceTag)
	srcConvID := "src-conv-id-int"

	// Add conversation to source space.
	srcSpace.Conversations = []lumo.Conversation{
		{
			ID:              srcConvID,
			SpaceID:         srcSpace.ID,
			ConversationTag: srcConvTag,
			Encrypted:       srcConvEncrypted,
			CreateTime:      "2024-01-01T00:00:00Z",
		},
	}

	// Project conversation.
	projConvTag := "proj-conv-tag-int"
	projConvTitle := "Project Chat"
	projConvEncrypted := encryptTitle(t, projConvTitle, projDEK, projConvTag, projSpace.SpaceTag)
	projConvID := "proj-conv-id-int"

	projSpace.Conversations = []lumo.Conversation{
		{
			ID:              projConvID,
			SpaceID:         projSpace.ID,
			ConversationTag: projConvTag,
			Encrypted:       projConvEncrypted,
			CreateTime:      "2024-01-02T00:00:00Z",
		},
	}

	// Destination existing conversation.
	destConvTag := "dest-conv-tag-int"
	destConvTitle := "Existing Dest Chat"
	destConvEncrypted := encryptTitle(t, destConvTitle, destDEK, destConvTag, destSpace.SpaceTag)
	destConvID := "dest-conv-id-int"

	destSpace.Conversations = []lumo.Conversation{
		{
			ID:              destConvID,
			SpaceID:         destSpace.ID,
			ConversationTag: destConvTag,
			Encrypted:       destConvEncrypted,
			CreateTime:      "2024-01-03T00:00:00Z",
		},
	}

	// Source messages.
	srcMsgs := []srcMsgDef{
		{ID: "int-msg-1", Tag: "int-msg-tag-1", Role: lumo.WireRoleUser, RoleStr: "user", Content: "Hello from integration"},
		{ID: "int-msg-2", Tag: "int-msg-tag-2", Role: lumo.WireRoleAssistant, RoleStr: "assistant", Content: "Hi there!", ParentID: "int-msg-1"},
	}

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

	return &integrationEnv{
		tc:                tc,
		srcSpace:          srcSpace,
		srcSpaceKey:       srcSpaceKey,
		srcDEK:            srcDEK,
		destSpace:         destSpace,
		destSpaceKey:      destSpaceKey,
		destDEK:           destDEK,
		projSpace:         projSpace,
		projSpaceKey:      projSpaceKey,
		projDEK:           projDEK,
		srcConvTag:        srcConvTag,
		srcConvTitle:      srcConvTitle,
		srcConvEncrypted:  srcConvEncrypted,
		srcConvID:         srcConvID,
		projConvTag:       projConvTag,
		projConvTitle:     projConvTitle,
		projConvEncrypted: projConvEncrypted,
		projConvID:        projConvID,
		destConvTag:       destConvTag,
		destConvTitle:     destConvTitle,
		destConvEncrypted: destConvEncrypted,
		destConvID:        destConvID,
		srcMsgs:           srcMsgs,
		srcEncrypted:      srcEncrypted,
	}
}

// integrationServer creates a mock HTTP server for the given environment.
// newConvID is the ID returned when creating a new conversation.
// newSpaceID/newSpaceTag are used when creating a new space.
// createdMsgs collects all CreateRawMessage requests.
func (env *integrationEnv) integrationServer(
	t *testing.T,
	newConvID string,
	newConvTag string,
	newSpace *lumo.Space,
	createdMsgs *[]lumo.CreateMessageReq,
) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case path == "/api/lumo/v1/masterkeys" && r.Method == "GET":
			cmdWriteJSON(t, w, lumo.ListMasterKeysResponse{
				Code:        1000,
				Eligibility: 1,
				MasterKeys: []lumo.MasterKeyEntry{
					{ID: "mk1", IsLatest: true, Version: 1, CreateTime: "2024-01-01T00:00:00Z", MasterKey: env.tc.armored},
				},
			})

		case path == "/api/lumo/v1/spaces" && r.Method == "GET":
			cmdWriteJSON(t, w, lumo.ListSpacesResponse{
				Code:   1000,
				Spaces: []lumo.Space{env.srcSpace, env.destSpace, env.projSpace},
			})

		case path == "/api/lumo/v1/spaces/src-simple-id" && r.Method == "GET":
			cmdWriteJSON(t, w, lumo.GetSpaceResponse{Code: 1000, Space: env.srcSpace})

		case path == "/api/lumo/v1/spaces/dest-simple-id" && r.Method == "GET":
			cmdWriteJSON(t, w, lumo.GetSpaceResponse{Code: 1000, Space: env.destSpace})

		case path == "/api/lumo/v1/spaces/proj-space-id" && r.Method == "GET":
			cmdWriteJSON(t, w, lumo.GetSpaceResponse{Code: 1000, Space: env.projSpace})

		case path == "/api/lumo/v1/spaces" && r.Method == "POST":
			if newSpace != nil {
				cmdWriteJSON(t, w, lumo.GetSpaceResponse{Code: 1000, Space: *newSpace})
			} else {
				http.Error(w, "no new space configured", 500)
			}

		case path == "/api/lumo/v1/conversations/"+env.srcConvID && r.Method == "GET":
			msgs := make([]lumo.Message, len(env.srcMsgs))
			for i, m := range env.srcMsgs {
				msgs[i] = lumo.Message{
					ID:             m.ID,
					ConversationID: env.srcConvID,
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
					ID:              env.srcConvID,
					SpaceID:         env.srcSpace.ID,
					ConversationTag: env.srcConvTag,
					Encrypted:       env.srcConvEncrypted,
					Messages:        msgs,
					CreateTime:      "2024-01-01T00:00:00Z",
				},
			})

		case path == "/api/lumo/v1/conversations/"+env.projConvID && r.Method == "GET":
			cmdWriteJSON(t, w, lumo.GetConversationResponse{
				Code: 1000,
				Conversation: lumo.Conversation{
					ID:              env.projConvID,
					SpaceID:         env.projSpace.ID,
					ConversationTag: env.projConvTag,
					Encrypted:       env.projConvEncrypted,
					Messages: []lumo.Message{
						{
							ID:             "proj-msg-1",
							ConversationID: env.projConvID,
							MessageTag:     "proj-msg-tag-1",
							Role:           lumo.WireRoleUser,
							Status:         2,
							CreateTime:     "2024-01-02T00:00:00Z",
						},
					},
					CreateTime: "2024-01-02T00:00:00Z",
				},
			})

		// Create conversation in any space.
		case strings.HasSuffix(path, "/conversations") && r.Method == "POST":
			var req lumo.CreateConversationReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode conv req: %v", err)
				http.Error(w, "bad", 400)
				return
			}
			// Determine which space this is for.
			spaceID := ""
			for _, prefix := range []string{
				"/api/lumo/v1/spaces/src-simple-id/conversations",
				"/api/lumo/v1/spaces/dest-simple-id/conversations",
				"/api/lumo/v1/spaces/proj-space-id/conversations",
			} {
				if path == prefix {
					spaceID = strings.TrimPrefix(prefix, "/api/lumo/v1/spaces/")
					spaceID = strings.TrimSuffix(spaceID, "/conversations")
					break
				}
			}
			if newSpace != nil && strings.Contains(path, newSpace.ID) {
				spaceID = newSpace.ID
			}
			cmdWriteJSON(t, w, lumo.GetConversationResponse{
				Code: 1000,
				Conversation: lumo.Conversation{
					ID:              newConvID,
					SpaceID:         spaceID,
					ConversationTag: newConvTag,
					Encrypted:       req.Encrypted,
					CreateTime:      "2024-02-01T00:00:00Z",
				},
			})

		// Get individual messages.
		case strings.HasPrefix(path, "/api/lumo/v1/messages/") && r.Method == "GET":
			msgID := strings.TrimPrefix(path, "/api/lumo/v1/messages/")
			for _, m := range env.srcMsgs {
				if m.ID == msgID {
					cmdWriteJSON(t, w, lumo.GetMessageResponse{
						Code: 1000,
						Message: lumo.Message{
							ID:             m.ID,
							ConversationID: env.srcConvID,
							MessageTag:     m.Tag,
							Role:           m.Role,
							Status:         2,
							Encrypted:      env.srcEncrypted[m.ID],
							CreateTime:     "2024-01-01T00:00:00Z",
							ParentID:       m.ParentID,
						},
					})
					return
				}
			}
			// Check project messages.
			if msgID == "proj-msg-1" {
				projMsgEnc := encryptMessage(t, "Project message content", env.projDEK, "proj-msg-tag-1", "user", "", env.projConvTag)
				cmdWriteJSON(t, w, lumo.GetMessageResponse{
					Code: 1000,
					Message: lumo.Message{
						ID:             "proj-msg-1",
						ConversationID: env.projConvID,
						MessageTag:     "proj-msg-tag-1",
						Role:           lumo.WireRoleUser,
						Status:         2,
						Encrypted:      projMsgEnc,
						CreateTime:     "2024-01-02T00:00:00Z",
					},
				})
				return
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
			*createdMsgs = append(*createdMsgs, req)
			cmdWriteJSON(t, w, lumo.GetMessageResponse{
				Code: 1000,
				Message: lumo.Message{
					ID:             fmt.Sprintf("new-msg-%d", len(*createdMsgs)),
					ConversationID: newConvID,
					MessageTag:     req.MessageTag,
					Role:           req.Role,
					Status:         req.Status,
					Encrypted:      req.Encrypted,
					CreateTime:     "2024-02-01T00:00:00Z",
				},
			})

		default:
			t.Logf("unhandled request: %s %s", r.Method, path)
			http.NotFound(w, r)
		}
	}))
}

// makeClient creates a lumo.Client pointing at the given test server.
func (env *integrationEnv) makeClient(t *testing.T, srvURL string) *lumo.Client {
	t.Helper()
	sess := &api.Session{
		Auth: proton.Auth{
			UID:         "test-uid",
			AccessToken: "test-token",
		},
		AppVersion:  "cli@2.0.0",
		UserAgent:   "proton-cli/test",
		UserKeyRing: env.tc.kr,
	}
	client := lumo.NewClient(sess)
	client.BaseURL = srvURL + "/api"
	return client
}

// runCopyFlow executes the full copy logic (same as runChatCp but with
// a pre-built client). Returns stdout, stderr, and error.
func runCopyFlow(
	ctx context.Context,
	t *testing.T,
	client *lumo.Client,
	args []string,
) (stdout, stderr string, err error) {
	t.Helper()

	if len(args) == 0 || len(args) > 2 {
		return "", "", fmt.Errorf("usage: proton lumo chat cp <source> [destination]")
	}

	// Normalize and parse source.
	srcNorm := normalizeArg(args[0])
	srcURI, perr := parseLumoURI(srcNorm)
	if perr != nil {
		return "", "", perr
	}
	if srcURI.Path == "" {
		return "", "", fmt.Errorf("source path must not be empty; provide a conversation ID or title")
	}

	// Normalize and parse destination.
	destArg := "lumo:///"
	if len(args) >= 2 {
		destArg = normalizeArg(args[1])
	}
	destURI, perr := parseLumoURI(destArg)
	if perr != nil {
		return "", "", perr
	}

	// Fetch all spaces.
	spaces, lerr := client.ListSpaces(ctx)
	if lerr != nil {
		return "", "", fmt.Errorf("listing spaces: %w", lerr)
	}

	// Build conversation pairs.
	var pairs []lumo.SpaceConversation
	for i := range spaces {
		for _, conv := range spaces[i].Conversations {
			pairs = append(pairs, lumo.SpaceConversation{
				Space:        &spaces[i],
				Conversation: conv,
			})
		}
	}

	// Callbacks.
	isSimple := func(s *lumo.Space) bool {
		return classifySpace(ctx, client, s) == "simple"
	}
	deriveDEKFn := func(s *lumo.Space) ([]byte, error) {
		return client.DeriveSpaceDEK(ctx, s)
	}

	// Resolve source space if specified.
	var srcSpaceID string
	if srcURI.Space != "" {
		decryptName := func(s *lumo.Space) string {
			return decryptSpaceName(ctx, client, s)
		}
		srcSpace, serr := resolveSpace(spaces, srcURI.Space, decryptName)
		if serr != nil {
			return "", "", fmt.Errorf("resolve source space: %w", serr)
		}
		srcSpaceID = srcSpace.ID
	}

	resolved, rerr := resolveConversationScoped(pairs, srcURI.Path, srcSpaceID, isSimple, decryptConversationTitle, deriveDEKFn)
	if rerr != nil {
		return "", "", rerr
	}

	// Load source space and derive DEK.
	space, dek, serr := resolveSpaceAndDEK(ctx, client, resolved.SpaceID)
	if serr != nil {
		return "", "", serr
	}

	// Fetch source conversation.
	srcConv, gerr := client.GetConversation(ctx, resolved.ConversationID)
	if gerr != nil {
		return "", "", fmt.Errorf("loading conversation: %w", gerr)
	}

	// Decrypt source title.
	srcTitle := decryptConversationTitle(*srcConv, dek, space.SpaceTag)

	// Resolve destination.
	dest, derr := resolveDestination(ctx, client, spaces, destURI, srcTitle)
	if derr != nil {
		return "", "", derr
	}

	// Capture stderr.
	oldStderr := os.Stderr
	rErr, wErr, _ := os.Pipe()
	os.Stderr = wErr

	// Cascade-deletion warning.
	if !dest.IsNew && classifySpace(ctx, client, dest.Space) == "simple" && len(dest.Space.Conversations) > 0 {
		destName := decryptSpaceName(ctx, client, dest.Space)
		_, _ = fmt.Fprintf(os.Stderr, "warning: space %q already has a conversation; web-UI deletion will cascade to both\n", destName)
	}

	// Create conversation.
	newConv, cerr := client.CreateConversation(ctx, dest.Space, dest.Title)
	if cerr != nil {
		_ = wErr.Close()
		os.Stderr = oldStderr
		return "", "", fmt.Errorf("creating conversation: %w", cerr)
	}

	// Build ID→Tag map.
	idToTag := make(map[string]string, len(srcConv.Messages))
	for _, m := range srcConv.Messages {
		idToTag[m.ID] = m.MessageTag
	}

	// Copy messages.
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
		plainJSON, decErr := lumo.DecryptString(msg.Encrypted, dek, srcAD)
		if decErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to decrypt message %s: %v\n", msg.ID, decErr)
			failed++
			continue
		}

		freshTag, _ := lumo.GenerateTag()
		targetAD := lumo.MessageAD(freshTag, role, "", newConv.ConversationTag)

		encrypted, encErr := lumo.EncryptString(plainJSON, dest.DEK, targetAD)
		if encErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to encrypt message %s: %v\n", msg.ID, encErr)
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
		_, cmErr := client.CreateRawMessage(ctx, req)
		if cmErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "warning: failed to create message: %v\n", cmErr)
			failed++
			continue
		}
		copied++
	}

	// Capture stdout.
	oldStdout := os.Stdout
	rOut, wOut, _ := os.Pipe()
	os.Stdout = wOut

	fmt.Println(newConv.ID)
	if failed > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "Copied %d messages (%d failed)\n", copied, failed)
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "Copied %d messages\n", copied)
	}

	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	var outBuf, errBuf bytes.Buffer
	_, _ = outBuf.ReadFrom(rOut)
	_, _ = errBuf.ReadFrom(rErr)
	_ = rOut.Close()
	_ = rErr.Close()

	return outBuf.String(), errBuf.String(), nil
}

// TestIntegration_SingleBareArg tests backward-compatible single bare arg:
// resolves in simple spaces, copies to new simple space with " (copy)" suffix.
//
// Validates: Requirements 8.1, 8.2, 8.3, 9.1, 9.2
func TestIntegration_SingleBareArg(t *testing.T) {
	env := newIntegrationEnv(t)

	newConvID := "new-conv-bare"
	newConvTag, _ := lumo.GenerateTag()
	newSpace, _ := env.tc.makeSpace(t, "new-bare-space-id", "new-bare-space-tag")

	var createdMsgs []lumo.CreateMessageReq
	srv := env.integrationServer(t, newConvID, newConvTag, &newSpace, &createdMsgs)
	defer srv.Close()

	client := env.makeClient(t, srv.URL)
	ctx := context.Background()

	stdout, stderr, err := runCopyFlow(ctx, t, client, []string{env.srcConvTitle})
	if err != nil {
		t.Fatalf("runCopyFlow: %v", err)
	}

	// Verify stdout contains new conversation ID.
	if !strings.Contains(stdout, newConvID) {
		t.Fatalf("stdout = %q, want to contain %q", stdout, newConvID)
	}

	// Verify stderr contains copy summary.
	if !strings.Contains(stderr, "Copied 2 messages") {
		t.Fatalf("stderr = %q, want to contain 'Copied 2 messages'", stderr)
	}

	// Verify all messages were created.
	if len(createdMsgs) != 2 {
		t.Fatalf("expected 2 created messages, got %d", len(createdMsgs))
	}

	// Verify roles preserved.
	if createdMsgs[0].Role != lumo.WireRoleUser {
		t.Fatalf("msg 0 role = %d, want %d", createdMsgs[0].Role, lumo.WireRoleUser)
	}
	if createdMsgs[1].Role != lumo.WireRoleAssistant {
		t.Fatalf("msg 1 role = %d, want %d", createdMsgs[1].Role, lumo.WireRoleAssistant)
	}
}

// TestIntegration_TwoArg_SpaceAndTitle tests two-arg form with
// lumo://space/title: resolves source in specified space, copies to
// specified space with explicit title.
//
// Validates: Requirements 8.1, 8.2, 8.3, 8.4
func TestIntegration_TwoArg_SpaceAndTitle(t *testing.T) {
	env := newIntegrationEnv(t)

	newConvID := "new-conv-two-arg"
	newConvTag, _ := lumo.GenerateTag()

	var createdMsgs []lumo.CreateMessageReq
	srv := env.integrationServer(t, newConvID, newConvTag, nil, &createdMsgs)
	defer srv.Close()

	client := env.makeClient(t, srv.URL)
	ctx := context.Background()

	// Copy from source space to dest space with explicit title.
	// Source: lumo://src-simple-id/Integration Chat
	// Dest: lumo://dest-simple-id/Copied Title
	stdout, stderr, err := runCopyFlow(ctx, t, client, []string{
		"lumo://src-simple-id/Integration Chat",
		"lumo://dest-simple-id/Copied Title",
	})
	if err != nil {
		t.Fatalf("runCopyFlow: %v", err)
	}

	if !strings.Contains(stdout, newConvID) {
		t.Fatalf("stdout = %q, want to contain %q", stdout, newConvID)
	}
	if !strings.Contains(stderr, "Copied 2 messages") {
		t.Fatalf("stderr = %q, want 'Copied 2 messages'", stderr)
	}
	if len(createdMsgs) != 2 {
		t.Fatalf("expected 2 created messages, got %d", len(createdMsgs))
	}
}

// TestIntegration_TwoArg_EmptyPath tests two-arg with lumo://space/
// (empty path): copies to specified space with source title + " (copy)".
//
// Validates: Requirements 8.1, 8.2, 8.5
func TestIntegration_TwoArg_EmptyPath(t *testing.T) {
	env := newIntegrationEnv(t)

	newConvID := "new-conv-empty-path"
	newConvTag, _ := lumo.GenerateTag()

	var createdMsgs []lumo.CreateMessageReq
	srv := env.integrationServer(t, newConvID, newConvTag, nil, &createdMsgs)
	defer srv.Close()

	client := env.makeClient(t, srv.URL)
	ctx := context.Background()

	// Dest: lumo://dest-simple-id/ (empty path → title = source + " (copy)")
	stdout, stderr, err := runCopyFlow(ctx, t, client, []string{
		env.srcConvTitle,
		"lumo://dest-simple-id/",
	})
	if err != nil {
		t.Fatalf("runCopyFlow: %v", err)
	}

	if !strings.Contains(stdout, newConvID) {
		t.Fatalf("stdout = %q, want to contain %q", stdout, newConvID)
	}
	if !strings.Contains(stderr, "Copied 2 messages") {
		t.Fatalf("stderr = %q, want 'Copied 2 messages'", stderr)
	}
}

// TestIntegration_TwoArg_NewSpaceExplicitTitle tests two-arg with
// lumo:///title: copies to new simple space with explicit title.
//
// Validates: Requirements 8.1, 8.2, 8.4
func TestIntegration_TwoArg_NewSpaceExplicitTitle(t *testing.T) {
	env := newIntegrationEnv(t)

	newConvID := "new-conv-explicit-title"
	newConvTag, _ := lumo.GenerateTag()
	newSpace, _ := env.tc.makeSpace(t, "new-explicit-space-id", "new-explicit-space-tag")

	var createdMsgs []lumo.CreateMessageReq
	srv := env.integrationServer(t, newConvID, newConvTag, &newSpace, &createdMsgs)
	defer srv.Close()

	client := env.makeClient(t, srv.URL)
	ctx := context.Background()

	// Dest: lumo:///My New Title (new space, explicit title)
	stdout, stderr, err := runCopyFlow(ctx, t, client, []string{
		env.srcConvTitle,
		"lumo:///My New Title",
	})
	if err != nil {
		t.Fatalf("runCopyFlow: %v", err)
	}

	if !strings.Contains(stdout, newConvID) {
		t.Fatalf("stdout = %q, want to contain %q", stdout, newConvID)
	}
	if !strings.Contains(stderr, "Copied 2 messages") {
		t.Fatalf("stderr = %q, want 'Copied 2 messages'", stderr)
	}
	if len(createdMsgs) != 2 {
		t.Fatalf("expected 2 created messages, got %d", len(createdMsgs))
	}
}

// TestIntegration_CrossSpace_DestDEK verifies that CreateRawMessage calls
// use the destination space's DEK for encryption (cross-space copy).
//
// Validates: Requirements 8.1, 8.4, 8.5
func TestIntegration_CrossSpace_DestDEK(t *testing.T) {
	env := newIntegrationEnv(t)

	newConvID := "new-conv-cross"
	newConvTag, _ := lumo.GenerateTag()

	var createdMsgs []lumo.CreateMessageReq
	srv := env.integrationServer(t, newConvID, newConvTag, nil, &createdMsgs)
	defer srv.Close()

	client := env.makeClient(t, srv.URL)
	ctx := context.Background()

	// Copy from src-simple-id to dest-simple-id (cross-space).
	_, _, err := runCopyFlow(ctx, t, client, []string{
		"lumo://src-simple-id/Integration Chat",
		"lumo://dest-simple-id/Cross Copy",
	})
	if err != nil {
		t.Fatalf("runCopyFlow: %v", err)
	}

	if len(createdMsgs) != 2 {
		t.Fatalf("expected 2 created messages, got %d", len(createdMsgs))
	}

	// Verify messages are decryptable with destination DEK (not source DEK).
	for i, cm := range createdMsgs {
		role := "user"
		if cm.Role == lumo.WireRoleAssistant {
			role = "assistant"
		}
		ad := lumo.MessageAD(cm.MessageTag, role, "", newConvTag)

		// Should decrypt with dest DEK.
		plainJSON, derr := lumo.DecryptString(cm.Encrypted, env.destDEK, ad)
		if derr != nil {
			t.Fatalf("msg %d: failed to decrypt with dest DEK: %v", i, derr)
		}

		var priv map[string]string
		if err := json.Unmarshal([]byte(plainJSON), &priv); err != nil {
			t.Fatalf("msg %d: unmarshal: %v", i, err)
		}
		if priv["content"] != env.srcMsgs[i].Content {
			t.Fatalf("msg %d: content = %q, want %q", i, priv["content"], env.srcMsgs[i].Content)
		}

		// Should NOT decrypt with source DEK (different key).
		_, srcErr := lumo.DecryptString(cm.Encrypted, env.srcDEK, ad)
		if srcErr == nil {
			t.Fatalf("msg %d: unexpectedly decrypted with source DEK", i)
		}
	}
}

// TestIntegration_SameSpace tests that same-space copy is permitted.
//
// Validates: Requirements 8.1, 8.5, 8.6
func TestIntegration_SameSpace(t *testing.T) {
	env := newIntegrationEnv(t)

	newConvID := "new-conv-same"
	newConvTag, _ := lumo.GenerateTag()

	var createdMsgs []lumo.CreateMessageReq
	srv := env.integrationServer(t, newConvID, newConvTag, nil, &createdMsgs)
	defer srv.Close()

	client := env.makeClient(t, srv.URL)
	ctx := context.Background()

	// Copy within the same space.
	_, stderr, err := runCopyFlow(ctx, t, client, []string{
		"lumo://src-simple-id/Integration Chat",
		"lumo://src-simple-id/Same Space Copy",
	})
	if err != nil {
		t.Fatalf("runCopyFlow: %v", err)
	}

	if !strings.Contains(stderr, "Copied 2 messages") {
		t.Fatalf("stderr = %q, want 'Copied 2 messages'", stderr)
	}
	if len(createdMsgs) != 2 {
		t.Fatalf("expected 2 created messages, got %d", len(createdMsgs))
	}
}

// TestIntegration_CascadeDeletionWarning verifies stderr output for
// existing simple space destination that already has a conversation.
//
// Validates: Requirements 8.1, 8.5, 9.1
func TestIntegration_CascadeDeletionWarning(t *testing.T) {
	env := newIntegrationEnv(t)

	newConvID := "new-conv-cascade"
	newConvTag, _ := lumo.GenerateTag()

	var createdMsgs []lumo.CreateMessageReq
	srv := env.integrationServer(t, newConvID, newConvTag, nil, &createdMsgs)
	defer srv.Close()

	client := env.makeClient(t, srv.URL)
	ctx := context.Background()

	// Copy to dest-simple-id which already has a conversation.
	_, stderr, err := runCopyFlow(ctx, t, client, []string{
		env.srcConvTitle,
		"lumo://dest-simple-id/Cascade Test",
	})
	if err != nil {
		t.Fatalf("runCopyFlow: %v", err)
	}

	// Verify cascade-deletion warning is present.
	if !strings.Contains(stderr, "warning:") {
		t.Fatalf("stderr should contain warning, got: %q", stderr)
	}
	if !strings.Contains(stderr, "already has a conversation") {
		t.Fatalf("stderr should mention cascade deletion, got: %q", stderr)
	}
	if !strings.Contains(stderr, "web-UI deletion will cascade") {
		t.Fatalf("stderr should mention cascade, got: %q", stderr)
	}
}

// TestIntegration_NoCascadeWarning_ProjectSpace verifies no warning for
// project space destination.
//
// Validates: Requirements 8.5
func TestIntegration_NoCascadeWarning_ProjectSpace(t *testing.T) {
	env := newIntegrationEnv(t)

	newConvID := "new-conv-proj-dest"
	newConvTag, _ := lumo.GenerateTag()

	var createdMsgs []lumo.CreateMessageReq
	srv := env.integrationServer(t, newConvID, newConvTag, nil, &createdMsgs)
	defer srv.Close()

	client := env.makeClient(t, srv.URL)
	ctx := context.Background()

	// Copy to project space (should NOT produce cascade warning).
	_, stderr, err := runCopyFlow(ctx, t, client, []string{
		env.srcConvTitle,
		"lumo://proj-space-id/Project Copy",
	})
	if err != nil {
		t.Fatalf("runCopyFlow: %v", err)
	}

	if strings.Contains(stderr, "cascade") {
		t.Fatalf("stderr should NOT contain cascade warning for project space, got: %q", stderr)
	}
	if !strings.Contains(stderr, "Copied 2 messages") {
		t.Fatalf("stderr = %q, want 'Copied 2 messages'", stderr)
	}
}

// TestIntegration_Error_NotFoundSource verifies error when source
// conversation is not found.
//
// Validates: Requirements 8.6, 9.4
func TestIntegration_Error_NotFoundSource(t *testing.T) {
	env := newIntegrationEnv(t)

	var createdMsgs []lumo.CreateMessageReq
	srv := env.integrationServer(t, "unused", "unused", nil, &createdMsgs)
	defer srv.Close()

	client := env.makeClient(t, srv.URL)
	ctx := context.Background()

	_, _, err := runCopyFlow(ctx, t, client, []string{"nonexistent-chat-xyz"})
	if err == nil {
		t.Fatal("expected error for not-found source, got nil")
	}
	if !strings.Contains(err.Error(), "no conversation matching") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestIntegration_Error_EmptySourcePath verifies error when source path
// is empty.
//
// Validates: Requirements 8.6
func TestIntegration_Error_EmptySourcePath(t *testing.T) {
	env := newIntegrationEnv(t)
	_ = env // env not needed for this test

	_, _, err := runCopyFlow(context.Background(), t, nil, []string{"lumo://space/"})
	if err == nil {
		t.Fatal("expected error for empty source path, got nil")
	}
	if !strings.Contains(err.Error(), "source path must not be empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestIntegration_Error_TooManyArgs verifies error for 3+ args.
//
// Validates: Requirements 8.6
func TestIntegration_Error_TooManyArgs(t *testing.T) {
	_, _, err := runCopyFlow(context.Background(), t, nil, []string{"a", "b", "c"})
	if err == nil {
		t.Fatal("expected error for 3 args, got nil")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestIntegration_Error_BareStringNoProjectMatch verifies that bare
// strings do NOT match conversations in project spaces.
//
// Validates: Requirements 9.4
func TestIntegration_Error_BareStringNoProjectMatch(t *testing.T) {
	env := newIntegrationEnv(t)

	var createdMsgs []lumo.CreateMessageReq
	srv := env.integrationServer(t, "unused", "unused", nil, &createdMsgs)
	defer srv.Close()

	client := env.makeClient(t, srv.URL)
	ctx := context.Background()

	// "Project Chat" exists only in the project space.
	// A bare string should NOT find it.
	_, _, err := runCopyFlow(ctx, t, client, []string{env.projConvTitle})
	if err == nil {
		t.Fatal("expected error: bare string should not match project space conversation")
	}
	if !strings.Contains(err.Error(), "no conversation matching") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestIntegration_QualifiedURI_ProjectSpace verifies that a fully
// qualified URI can reach a project space conversation.
//
// Validates: Requirements 8.1, 8.4, 9.4
func TestIntegration_QualifiedURI_ProjectSpace(t *testing.T) {
	env := newIntegrationEnv(t)

	newConvID := "new-conv-proj-src"
	newConvTag, _ := lumo.GenerateTag()
	newSpace, _ := env.tc.makeSpace(t, "new-proj-dest-id", "new-proj-dest-tag")

	var createdMsgs []lumo.CreateMessageReq
	srv := env.integrationServer(t, newConvID, newConvTag, &newSpace, &createdMsgs)
	defer srv.Close()

	client := env.makeClient(t, srv.URL)
	ctx := context.Background()

	// Use qualified URI to reach project conversation.
	stdout, stderr, err := runCopyFlow(ctx, t, client, []string{
		"lumo://proj-space-id/Project Chat",
	})
	if err != nil {
		t.Fatalf("runCopyFlow: %v", err)
	}

	if !strings.Contains(stdout, newConvID) {
		t.Fatalf("stdout = %q, want to contain %q", stdout, newConvID)
	}
	if !strings.Contains(stderr, "Copied 1 messages") {
		t.Fatalf("stderr = %q, want 'Copied 1 messages'", stderr)
	}
}
