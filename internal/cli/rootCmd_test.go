package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	common "github.com/major0/proton-utils/api"
	"github.com/major0/proton-utils/api/config"
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
	origParams := rootParams
	t.Cleanup(func() { rootParams = origParams })

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

			// Verify RuntimeContext was set correctly.
			rc := GetContext(rootCmd)
			if rc == nil {
				t.Fatal("RuntimeContext is nil after PreRunE")
			}
			if rc.Account != tt.account {
				t.Errorf("rc.Account = %q, want %q", rc.Account, tt.account)
			}
			if rc.Timeout != 5 {
				t.Errorf("rc.Timeout = %v, want 5", rc.Timeout)
			}
			if rc.Config == nil {
				t.Error("rc.Config is nil after PreRunE")
			}
			if tt.verbose >= 3 && !rc.DebugHTTP {
				t.Error("rc.DebugHTTP should be true for verbose >= 3")
			}
			if tt.verbose < 3 && rc.DebugHTTP {
				t.Error("rc.DebugHTTP should be false for verbose < 3")
			}
		})
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// TestExecute verifies that Execute runs the root command without error.
func TestExecute(t *testing.T) {
	rootCmd.SetArgs([]string{})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("rootCmd.Execute: %v", err)
	}
}

// --- SetServiceCmd and version resolution tests ---

func TestSetServiceCmd(t *testing.T) {
	origParams := rootParams
	t.Cleanup(func() { rootParams = origParams })

	rootParams.SessionFile = "/tmp/test-sessions.db"

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
			cmd := &cobra.Command{}
			rc := &RuntimeContext{
				Account:     "default",
				SessionFile: rootParams.SessionFile,
			}
			SetContext(cmd, rc)

			SetServiceCmd(cmd, tt.service)

			if rc.ServiceName != tt.wantService {
				t.Errorf("rc.ServiceName = %q, want %q", rc.ServiceName, tt.wantService)
			}

			// ProtonOpts should be rebuilt (non-nil, non-empty).
			if len(rc.ProtonOpts) == 0 {
				t.Error("rc.ProtonOpts is empty after SetServiceCmd")
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
	cfg := config.DefaultConfig()
	cfg.Subsystems["drive"] = &config.CoreConfig{
		MaxJobs:    config.NewParam(10),
		Account:    config.NewParam("default"),
		AppVersion: config.NewParam(""),
	}
	cfg.Subsystems["drive"].AppVersion.SetFile("1.0.0.0")

	rc := &RuntimeContext{
		Config: cfg,
	}
	got := resolveVersionRC(rc, "drive")
	if got != "1.0.0.0" {
		t.Errorf("resolveVersionRC(drive) = %q, want %q", got, "1.0.0.0")
	}

	got = resolveVersionRC(rc, "lumo")
	if got != "" {
		t.Errorf("resolveVersionRC(lumo) = %q, want %q", got, "")
	}
}

// --- SetupSession tests ---

func TestSetupSession_WildcardService(t *testing.T) {
	cmd := &cobra.Command{}
	rc := &RuntimeContext{
		ServiceName:  "*",
		SessionStore: &mockSessionStore{loadErr: common.ErrKeyNotFound},
		CookieStore:  &mockSessionStore{loadErr: common.ErrKeyNotFound},
		Config:       config.DefaultConfig(),
	}
	SetContext(cmd, rc)

	_, err := SetupSession(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "not logged in") {
		t.Errorf("error = %q, want containing %q", err.Error(), "not logged in")
	}
}

func TestSetupSession_SpecificService(t *testing.T) {
	cmd := &cobra.Command{}
	rc := &RuntimeContext{
		ServiceName:  "drive",
		SessionStore: &mockSessionStore{loadErr: common.ErrKeyNotFound},
		AccountStore: &mockSessionStore{loadErr: common.ErrKeyNotFound},
		CookieStore:  &mockSessionStore{loadErr: common.ErrKeyNotFound},
		Config:       config.DefaultConfig(),
	}
	SetContext(cmd, rc)

	_, err := SetupSession(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "not logged in") {
		t.Errorf("error = %q, want containing %q", err.Error(), "not logged in")
	}
}

func TestSetupSession_EmptyService(t *testing.T) {
	cmd := &cobra.Command{}
	rc := &RuntimeContext{
		ServiceName:  "",
		SessionStore: &mockSessionStore{loadErr: common.ErrKeyNotFound},
		CookieStore:  &mockSessionStore{loadErr: common.ErrKeyNotFound},
		Config:       config.DefaultConfig(),
	}
	SetContext(cmd, rc)

	_, err := SetupSession(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "not logged in") {
		t.Errorf("error = %q, want containing %q", err.Error(), "not logged in")
	}
}

func TestResolveVersionRC(t *testing.T) {
	tests := []struct {
		name     string
		override string
		subsys   map[string]*config.CoreConfig
		service  string
		want     string
	}{
		{
			"flag override takes precedence",
			"1.2.3.4",
			map[string]*config.CoreConfig{
				"drive": func() *config.CoreConfig {
					c := &config.CoreConfig{
						MaxJobs:    config.NewParam(10),
						Account:    config.NewParam("default"),
						AppVersion: config.NewParam(""),
					}
					c.AppVersion.SetFile("9.9.9.9")
					return c
				}(),
			},
			"drive",
			"1.2.3.4",
		},
		{
			"config override used when no flag",
			"",
			map[string]*config.CoreConfig{
				"drive": func() *config.CoreConfig {
					c := &config.CoreConfig{
						MaxJobs:    config.NewParam(10),
						Account:    config.NewParam("default"),
						AppVersion: config.NewParam(""),
					}
					c.AppVersion.SetFile("2.0.0.0")
					return c
				}(),
			},
			"drive",
			"2.0.0.0",
		},
		{
			"empty when no overrides",
			"",
			nil,
			"drive",
			"",
		},
		{
			"config for different service not used",
			"",
			map[string]*config.CoreConfig{
				"lumo": func() *config.CoreConfig {
					c := &config.CoreConfig{
						MaxJobs:    config.NewParam(10),
						Account:    config.NewParam("default"),
						AppVersion: config.NewParam(""),
					}
					c.AppVersion.SetFile("3.0.0.0")
					return c
				}(),
			},
			"drive",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			if tt.subsys != nil {
				cfg.Subsystems = tt.subsys
			}
			rc := &RuntimeContext{
				AppVersionOverride: tt.override,
				Config:             cfg,
			}

			got := resolveVersionRC(rc, tt.service)
			if got != tt.want {
				t.Errorf("resolveVersionRC(%q) = %q, want %q", tt.service, got, tt.want)
			}
		})
	}
}

