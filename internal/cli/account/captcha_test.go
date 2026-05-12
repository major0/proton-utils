package accountCmd

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	proton "github.com/ProtonMail/go-proton-api"
	"pgregory.net/rapid"
)

func TestCaptchaURL(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{
			"basic token",
			"abc123",
			"https://drive-api.proton.me/core/v4/captcha?Token=abc123&ForceWebMessaging=1",
		},
		{
			"token with special chars",
			"abc-123_XYZ",
			"https://drive-api.proton.me/core/v4/captcha?Token=abc-123_XYZ&ForceWebMessaging=1",
		},
		{
			"empty token",
			"",
			"https://drive-api.proton.me/core/v4/captcha?Token=&ForceWebMessaging=1",
		},
		{
			"long token",
			strings.Repeat("x", 100),
			"https://drive-api.proton.me/core/v4/captcha?Token=" + strings.Repeat("x", 100) + "&ForceWebMessaging=1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := captchaURL(tt.token)
			if got != tt.want {
				t.Errorf("captchaURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCaptchaURLProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		token := rapid.StringMatching(`[a-zA-Z0-9_\-]{1,64}`).Draw(t, "token")
		url := captchaURL(token)

		// URL must contain the token.
		if !strings.Contains(url, "Token="+token) {
			t.Fatalf("captchaURL(%q) = %q, missing Token= parameter", token, url)
		}

		// URL must have the correct prefix.
		const prefix = "https://drive-api.proton.me/core/v4/captcha?Token="
		if !strings.HasPrefix(url, prefix) {
			t.Fatalf("captchaURL(%q) = %q, missing expected prefix", token, url)
		}

		// URL must end with ForceWebMessaging=1.
		if !strings.HasSuffix(url, "&ForceWebMessaging=1") {
			t.Fatalf("captchaURL(%q) = %q, missing ForceWebMessaging suffix", token, url)
		}
	})
}

func TestHasDisplay(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		// On macOS/Windows, hasDisplay always returns true.
		if !hasDisplay() {
			t.Error("hasDisplay() = false on darwin/windows, want true")
		}
		return
	}

	// On Linux, hasDisplay depends on DISPLAY/WAYLAND_DISPLAY env vars.
	tests := []struct {
		name           string
		display        string
		waylandDisplay string
		want           bool
	}{
		{"both set", ":0", "", true},
		{"wayland set", "", "wayland-0", true},
		{"both set", ":1", "wayland-1", true},
		{"neither set", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DISPLAY", tt.display)
			t.Setenv("WAYLAND_DISPLAY", tt.waylandDisplay)

			got := hasDisplay()
			if got != tt.want {
				t.Errorf("hasDisplay() = %v, want %v (DISPLAY=%q, WAYLAND_DISPLAY=%q)",
					got, tt.want, tt.display, tt.waylandDisplay)
			}
		})
	}
}

func TestSolveCaptcha_NoBrowserNoDisplay(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("cannot force headless on darwin/windows")
	}

	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	if hasDisplay() {
		t.Fatal("hasDisplay() should be false with empty DISPLAY/WAYLAND_DISPLAY")
	}
}

func TestSolveCaptcha_NoBrowserFallsToManual(t *testing.T) {
	// Replace the manual solver with a mock.
	origManual := manualSolver
	t.Cleanup(func() { manualSolver = origManual })

	manualSolver = func(hv *proton.APIHVDetails) (string, error) {
		return "manual-token-" + hv.Token, nil
	}

	hv := &proton.APIHVDetails{Token: "test123"}
	got, err := SolveCaptcha(hv, true) // noBrowser=true → skip Chrome
	if err != nil {
		t.Fatalf("SolveCaptcha(noBrowser=true): %v", err)
	}
	if got != "manual-token-test123" {
		t.Errorf("got %q, want %q", got, "manual-token-test123")
	}
}

func TestSolveCaptcha_ChromeSucceeds(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Setenv("DISPLAY", ":0")
	}

	// Replace the Chrome solver with a mock.
	origChrome := captchaSolver
	origManual := manualSolver
	t.Cleanup(func() {
		captchaSolver = origChrome
		manualSolver = origManual
	})

	captchaSolver = func(hv *proton.APIHVDetails) (string, error) {
		return "chrome-token-" + hv.Token, nil
	}
	manualSolver = func(_ *proton.APIHVDetails) (string, error) {
		t.Fatal("manual solver should not be called when Chrome succeeds")
		return "", nil
	}

	hv := &proton.APIHVDetails{Token: "abc"}
	got, err := SolveCaptcha(hv, false)
	if err != nil {
		t.Fatalf("SolveCaptcha(noBrowser=false): %v", err)
	}
	if got != "chrome-token-abc" {
		t.Errorf("got %q, want %q", got, "chrome-token-abc")
	}
}

func TestSolveCaptcha_ChromeFailsFallsToManual(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Setenv("DISPLAY", ":0")
	}

	origChrome := captchaSolver
	origManual := manualSolver
	t.Cleanup(func() {
		captchaSolver = origChrome
		manualSolver = origManual
	})

	captchaSolver = func(_ *proton.APIHVDetails) (string, error) {
		return "", fmt.Errorf("chrome not found")
	}
	manualSolver = func(hv *proton.APIHVDetails) (string, error) {
		return "fallback-" + hv.Token, nil
	}

	hv := &proton.APIHVDetails{Token: "xyz"}
	got, err := SolveCaptcha(hv, false)
	if err != nil {
		t.Fatalf("SolveCaptcha fallback: %v", err)
	}
	if got != "fallback-xyz" {
		t.Errorf("got %q, want %q", got, "fallback-xyz")
	}
}

func TestSolveCaptcha_ManualError(t *testing.T) {
	origManual := manualSolver
	t.Cleanup(func() { manualSolver = origManual })

	manualSolver = func(_ *proton.APIHVDetails) (string, error) {
		return "", fmt.Errorf("stdin closed")
	}

	hv := &proton.APIHVDetails{Token: "err"}
	_, err := SolveCaptcha(hv, true)
	if err == nil {
		t.Fatal("expected error from manual solver, got nil")
	}
	if !strings.Contains(err.Error(), "stdin closed") {
		t.Errorf("error = %q, want substring %q", err.Error(), "stdin closed")
	}
}

func TestSolveManual(t *testing.T) {
	origPrompt := userPromptFn
	t.Cleanup(func() { userPromptFn = origPrompt })

	tests := []struct {
		name      string
		token     string
		response  string
		wantToken string
		wantErr   string
	}{
		{
			"successful solve",
			"hv-token-123",
			"  solved-abc  ",
			"solved-abc",
			"",
		},
		{
			"prompt error",
			"hv-token-456",
			"",
			"",
			"terminal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userPromptFn = func(_ string, _ bool) (string, error) {
				if tt.wantErr != "" {
					return "", fmt.Errorf("terminal error")
				}
				return tt.response, nil
			}

			hv := &proton.APIHVDetails{Token: tt.token}
			got, err := solveManual(hv)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantToken {
				t.Errorf("got %q, want %q", got, tt.wantToken)
			}
		})
	}
}

func TestOpenBrowserCmd(t *testing.T) {
	got := openBrowserCmd()
	switch runtime.GOOS {
	case "darwin":
		if got != "open" {
			t.Errorf("openBrowserCmd() = %q, want %q", got, "open")
		}
	default:
		if got != "xdg-open" {
			t.Errorf("openBrowserCmd() = %q, want %q", got, "xdg-open")
		}
	}
}
