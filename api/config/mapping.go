package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/major0/proton-cli/api"
)

// ParamDef describes a single addressable config field for the selector mapping.
type ParamDef struct {
	Parse  func(value string) (any, error)
	Format func(value any) string
	Get    func(cc *CoreConfig) ParamInfo
	Set    func(cc *CoreConfig, value any)
	Unset  func(cc *CoreConfig)
}

// coreFields maps option name → ParamDef for the "core" and subsystem namespaces.
// The same table is reused for both core.* and <subsystem>.* selectors.
var coreFields = map[string]*ParamDef{
	"max_jobs": {
		Parse:  parsePositiveInt,
		Format: formatInt,
		Get:    func(cc *CoreConfig) ParamInfo { return cc.MaxJobs.Info(formatInt) },
		Set:    func(cc *CoreConfig, v any) { cc.MaxJobs.SetFile(v.(int)) },
		Unset:  func(cc *CoreConfig) { cc.MaxJobs.Reset() },
	},
	"account": {
		Parse:  parseNonEmptyString,
		Format: formatString,
		Get:    func(cc *CoreConfig) ParamInfo { return cc.Account.Info(formatString) },
		Set:    func(cc *CoreConfig, v any) { cc.Account.SetFile(v.(string)) },
		Unset:  func(cc *CoreConfig) { cc.Account.Reset() },
	},
	"app_version": {
		Parse:  parseNonEmptyString,
		Format: formatString,
		Get:    func(cc *CoreConfig) ParamInfo { return cc.AppVersion.Info(formatString) },
		Set:    func(cc *CoreConfig, v any) { cc.AppVersion.SetFile(v.(string)) },
		Unset:  func(cc *CoreConfig) { cc.AppVersion.Reset() },
	},
}

// shareFields maps option name → ParamDef for the "share" namespace.
var shareFields = map[string]*ParamDef{
	"memory_cache": {
		Parse:  parseMemoryCacheLevel,
		Format: formatString,
	},
	"disk_cache": {
		Parse:  parseDiskCacheLevel,
		Format: formatString,
	},
}

// Entry represents a single config value for list/show output.
type Entry struct {
	Selector string
	Value    string
	Source   ParamSource
}

// Get resolves a selector to its formatted value string.
func Get(cfg *Config, sel Selector) (string, error) {
	if len(sel.Segments) < 2 {
		return "", fmt.Errorf("config: selector requires at least two segments (namespace.field), got %q", sel.String())
	}

	ns := sel.Segments[0].Name

	switch ns {
	case "core":
		return getCoreField(cfg, sel)
	case "share":
		return getShareField(cfg, sel)
	default:
		return getSubsystemField(cfg, sel)
	}
}

// Set applies a string value to the Config at the location identified by the selector.
func Set(cfg *Config, sel Selector, value string) error {
	if len(sel.Segments) < 2 {
		return fmt.Errorf("config: selector requires at least two segments (namespace.field), got %q", sel.String())
	}

	ns := sel.Segments[0].Name

	switch ns {
	case "core":
		return setCoreField(cfg, sel, value)
	case "share":
		return setShareField(cfg, sel, value)
	default:
		return setSubsystemField(cfg, sel, value)
	}
}

// UnsetField reverts the config field addressed by the selector to its default.
func UnsetField(cfg *Config, sel Selector) error {
	if len(sel.Segments) < 2 {
		return fmt.Errorf("config: selector requires at least two segments (namespace.field), got %q", sel.String())
	}

	ns := sel.Segments[0].Name

	switch ns {
	case "core":
		return unsetCoreField(cfg, sel)
	case "share":
		return unsetShareField(cfg, sel)
	default:
		return unsetSubsystemField(cfg, sel)
	}
}

