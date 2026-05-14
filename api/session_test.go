package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"pgregory.net/rapid"
)

// genSerialCookie generates an arbitrary SerialCookie.
func genSerialCookie(t *rapid.T) SerialCookie {
	return SerialCookie{
		Name:   rapid.String().Draw(t, "name"),
		Value:  rapid.String().Draw(t, "value"),
		Domain: rapid.String().Draw(t, "domain"),
		Path:   rapid.String().Draw(t, "path"),
	}
}

// genSessionConfig generates an arbitrary SessionCredentials with random cookies
// and timestamps.
func genSessionConfig(t *rapid.T) SessionCredentials {
	n := rapid.IntRange(0, 20).Draw(t, "numCookies")
	cookies := make([]SerialCookie, n)
	for i := range cookies {
		cookies[i] = genSerialCookie(t)
	}

	// Generate a timestamp truncated to second precision — JSON round-trips
	// time.Time at nanosecond precision via RFC 3339, but we truncate to
	// seconds to match real-world cookie timestamps and avoid false negatives
	// from sub-second jitter in marshaling formats.
	sec := rapid.Int64Range(-62135596800, 253402300799).Draw(t, "unixSec")
	ts := time.Unix(sec, 0).UTC()

	return SessionCredentials{
		UID:           rapid.String().Draw(t, "uid"),
		AccessToken:   rapid.String().Draw(t, "accessToken"),
		RefreshToken:  rapid.String().Draw(t, "refreshToken"),
		SaltedKeyPass: rapid.String().Draw(t, "saltedKeyPass"),
		Cookies:       cookies,
		LastRefresh:   ts,
		Service:       rapid.String().Draw(t, "service"),
		CookieAuth:    rapid.Bool().Draw(t, "cookieAuth"),
	}
}

// TestPropertySessionConfigCookieRoundTrip verifies that for any SessionCredentials
// with arbitrary cookies and timestamps, JSON marshal/unmarshal produces
// identical Cookies and LastRefresh.
//
// **Validates: Requirements 3.2, 3.5**
func TestPropertySessionConfigCookieRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		original := genSessionConfig(t)

		//nolint:gosec // G117: property test intentionally marshals SessionCredentials with tokens.
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		var restored SessionCredentials
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		// Verify Cookies slice equality.
		if len(original.Cookies) != len(restored.Cookies) {
			t.Fatalf("cookie count: got %d, want %d", len(restored.Cookies), len(original.Cookies))
		}
		for i, orig := range original.Cookies {
			got := restored.Cookies[i]
			if orig != got {
				t.Fatalf("cookie[%d]: got %+v, want %+v", i, got, orig)
			}
		}

		// Verify LastRefresh equality.
		if !original.LastRefresh.Equal(restored.LastRefresh) {
			t.Fatalf("LastRefresh: got %v, want %v", restored.LastRefresh, original.LastRefresh)
		}

		// Verify Service equality.
		if original.Service != restored.Service {
			t.Fatalf("Service: got %q, want %q", restored.Service, original.Service)
		}

		// Verify CookieAuth equality.
		if original.CookieAuth != restored.CookieAuth {
			t.Fatalf("CookieAuth: got %v, want %v", restored.CookieAuth, original.CookieAuth)
		}
	})
}

// genCookieName generates a valid HTTP cookie name: one or more ASCII letters
// or digits. This avoids special characters that net/http/cookiejar may reject
// or sanitize.
func genCookieName(t *rapid.T, label string) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	n := rapid.IntRange(1, 32).Draw(t, label+"Len")
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rapid.IntRange(0, len(chars)-1).Draw(t, label+"Char")]
	}
	return string(b)
}

// genCookieValue generates a valid HTTP cookie value: ASCII printable
// characters excluding semicolons, commas, spaces, and double quotes, which
// can cause cookie parsing issues.
func genCookieValue(t *rapid.T, label string) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-."
	n := rapid.IntRange(0, 64).Draw(t, label+"Len")
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rapid.IntRange(0, len(chars)-1).Draw(t, label+"Char")]
	}
	return string(b)
}

