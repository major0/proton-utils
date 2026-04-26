package client

import (
	"syscall"

	"github.com/major0/proton-cli/api/drive"
)

// FileExistsError indicates an active file with the same name blocks the
// operation. Carries the blocking Link for callers to act on (e.g.
// create a new revision). Maps to syscall.EEXIST for FUSE compatibility.
type FileExistsError struct {
	Link *drive.Link
}

func (e *FileExistsError) Error() string { return "file exists: " + e.Link.LinkID() }

// Is reports whether target matches this error type's errno.
func (e *FileExistsError) Is(target error) bool {
	return target == syscall.EEXIST
}

// DirExistsError indicates a directory with the same name blocks the
// operation. Maps to syscall.EISDIR for FUSE compatibility.
type DirExistsError struct {
	Link *drive.Link
}

func (e *DirExistsError) Error() string { return "directory exists: " + e.Link.LinkID() }

// Is reports whether target matches this error type's errno.
func (e *DirExistsError) Is(target error) bool {
	return target == syscall.EISDIR
}

// DraftExistsError indicates a draft revision blocks the operation.
// The Link may be a draft-only link (no active revision) or an active
// file with a stale draft. Maps to syscall.EEXIST for FUSE compatibility.
type DraftExistsError struct {
	Link *drive.Link
}

func (e *DraftExistsError) Error() string { return "draft exists: " + e.Link.LinkID() }

// Is reports whether target matches this error type's errno.
func (e *DraftExistsError) Is(target error) bool {
	return target == syscall.EEXIST
}
