//go:build linux

package redirector

import (
	"os"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// NewRoot returns a new RedirectorRoot node for use as the FUSE root.
// The info parameter provides timestamps from the mountpoint directory.
func NewRoot(info os.FileInfo) *RedirectorRoot {
	return &RedirectorRoot{mtime: info.ModTime()}
}

// Mount creates and starts the redirector FUSE server at the given mountpoint.
// Uses DirectMount to call mount(2) directly rather than fusermount, since the
// binary is setuid root and has the necessary privilege. The info parameter
// provides the mountpoint's timestamps for Getattr.
func Mount(mountpoint string, info os.FileInfo) (*fuse.Server, error) {
	root := NewRoot(info)
	server, err := fs.Mount(mountpoint, root, &fs.Options{
		MountOptions: fuse.MountOptions{
			AllowOther:  true,
			DirectMount: true,
			FsName:      "proton-redirector",
			Name:        "proton",
			Options:     []string{"ro"},
		},
	})
	return server, err
}

// ClearEnvironment removes all environment variables. This is a defense-in-depth
// measure for the setuid redirector binary.
func ClearEnvironment() {
	os.Clearenv()
}