func TestResolveMaxJobs(t *testing.T) {
	tests := []struct {
		name        string
		flagChanged bool
		flagValue   int
		coreSet     bool
		coreValue   int
		subsys      string
		subsysSet   bool
		subsysValue int
		service     string
		want        int
	}{
		{
			"CLI flag takes precedence over everything",
			true, 20,
			true, 8,
			"drive", true, 4,
			"drive",
			20,
		},
		{
			"subsystem override used when no CLI flag",
			false, 10,
			true, 8,
			"drive", true, 4,
			"drive",
			4,
		},
		{
			"core config used when no subsystem override",
			false, 10,
			true, 8,
			"", false, 0,
			"drive",
			8,
		},
		{
			"DefaultMaxWorkers when nothing configured",
			false, 10,
			false, 0,
			"", false, 0,
			"drive",
			common.DefaultMaxWorkers(),
		},
		{
			"subsystem for different service not used",
			false, 10,
			false, 0,
			"lumo", true, 4,
			"drive",
			common.DefaultMaxWorkers(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.PersistentFlags().IntVarP(&rootParams.MaxWorkers, "max-jobs", "j", 10, "")
			if tt.flagChanged {
				rootParams.MaxWorkers = tt.flagValue
				_ = cmd.PersistentFlags().Set("max-jobs", fmt.Sprintf("%d", tt.flagValue))
			}

			cfg := config.DefaultConfig()
			if tt.coreSet {
				cfg.MaxJobs.SetFile(tt.coreValue)
			}
			if tt.subsys != "" && tt.subsysSet {
				cfg.Subsystems[tt.subsys] = &config.CoreConfig{
					MaxJobs:    config.NewParam(common.DefaultMaxWorkers()),
					Account:    config.NewParam("default"),
					AppVersion: config.NewParam(""),
				}
				cfg.Subsystems[tt.subsys].MaxJobs.SetFile(tt.subsysValue)
			}

			rc := &RuntimeContext{
				Config:      cfg,
				ServiceName: tt.service,
			}

			got := resolveMaxJobs(cmd, rc)
			if got != tt.want {
				t.Errorf("resolveMaxJobs() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSessionConfigFromRC(t *testing.T) {
	// Create a minimal command tree so resolveMaxJobs can look up the flag.
	cmd := &cobra.Command{}
	cmd.PersistentFlags().IntVarP(&rootParams.MaxWorkers, "max-jobs", "j", 10, "")

	t.Run("nil config", func(t *testing.T) {
		rc := &RuntimeContext{Config: nil}
		got := sessionConfigFromRC(cmd, rc)
		if got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("with config", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Shares["test"] = common.ShareConfig{MemoryCache: common.CacheMetadata}
		cfg.Subsystems["drive"] = &config.CoreConfig{
			MaxJobs:    config.NewParam(10),
			Account:    config.NewParam("default"),
			AppVersion: config.NewParam(""),
		}
		cfg.Subsystems["drive"].Account.SetFile("myaccount")
		rc := &RuntimeContext{Config: cfg}

		got := sessionConfigFromRC(cmd, rc)
		if got == nil {
			t.Fatal("expected non-nil SessionConfig")
		}
		if got.Shares["test"].MemoryCache != common.CacheMetadata {
			t.Errorf("Shares[test].MemoryCache = %v, want %v", got.Shares["test"].MemoryCache, common.CacheMetadata)
		}
		if got.Defaults["drive"] != "myaccount" {
			t.Errorf("Defaults[drive] = %q, want %q", got.Defaults["drive"], "myaccount")
		}
		if got.MaxJobs != common.DefaultMaxWorkers() {
			t.Errorf("MaxJobs = %d, want %d", got.MaxJobs, common.DefaultMaxWorkers())
		}
	})
}
