//go:build linux

package redirector

import (
	"os"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// NewRoot returns a new Root node for use as the FUSE root.
// The root's timestamps are set to the current time (process start),
// serving as an uptime indicator.
func NewRoot() *Root {
	return &Root{startTime: time.Now()}
}

// Mount creates and starts the redirector FUSE server at the given mountpoint.
// Uses DirectMount to call mount(2) directly rather than fusermount, since the
// binary is setuid root and has the necessary privilege.
func Mount(mountpoint string) (*fuse.Server, error) {
	root := NewRoot()
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
