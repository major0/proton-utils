package driveCmd

import (
	"fmt"
	"strings"
)

// shortIDDefaultLength is the minimum number of characters in a short ID.
const shortIDDefaultLength = 8

// shortIDNotFoundError is returned when no ID in the set matches the prefix.
type shortIDNotFoundError struct {
	Prefix string
}

func (e *shortIDNotFoundError) Error() string {
	return fmt.Sprintf("no ID matching prefix %q", e.Prefix)
}

// shortIDAmbiguousError is returned when multiple IDs match the prefix.
type shortIDAmbiguousError struct {
	Prefix  string
	Matches []string // full IDs that matched
}

func (e *shortIDAmbiguousError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "prefix %q is ambiguous, matches:", e.Prefix)
	for _, m := range e.Matches {
		fmt.Fprintf(&b, "\n  %s", m)
	}
	return b.String()
}

// stripPadding removes '=' padding from an ID.
func stripPadding(id string) string {
	return strings.TrimRight(id, "=")
}

// resolveShortID returns the unique full ID from ids whose padding-stripped
// form starts with prefix. If prefix contains '=' it is treated as a
// full ID and returned directly after confirming membership in ids.
func resolveShortID(ids []string, prefix string) (string, error) {
	if strings.Contains(prefix, "=") {
		for _, id := range ids {
			if id == prefix {
				return id, nil
			}
		}
		return "", &shortIDNotFoundError{Prefix: prefix}
	}

	var matches []string
	for _, id := range ids {
		if strings.HasPrefix(stripPadding(id), prefix) {
			matches = append(matches, id)
		}
	}

	switch len(matches) {
	case 0:
		return "", &shortIDNotFoundError{Prefix: prefix}
	case 1:
		return matches[0], nil
	default:
		return "", &shortIDAmbiguousError{Prefix: prefix, Matches: matches}
	}
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
