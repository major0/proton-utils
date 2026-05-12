package driveCmd

import "testing"

// TestDisplayID_WithShortIDs verifies that displayID returns the short
// ID when the map contains the key.
func TestDisplayID_WithShortIDs(t *testing.T) {
	opts := listOpts{
		shortIDs: map[string]string{
			"abc123def456==": "abc123de",
		},
	}
	got := displayID("abc123def456==", opts)
	if got != "abc123de" {
		t.Fatalf("displayID = %q, want %q", got, "abc123de")
	}
}

// TestDisplayID_WithoutShortIDs verifies that displayID returns the
// full ID when the map is nil.
func TestDisplayID_WithoutShortIDs(t *testing.T) {
	opts := listOpts{}
	got := displayID("abc123def456==", opts)
	if got != "abc123def456==" {
		t.Fatalf("displayID = %q, want %q", got, "abc123def456==")
	}
}

// TestDisplayID_MissingKey verifies that displayID returns the full ID
// when the key is not in the map.
func TestDisplayID_MissingKey(t *testing.T) {
	opts := listOpts{
		shortIDs: map[string]string{
			"other==": "otherxxx",
		},
	}
	got := displayID("abc123def456==", opts)
	if got != "abc123def456==" {
		t.Fatalf("displayID = %q, want %q", got, "abc123def456==")
	}
}
