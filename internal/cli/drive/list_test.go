package driveCmd

import (
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
)

func TestFormatSize(t *testing.T) {
	tests := []struct {
		name  string
		size  int64
		human bool
		want  string
	}{
		{"raw zero", 0, false, "0"},
		{"raw 1024", 1024, false, "1024"},
		{"raw large", 1073741824, false, "1073741824"},
		{"human zero", 0, true, "0 B"},
		{"human 1kB", 1000, true, "1 kB"},
		{"human 1MB", 1000000, true, "1 MB"},
		{"human 1GB", 1000000000, true, "1 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := listOpts{human: tt.human}
			got := formatSize(tt.size, opts)
			if tt.human {
				// Human-readable uses docker/go-units which may vary slightly.
				// Just verify it's not raw digits.
				if tt.size > 0 && !strings.ContainsAny(got, "kMGTBb") {
					t.Errorf("formatSize(%d, human=true) = %q, expected human-readable", tt.size, got)
				}
			} else {
				if got != tt.want {
					t.Errorf("formatSize(%d, human=false) = %q, want %q", tt.size, got, tt.want)
				}
			}
		})
	}
}

func TestFormatTimestamp(t *testing.T) {
	// formatTimestamp takes a Unix epoch (seconds) and formats with time.Unix(epoch, 0).
	// Nanoseconds are always zero in the output.
	ref := time.Date(2024, 6, 15, 14, 30, 45, 0, time.Local)
	epoch := ref.Unix()

	tests := []struct {
		name  string
		epoch int64
		style timeStyle
		want  string
	}{
		{"full-iso", epoch, timeFull, ref.Format("2006-01-02 15:04:05.000000000 -0700")},
		{"long-iso", epoch, timeLongISO, ref.Format("2006-01-02 15:04")},
		{"iso", epoch, timeISO, ref.Format("01-02 15:04")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimestamp(tt.epoch, tt.style)
			if got != tt.want {
				t.Errorf("formatTimestamp(%d, %d) = %q, want %q", tt.epoch, tt.style, got, tt.want)
			}
		})
	}

	// Default style: recent vs old.
	t.Run("default recent", func(t *testing.T) {
		recent := time.Now().Add(-24 * time.Hour).Unix()
		got := formatTimestamp(recent, timeDefault)
		// Recent timestamps show "Mon DD HH:MM" format.
		if !strings.Contains(got, ":") {
			t.Errorf("recent timestamp should contain time: %q", got)
		}
	})

	t.Run("default old", func(t *testing.T) {
		old := time.Now().AddDate(-1, 0, 0).Unix()
		got := formatTimestamp(old, timeDefault)
		// Old timestamps show "Mon DD  YYYY" format.
		if strings.Count(got, ":") > 0 && !strings.Contains(got, "20") {
			// May contain year instead of time.
			t.Logf("old timestamp: %q", got)
		}
	})
}

func TestTypeChar(t *testing.T) {
	tests := []struct {
		name string
		lt   proton.LinkType
		want byte
	}{
		{"folder", proton.LinkTypeFolder, 'd'},
		{"file", proton.LinkTypeFile, '-'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := typeChar(tt.lt); got != tt.want {
				t.Errorf("typeChar(%d) = %c, want %c", tt.lt, got, tt.want)
			}
		})
	}
}

