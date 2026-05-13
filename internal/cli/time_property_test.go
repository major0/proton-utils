package cli

import (
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// TestPropertyFormatLocalTimeFormat verifies that FormatLocalTime always
// produces a 19-character string with correct separator positions.
//
// **Validates: Requirements 3.1**
func TestPropertyFormatLocalTimeFormat(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate arbitrary time values within a reasonable range.
		epoch := rapid.Int64Range(-62135596800, 253402300799).Draw(t, "epoch")
		ts := time.Unix(epoch, 0)

		result := FormatLocalTime(ts)

		if len(result) != 19 {
			t.Fatalf("expected 19 chars, got %d: %q", len(result), result)
		}
		// Check separator positions: 4='-', 7='-', 10=' ', 13=':', 16=':'
		if result[4] != '-' {
			t.Fatalf("expected '-' at position 4, got %q in %q", result[4], result)
		}
		if result[7] != '-' {
			t.Fatalf("expected '-' at position 7, got %q in %q", result[7], result)
		}
		if result[10] != ' ' {
			t.Fatalf("expected ' ' at position 10, got %q in %q", result[10], result)
		}
		if result[13] != ':' {
			t.Fatalf("expected ':' at position 13, got %q in %q", result[13], result)
		}
		if result[16] != ':' {
			t.Fatalf("expected ':' at position 16, got %q in %q", result[16], result)
		}
		// Check digit positions.
		for _, i := range []int{0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18} {
			if result[i] < '0' || result[i] > '9' {
				t.Fatalf("expected digit at position %d, got %q in %q", i, result[i], result)
			}
		}
	})
}

// TestPropertyFormatISOValidInput verifies that FormatISO with valid RFC 3339
// input produces a 19-character string matching the YYYY-MM-DD HH:MM:SS pattern.
//
// **Validates: Requirements 3.2**
func TestPropertyFormatISOValidInput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate valid RFC 3339 timestamps.
		year := rapid.IntRange(1970, 2099).Draw(t, "year")
		month := rapid.IntRange(1, 12).Draw(t, "month")
		day := rapid.IntRange(1, 28).Draw(t, "day") // 28 avoids invalid dates
		hour := rapid.IntRange(0, 23).Draw(t, "hour")
		min := rapid.IntRange(0, 59).Draw(t, "min")
		sec := rapid.IntRange(0, 59).Draw(t, "sec")
		// Generate timezone offset.
		offsetHour := rapid.IntRange(-12, 14).Draw(t, "offsetHour")
		offsetMin := rapid.IntRange(0, 59).Draw(t, "offsetMin")

		var tz string
		if offsetHour == 0 && offsetMin == 0 {
			tz = "Z"
		} else {
			sign := "+"
			oh := offsetHour
			if oh < 0 {
				sign = "-"
				oh = -oh
			}
			tz = fmt.Sprintf("%s%02d:%02d", sign, oh, offsetMin)
		}

		iso := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d%s", year, month, day, hour, min, sec, tz)

		result := FormatISO(iso)

		if len(result) != 19 {
			t.Fatalf("expected 19 chars for input %q, got %d: %q", iso, len(result), result)
		}
		if result[4] != '-' || result[7] != '-' || result[10] != ' ' || result[13] != ':' || result[16] != ':' {
			t.Fatalf("separator mismatch in %q for input %q", result, iso)
		}
	})
}

// TestPropertyFormatISOInvalidInput verifies that FormatISO returns the input
// unchanged when it is not a valid RFC 3339 string.
//
// **Validates: Requirements 3.3**
func TestPropertyFormatISOInvalidInput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate strings that are definitely not valid RFC 3339.
		// Strategy: generate arbitrary strings that don't match the pattern.
		s := rapid.OneOf(
			rapid.StringMatching(`[a-z]{1,20}`),
			rapid.StringMatching(`\d{1,8}`),
			rapid.StringMatching(`[^T]+`),
			rapid.Just(""),
			rapid.Just("not-a-date"),
			rapid.Just("2024-13-01T00:00:00Z"), // invalid month
			rapid.Just("2024-01-32T00:00:00Z"), // invalid day
		).Draw(t, "invalid_iso")

		// Verify it's actually not parseable as RFC 3339.
		if _, err := time.Parse(time.RFC3339, s); err == nil {
			t.Skip("generated string happens to be valid RFC 3339")
		}

		result := FormatISO(s)
		if result != s {
			t.Fatalf("expected passthrough %q, got %q", s, result)
		}
	})
}

// TestPropertyFormatEpochFormat verifies that FormatEpoch produces a 10-char
// YYYY-MM-DD string for non-zero epochs and "-" for zero.
//
// **Validates: Requirements 3.4, 3.5**
func TestPropertyFormatEpochFormat(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		epoch := rapid.Int64Range(-62135596800, 253402300799).Draw(t, "epoch")

		result := FormatEpoch(epoch)

		if epoch == 0 {
			if result != "-" {
				t.Fatalf("expected %q for zero epoch, got %q", "-", result)
			}
			return
		}

		if len(result) != 10 {
			t.Fatalf("expected 10 chars for epoch %d, got %d: %q", epoch, len(result), result)
		}
		if result[4] != '-' || result[7] != '-' {
			t.Fatalf("separator mismatch in %q for epoch %d", result, epoch)
		}
		// Verify digits at expected positions.
		for _, i := range []int{0, 1, 2, 3, 5, 6, 8, 9} {
			if result[i] < '0' || result[i] > '9' {
				t.Fatalf("expected digit at position %d, got %q in %q", i, result[i], result)
			}
		}
	})
}
