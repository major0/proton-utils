package lumoCmd

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// TestFormatSpaceList_Property verifies that for any non-empty slice of
// SpaceRow values, FormatSpaceList output contains each row's ID,
// CreateTime, and Name, and rows are sorted by CreateTime descending.
//
// Feature: lumo-space, Property 1: Space list output contains all fields and is sorted
//
// **Validates: Requirements 2.1, 2.2**
func TestFormatSpaceList_Property(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(rt, "num_rows")
		rows := make([]SpaceRow, n)
		for i := range rows {
			rows[i] = SpaceRow{
				ID:         rapid.StringMatching(`[a-zA-Z0-9]{4,16}`).Draw(rt, "id"),
				CreateTime: rapid.StringMatching(`2024-0[1-9]-[0][1-9]T[01][0-9]:[0-5][0-9]:[0-5][0-9]Z`).Draw(rt, "create_time"),
				ConvCount:  rapid.IntRange(0, 100).Draw(rt, "conv_count"),
				Name:       rapid.StringMatching(`[a-zA-Z0-9 ]{1,16}`).Draw(rt, "name"),
			}
		}

		output := FormatSpaceList(rows)

		// Every row's ID and Name must appear in the output.
		// CreateTime is formatted to local time, so check the formatted form.
		for _, r := range rows {
			if !strings.Contains(output, r.ID) {
				rt.Fatalf("output missing ID %q", r.ID)
			}
			formatted := fmtLocalTime(r.CreateTime)
			if !strings.Contains(output, formatted) {
				rt.Fatalf("output missing formatted CreateTime %q (from %q)", formatted, r.CreateTime)
			}
			if !strings.Contains(output, r.Name) {
				rt.Fatalf("output missing Name %q", r.Name)
			}
		}

		// Verify rows are sorted by CreateTime descending.
		lines := strings.Split(strings.TrimSpace(output), "\n")
		// First line is the header.
		dataLines := lines[1:]
		if len(dataLines) != n {
			rt.Fatalf("got %d data lines, want %d", len(dataLines), n)
		}

		// Extract CreateTime from each data line. The ID column width
		// is dynamic — compute it from the longest ID in the set.
		maxIDLen := 2
		for _, r := range rows {
			if len(r.ID) > maxIDLen {
				maxIDLen = len(r.ID)
			}
		}
		idOffset := maxIDLen + 2 // ID column + 2 spaces

		// Extract formatted CreateTime from each data line.
		// The ID column width is dynamic, followed by 2 spaces,
		// then the 19-char formatted timestamp.
		var times []string
		for _, line := range dataLines {
			if len(line) < idOffset+19 {
				rt.Fatalf("line too short: %q", line)
			}
			ct := strings.TrimSpace(line[idOffset : idOffset+19])
			times = append(times, ct)
		}
		for i := 1; i < len(times); i++ {
			if times[i] > times[i-1] {
				rt.Fatalf("rows not sorted descending: %q > %q at index %d", times[i], times[i-1], i)
			}
		}
	})
}

// TestFormatSpaceList_Empty verifies that empty input returns the
// expected sentinel message.
func TestFormatSpaceList_Empty(t *testing.T) {
	got := FormatSpaceList(nil)
	want := "No spaces found.\n"
	if got != want {
		t.Fatalf("FormatSpaceList(nil) = %q, want %q", got, want)
	}

	got = FormatSpaceList([]SpaceRow{})
	if got != want {
		t.Fatalf("FormatSpaceList([]) = %q, want %q", got, want)
	}
}
