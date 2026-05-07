package lumoCmd

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// Feature: lumo-chat-cp-dest, Property 1: URI normalization is idempotent on lumo:// URIs and prepends lumo:/// to bare strings
//
// **Validates: Requirements 1.2, 1.3**
func TestPropertyURINormalization(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.String().Draw(t, "input")

		result := normalizeArg(s)

		if strings.HasPrefix(s, "lumo://") {
			// lumo:// URIs pass through unchanged.
			if result != s {
				t.Fatalf("normalizeArg(%q) = %q; want unchanged %q", s, result, s)
			}
		} else {
			// Bare strings get lumo:/// prepended.
			expected := "lumo:///" + s
			if result != expected {
				t.Fatalf("normalizeArg(%q) = %q; want %q", s, result, expected)
			}
		}

		// Idempotence: normalizing a normalized URI is a no-op.
		if normalizeArg(result) != result {
			t.Fatalf("normalizeArg is not idempotent: normalizeArg(%q) = %q", result, normalizeArg(result))
		}
	})
}

// Feature: lumo-chat-cp-dest, Property 2: URI parsing round-trip
//
// **Validates: Requirements 2.1, 2.2, 2.3**
func TestPropertyURIParsingRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate space without "/" and without "lumo://" substring.
		space := rapid.StringMatching(`[a-zA-Z0-9_\-]{0,20}`).Draw(t, "space")
		path := rapid.StringMatching(`[a-zA-Z0-9_\- ]{0,30}`).Draw(t, "path")

		// Construct a URI with explicit slash separator.
		uri := "lumo://" + space + "/" + path
		parsed, err := parseLumoURI(uri)
		if err != nil {
			t.Fatalf("parseLumoURI(%q) error: %v", uri, err)
		}

		if parsed.Space != space {
			t.Fatalf("parseLumoURI(%q).Space = %q; want %q", uri, parsed.Space, space)
		}
		if parsed.Path != path {
			t.Fatalf("parseLumoURI(%q).Path = %q; want %q", uri, parsed.Path, path)
		}

		// Verify that "lumo://<space>" (no trailing slash) parses with empty Path,
		// equivalent to "lumo://<space>/".
		uriNoSlash := "lumo://" + space
		parsedNoSlash, err := parseLumoURI(uriNoSlash)
		if err != nil {
			t.Fatalf("parseLumoURI(%q) error: %v", uriNoSlash, err)
		}

		uriWithSlash := "lumo://" + space + "/"
		parsedWithSlash, err := parseLumoURI(uriWithSlash)
		if err != nil {
			t.Fatalf("parseLumoURI(%q) error: %v", uriWithSlash, err)
		}

		if parsedNoSlash.Space != parsedWithSlash.Space {
			t.Fatalf("Space mismatch: %q (no slash) vs %q (with slash)", parsedNoSlash.Space, parsedWithSlash.Space)
		}
		if parsedNoSlash.Path != parsedWithSlash.Path {
			t.Fatalf("Path mismatch: %q (no slash) vs %q (with slash)", parsedNoSlash.Path, parsedWithSlash.Path)
		}

		// Both should have empty Path.
		if parsedNoSlash.Path != "" {
			t.Fatalf("parseLumoURI(%q).Path = %q; want empty", uriNoSlash, parsedNoSlash.Path)
		}
	})
}

// TestParseLumoURIRejectsInvalid verifies that parseLumoURI returns an error
// for URIs that don't start with "lumo://".
func TestParseLumoURIRejectsInvalid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate strings that do NOT start with "lumo://".
		s := rapid.String().Filter(func(s string) bool {
			return !strings.HasPrefix(s, "lumo://")
		}).Draw(t, "invalid_uri")

		_, err := parseLumoURI(s)
		if err == nil {
			t.Fatalf("parseLumoURI(%q) should return error for non-lumo:// URI", s)
		}
	})
}
