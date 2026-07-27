//go:build !unix

package account

import "os"

// flockExclusive is a no-op on platforms without flock. Such platforms are
// single-process for our purposes and degrade to best-effort, uncoordinated
// refresh.
func flockExclusive(_ *os.File) error { return nil }

// flockUnlock is a no-op on platforms without flock.
func flockUnlock(_ *os.File) error { return nil }
