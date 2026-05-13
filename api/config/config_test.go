package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/major0/proton-utils/api"
	"pgregory.net/rapid"
)

func TestLoadConfig_MissingFile(t *testing.T) {
	cfg, err := LoadConfig("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(cfg.Shares) != 0 {
		t.Fatalf("expected empty shares, got %d", len(cfg.Shares))
	}
	if len(cfg.Subsystems) != 0 {
		t.Fatalf("expected empty subsystems, got %d", len(cfg.Subsystems))
	}
}

func TestLoadConfig_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	_ = os.WriteFile(path, []byte("{{invalid yaml"), 0600)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

func TestSaveConfig_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "config.yaml")

	cfg := DefaultConfig()
	cfg.Shares["test"] = api.ShareConfig{MemoryCache: api.CacheLinkName}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := DefaultConfig()
	cfg.Shares["MyFolder"] = api.ShareConfig{
		MemoryCache: api.CacheMetadata,
		DiskCache:   api.DiskCacheDisabled,
	}
	cfg.Subsystems["drive"] = &CoreConfig{
		MaxJobs:    NewParam(api.DefaultMaxWorkers()),
		Account:    NewParam("default"),
		AppVersion: NewParam(""),
	}
	cfg.Subsystems["drive"].Account.SetFile("work")

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if loaded.Shares["MyFolder"].MemoryCache != api.CacheMetadata {
		t.Fatalf("shares mismatch: got %v", loaded.Shares["MyFolder"])
	}
	if !loaded.Subsystems["drive"].Account.IsSet() {
		t.Fatal("expected drive account to be set")
	}
	if loaded.Subsystems["drive"].Account.Value() != "work" {
		t.Fatalf("drive account: got %q, want %q", loaded.Subsystems["drive"].Account.Value(), "work")
	}
}

func TestSaveConfig_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := DefaultConfig()
	cfg.Shares["test"] = api.ShareConfig{DiskCache: api.DiskCacheObjectStore}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// Verify the file is valid YAML (not a partial write).
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if loaded.Shares["test"].DiskCache != api.DiskCacheObjectStore {
		t.Fatal("expected DiskCache=objectstore after save")
	}

	// Verify no temp file left behind.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file should not exist after successful save")
	}
}

func TestDefaultAccount(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.DefaultAccount("drive"); got != "default" {
		t.Fatalf("unconfigured service: got %q, want %q", got, "default")
	}

	cfg.Subsystems["drive"] = &CoreConfig{
		MaxJobs:    NewParam(api.DefaultMaxWorkers()),
		Account:    NewParam("default"),
		AppVersion: NewParam(""),
	}
	cfg.Subsystems["drive"].Account.SetFile("work")
	if got := cfg.DefaultAccount("drive"); got != "work" {
		t.Fatalf("configured service: got %q, want %q", got, "work")
	}

	if got := cfg.DefaultAccount("mail"); got != "default" {
		t.Fatalf("other service: got %q, want %q", got, "default")
	}
}

func TestShareConfigDefaults(t *testing.T) {
	var sc api.ShareConfig
	if sc.MemoryCache != api.CacheDisabled {
		t.Fatalf("default MemoryCache: got %v, want disabled", sc.MemoryCache)
	}
	if sc.DiskCache != api.DiskCacheDisabled {
		t.Fatalf("default DiskCache: got %v, want disabled", sc.DiskCache)
	}
}

func TestShareConfigYAMLRoundTrip_AllValues(t *testing.T) {
	tests := []struct {
		name string
		sc   api.ShareConfig
	}{
		{"disabled/disabled", api.ShareConfig{MemoryCache: api.CacheDisabled, DiskCache: api.DiskCacheDisabled}},
		{"linkname/disabled", api.ShareConfig{MemoryCache: api.CacheLinkName, DiskCache: api.DiskCacheDisabled}},
		{"metadata/disabled", api.ShareConfig{MemoryCache: api.CacheMetadata, DiskCache: api.DiskCacheDisabled}},
		{"disabled/objectstore", api.ShareConfig{MemoryCache: api.CacheDisabled, DiskCache: api.DiskCacheObjectStore}},
		{"metadata/objectstore", api.ShareConfig{MemoryCache: api.CacheMetadata, DiskCache: api.DiskCacheObjectStore}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")

			cfg := DefaultConfig()
			cfg.Shares["test"] = tt.sc

			if err := SaveConfig(path, cfg); err != nil {
				t.Fatalf("SaveConfig: %v", err)
			}

			loaded, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}

			got := loaded.Shares["test"]
			if got.MemoryCache != tt.sc.MemoryCache {
				t.Fatalf("MemoryCache: got %v, want %v", got.MemoryCache, tt.sc.MemoryCache)
			}
			if got.DiskCache != tt.sc.DiskCache {
				t.Fatalf("DiskCache: got %v, want %v", got.DiskCache, tt.sc.DiskCache)
			}
		})
	}
}

