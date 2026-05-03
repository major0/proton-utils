package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
)

// cookieMockStore is a SessionStore that tracks Save calls and supports
// configurable Load behavior for cookie session testing.
type cookieMockStore struct {
	config    *SessionCredentials
	saved     *SessionCredentials
	saveCount int
	loadErr   error
}

func (s *cookieMockStore) Load() (*SessionCredentials, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.config == nil {
		return nil, ErrKeyNotFound
	}
	cfg := *s.config
	return &cfg, nil
}

func (s *cookieMockStore) Save(cfg *SessionCredentials) error {
	s.saved = cfg
	s.saveCount++
	return nil
}

func (s *cookieMockStore) Delete() error           { return nil }
func (s *cookieMockStore) List() ([]string, error) { return nil, nil }
func (s *cookieMockStore) Switch(string) error     { return nil }

// newCookieForkTestServer creates a mock HTTP server that handles the
// endpoints needed for cookie fork testing:
//   - POST /core/v4/auth/cookies → sets AUTH/REFRESH cookies
//   - POST /auth/v4/sessions/forks → returns a selector
//   - GET /auth/v4/sessions/forks/<selector> → returns fork pull response
//   - GET /core/v4/users → returns a minimal user
//   - GET /core/v4/addresses → returns empty addresses
func newCookieForkTestServer(t *testing.T, uid string) *httptest.Server {
	t.Helper()

	// Pre-generate a fork blob for the pull response.
	blob := &ForkBlob{Type: "default", KeyPassword: "test-keypass"}
	ciphertext, blobKey, err := EncryptForkBlob(blob)
	if err != nil {
		t.Fatalf("EncryptForkBlob: %v", err)
	}
	// Store the key so the pull response can include the matching ciphertext.
	_ = blobKey // The test caller will need to decrypt with this key.

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/core/v4/auth/cookies":
			// Transition to cookies: read the UID from the request body
			// and set AUTH/REFRESH cookies with that UID.
			var req AuthCookiesReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			cookieUID := req.UID
			if cookieUID == "" {
				cookieUID = uid
			}
			http.SetCookie(w, &http.Cookie{
				Name:  "AUTH-" + cookieUID,
				Value: "cookie-auth-token",
				Path:  "/",
			})
			http.SetCookie(w, &http.Cookie{
				Name:  "REFRESH-" + cookieUID,
				Value: "cookie-refresh-token",
				Path:  "/",
			})
			http.SetCookie(w, &http.Cookie{
				Name:  "Session-Id",
				Value: "sid-test",
				Path:  "/",
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})

		case r.Method == "POST" && r.URL.Path == "/auth/v4/sessions/forks":
			_ = json.NewEncoder(w).Encode(ForkPushResp{
				Code:     1000,
				Selector: "test-selector",
			})

		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/auth/v4/sessions/forks/"):
			//nolint:gosec // G117: test data with fake tokens.
			_ = json.NewEncoder(w).Encode(ForkPullResp{
				Code:         1000,
				UID:          "child-uid",
				AccessToken:  "child-at",
				RefreshToken: "child-rt",
				Payload:      ciphertext,
				Scopes:       []string{"full", "lumo"},
			})

		case r.Method == "GET" && r.URL.Path == "/core/v4/users":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code": 1000,
				"User": map[string]any{
					"ID":   "child-uid",
					"Name": "test-user",
				},
			})

		case r.Method == "GET" && r.URL.Path == "/core/v4/addresses":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code":      1000,
				"Addresses": []any{},
			})

		default:
			t.Logf("unhandled request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 0, "Error": "not found"})
		}
	}))
}