// List returns all File-sourced config entries.
func List(cfg *Config) []Entry {
	var entries []Entry

	// Core fields.
	for _, name := range sortedCoreFieldNames() {
		pd := coreFields[name]
		info := pd.Get(&cfg.CoreConfig)
		if info.Source == File {
			entries = append(entries, Entry{
				Selector: "core." + name,
				Value:    info.Value,
				Source:   File,
			})
		}
	}

	// Core-only: memory_cache_watermark.
	if cfg.MemoryCacheWatermark.Source() == File {
		entries = append(entries, Entry{
			Selector: "core.memory_cache_watermark",
			Value:    formatWatermark(cfg.MemoryCacheWatermark.Value()),
			Source:   File,
		})
	}

	// Subsystem overrides.
	for _, svc := range sortedKeys(cfg.Subsystems) {
		sub := cfg.Subsystems[svc]
		for _, name := range sortedCoreFieldNames() {
			pd := coreFields[name]
			info := pd.Get(sub)
			if info.Source == File {
				entries = append(entries, Entry{
					Selector: svc + "." + name,
					Value:    info.Value,
					Source:   File,
				})
			}
		}
	}

	// Share entries.
	for _, id := range sortedKeys(cfg.Shares) {
		sc := cfg.Shares[id]
		if sc.MemoryCache != api.CacheDisabled {
			entries = append(entries, Entry{
				Selector: fmt.Sprintf("share[id=%s].memory_cache", id),
				Value:    sc.MemoryCache.String(),
				Source:   File,
			})
		}
		if sc.DiskCache != api.DiskCacheDisabled {
			entries = append(entries, Entry{
				Selector: fmt.Sprintf("share[id=%s].disk_cache", id),
				Value:    sc.DiskCache.String(),
				Source:   File,
			})
		}
	}

	return entries
}

// Show returns all config entries with their source annotation.
func Show(cfg *Config) []Entry {
	var entries []Entry

	// Core fields.
	for _, name := range sortedCoreFieldNames() {
		pd := coreFields[name]
		info := pd.Get(&cfg.CoreConfig)
		entries = append(entries, Entry{
			Selector: "core." + name,
			Value:    info.Value,
			Source:   info.Source,
		})
	}

	// Core-only: memory_cache_watermark.
	wmInfo := cfg.MemoryCacheWatermark.Info(func(v any) string {
		return formatWatermark(v.([2]int64))
	})
	entries = append(entries, Entry{
		Selector: "core.memory_cache_watermark",
		Value:    wmInfo.Value,
		Source:   wmInfo.Source,
	})

	// Subsystem overrides.
	for _, svc := range sortedKeys(cfg.Subsystems) {
		sub := cfg.Subsystems[svc]
		for _, name := range sortedCoreFieldNames() {
			pd := coreFields[name]
			info := pd.Get(sub)
			if info.Source == File {
				entries = append(entries, Entry{
					Selector: svc + "." + name,
					Value:    info.Value,
					Source:   info.Source,
				})
			}
		}
	}

	// Share entries.
	for _, id := range sortedKeys(cfg.Shares) {
		sc := cfg.Shares[id]
		entries = append(entries, Entry{
			Selector: fmt.Sprintf("share[id=%s].memory_cache", id),
			Value:    sc.MemoryCache.String(),
			Source:   File,
		})
		entries = append(entries, Entry{
			Selector: fmt.Sprintf("share[id=%s].disk_cache", id),
			Value:    sc.DiskCache.String(),
			Source:   File,
		})
	}

	return entries
}

// --- Core field dispatch ---

func getCoreField(cfg *Config, sel Selector) (string, error) {
	fieldName := sel.Segments[1].Name

	if fieldName == "memory_cache_watermark" {
		return formatWatermark(cfg.MemoryCacheWatermark.Value()), nil
	}

	pd, ok := coreFields[fieldName]
	if !ok {
		return "", unknownFieldError("core", fieldName)
	}
	info := pd.Get(&cfg.CoreConfig)
	return info.Value, nil
}

func setCoreField(cfg *Config, sel Selector, value string) error {
	fieldName := sel.Segments[1].Name

	if fieldName == "memory_cache_watermark" {
		v, err := parseWatermark(value)
		if err != nil {
			return err
		}
		cfg.MemoryCacheWatermark.SetFile(v.([2]int64))
		return nil
	}

	pd, ok := coreFields[fieldName]
	if !ok {
		return unknownFieldError("core", fieldName)
	}
	v, err := pd.Parse(value)
	if err != nil {
		return err
	}
	pd.Set(&cfg.CoreConfig, v)
	return nil
}

