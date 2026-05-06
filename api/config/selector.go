package config

import (
	"fmt"
	"strings"
)

// Segment is one component of a parsed selector path.
type Segment struct {
	Name     string // identifier: "core", "max_jobs", "share"
	IndexKey string // map index key: "name", "id" (empty if no index)
	IndexVal string // map index value: "MyShare", "abc123" (empty if no index)
}

// Selector is a parsed selector path.
type Selector struct {
	Segments []Segment
}

// String prints the selector back to its canonical string form.
// Plain segments are printed as their name; indexed segments as name[key=value].
// Segments are joined by '.'.
func (s Selector) String() string {
	var b strings.Builder
	for i, seg := range s.Segments {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(seg.Name)
		if seg.IndexKey != "" {
			b.WriteByte('[')
			b.WriteString(seg.IndexKey)
			b.WriteByte('=')
			b.WriteString(seg.IndexVal)
			b.WriteByte(']')
		}
	}
	return b.String()
}

// Parse parses a selector string into a Selector. The grammar is:
//
//	selector   := segment ('.' segment)*
//	segment    := identifier ('[' identifier '=' value ']')?
//	identifier := [a-zA-Z_][a-zA-Z0-9_-]*
//	value      := [^\]]+
//
// Parse returns descriptive errors for invalid input.
func Parse(input string) (Selector, error) {
	if input == "" {
		return Selector{}, fmt.Errorf("selector: empty input")
	}

	var segments []Segment
	pos := 0

	for {
		// Parse identifier.
		start := pos
		if pos >= len(input) || !isIdentStart(input[pos]) {
			return Selector{}, fmt.Errorf("selector: empty identifier at position %d", pos)
		}
		pos++
		for pos < len(input) && isIdentCont(input[pos]) {
			pos++
		}
		name := input[start:pos]

		seg := Segment{Name: name}

		// Check for optional index: '[' identifier '=' value ']'
		if pos < len(input) && input[pos] == '[' {
			bracketStart := pos
			pos++ // skip '['

			// Find closing ']'.
			closeIdx := strings.IndexByte(input[pos:], ']')
			if closeIdx < 0 {
				return Selector{}, fmt.Errorf("selector: unmatched '[' in segment %q", name)
			}
			inner := input[pos : pos+closeIdx]
			pos += closeIdx + 1 // skip past ']'

			// Split inner on '='.
			eqIdx := strings.IndexByte(inner, '=')
			if eqIdx < 0 {
				raw := input[bracketStart:pos]
				return Selector{}, fmt.Errorf("selector: missing '=' in index at segment %q", name+raw)
			}

			key := inner[:eqIdx]
			val := inner[eqIdx+1:]

			if val == "" {
				raw := input[bracketStart:pos]
				return Selector{}, fmt.Errorf("selector: empty value in index at segment %q", name+raw)
			}

			seg.IndexKey = key
			seg.IndexVal = val
		}

		segments = append(segments, seg)

		// Check for separator or end.
		if pos >= len(input) {
			break
		}
		if input[pos] == '.' {
			pos++ // skip '.'
			continue
		}
		// Unexpected character — shouldn't happen with valid input,
		// but handle gracefully.
		return Selector{}, fmt.Errorf("selector: unexpected character %q at position %d", input[pos], pos)
	}

	return Selector{Segments: segments}, nil
}

// isIdentStart returns true if c is a valid first character of an identifier.
func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

// isIdentCont returns true if c is a valid continuation character of an identifier.
func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '-'
}
