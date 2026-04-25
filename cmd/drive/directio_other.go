//go:build !linux && !darwin

package driveCmd

import "os"

// openDirect is a no-op fallback for platforms without direct I/O
// support (Windows, BSDs, etc.). Always returns direct=false.
func openDirect(path string, flag int, perm os.FileMode) (*os.File, bool, error) {
	f, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, false, err
	}
	return f, false, nil
}

// alignedAlloc returns a normal byte slice on platforms without
// direct I/O alignment requirements.
func alignedAlloc(size int) []byte {
	return make([]byte, size)
}
