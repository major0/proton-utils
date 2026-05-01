package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// MemoryCacheLevel controls in-memory caching of decrypted data on Link
// objects. Each level includes the previous: metadata implies linkname.
type MemoryCacheLevel int

const (
	// CacheDisabled disables all in-memory caching. Decryption happens
	// on every accessor call.
	CacheDisabled MemoryCacheLevel = iota
	// CacheLinkName caches decrypted names on Link objects for the session.
	CacheLinkName
	// CacheMetadata caches all decrypted metadata (names, stat, keyrings)
	// on Link objects for the session. Implies CacheLinkName.
	CacheMetadata
)

// String returns the YAML-friendly string for a MemoryCacheLevel.
func (m MemoryCacheLevel) String() string {
	switch m {
	case CacheLinkName:
		return "linkname"
	case CacheMetadata:
		return "metadata"
	default:
		return "disabled"
	}
}

// MarshalYAML encodes a MemoryCacheLevel as a YAML string.
func (m MemoryCacheLevel) MarshalYAML() (interface{}, error) {
	return m.String(), nil
}

// UnmarshalYAML decodes a YAML string into a MemoryCacheLevel.
func (m *MemoryCacheLevel) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	switch s {
	case "disabled", "":
		*m = CacheDisabled
	case "linkname":
		*m = CacheLinkName
	case "metadata":
		*m = CacheMetadata
	default:
		return fmt.Errorf("unknown memory_cache level: %q", s)
	}
	return nil
}

// DiskCacheLevel controls on-disk caching of encrypted API objects.
type DiskCacheLevel int

const (
	// DiskCacheDisabled disables on-disk caching.
	DiskCacheDisabled DiskCacheLevel = iota
	// DiskCacheObjectStore enables on-disk caching via a diskv instance.
	DiskCacheObjectStore
)

// String returns the YAML-friendly string for a DiskCacheLevel.
func (d DiskCacheLevel) String() string {
	switch d {
	case DiskCacheObjectStore:
		return "objectstore"
	default:
		return "disabled"
	}
}

// MarshalYAML encodes a DiskCacheLevel as a YAML string.
func (d DiskCacheLevel) MarshalYAML() (interface{}, error) {
	return d.String(), nil
}

// UnmarshalYAML decodes a YAML string into a DiskCacheLevel.
func (d *DiskCacheLevel) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	switch s {
	case "disabled", "":
		*d = DiskCacheDisabled
	case "objectstore":
		*d = DiskCacheObjectStore
	default:
		return fmt.Errorf("unknown disk_cache level: %q", s)
	}
	return nil
}

// ShareConfig controls per-share caching policy. Both fields default
// to disabled (strictest encrypted-data-handling compliance).
type ShareConfig struct {
	MemoryCache MemoryCacheLevel `yaml:"memory_cache"`
	DiskCache   DiskCacheLevel   `yaml:"disk_cache"`
}

// Config holds application-level settings loaded from YAML.
type Config struct {
	Shares          map[string]ShareConfig `yaml:"shares,omitempty"`
	Defaults        map[string]string      `yaml:"defaults,omitempty"`
	ServiceVersions map[string]string      `yaml:"service_versions,omitempty"`
}

// DefaultConfig returns a Config with all defaults (empty maps, caching off).
func DefaultConfig() *Config {
	return &Config{
		Shares:          make(map[string]ShareConfig),
		Defaults:        make(map[string]string),
		ServiceVersions: make(map[string]string),
	}
}

// DefaultAccount returns the configured default account for a service,
// or "default" when not configured.
func (c *Config) DefaultAccount(service string) string {
	if c.Defaults != nil {
		if acct, ok := c.Defaults[service]; ok && acct != "" {
			return acct
		}
	}
	return "default"
}

// ServiceVersion returns the version override for a service, or the
// defaultVersion if none is configured. Returns empty string if
// defaultVersion is empty and no override exists.
func (c *Config) ServiceVersion(service, defaultVersion string) string {
	if c.ServiceVersions != nil {
		if v, ok := c.ServiceVersions[service]; ok && v != "" {
			return v
		}
	}
	return defaultVersion
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
		cfg.Shares = make(map[string]ShareConfig)
	}
	if cfg.Defaults == nil {
		cfg.Defaults = make(map[string]string)
	}
	if cfg.ServiceVersions == nil {
		cfg.ServiceVersions = make(map[string]string)
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
