package config

import (
	"testing"

	"github.com/major0/proton-cli/api"
	"pgregory.net/rapid"
)

// validCoreSelectors returns all valid core selectors (including memory_cache_watermark).
var validCoreSelectors = []string{
	"core.max_jobs",
	"core.account",
	"core.app_version",
	"core.memory_cache_watermark",
}

// validSubsystemSelectors returns valid subsystem selectors for registered services.
func validSubsystemSelectors() []string {
	var sels []string
	for svc := range api.Services {
		for field := range coreFields {
			sels = append(sels, svc+"."+field)
		}
	}
	return sels
}

// validShareSelectors returns valid share selectors for a given share ID.
func validShareSelectors(id string) []string {
	return []string{
		"shares[id=" + id + "].memory_cache",
		"shares[id=" + id + "].disk_cache",
	}
}

// allValidLeafSelectors returns all valid leaf selectors for a given config.
func allValidLeafSelectors(cfg *Config) []string {
	var sels []string
	sels = append(sels, validCoreSelectors...)
	sels = append(sels, validSubsystemSelectors()...)
	for id := range cfg.Shares {
		sels = append(sels, validShareSelectors(id)...)
	}
	return sels
}

// genValidSelectorAndValue generates a random valid (selector string, value string) pair.
func genValidSelectorAndValue(t *rapid.T) (string, string) {
	// Choose a category: core, subsystem, or share.
	category := rapid.IntRange(0, 2).Draw(t, "category")

	switch category {
	case 0: // core
		field := rapid.IntRange(0, 3).Draw(t, "coreField")
		switch field {
		case 0:
			v := rapid.IntRange(1, 200).Draw(t, "maxJobs")
			return "core.max_jobs", formatInt(v)
		case 1:
			v := rapid.StringMatching(`[a-z]{3,10}`).Draw(t, "account")
			return "core.account", v
		case 2:
			v := rapid.StringMatching(`[0-9]+\.[0-9]+\.[0-9]+`).Draw(t, "appVersion")
			return "core.app_version", v
		default:
			min := rapid.Int64Range(0, 500).Draw(t, "wmMin")
			max := rapid.Int64Range(min, min+500).Draw(t, "wmMax")
			return "core.memory_cache_watermark", formatWatermark([2]int64{min, max})
		}
	case 1: // subsystem
		svcNames := make([]string, 0, len(api.Services))
		for name := range api.Services {
			svcNames = append(svcNames, name)
		}
		svc := rapid.SampledFrom(svcNames).Draw(t, "service")
		field := rapid.IntRange(0, 2).Draw(t, "subField")
		switch field {
		case 0:
			v := rapid.IntRange(1, 200).Draw(t, "maxJobs")
			return svc + ".max_jobs", formatInt(v)
		case 1:
			v := rapid.StringMatching(`[a-z]{3,10}`).Draw(t, "account")
			return svc + ".account", v
		default:
			v := rapid.StringMatching(`[0-9]+\.[0-9]+\.[0-9]+`).Draw(t, "appVersion")
			return svc + ".app_version", v
		}
	default: // share
		id := rapid.StringMatching(`[a-zA-Z0-9]{8,16}`).Draw(t, "shareID")
		field := rapid.IntRange(0, 1).Draw(t, "shareField")
		switch field {
		case 0:
			v := rapid.SampledFrom([]string{"disabled", "linkname", "metadata"}).Draw(t, "memCache")
			return "shares[id=" + id + "].memory_cache", v
		default:
			v := rapid.SampledFrom([]string{"disabled", "objectstore"}).Draw(t, "diskCache")
			return "shares[id=" + id + "].disk_cache", v
		}
	}
}

