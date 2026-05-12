package cli

import "time"

// FormatLocalTime formats a time.Time as "YYYY-MM-DD HH:MM:SS" in local timezone.
func FormatLocalTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04:05")
}

// FormatISO parses an ISO 8601 (RFC 3339) string and formats it as
// "YYYY-MM-DD HH:MM:SS" in local timezone. Returns the raw string
// unchanged if parsing fails.
func FormatISO(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// FormatEpoch converts a Unix epoch to "YYYY-MM-DD" in local timezone.
// Returns "-" for zero.
func FormatEpoch(epoch int64) string {
	if epoch == 0 {
		return "-"
	}
	return time.Unix(epoch, 0).Local().Format("2006-01-02")
}