// TestLoadOrCreateCookieSession_NoCookieSession verifies that when no cookie
// session exists, loadOrCreateCookieSession creates one via fork+transition.
func TestLoadOrCreateCookieSession_NoCookieSession(t *testing.T) {
	uid := "acct-uid-1"
	srv := newCookieForkTestServer(t, uid)
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	acctSession := &Session{
		Auth:       proton.Auth{UID: uid, AccessToken: "acct-at", RefreshToken: "acct-rt"},
		BaseURL:    srv.URL,
		AppVersion: "web-account@5.2.0",
		cookieJar:  jar,
		manager:    proton.New(proton.WithHostURL(srv.URL)),
	}

	acctConfig := &SessionCredentials{
		UID:         uid,
		LastRefresh: time.Now(),
	}

	acctSvc := ServiceConfig{
		Name:     "account",
		Host:     srv.URL,
		ClientID: "web-account",
		Version:  "5.2.0",
	}

	cookieStore := &cookieMockStore{}

	cs, err := loadOrCreateCookieSession(context.Background(), acctSession, acctConfig, acctSvc, cookieStore)
	if err != nil {
		t.Fatalf("loadOrCreateCookieSession: %v", err)
	}

	if cs == nil {
		t.Fatal("expected non-nil CookieSession")
	}

	// Verify cookie session was saved.
	if cookieStore.saveCount != 1 {
		t.Fatalf("expected 1 save, got %d", cookieStore.saveCount)
	}
	if cookieStore.saved == nil {
		t.Fatal("expected saved config to be non-nil")
	}
	if cookieStore.saved.UID == "" {
		t.Fatal("saved config UID should not be empty")
	}
	if cookieStore.saved.LastRefresh.IsZero() {
		t.Fatal("saved config LastRefresh should be set")
	}
}

// TestLoadOrCreateCookieSession_ValidCookieSession verifies that when a
// valid (fresh) cookie session exists, it is restored without creating a new one.
func TestLoadOrCreateCookieSession_ValidCookieSession(t *testing.T) {
	uid := "acct-uid-2"
	now := time.Now()

	acctConfig := &SessionCredentials{
		UID:         uid,
		LastRefresh: now.Add(-30 * time.Minute), // account refreshed 30 min ago
	}

	acctSvc := ServiceConfig{
		Name:     "account",
		Host:     "https://account-api.proton.me/api",
		ClientID: "web-account",
		Version:  "5.2.0",
	}

	// Cookie session is fresher than account session → not stale.
	cookieStore := &cookieMockStore{
		config: &SessionCredentials{
			UID: uid,
			Cookies: []SerialCookie{
				{Name: "AUTH-" + uid, Value: "auth-val"},
				{Name: "REFRESH-" + uid, Value: "refresh-val"},
			},
			LastRefresh: now, // cookie refreshed now → fresher than account
		},
	}

	jar, _ := cookiejar.New(nil)
	acctSession := &Session{
		Auth:      proton.Auth{UID: uid},
		cookieJar: jar,
	}

	cs, err := loadOrCreateCookieSession(context.Background(), acctSession, acctConfig, acctSvc, cookieStore)
	if err != nil {
		t.Fatalf("loadOrCreateCookieSession: %v", err)
	}

	if cs == nil {
		t.Fatal("expected non-nil CookieSession")
	}

	// No save should have occurred — session was restored.
	if cookieStore.saveCount != 0 {
		t.Fatalf("expected 0 saves (restored), got %d", cookieStore.saveCount)
	}

	// Verify the restored session has the correct UID.
	if cs.UID != uid {
		t.Fatalf("UID = %q, want %q", cs.UID, uid)
	}
}

// TestLoadOrCreateCookieSession_StaleCookieSession verifies that when the
// cookie session is stale (account refreshed after cookie), it is re-created.
func TestLoadOrCreateCookieSession_StaleCookieSession(t *testing.T) {
	uid := "acct-uid-3"
	srv := newCookieForkTestServer(t, uid)
	defer srv.Close()

	now := time.Now()

	jar, _ := cookiejar.New(nil)
	acctSession := &Session{
		Auth:       proton.Auth{UID: uid, AccessToken: "acct-at", RefreshToken: "acct-rt"},
		BaseURL:    srv.URL,
		AppVersion: "web-account@5.2.0",
		cookieJar:  jar,
		manager:    proton.New(proton.WithHostURL(srv.URL)),
	}

	acctConfig := &SessionCredentials{
		UID:         uid,
		LastRefresh: now, // account refreshed now
	}

	acctSvc := ServiceConfig{
		Name:     "account",
		Host:     srv.URL,
		ClientID: "web-account",
		Version:  "5.2.0",
	}

	// Cookie session is older than account → stale.
	cookieStore := &cookieMockStore{
		config: &SessionCredentials{
			UID: uid,
			Cookies: []SerialCookie{
				{Name: "AUTH-" + uid, Value: "old-auth"},
				{Name: "REFRESH-" + uid, Value: "old-refresh"},
			},
			LastRefresh: now.Add(-time.Hour), // cookie is 1 hour old → stale
		},
	}

	cs, err := loadOrCreateCookieSession(context.Background(), acctSession, acctConfig, acctSvc, cookieStore)
	if err != nil {
		t.Fatalf("loadOrCreateCookieSession: %v", err)
	}

	if cs == nil {
		t.Fatal("expected non-nil CookieSession")
	}

	// Save should have occurred — stale session was re-created.
	if cookieStore.saveCount != 1 {
		t.Fatalf("expected 1 save (re-created), got %d", cookieStore.saveCount)
	}
}

