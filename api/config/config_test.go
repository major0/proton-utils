package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/major0/proton-cli/api"
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
	if len(cfg.Defaults) != 0 {
		t.Fatalf("expected empty defaults, got %d", len(cfg.Defaults))
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
	cfg.Defaults["drive"] = "work"

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if !reflect.DeepEqual(cfg.Shares, loaded.Shares) {
		t.Fatalf("shares mismatch:\n  got:  %+v\n  want: %+v", loaded.Shares, cfg.Shares)
	}
	if !reflect.DeepEqual(cfg.Defaults, loaded.Defaults) {
		t.Fatalf("defaults mismatch:\n  got:  %+v\n  want: %+v", loaded.Defaults, cfg.Defaults)
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

	cfg.Defaults["drive"] = "work"
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

// TestConfigRoundTrip_Property verifies that for any valid Config,
// SaveConfig + LoadConfig produces an equivalent Config.
//
// **Property 1: Config serialization round-trip**
// **Validates: Requirements 5.1**
func TestConfigRoundTrip_Property(t *testing.T) {
	dir := t.TempDir()

	memoryLevelGen := rapid.SampledFrom([]api.MemoryCacheLevel{api.CacheDisabled, api.CacheLinkName, api.CacheMetadata})
	diskLevelGen := rapid.SampledFrom([]api.DiskCacheLevel{api.DiskCacheDisabled, api.DiskCacheObjectStore})

	rapid.Check(t, func(t *rapid.T) {
		cfg := &Config{
			Shares:   make(map[string]api.ShareConfig),
			Defaults: make(map[string]string),
		}

		nShares := rapid.IntRange(0, 5).Draw(t, "nShares")
		for i := 0; i < nShares; i++ {
			name := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9 ]{0,15}`).Draw(t, "shareName")
			cfg.Shares[name] = api.ShareConfig{
				MemoryCache: memoryLevelGen.Draw(t, "memory"),
				DiskCache:   diskLevelGen.Draw(t, "disk"),
			}
		}

		nDefaults := rapid.IntRange(0, 3).Draw(t, "nDefaults")
		for i := 0; i < nDefaults; i++ {
			svc := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, "service")
			acct := rapid.StringMatching(`[a-z]{3,8}`).Draw(t, "account")
			cfg.Defaults[svc] = acct
		}

		path := filepath.Join(dir, rapid.StringMatching(`[a-z]{8}`).Draw(t, "file")+".yaml")

		if err := SaveConfig(path, cfg); err != nil {
			t.Fatalf("SaveConfig: %v", err)
		}

		loaded, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}

		if !reflect.DeepEqual(cfg.Shares, loaded.Shares) {
			t.Fatalf("shares mismatch")
		}
		if !reflect.DeepEqual(cfg.Defaults, loaded.Defaults) {
			t.Fatalf("defaults mismatch")
		}
	})
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
