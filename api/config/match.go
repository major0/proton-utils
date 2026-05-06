package config

import (
	"path/filepath"
	"strings"
)

// MatchPrefix returns true if selector starts with prefix at a segment boundary.
// The prefix must match one or more complete segments (up to a '.' or end of string).
// Examples:
//
//	MatchPrefix("core.max_jobs", "core")           → true
//	MatchPrefix("core.max_jobs", "core.max_jobs")  → true (exact match)
//	MatchPrefix("core.max_jobs", "core.max")       → false (partial segment)
//	MatchPrefix("share[id=abc].memory_cache", "share[id=abc]") → true
func MatchPrefix(selector, prefix string) bool {
	if prefix == "" {
		return true
	}
	if !strings.HasPrefix(selector, prefix) {
		return false
	}
	if len(selector) == len(prefix) {
		return true
	}
	return selector[len(prefix)] == '.'
}

// MatchPattern returns true if the selector matches the given pattern.
// If the pattern contains glob metacharacters (* or ?), it uses filepath.Match
// for glob matching against the full selector string.
// Otherwise, it uses MatchPrefix for complete-segment prefix matching.
//
// Brackets in the pattern are escaped before glob matching because our selector
// syntax uses [...] for map indices, not as glob character classes.
func MatchPattern(selector, pattern string) bool {
	if strings.ContainsAny(pattern, "*?") {
		matched, _ := filepath.Match(escapeBrackets(pattern), selector)
		return matched
	}
	return MatchPrefix(selector, pattern)
}

// escapeBrackets escapes '[' and ']' characters in a glob pattern so that
// filepath.Match treats them as literals. Our selector syntax uses brackets
// for map indices (e.g., shares[id=abc]), not as glob character classes.
func escapeBrackets(pattern string) string {
	var b strings.Builder
	b.Grow(len(pattern) + 4)
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '[':
			b.WriteString(`\[`)
		case ']':
			b.WriteString(`\]`)
		default:
			b.WriteByte(pattern[i])
		}
	}
	return b.String()
}
