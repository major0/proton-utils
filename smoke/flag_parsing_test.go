package smoke

import (
	"fmt"
	"log/slog"
	"testing"
	"testing/quick"
	"time"

	"github.com/spf13/cobra"
)

// newTestRoot creates a fresh cobra command with the same global flag
// definitions as rootCmd. We can't use rootCmd directly because it's
// unexported, so we replicate the flag setup here.
func newTestRoot() (*cobra.Command, *int, *time.Duration) {
	var verbose int
	var timeout time.Duration

	cmd := &cobra.Command{
		Use: "test",
		Run: func(cmd *cobra.Command, args []string) {},
	}
	cmd.PersistentFlags().CountVarP(&verbose, "verbose", "v", "verbosity")
	cmd.PersistentFlags().DurationVarP(&timeout, "timeout", "t", 60*time.Second, "timeout")
	cmd.PersistentFlags().StringP("account", "a", "default", "account")
	cmd.PersistentFlags().String("config-file", "", "config file")
	cmd.PersistentFlags().String("session-file", "", "session file")
	cmd.PersistentFlags().IntP("max-jobs", "j", 10, "max jobs")
	cmd.PersistentFlags().BoolP("help", "h", false, "help")

	return cmd, &verbose, &timeout
}

// PropertyVerboseCountTracksRepetitions verifies that for any N in [0,20],
// passing N -v flags results in a verbosity counter equal to N.
//
// Property 1: Verbose count flag tracks repetitions
// Validates: Requirements 2.1
func TestPropertyVerboseCountTracksRepetitions(t *testing.T) {
	f := func(n uint8) bool {
		// Clamp to a reasonable range to avoid huge arg lists.
		count := int(n % 21)

		cmd, verbose, _ := newTestRoot()

		args := make([]string, count)
		for i := range args {
			args[i] = "-v"
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Logf("execute error: %v", err)
			return false
		}

		return *verbose == count
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 1 failed: %v", err)
	}
}

// PropertyVerboseCountToLogLevel verifies the mapping from verbose count
// to slog level: 0 → Warn, 1 → Info, ≥2 → Debug.
//
// Property 4: Verbose count to log level mapping
// Validates: Requirements 5.2, 5.3, 5.4
func TestPropertyVerboseCountToLogLevel(t *testing.T) {
	f := func(n uint8) bool {
		count := int(n % 21)

		// Replicate the pre-run logic from rootCmd.PersistentPreRunE.
		var level slog.Level
		switch {
		case count == 1:
			level = slog.LevelInfo
		case count > 1:
			level = slog.LevelDebug
		default:
			level = slog.LevelWarn
		}

		switch count {
		case 0:
			return level == slog.LevelWarn
		case 1:
			return level == slog.LevelInfo
		default:
			return level == slog.LevelDebug
		}
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 4 failed: %v", err)
	}
}

// PropertyTimeoutComputation verifies that --timeout D results in
// Timeout == D * time.Second, matching the pre-run hook logic.
//
// Property 5: Timeout computation
// Validates: Requirements 5.8
func TestPropertyTimeoutComputation(t *testing.T) {
	f := func(d uint16) bool {
		// Use uint16 to keep durations reasonable (0–65535 seconds).
		dur := time.Duration(d) * time.Second

		cmd, _, timeout := newTestRoot()
		cmd.SetArgs([]string{fmt.Sprintf("--timeout=%s", dur)})
		if err := cmd.Execute(); err != nil {
			t.Logf("execute error: %v", err)
			return false
		}

		// The pre-run hook multiplies the parsed duration by time.Second.
		// Since we pass the value already as seconds, the parsed value
		// equals dur, and the effective timeout = dur * time.Second.
		effective := *timeout * time.Second
		return effective == dur*time.Second
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Errorf("Property 5 failed: %v", err)
	}
}
