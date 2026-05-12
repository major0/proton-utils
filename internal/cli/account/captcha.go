package accountCmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/chromedp/chromedp"
)

// captchaURL builds the CAPTCHA page URL.
func captchaURL(token string) string {
	return fmt.Sprintf("https://drive-api.proton.me/core/v4/captcha?Token=%s&ForceWebMessaging=1", token)
}

// hasDisplay reports whether a graphical display is available.
func hasDisplay() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	default:
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	}
}

// openBrowserCmd returns the command name used to open URLs on the current platform.
func openBrowserCmd() string {
	switch runtime.GOOS {
	case "darwin":
		return "open"
	default:
		return "xdg-open"
	}
}

// openBrowser opens the given URL in the user's default browser.
func openBrowser(rawURL string) error {
	return exec.Command(openBrowserCmd(), rawURL).Start() //nolint:gosec
}

// captchaSolver is the function used to solve CAPTCHAs via Chrome.
// It is a variable so tests can replace it without launching a browser.
var captchaSolver = solveChromeDP

// manualSolver is the function used for manual CAPTCHA token entry.
// It is a variable so tests can replace it without reading stdin.
var manualSolver = solveManual

// SolveCaptcha presents the CAPTCHA to the user and returns the solved
// composite token. Uses chromedp (headed Chrome) when a display is
// available, falls back to manual token entry for headless/SSH sessions
// or when --no-browser is set.
func SolveCaptcha(hv *proton.APIHVDetails, noBrowser bool) (string, error) {
	if !noBrowser && hasDisplay() {
		token, err := captchaSolver(hv)
		if err == nil {
			return token, nil
		}
		fmt.Fprintf(os.Stderr, "Chrome CAPTCHA failed: %v\nFalling back to manual mode.\n", err)
	}
	return manualSolver(hv)
}

// solveChromeDP launches a headed Chrome window with the CAPTCHA page,
// injects a postMessage listener, and waits for the user to solve it.
// The solved token is captured via a JS variable polled from Go.
func solveChromeDP(hv *proton.APIHVDetails) (string, error) {
	url := captchaURL(hv.Token)

	fmt.Println("\nHuman verification required. Opening CAPTCHA in Chrome...")

	// Create a headed (visible) Chrome context.
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", false),
		chromedp.WindowSize(500, 700),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// 5 minute timeout for the user to solve the CAPTCHA.
	ctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Navigate and inject the postMessage listener.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.Evaluate(`
			window.__captchaToken = "";
			window.addEventListener("message", function(e) {
				if (e.data && e.data.type === "pm_captcha" && e.data.token) {
					window.__captchaToken = e.data.token;
				}
			});
		`, nil),
	); err != nil {
		return "", fmt.Errorf("navigating to CAPTCHA: %w", err)
	}

	// Poll for the token.
	var token string
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("CAPTCHA timeout: %w", ctx.Err())
		case <-ticker.C:
		}

		if err := chromedp.Run(ctx,
			chromedp.Evaluate(`window.__captchaToken`, &token),
		); err != nil {
			return "", fmt.Errorf("polling token: %w", err)
		}

		if token != "" {
			fmt.Println("CAPTCHA solved.")
			return token, nil
		}
	}
}

// solveManual falls back to manual token entry for headless sessions
// or when Chrome is unavailable.
func solveManual(hv *proton.APIHVDetails) (string, error) {
	url := captchaURL(hv.Token)

	fmt.Println("\nHuman verification required (manual mode).")
	fmt.Println("")
	fmt.Println("Steps:")
	fmt.Println("  1. Open your browser's Developer Tools (F12)")
	fmt.Println("  2. Go to the Console tab and paste this snippet:")
	fmt.Println("")
	fmt.Println(`     window.addEventListener("message", e => { if(e.data?.type==="pm_captcha") prompt("Copy this token:",e.data.token) })`)
	fmt.Println("")
	fmt.Println("  3. Navigate to this URL:")
	fmt.Printf("     %s\n\n", url)
	fmt.Println("  4. Solve the CAPTCHA — a prompt will show the token")
	fmt.Println("  5. Copy and paste it below")
	fmt.Println("")

	token, err := userPromptFn("Solved token", false)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(token), nil
}
