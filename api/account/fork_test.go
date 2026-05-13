package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	proton "github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

// TestForkResponseToSessionMapping_Property verifies that for any valid
// ForkPullResp and ServiceConfig, the Session constructed from the pull
// response has its UID, AccessToken, and RefreshToken matching the response
// fields, and its BaseURL matching the ServiceConfig's Host.
//
// **Validates: Requirements 2.1, 4.2**
// Tag: Feature: session-fork, Property 4: Fork response to Session mapping
func TestForkResponseToSessionMapping_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		uid := rapid.StringMatching(`[a-zA-Z0-9]{1,32}`).Draw(t, "uid")
		accessToken := rapid.StringMatching(`[a-zA-Z0-9]{1,64}`).Draw(t, "accessToken")
		refreshToken := rapid.StringMatching(`[a-zA-Z0-9]{1,64}`).Draw(t, "refreshToken")
		host := rapid.StringMatching(`https://[a-z]{3,12}\\.proton\\.me/api`).Draw(t, "host")
		clientID := rapid.StringMatching(`web-[a-z]{3,10}`).Draw(t, "clientID")
		version := rapid.StringMatching(`[0-9]{1,3}\\.[0-9]{1,3}\\.[0-9]{1,3}\\.[0-9]{1,3}`).Draw(t, "version")

		pull := &ForkPullResp{
			Code:         1000,
			UID:          uid,
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
		}

		svc := api.ServiceConfig{
			Name:     "test",
			Host:     host,
			ClientID: clientID,
			Version:  version,
		}

		session := SessionFromForkPull(context.Background(), pull, svc, version)
		defer session.Stop()

		// Verify UID mapping.
		if session.Auth.UID != uid {
			t.Fatalf("UID: got %q, want %q", session.Auth.UID, uid)
		}

		// Verify AccessToken mapping.
		if session.Auth.AccessToken != accessToken {
			t.Fatalf("AccessToken: got %q, want %q", session.Auth.AccessToken, accessToken)
		}

		// Verify RefreshToken mapping.
		if session.Auth.RefreshToken != refreshToken {
			t.Fatalf("RefreshToken: got %q, want %q", session.Auth.RefreshToken, refreshToken)
		}

		// Verify BaseURL mapping.
		if session.BaseURL != host {
			t.Fatalf("BaseURL: got %q, want %q", session.BaseURL, host)
		}

		// Verify AppVersion format.
		wantAppVer := clientID + "@" + version + ""
		if session.AppVersion != wantAppVer {
			t.Fatalf("AppVersion: got %q, want %q", session.AppVersion, wantAppVer)
		}
	})
}

// --- Unit tests for fork protocol ---

// TestForkPushRequestBody verifies that the push request body has the correct
// shape: ChildClientID and Independent fields are set correctly.
func TestForkPushRequestBody(t *testing.T) {
	var gotBody ForkPushReq

	pushSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/auth/v4/sessions/forks") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":     1000,
			"Selector": "test-selector",
		})
	}))
	defer pushSrv.Close()

	jar, _ := cookiejar.New(nil)
	parent := &api.Session{
		Auth: proton.Auth{
			UID:         "parent-uid",
			AccessToken: "parent-token",
		},
		BaseURL: pushSrv.URL,
	}
	parent.SetCookieJar(jar)

	pushReq := ForkPushReq{
		ChildClientID: "web-lumo",
		Independent:   0,
		Payload:       "encrypted-blob",
	}
	var pushResp ForkPushResp
	err := parent.DoJSON(context.Background(), "POST", "/auth/v4/sessions/forks", pushReq, &pushResp)
	if err != nil {
		t.Fatalf("DoJSON push: %v", err)
	}

	if gotBody.ChildClientID != "web-lumo" {
		t.Fatalf("ChildClientID = %q, want %q", gotBody.ChildClientID, "web-lumo")
	}
	if gotBody.Independent != 0 {
		t.Fatalf("Independent = %d, want 0", gotBody.Independent)
	}
	if gotBody.Payload != "encrypted-blob" {
		t.Fatalf("Payload = %q, want %q", gotBody.Payload, "encrypted-blob")
	}
	if pushResp.Selector != "test-selector" {
		t.Fatalf("Selector = %q, want %q", pushResp.Selector, "test-selector")
	}
}

