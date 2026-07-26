package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// eventCursorFileMode is the permission for the persisted cursor file. It
// contains only cleartext identifiers (volume IDs and event IDs), never
// decrypted content, but is still owner-only.
const eventCursorFileMode = 0o600

// eventCursorPath returns the path to the per-volume event cursor file and
// whether a location is available. It lives alongside the drive object cache
// at $XDG_RUNTIME_DIR/proton/drive/event_cursor. When $XDG_RUNTIME_DIR is
// unset, cursor persistence is disabled (ok=false) — the daemon falls back to
// starting each volume from the latest event ID.
func eventCursorPath() (path string, ok bool) {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return "", false
	}
	return filepath.Join(runtimeDir, "proton", "drive", "event_cursor"), true
}

// loadEventCursors reads the per-volume cursor map from path. A missing file
// yields an empty map and no error (cold start). Each line is
// "volumeID=eventID"; blank lines and malformed lines are skipped.
func loadEventCursors(path string) (map[string]string, error) {
	cursors := make(map[string]string)

	f, err := os.Open(path) //nolint:gosec // path is daemon-controlled, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return cursors, nil
		}
		return nil, fmt.Errorf("load event cursors: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		vol, event, found := strings.Cut(line, "=")
		if !found || vol == "" || event == "" {
			continue
		}
		cursors[vol] = event
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("load event cursors: %w", err)
	}
	return cursors, nil
}

// saveEventCursors writes the per-volume cursor map to path atomically
// (write to a temp file, then rename), creating the parent directory as
// needed. Lines are sorted by volume ID for deterministic output. The file
// contains only volume IDs and event IDs — no decrypted content.
func saveEventCursors(path string, cursors map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("save event cursors: mkdir: %w", err)
	}

	vols := make([]string, 0, len(cursors))
	for vol := range cursors {
		vols = append(vols, vol)
	}
	sort.Strings(vols)

	var b strings.Builder
	for _, vol := range vols {
		fmt.Fprintf(&b, "%s=%s\n", vol, cursors[vol])
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), eventCursorFileMode); err != nil {
		return fmt.Errorf("save event cursors: write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("save event cursors: rename: %w", err)
	}
	return nil
}
