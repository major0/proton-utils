package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	common "github.com/major0/proton-cli/api"
	"github.com/spf13/cobra"
)

// mockSessionStore implements common.SessionStore for testing.
type mockSessionStore struct {
	loadErr error
}

func (m *mockSessionStore) Load() (*common.SessionCredentials, error) {
	return nil, m.loadErr
}

func (m *mockSessionStore) Save(_ *common.SessionCredentials) error { return nil }
func (m *mockSessionStore) Delete() error                           { return nil }
func (m *mockSessionStore) List() ([]string, error)                 { return nil, nil }
func (m *mockSessionStore) Switch(_ string) error                   { return nil }

func TestRestoreSession(t *testing.T) {
	tests := []struct {
		name    string
		store   common.SessionStore
		wantErr string
	}{
		{
			name:    "not logged in",
			store:   &mockSessionStore{loadErr: common.ErrKeyNotFound},
			wantErr: "not logged in",
		},
		{
			name:    "store load error",
			store:   &mockSessionStore{loadErr: errors.New("disk failure")},
			wantErr: "disk failure",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore the package-level variable.
			origStore := SessionStoreVar
			t.Cleanup(func() { SessionStoreVar = origStore })
			SessionStoreVar = tt.store

			_, err := RestoreSession(context.Background())
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestConfigFilePath(t *testing.T) {
	// Set a known value and verify it's returned.
	orig := rootParams.ConfigFile
	t.Cleanup(func() { rootParams.ConfigFile = orig })

	rootParams.ConfigFile = "/test/config.yaml"
	got := ConfigFilePath()
	if got != "/test/config.yaml" {
		t.Errorf("got %q, want %q", got, "/test/config.yaml")
	}
}

func TestAddCommand(t *testing.T) {
	sub := &cobra.Command{
		Use:   "testsub",
		Short: "test subcommand",
	}
	AddCommand(sub)
	t.Cleanup(func() { rootCmd.RemoveCommand(sub) })

	found := false
	for _, c := range rootCmd.Commands() {
		if c.Use == "testsub" {
			found = true
			break
		}
	}
	if !found {
		t.Error("AddCommand did not register the subcommand")
	}
}

func TestPersistentPreRunE(t *testing.T) {
	// Save originals.
	origParams := rootParams
	origTimeout := Timeout
	origDebugHTTP := DebugHTTP
	origAccount := Account
	origOpts := ProtonOpts
	origStore := SessionStoreVar
	origConfig := ConfigVar
	origCookieStore := CookieStoreVar
	t.Cleanup(func() {
		rootParams = origParams
		Timeout = origTimeout
		DebugHTTP = origDebugHTTP
		Account = origAccount
		ProtonOpts = origOpts
		SessionStoreVar = origStore
		ConfigVar = origConfig
		CookieStoreVar = origCookieStore
	})

	tests := []struct {
		name    string
		verbose int
		account string
	}{
		{"default verbosity", 0, "test-account"},
		{"verbose 1", 1, "default"},
		{"verbose 2", 2, "other"},
		{"verbose 3 enables debug", 3, "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rootParams = rootParamsType{
				Verbose:    tt.verbose,
				Account:    tt.account,
				ConfigFile: "nonexistent.yaml", // triggers config load failure → defaults
				Timeout:    5,
			}

			preRun := rootCmd.PersistentPreRunE
			if err := preRun(rootCmd, nil); err != nil {
				t.Fatalf("PersistentPreRunE: %v", err)
			}

			if Account != tt.account {
				t.Errorf("Account = %q, want %q", Account, tt.account)
			}
			if Timeout != 5 {
				t.Errorf("Timeout = %v, want 5", Timeout)
			}
			if ConfigVar == nil {
				t.Error("ConfigVar is nil after PreRunE")
			}
			if tt.verbose >= 3 && !DebugHTTP {
				t.Error("DebugHTTP should be true for verbose >= 3")
			}
			if tt.verbose < 3 && DebugHTTP {
				t.Error("DebugHTTP should be false for verbose < 3")
			}
		})
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// TestExecute verifies that Execute runs the root command without error.
// The root command's Run handler just prints help, so this exercises the
// Execute → rootCmd.Execute path.
func TestExecute(t *testing.T) {
	// rootCmd.Execute() calls os.Exit on error, but the default Run
	// handler (help) succeeds. We call rootCmd.Execute() directly to
	// avoid the os.Exit wrapper.
	rootCmd.SetArgs([]string{})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute: %v", err)
	}
}

// --- SetService and version resolution tests ---

func TestSetService(t *testing.T) {
	// Save originals.
	origService := ServiceName
	origStore := SessionStoreVar
	origAcctStore := AccountStoreVar
	origOpts := ProtonOpts
	origDebug := DebugHTTP
	origConfig := ConfigVar
	origOverride := AppVersionOverride
	origParams := rootParams
	t.Cleanup(func() {
		ServiceName = origService
		SessionStoreVar = origStore
		AccountStoreVar = origAcctStore
		ProtonOpts = origOpts
		DebugHTTP = origDebug
		ConfigVar = origConfig
		AppVersionOverride = origOverride
		rootParams = origParams
	})

	// Initialize required state.
	rootParams.SessionFile = "/tmp/test-sessions.db"
	Account = "default"
	DebugHTTP = false
	ConfigVar = common.DefaultConfig()
	AppVersionOverride = ""

	tests := []struct {
		name        string
		service     string
		wantService string
	}{
		{"drive", "drive", "drive"},
		{"lumo", "lumo", "lumo"},
		{"account", "account", "account"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetService(tt.service)

			if ServiceName != tt.wantService {
				t.Errorf("ServiceName = %q, want %q", ServiceName, tt.wantService)
			}

			// ProtonOpts should be rebuilt (non-nil, non-empty).
			if len(ProtonOpts) == 0 {
				t.Error("ProtonOpts is empty after SetService")
			}
		})
	}
}

func TestResolveVersion(t *testing.T) {
	origConfig := ConfigVar
	origOverride := AppVersionOverride
	t.Cleanup(func() {
		ConfigVar = origConfig
		AppVersionOverride = origOverride
	})

	tests := []struct {
		name     string
		override string
		config   map[string]string
		service  string
		want     string
	}{
		{
			"flag override takes precedence",
			"1.2.3.4",
			map[string]string{"drive": "9.9.9.9"},
			"drive",
			"1.2.3.4",
		},
		{
			"config override used when no flag",
			"",
			map[string]string{"drive": "2.0.0.0"},
			"drive",
			"2.0.0.0",
		},
		{
			"default version when no overrides",
			"",
			nil,
			"drive",
			common.DefaultVersion,
		},
		{
			"config for different service not used",
			"",
			map[string]string{"lumo": "3.0.0.0"},
			"drive",
			common.DefaultVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			AppVersionOverride = tt.override
			cfg := common.DefaultConfig()
			if tt.config != nil {
				cfg.ServiceVersions = tt.config
			}
			ConfigVar = cfg

			got := resolveVersion(tt.service)
			if got != tt.want {
				t.Errorf("resolveVersion(%q) = %q, want %q", tt.service, got, tt.want)
			}
		})
	}
}

