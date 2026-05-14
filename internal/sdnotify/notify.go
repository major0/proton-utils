//go:build linux

// Package sdnotify implements a minimal sd_notify(3) client for signaling
// service readiness to systemd.
package sdnotify

import (
	"net"
	"os"
)

// Ready sends READY=1 to the systemd notification socket.
// If NOTIFY_SOCKET is unset or empty, Ready is a no-op and returns nil.
func Ready() error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return nil
	}

	// Abstract socket: systemd uses '@' prefix, Go uses '\x00'.
	if addr[0] == '@' {
		addr = "\x00" + addr[1:]
	}

	conn, err := net.Dial("unixgram", addr) //nolint:gosec // G704: Unix domain socket — not a network SSRF vector
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	_, err = conn.Write([]byte("READY=1"))
	return err
}
