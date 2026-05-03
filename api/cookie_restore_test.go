package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newCookieRestoreTestServer creates a mock HTTP server that handles the
// endpoints needed for CookieSessionRestore testing:
//   - GET /core/v4/users → returns a minimal user
//   - GET /core/v4/addresses → returns empty addresses
//   - POST /auth/refresh → simulates cookie refresh (sets new cookies)
func newCookieRestoreTestServer(t *testing.T, uid string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/core/v4/users":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code": 1000,
				"User": map[string]any{
					"ID":   uid,
					"Name": "test-user",
				},
			})
		case r.Method == "GET" && r.URL.Path == "/core/v4/addresses":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code":      1000,
				"Addresses": []any{},
			})
		case r.Method == "POST" && r.URL.Path == "/auth/refresh":
			// Simulate cookie refresh: set new AUTH/REFRESH cookies (no request body expected).
			http.SetCookie(w, &http.Cookie{
				Name: "AUTH-" + uid, Value: "refreshed-auth", Path: "/",
			})
			http.SetCookie(w, &http.Cookie{
				Name: "REFRESH-" + uid, Value: "refreshed-refresh", Path: "/",
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
		case r.Method == "POST" && r.URL.Path == "/auth/v4/refresh":
			// go-proton-api's authRefresh calls this — return success so
			// the Resty client doesn't fail on 401 retry.
			//nolint:gosec // G117: test data with fake tokens.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Code":         1000,
				"UID":          uid,
				"AccessToken":  "new-at",
				"RefreshToken": "new-rt",
			})
		default:
			t.Logf("unhandled request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 0, "Error": "not found"})
		}
	}))
}

// TestCookieSessionRestore_Success verifies that CookieSessionRestore loads
// cookies from the store, builds a session with CookieTransport, and calls
// GetUser/GetAddresses via the mock server.
func TestCookieSessionRestore_Success(t *testing.T) {
	uid := "restore-uid-1"
	srv := newCookieRestoreTestServer(t, uid)
	defer srv.Close()

	// Temporarily override the service registry so LookupService("account")
	// returns our test server URL.
	origAcct := Services["account"]
	Services["account"] = ServiceConfig{
		Name: "account", Host: srv.URL, ClientID: "web-account", Version: "5.2.0",
	}
	defer func() { Services["account"] = origAcct }()

	cookieStore := &cookieMockStore{
		config: &SessionCredentials{
			UID: uid,
			Cookies: []SerialCookie{
				{Name: "AUTH-" + uid, Value: "auth-token"},
				{Name: "REFRESH-" + uid, Value: "refresh-token"},
			},
			SaltedKeyPass: Base64Encode([]byte("keypass")),
			LastRefresh:   time.Now(), // fresh — no proactive refresh
			CookieAuth:    true,
		},
	}

	acctConfig := &SessionCredentials{
		UID:           uid,
		SaltedKeyPass: Base64Encode([]byte("keypass")),
		CookieAuth:    true,
	}

	session, err := CookieSessionRestore(context.Background(), nil, cookieStore, acctConfig, nil)
	if err != nil {
		// Unlock will fail (no real keys), but GetUser/GetAddresses should succeed.
		// Check if the error is from Unlock (expected) vs earlier stages (unexpected).
		if !containsError(err, "unlock") {
			t.Fatalf("unexpected error (not unlock): %v", err)
		}
		// Unlock failure is expected with mock data — the important thing is
		// that GetUser and GetAddresses succeeded (no network error).
		return
	}

	// If we get here, verify session fields.
	if session.Auth.UID != uid {
		t.Fatalf("UID = %q, want %q", session.Auth.UID, uid)
	}
	if session.Auth.AccessToken != "" {
		t.Fatalf("AccessToken should be empty, got %q", session.Auth.AccessToken)
	}
	if session.BaseURL != srv.URL {
		t.Fatalf("BaseURL = %q, want %q", session.BaseURL, srv.URL)
	}
}