func TestResolveOpts(t *testing.T) {
	tests := []struct {
		name    string
		setup   func()
		check   func(t *testing.T, opts listOpts)
		wantErr string
	}{
		{
			name: "default values",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{color: "never"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if opts.sortBy != sortName {
					t.Errorf("sortBy = %d, want sortName", opts.sortBy)
				}
				if opts.color {
					t.Error("color should be false with 'never'")
				}
			},
		},
		{
			name: "long format",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{long: true, color: "never"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if opts.format != formatLong {
					t.Errorf("format = %d, want formatLong", opts.format)
				}
			},
		},
		{
			name: "sort by size",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{sortSize: true, color: "never"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if opts.sortBy != sortSize {
					t.Errorf("sortBy = %d, want sortSize", opts.sortBy)
				}
			},
		},
		{
			name: "sort by time",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{sortTime: true, color: "never"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if opts.sortBy != sortTime {
					t.Errorf("sortBy = %d, want sortTime", opts.sortBy)
				}
			},
		},
		{
			name: "unsorted",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{unsorted: true, color: "never"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if opts.sortBy != sortNone {
					t.Errorf("sortBy = %d, want sortNone", opts.sortBy)
				}
			},
		},
		{
			name: "invalid format",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{format: "bogus", color: "never"}
			},
			wantErr: "invalid --format value",
		},
		{
			name: "invalid sort word",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{sortWord: "bogus", color: "never"}
			},
			wantErr: "invalid --sort value",
		},
		{
			name: "invalid time-style",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{timeStyle: "bogus", color: "never"}
			},
			wantErr: "invalid --time-style value",
		},
		{
			name: "invalid color",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{color: "bogus"}
			},
			wantErr: "invalid --color value",
		},
		{
			name: "full-time implies long + timeFull",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{fullTime: true, color: "never"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if opts.format != formatLong {
					t.Errorf("format = %d, want formatLong", opts.format)
				}
				if opts.timeStyle != timeFull {
					t.Errorf("timeStyle = %d, want timeFull", opts.timeStyle)
				}
			},
		},
		{
			name: "format string long",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{format: "long", color: "never"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if opts.format != formatLong {
					t.Errorf("format = %d, want formatLong", opts.format)
				}
			},
		},
		{
			name: "format string verbose",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{format: "verbose", color: "never"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if opts.format != formatLong {
					t.Errorf("format = %d, want formatLong", opts.format)
				}
			},
		},
		{
			name: "sort word name",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{sortWord: "name", color: "never"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if opts.sortBy != sortName {
					t.Errorf("sortBy = %d, want sortName", opts.sortBy)
				}
			},
		},
		{
			name: "time-style full-iso",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{timeStyle: "full-iso", color: "never"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if opts.timeStyle != timeFull {
					t.Errorf("timeStyle = %d, want timeFull", opts.timeStyle)
				}
			},
		},
		{
			name: "time-style long-iso",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{timeStyle: "long-iso", color: "never"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if opts.timeStyle != timeLongISO {
					t.Errorf("timeStyle = %d, want timeLongISO", opts.timeStyle)
				}
			},
		},
		{
			name: "time-style iso",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{timeStyle: "iso", color: "never"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if opts.timeStyle != timeISO {
					t.Errorf("timeStyle = %d, want timeISO", opts.timeStyle)
				}
			},
		},
		{
			name: "color always",
			setup: func() {
				listFlags = struct {
					all, almostAll, long, single, across, columns bool
					human, recursive, reverse                     bool
					sortSize, sortTime, unsorted                  bool
					fullTime, trash, classify, inode              bool
					format, sortWord, timeStyle, color            string
				}{color: "always"}
			},
			check: func(t *testing.T, opts listOpts) {
				t.Helper()
				if !opts.color {
					t.Error("color should be true with 'always'")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			opts, err := resolveOpts()
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, opts)
			}
		})
	}
}

