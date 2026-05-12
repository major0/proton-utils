package driveCmd

import "testing"

func TestParsePreserve(t *testing.T) {
	tests := []struct {
		name           string
		preserve       string
		wantMode       bool
		wantTimestamps bool
	}{
		{"empty string", "", false, false},
		{"mode only", "mode", true, false},
		{"timestamps only", "timestamps", false, true},
		{"both comma-separated", "mode,timestamps", true, true},
		{"both reversed", "timestamps,mode", true, true},
		{"with spaces", " mode , timestamps ", true, true},
		{"unknown value ignored", "unknown", false, false},
		{"mode with unknown", "mode,unknown", true, false},
		{"timestamps with unknown", "unknown,timestamps", false, true},
		{"duplicate mode", "mode,mode", true, false},
		{"all three", "mode,timestamps,unknown", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := cpOptions{preserve: tt.preserve}
			pf := parsePreserve(opts)
			if pf.mode != tt.wantMode {
				t.Errorf("mode = %v, want %v", pf.mode, tt.wantMode)
			}
			if pf.timestamps != tt.wantTimestamps {
				t.Errorf("timestamps = %v, want %v", pf.timestamps, tt.wantTimestamps)
			}
		})
	}
}