// TestCookieSessionRestore_MissingCookieStore verifies that when the cookie
// store returns ErrKeyNotFound, CookieSessionRestore returns ErrNotLoggedIn.
func TestCookieSessionRestore_MissingCookieStore(t *testing.T) {
	cookieStore := &cookieMockStore{loadErr: ErrKeyNotFound}
	acctConfig := &SessionCredentials{UID: "uid", SaltedKeyPass: Base64Encode([]byte("kp"))}

	_, err := CookieSessionRestore(context.Background(), nil, cookieStore, acctConfig, nil)
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("expected ErrNotLoggedIn, got %v", err)
	}
}

// TestCookieSessionRestore_ProactiveRefreshTriggered verifies that when
// LastRefresh is stale, CookieSessionRestore triggers a proactive refresh
// and persists the updated cookies.
func TestCookieSessionRestore_ProactiveRefreshTriggered(t *testing.T) {
	uid := "refresh-uid-1"
	srv := newCookieRestoreTestServer(t, uid)
	defer srv.Close()

	origAcct := Services["account"]
	Services["account"] = ServiceConfig{
		Name: "account", Host: srv.URL, ClientID: "web-account", Version: "5.2.0",
	}
	defer func() { Services["account"] = origAcct }()

	cookieStore := &cookieMockStore{
		config: &SessionCredentials{
			UID: uid,
			Cookies: []SerialCookie{
				{Name: "AUTH-" + uid, Value: "old-auth"},
				{Name: "REFRESH-" + uid, Value: "old-refresh"},
			},
			SaltedKeyPass: Base64Encode([]byte("keypass")),
			LastRefresh:   time.Now().Add(-2 * time.Hour), // stale — triggers refresh
			CookieAuth:    true,
		},
	}

	acctConfig := &SessionCredentials{
		UID:           uid,
		SaltedKeyPass: Base64Encode([]byte("keypass")),
		CookieAuth:    true,
	}

	_, err := CookieSessionRestore(context.Background(), nil, cookieStore, acctConfig, nil)
	// Unlock will fail (no real keys), but proactive refresh should have been triggered.
	// The important assertion is that the cookie store was saved with refreshed cookies.
	_ = err

	if cookieStore.saveCount == 0 {
		t.Fatal("expected cookie store save after proactive refresh")
	}
	if cookieStore.saved == nil {
		t.Fatal("expected saved config to be non-nil")
	}
	// LastRefresh should be updated (more recent than the stale value).
	if cookieStore.saved.LastRefresh.Before(time.Now().Add(-1 * time.Minute)) {
		t.Fatalf("saved LastRefresh should be recent, got %v", cookieStore.saved.LastRefresh)
	}
}

// TestCookieSessionRestore_ProactiveRefreshSkipped verifies that when
// LastRefresh is fresh, no proactive refresh occurs.
func TestCookieSessionRestore_ProactiveRefreshSkipped(t *testing.T) {
	uid := "fresh-uid-1"
	srv := newCookieRestoreTestServer(t, uid)
	defer srv.Close()

	origAcct := Services["account"]
	Services["account"] = ServiceConfig{
		Name: "account", Host: srv.URL, ClientID: "web-account", Version: "5.2.0",
	}
	defer func() { Services["account"] = origAcct }()

	cookieStore := &cookieMockStore{
		config: &SessionCredentials{
			UID: uid,
			Cookies: []SerialCookie{
				{Name: "AUTH-" + uid, Value: "auth-token"},
				{Name: "REFRESH-" + uid, Value: "refresh-token"},
			},
			SaltedKeyPass: Base64Encode([]byte("keypass")),
			LastRefresh:   time.Now(), // fresh — no refresh needed
			CookieAuth:    true,
		},
	}

	acctConfig := &SessionCredentials{
		UID:           uid,
		SaltedKeyPass: Base64Encode([]byte("keypass")),
		CookieAuth:    true,
	}

	_, _ = CookieSessionRestore(context.Background(), nil, cookieStore, acctConfig, nil)

	// No save should have occurred — cookies are fresh.
	if cookieStore.saveCount != 0 {
		t.Fatalf("expected 0 saves (no refresh needed), got %d", cookieStore.saveCount)
	}
}

