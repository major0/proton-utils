package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
)

// Sentinel constants for secret logging tests. These are injected as
// token/cookie values and must never appear in any log output.
const (
	sentinelAccess  = "SECRET_ACCESS_TOKEN_a1b2c3d4e5f6"
	sentinelRefresh = "SECRET_REFRESH_TOKEN_f6e5d4c3b2a1"
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