// genJarCookie generates a SerialCookie suitable for cookie jar round-trip
// testing. Domain and Path are empty because net/http/cookiejar does not
// expose these fields via Cookies() — the jar manages domain/path matching
// internally. The round-trip therefore preserves only Name and Value.
func genJarCookie(t *rapid.T, idx int) SerialCookie {
	return SerialCookie{
		Name:  genCookieName(t, fmt.Sprintf("name%d", idx)),
		Value: genCookieValue(t, fmt.Sprintf("value%d", idx)),
	}
}

// TestPropertyCookieJarRoundTrip verifies that for any set of cookie entries,
// LoadCookies followed by SerializeCookies returns equivalent cookies.
//
// The cookie jar (net/http/cookiejar) normalizes cookies: Domain is not
// returned by Cookies(), and cookies with duplicate Name+Path are deduplicated
// (last wins). The generator produces cookies with unique names, empty Domain,
// and Path="/" to ensure a clean round-trip.
//
// **Validates: Requirements 3.1, 3.2**
func TestPropertyCookieJarRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "numCookies")

		// Generate cookies with unique names to avoid jar deduplication.
		seen := make(map[string]bool, n)
		cookies := make([]SerialCookie, 0, n)
		for i := 0; i < n; i++ {
			c := genJarCookie(t, i)
			if seen[c.Name] {
				continue // skip duplicate names
			}
			seen[c.Name] = true
			cookies = append(cookies, c)
		}

		apiURL := CookieURL()

		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookiejar.New: %v", err)
		}

		LoadCookies(jar, cookies, apiURL)
		got := SerializeCookies(jar, apiURL)

		if len(got) != len(cookies) {
			t.Fatalf("cookie count: got %d, want %d", len(got), len(cookies))
		}

		// Build a map for order-independent comparison — the jar may return
		// cookies in a different order than they were inserted. Compare by
		// Name and Value only; Domain and Path are not preserved by the jar's
		// Cookies() method.
		type key struct{ Name, Value string }
		want := make(map[key]bool, len(cookies))
		for _, c := range cookies {
			want[key{c.Name, c.Value}] = true
		}
		for _, c := range got {
			k := key{c.Name, c.Value}
			if !want[k] {
				t.Fatalf("unexpected cookie: %+v", c)
			}
		}
	})
}

// --- Unit tests for cookie edge cases ---

// TestSerializeCookiesEmptyJar verifies that a fresh jar with no cookies
// produces a nil slice from SerializeCookies.
func TestSerializeCookiesEmptyJar(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	got := SerializeCookies(jar, CookieURL())
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

// TestLoadCookiesNil verifies that calling LoadCookies with a nil slice
// does not panic and leaves the jar empty.
func TestLoadCookiesNil(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	apiURL := CookieURL()
	LoadCookies(jar, nil, apiURL)

	if cookies := jar.Cookies(apiURL); len(cookies) != 0 {
		t.Fatalf("expected empty jar, got %d cookies", len(cookies))
	}
}

// TestLoadCookiesEmpty verifies that calling LoadCookies with an empty
// (non-nil) slice does not panic and leaves the jar empty.
func TestLoadCookiesEmpty(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	apiURL := CookieURL()
	LoadCookies(jar, []SerialCookie{}, apiURL)

	if cookies := jar.Cookies(apiURL); len(cookies) != 0 {
		t.Fatalf("expected empty jar, got %d cookies", len(cookies))
	}
}

// TestSessionConfigBackwardCompat verifies that JSON without the Cookies and
// LastRefresh fields deserializes cleanly into a SessionCredentials with nil
// Cookies and zero-value LastRefresh.
func TestSessionConfigBackwardCompat(t *testing.T) {
	raw := `{"uid":"u1","access_token":"a","refresh_token":"r","salted_key_pass":"k"}`
	var cfg SessionCredentials
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Cookies != nil {
		t.Fatalf("expected nil Cookies, got %v", cfg.Cookies)
	}
	if !cfg.LastRefresh.IsZero() {
		t.Fatalf("expected zero LastRefresh, got %v", cfg.LastRefresh)
	}
	if cfg.UID != "u1" || cfg.AccessToken != "a" || cfg.RefreshToken != "r" || cfg.SaltedKeyPass != "k" {
		t.Fatalf("unexpected field values: %+v", cfg)
	}
	if cfg.Service != "" {
		t.Fatalf("expected empty Service, got %q", cfg.Service)
	}
}

// TestSessionConfigLastRefreshPreserved verifies that a SessionCredentials with a
// specific LastRefresh timestamp survives JSON marshal/unmarshal with the
// timestamp preserved.
func TestSessionConfigLastRefreshPreserved(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 30, 45, 0, time.UTC)
	cfg := SessionCredentials{
		UID:           "u1",
		AccessToken:   "a",
		RefreshToken:  "r",
		SaltedKeyPass: "k",
		LastRefresh:   ts,
	}

	//nolint:gosec // G117: unit test intentionally marshals SessionCredentials with tokens.
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored SessionCredentials
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !restored.LastRefresh.Equal(ts) {
		t.Fatalf("LastRefresh: got %v, want %v", restored.LastRefresh, ts)
	}
}

