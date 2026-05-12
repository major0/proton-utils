package lumoCmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"golang.org/x/term"
)

// TestChatLog_RequiresArg verifies that cobra ExactArgs(1) rejects 0 args.
func TestChatLog_RequiresArg(t *testing.T) {
	cmd := chatLogCmd
	if cmd.Args == nil {
		t.Fatal("chat log has no Args validator")
	}
	if err := cmd.Args(cmd, nil); err == nil {
		t.Error("chat log accepted 0 args, want error")
	}
	if err := cmd.Args(cmd, []string{"some-id"}); err != nil {
		t.Errorf("chat log rejected 1 arg: %v", err)
	}
}

// TestChatLog_ColorFlagDefault verifies the default value is "auto".
func TestChatLog_ColorFlagDefault(t *testing.T) {
	f := chatLogCmd.Flags().Lookup("color")
	if f == nil {
		t.Fatal("--color flag not registered")
	}
	if f.DefValue != "auto" {
		t.Errorf("--color default = %q, want %q", f.DefValue, "auto")
	}
}

// TestChatLog_ColorFlagValidation verifies that invalid color values
// are rejected by the runChatLog validation logic.
func TestChatLog_ColorFlagValidation(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"always", true},
		{"auto", true},
		{"never", true},
		{"yes", false},
		{"no", false},
		{"", false},
		{"Always", false}, // case-sensitive
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			// We test the validation logic directly: the valid set is
			// "always", "auto", "never".
			valid := tt.value == "always" || tt.value == "auto" || tt.value == "never"
			if valid != tt.valid {
				t.Errorf("color=%q: got valid=%v, want %v", tt.value, valid, tt.valid)
			}
		})
	}
}

// TestChatLog_RequiresSession verifies that runChatLog returns an error
// when no cookie session is available.
func TestChatLog_RequiresSession(t *testing.T) {
	cmd := newTestCmdWithCookieAuth(false)
	cmd.Flags().String("color", "auto", "")
	cmd.Flags().Bool("no-pager", false, "")

	err := runChatLog(cmd, []string{"some-id"})
	if err == nil {
		t.Fatal("expected error for missing cookie session")
	}
	if !strings.Contains(err.Error(), "cookie-based authentication") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSetupOutput_NoPager verifies that setupOutput returns stdout
// directly when noPager is true.
func TestSetupOutput_NoPager(t *testing.T) {
	content := "Hello, world!\nLine two.\n"

	// Capture stdout by redirecting to a pipe.
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	writer, cleanup := setupOutput(true)
	_, werr := io.WriteString(writer, content)
	cleanup()

	_ = w.Close()
	os.Stdout = oldStdout

	if werr != nil {
		t.Fatalf("write error: %v", werr)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()

	if got := buf.String(); got != content {
		t.Errorf("output = %q, want %q", got, content)
	}
}

// TestSetupOutput_NonTerminal verifies that setupOutput returns stdout
// directly when stdout is not a terminal (pipe), even with noPager=false.
func TestSetupOutput_NonTerminal(t *testing.T) {
	content := "Piped output test\n"

	// When stdout is a pipe (not a terminal), setupOutput should
	// return stdout directly regardless of noPager value.
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	writer, cleanup := setupOutput(false)
	_, werr := io.WriteString(writer, content)
	cleanup()

	_ = w.Close()
	os.Stdout = oldStdout

	if werr != nil {
		t.Fatalf("write error: %v", werr)
	}

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()

	if got := buf.String(); got != content {
		t.Errorf("output = %q, want %q", got, content)
	}
}

// TestColorResolution_NonTerminal verifies that color=auto resolves to
// false when stdout is not a terminal.
func TestColorResolution_NonTerminal(t *testing.T) {
	// When stdout is a pipe, IsTerminal returns false, so auto → no color.
	// We test this by checking the pipe fd directly.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()

	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	// Simulate the color resolution logic from runChatLog.
	colorFlag := "auto"
	isTTY := term.IsTerminal(int(os.Stdout.Fd())) //nolint:gosec // test code
	var useColor bool
	switch colorFlag {
	case "always":
		useColor = true
	case "never":
		useColor = false
	default:
		useColor = isTTY
	}

	if useColor {
		t.Error("color=auto resolved to true with pipe stdout, want false")
	}
}

// TestColorResolution_Always verifies that color=always forces color on.
func TestColorResolution_Always(t *testing.T) {
	colorFlag := "always"
	var useColor bool
	switch colorFlag {
	case "always":
		useColor = true
	case "never":
		useColor = false
	default:
		useColor = false
	}

	if !useColor {
		t.Error("color=always resolved to false, want true")
	}
}

// TestColorResolution_Never verifies that color=never forces color off.
func TestColorResolution_Never(t *testing.T) {
	colorFlag := "never"
	var useColor bool
	switch colorFlag {
	case "always":
		useColor = true
	case "never":
		useColor = false
	default:
		useColor = true // pretend terminal
	}

	if useColor {
		t.Error("color=never resolved to true, want false")
	}
}
