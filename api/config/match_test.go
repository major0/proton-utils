package config

import "testing"

func TestMatchPrefix(t *testing.T) {
	tests := []struct {
		name     string
		selector string
		prefix   string
		want     bool
	}{
		{"exact match", "core.max_jobs", "core.max_jobs", true},
		{"namespace prefix", "core.max_jobs", "core", true},
		{"partial segment rejected", "core.max_jobs", "core.max", false},
		{"map-indexed prefix", "share[id=abc].memory_cache", "share[id=abc]", true},
		{"different namespace", "drive.max_jobs", "core", false},
		{"prefix longer than selector", "core", "core.max_jobs", false},
		{"empty prefix matches all", "core.max_jobs", "", true},
		{"subsystem prefix", "drive.account", "drive", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchPrefix(tt.selector, tt.prefix)
			if got != tt.want {
				t.Errorf("MatchPrefix(%q, %q) = %v, want %v", tt.selector, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		name     string
		selector string
		pattern  string
		want     bool
	}{
		// Prefix mode (no glob chars)
		{"prefix mode exact", "core.max_jobs", "core.max_jobs", true},
		{"prefix mode namespace", "core.max_jobs", "core", true},
		{"prefix mode partial rejected", "core.max_jobs", "core.max", false},

		// Glob mode
		{"glob star matches any", "core.max_jobs", "*.max_jobs", true},
		{"glob star no match", "core.max_jobs", "*.account", false},
		{"glob question mark", "core.max_jobs", "?ore.max_jobs", true},
		{"glob shares wildcard", "share[id=abc].memory_cache", "share[*].memory_cache", true},
		{"glob shares no match", "share[id=abc].disk_cache", "share[*].memory_cache", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchPattern(tt.selector, tt.pattern)
			if got != tt.want {
				t.Errorf("MatchPattern(%q, %q) = %v, want %v", tt.selector, tt.pattern, got, tt.want)
			}
		})
	}
}
