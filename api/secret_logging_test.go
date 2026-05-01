package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
)

// Sentinel constants for secret logging tests. These are injected as
// token/cookie values and must never appear in any log output.
const (
	sentinelAccess  = "SECRET_ACCESS_TOKEN_a1b2c3d4e5f6"
	sentinelRefresh = "SECRET_REFRESH_TOKEN_f6e5d4c3b2a1"
	sentinelCookie  = "SECRET_COOKIE_VALUE_deadbeef1234"
)

// captureSlog replaces the default slog handler with one that writes to
// a buffer. Returns the buffer and a cleanup function that restores the
// original handler.
func captureSlog(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	old := slog.Default()
	slog.SetDefault(slog.New(handler))
	return &buf, func() { slog.SetDefault(old) }
}

// scanForSentinels checks that none of the sentinel strings appear in output.
func scanForSentinels(t *testing.T, output string, sentinels ...string) {
	t.Helper()
	for _, s := range sentinels {
		if strings.Contains(output, s) {
			t.Fatalf("sentinel %q leaked in output:\n%s", s, output)
		}
	}
}

// TestSecretLogging_AuthHandler verifies that NewAuthHandler logs tokens
// as "<redacted>" and never leaks sentinel values.
func TestSecretLogging_AuthHandler(t *testing.T) {
	buf, cleanup := captureSlog(t)
	defer cleanup()

	jar, _ := cookiejar.New(nil)
	session := &Session{cookieJar: jar}
	store := &mockStore{}
	handler := NewAuthHandler(store, session)

	handler(proton.Auth{
		UID:          "test-uid",
		AccessToken:  sentinelAccess,
		RefreshToken: sentinelRefresh,
	})

	output := buf.String()
	scanForSentinels(t, output, sentinelAccess, sentinelRefresh, sentinelCookie)

	// Verify redaction markers are present.
	if !strings.Contains(output, "<redacted>") {
		t.Fatal("expected <redacted> in auth handler log output")
	}
}

// TestSecretLogging_SessionFromCredentials verifies that
// SessionFromCredentials logs tokens as "<redacted>".
func TestSecretLogging_SessionFromCredentials(t *testing.T) {
	buf, cleanup := captureSlog(t)
	defer cleanup()

	// SessionFromCredentials will fail at GetUser (no real server), but
	// the slog calls happen before the network call.
	_, _ = SessionFromCredentials(context.Background(), nil, &SessionConfig{
		UID:          "test-uid",
		AccessToken:  sentinelAccess,
		RefreshToken: sentinelRefresh,
	}, nil)

	output := buf.String()
	scanForSentinels(t, output, sentinelAccess, sentinelRefresh)

	if !strings.Contains(output, "<redacted>") {
		t.Fatal("expected <redacted> in SessionFromCredentials log output")
	}
}

// TestSecretLogging_SessionRestore verifies that SessionRestore logs
// tokens as "<redacted>" and never leaks sentinel values.
func TestSecretLogging_SessionRestore(t *testing.T) {
	buf, cleanup := captureSlog(t)
	defer cleanup()

	store := &configStore{config: &SessionConfig{
		UID:          "test-uid",
		AccessToken:  sentinelAccess,
		RefreshToken: sentinelRefresh,
	}}

	// Will fail at GetUser, but exercises the logging path.
	_, _ = SessionRestore(context.Background(), nil, store, nil, nil)

	output := buf.String()
	scanForSentinels(t, output, sentinelAccess, sentinelRefresh)

	if !strings.Contains(output, "<redacted>") {
		t.Fatal("expected <redacted> in SessionRestore log output")
	}
}

// TestSecretLogging_LogCookies verifies that logCookies logs only cookie
// names, never values.
func TestSecretLogging_LogCookies(t *testing.T) {
	buf, cleanup := captureSlog(t)
	defer cleanup()

	jar, _ := cookiejar.New(nil)
	protonURL, _ := url.Parse("https://proton.me/")
	jar.SetCookies(protonURL, []*http.Cookie{
		{Name: "Session-Id", Value: sentinelCookie, Domain: "proton.me", Path: "/"},
		{Name: "AUTH", Value: sentinelAccess, Domain: "proton.me", Path: "/api/"},
	})

	session := &Session{cookieJar: jar}
	logCookies("test.cookies", session)

	output := buf.String()
	scanForSentinels(t, output, sentinelCookie, sentinelAccess)

	// Cookie names should appear in the output.
	if !strings.Contains(output, "Session-Id") {
		t.Fatal("expected cookie name 'Session-Id' in log output")
	}
}

// TestSecretLogging_DoJSON verifies that DoJSON does not leak tokens
// in log output during request execution.
func TestSecretLogging_DoJSON(t *testing.T) {
	buf, cleanup := captureSlog(t)
	defer cleanup()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"Code": 1000})
	}))
	defer srv.Close()

	jar, _ := cookiejar.New(nil)
	s := &Session{
		Auth: proton.Auth{
			UID:          "test-uid",
			AccessToken:  sentinelAccess,
			RefreshToken: sentinelRefresh,
		},
		cookieJar: jar,
	}

	_ = s.DoJSON(context.Background(), "GET", srv.URL+"/test", nil, nil)

	output := buf.String()
	scanForSentinels(t, output, sentinelAccess, sentinelRefresh)
}
