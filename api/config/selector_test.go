package config

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// TestSelectorRoundTrip_Property verifies that for any valid Selector,
// printing it to a string and parsing it back produces an equivalent Selector.
//
// **Property 1: Selector round-trip**
// **Validates: Requirements 1.5**
func TestSelectorRoundTrip_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		seg := genSelector(t)
		str := seg.String()
		parsed, err := Parse(str)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", str, err)
		}
		if len(parsed.Segments) != len(seg.Segments) {
			t.Fatalf("segment count mismatch: got %d, want %d\ninput: %q", len(parsed.Segments), len(seg.Segments), str)
		}
		for i, got := range parsed.Segments {
			want := seg.Segments[i]
			if got.Name != want.Name {
				t.Fatalf("segment[%d].Name: got %q, want %q\ninput: %q", i, got.Name, want.Name, str)
			}
			if got.IndexKey != want.IndexKey {
				t.Fatalf("segment[%d].IndexKey: got %q, want %q\ninput: %q", i, got.IndexKey, want.IndexKey, str)
			}
			if got.IndexVal != want.IndexVal {
				t.Fatalf("segment[%d].IndexVal: got %q, want %q\ninput: %q", i, got.IndexVal, want.IndexVal, str)
			}
		}
	})
}

// genSelector generates a random valid Selector with 1–4 segments.
func genSelector(t *rapid.T) Selector {
	count := rapid.IntRange(1, 4).Draw(t, "segmentCount")
	segments := make([]Segment, count)
	for i := range segments {
		segments[i] = genSegment(t)
	}
	return Selector{Segments: segments}
}

// genSegment generates a random valid Segment with an optional map index.
func genSegment(t *rapid.T) Segment {
	name := rapid.StringMatching("[a-zA-Z_][a-zA-Z0-9_-]{0,10}").Draw(t, "name")
	hasIndex := rapid.Bool().Draw(t, "hasIndex")
	if !hasIndex {
		return Segment{Name: name}
	}
	key := rapid.StringMatching("[a-zA-Z_][a-zA-Z0-9_-]{0,10}").Draw(t, "indexKey")
	val := rapid.StringMatching("[^\\]]{1,20}").Draw(t, "indexVal")
	return Segment{Name: name, IndexKey: key, IndexVal: val}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // substring of error message
	}{
		{"empty", "", "empty input"},
		{"trailing dot", "core.", "empty identifier"},
		{"leading dot", ".core", "empty identifier"},
		{"double dot", "core..max_jobs", "empty identifier"},
		{"unmatched bracket", "share[name=foo", "unmatched"},
		{"missing equals", "share[name]", "missing '='"},
		{"empty value", "share[name=]", "empty value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.input)
			if err == nil {
				t.Fatalf("Parse(%q) returned nil error, want error containing %q", tt.input, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Parse(%q) error = %q, want substring %q", tt.input, err.Error(), tt.want)
			}
		})
	}
}

func TestParse_Valid(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Selector
	}{
		{
			name:  "simple two segments",
			input: "core.max_jobs",
			want:  Selector{Segments: []Segment{{Name: "core"}, {Name: "max_jobs"}}},
		},
		{
			name:  "indexed segment with trailing",
			input: "share[name=MyShare].memory_cache",
			want: Selector{Segments: []Segment{
				{Name: "share", IndexKey: "name", IndexVal: "MyShare"},
				{Name: "memory_cache"},
			}},
		},
		{
			name:  "subsystem app version",
			input: "drive.app_version",
			want:  Selector{Segments: []Segment{{Name: "drive"}, {Name: "app_version"}}},
		},
		{
			name:  "single segment",
			input: "core",
			want:  Selector{Segments: []Segment{{Name: "core"}}},
		},
		{
			name:  "indexed by id",
			input: "share[id=abc123].disk_cache",
			want: Selector{Segments: []Segment{
				{Name: "share", IndexKey: "id", IndexVal: "abc123"},
				{Name: "disk_cache"},
			}},
		},
		{
			name:  "hyphenated identifier",
			input: "my-ns.some-option",
			want:  Selector{Segments: []Segment{{Name: "my-ns"}, {Name: "some-option"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.input, err)
			}
			if len(got.Segments) != len(tt.want.Segments) {
				t.Fatalf("Parse(%q) segment count = %d, want %d", tt.input, len(got.Segments), len(tt.want.Segments))
			}
			for i, g := range got.Segments {
				w := tt.want.Segments[i]
				if g.Name != w.Name || g.IndexKey != w.IndexKey || g.IndexVal != w.IndexVal {
					t.Fatalf("Parse(%q) segment[%d] = %+v, want %+v", tt.input, i, g, w)
				}
			}
		})
	}
}

func TestSelector_String(t *testing.T) {
	tests := []struct {
		name string
		sel  Selector
		want string
	}{
		{
			name: "plain segments",
			sel:  Selector{Segments: []Segment{{Name: "core"}, {Name: "max_jobs"}}},
			want: "core.max_jobs",
		},
		{
			name: "indexed segment",
			sel: Selector{Segments: []Segment{
				{Name: "share", IndexKey: "name", IndexVal: "MyShare"},
				{Name: "memory_cache"},
			}},
			want: "share[name=MyShare].memory_cache",
		},
		{
			name: "single segment",
			sel:  Selector{Segments: []Segment{{Name: "core"}}},
			want: "core",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.sel.String()
			if got != tt.want {
				t.Fatalf("Selector.String() = %q, want %q", got, tt.want)
			}
		})
	}
}
