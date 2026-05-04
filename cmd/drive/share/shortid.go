package shareCmd

import "strings"

// shortIDDefaultLength is the minimum number of characters in a short ID.
const shortIDDefaultLength = 8

// stripPadding removes '=' padding from an ID.
func stripPadding(id string) string {
	return strings.TrimRight(id, "=")
}

// formatShortIDs returns a map from full ID to its shortest unique display
// prefix. The minimum prefix length is shortIDDefaultLength (8). Padding
// ('=') is stripped from display prefixes.
func formatShortIDs(ids []string) map[string]string {
	if len(ids) == 0 {
		return map[string]string{}
	}

	stripped := make([]string, len(ids))
	for i, id := range ids {
		stripped[i] = stripPadding(id)
	}

	result := make(map[string]string, len(ids))
	for i, full := range ids {
		s := stripped[i]
		n := shortIDDefaultLength
		if n > len(s) {
			n = len(s)
		}

		for n <= len(s) {
			prefix := s[:n]
			unique := true
			for j, other := range stripped {
				if j != i && other != s && strings.HasPrefix(other, prefix) {
					unique = false
					break
				}
			}
			if unique {
				break
			}
			n++
		}

		if n > len(s) {
			n = len(s)
		}
		result[full] = s[:n]
	}

	return result
}
