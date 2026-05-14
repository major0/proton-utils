//go:build linux

// Package daemon provides signal handling utilities for long-running processes.
package daemon

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// Unmounter is the interface for a FUSE server that can be unmounted and waited on.
type Unmounter interface {
	Unmount() error
	Wait()
}

// WaitWithSignal blocks until the server's Wait() returns. On SIGTERM
// or SIGINT, calls Unmount() first. Unmount errors are written to stderr.
func WaitWithSignal(server Unmounter) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	done := make(chan struct{})
	go func() {
		select {
		case <-sigCh:
			if err := server.Unmount(); err != nil {
				fmt.Fprintf(os.Stderr, "unmount: %v\n", err)
			}
		case <-done:
		}
	}()

	server.Wait()
	close(done)
}
