package account

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/major0/proton-utils/api"
)

// TestCookieTransport_401TriggersRefresh verifies that when CookieTransport
// receives a 401 response and has a CookieSession attached, it calls
// RefreshCookies and retries the request.
func TestCookieTransport_401TriggersRefresh(t *testing.T) {
	uid := "ct-401-uid"
	var requestCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/auth/refresh":
			// Cookie refresh endpoint: set new cookies (no request body expected).
			http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: test cookie — security attributes not relevant here
				Name: "AUTH-" + uid, Value: "refreshed-auth", Path: "/",
			})
			http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: test cookie — security attributes not relevant here
				Name: "REFRESH-" + uid, Value: "refreshed-refresh", Path: "/",
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})

		case r.Method == "GET" && r.URL.Path == "/test/endpoint":
			count := requestCount.Add(1)
			if count == 1 {
				// First request: return 401 to trigger refresh.
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"Code": 401, "Error": "Unauthorized"})
				return
			}
			// Retry after refresh: return success.
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000, "Data": "ok"})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// Build a CookieSession with the test server's cookies.
	cs := CookieSessionFromConfig(&CookieSessionConfig{
		UID: uid,
		Cookies: []api.SerialCookie{
			{Name: "AUTH-" + uid, Value: "old-auth"},
			{Name: "REFRESH-" + uid, Value: "old-refresh"},
		},
	}, srv.URL)
	cs.AppVersion = "web-account@5.2.0"

	// Build CookieTransport with the CookieSession attached.
	store := &cookieMockStore{
		config: &api.SessionCredentials{
			UID:         uid,
			LastRefresh: time.Now(),
		},
	}
	ct := &CookieTransport{Base: http.DefaultTransport}
	ct.SetCookieSession(cs, store)

	// Make a request through the transport.
	req, _ := http.NewRequest("GET", srv.URL+"/test/endpoint", nil)
	req.Header.Set("Authorization", "Bearer fake-token")

	client := &http.Client{Transport: ct, Jar: cs.cookieJar}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// The transport should have retried after refresh → 200.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after retry, got %d", resp.StatusCode)
	}

	// Verify the endpoint was called twice (original 401 + retry).
	if got := requestCount.Load(); got != 2 {
		t.Fatalf("expected 2 requests, got %d", got)
	}

	// Verify cookies were persisted to the store.
	if store.saveCount == 0 {
		t.Fatal("expected cookie store save after refresh")
	}
}

// TestCookieTransport_RefreshFailReturnsOriginal401 verifies that when
// cookie refresh fails, the original 401 response is returned.
func TestCookieTransport_RefreshFailReturnsOriginal401(t *testing.T) {
	uid := "ct-fail-uid"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/auth/refresh":
			// Refresh fails with 401 (expired REFRESH cookie).
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 401, "Error": "Invalid refresh"})

		case r.Method == "GET" && r.URL.Path == "/test/endpoint":
			// Always return 401.
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 401, "Error": "Unauthorized"})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cs := CookieSessionFromConfig(&CookieSessionConfig{
		UID: uid,
		Cookies: []api.SerialCookie{
			{Name: "AUTH-" + uid, Value: "expired-auth"},
			{Name: "REFRESH-" + uid, Value: "expired-refresh"},
		},
	}, srv.URL)
	cs.AppVersion = "web-account@5.2.0"

	store := &cookieMockStore{
		config: &api.SessionCredentials{UID: uid, LastRefresh: time.Now()},
	}
	ct := &CookieTransport{Base: http.DefaultTransport}
	ct.SetCookieSession(cs, store)

	req, _ := http.NewRequest("GET", srv.URL+"/test/endpoint", nil)
	client := &http.Client{Transport: ct, Jar: cs.cookieJar}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Should return the original 401 since refresh failed.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	// Store should NOT have been saved (refresh failed).
	if store.saveCount != 0 {
		t.Fatalf("expected 0 saves after failed refresh, got %d", store.saveCount)
	}
}

// TestCookieTransport_SuccessfulRefreshPersistsCookies verifies that after
// a successful 401 refresh, the updated cookies are persisted to the store.
func TestCookieTransport_SuccessfulRefreshPersistsCookies(t *testing.T) {
	uid := "ct-persist-uid"
	var requestCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/auth/refresh":
			// Cookie refresh endpoint (no request body expected).
			http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: test cookie — security attributes not relevant here
				Name: "AUTH-" + uid, Value: "new-auth-value", Path: "/",
			})
			http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: test cookie — security attributes not relevant here
				Name: "REFRESH-" + uid, Value: "new-refresh-value", Path: "/",
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})

		case r.Method == "GET" && r.URL.Path == "/test/endpoint":
			count := requestCount.Add(1)
			if count == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"Code": 401, "Error": "Unauthorized"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cs := CookieSessionFromConfig(&CookieSessionConfig{
		UID: uid,
		Cookies: []api.SerialCookie{
			{Name: "AUTH-" + uid, Value: "old-auth"},
			{Name: "REFRESH-" + uid, Value: "old-refresh"},
		},
	}, srv.URL)
	cs.AppVersion = "web-account@5.2.0"

	store := &cookieMockStore{
		config: &api.SessionCredentials{
			UID: uid,
			Cookies: []api.SerialCookie{
				{Name: "AUTH-" + uid, Value: "old-auth"},
				{Name: "REFRESH-" + uid, Value: "old-refresh"},
			},
			LastRefresh: time.Now().Add(-2 * time.Hour),
		},
	}
	ct := &CookieTransport{Base: http.DefaultTransport}
	ct.SetCookieSession(cs, store)

	req, _ := http.NewRequest("GET", srv.URL+"/test/endpoint", nil)
	client := &http.Client{Transport: ct, Jar: cs.cookieJar}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	// Verify cookies were persisted.
	if store.saveCount == 0 {
		t.Fatal("expected cookie store save after successful refresh")
	}
	if store.saved == nil {
		t.Fatal("expected saved config to be non-nil")
	}

	// Verify the saved cookies contain the new values.
	hasNewAuth := false
	for _, c := range store.saved.Cookies {
		if c.Name == "AUTH-"+uid && c.Value == "new-auth-value" {
			hasNewAuth = true
		}
	}
	if !hasNewAuth {
		t.Fatal("saved cookies should contain the refreshed AUTH cookie")
	}
}

// TestCookieTransport_NoCookieSession_No401Retry verifies that without a
// CookieSession attached, 401 responses are returned as-is.
func TestCookieTransport_NoCookieSession_No401Retry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 401, "Error": "Unauthorized"})
	}))
	defer srv.Close()

	ct := &CookieTransport{Base: http.DefaultTransport}
	// No CookieSession attached.

	req, _ := http.NewRequest("GET", srv.URL+"/test", nil)
	client := &http.Client{Transport: ct}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}
