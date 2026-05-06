// Package config provides primitives for loading, saving, and validating
// application configuration files. It produces config values that the
// consumer sets on the session for subsystem packages to read.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/major0/proton-cli/api"
	"gopkg.in/yaml.v3"
)

// CoreConfig holds the overridable scalar settings. This struct is used
// both at the top level (core.* selectors) and per-subsystem (<svc>.*
// selectors). Same shape, same dispatch table, no duplication.
type CoreConfig struct {
	MaxJobs    Param[int]
	Account    Param[string]
	AppVersion Param[string]
}

// Config holds application-level settings loaded from YAML.
type Config struct {
	CoreConfig // embedded — top-level "core.*" settings

	// MemoryCacheWatermark is core-only (not overridable per-subsystem).
	MemoryCacheWatermark Param[[2]int64]

	// Shares is keyed by Proton share ID.
	Shares map[string]api.ShareConfig

	// Subsystems holds per-subsystem overrides keyed by service name.
	Subsystems map[string]*CoreConfig
}

// DefaultAccount returns the configured default account for a service.
// Checks subsystem override first, then core Account, then returns "default".
func (c *Config) DefaultAccount(service string) string {
	if sub, ok := c.Subsystems[service]; ok && sub.Account.IsSet() {
		return sub.Account.Value()
	}
	if c.Account.IsSet() {
		return c.Account.Value()
	}
	return c.Account.Default()
}

// coreConfigYAML is the on-disk representation of CoreConfig fields.
type coreConfigYAML struct {
	MaxJobs    *int    `yaml:"max_jobs,omitempty"`
	Account    *string `yaml:"account,omitempty"`
	AppVersion *string `yaml:"app_version,omitempty"`
}

// configYAML is the on-disk YAML representation.
type configYAML struct {
	coreConfigYAML       `yaml:",inline"`
	MemoryCacheWatermark *string                    `yaml:"memory_cache_watermark,omitempty"`
	Shares               map[string]api.ShareConfig `yaml:"shares,omitempty"`
	Subsystems           map[string]coreConfigYAML  `yaml:"subsystems,omitempty"`
}

// MarshalYAML implements yaml.Marshaler for Config.
func (c *Config) MarshalYAML() (interface{}, error) {
	y := &configYAML{
		Shares:     make(map[string]api.ShareConfig),
		Subsystems: make(map[string]coreConfigYAML),
	}
	marshalCoreConfig(&c.CoreConfig, &y.coreConfigYAML)
	if c.MemoryCacheWatermark.Source() == File {
		wm := c.MemoryCacheWatermark.Value()
		s := fmt.Sprintf("%d:%d", wm[0], wm[1])
		y.MemoryCacheWatermark = &s
	}
	for id, sc := range c.Shares {
		y.Shares[id] = sc
	}
	for name, sub := range c.Subsystems {
		var sy coreConfigYAML
		marshalCoreConfig(sub, &sy)
		if sy != (coreConfigYAML{}) {
			y.Subsystems[name] = sy
		}
	}
	if len(y.Shares) == 0 {
		y.Shares = nil
	}
	if len(y.Subsystems) == 0 {
		y.Subsystems = nil
	}
	return y, nil
}

// UnmarshalYAML implements yaml.Unmarshaler for Config.
func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	var y configYAML
	if err := value.Decode(&y); err != nil {
		return err
	}
	unmarshalCoreConfig(&y.coreConfigYAML, &c.CoreConfig)
	if y.MemoryCacheWatermark != nil {
		wm, err := parseWatermarkString(*y.MemoryCacheWatermark)
		if err != nil {
			return fmt.Errorf("memory_cache_watermark: %w", err)
		}
		c.MemoryCacheWatermark.SetFile(wm)
	}
	if y.Shares != nil {
		c.Shares = y.Shares
	}
	if c.Shares == nil {
		c.Shares = make(map[string]api.ShareConfig)
	}
	if y.Subsystems != nil {
		for name, sy := range y.Subsystems {
			sub := &CoreConfig{
				MaxJobs:    NewParam(api.DefaultMaxWorkers()),
				Account:    NewParam("default"),
				AppVersion: NewParam(""),
			}
			unmarshalCoreConfig(&sy, sub)
			c.Subsystems[name] = sub
		}
	}
	if c.Subsystems == nil {
		c.Subsystems = make(map[string]*CoreConfig)
	}
	return nil
}

// marshalCoreConfig writes File-sourced Params from a CoreConfig into
// the corresponding coreConfigYAML fields.
func marshalCoreConfig(src *CoreConfig, dst *coreConfigYAML) {
	if src.MaxJobs.Source() == File {
		v := src.MaxJobs.Value()
		dst.MaxJobs = &v
	}
	if src.Account.Source() == File {
		v := src.Account.Value()
		dst.Account = &v
	}
	if src.AppVersion.Source() == File {
		v := src.AppVersion.Value()
		dst.AppVersion = &v
	}
}

// unmarshalCoreConfig marks loaded fields as source File on the CoreConfig.
func unmarshalCoreConfig(src *coreConfigYAML, dst *CoreConfig) {
	if src.MaxJobs != nil {
		dst.MaxJobs.SetFile(*src.MaxJobs)
	}
	if src.Account != nil {
		dst.Account.SetFile(*src.Account)
	}
	if src.AppVersion != nil {
		dst.AppVersion.SetFile(*src.AppVersion)
	}
}

// parseWatermarkString parses a "min:max" watermark string.
func parseWatermarkString(s string) ([2]int64, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return [2]int64{}, fmt.Errorf("expected format min:max, got %q", s)
	}
	lo, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return [2]int64{}, fmt.Errorf("invalid min value: %w", err)
	}
	hi, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return [2]int64{}, fmt.Errorf("invalid max value: %w", err)
	}
	return [2]int64{lo, hi}, nil
}

// DefaultConfig returns a Config with all defaults (empty maps, caching off).
func DefaultConfig() *Config {
	return &Config{
		CoreConfig: CoreConfig{
			MaxJobs:    NewParam(api.DefaultMaxWorkers()),
			Account:    NewParam("default"),
			AppVersion: NewParam(""),
		},
		MemoryCacheWatermark: NewParam([2]int64{0, 0}),
		Shares:               make(map[string]api.ShareConfig),
		Subsystems:           make(map[string]*CoreConfig),
	}
}

// LoadConfig reads a YAML config file. Returns DefaultConfig if the file
// does not exist. Returns an error only for I/O or parse failures.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path from user config, not tainted input
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultConfig(), nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	// Ensure maps are initialized even if YAML had empty sections.
	if cfg.Shares == nil {
		cfg.Shares = make(map[string]api.ShareConfig)
	}
	if cfg.Subsystems == nil {
		cfg.Subsystems = make(map[string]*CoreConfig)
	}

	return cfg, nil
}

// SaveConfig writes the config to path as YAML. Creates parent directories
// and uses atomic write (temp file + rename) to prevent corruption.
func SaveConfig(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", dir, err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	// Atomic write: temp file in same directory, then rename.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("config: write %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("config: rename %s: %w", path, err)
	}

	return nil
}