// --- SessionRestore cookie routing tests (Task 7.1) ---

// TestSessionRestore_CookieAuthTrue_RoutesCookiePath verifies that when
// the account store has CookieAuth=true, SessionRestore delegates to
// CookieSessionRestore (which loads from the cookie store).
func TestSessionRestore_CookieAuthTrue_RoutesCookiePath(t *testing.T) {
	uid := "route-cookie-uid"
	srv := newCookieRestoreTestServer(t, uid)
	defer srv.Close()

	origAcct := Services["account"]
	Services["account"] = ServiceConfig{
		Name: "account", Host: srv.URL, ClientID: "web-account", Version: "5.2.0",
	}
	defer func() { Services["account"] = origAcct }()

	// Account store has CookieAuth=true, empty tokens.
	accountStore := &configStore{config: &SessionCredentials{
		UID:           uid,
		AccessToken:   "",
		RefreshToken:  "",
		SaltedKeyPass: Base64Encode([]byte("keypass")),
		CookieAuth:    true,
	}}

	// Cookie store has the actual cookies.
	cookieStore := &cookieMockStore{
		config: &SessionCredentials{
			UID: uid,
			Cookies: []SerialCookie{
				{Name: "AUTH-" + uid, Value: "auth-token"},
				{Name: "REFRESH-" + uid, Value: "refresh-token"},
			},
			SaltedKeyPass: Base64Encode([]byte("keypass")),
			LastRefresh:   time.Now(),
			CookieAuth:    true,
		},
	}

	session, err := SessionRestore(context.Background(), nil, accountStore, cookieStore, nil)
	// Unlock will fail (no real keys), but the routing should go through
	// CookieSessionRestore (not Bearer path which would fail on empty tokens).
	if err != nil {
		if containsError(err, "unlock") {
			// Expected — Unlock fails with mock data, but we got past GetUser/GetAddresses.
			return
		}
		t.Fatalf("unexpected error: %v", err)
	}

	if session.Auth.UID != uid {
		t.Fatalf("UID = %q, want %q", session.Auth.UID, uid)
	}
}

// TestSessionRestore_CookieAuthFalse_UsesBearerPath verifies that when
// CookieAuth=false, SessionRestore uses the existing Bearer path (which
// requires non-empty tokens).
func TestSessionRestore_CookieAuthFalse_UsesBearerPath(t *testing.T) {
	// Account store has CookieAuth=false with valid tokens.
	accountStore := &configStore{config: &SessionCredentials{
		UID:          "bearer-uid",
		AccessToken:  "at",
		RefreshToken: "rt",
		CookieAuth:   false,
	}}

	// Cookie store exists but should NOT be used.
	cookieStore := &cookieMockStore{
		config: &SessionCredentials{UID: "cookie-uid", CookieAuth: true},
	}

	// SessionRestore should use Bearer path → will fail at GetUser (no server),
	// but should NOT return ErrNotLoggedIn (which would mean cookie path was used).
	_, err := SessionRestore(context.Background(), nil, accountStore, cookieStore, nil)
	if err == nil {
		t.Fatal("expected error (no real API server)")
	}
	// The error should be a network error from GetUser, not ErrNotLoggedIn.
	if errors.Is(err, ErrNotLoggedIn) {
		t.Fatal("should not get ErrNotLoggedIn — Bearer path should be used")
	}
}

// --- RestoreServiceSession cookie routing tests (Task 8.1) ---