func unsetCoreField(cfg *Config, sel Selector) error {
	fieldName := sel.Segments[1].Name

	if fieldName == "memory_cache_watermark" {
		cfg.MemoryCacheWatermark.Reset()
		return nil
	}

	pd, ok := coreFields[fieldName]
	if !ok {
		return unknownFieldError("core", fieldName)
	}
	pd.Unset(&cfg.CoreConfig)
	return nil
}

// --- Share field dispatch ---

func getShareField(cfg *Config, sel Selector) (string, error) {
	if sel.Segments[0].IndexKey == "" {
		return "", fmt.Errorf("config: shares requires an index (share[name=X] or share[id=X])")
	}
	if len(sel.Segments) < 2 {
		return "", fmt.Errorf("config: shares selector requires a field (share[id=X].field)")
	}

	id := sel.Segments[0].IndexVal
	fieldName := sel.Segments[1].Name

	if _, ok := shareFields[fieldName]; !ok {
		return "", unknownFieldError("share", fieldName)
	}

	sc := cfg.Shares[id] // zero value if absent — defaults to disabled

	switch fieldName {
	case "memory_cache":
		return sc.MemoryCache.String(), nil
	case "disk_cache":
		return sc.DiskCache.String(), nil
	default:
		return "", unknownFieldError("share", fieldName)
	}
}

func setShareField(cfg *Config, sel Selector, value string) error {
	if sel.Segments[0].IndexKey == "" {
		return fmt.Errorf("config: shares requires an index (share[name=X] or share[id=X])")
	}
	if len(sel.Segments) < 2 {
		return fmt.Errorf("config: shares selector requires a field (share[id=X].field)")
	}

	id := sel.Segments[0].IndexVal
	fieldName := sel.Segments[1].Name

	pd, ok := shareFields[fieldName]
	if !ok {
		return unknownFieldError("share", fieldName)
	}

	v, err := pd.Parse(value)
	if err != nil {
		return err
	}

	sc := cfg.Shares[id]
	switch fieldName {
	case "memory_cache":
		sc.MemoryCache = v.(api.MemoryCacheLevel)
	case "disk_cache":
		sc.DiskCache = v.(api.DiskCacheLevel)
	}
	cfg.Shares[id] = sc
	return nil
}

func unsetShareField(cfg *Config, sel Selector) error {
	if sel.Segments[0].IndexKey == "" {
		return fmt.Errorf("config: shares requires an index (share[name=X] or share[id=X])")
	}
	if len(sel.Segments) < 2 {
		return fmt.Errorf("config: shares selector requires a field (share[id=X].field)")
	}

	id := sel.Segments[0].IndexVal
	fieldName := sel.Segments[1].Name

	if _, ok := shareFields[fieldName]; !ok {
		return unknownFieldError("share", fieldName)
	}

	sc, exists := cfg.Shares[id]
	if !exists {
		return nil // already unset — idempotent
	}

	switch fieldName {
	case "memory_cache":
		sc.MemoryCache = api.CacheDisabled
	case "disk_cache":
		sc.DiskCache = api.DiskCacheDisabled
	}

	// If both fields are at their zero/disabled state, remove the entry.
	if sc.MemoryCache == api.CacheDisabled && sc.DiskCache == api.DiskCacheDisabled {
		delete(cfg.Shares, id)
	} else {
		cfg.Shares[id] = sc
	}
	return nil
}

// --- Subsystem field dispatch ---

func getSubsystemField(cfg *Config, sel Selector) (string, error) {
	svc := sel.Segments[0].Name
	if _, err := api.LookupService(svc); err != nil {
		return "", unknownNamespaceError(svc)
	}

	fieldName := sel.Segments[1].Name
	pd, ok := coreFields[fieldName]
	if !ok {
		return "", unknownFieldError(svc, fieldName)
	}

	// Precedence: subsystem → core → default.
	if sub, ok := cfg.Subsystems[svc]; ok {
		info := pd.Get(sub)
		if info.Source != Unset {
			return info.Value, nil
		}
	}

	// Fall through to core.
	info := pd.Get(&cfg.CoreConfig)
	return info.Value, nil
}

