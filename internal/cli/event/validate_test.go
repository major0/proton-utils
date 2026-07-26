package eventCmd

import (
	"testing"
	"time"
)

func TestValidateWatchFlags(t *testing.T) {
	cases := []struct {
		name    string
		params  watchParams
		wantErr bool
	}{
		{"default ok", watchParams{interval: 5 * time.Second}, false},
		{"drive ok", watchParams{drive: true, interval: time.Second}, false},
		{"share ok", watchParams{share: "s1", interval: time.Second}, false},
		{"drive+share mutually exclusive", watchParams{drive: true, share: "s1", interval: time.Second}, true},
		{"interval below floor", watchParams{interval: 500 * time.Millisecond}, true},
		{"interval zero", watchParams{interval: 0}, true},
		{"core type ok in default mode", watchParams{interval: time.Second, types: []string{typeMessages}}, false},
		{"drive type ok in drive mode", watchParams{drive: true, interval: time.Second, types: []string{typeLinkCreate}}, false},
		{"unknown type rejected", watchParams{interval: time.Second, types: []string{"bogus"}}, true},
		{"core type in drive mode rejected", watchParams{drive: true, interval: time.Second, types: []string{typeMessages}}, true},
		{"drive type in core mode rejected", watchParams{interval: time.Second, types: []string{typeLinkCreate}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWatchFlags(tc.params)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateWatchFlags(%+v) error = %v, wantErr = %v", tc.params, err, tc.wantErr)
			}
		})
	}
}

// TestValidateDriveFrom validates Property 5: --from combined with --drive
// resolving to more than one volume is rejected; one volume is allowed.
//
// Validates: Requirements 4.2
func TestValidateDriveFrom(t *testing.T) {
	cases := []struct {
		name        string
		from        string
		volumeCount int
		wantErr     bool
	}{
		{"no from, many volumes", "", 3, false},
		{"from, single volume", "e100", 1, false},
		{"from, many volumes rejected", "e100", 2, true},
		{"no from, single volume", "", 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDriveFrom(tc.from, tc.volumeCount)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateDriveFrom(%q, %d) error = %v, wantErr = %v", tc.from, tc.volumeCount, err, tc.wantErr)
			}
		})
	}
}
