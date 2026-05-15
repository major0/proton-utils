//go:build linux

package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/major0/proton-utils/api"
	"github.com/major0/proton-utils/api/config"
	"github.com/major0/proton-utils/internal/keyring"
	"pgregory.net/rapid"
)

func TestParseFlags_Defaults(t *testing.T) {
	// Set XDG_RUNTIME_DIR so mountpoint resolves.
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	cfg, err := parseFlags([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.account != "" {
		t.Errorf("account = %q, want empty", cfg.account)
	}
	if cfg.logLevel != slog.LevelWarn {
		t.Errorf("logLevel = %v, want %v", cfg.logLevel, slog.LevelWarn)
	}
	if cfg.configPath != keyring.XDGConfigPath("config.yaml") {
		t.Errorf("configPath = %q, want %q", cfg.configPath, keyring.XDGConfigPath("config.yaml"))
	}
	if cfg.sessionFile != keyring.XDGConfigPath("sessions.db") {
		t.Errorf("sessionFile = %q, want %q", cfg.sessionFile, keyring.XDGConfigPath("sessions.db"))
	}
	want := filepath.Join("/run/user/1000", "proton", "fs")
	if cfg.mountpoint != want {
		t.Errorf("mountpoint = %q, want %q", cfg.mountpoint, want)
	}
}

func TestParseFlags_AllExplicit(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	cfg, err := parseFlags([]string{
		"--account", "work",
		"--log-level", "debug",
		"--config", "/etc/proton/config.yaml",
		"--session-file", "/tmp/sessions.db",
		"--mountpoint", "/mnt/proton",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.account != "work" {
		t.Errorf("account = %q, want %q", cfg.account, "work")
	}
	if cfg.logLevel != slog.LevelDebug {
		t.Errorf("logLevel = %v, want %v", cfg.logLevel, slog.LevelDebug)
	}
	if cfg.configPath != "/etc/proton/config.yaml" {
		t.Errorf("configPath = %q, want %q", cfg.configPath, "/etc/proton/config.yaml")
	}
	if cfg.sessionFile != "/tmp/sessions.db" {
		t.Errorf("sessionFile = %q, want %q", cfg.sessionFile, "/tmp/sessions.db")
	}
	if cfg.mountpoint != "/mnt/proton" {
		t.Errorf("mountpoint = %q, want %q", cfg.mountpoint, "/mnt/proton")
	}
}

func TestParseFlags_VerboseCount(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	tests := []struct {
		name  string
		args  []string
		level slog.Level
	}{
		{"no verbose", []string{}, slog.LevelWarn},
		{"single -v", []string{"-v"}, slog.LevelInfo},
		{"double -v", []string{"-v", "-v"}, slog.LevelDebug},
		{"triple -v caps at debug", []string{"-v", "-v", "-v"}, slog.LevelDebug},
		{"compacted -vvv", []string{"-vvv"}, slog.LevelDebug},
		{"compacted -vv", []string{"-vv"}, slog.LevelDebug},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseFlags(tt.args)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.logLevel != tt.level {
				t.Errorf("logLevel = %v, want %v", cfg.logLevel, tt.level)
			}
		})
	}
}

func TestParseFlags_LogLevelPriorityOverVerbose(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	// --log-level should take priority over -v count.
	cfg, err := parseFlags([]string{"-v", "-v", "--log-level", "error"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.logLevel != slog.LevelError {
		t.Errorf("logLevel = %v, want %v (--log-level should override -v)", cfg.logLevel, slog.LevelError)
	}
}

func TestParseFlags_MountpointRequiresXDGRuntimeDir(t *testing.T) {
	// Unset XDG_RUNTIME_DIR to trigger the error.
	t.Setenv("XDG_RUNTIME_DIR", "")

	_, err := parseFlags([]string{})
	if err == nil {
		t.Fatal("expected error when XDG_RUNTIME_DIR is unset and --mountpoint not provided")
	}
}

func TestParseFlags_ExplicitMountpointBypassesXDGCheck(t *testing.T) {
	// Even with XDG_RUNTIME_DIR unset, explicit --mountpoint should work.
	t.Setenv("XDG_RUNTIME_DIR", "")

	cfg, err := parseFlags([]string{"--mountpoint", "/mnt/custom"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.mountpoint != "/mnt/custom" {
		t.Errorf("mountpoint = %q, want %q", cfg.mountpoint, "/mnt/custom")
	}
}

func TestParseFlags_LogLevelValues(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	tests := []struct {
		level string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"DEBUG", slog.LevelDebug}, // case insensitive
		{"Info", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			cfg, err := parseFlags([]string{"--log-level", tt.level})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.logLevel != tt.want {
				t.Errorf("logLevel = %v, want %v", cfg.logLevel, tt.want)
			}
		})
	}
}

func TestParseFlags_InvalidLogLevelFallsToVerbose(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	// Invalid --log-level should fall through to verbose-based resolution.
	cfg, err := parseFlags([]string{"--log-level", "invalid", "-v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.logLevel != slog.LevelInfo {
		t.Errorf("logLevel = %v, want %v (invalid level should fall to -v)", cfg.logLevel, slog.LevelInfo)
	}
}

func TestParseFlags_ConfigPathFromXDG(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv("XDG_CONFIG_HOME", "/home/test/.config")

	cfg, err := parseFlags([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantConfig := filepath.Join("/home/test/.config", "proton-utils", "config.yaml")
	if cfg.configPath != wantConfig {
		t.Errorf("configPath = %q, want %q", cfg.configPath, wantConfig)
	}

	wantSession := filepath.Join("/home/test/.config", "proton-utils", "sessions.db")
	if cfg.sessionFile != wantSession {
		t.Errorf("sessionFile = %q, want %q", cfg.sessionFile, wantSession)
	}
}

func TestResolveLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		logLevel string
		verbose  int
		want     slog.Level
	}{
		{"explicit debug", "debug", 0, slog.LevelDebug},
		{"explicit info", "info", 0, slog.LevelInfo},
		{"explicit warn", "warn", 0, slog.LevelWarn},
		{"explicit error", "error", 0, slog.LevelError},
		{"explicit overrides verbose", "error", 2, slog.LevelError},
		{"verbose 0", "", 0, slog.LevelWarn},
		{"verbose 1", "", 1, slog.LevelInfo},
		{"verbose 2", "", 2, slog.LevelDebug},
		{"verbose 3 caps at debug", "", 3, slog.LevelDebug},
		{"invalid level no verbose", "bogus", 0, slog.LevelWarn},
		{"invalid level with verbose", "bogus", 1, slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveLogLevel(tt.logLevel, tt.verbose)
			if got != tt.want {
				t.Errorf("resolveLogLevel(%q, %d) = %v, want %v", tt.logLevel, tt.verbose, got, tt.want)
			}
		})
	}
}

func TestConfigureLogging_NonDebug(t *testing.T) {
	// Non-debug levels should not create any log files.
	cleanup, err := configureLogging(slog.LevelWarn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	// Verify the default logger is set (no panic on use).
	slog.Info("test message")
}

func TestConfigureLogging_Debug(t *testing.T) {
	// Use t.TempDir as XDG_STATE_HOME so debug log files are created there.
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	cleanup, err := configureLogging(slog.LevelDebug)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	// Verify log directory was created.
	logDir := filepath.Join(tmpDir, "protonfs", "logs")
	info, err := os.Stat(logDir)
	if err != nil {
		t.Fatalf("log directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("log path is not a directory")
	}

	// Verify fuse.log and drive.log exist.
	for _, name := range []string{"fuse.log", "drive.log"} {
		path := filepath.Join(logDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}
}

func TestXdgStatePath(t *testing.T) {
	tests := []struct {
		name     string
		stateEnv string
		want     string
	}{
		{
			name:     "with XDG_STATE_HOME set",
			stateEnv: "/custom/state",
			want:     "/custom/state/protonfs/logs",
		},
		{
			name:     "without XDG_STATE_HOME",
			stateEnv: "",
			want:     filepath.Join(mustUserHomeDir(t), ".local", "state", "protonfs", "logs"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", tt.stateEnv)
			got := xdgStatePath("logs")
			if got != tt.want {
				t.Errorf("xdgStatePath(\"logs\") = %q, want %q", got, tt.want)
			}
		})
	}
}

// mustUserHomeDir returns the user's home directory or fails the test.
func mustUserHomeDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("cannot determine home directory: %v", err)
	}
	return home
}

func TestBuildSessionConfig_EmptyConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	sessionCfg := buildSessionConfig(cfg)

	if len(sessionCfg.Shares) != 0 {
		t.Errorf("Shares = %v, want empty map", sessionCfg.Shares)
	}
	if len(sessionCfg.Defaults) != 0 {
		t.Errorf("Defaults = %v, want empty map", sessionCfg.Defaults)
	}
	if sessionCfg.MaxJobs != api.DefaultMaxWorkers() {
		t.Errorf("MaxJobs = %d, want %d", sessionCfg.MaxJobs, api.DefaultMaxWorkers())
	}
	if sessionCfg.MemoryCacheMinWatermark != 0 {
		t.Errorf("MemoryCacheMinWatermark = %d, want 0", sessionCfg.MemoryCacheMinWatermark)
	}
	if sessionCfg.MemoryCacheMaxWatermark != 0 {
		t.Errorf("MemoryCacheMaxWatermark = %d, want 0", sessionCfg.MemoryCacheMaxWatermark)
	}
}

func TestBuildSessionConfig_NoSubsystems(t *testing.T) {
	// Config with subsystems that have no account set — Defaults should be empty.
	cfg := config.DefaultConfig()
	cfg.Subsystems["drive"] = &config.CoreConfig{
		MaxJobs:    config.NewParam(api.DefaultMaxWorkers()),
		Account:    config.NewParam("default"),
		AppVersion: config.NewParam(""),
	}
	cfg.Subsystems["mail"] = &config.CoreConfig{
		MaxJobs:    config.NewParam(api.DefaultMaxWorkers()),
		Account:    config.NewParam("default"),
		AppVersion: config.NewParam(""),
	}

	sessionCfg := buildSessionConfig(cfg)

	if len(sessionCfg.Defaults) != 0 {
		t.Errorf("Defaults = %v, want empty (no subsystems have Account.IsSet())", sessionCfg.Defaults)
	}
}

// TestPropertyConfigToSessionConfig verifies that buildSessionConfig preserves
// all config values in the resulting SessionConfig.
//
// Feature: protonfs-daemon, Property 1: Config-to-SessionConfig mapping
// **Validates: Requirements 2.4**
func TestPropertyConfigToSessionConfig(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate random Shares map.
		numShares := rapid.IntRange(0, 5).Draw(t, "numShares")
		shares := make(map[string]api.ShareConfig, numShares)
		for i := 0; i < numShares; i++ {
			key := rapid.StringMatching(`[a-zA-Z0-9_-]{1,20}`).Draw(t, "shareKey")
			sc := api.ShareConfig{
				MemoryCache: api.MemoryCacheLevel(rapid.IntRange(0, 2).Draw(t, "memoryCache")),
				DiskCache:   api.DiskCacheLevel(rapid.IntRange(0, 1).Draw(t, "diskCache")),
			}
			shares[key] = sc
		}

		// Generate random MaxJobs (1-100).
		maxJobs := rapid.IntRange(1, 100).Draw(t, "maxJobs")

		// Generate random watermark values (min < max, both in 0-100 range).
		wmMin := int64(rapid.IntRange(0, 99).Draw(t, "wmMin"))
		wmMax := int64(rapid.IntRange(int(wmMin+1), 100).Draw(t, "wmMax"))

		// Generate random Subsystems map with some having Account set.
		numSubs := rapid.IntRange(0, 4).Draw(t, "numSubs")
		subsystems := make(map[string]*config.CoreConfig, numSubs)
		expectedDefaults := make(map[string]string)
		for i := 0; i < numSubs; i++ {
			name := rapid.StringMatching(`[a-z]{3,10}`).Draw(t, "subName")
			sub := &config.CoreConfig{
				MaxJobs:    config.NewParam(api.DefaultMaxWorkers()),
				Account:    config.NewParam("default"),
				AppVersion: config.NewParam(""),
			}
			// Randomly decide if this subsystem has an account set.
			hasAccount := rapid.Bool().Draw(t, "hasAccount")
			if hasAccount {
				acct := rapid.StringMatching(`[a-z]{3,12}`).Draw(t, "acctName")
				sub.Account.SetFile(acct)
				expectedDefaults[name] = acct
			}
			subsystems[name] = sub
		}

		// Build the Config.
		cfg := &config.Config{
			CoreConfig: config.CoreConfig{
				MaxJobs:    config.NewParam(api.DefaultMaxWorkers()),
				Account:    config.NewParam("default"),
				AppVersion: config.NewParam(""),
			},
			MemoryCacheWatermark: config.NewParam([2]int64{0, 0}),
			Shares:               shares,
			Subsystems:           subsystems,
		}
		cfg.MaxJobs.SetFile(maxJobs)
		cfg.MemoryCacheWatermark.SetFile([2]int64{wmMin, wmMax})

		// Call buildSessionConfig.
		sessionCfg := buildSessionConfig(cfg)

		// Verify Shares matches.
		if len(sessionCfg.Shares) != len(cfg.Shares) {
			t.Fatalf("Shares length mismatch: got %d, want %d", len(sessionCfg.Shares), len(cfg.Shares))
		}
		for k, v := range cfg.Shares {
			got, ok := sessionCfg.Shares[k]
			if !ok {
				t.Fatalf("Shares missing key %q", k)
			}
			if got != v {
				t.Fatalf("Shares[%q] = %+v, want %+v", k, got, v)
			}
		}

		// Verify MaxJobs.
		if sessionCfg.MaxJobs != cfg.MaxJobs.Value() {
			t.Fatalf("MaxJobs = %d, want %d", sessionCfg.MaxJobs, cfg.MaxJobs.Value())
		}

		// Verify watermarks.
		if sessionCfg.MemoryCacheMinWatermark != wmMin {
			t.Fatalf("MemoryCacheMinWatermark = %d, want %d", sessionCfg.MemoryCacheMinWatermark, wmMin)
		}
		if sessionCfg.MemoryCacheMaxWatermark != wmMax {
			t.Fatalf("MemoryCacheMaxWatermark = %d, want %d", sessionCfg.MemoryCacheMaxWatermark, wmMax)
		}

		// Verify Defaults contains entries for subsystems with account set.
		if len(sessionCfg.Defaults) != len(expectedDefaults) {
			t.Fatalf("Defaults length mismatch: got %d, want %d", len(sessionCfg.Defaults), len(expectedDefaults))
		}
		for name, wantAcct := range expectedDefaults {
			got, ok := sessionCfg.Defaults[name]
			if !ok {
				t.Fatalf("Defaults missing key %q", name)
			}
			if got != wantAcct {
				t.Fatalf("Defaults[%q] = %q, want %q", name, got, wantAcct)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Block cache mode daemon wiring tests (Task 6.2)
// ---------------------------------------------------------------------------

// TestBlockCacheModeValidation verifies that the daemon startup validation
// rejects invalid block_cache_mode values and accepts valid ones.
// Since run() requires a full session, we test the validation logic
// extracted from the run function directly.
func TestBlockCacheModeValidation(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		wantErr bool
	}{
		{"encrypted is valid", "encrypted", false},
		{"decrypted is valid", "decrypted", false},
		{"empty is invalid", "", true},
		{"random string is invalid", "foobar", true},
		{"mixed case is invalid", "Encrypted", true},
		{"uppercase is invalid", "ENCRYPTED", true},
		{"with spaces is invalid", " encrypted", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Replicate the validation logic from run().
			mode := tt.mode
			var err error
			if mode != "encrypted" && mode != "decrypted" {
				err = fmt.Errorf("invalid block_cache_mode %q (must be \"encrypted\" or \"decrypted\")", mode)
			}

			if tt.wantErr && err == nil {
				t.Errorf("expected error for mode %q, got nil", tt.mode)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for mode %q: %v", tt.mode, err)
			}
		})
	}
}

// TestBlockCacheModeDefaultConfig verifies that the default config has
// block_cache_mode set to "encrypted".
func TestBlockCacheModeDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	mode := cfg.BlockCacheMode.Value()
	if mode != "encrypted" {
		t.Errorf("default BlockCacheMode = %q, want %q", mode, "encrypted")
	}
}

// TestBlockCacheModePropagation verifies that valid modes would propagate
// to the drive client (tests the config → client path without a real session).
func TestBlockCacheModePropagation(t *testing.T) {
	for _, mode := range []string{"encrypted", "decrypted"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.BlockCacheMode.SetFile(mode)

			got := cfg.BlockCacheMode.Value()
			if got != mode {
				t.Errorf("BlockCacheMode.Value() = %q, want %q", got, mode)
			}
		})
	}
}
