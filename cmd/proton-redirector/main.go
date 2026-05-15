//go:build linux

// Command proton-redirector mounts the system-wide /proton redirector filesystem.
package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/major0/proton-utils/internal/daemon"
	"github.com/major0/proton-utils/internal/redirector"
	"github.com/major0/proton-utils/internal/sdnotify"
)

func main() {
	// Save NOTIFY_SOCKET before clearing the environment — systemd needs
	// it for the Type=notify readiness signal.
	notifySocket := os.Getenv("NOTIFY_SOCKET")
	redirector.ClearEnvironment()
	if notifySocket != "" {
		if err := os.Setenv("NOTIFY_SOCKET", notifySocket); err != nil {
			fmt.Fprintf(os.Stderr, "setenv NOTIFY_SOCKET: %v\n", err)
			os.Exit(1)
		}
	}

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: proton-redirector <mountpoint>\n")
		os.Exit(1)
	}
	mountpoint := os.Args[1]

	// Validate the mountpoint exists and is a directory with acceptable
	// permissions. The binary does NOT create or modify the directory —
	// that is the responsibility of `make install` or the distribution
	// package manager.
	mountInfo, err := os.Stat(mountpoint) //nolint:gosec // G703: mountpoint is validated below, not used for file I/O
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot stat %s: %v\n", mountpoint, err)
		fmt.Fprintf(os.Stderr, "hint: the mountpoint must be pre-created by 'make install' or your package manager\n")
		os.Exit(1)
	}
	if !mountInfo.IsDir() {
		fmt.Fprintf(os.Stderr, "error: %s is not a directory\n", mountpoint)
		os.Exit(1)
	}

	// Verify ownership: must be root-owned.
	if stat, ok := mountInfo.Sys().(*syscall.Stat_t); ok {
		if stat.Uid != 0 {
			fmt.Fprintf(os.Stderr, "warning: %s is owned by uid %d, expected root (0)\n", mountpoint, stat.Uid)
			os.Exit(1)
		}
	}

	// Verify permissions: must not be writable by group or other.
	perm := mountInfo.Mode().Perm()
	if perm&0022 != 0 {
		fmt.Fprintf(os.Stderr, "warning: %s has mode %04o, expected no group/other write (e.g. 0555)\n", mountpoint, perm)
		os.Exit(1)
	}

	server, err := redirector.Mount(mountpoint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mount failed: %v\n", err)
		os.Exit(1)
	}

	if err := sdnotify.Ready(); err != nil {
		fmt.Fprintf(os.Stderr, "sd_notify: %v\n", err)
	}

	daemon.WaitWithSignal(server)
}
