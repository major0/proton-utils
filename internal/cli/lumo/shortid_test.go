package lumoCmd

import (
	"strings"
	"testing"

	cli "github.com/major0/proton-cli/internal/cli"
)

// TestFormatConversationList_ShortIDs verifies that short IDs produce
// a narrower ID column than full IDs.
func TestFormatConversationList_ShortIDs(t *testing.T) {
	rows := []ConversationRow{
		{ID: "abc12345", Title: "Chat 1", CreateTime: "2024-06-15T10:30:00Z"},
		{ID: "def67890", Title: "Chat 2", CreateTime: "2024-06-14T09:00:00Z"},
	}

	output := FormatConversationList(rows)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected header + data, got %d lines", len(lines))
	}

	// Header should have ID column width of 8 (short ID length).
	header := lines[0]
	idEnd := strings.Index(header, "CREATED")
	if idEnd < 0 {
		t.Fatal("header missing CREATED column")
	}
	// ID column + 2 spaces = idEnd. Short IDs are 8 chars → idEnd should be 10.
	if idEnd != 10 {
		t.Errorf("ID column end = %d, want 10 for 8-char short IDs", idEnd)
	}
}

// TestFormatConversationList_FullIDs verifies that full IDs produce a
// wider ID column.
func TestFormatConversationList_FullIDs(t *testing.T) {
	rows := []ConversationRow{
		{ID: "abc123def456ghi789jkl012mno345pqr678==", Title: "Chat 1", CreateTime: "2024-06-15T10:30:00Z"},
		{ID: "xyz987wvu654tsr321qpo098nml765kji432==", Title: "Chat 2", CreateTime: "2024-06-14T09:00:00Z"},
	}

	output := FormatConversationList(rows)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	header := lines[0]
	idEnd := strings.Index(header, "CREATED")
	// Full IDs are 38 chars → idEnd should be 40 (38 + 2 spaces).
	if idEnd != 40 {
		t.Errorf("ID column end = %d, want 40 for full IDs", idEnd)
	}
}

// TestFmtLocalTime_ValidISO verifies that a valid ISO timestamp is
// formatted to local time with space separator.
func TestFmtLocalTime_ValidISO(t *testing.T) {
	got := cli.FormatISO("2024-06-15T10:30:00Z")
	// Should be "YYYY-MM-DD HH:MM:SS" format (19 chars).
	if len(got) != 19 {
		t.Fatalf("FormatISO length = %d, want 19: %q", len(got), got)
	}
	if got[10] != ' ' {
		t.Errorf("expected space at position 10, got %q in %q", got[10], got)
	}
	// Should not contain 'T'.
	if strings.Contains(got, "T") {
		t.Errorf("formatted time should not contain 'T': %q", got)
	}
}

// TestFmtLocalTime_InvalidFallback verifies that an unparseable string
// is returned unchanged.
func TestFmtLocalTime_InvalidFallback(t *testing.T) {
	got := cli.FormatISO("not-a-date")
	if got != "not-a-date" {
		t.Fatalf("expected fallback to raw string, got %q", got)
	}
}