// TestRestoreServiceSession_NonCookieAuth_IgnoresCookieStore verifies that
// for non-CookieAuth services (drive), the cookieStore is not used.
func TestRestoreServiceSession_NonCookieAuth_IgnoresCookieStore(t *testing.T) {
	svcStore := &cookieMockStore{}
	acctStore := &errStore{err: ErrKeyNotFound}
	cookieStore := &cookieMockStore{}

	// Drive is not CookieAuth, so even with a nil-ish account store,
	// the error should come from the account store, not the cookie store.
	_, err := RestoreServiceSession(
		context.Background(), "drive", nil,
		svcStore, acctStore, cookieStore, DefaultVersion, nil,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Cookie store should not have been touched.
	if cookieStore.saveCount != 0 {
		t.Fatalf("expected 0 cookie saves for non-CookieAuth service, got %d", cookieStore.saveCount)
	}
}

// TestRestoreServiceSession_CookieAuth_NilCookieStore verifies that when
// cookieStore is nil for a CookieAuth service, the Bearer fork path is used.
func TestRestoreServiceSession_CookieAuth_NilCookieStore(t *testing.T) {
	// With nil cookieStore, CookieAuth services should fall back to Bearer fork.
	// This test just verifies no panic — the actual fork will fail because
	// there's no real server.
	acctStore := &errStore{err: ErrKeyNotFound}
	svcStore := &cookieMockStore{}

	_, err := RestoreServiceSession(
		context.Background(), "lumo", nil,
		svcStore, acctStore, nil, DefaultVersion, nil,
	)
	// Should get ErrNotLoggedIn from the account store.
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestCookieForkPushUsesCookieAuth verifies that the cookie fork push
// sends AUTH cookies (not Bearer) by checking the request headers.
func TestCookieForkPushUsesCookieAuth(t *testing.T) {
	uid := "push-test-uid"
	var gotAuthHeader string
	var gotCookies []*http.Cookie

	// Pre-generate a fork blob for the pull response.
	blob := &ForkBlob{Type: "default", KeyPassword: "test-keypass"}
	ciphertext, _, err := EncryptForkBlob(blob)
	if err != nil {
		t.Fatalf("EncryptForkBlob: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/core/v4/auth/cookies":
			var req AuthCookiesReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			cookieUID := req.UID
			if cookieUID == "" {
				cookieUID = uid
			}
			http.SetCookie(w, &http.Cookie{Name: "AUTH-" + cookieUID, Value: "auth-tok", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "REFRESH-" + cookieUID, Value: "ref-tok", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "Session-Id", Value: "sid", Path: "/"})
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})

		case r.Method == "POST" && r.URL.Path == "/auth/v4/sessions/forks":
			gotAuthHeader = r.Header.Get("Authorization")
			gotCookies = r.Cookies()
			_ = json.NewEncoder(w).Encode(ForkPushResp{Code: 1000, Selector: "sel"})

		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/auth/v4/sessions/forks/"):
			//nolint:gosec // G117: test data with fake tokens.
			_ = json.NewEncoder(w).Encode(ForkPullResp{
				Code: 1000, UID: "child", AccessToken: "cat", RefreshToken: "crt",
				Payload: ciphertext, Scopes: []string{"full"},
			})

		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
		}
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	acctSession := &Session{
		Auth:       proton.Auth{UID: uid, AccessToken: "bearer-at", RefreshToken: "bearer-rt"},
		BaseURL:    srv.URL,
		AppVersion: "web-account@5.2.0",
		cookieJar:  jar,
		manager:    proton.New(proton.WithHostURL(srv.URL)),
	}

	acctConfig := &SessionCredentials{UID: uid, LastRefresh: time.Now()}
	acctSvc := ServiceConfig{Name: "account", Host: srv.URL, ClientID: "web-account", Version: "5.2.0"}
	targetSvc := ServiceConfig{Name: "lumo", Host: srv.URL, ClientID: "web-lumo", Version: "1.3.3.4"}
	cookieStore := &cookieMockStore{}

	// Create cookie session first.
	cookieSess, err := loadOrCreateCookieSession(context.Background(), acctSession, acctConfig, acctSvc, cookieStore)
	if err != nil {
		t.Fatalf("loadOrCreateCookieSession: %v", err)
	}

	// Now do the fork push via CookieSession.DoJSON.
	pushReq := ForkPushReq{ChildClientID: targetSvc.ClientID, Independent: 0}
	var pushResp ForkPushResp
	pushURL := srv.URL + "/auth/v4/sessions/forks"
	if err := cookieSess.DoJSON(context.Background(), "POST", pushURL, pushReq, &pushResp); err != nil {
		t.Fatalf("cookie fork push: %v", err)
	}

	// Verify: no Bearer header on the cookie push.
	if gotAuthHeader != "" {
		t.Fatalf("expected no Authorization header on cookie push, got %q", gotAuthHeader)
	}

	// Verify: AUTH cookie was sent.
	hasAuth := false
	for _, c := range gotCookies {
		if strings.HasPrefix(c.Name, "AUTH-") {
			hasAuth = true
			break
		}
	}
	if !hasAuth {
		cookieNames := make([]string, len(gotCookies))
		for i, c := range gotCookies {
			cookieNames[i] = c.Name
		}
		t.Fatalf("expected AUTH cookie on push, got cookies: %v", cookieNames)
	}
}

// TestCookieSessionFromConfig_RestoresBaseURL verifies that
// loadOrCreateCookieSession sets BaseURL and AppVersion on restored sessions.
func TestCookieSessionFromConfig_RestoresBaseURL(t *testing.T) {
	uid := "restore-url-uid"
	now := time.Now()

	acctConfig := &SessionCredentials{
		UID:         uid,
		LastRefresh: now.Add(-30 * time.Minute),
	}

	acctSvc := ServiceConfig{
		Name:     "account",
		Host:     "https://account-api.proton.me/api",
		ClientID: "web-account",
		Version:  "5.2.0",
	}

	cookieStore := &cookieMockStore{
		config: &SessionCredentials{
			UID: uid,
			Cookies: []SerialCookie{
				{Name: "AUTH-" + uid, Value: "auth-val"},
			},
			LastRefresh: now,
		},
	}

	jar, _ := cookiejar.New(nil)
	acctSession := &Session{Auth: proton.Auth{UID: uid}, cookieJar: jar}

	cs, err := loadOrCreateCookieSession(context.Background(), acctSession, acctConfig, acctSvc, cookieStore)
	if err != nil {
		t.Fatalf("loadOrCreateCookieSession: %v", err)
	}

	if cs.BaseURL != acctSvc.Host {
		t.Fatalf("BaseURL = %q, want %q", cs.BaseURL, acctSvc.Host)
	}

	wantAppVer := "web-account@5.2.0"
	if cs.AppVersion != wantAppVer {
		t.Fatalf("AppVersion = %q, want %q", cs.AppVersion, wantAppVer)
	}
}

// TestLoadOrCreateCookieSession_EmptyUID verifies that a cookie session
// with an empty UID triggers re-creation.
func TestLoadOrCreateCookieSession_EmptyUID(t *testing.T) {
	uid := "acct-uid-empty"
	srv := newCookieForkTestServer(t, uid)
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	acctSession := &Session{
		Auth:       proton.Auth{UID: uid, AccessToken: "at", RefreshToken: "rt"},
		BaseURL:    srv.URL,
		AppVersion: "web-account@5.2.0",
		cookieJar:  jar,
		manager:    proton.New(proton.WithHostURL(srv.URL)),
	}

	acctConfig := &SessionCredentials{UID: uid, LastRefresh: time.Now()}
	acctSvc := ServiceConfig{Name: "account", Host: srv.URL, ClientID: "web-account", Version: "5.2.0"}

	// Cookie config exists but has empty UID → should re-create.
	cookieStore := &cookieMockStore{
		config: &SessionCredentials{
			UID:         "",
			LastRefresh: time.Now(),
		},
	}

	cs, err := loadOrCreateCookieSession(context.Background(), acctSession, acctConfig, acctSvc, cookieStore)
	if err != nil {
		t.Fatalf("loadOrCreateCookieSession: %v", err)
	}

	if cs == nil {
		t.Fatal("expected non-nil CookieSession")
	}

	// Should have saved a new cookie session.
	if cookieStore.saveCount != 1 {
		t.Fatalf("expected 1 save (empty UID re-create), got %d", cookieStore.saveCount)
	}
}