// TestGetSetRoundTrip_Property verifies that for any valid (selector, value) pair,
// Set followed by Get returns the original value string.
//
// **Property 2: Config get/set round-trip**
// **Validates: Requirements 2.1, 2.2**
func TestGetSetRoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cfg := DefaultConfig()
		selStr, value := genValidSelectorAndValue(t)

		sel, err := Parse(selStr)
		if err != nil {
			t.Fatalf("Parse(%q): %v", selStr, err)
		}

		if err := Set(cfg, sel, value); err != nil {
			t.Fatalf("Set(%q, %q): %v", selStr, value, err)
		}

		got, err := Get(cfg, sel)
		if err != nil {
			t.Fatalf("Get(%q): %v", selStr, err)
		}

		if got != value {
			t.Fatalf("Get(%q) = %q, want %q", selStr, got, value)
		}
	})
}

// TestValidationRejectsInvalid_Property verifies that invalid values are rejected
// and the Config remains unchanged.
//
// **Property 5: Validation rejects invalid values**
// **Validates: Requirements 10.1, 10.2**
func TestValidationRejectsInvalid_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cfg := DefaultConfig()

		// Choose a field type and generate an invalid value.
		fieldType := rapid.IntRange(0, 4).Draw(t, "fieldType")

		var selStr, value string
		switch fieldType {
		case 0: // max_jobs: negative, zero, or non-numeric
			selStr = "core.max_jobs"
			kind := rapid.IntRange(0, 2).Draw(t, "invalidKind")
			switch kind {
			case 0:
				value = formatInt(-rapid.IntRange(1, 100).Draw(t, "neg"))
			case 1:
				value = "0"
			default:
				value = rapid.StringMatching(`[a-z]{2,5}`).Draw(t, "nonNumeric")
			}
		case 1: // memory_cache_watermark: malformed
			selStr = "core.memory_cache_watermark"
			kind := rapid.IntRange(0, 2).Draw(t, "invalidWmKind")
			switch kind {
			case 0:
				// Single number, no colon.
				value = formatInt(rapid.IntRange(0, 100).Draw(t, "single"))
			case 1:
				// max < min.
				min := rapid.Int64Range(10, 100).Draw(t, "min")
				max := rapid.Int64Range(0, min-1).Draw(t, "max")
				value = formatWatermark([2]int64{min, max})
			default:
				// Non-numeric.
				value = rapid.StringMatching(`[a-z]{2,5}:[a-z]{2,5}`).Draw(t, "nonNumWm")
			}
		case 2: // memory_cache: unknown enum
			id := rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "shareID")
			selStr = "shares[id=" + id + "].memory_cache"
			value = rapid.StringMatching(`[a-z]{5,10}`).Draw(t, "badEnum")
			// Ensure it's not a valid value.
			for value == "disabled" || value == "linkname" || value == "metadata" {
				value = rapid.StringMatching(`[a-z]{5,10}`).Draw(t, "badEnum2")
			}
		case 3: // disk_cache: unknown enum
			id := rapid.StringMatching(`[a-zA-Z0-9]{8}`).Draw(t, "shareID")
			selStr = "shares[id=" + id + "].disk_cache"
			value = rapid.StringMatching(`[a-z]{5,10}`).Draw(t, "badEnum")
			// Ensure it's not a valid value.
			for value == "disabled" || value == "objectstore" {
				value = rapid.StringMatching(`[a-z]{5,10}`).Draw(t, "badEnum2")
			}
		case 4: // subsystem max_jobs: negative or non-numeric
			svcNames := make([]string, 0, len(api.Services))
			for name := range api.Services {
				svcNames = append(svcNames, name)
			}
			svc := rapid.SampledFrom(svcNames).Draw(t, "service")
			selStr = svc + ".max_jobs"
			kind := rapid.IntRange(0, 1).Draw(t, "invalidKind")
			switch kind {
			case 0:
				value = formatInt(-rapid.IntRange(1, 100).Draw(t, "neg"))
			default:
				value = rapid.StringMatching(`[a-z]{2,5}`).Draw(t, "nonNumeric")
			}
		}

		// Snapshot config state before.
		before := snapshotConfig(cfg)

		sel, err := Parse(selStr)
		if err != nil {
			t.Fatalf("Parse(%q): %v", selStr, err)
		}

		err = Set(cfg, sel, value)
		if err == nil {
			t.Fatalf("Set(%q, %q) should have returned error", selStr, value)
		}

		// Verify config unchanged.
		after := snapshotConfig(cfg)
		if before != after {
			t.Fatalf("Config changed after failed Set(%q, %q)", selStr, value)
		}
	})
}

