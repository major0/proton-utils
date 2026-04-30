// Package shortid provides git-style short ID prefix matching for
// Proton resource IDs. All functions are pure — no I/O, no API calls.
//
// Proton IDs are base64url-encoded strings with '=' padding. Short IDs
// are the first N characters of the padding-stripped form, used for
// display and prefix-based resolution.
package shortid

import (
	"fmt"
	"strings"
)

// DefaultLength is the minimum number of characters in a short ID.
const DefaultLength = 8

// NotFoundError is returned when no ID in the set matches the prefix.
type NotFoundError struct {
	Prefix string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no ID matching prefix %q", e.Prefix)
}

// AmbiguousError is returned when multiple IDs match the prefix.
type AmbiguousError struct {
	Prefix  string
	Matches []string // full IDs that matched
}

func (e *AmbiguousError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "prefix %q is ambiguous, matches:", e.Prefix)
	for _, m := range e.Matches {
		fmt.Fprintf(&b, "\n  %s", m)
	}
	return b.String()
}

// strip removes '=' padding from an ID.
func strip(id string) string {
	return strings.TrimRight(id, "=")
}

// Resolve returns the unique full ID from ids whose padding-stripped
// form starts with prefix. If prefix contains '=' it is treated as a
// full ID and returned directly after confirming membership in ids.
// Returns ErrNotFound if no ID matches, ErrAmbiguous if multiple match.
func Resolve(ids []string, prefix string) (string, error) {
	// Full ID passthrough: if the prefix contains '=' padding,
	// treat it as a full ID lookup.
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
		if strings.HasPrefix(strip(id), prefix) {
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

// Format returns a map from full ID to its shortest unique display
// prefix. The minimum prefix length is DefaultLength (8). Padding
// ('=') is stripped from display prefixes.
func Format(ids []string) map[string]string {
	if len(ids) == 0 {
		return map[string]string{}
	}

	// Strip padding once for all IDs.
	stripped := make([]string, len(ids))
	for i, id := range ids {
		stripped[i] = strip(id)
	}

	result := make(map[string]string, len(ids))
	for i, full := range ids {
		s := stripped[i]
		n := DefaultLength
		if n > len(s) {
			n = len(s)
		}

		// Extend until the prefix is unique among all stripped IDs.
		for n <= len(s) {
			prefix := s[:n]
			unique := true
			for j, other := range stripped {
				if j != i && strings.HasPrefix(other, prefix) {
					unique = false
					break
				}
			}
			if unique {
				break
			}
			n++
		}

		// Clamp to the stripped length.
		if n > len(s) {
			n = len(s)
		}
		result[full] = s[:n]
	}

	return result
}