// TestForkPullGoesToCorrectHost verifies that the pull request goes to the
// child host with the correct selector path and parent auth headers.
func TestForkPullGoesToCorrectHost(t *testing.T) {
	var gotUID, gotAuth, gotPath string

	childSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUID = r.Header.Get("x-pm-uid")
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path

		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":         1000,
			"UID":          "child-uid",
			"AccessToken":  "child-at",
			"RefreshToken": "child-rt",
			"Payload":      "",
		})
	}))
	defer childSrv.Close()

	jar, _ := cookiejar.New(nil)
	parent := &api.Session{
		Auth: proton.Auth{
			UID:         "parent-uid",
			AccessToken: "parent-token",
		},
		AppVersion: "web-account@1.0.0",
		UserAgent:  "proton-cli/test",
	}
	parent.SetCookieJar(jar)

	resp, err := forkPull(context.Background(), parent, childSrv.URL, "sel-123", "web-lumo@1.3.3.4")
	if err != nil {
		t.Fatalf("forkPull: %v", err)
	}

	if gotPath != "/auth/v4/sessions/forks/sel-123" {
		t.Fatalf("path = %q, want %q", gotPath, "/auth/v4/sessions/forks/sel-123")
	}
	// Pull is unauthenticated — no x-pm-uid or Authorization headers.
	if gotUID != "" {
		t.Fatalf("x-pm-uid = %q, want empty (unauthenticated pull)", gotUID)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization = %q, want empty (unauthenticated pull)", gotAuth)
	}
	if resp.UID != "child-uid" {
		t.Fatalf("UID = %q, want %q", resp.UID, "child-uid")
	}
	if resp.AccessToken != "child-at" {
		t.Fatalf("AccessToken = %q, want %q", resp.AccessToken, "child-at")
	}
}

// TestForkPullAPIError verifies that a non-1000 API code from the pull
// endpoint returns an *api.Error.
func TestForkPullAPIError(t *testing.T) {
	childSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":  9100,
			"Error": "insufficient scope",
		})
	}))
	defer childSrv.Close()

	jar, _ := cookiejar.New(nil)
	parent := &api.Session{
		Auth: proton.Auth{UID: "uid", AccessToken: "at"},
	}
	parent.SetCookieJar(jar)

	_, err := forkPull(context.Background(), parent, childSrv.URL, "bad-sel", "web-lumo@1.3.3.4")
	if err == nil {
		t.Fatal("expected error from forkPull")
	}

	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *api.Error, got %T: %v", err, err)
	}
	if apiErr.Code != 9100 {
		t.Fatalf("Code = %d, want 9100", apiErr.Code)
	}
}

