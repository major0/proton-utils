//go:build unix

package account

import (
	"os"
	"syscall"
)

// flockExclusive blocks until it holds an exclusive (LOCK_EX) advisory lock on
// the file. The lock is released when the file descriptor is closed or when
// flockUnlock is called.
func flockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) //nolint:gosec // G115: a file descriptor always fits in int
}

// flockUnlock releases the advisory lock held on the file (LOCK_UN).
func flockUnlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // G115: a file descriptor always fits in int
}
