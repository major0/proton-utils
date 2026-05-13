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
	redirector.ClearEnvironment()

	if len(os.Args) < 2 || os.Args[1] != "/proton" {
		fmt.Fprintf(os.Stderr, "usage: proton-redirector /proton\n")
		os.Exit(1)
	}

	server, err := redirector.Mount("/proton")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mount failed: %v\n", err)
		os.Exit(1)
	}

	if err := sdnotify.Ready(); err != nil {
		fmt.Fprintf(os.Stderr, "sd_notify: %v\n", err)
	}

	daemon.WaitWithSignal(server)
}