func TestSortEntries(t *testing.T) {
	// Create test entries with mock links.
	makeEntry := func(name string, size int64, mtime int64, lt proton.LinkType) listEntry {
		pl := &proton.Link{
			LinkID:     name + "-id",
			Type:       lt,
			ModifyTime: mtime,
			State:      proton.LinkStateActive,
		}
		if lt == proton.LinkTypeFile {
			pl.FileProperties = &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					Size:       size,
					CreateTime: mtime,
				},
			}
		}
		l := drive.NewTestLink(pl, nil, nil, nil, name)
		return listEntry{entry: drive.DirEntry{Link: l}, name: name}
	}

	t.Run("sort by name", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("charlie", 100, 1000, proton.LinkTypeFile),
			makeEntry("alpha", 200, 2000, proton.LinkTypeFile),
			makeEntry("bravo", 150, 1500, proton.LinkTypeFile),
		}
		sortEntries(entries, listOpts{sortBy: sortName})
		if entries[0].name != "alpha" || entries[1].name != "bravo" || entries[2].name != "charlie" {
			t.Errorf("sort by name failed: %v", []string{entries[0].name, entries[1].name, entries[2].name})
		}
	})

	t.Run("sort by size descending", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("small", 100, 1000, proton.LinkTypeFile),
			makeEntry("large", 300, 2000, proton.LinkTypeFile),
			makeEntry("medium", 200, 1500, proton.LinkTypeFile),
		}
		sortEntries(entries, listOpts{sortBy: sortSize})
		if entries[0].name != "large" || entries[1].name != "medium" || entries[2].name != "small" {
			t.Errorf("sort by size failed: %v", []string{entries[0].name, entries[1].name, entries[2].name})
		}
	})

	t.Run("sort by time descending", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("old", 100, 1000, proton.LinkTypeFile),
			makeEntry("new", 100, 3000, proton.LinkTypeFile),
			makeEntry("mid", 100, 2000, proton.LinkTypeFile),
		}
		sortEntries(entries, listOpts{sortBy: sortTime})
		if entries[0].name != "new" || entries[1].name != "mid" || entries[2].name != "old" {
			t.Errorf("sort by time failed: %v", []string{entries[0].name, entries[1].name, entries[2].name})
		}
	})

	t.Run("reverse sort", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("alpha", 100, 1000, proton.LinkTypeFile),
			makeEntry("bravo", 200, 2000, proton.LinkTypeFile),
			makeEntry("charlie", 300, 3000, proton.LinkTypeFile),
		}
		sortEntries(entries, listOpts{sortBy: sortName, reverse: true})
		if entries[0].name != "charlie" || entries[2].name != "alpha" {
			t.Errorf("reverse sort failed: %v", []string{entries[0].name, entries[1].name, entries[2].name})
		}
	})

	t.Run("sortNone preserves order", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("bravo", 100, 1000, proton.LinkTypeFile),
			makeEntry("alpha", 200, 2000, proton.LinkTypeFile),
			makeEntry("charlie", 300, 3000, proton.LinkTypeFile),
		}
		sortEntries(entries, listOpts{sortBy: sortNone})
		if entries[0].name != "bravo" || entries[1].name != "alpha" || entries[2].name != "charlie" {
			t.Errorf("sortNone should preserve order: %v", []string{entries[0].name, entries[1].name, entries[2].name})
		}
	})

	t.Run("sortNone reverse", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("first", 100, 1000, proton.LinkTypeFile),
			makeEntry("second", 200, 2000, proton.LinkTypeFile),
			makeEntry("third", 300, 3000, proton.LinkTypeFile),
		}
		sortEntries(entries, listOpts{sortBy: sortNone, reverse: true})
		if entries[0].name != "third" || entries[2].name != "first" {
			t.Errorf("sortNone reverse failed: %v", []string{entries[0].name, entries[1].name, entries[2].name})
		}
	})
}