func TestAppVersionFlag(t *testing.T) {
	// Verify the --app-version flag is registered on rootCmd.
	f := rootCmd.PersistentFlags().Lookup("app-version")
	if f == nil {
		t.Fatal("--app-version flag not registered")
	}
	if f.DefValue != "" {
		t.Errorf("--app-version default = %q, want empty", f.DefValue)
	}
}

func TestServiceVersionConfig(t *testing.T) {
	cfg := common.DefaultConfig()
	cfg.ServiceVersions["drive"] = "1.0.0.0"

	got := cfg.ServiceVersion("drive", common.DefaultVersion)
	if got != "1.0.0.0" {
		t.Errorf("ServiceVersion(drive) = %q, want %q", got, "1.0.0.0")
	}

	got = cfg.ServiceVersion("lumo", common.DefaultVersion)
	if got != common.DefaultVersion {
		t.Errorf("ServiceVersion(lumo) = %q, want %q", got, common.DefaultVersion)
	}
}

func TestRestoreSession_ServiceAware(t *testing.T) {
	// When ServiceName is set to a specific service, RestoreSession should
	// attempt RestoreServiceSession. With a failing store, it should return
	// an error (not panic).
	origService := ServiceName
	origStore := SessionStoreVar
	origAcctStore := AccountStoreVar
	origCookieStore := CookieStoreVar
	origOpts := ProtonOpts
	t.Cleanup(func() {
		ServiceName = origService
		SessionStoreVar = origStore
		AccountStoreVar = origAcctStore
		CookieStoreVar = origCookieStore
		ProtonOpts = origOpts
	})

	ServiceName = "drive"
	SessionStoreVar = &mockSessionStore{loadErr: common.ErrKeyNotFound}
	AccountStoreVar = &mockSessionStore{loadErr: common.ErrKeyNotFound}
	CookieStoreVar = &mockSessionStore{loadErr: common.ErrKeyNotFound}

	_, err := RestoreSession(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRestoreSession_WildcardFallback(t *testing.T) {
	// When ServiceName is "*", RestoreSession should use the old path.
	origService := ServiceName
	origStore := SessionStoreVar
	origOpts := ProtonOpts
	t.Cleanup(func() {
		ServiceName = origService
		SessionStoreVar = origStore
		ProtonOpts = origOpts
	})

	ServiceName = "*"
	SessionStoreVar = &mockSessionStore{loadErr: common.ErrKeyNotFound}

	_, err := RestoreSession(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "not logged in") {
		t.Errorf("error = %q, want containing %q", err.Error(), "not logged in")
	}
}
