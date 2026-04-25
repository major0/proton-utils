//go:build darwin

package driveCmd

import (
	"os"
	"syscall"
	"unsafe"
)

// openDirect opens a file and sets F_NOCACHE to bypass the unified
// buffer cache on macOS. Returns the file and true if nocache is
// active, or falls back to normal open (and false) on error.
func openDirect(path string, flag int, perm os.FileMode) (*os.File, bool, error) {
	f, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, false, err
	}
	// F_NOCACHE = 48 on darwin; tells the kernel not to cache this FD's I/O.
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), 48, 1)
	if errno != 0 {
		// F_NOCACHE failed — still usable, just cached.
		return f, false, nil
	}
	return f, true, nil
}

// alignedAlloc returns a byte slice of the given size whose starting
// address is aligned to 4096 bytes. macOS F_NOCACHE doesn't strictly
// require alignment, but aligned buffers avoid extra copies in the
// kernel's I/O path.
func alignedAlloc(size int) []byte {
	const align = 4096
	raw := make([]byte, size+align)
	addr := uintptr(unsafe.Pointer(&raw[0]))
	offset := (align - int(addr%uintptr(align))) % align
	return raw[offset : offset+size]
}