func TestFilterEntries(t *testing.T) {
	makeEntry := func(name string, state proton.LinkState) listEntry {
		pl := &proton.Link{
			LinkID: name + "-id",
			Type:   proton.LinkTypeFile,
			State:  state,
		}
		l := drive.NewTestLink(pl, nil, nil, nil, name)
		return listEntry{entry: drive.DirEntry{Link: l}, name: name}
	}

	t.Run("default hides dot-files and trashed", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("visible.txt", proton.LinkStateActive),
			makeEntry(".hidden", proton.LinkStateActive),
			makeEntry("trashed.txt", proton.LinkStateTrashed),
			makeEntry("deleted.txt", proton.LinkStateDeleted),
		}
		got := filterEntries(entries, listOpts{})
		if len(got) != 1 || got[0].name != "visible.txt" {
			names := make([]string, len(got))
			for i, e := range got {
				names[i] = e.name
			}
			t.Errorf("default filter: got %v, want [visible.txt]", names)
		}
	})

	t.Run("-a shows dot-files and trashed", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("visible.txt", proton.LinkStateActive),
			makeEntry(".hidden", proton.LinkStateActive),
			makeEntry("trashed.txt", proton.LinkStateTrashed),
			makeEntry("deleted.txt", proton.LinkStateDeleted),
		}
		got := filterEntries(entries, listOpts{all: true})
		// Should include visible, .hidden, trashed — but not deleted.
		if len(got) != 3 {
			names := make([]string, len(got))
			for i, e := range got {
				names[i] = e.name
			}
			t.Errorf("-a filter: got %v, want 3 entries", names)
		}
	})

	t.Run("-A shows dot-files and trashed", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("visible.txt", proton.LinkStateActive),
			makeEntry(".hidden", proton.LinkStateActive),
			makeEntry("trashed.txt", proton.LinkStateTrashed),
			makeEntry("deleted.txt", proton.LinkStateDeleted),
		}
		got := filterEntries(entries, listOpts{almostAll: true})
		// Should include visible, .hidden, trashed — but not deleted.
		if len(got) != 3 {
			names := make([]string, len(got))
			for i, e := range got {
				names[i] = e.name
			}
			t.Errorf("-A filter: got %v, want 3 entries", names)
		}
	})

	t.Run("--trash shows only trashed", func(t *testing.T) {
		entries := []listEntry{
			makeEntry("active.txt", proton.LinkStateActive),
			makeEntry("trashed.txt", proton.LinkStateTrashed),
			makeEntry("deleted.txt", proton.LinkStateDeleted),
		}
		got := filterEntries(entries, listOpts{trash: true})
		if len(got) != 1 || got[0].name != "trashed.txt" {
			names := make([]string, len(got))
			for i, e := range got {
				names[i] = e.name
			}
			t.Errorf("--trash filter: got %v, want [trashed.txt]", names)
		}
	})
}

func TestDfVolState(t *testing.T) {
	tests := []struct {
		name  string
		state proton.VolumeState
		want  string
	}{
		{"active", proton.VolumeStateActive, "active"},
		{"locked", proton.VolumeStateLocked, "locked"},
		{"unknown", proton.VolumeState(99), "unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dfVolState(tt.state)
			if got != tt.want {
				t.Errorf("dfVolState(%d) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

// TestFilterEntries_DuplicateNames_WithAlmostAll verifies that when -A is
// set, filterEntries retains both an active and a trashed entry with the
// same name. This is the regression test for the bug where `ls -A <file>`
// only showed one of the two entries because Lookup stopped on the first
// match. The fix is in resolveEntries which now scans the parent directory
// for all name matches when -a/-A is set.
func TestFilterEntries_DuplicateNames_WithAlmostAll(t *testing.T) {
	makeEntry := func(name, id string, state proton.LinkState) listEntry {
		pl := &proton.Link{
			LinkID: id,
			Type:   proton.LinkTypeFile,
			State:  state,
		}
		l := drive.NewTestLink(pl, nil, nil, nil, name)
		return listEntry{entry: drive.DirEntry{Link: l}, name: name}
	}

	entries := []listEntry{
		makeEntry(".acidriprc", "active-id", proton.LinkStateActive),
		makeEntry(".acidriprc", "trashed-id", proton.LinkStateTrashed),
	}

	// With -A: both should pass through.
	got := filterEntries(entries, listOpts{almostAll: true})
	if len(got) != 2 {
		names := make([]string, len(got))
		for i, e := range got {
			names[i] = e.entry.Link.LinkID()
		}
		t.Fatalf("-A filter: got %d entries %v, want 2 (active + trashed)", len(got), names)
	}

	// Without -a/-A: only the active one should pass (trashed is hidden,
	// and dot-files are hidden).
	got = filterEntries(entries, listOpts{})
	if len(got) != 0 {
		t.Fatalf("default filter: got %d entries, want 0 (dot-file hidden)", len(got))
	}

	// With --trash: only the trashed one.
	got = filterEntries(entries, listOpts{trash: true})
	if len(got) != 1 || got[0].entry.Link.LinkID() != "trashed-id" {
		t.Fatalf("--trash filter: got %d entries, want 1 (trashed only)", len(got))
	}
}
