package account

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// refreshLock is a cross-process advisory lock keyed by account UID, used to
// serialize refreshes of the shared account token. The lock file lives under
// $XDG_RUNTIME_DIR/proton/ and contains no secrets — it is coordination state
// only (its filename carries the sanitized UID; its contents stay empty).
//
// When $XDG_RUNTIME_DIR is unset the lock degrades to a no-op (f == nil) so
// that single-process or restricted environments fall back to best-effort,
// uncoordinated refresh rather than failing.
type refreshLock struct{ f *os.File }

// lockDirPerm and lockFilePerm are the permissions for the runtime lock
// directory and lock file. The file holds no secrets but is owner-only by
// convention.
const (
	lockDirPerm  = 0o700
	lockFilePerm = 0o600
)

// acquireRefreshLock opens (creating if needed) the per-UID lock file and
// blocks until it holds an exclusive advisory lock on it. When
// $XDG_RUNTIME_DIR is unset it returns a no-op lock whose release is a no-op.
func acquireRefreshLock(uid string) (*refreshLock, error) {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		// Best-effort: no runtime dir means no coordination surface.
		return &refreshLock{}, nil
	}

	// The lock path is built from the OS-provided $XDG_RUNTIME_DIR and a
	// sanitizeUID'd component, so it is not attacker-tainted input.
	dir := filepath.Join(runtimeDir, "proton")
	if err := os.MkdirAll(dir, lockDirPerm); err != nil { //nolint:gosec // dir from $XDG_RUNTIME_DIR, not tainted input
		return nil, fmt.Errorf("account: create lock dir: %w", err)
	}

	path := filepath.Join(dir, "session-"+sanitizeUID(uid)+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, lockFilePerm) //nolint:gosec // path from $XDG_RUNTIME_DIR + sanitized UID
	if err != nil {
		return nil, fmt.Errorf("account: open lock file: %w", err)
	}

	if err := flockExclusive(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("account: acquire lock: %w", err)
	}

	return &refreshLock{f: f}, nil
}

// release unlocks and closes the lock file. It is idempotent: calling it on a
// no-op lock or an already-released lock does nothing.
func (l *refreshLock) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = flockUnlock(l.f)
	_ = l.f.Close()
	l.f = nil
}

// sanitizeUID reduces a UID to characters safe for use in a filename,
// replacing anything outside [A-Za-z0-9._-] with an underscore. An empty or
// fully-stripped UID maps to "default" so a valid filename always results.
func sanitizeUID(uid string) string {
	var b strings.Builder
	b.Grow(len(uid))
	for _, r := range uid {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}
