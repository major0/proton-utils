//go:build linux

package driveCmd

import (
	"os"
	"syscall"
	"unsafe"
)

// openDirect opens a file with O_DIRECT to bypass the page cache.
// Returns the file and true if direct I/O is active, or falls back
// to a normal open (and false) if the filesystem doesn't support it.
func openDirect(path string, flag int, perm os.FileMode) (*os.File, bool, error) {
	f, err := os.OpenFile(path, flag|syscall.O_DIRECT, perm) //nolint:gosec // caller-controlled path
	if err == nil {
		return f, true, nil
	}
	// EINVAL: filesystem doesn't support O_DIRECT (tmpfs, some FUSE).
	// Fall back to normal open.
	f, err = os.OpenFile(path, flag, perm) //nolint:gosec // caller-controlled path
	if err != nil {
		return nil, false, err
	}
	return f, false, nil
}

// alignedAlloc returns a byte slice of the given size whose starting
// address is aligned to 4096 bytes (required for O_DIRECT buffers).
func alignedAlloc(size int) []byte {
	const align = 4096
	// Allocate extra bytes so we can find an aligned offset within.
	raw := make([]byte, size+align)
	addr := uintptr(unsafe.Pointer(&raw[0])) //nolint:gosec // aligned alloc requires pointer arithmetic
	offset := (align - int(addr%uintptr(align))) % align
	return raw[offset : offset+size]
}
