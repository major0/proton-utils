//go:build linux

package sdnotify_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/major0/proton-cli/internal/sdnotify"
)

func TestReady_NoSocket(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")

	if err := sdnotify.Ready(); err != nil {
		t.Fatalf("Ready() with empty NOTIFY_SOCKET: %v", err)
	}
}

func TestReady_FilesystemSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "notify.sock")

	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn.Close()

	t.Setenv("NOTIFY_SOCKET", sock)

	if err := sdnotify.Ready(); err != nil {
		t.Fatalf("Ready(): %v", err)
	}

	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	got := string(buf[:n])
	if got != "READY=1" {
		t.Fatalf("got %q, want %q", got, "READY=1")
	}
}

func TestReady_AbstractSocket(t *testing.T) {
	// Abstract sockets use \x00 prefix in Go, @ prefix in NOTIFY_SOCKET.
	abstract := "\x00sdnotify-test-" + filepath.Base(t.TempDir())

	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: abstract, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn.Close()

	// Set NOTIFY_SOCKET with @ prefix (systemd convention).
	t.Setenv("NOTIFY_SOCKET", "@"+abstract[1:])

	if err := sdnotify.Ready(); err != nil {
		t.Fatalf("Ready(): %v", err)
	}

	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	got := string(buf[:n])
	if got != "READY=1" {
		t.Fatalf("got %q, want %q", got, "READY=1")
	}
}

func TestReady_UnsetSocket(t *testing.T) {
	os.Unsetenv("NOTIFY_SOCKET")

	if err := sdnotify.Ready(); err != nil {
		t.Fatalf("Ready() with unset NOTIFY_SOCKET: %v", err)
	}
}
