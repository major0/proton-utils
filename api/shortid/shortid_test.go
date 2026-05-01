package shortid

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// genBase64URL generates a random base64url string of the given length.
func genBase64URL(t *rapid.T, label string, minLen, maxLen int) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"
	n := rapid.IntRange(minLen, maxLen).Draw(t, label+"Len")
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rapid.IntRange(0, len(chars)-1).Draw(t, label+"Char")]
	}
	return string(b)
}

// genFullID generates a random full Proton ID (base64url + '=' padding).
func genFullID(t *rapid.T, label string) string {
	body := genBase64URL(t, label, 12, 64)
	pad := rapid.IntRange(0, 2).Draw(t, label+"Pad")
	return body + strings.Repeat("=", pad)
}

// genDistinctIDs generates a set of distinct full IDs.
func genDistinctIDs(t *rapid.T, minN, maxN int) []string {
	n := rapid.IntRange(minN, maxN).Draw(t, "numIDs")
	seen := make(map[string]bool, n)
	ids := make([]string, 0, n)
	for len(ids) < n {
		id := genFullID(t, "id")
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// --- Property Tests ---

// TestFormatResolveRoundTrip_Property verifies that for any set of
// distinct full IDs, Format produces short IDs that Resolve maps back
// to the original full IDs.
//
// Feature: short-id, Property 1: Format→Resolve Round-Trip
func TestFormatResolveRoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ids := genDistinctIDs(t, 1, 50)
		short := Format(ids)

		for _, fullID := range ids {
			s, ok := short[fullID]
			if !ok {
				t.Fatalf("Format missing key for %q", fullID)
			}
			resolved, err := Resolve(ids, s)
			if err != nil {
				t.Fatalf("Resolve(%q) error: %v", s, err)
			}
			if resolved != fullID {
				t.Fatalf("round-trip failed: Format(%q)=%q, Resolve(%q)=%q",
					fullID, s, s, resolved)
			}
		}
	})
}

// TestFormatOutputInvariants_Property verifies that Format output has
// unique short IDs, all >= 8 chars, none containing '='.
//
// Feature: short-id, Property 2: Format Output Invariants
func TestFormatOutputInvariants_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ids := genDistinctIDs(t, 1, 50)
		short := Format(ids)

		// All short IDs must be unique.
		seen := make(map[string]string, len(short))
		for fullID, s := range short {
			if prev, dup := seen[s]; dup {
				t.Fatalf("duplicate short ID %q for %q and %q", s, prev, fullID)
			}
			seen[s] = fullID
		}

		for fullID, s := range short {
			// Minimum length.
			stripped := strip(fullID)
			wantMin := DefaultLength
			if len(stripped) < wantMin {
				wantMin = len(stripped)
			}
			if len(s) < wantMin {
				t.Fatalf("short ID %q for %q is %d chars, want >= %d",
					s, fullID, len(s), wantMin)
			}

			// No padding.
			if strings.Contains(s, "=") {
				t.Fatalf("short ID %q contains '='", s)
			}
		}
	})
}

// TestResolveNotFound_Property verifies that Resolve returns NotFoundError
// for prefixes that don't match any ID in the set.
//
// Feature: short-id, Property 3: Resolve Not-Found
func TestResolveNotFound_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ids := genDistinctIDs(t, 1, 20)

		// Generate a prefix that doesn't match any ID.
		prefix := genBase64URL(t, "prefix", 8, 16)
		matches := false
		for _, id := range ids {
			if strings.HasPrefix(strip(id), prefix) {
				matches = true
				break
			}
		}
		if matches {
			return // skip — this prefix happens to match
		}

		_, err := Resolve(ids, prefix)
		if err == nil {
			t.Fatalf("expected NotFoundError for prefix %q, got nil", prefix)
		}
		var nf *NotFoundError
		if !errors.As(err, &nf) {
			t.Fatalf("expected NotFoundError, got %T: %v", err, err)
		}
		if nf.Prefix != prefix {
			t.Fatalf("NotFoundError.Prefix = %q, want %q", nf.Prefix, prefix)
		}
	})
}

// TestResolveAmbiguous_Property verifies that Resolve returns
// AmbiguousError when multiple IDs share a common prefix.
//
// Feature: short-id, Property 4: Resolve Ambiguous
func TestResolveAmbiguous_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a shared prefix and two IDs that start with it.
		shared := genBase64URL(t, "shared", 4, 8)
		suffix1 := genBase64URL(t, "suf1", 8, 32)
		suffix2 := genBase64URL(t, "suf2", 8, 32)
		id1 := shared + suffix1 + "="
		id2 := shared + suffix2 + "=="
		if id1 == id2 {
			return // skip degenerate case
		}

		ids := []string{id1, id2}
		_, err := Resolve(ids, shared)
		if err == nil {
			t.Fatalf("expected AmbiguousError for prefix %q, got nil", shared)
		}
		var amb *AmbiguousError
		if !errors.As(err, &amb) {
			t.Fatalf("expected AmbiguousError, got %T: %v", err, err)
		}
		if amb.Prefix != shared {
			t.Fatalf("AmbiguousError.Prefix = %q, want %q", amb.Prefix, shared)
		}
		sort.Strings(amb.Matches)
		sort.Strings(ids)
		if len(amb.Matches) != len(ids) {
			t.Fatalf("AmbiguousError.Matches = %v, want %v", amb.Matches, ids)
		}
		for i := range ids {
			if amb.Matches[i] != ids[i] {
				t.Fatalf("AmbiguousError.Matches[%d] = %q, want %q",
					i, amb.Matches[i], ids[i])
			}
		}
	})
}