// TestUnsetIdempotence_Property verifies that applying UnsetField twice
// produces the same state as applying it once.
//
// **Property 6: Unset idempotence**
// **Validates: Requirements 7.4**
func TestUnsetIdempotence_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cfg := DefaultConfig()

		// Set some random fields.
		nSets := rapid.IntRange(1, 5).Draw(t, "nSets")
		for i := 0; i < nSets; i++ {
			selStr, value := genValidSelectorAndValue(t)
			sel, err := Parse(selStr)
			if err != nil {
				t.Fatalf("Parse(%q): %v", selStr, err)
			}
			_ = Set(cfg, sel, value)
		}

		// Pick a random valid leaf selector to unset.
		allSels := allValidLeafSelectors(cfg)
		selStr := rapid.SampledFrom(allSels).Draw(t, "unsetSel")
		sel, err := Parse(selStr)
		if err != nil {
			t.Fatalf("Parse(%q): %v", selStr, err)
		}

		// First unset.
		if err := UnsetField(cfg, sel); err != nil {
			t.Fatalf("UnsetField(%q) first: %v", selStr, err)
		}
		after1 := snapshotConfig(cfg)

		// Second unset.
		if err := UnsetField(cfg, sel); err != nil {
			t.Fatalf("UnsetField(%q) second: %v", selStr, err)
		}
		after2 := snapshotConfig(cfg)

		if after1 != after2 {
			t.Fatalf("UnsetField(%q) not idempotent", selStr)
		}
	})
}

// TestListSubsetOfShow_Property verifies that every List entry appears
// in Show with source File.
//
// **Property 7: List output is subset of show output**
// **Validates: Requirements 8.1, 9.1**
func TestListSubsetOfShow_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cfg := DefaultConfig()

		// Set some random fields.
		nSets := rapid.IntRange(0, 8).Draw(t, "nSets")
		for i := 0; i < nSets; i++ {
			selStr, value := genValidSelectorAndValue(t)
			sel, err := Parse(selStr)
			if err != nil {
				t.Fatalf("Parse(%q): %v", selStr, err)
			}
			_ = Set(cfg, sel, value)
		}

		listEntries := List(cfg)
		showEntries := Show(cfg)

		// Build a set of (selector, value) from Show with source File.
		type key struct {
			sel string
			val string
		}
		showSet := make(map[key]bool)
		for _, e := range showEntries {
			if e.Source == File {
				showSet[key{e.Selector, e.Value}] = true
			}
		}

		for _, e := range listEntries {
			if e.Source != File {
				t.Fatalf("List entry %q has source %v, want File", e.Selector, e.Source)
			}
			if !showSet[key{e.Selector, e.Value}] {
				t.Fatalf("List entry (%q, %q) not found in Show output", e.Selector, e.Value)
			}
		}
	})
}

