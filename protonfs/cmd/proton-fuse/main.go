//go:build linux

package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/major0/proton-cli/protonfs/internal/fusemount"
	"github.com/major0/proton-cli/protonfs/internal/sdnotify"
)

func main() {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		fmt.Fprintln(os.Stderr, "error: XDG_RUNTIME_DIR is not set")
		os.Exit(1)
	}

	mountpoint := filepath.Join(runtimeDir, "proton", "fs")

	cfg := fusemount.MountConfig{Mountpoint: mountpoint}
	registry := fusemount.NewRegistry()

	server, err := fusemount.Mount(cfg, registry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := sdnotify.Ready(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: sd_notify failed: %v\n", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		if err := server.Unmount(); err != nil {
			fmt.Fprintf(os.Stderr, "error unmounting: %v\n", err)
		}
	}()

	server.Wait()
}
