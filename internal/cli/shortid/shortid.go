// Package shortid provides short ID resolution and formatting utilities for
// CLI commands that display and resolve base64-encoded entity IDs.
package shortid

import (
	"fmt"
	"strings"
)

// DefaultLength is the minimum number of characters in a short ID prefix.
const DefaultLength = 8

// NotFoundError is returned when no ID in the set matches the prefix.
type NotFoundError struct {
	Prefix string
}

// Error returns a human-readable message indicating no ID matched.
func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no ID matching prefix %q", e.Prefix)
}

// AmbiguousError is returned when multiple IDs match the prefix.
type AmbiguousError struct {
	Prefix  string
	Matches []string // full IDs that matched
}

// Error returns a human-readable message listing all ambiguous matches.
func (e *AmbiguousError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "prefix %q is ambiguous, matches:", e.Prefix)
	for _, m := range e.Matches {
		fmt.Fprintf(&b, "\n  %s", m)
	}
	return b.String()
}

// StripPadding removes trailing '=' padding characters from an ID string.
func StripPadding(id string) string {
	return strings.TrimRight(id, "=")
}

// Resolve returns the unique full ID from ids whose padding-stripped form
// starts with prefix. If prefix contains '=' it is treated as a full ID and
// returned directly after confirming membership in ids.
func Resolve(ids []string, prefix string) (string, error) {
	if strings.Contains(prefix, "=") {
		for _, id := range ids {
			if id == prefix {
				return id, nil
			}
		}
		return "", &NotFoundError{Prefix: prefix}
	}

	var matches []string
	for _, id := range ids {
		if strings.HasPrefix(StripPadding(id), prefix) {
			matches = append(matches, id)
		}
	}

	switch len(matches) {
	case 0:
		return "", &NotFoundError{Prefix: prefix}
	case 1:
		return matches[0], nil
	default:
		return "", &AmbiguousError{Prefix: prefix, Matches: matches}
	}
}

// FormatShortIDs returns a map from full ID to its shortest unique display
// prefix. The minimum prefix length is DefaultLength (8). Padding ('=') is
// stripped from display prefixes. Empty input returns an empty map.
func FormatShortIDs(ids []string) map[string]string {
	if len(ids) == 0 {
		return map[string]string{}
	}

	stripped := make([]string, len(ids))
	for i, id := range ids {
		stripped[i] = StripPadding(id)
	}

	result := make(map[string]string, len(ids))
	for i, full := range ids {
		s := stripped[i]
		n := DefaultLength
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