// TestSetIsolation_Property verifies that Set modifies only the targeted field.
//
// **Property 8: Set isolation**
// **Validates: Requirements 6.2**
func TestSetIsolation_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cfg := DefaultConfig()

		// Set some initial fields.
		nSets := rapid.IntRange(0, 5).Draw(t, "nInitSets")
		for i := 0; i < nSets; i++ {
			selStr, value := genValidSelectorAndValue(t)
			sel, err := Parse(selStr)
			if err != nil {
				t.Fatalf("Parse(%q): %v", selStr, err)
			}
			_ = Set(cfg, sel, value)
		}

		// Snapshot before.
		beforeEntries := Show(cfg)

		// Pick a random valid (selector, value) to set.
		selStr, value := genValidSelectorAndValue(t)
		sel, err := Parse(selStr)
		if err != nil {
			t.Fatalf("Parse(%q): %v", selStr, err)
		}

		if err := Set(cfg, sel, value); err != nil {
			t.Fatalf("Set(%q, %q): %v", selStr, value, err)
		}

		// Snapshot after.
		afterEntries := Show(cfg)

		// Build maps for comparison.
		beforeMap := make(map[string]Entry)
		for _, e := range beforeEntries {
			beforeMap[e.Selector] = e
		}
		afterMap := make(map[string]Entry)
		for _, e := range afterEntries {
			afterMap[e.Selector] = e
		}

		// Check that only the targeted selector changed.
		for sel, afterEntry := range afterMap {
			if sel == selStr {
				continue // this is the one we changed
			}
			beforeEntry, existed := beforeMap[sel]
			if !existed {
				// New entry appeared that wasn't the target — this could be
				// a subsystem entry that was created. Check if it's part of
				// the same subsystem that was just created.
				if isRelatedNewEntry(selStr, sel) {
					continue
				}
				t.Fatalf("new entry %q appeared after Set(%q, %q)", sel, selStr, value)
			}
			if beforeEntry.Value != afterEntry.Value || beforeEntry.Source != afterEntry.Source {
				t.Fatalf("entry %q changed: before=(%q, %v), after=(%q, %v)",
					sel, beforeEntry.Value, beforeEntry.Source, afterEntry.Value, afterEntry.Source)
			}
		}
	})
}

// isRelatedNewEntry checks if a new entry appearing in Show is expected
// because it's part of a newly-created subsystem (when we set a subsystem
// field, the subsystem entry is created with default values for other fields,
// but those remain Unset and won't appear in Show with source File).
// Actually, Show only includes subsystem entries with source File, so new
// entries from subsystem creation shouldn't appear. This handles the share
// case where setting one share field creates the share entry, and the other
// field shows up as "disabled" in Show.
func isRelatedNewEntry(targetSel, newSel string) bool {
	// If both are share selectors for the same share ID, it's expected.
	targetParsed, err := Parse(targetSel)
	if err != nil {
		return false
	}
	newParsed, err := Parse(newSel)
	if err != nil {
		return false
	}
	if len(targetParsed.Segments) < 1 || len(newParsed.Segments) < 1 {
		return false
	}
	if targetParsed.Segments[0].Name == "shares" && newParsed.Segments[0].Name == "shares" {
		return targetParsed.Segments[0].IndexVal == newParsed.Segments[0].IndexVal
	}
	return false
}

// snapshotConfig creates a comparable snapshot of a Config's state.
// Uses the Show output as a proxy for full state comparison.
type configSnapshot struct {
	entries string
}

func snapshotConfig(cfg *Config) configSnapshot {
	entries := Show(cfg)
	var b string
	for _, e := range entries {
		b += e.Selector + "=" + e.Value + "(" + e.Source.String() + ")\n"
	}
	return configSnapshot{entries: b}
}

// --- Unit tests for mapping functions ---