// --- Session helper types for tests ---

// mockStore is a thread-safe in-memory SessionStore for testing.
type mockStore struct {
	config *SessionCredentials
}

func (m *mockStore) Load() (*SessionCredentials, error) {
	if m.config == nil {
		return &SessionCredentials{}, nil
	}
	cfg := *m.config
	return &cfg, nil
}

func (m *mockStore) Save(cfg *SessionCredentials) error {
	m.config = cfg
	return nil
}

func (m *mockStore) Delete() error           { return nil }
func (m *mockStore) List() ([]string, error) { return nil, nil }
func (m *mockStore) Switch(string) error     { return nil }

// --- Session accessor tests ---

// TestSessionAccessors verifies the simple accessor methods on Session.
func TestSessionAccessors(t *testing.T) {
	session := &Session{
		user: proton.User{ID: "user-1", Name: "test"},
		addressKeyRings: map[string]*crypto.KeyRing{
			"addr@example.com": nil,
		},
	}

	if got := session.User(); got.ID != "user-1" {
		t.Fatalf("User().ID = %q, want %q", got.ID, "user-1")
	}

	kr := session.AddressKeyRings()
	if _, ok := kr["addr@example.com"]; !ok {
		t.Fatal("AddressKeyRings missing expected key")
	}
}

// --- DoJSONCookie tests ---

// TestUnlock verifies that Unlock populates the address map. It will fail
// at the crypto layer (no real keys), but we verify the address map setup.
func TestUnlock(t *testing.T) {
	session := &Session{
		user: proton.User{
			ID:   "user-1",
			Name: "test",
		},
	}

	addrs := []proton.Address{
		{ID: "addr-1", Email: "test@example.com"},
		{ID: "addr-2", Email: "other@example.com"},
	}

	// Unlock will fail because there are no real keys, but the address
	// map should still be populated before the crypto call.
	_ = session.Unlock([]byte("keypass"), addrs)

	// Verify addresses were stored (even though Unlock returned an error).
	if len(session.addresses) != 2 {
		t.Fatalf("expected 2 addresses, got %d", len(session.addresses))
	}
	if _, ok := session.addresses["test@example.com"]; !ok {
		t.Fatal("missing test@example.com in address map")
	}
}

// --- resolveAppVersion tests ---

