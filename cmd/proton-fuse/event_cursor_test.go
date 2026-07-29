//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// TestPropertyEventCursorRoundTrip validates Property 4: persisting a
// per-volume cursor map and loading it back yields an equal map, and the file
// contains only the volume IDs and event IDs.
//
// Validates: Requirements 5.1, 5.2, 5.4
func TestPropertyEventCursorRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Volume/event IDs are URL-safe identifiers (no '=' or newlines).
		idGen := rapid.StringMatching(`[A-Za-z0-9_-]{1,24}`)
		cursors := rapid.MapOfN(idGen, idGen, 0, 8).Draw(rt, "cursors")

		path := filepath.Join(t.TempDir(), "event_cursor")
		if err := saveEventCursors(path, cursors); err != nil {
			rt.Fatalf("save: %v", err)
		}

		loaded, err := loadEventCursors(path)
		if err != nil {
			rt.Fatalf("load: %v", err)
		}

		if len(loaded) != len(cursors) {
			rt.Fatalf("loaded %d entries, want %d", len(loaded), len(cursors))
		}
		for vol, event := range cursors {
			if loaded[vol] != event {
				rt.Fatalf("cursor[%q] = %q, want %q", vol, loaded[vol], event)
			}
		}

		// The file must contain only the IDs (as volumeID=eventID lines).
		data, err := os.ReadFile(path) //nolint:gosec // test-controlled path
		if err != nil {
			rt.Fatalf("read file: %v", err)
		}
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if line == "" {
				continue
			}
			vol, event, found := strings.Cut(line, "=")
			if !found {
				rt.Fatalf("line %q is not volumeID=eventID", line)
			}
			if cursors[vol] != event {
				rt.Fatalf("file line %q does not match input", line)
			}
		}
	})
}

// TestLoadEventCursorsMissingFile verifies a cold start (no file) yields an
// empty map without error.
func TestLoadEventCursorsMissingFile(t *testing.T) {
	cursors, err := loadEventCursors(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cursors) != 0 {
		t.Fatalf("expected empty map, got %d entries", len(cursors))
	}
}

// TestSaveEventCursorsFileMode verifies the cursor file is written 0600.
func TestSaveEventCursorsFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event_cursor")
	if err := saveEventCursors(path, map[string]string{"vol1": "e1"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != eventCursorFileMode {
		t.Fatalf("file mode = %o, want %o", perm, eventCursorFileMode)
	}
}