// --- Unit Tests ---

// TestResolve_FullIDPassthrough verifies that a full ID with '='
// padding is returned directly without prefix matching.
func TestResolve_FullIDPassthrough(t *testing.T) {
	ids := []string{"abc123def456==", "xyz789ghi012="}
	got, err := Resolve(ids, "abc123def456==")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc123def456==" {
		t.Fatalf("got %q, want %q", got, "abc123def456==")
	}
}

// TestResolve_FullIDNotInSet verifies that a full ID not in the set
// returns NotFoundError.
func TestResolve_FullIDNotInSet(t *testing.T) {
	ids := []string{"abc123def456=="}
	_, err := Resolve(ids, "notinset==")
	if err == nil {
		t.Fatal("expected error")
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("expected NotFoundError, got %T", err)
	}
}

// TestResolve_SingleCharPrefix verifies that a 1-character prefix
// resolves when unique.
func TestResolve_SingleCharPrefix(t *testing.T) {
	ids := []string{"Abc123==", "Xyz789=="}
	got, err := Resolve(ids, "A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Abc123==" {
		t.Fatalf("got %q, want %q", got, "Abc123==")
	}
}

// TestResolve_EmptyPrefix verifies that an empty prefix matches all
// IDs (ambiguous for >1, unique for exactly 1).
func TestResolve_EmptyPrefix(t *testing.T) {
	// Multiple IDs → ambiguous.
	ids := []string{"abc==", "xyz=="}
	_, err := Resolve(ids, "")
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("expected AmbiguousError for empty prefix with 2 IDs, got %v", err)
	}

	// Single ID → resolves.
	got, err := Resolve([]string{"abc=="}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc==" {
		t.Fatalf("got %q, want %q", got, "abc==")
	}
}

// TestFormat_EmptySet verifies that Format returns an empty map for
// an empty ID set.
func TestFormat_EmptySet(t *testing.T) {
	got := Format(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
	got = Format([]string{})
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

// TestFormat_SingleID verifies that a single ID gets an 8-char prefix.
func TestFormat_SingleID(t *testing.T) {
	ids := []string{"ABCDEFGHIJKLMNOPqrstuvwxyz0123456789=="}
	got := Format(ids)
	s := got[ids[0]]
	if len(s) != DefaultLength {
		t.Fatalf("short ID length = %d, want %d", len(s), DefaultLength)
	}
	if s != "ABCDEFGH" {
		t.Fatalf("short ID = %q, want %q", s, "ABCDEFGH")
	}
}

// TestFormat_AllUnique8Char verifies that when all 8-char prefixes are
// unique, all short IDs are exactly 8 characters.
func TestFormat_AllUnique8Char(t *testing.T) {
	ids := []string{
		"AAAAAAAA_rest1==",
		"BBBBBBBB_rest2==",
		"CCCCCCCC_rest3==",
	}
	got := Format(ids)
	for _, id := range ids {
		s := got[id]
		if len(s) != DefaultLength {
			t.Fatalf("short ID for %q = %q (len %d), want len %d",
				id, s, len(s), DefaultLength)
		}
	}
}

// TestFormat_CollisionExtends verifies that colliding 8-char prefixes
// are extended until unique.
func TestFormat_CollisionExtends(t *testing.T) {
	ids := []string{
		"AAAAAAAA_BBB_rest1==",
		"AAAAAAAA_CCC_rest2==",
	}
	got := Format(ids)
	s1 := got[ids[0]]
	s2 := got[ids[1]]
	if s1 == s2 {
		t.Fatalf("short IDs should differ: %q vs %q", s1, s2)
	}
	if len(s1) <= DefaultLength {
		t.Fatalf("expected extended prefix, got len %d", len(s1))
	}
}

// TestFormat_ShortFullID verifies that an ID shorter than DefaultLength
// uses its full stripped length.
func TestFormat_ShortFullID(t *testing.T) {
	ids := []string{"ABCD="}
	got := Format(ids)
	s := got[ids[0]]
	if s != "ABCD" {
		t.Fatalf("short ID = %q, want %q", s, "ABCD")
	}
}

// TestFormat_DuplicateIDs verifies that when the same ID appears
// multiple times (e.g. . and .. at a share root), Format still
// produces a short prefix — duplicates of the same ID are not treated
// as collisions.
func TestFormat_DuplicateIDs(t *testing.T) {
	id := "pan7_dtq6cYKw_i5rC9Zq03R4Iue52a_sL7yXD3vrF1-CVTt1QLnVR8RyPdXZmYWw1fM7zrkiyd14Asxdn8Pkw=="
	child := "x8bSGA7ZabcdefghijklmnopqrstuvwxyzABCDEFGH=="

	// Share root listing: . and .. both have the same LinkID.
	ids := []string{id, id, child}
	got := Format(ids)

	shortRoot := got[id]
	shortChild := got[child]

	// The root ID should be shortened, not the full stripped form.
	stripped := strip(id)
	if shortRoot == stripped {
		t.Fatalf("duplicate ID produced full-length prefix %q; want shortened", shortRoot)
	}
	if len(shortRoot) < DefaultLength {
		t.Fatalf("short ID %q is shorter than minimum %d", shortRoot, DefaultLength)
	}

	// The child should also be shortened.
	if len(shortChild) < DefaultLength {
		t.Fatalf("child short ID %q is shorter than minimum %d", shortChild, DefaultLength)
	}

	// Both should be distinct from each other.
	if shortRoot == shortChild {
		t.Fatalf("root and child short IDs should differ: both %q", shortRoot)
	}
}