// TestForkSessionEndToEnd verifies the full fork flow with mock push and pull
// endpoints.
func TestForkSessionEndToEnd(t *testing.T) {
	// Encrypt a blob to use as the payload.
	blob := &ForkBlob{Type: "default", KeyPassword: "test-salted-key"}
	ct, blobKey, err := EncryptForkBlob(blob)
	if err != nil {
		t.Fatalf("EncryptForkBlob: %v", err)
	}

	// Mock push endpoint.
	pushSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("push: expected POST, got %s", r.Method)
		}

		var req ForkPushReq
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("push: unmarshal: %v", err)
		}

		if req.ChildClientID != "web-lumo" {
			t.Fatalf("push: ChildClientID = %q, want %q", req.ChildClientID, "web-lumo")
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":     1000,
			"Selector": "fork-sel-abc",
		})
	}))
	defer pushSrv.Close()

	// Mock pull endpoint — returns the encrypted payload.
	pullSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("pull: expected GET, got %s", r.Method)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":         1000,
			"UID":          "child-uid-123",
			"AccessToken":  "child-at-456",
			"RefreshToken": "child-rt-789",
			"Payload":      ct,
		})
	}))
	defer pullSrv.Close()

	jar, _ := cookiejar.New(nil)
	parent := &api.Session{
		Auth: proton.Auth{
			UID:         "parent-uid",
			AccessToken: "parent-at",
		},
		BaseURL: pushSrv.URL,
	}
	parent.SetCookieJar(jar)

	targetSvc := api.ServiceConfig{
		Name:     "lumo",
		Host:     pullSrv.URL,
		ClientID: "web-lumo",
	}

	// Push.
	pushReq := ForkPushReq{
		ChildClientID: targetSvc.ClientID,
		Independent:   0,
		Payload:       ct,
	}
	var pushResp ForkPushResp
	if err := parent.DoJSON(context.Background(), "POST", "/auth/v4/sessions/forks", pushReq, &pushResp); err != nil {
		t.Fatalf("push: %v", err)
	}

	if pushResp.Selector != "fork-sel-abc" {
		t.Fatalf("Selector = %q, want %q", pushResp.Selector, "fork-sel-abc")
	}

	// Pull.
	pullResp, err := forkPull(context.Background(), parent, targetSvc.Host, pushResp.Selector, targetSvc.AppVersion(""))
	if err != nil {
		t.Fatalf("pull: %v", err)
	}

	// Decrypt blob.
	decrypted, err := DecryptForkBlob(pullResp.Payload, blobKey)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if decrypted.KeyPassword != "test-salted-key" {
		t.Fatalf("KeyPassword = %q, want %q", decrypted.KeyPassword, "test-salted-key")
	}

	// Build child session.
	child := SessionFromForkPull(context.Background(), pullResp, targetSvc, api.DefaultVersion)
	defer child.Stop()

	if child.Auth.UID != "child-uid-123" {
		t.Fatalf("child UID = %q, want %q", child.Auth.UID, "child-uid-123")
	}
	if child.Auth.AccessToken != "child-at-456" {
		t.Fatalf("child AccessToken = %q, want %q", child.Auth.AccessToken, "child-at-456")
	}
	if child.Auth.RefreshToken != "child-rt-789" {
		t.Fatalf("child RefreshToken = %q, want %q", child.Auth.RefreshToken, "child-rt-789")
	}
	if child.BaseURL != pullSrv.URL {
		t.Fatalf("child BaseURL = %q, want %q", child.BaseURL, pullSrv.URL)
	}
}

// TestBuildChildSession verifies that SessionFromForkPull sets the correct
// fields on the returned Session.
func TestBuildChildSession(t *testing.T) {
	pull := &ForkPullResp{
		Code:         1000,
		UID:          "uid-abc",
		AccessToken:  "at-def",
		RefreshToken: "rt-ghi",
	}

	svc := api.ServiceConfig{
		Name:     "drive",
		Host:     "https://drive-api.proton.me/api",
		ClientID: "web-drive",
		Version:  "5.2.0",
	}

	session := SessionFromForkPull(context.Background(), pull, svc, "1.2.3.4")
	defer session.Stop()

	if session.Auth.UID != "uid-abc" {
		t.Fatalf("UID = %q, want %q", session.Auth.UID, "uid-abc")
	}
	if session.BaseURL != "https://drive-api.proton.me/api" {
		t.Fatalf("BaseURL = %q, want %q", session.BaseURL, "https://drive-api.proton.me/api")
	}
	if session.AppVersion != "web-drive@5.2.0" {
		t.Fatalf("AppVersion = %q, want %q", session.AppVersion, "web-drive@5.2.0")
	}
	if session.Client == nil {
		t.Fatal("Client is nil")
	}
	if session.CookieJar() == nil {
		t.Fatal("CookieJar is nil")
	}
	if session.Sem == nil {
		t.Fatal("Sem is nil")
	}
	if session.Throttle == nil {
		t.Fatal("Throttle is nil")
	}
}

// TestErrForkFailed verifies the sentinel error.
func TestErrForkFailed(t *testing.T) {
	if ErrForkFailed.Error() != "account: session fork failed" {
		t.Fatalf("ErrForkFailed = %q, want %q", ErrForkFailed.Error(), "account: session fork failed")
	}
}