// TestConfigRoundTrip_Property verifies that for any valid Config with
// File-sourced Params, SaveConfig + LoadConfig produces an equivalent Config.
//
// **Property 4: SaveConfig/LoadConfig round-trip with Param**
// **Validates: Requirements 12.6**
func TestConfigRoundTrip_Property(t *testing.T) {
	dir := t.TempDir()

	memoryLevelGen := rapid.SampledFrom([]api.MemoryCacheLevel{api.CacheDisabled, api.CacheLinkName, api.CacheMetadata})
	diskLevelGen := rapid.SampledFrom([]api.DiskCacheLevel{api.DiskCacheDisabled, api.DiskCacheObjectStore})

	rapid.Check(t, func(t *rapid.T) {
		cfg := DefaultConfig()

		// Randomly set core fields with source File.
		if rapid.Bool().Draw(t, "setMaxJobs") {
			cfg.MaxJobs.SetFile(rapid.IntRange(1, 100).Draw(t, "maxJobs"))
		}
		if rapid.Bool().Draw(t, "setAccount") {
			cfg.Account.SetFile(rapid.StringMatching(`[a-z]{3,8}`).Draw(t, "account"))
		}
		if rapid.Bool().Draw(t, "setAppVersion") {
			cfg.AppVersion.SetFile(rapid.StringMatching(`[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+`).Draw(t, "appVersion"))
		}
		if rapid.Bool().Draw(t, "setWatermark") {
			lo := rapid.Int64Range(0, 1000).Draw(t, "wmMin")
			hi := rapid.Int64Range(lo, lo+1000).Draw(t, "wmMax")
			cfg.MemoryCacheWatermark.SetFile([2]int64{lo, hi})
		}

		// Random shares.
		nShares := rapid.IntRange(0, 5).Draw(t, "nShares")
		for i := 0; i < nShares; i++ {
			name := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{0,15}`).Draw(t, "shareName")
			cfg.Shares[name] = api.ShareConfig{
				MemoryCache: memoryLevelGen.Draw(t, "memory"),
				DiskCache:   diskLevelGen.Draw(t, "disk"),
			}
		}

		// Random subsystems.
		nSubs := rapid.IntRange(0, 3).Draw(t, "nSubs")
		for i := 0; i < nSubs; i++ {
			svc := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, "service")
			sub := &CoreConfig{
				MaxJobs:    NewParam(api.DefaultMaxWorkers()),
				Account:    NewParam("default"),
				AppVersion: NewParam(""),
			}
			if rapid.Bool().Draw(t, "subMaxJobs") {
				sub.MaxJobs.SetFile(rapid.IntRange(1, 100).Draw(t, "subMaxJobsVal"))
			}
			if rapid.Bool().Draw(t, "subAccount") {
				sub.Account.SetFile(rapid.StringMatching(`[a-z]{3,8}`).Draw(t, "subAccountVal"))
			}
			if rapid.Bool().Draw(t, "subAppVersion") {
				sub.AppVersion.SetFile(rapid.StringMatching(`[0-9]+\.[0-9]+`).Draw(t, "subAppVersionVal"))
			}
			cfg.Subsystems[svc] = sub
		}

		path := filepath.Join(dir, rapid.StringMatching(`[a-z]{8}`).Draw(t, "file")+".yaml")

		if err := SaveConfig(path, cfg); err != nil {
			t.Fatalf("SaveConfig: %v", err)
		}

		loaded, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}

		// Verify core fields.
		assertParamEqual(t, "MaxJobs", cfg.MaxJobs, loaded.MaxJobs)
		assertParamEqual(t, "Account", cfg.Account, loaded.Account)
		assertParamEqual(t, "AppVersion", cfg.AppVersion, loaded.AppVersion)
		assertParamEqualArr(t, "MemoryCacheWatermark", cfg.MemoryCacheWatermark, loaded.MemoryCacheWatermark)

		// Verify shares.
		if len(cfg.Shares) != len(loaded.Shares) {
			t.Fatalf("shares count: got %d, want %d", len(loaded.Shares), len(cfg.Shares))
		}
		for id, sc := range cfg.Shares {
			lsc, ok := loaded.Shares[id]
			if !ok {
				t.Fatalf("share %q missing after load", id)
			}
			if lsc.MemoryCache != sc.MemoryCache || lsc.DiskCache != sc.DiskCache {
				t.Fatalf("share %q mismatch", id)
			}
		}

		// Verify subsystems.
		for name, sub := range cfg.Subsystems {
			lsub, ok := loaded.Subsystems[name]
			if !ok {
				// Subsystem with no File-sourced fields won't be persisted.
				if sub.MaxJobs.Source() == File || sub.Account.Source() == File || sub.AppVersion.Source() == File {
					t.Fatalf("subsystem %q missing after load", name)
				}
				continue
			}
			assertParamEqual(t, name+".MaxJobs", sub.MaxJobs, lsub.MaxJobs)
			assertParamEqual(t, name+".Account", sub.Account, lsub.Account)
			assertParamEqual(t, name+".AppVersion", sub.AppVersion, lsub.AppVersion)
		}
	})
}

func assertParamEqual[T comparable](t interface{ Fatalf(string, ...any) }, name string, want, got Param[T]) {
	if want.Source() == File {
		if got.Source() != File {
			t.Fatalf("%s: source got %v, want File", name, got.Source())
		}
		if got.Value() != want.Value() {
			t.Fatalf("%s: value got %v, want %v", name, got.Value(), want.Value())
		}
	} else if got.Source() != Unset {
		t.Fatalf("%s: source got %v, want Unset", name, got.Source())
	}
}

func assertParamEqualArr(t interface{ Fatalf(string, ...any) }, name string, want, got Param[[2]int64]) {
	if want.Source() == File {
		if got.Source() != File {
			t.Fatalf("%s: source got %v, want File", name, got.Source())
		}
		if got.Value() != want.Value() {
			t.Fatalf("%s: value got %v, want %v", name, got.Value(), want.Value())
		}
	} else if got.Source() != Unset {
		t.Fatalf("%s: source got %v, want Unset", name, got.Source())
	}
}

// TestUnconfiguredShareDefaults_Property verifies that shares not in
// the config map have all caches disabled.
//
// **Property 2: Unconfigured shares default to caching disabled**
// **Validates: Requirements 5.1**
func TestUnconfiguredShareDefaults_Property(t *testing.T) {
	memoryLevelGen := rapid.SampledFrom([]api.MemoryCacheLevel{api.CacheDisabled, api.CacheLinkName, api.CacheMetadata})
	diskLevelGen := rapid.SampledFrom([]api.DiskCacheLevel{api.DiskCacheDisabled, api.DiskCacheObjectStore})

	rapid.Check(t, func(t *rapid.T) {
		cfg := DefaultConfig()
		nShares := rapid.IntRange(0, 5).Draw(t, "nShares")
		for i := 0; i < nShares; i++ {
			name := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, "name")
			cfg.Shares[name] = api.ShareConfig{
				MemoryCache: memoryLevelGen.Draw(t, "m"),
				DiskCache:   diskLevelGen.Draw(t, "d"),
			}
		}

		// Generate a name guaranteed absent.
		absent := "ABSENT_" + rapid.StringMatching(`[A-Z]{8}`).Draw(t, "absent")
		sc := cfg.Shares[absent] // zero value

		if sc.MemoryCache != api.CacheDisabled || sc.DiskCache != api.DiskCacheDisabled {
			t.Fatal("unconfigured share should have all caches disabled")
		}
	})
}

// TestSaveConfigError verifies SaveConfig returns an error for an
// unwritable directory.
func TestSaveConfigError(t *testing.T) {
	err := SaveConfig("/proc/nonexistent/deep/path/config.yaml", DefaultConfig())
	if err == nil {
		t.Fatal("expected error for unwritable path")
	}
}
