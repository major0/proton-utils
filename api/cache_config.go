package api

import (
	"fmt"

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
