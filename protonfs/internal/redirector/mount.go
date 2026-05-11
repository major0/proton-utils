//go:build linux

package redirector

import (
	"os"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// NewRoot returns a new RedirectorRoot node for use as the FUSE root.
func NewRoot() *RedirectorRoot {
	return &RedirectorRoot{}
}

// Mount creates and starts the redirector FUSE server at the given mountpoint.
func Mount(mountpoint string) (*fuse.Server, error) {
	root := NewRoot()
	server, err := fs.Mount(mountpoint, root, &fs.Options{
		MountOptions: fuse.MountOptions{
			AllowOther: true,
			FsName:     "proton-redirector",
			Name:       "proton",
			Options:    []string{"ro"},
		},
	})
	return server, err
}

// ClearEnvironment removes all environment variables. This is a defense-in-depth
// measure for the setuid redirector binary.
func ClearEnvironment() {
	os.Clearenv()
}
