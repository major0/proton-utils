package driveCmd

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// TestParseProtonURI_RoundTrip_Property verifies that parsing a valid proton://
// URI and reconstructing it produces the same parse result on re-parse.
//
// **Validates: Requirements 2.3**
func TestParseProtonURI_RoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate a valid share component (non-empty, no slashes, no dots-only).
		share := rapid.StringMatching(`[a-zA-Z0-9_-]{1,20}`).Draw(t, "share")

		// Generate 0-3 path segments (each non-empty, no slashes, no dots-only).
		numSegs := rapid.IntRange(0, 3).Draw(t, "numSegs")
		var segs []string
		for i := 0; i < numSegs; i++ {
			seg := rapid.StringMatching(`[a-zA-Z0-9_.-]{1,15}`).Draw(t, "seg")
			// Avoid "." and ".." which NormalizePath collapses.
			if seg == "." || seg == ".." {
				seg = "file"
			}
			segs = append(segs, seg)
		}

		// Construct URI: proton://<share>/<path>
		var uri string
		if len(segs) == 0 {
			uri = "proton://" + share
		} else {
			uri = "proton://" + share + "/" + strings.Join(segs, "/")
		}

		// First parse.
		s1, p1, err1 := parseProtonURI(uri)
		if err1 != nil {
			t.Fatalf("first parse of %q failed: %v", uri, err1)
		}

		// Reconstruct.
		var reconstructed string
		if p1 == "" {
			reconstructed = "proton://" + s1
		} else {
			reconstructed = "proton://" + s1 + "/" + p1
		}

		// Second parse.
		s2, p2, err2 := parseProtonURI(reconstructed)
		if err2 != nil {
			t.Fatalf("second parse of %q failed: %v", reconstructed, err2)
		}

		// Round-trip: both parses produce the same result.
		if s1 != s2 {
			t.Errorf("share mismatch: %q vs %q (uri=%q)", s1, s2, uri)
		}
		if p1 != p2 {
			t.Errorf("path mismatch: %q vs %q (uri=%q)", p1, p2, uri)
		}
	})
}

// TestParseProtonURI_TripleSlash_RoundTrip_Property verifies round-trip for
// proton:///path (empty share) URIs.
//
// **Validates: Requirements 2.3**
func TestParseProtonURI_TripleSlash_RoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate 1-3 path segments.
		numSegs := rapid.IntRange(1, 3).Draw(t, "numSegs")
		var segs []string
		for i := 0; i < numSegs; i++ {
			seg := rapid.StringMatching(`[a-zA-Z0-9_-]{1,15}`).Draw(t, "seg")
			segs = append(segs, seg)
		}

		uri := "proton:///" + strings.Join(segs, "/")

		s1, p1, err1 := parseProtonURI(uri)
		if err1 != nil {
			t.Fatalf("first parse of %q failed: %v", uri, err1)
		}
		if s1 != "" {
			t.Fatalf("expected empty share for triple-slash URI, got %q", s1)
		}

		// Reconstruct.
		reconstructed := "proton:///" + p1

		s2, p2, err2 := parseProtonURI(reconstructed)
		if err2 != nil {
			t.Fatalf("second parse of %q failed: %v", reconstructed, err2)
		}

		if s1 != s2 {
			t.Errorf("share mismatch: %q vs %q", s1, s2)
		}
		if p1 != p2 {
			t.Errorf("path mismatch: %q vs %q", p1, p2)
		}
	})
}

// TestClassifyPath_Property verifies that proton:// prefix → PathProton,
// everything else → PathLocal.
//
// **Validates: Requirements 2.2**
func TestClassifyPath_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate an arbitrary string that does NOT start with "proton://".
		local := rapid.StringMatching(`[^p].*|p[^r].*|pr[^o].*|pro[^t].*|prot[^o].*|proto[^n].*|proton[^:].*|proton:[^/].*|proton:/[^/].*`).Draw(t, "local")
		if strings.HasPrefix(local, "proton://") {
			// Safety net — skip if regex accidentally matches.
			return
		}
		if got := classifyPath(local); got != PathLocal {
			t.Errorf("classifyPath(%q) = PathProton, want PathLocal", local)
		}
	})
}

// TestClassifyPath_ProtonPrefix_Property verifies any string starting with
// "proton://" is classified as PathProton.
//
// **Validates: Requirements 2.2**
func TestClassifyPath_ProtonPrefix_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		suffix := rapid.String().Draw(t, "suffix")
		arg := "proton://" + suffix
		if got := classifyPath(arg); got != PathProton {
			t.Errorf("classifyPath(%q) = PathLocal, want PathProton", arg)
		}
	})
}

func TestParseProtonURI(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantShare string
		wantPath  string
		wantErr   string
	}{
		{"triple-slash with path", "proton:///Documents/file.txt", "", "Documents/file.txt", ""},
		{"named share with path", "proton://Photos/2024/vacation.jpg", "Photos", "2024/vacation.jpg", ""},
		{"named share no path", "proton://Drive", "Drive", "", ""},
		{"named share trailing slash", "proton://Drive/", "Drive", "", ""},
		{"triple-slash root", "proton:///", "", "", ""},
		{"bare proton://", "proton://", "", "", "no share specified"},
		{"not proton prefix", "/local/path", "", "", "invalid path"},
		{"share with id syntax", "proton://{abc123}/docs", "{abc123}", "docs", ""},
		{"path with dot-dot collapses", "proton://share/a/../b", "share", "b", ""},
		{"path all dots collapses to root", "proton:///test/..", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			share, path, err := parseProtonURI(tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if share != tt.wantShare {
				t.Errorf("share = %q, want %q", share, tt.wantShare)
			}
			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}
		})
	}
}
