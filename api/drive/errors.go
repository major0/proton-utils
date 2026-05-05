// Package drive provides Proton Drive resource types and interfaces.
package drive

import "errors"

var (
	// ErrFileNotFound indicates that the requested file or link was not found.
	ErrFileNotFound = errors.New("file not found")
	// ErrNotAFolder indicates that the target link is not a folder.
	ErrNotAFolder = errors.New("not a folder")
	// ErrNotEmpty indicates that the directory is not empty.
	ErrNotEmpty = errors.New("directory not empty")
	// ErrInvalidPath indicates that the provided path is malformed.
	ErrInvalidPath = errors.New("invalid path")
	// ErrShareURLExists indicates that a ShareURL already exists for the share.
	ErrShareURLExists = errors.New("drive: share URL already exists")
	// ErrNoShareURL indicates that no ShareURL exists for the share.
	ErrNoShareURL = errors.New("drive: no share URL exists")
	// ErrNotStandardShare indicates that the operation requires a standard share.
	ErrNotStandardShare = errors.New("drive: operation requires a standard share")
)