// TestResolveAppVersion_AbsoluteKnownHost verifies that an absolute URL
// targeting a known service host resolves to that service's app version.
func TestResolveAppVersion_AbsoluteKnownHost(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	s := &Session{
		AppVersion: "web-account@5.0.367.1",
		cookieJar:  jar,
	}

	tests := []struct {
		url  string
		want string
	}{
		{"https://account.proton.me/api/core/v4/users", "web-account@5.0.367.1"},
		{"https://lumo.proton.me/api/lumo/v1/spaces", "web-lumo@1.3.3.4"},
		{"https://drive-api.proton.me/api/drive/shares", "web-drive@5.2.0"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := s.resolveAppVersion(tt.url)
			if got != tt.want {
				t.Fatalf("resolveAppVersion(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

// TestResolveAppVersion_RelativePath verifies that relative paths fall back
// to s.AppVersion.
func TestResolveAppVersion_RelativePath(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	s := &Session{
		AppVersion: "web-account@5.0.367.1",
		cookieJar:  jar,
	}

	got := s.resolveAppVersion("/core/v4/users")
	if got != s.AppVersion {
		t.Fatalf("resolveAppVersion(relative) = %q, want %q", got, s.AppVersion)
	}
}

// TestResolveAppVersion_UnknownHost verifies that an absolute URL targeting
// an unknown host falls back to s.AppVersion.
func TestResolveAppVersion_UnknownHost(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	s := &Session{
		AppVersion: "web-account@5.0.367.1",
		cookieJar:  jar,
	}

	got := s.resolveAppVersion("https://unknown.example.com/api/foo")
	if got != s.AppVersion {
		t.Fatalf("resolveAppVersion(unknown) = %q, want %q", got, s.AppVersion)
	}
}

// TestResolveAppVersion_NeverEmpty verifies that resolveAppVersion never
// returns an empty string when s.AppVersion is set.
func TestResolveAppVersion_NeverEmpty(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	s := &Session{
		AppVersion: "web-account@5.0.367.1",
		cookieJar:  jar,
	}

	inputs := []string{
		"/relative/path",
		"https://account.proton.me/api/foo",
		"https://unknown.host/bar",
		"",
		"not-a-url",
	}

	for _, input := range inputs {
		got := s.resolveAppVersion(input)
		if got == "" {
			t.Fatalf("resolveAppVersion(%q) returned empty string", input)
		}
	}
}

// --- DoJSON/DoSSE resolveAppVersion integration tests ---

// TestDoJSON_AbsoluteURLResolvesAppVersion verifies that DoJSON sets the
// correct x-pm-appversion when given an absolute URL to a known host.
func TestDoJSON_AbsoluteURLResolvesAppVersion(t *testing.T) {
	var gotAppVersion string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAppVersion = r.Header.Get("x-pm-appversion")
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	s := &Session{
		Auth:       proton.Auth{UID: "uid", AccessToken: "at"},
		AppVersion: "web-account@5.0.367.1",
		cookieJar:  jar,
	}

	// Relative path should use session's AppVersion.
	s.BaseURL = srv.URL
	err := s.DoJSON(context.Background(), "GET", "/core/v4/users", nil, nil)
	if err != nil {
		t.Fatalf("DoJSON relative: %v", err)
	}
	if gotAppVersion != "web-account@5.0.367.1" {
		t.Fatalf("relative path appversion = %q, want %q", gotAppVersion, "web-account@5.0.367.1")
	}

	// Absolute URL to the test server (unknown host) should fall back to session's AppVersion.
	err = s.DoJSON(context.Background(), "GET", srv.URL+"/core/v4/users", nil, nil)
	if err != nil {
		t.Fatalf("DoJSON absolute unknown: %v", err)
	}
	if gotAppVersion != "web-account@5.0.367.1" {
		t.Fatalf("unknown host appversion = %q, want %q", gotAppVersion, "web-account@5.0.367.1")
	}
}

// TestDoSSE_AbsoluteURLResolvesAppVersion verifies that DoSSE sets the
// correct x-pm-appversion when given an absolute URL.
func TestDoSSE_AbsoluteURLResolvesAppVersion(t *testing.T) {
	var gotAppVersion string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAppVersion = r.Header.Get("x-pm-appversion")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	s := &Session{
		Auth:       proton.Auth{UID: "uid", AccessToken: "at"},
		AppVersion: "web-account@5.0.367.1",
		cookieJar:  jar,
	}

	// Relative path should use session's AppVersion.
	s.BaseURL = srv.URL
	body, err := s.DoSSE(context.Background(), "/events/stream", nil)
	if err != nil {
		t.Fatalf("DoSSE relative: %v", err)
	}
	_ = body.Close()
	if gotAppVersion != "web-account@5.0.367.1" {
		t.Fatalf("relative path appversion = %q, want %q", gotAppVersion, "web-account@5.0.367.1")
	}

	// Absolute URL to the test server (unknown host) should fall back.
	body, err = s.DoSSE(context.Background(), srv.URL+"/events/stream", nil)
	if err != nil {
		t.Fatalf("DoSSE absolute unknown: %v", err)
	}
	_ = body.Close()
	if gotAppVersion != "web-account@5.0.367.1" {
		t.Fatalf("unknown host appversion = %q, want %q", gotAppVersion, "web-account@5.0.367.1")
	}
}

// --- DoJSONCookie tests ---

// TestDoJSONCookie_SendsAuthHeaders verifies that DoJSONCookie sends both
// Bearer auth (as fallback) and cookie auth, plus x-pm-uid.
func TestDoJSONCookie_SendsAuthHeaders(t *testing.T) {
	var gotAuth, gotUID, gotAppVersion string
	var gotCookies []*http.Cookie

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUID = r.Header.Get("x-pm-uid")
		gotAppVersion = r.Header.Get("x-pm-appversion")
		gotCookies = r.Cookies()
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	// Set an AUTH-* cookie in the jar for the test server.
	srvURL, _ := url.Parse(srv.URL)
	jar.SetCookies(srvURL, []*http.Cookie{
		{Name: "AUTH-parent-uid", Value: "parent-token"}, //nolint:gosec // G124: test cookie — security attributes not relevant here
		{Name: "Session-Id", Value: "sid-123"},           //nolint:gosec // G124: test cookie — security attributes not relevant here
	})

	s := &Session{
		Auth:       proton.Auth{UID: "parent-uid", AccessToken: "parent-at"},
		AppVersion: "web-account@5.0.367.1",
		BaseURL:    srv.URL,
		cookieJar:  jar,
	}

	err := s.DoJSONCookie(context.Background(), "POST", "/auth/v4/sessions/forks", map[string]string{"test": "data"}, nil)
	if err != nil {
		t.Fatalf("DoJSONCookie: %v", err)
	}

	// Bearer auth is sent as fallback.
	if gotAuth != "Bearer parent-at" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer parent-at")
	}

	// x-pm-uid must be present.
	if gotUID != "parent-uid" {
		t.Fatalf("x-pm-uid = %q, want %q", gotUID, "parent-uid")
	}

	// x-pm-appversion should be resolved (falls back to session default for test server).
	if gotAppVersion != "web-account@5.0.367.1" {
		t.Fatalf("x-pm-appversion = %q, want %q", gotAppVersion, "web-account@5.0.367.1")
	}

	// AUTH-* cookie must be sent.
	hasAuthCookie := false
	for _, c := range gotCookies {
		if strings.HasPrefix(c.Name, "AUTH-") {
			hasAuthCookie = true
			break
		}
	}
	if !hasAuthCookie {
		t.Fatal("expected AUTH-* cookie to be sent, but none found")
	}
}

// TestDoJSONCookie_UnmarshalResult verifies that DoJSONCookie correctly
// unmarshals the response body into the result parameter.
func TestDoJSONCookie_UnmarshalResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":     1000,
			"Selector": "fork-sel-abc",
		})
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	s := &Session{
		Auth:       proton.Auth{UID: "uid", AccessToken: "at"},
		AppVersion: "web-account@5.0.367.1",
		BaseURL:    srv.URL,
		cookieJar:  jar,
	}

	var result struct {
		Code     int    `json:"Code"`
		Selector string `json:"Selector"`
	}
	err := s.DoJSONCookie(context.Background(), "POST", "/auth/v4/sessions/forks", nil, &result)
	if err != nil {
		t.Fatalf("DoJSONCookie: %v", err)
	}
	if result.Selector != "fork-sel-abc" {
		t.Fatalf("Selector = %q, want %q", result.Selector, "fork-sel-abc")
	}
}

// TestDoJSONCookie_APIError verifies that DoJSONCookie returns *Error on
// non-1000 API codes.
func TestDoJSONCookie_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Code":  9100,
			"Error": "insufficient scope",
		})
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	s := &Session{
		Auth:       proton.Auth{UID: "uid", AccessToken: "at"},
		AppVersion: "web-account@5.0.367.1",
		BaseURL:    srv.URL,
		cookieJar:  jar,
	}

	err := s.DoJSONCookie(context.Background(), "POST", "/auth/v4/sessions/forks", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}

	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if apiErr.Code != 9100 {
		t.Fatalf("Code = %d, want 9100", apiErr.Code)
	}
}
