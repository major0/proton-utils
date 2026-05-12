//go:build !windows

package lumoCmd

import (
	"os"
	"syscall"
)

// signalInterrupt returns the OS signals used for Ctrl+C cancellation.
func signalInterrupt() []os.Signal {
	return []os.Signal{syscall.SIGINT}
}
