package driveCmd

import (
	"strings"
	"testing"
)

func TestFormatBytesEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input int64
		want  string
	}{
		{"zero", 0, "0 B"},
		{"one byte", 1, "1 B"},
		{"max below KiB", 1023, "1023 B"},
		{"exact KiB boundary", 1024, "1.0 KiB"},
		{"just above KiB", 1025, "1.0 KiB"},
		{"1.5 KiB", 1536, "1.5 KiB"},
		{"max below MiB", 1<<20 - 1, "1024.0 KiB"},
		{"exact MiB boundary", 1 << 20, "1.0 MiB"},
		{"1.5 MiB", 3 * (1 << 19), "1.5 MiB"},
		{"max below GiB", 1<<30 - 1, "1024.0 MiB"},
		{"exact GiB boundary", 1 << 30, "1.0 GiB"},
		{"1.5 GiB", 3 * (1 << 29), "1.5 GiB"},
		{"large GiB", 10 * (1 << 30), "10.0 GiB"},
		{"negative value", -1, "-1 B"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatBytes(tt.input)
			if got != tt.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatBytesUnits(t *testing.T) {
	// Verify each unit suffix appears at the right threshold.
	tests := []struct {
		input      int64
		wantSuffix string
	}{
		{0, " B"},
		{512, " B"},
		{1024, " KiB"},
		{1 << 20, " MiB"},
		{1 << 30, " GiB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.input)
		if !strings.HasSuffix(got, tt.wantSuffix) {
			t.Errorf("formatBytes(%d) = %q, want suffix %q", tt.input, got, tt.wantSuffix)
		}
	}
}
