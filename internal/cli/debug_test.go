package cli

import (
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
)

func TestRedactHeader(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{"non-sensitive header", "Content-Type", "application/json", "application/json"},
		{"authorization", "Authorization", "Bearer token123", "<redacted>"},
		{"authorization lowercase", "authorization", "Bearer token123", "<redacted>"},
		{"x-pm-uid", "X-Pm-Uid", "abc123", "<redacted>"},
		{"set-cookie", "Set-Cookie", "session=xyz", "<redacted>"},
		{"cookie", "Cookie", "session=xyz", "<redacted>"},
		{"hv token", "X-Pm-Human-Verification-Token", "captcha123", "<redacted>"},
		{"safe header", "X-Custom", "safe-value", "safe-value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactHeader(tt.key, tt.value)
			if got != tt.want {
				t.Errorf("redactHeader(%q, %q) = %q, want %q", tt.key, tt.value, got, tt.want)
			}
		})
	}
}

func TestRedactBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string // exact expected output
		deny string // substring that must NOT appear (empty = skip)
	}{
		{
			name: "empty body",
			body: "",
			want: "",
		},
		{
			name: "no sensitive fields",
			body: `{"Code":1000,"Status":"ok"}`,
			want: `{"Code":1000,"Status":"ok"}`,
		},
		{
			name: "redacts AccessToken",
			body: `{"AccessToken":"secret123","Status":1}`,
			want: `{"AccessToken":"<redacted>","Status":1}`,
			deny: "secret123",
		},
		{
			name: "redacts RefreshToken",
			body: `{"RefreshToken":"refresh_secret"}`,
			want: `{"RefreshToken":"<redacted>"}`,
			deny: "refresh_secret",
		},
		{
			name: "redacts UID",
			body: `{"UID":"uid_value","Code":1000}`,
			want: `{"UID":"<redacted>","Code":1000}`,
			deny: "uid_value",
		},
		{
			name: "redacts Password",
			body: `{"Password":"hunter2"}`,
			want: `{"Password":"<redacted>"}`,
			deny: "hunter2",
		},
		{
			name: "redacts SaltedKeyPass",
			body: `{"SaltedKeyPass":"salted_secret"}`,
			want: `{"SaltedKeyPass":"<redacted>"}`,
			deny: "salted_secret",
		},
		{
			name: "multiple sensitive fields",
			body: `{"AccessToken":"at","RefreshToken":"rt","Code":1}`,
			want: `{"AccessToken":"<redacted>","RefreshToken":"<redacted>","Code":1}`,
			deny: `"at"`,
		},
		{
			name: "plain text no match",
			body: "hello world",
			want: "hello world",
		},
		{
			name: "unterminated value",
			body: `{"AccessToken":"no_closing_quote`,
			want: `{"AccessToken":"no_closing_quote`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactBody(tt.body)
			if got != tt.want {
				t.Errorf("redactBody() = %q, want %q", got, tt.want)
			}
			if tt.deny != "" && strings.Contains(got, tt.deny) {
				t.Errorf("redactBody() = %q, must not contain %q", got, tt.deny)
			}
		})
	}
}

// TestInstallDebugHooks verifies that InstallDebugHooks does not panic
// when adding hooks to a Manager. We cannot exercise the hooks without
// a real HTTP round-trip, but we verify the function completes.
func TestInstallDebugHooks(_ *testing.T) {
	m := proton.New(proton.WithHostURL("https://localhost:0"))
	defer m.Close()

	// Should not panic.
	InstallDebugHooks(m)
}
