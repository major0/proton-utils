//go:build linux

// Command proton-redirector mounts the system-wide /proton redirector filesystem.
package main

import (
	"fmt"
	"os"

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
		os.Setenv("NOTIFY_SOCKET", notifySocket)
	}

	if len(os.Args) < 2 || os.Args[1] != "/proton" {
		fmt.Fprintf(os.Stderr, "usage: proton-redirector /proton\n")
		os.Exit(1)
	}

	// Create the mountpoint if it doesn't exist. The binary is setuid
	// root so it has permission to create directories at /.
	// Ensure root:root 0755 regardless of whether we created it.
	if err := os.MkdirAll("/proton", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir /proton: %v\n", err)
		os.Exit(1)
	}
	if err := os.Chown("/proton", 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "chown /proton: %v\n", err)
		os.Exit(1)
	}
	if err := os.Chmod("/proton", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "chmod /proton: %v\n", err)
		os.Exit(1)
	}

	// Stat the mountpoint before mounting to capture its timestamps.
	// These are used by Getattr so the FUSE root reports meaningful times
	// instead of epoch zero.
	mountInfo, err := os.Stat("/proton")
	if err != nil {
		fmt.Fprintf(os.Stderr, "stat /proton: %v\n", err)
		os.Exit(1)
	}

	server, err := redirector.Mount("/proton", mountInfo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mount failed: %v\n", err)
		os.Exit(1)
	}

	if err := sdnotify.Ready(); err != nil {
		fmt.Fprintf(os.Stderr, "sd_notify: %v\n", err)
	}

	daemon.WaitWithSignal(server)
}