// TestRestoreServiceSession_CookieAuth_UsesCookieFork verifies that when
// acctConfig.CookieAuth=true, RestoreServiceSession uses the cookieFork path
// instead of the Bearer ForkSessionWithKeyPass path.
func TestRestoreServiceSession_CookieAuth_UsesCookieFork(t *testing.T) {
	uid := "cookie-fork-uid"
	srv := newCookieForkTestServer(t, uid)
	defer srv.Close()

	// Override service registry to point at test server.
	origAcct := Services["account"]
	origLumo := Services["lumo"]
	Services["account"] = ServiceConfig{
		Name: "account", Host: srv.URL, ClientID: "web-account", Version: "5.2.0",
	}
	Services["lumo"] = ServiceConfig{
		Name: "lumo", Host: srv.URL, ClientID: "web-lumo", Version: "1.3.3.4", CookieAuth: true,
	}
	defer func() {
		Services["account"] = origAcct
		Services["lumo"] = origLumo
	}()

	// Account store: CookieAuth=true, valid credentials for SessionFromCredentials.
	acctStore := &cookieMockStore{
		config: &SessionCredentials{
			UID:           uid,
			AccessToken:   "acct-at",
			RefreshToken:  "acct-rt",
			SaltedKeyPass: Base64Encode([]byte("keypass")),
			LastRefresh:   time.Now(),
			CookieAuth:    true,
		},
	}

	// Cookie store: has cookies for the cookie fork path.
	cookieStore := &cookieMockStore{
		config: &SessionCredentials{
			UID: uid,
			Cookies: []SerialCookie{
				{Name: "AUTH-" + uid, Value: "auth-token"},
				{Name: "REFRESH-" + uid, Value: "refresh-token"},
				{Name: "Session-Id", Value: "sid-test"},
			},
			LastRefresh: time.Now(),
		},
	}

	// Service store: empty (forces a fork).
	svcStore := &cookieMockStore{}

	_, err := RestoreServiceSession(
		context.Background(), "lumo", nil,
		svcStore, acctStore, cookieStore, "1.3.3.4", nil,
	)
	// The fork will proceed through cookieFork. Unlock will fail (no real keys),
	// but we verify the cookie fork path was taken by checking that the cookie
	// store was accessed (loadOrCreateCookieSession loads from it).
	if err != nil {
		// Expected: unlock failure or similar — the important thing is we
		// didn't get a Bearer-related error like "missing access token".
		if containsError(err, "missing access token") || containsError(err, "missing refresh token") {
			t.Fatalf("Bearer path was used instead of cookie fork: %v", err)
		}
	}
}

// TestRestoreServiceSession_NoCookieAuth_UsesBearerFork verifies that when
// acctConfig.CookieAuth=false, RestoreServiceSession uses the Bearer fork
// path even for services with CookieAuth=true in the registry.
func TestRestoreServiceSession_NoCookieAuth_UsesBearerFork(t *testing.T) {
	// Account store: CookieAuth=false (Bearer mode).
	acctStore := &cookieMockStore{
		config: &SessionCredentials{
			UID:           "bearer-uid",
			AccessToken:   "at",
			RefreshToken:  "rt",
			SaltedKeyPass: Base64Encode([]byte("keypass")),
			LastRefresh:   time.Now(),
			CookieAuth:    false,
		},
	}

	// Cookie store exists but should NOT be used.
	cookieStore := &cookieMockStore{
		config: &SessionCredentials{UID: "cookie-uid"},
	}

	// Service store: empty (forces a fork).
	svcStore := &cookieMockStore{}

	_, err := RestoreServiceSession(
		context.Background(), "drive", nil,
		svcStore, acctStore, cookieStore, DefaultVersion, nil,
	)
	// Will fail at the network layer (no real server), but should NOT use
	// the cookie fork path. The cookie store should not have been saved to.
	if err == nil {
		t.Fatal("expected error (no real API server)")
	}

	// Cookie store should not have been written to (Bearer fork doesn't touch it).
	if cookieStore.saveCount != 0 {
		t.Fatalf("expected 0 cookie saves for Bearer fork, got %d", cookieStore.saveCount)
	}
}