func TestGet_UnknownNamespace(t *testing.T) {
	cfg := DefaultConfig()
	sel, _ := Parse("unknown.max_jobs")
	_, err := Get(cfg, sel)
	if err == nil {
		t.Fatal("expected error for unknown namespace")
	}
	if !contains(err.Error(), "unknown namespace") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGet_UnknownField(t *testing.T) {
	cfg := DefaultConfig()
	sel, _ := Parse("core.nonexistent")
	_, err := Get(cfg, sel)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !contains(err.Error(), "unknown field") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGet_ShareMissingIndex(t *testing.T) {
	cfg := DefaultConfig()
	// Manually construct a selector with shares but no index.
	sel := Selector{Segments: []Segment{{Name: "shares"}, {Name: "memory_cache"}}}
	_, err := Get(cfg, sel)
	if err == nil {
		t.Fatal("expected error for shares without index")
	}
}

func TestGet_SubsystemPrecedence(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CoreConfig.MaxJobs.SetFile(10)

	// Without subsystem override, should return core value.
	sel, _ := Parse("drive.max_jobs")
	got, err := Get(cfg, sel)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "10" {
		t.Fatalf("got %q, want %q", got, "10")
	}

	// With subsystem override, should return subsystem value.
	cfg.Subsystems["drive"] = &CoreConfig{
		MaxJobs:    NewParam(api.DefaultMaxWorkers()),
		Account:    NewParam("default"),
		AppVersion: NewParam(""),
	}
	cfg.Subsystems["drive"].MaxJobs.SetFile(4)

	got, err = Get(cfg, sel)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "4" {
		t.Fatalf("got %q, want %q", got, "4")
	}
}

func TestSet_CreatesSubsystemEntry(t *testing.T) {
	cfg := DefaultConfig()
	sel, _ := Parse("drive.max_jobs")
	if err := Set(cfg, sel, "8"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	sub, ok := cfg.Subsystems["drive"]
	if !ok {
		t.Fatal("expected subsystem entry to be created")
	}
	if sub.MaxJobs.Value() != 8 {
		t.Fatalf("MaxJobs: got %d, want 8", sub.MaxJobs.Value())
	}
}

func TestUnsetField_RemovesEmptySubsystem(t *testing.T) {
	cfg := DefaultConfig()
	sel, _ := Parse("drive.max_jobs")
	_ = Set(cfg, sel, "8")

	if err := UnsetField(cfg, sel); err != nil {
		t.Fatalf("UnsetField: %v", err)
	}
	if _, ok := cfg.Subsystems["drive"]; ok {
		t.Fatal("expected subsystem entry to be removed after unset")
	}
}

func TestUnsetField_RemovesEmptyShare(t *testing.T) {
	cfg := DefaultConfig()
	sel, _ := Parse("shares[id=abc123].memory_cache")
	_ = Set(cfg, sel, "metadata")

	if err := UnsetField(cfg, sel); err != nil {
		t.Fatalf("UnsetField: %v", err)
	}
	if _, ok := cfg.Shares["abc123"]; ok {
		t.Fatal("expected share entry to be removed after unset")
	}
}

func TestUnsetField_KeepsShareWithOtherField(t *testing.T) {
	cfg := DefaultConfig()
	sel1, _ := Parse("shares[id=abc123].memory_cache")
	sel2, _ := Parse("shares[id=abc123].disk_cache")
	_ = Set(cfg, sel1, "metadata")
	_ = Set(cfg, sel2, "objectstore")

	if err := UnsetField(cfg, sel1); err != nil {
		t.Fatalf("UnsetField: %v", err)
	}
	sc, ok := cfg.Shares["abc123"]
	if !ok {
		t.Fatal("expected share entry to remain (disk_cache still set)")
	}
	if sc.MemoryCache != api.CacheDisabled {
		t.Fatalf("memory_cache should be disabled, got %v", sc.MemoryCache)
	}
	if sc.DiskCache != api.DiskCacheObjectStore {
		t.Fatalf("disk_cache should be objectstore, got %v", sc.DiskCache)
	}
}

func TestList_OnlyFileSourced(t *testing.T) {
	cfg := DefaultConfig()
	// Set one field.
	sel, _ := Parse("core.max_jobs")
	_ = Set(cfg, sel, "16")

	entries := List(cfg)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Selector != "core.max_jobs" || entries[0].Value != "16" {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
}

func TestShow_IncludesDefaults(t *testing.T) {
	cfg := DefaultConfig()
	entries := Show(cfg)

	// Should include core fields with default source.
	found := false
	for _, e := range entries {
		if e.Selector == "core.max_jobs" {
			found = true
			if e.Source != Unset {
				t.Fatalf("expected source Unset for default, got %v", e.Source)
			}
		}
	}
	if !found {
		t.Fatal("expected core.max_jobs in Show output")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