func setSubsystemField(cfg *Config, sel Selector, value string) error {
	svc := sel.Segments[0].Name
	if _, err := api.LookupService(svc); err != nil {
		return unknownNamespaceError(svc)
	}

	fieldName := sel.Segments[1].Name
	pd, ok := coreFields[fieldName]
	if !ok {
		return unknownFieldError(svc, fieldName)
	}

	v, err := pd.Parse(value)
	if err != nil {
		return err
	}

	// Create subsystem entry if absent.
	sub, ok := cfg.Subsystems[svc]
	if !ok {
		sub = &CoreConfig{
			MaxJobs:    NewParam(api.DefaultMaxWorkers()),
			Account:    NewParam("default"),
			AppVersion: NewParam(""),
		}
		cfg.Subsystems[svc] = sub
	}

	pd.Set(sub, v)
	return nil
}

func unsetSubsystemField(cfg *Config, sel Selector) error {
	svc := sel.Segments[0].Name
	if _, err := api.LookupService(svc); err != nil {
		return unknownNamespaceError(svc)
	}

	fieldName := sel.Segments[1].Name
	pd, ok := coreFields[fieldName]
	if !ok {
		return unknownFieldError(svc, fieldName)
	}

	sub, ok := cfg.Subsystems[svc]
	if !ok {
		return nil // already unset — idempotent
	}

	pd.Unset(sub)

	// If all fields are Unset, remove the subsystem entry.
	if !sub.MaxJobs.IsSet() && !sub.Account.IsSet() && !sub.AppVersion.IsSet() {
		delete(cfg.Subsystems, svc)
	}
	return nil
}

// --- Validation/parse functions ---

func parsePositiveInt(s string) (any, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("config: max_jobs must be a positive integer, got %q", s)
	}
	return n, nil
}

func parseNonEmptyString(s string) (any, error) {
	if s == "" {
		return nil, fmt.Errorf("config: value must be non-empty")
	}
	return s, nil
}

func parseWatermark(s string) (any, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("config: memory_cache_watermark must be in format min:max, got %q", s)
	}
	min, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("config: memory_cache_watermark min must be a non-negative integer, got %q", parts[0])
	}
	max, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("config: memory_cache_watermark max must be a non-negative integer, got %q", parts[1])
	}
	if min < 0 {
		return nil, fmt.Errorf("config: memory_cache_watermark min must be non-negative, got %d", min)
	}
	if max < min {
		return nil, fmt.Errorf("config: memory_cache_watermark max (%d) must be >= min (%d)", max, min)
	}
	return [2]int64{min, max}, nil
}

func parseMemoryCacheLevel(s string) (any, error) {
	switch s {
	case "disabled":
		return api.CacheDisabled, nil
	case "linkname":
		return api.CacheLinkName, nil
	case "metadata":
		return api.CacheMetadata, nil
	default:
		return nil, fmt.Errorf("config: memory_cache must be one of: disabled, linkname, metadata; got %q", s)
	}
}

func parseDiskCacheLevel(s string) (any, error) {
	switch s {
	case "disabled":
		return api.DiskCacheDisabled, nil
	case "objectstore":
		return api.DiskCacheObjectStore, nil
	default:
		return nil, fmt.Errorf("config: disk_cache must be one of: disabled, objectstore; got %q", s)
	}
}

// --- Format functions ---

func formatInt(v any) string    { return strconv.Itoa(v.(int)) }
func formatString(v any) string { return v.(string) }
func formatWatermark(wm [2]int64) string {
	return fmt.Sprintf("%d:%d", wm[0], wm[1])
}

// --- Error helpers ---

func unknownNamespaceError(ns string) error {
	var valid []string
	valid = append(valid, "core", "share")
	for name := range api.Services {
		valid = append(valid, name)
	}
	sort.Strings(valid)
	return fmt.Errorf("config: unknown namespace %q (valid: %s)", ns, strings.Join(valid, ", "))
}

func unknownFieldError(ns, field string) error {
	var valid []string
	switch ns {
	case "share":
		for name := range shareFields {
			valid = append(valid, name)
		}
	default:
		for name := range coreFields {
			valid = append(valid, name)
		}
		if ns == "core" {
			valid = append(valid, "memory_cache_watermark")
		}
	}
	sort.Strings(valid)
	return fmt.Errorf("config: unknown field %q in namespace %q (valid: %s)", field, ns, strings.Join(valid, ", "))
}

// --- Helpers ---

func sortedCoreFieldNames() []string {
	names := make([]string, 0, len(coreFields))
	for name := range coreFields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
